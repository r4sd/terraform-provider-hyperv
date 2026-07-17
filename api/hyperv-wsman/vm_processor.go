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
// Create/Update (SetProcessorSettings + 単位変換込みの書き戻し) は go-wsman 側に安全な
// 書き込みプリミティブが未実装のため、埋め込み hyperv_winrm.ClientConfig から promotion されて
// PS 実装にフォールバックする。
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
//     PS Maximum/Reserve は 0..100 (%) = CIM 値 / 1000。
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
