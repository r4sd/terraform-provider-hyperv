package hyperv_wsman

import (
	"context"
	"fmt"
	"math"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// GetVmProcessors は VM の CPU 設定を go-wsman 経由で取得する。
//
// PS 版 (Get-VMProcessor) をシャドウイングし、Read の無条件 PowerShell 実行を解消する。
func (c *ClientConfig) GetVmProcessors(ctx context.Context, vmName string) ([]api.VmProcessor, error) {
	guid, err := c.resolveVMGUID(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: GetVmProcessors %q: %w", vmName, err)
	}
	p, err := c.WsmanClient.GetProcessorSettings(ctx, guid)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: GetVmProcessors %q: %w", vmName, err)
	}
	return []api.VmProcessor{processorFromSettingData(vmName, p)}, nil
}

// processorFromSettingData は go-wsman の Msvm_ProcessorSettingData を provider の
// api.VmProcessor に変換する純関数。
//
// 単位変換 (CIM → PowerShell/provider 表現):
//   - CIM AllocationUnits = "percent / 1000" のため Limit/Reservation は 0..100000。
//     PS Maximum/Reserve は 0..100 (%) = CIM 値 / 1000。整数除算のため 1000 の倍数でない値
//     (生 WMI 書き込み等) は切り捨てられるが、PS/Terraform 管理下の値は常に 1000 の倍数 (%整数)
//     なので実運用で PS と乖離しない。
//   - Weight (0..10000) は PS RelativeWeight と 1:1 (変換なし)。
//   - LimitProcessorFeatures → CompatibilityForMigrationEnabled。
//   - LimitCPUID → CompatibilityForOlderOperatingSystemsEnabled。
//
// 符号付き整数への縮小変換は clampInt32/clampInt64 で上限を守る (実値は上記範囲に収まるため
// 実害はないが、gosec G115 の縮小変換検出を避けるための防御)。
func processorFromSettingData(vmName string, p *hyperv.Msvm_ProcessorSettingData) api.VmProcessor {
	return api.VmProcessor{
		VmName:                           vmName,
		CompatibilityForMigrationEnabled: p.LimitProcessorFeatures,
		CompatibilityForOlderOperatingSystemsEnabled: p.LimitCPUID,
		HwThreadCountPerCore:                         clampInt64(p.HwThreadsPerCore),
		Maximum:                                      clampInt64(p.Limit / 1000),
		Reserve:                                      clampInt64(p.Reservation / 1000),
		RelativeWeight:                               clampInt32(uint64(p.Weight)),
		MaximumCountPerNumaNode:                      clampInt32(p.MaxProcessorsPerNumaNode),
		MaximumCountPerNumaSocket:                    clampInt32(p.MaxNumaNodesPerSocket),
		EnableHostResourceProtection:                 p.EnableHostResourceProtection,
		ExposeVirtualizationExtensions:               p.ExposeVirtualizationExtensions,
	}
}

// CreateOrUpdateVmProcessors は VM の CPU 設定を go-wsman 経由で書き込む。
//
// PS 版 (Set-VMProcessor) をシャドウイングし、create/update の無条件 PowerShell 実行を解消する。
// go-wsman の GetProcessorSettings で現行設定 (InstanceID 込み) を取得し、要求値を単位変換して
// 反映し、SetProcessorSettings (ModifyResourceSettings) で書き戻す。
//
// 3 分岐で「無用な書き込みの回避」と「go-wsman で表現できない変更の正しい委譲」を両立する:
//
//  1. NUMA 正規化: maximum_count_per_numa_node/socket は config 既定 0 = "Dynamic" で、CIM は
//     ホスト解決済みの実値 (例: 論理プロセッサ数) を返す。要求 0 を現行値と等価に正規化する
//     (resource の DiffSuppress・PS テンプレの「0→ホスト値」と同セマンティクス)。これをしないと
//     既定 config でも毎回差分ありになり、strict PS-0 が成立しない。
//  2. 差分なしガード: 正規化後に現行==要求なら Set をスキップ (書き込み 0 件)。homelab の既定値
//     運用 (Maximum=100/Reserve=0/Weight=100/NUMA=Dynamic) はここで完結し strict モード (PS-0)。
//  3. ゼロ/false ダウングレードの PS 委譲: 非ゼロ→0 や true→false の変更は、go-wsman の
//     marshalEmbeddedInstance がゼロ値フィールドを送らない仕様のため CIM に反映されず「成功報告
//     なのに変わらない=恒常 diff」になる。この遷移だけは正しく処理できる PS 実装へフォールバック
//     する (strict モードではこの経路が PS 呼び出しとして検知される=未達の明示)。go-wsman 側で
//     ゼロ値明示送信を実装したらこの分岐を外せる (v2.1)。
func (c *ClientConfig) CreateOrUpdateVmProcessors(ctx context.Context, vmName string, vmProcessors []api.VmProcessor) error {
	if len(vmProcessors) == 0 {
		return nil
	}
	if len(vmProcessors) > 1 {
		return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmProcessors %q: only 1 vm processor setting allowed per a vm", vmName)
	}

	guid, err := c.resolveVMGUID(ctx, vmName)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmProcessors %q: %w", vmName, err)
	}
	current, err := c.WsmanClient.GetProcessorSettings(ctx, guid)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmProcessors %q: get: %w", vmName, err)
	}
	currentApi := processorFromSettingData(vmName, current)

	// 1. NUMA 0=Dynamic の正規化 (現行の解決済み値と等価扱い)。
	want := vmProcessors[0]
	norm := want
	if norm.MaximumCountPerNumaNode == 0 {
		norm.MaximumCountPerNumaNode = currentApi.MaximumCountPerNumaNode
	}
	if norm.MaximumCountPerNumaSocket == 0 {
		norm.MaximumCountPerNumaSocket = currentApi.MaximumCountPerNumaSocket
	}

	// 2. 差分なしガード。
	if processorSettingsEqual(currentApi, norm) {
		return nil
	}

	// 3. ゼロ/false ダウングレードは PS へ委譲 (go-wsman では表現不可)。元の要求値をそのまま渡す。
	if hasZeroDowngrade(currentApi, norm) {
		return c.ClientConfig.CreateOrUpdateVmProcessors(ctx, vmName, vmProcessors)
	}

	applyProcessorSettings(current, norm)
	jobRef, err := c.WsmanClient.SetProcessorSettings(ctx, current)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmProcessors %q: set: %w", vmName, err)
	}
	if err := c.WsmanClient.WaitForJob(ctx, jobRef); err != nil {
		return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmProcessors %q: wait: %w", vmName, err)
	}
	return nil
}

// hasZeroDowngrade は want が current に対して「非ゼロ→0」または「true→false」の遷移を含むかを返す。
// これらは marshalEmbeddedInstance のゼロ値非送信により go-wsman では反映できないため、PS 委譲の
// 判定に使う。NUMA フィールドは呼び出し側で現行値に正規化済みのため対象外。
func hasZeroDowngrade(current, want api.VmProcessor) bool {
	return (want.Maximum == 0 && current.Maximum != 0) ||
		(want.Reserve == 0 && current.Reserve != 0) ||
		(want.RelativeWeight == 0 && current.RelativeWeight != 0) ||
		(want.HwThreadCountPerCore == 0 && current.HwThreadCountPerCore != 0) ||
		(!want.CompatibilityForMigrationEnabled && current.CompatibilityForMigrationEnabled) ||
		(!want.CompatibilityForOlderOperatingSystemsEnabled && current.CompatibilityForOlderOperatingSystemsEnabled) ||
		(!want.EnableHostResourceProtection && current.EnableHostResourceProtection) ||
		(!want.ExposeVirtualizationExtensions && current.ExposeVirtualizationExtensions)
}

// applyProcessorSettings は要求 api.VmProcessor を Msvm_ProcessorSettingData に反映する
// (processorFromSettingData の逆変換)。InstanceID / VirtualQuantity (vCPU 数) は保持し、CPU の
// 割当・互換性フィールドのみ上書きする。単位: Maximum/Reserve は percent → CIM 値 (×1000)、
// RelativeWeight は 1:1。
func applyProcessorSettings(p *hyperv.Msvm_ProcessorSettingData, want api.VmProcessor) {
	p.Limit = clampUint64(want.Maximum) * 1000
	p.Reservation = clampUint64(want.Reserve) * 1000
	p.Weight = clampUint32(clampUint64(int64(want.RelativeWeight)))
	p.LimitProcessorFeatures = want.CompatibilityForMigrationEnabled
	p.LimitCPUID = want.CompatibilityForOlderOperatingSystemsEnabled
	p.HwThreadsPerCore = clampUint64(want.HwThreadCountPerCore)
	p.MaxProcessorsPerNumaNode = clampUint64(int64(want.MaximumCountPerNumaNode))
	p.MaxNumaNodesPerSocket = clampUint64(int64(want.MaximumCountPerNumaSocket))
	p.EnableHostResourceProtection = want.EnableHostResourceProtection
	p.ExposeVirtualizationExtensions = want.ExposeVirtualizationExtensions
}

// processorSettingsEqual は 2 つの api.VmProcessor を VmName を除く全フィールドで比較する。
// 差分なしガード (Set 省略) の判定に使う。
func processorSettingsEqual(a, b api.VmProcessor) bool {
	return a.CompatibilityForMigrationEnabled == b.CompatibilityForMigrationEnabled &&
		a.CompatibilityForOlderOperatingSystemsEnabled == b.CompatibilityForOlderOperatingSystemsEnabled &&
		a.HwThreadCountPerCore == b.HwThreadCountPerCore &&
		a.Maximum == b.Maximum &&
		a.Reserve == b.Reserve &&
		a.RelativeWeight == b.RelativeWeight &&
		a.MaximumCountPerNumaNode == b.MaximumCountPerNumaNode &&
		a.MaximumCountPerNumaSocket == b.MaximumCountPerNumaSocket &&
		a.EnableHostResourceProtection == b.EnableHostResourceProtection &&
		a.ExposeVirtualizationExtensions == b.ExposeVirtualizationExtensions
}

// clampInt64 は uint64 を int64 に安全に縮小する (上限超過は math.MaxInt64 に丸める)。
func clampInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

// clampInt32 は uint64 を int32 に安全に縮小する (上限超過は math.MaxInt32 に丸める)。
func clampInt32(v uint64) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(v)
}

// clampUint64 は int64 を uint64 に安全に拡大する (負値は 0 に丸める)。schema 上いずれも
// 非負だが、gosec G115 の符号付き→符号なし変換検出を避けるための防御。
func clampUint64(v int64) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}
