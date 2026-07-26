package hyperv_wsman

import (
	"context"
	"fmt"
	"log"
	"math"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// clampUint16 は int を uint16 に安全に縮小する (負値は 0、上限超過は MaxUint16 にクランプ)。
// 直接 uint16(v) すると CodeQL が上限チェックなしの縮小変換 (high) として検出するため
// (clampUint32 と同じ理由、vm.go 参照)。
func clampUint16(v int) uint16 {
	if v < 0 {
		return 0
	}
	if v > math.MaxUint16 {
		return math.MaxUint16
	}
	return uint16(v)
}

// firmwareCIMValues は CreateOrUpdateVmFirmware の要求パラメータを CIM (Msvm_VirtualSystemSettingData)
// の形に変換した値。判定・適用ロジックをネットワーク呼び出しから切り離してテスト可能にするための型。
type firmwareCIMValues struct {
	secureBoot             bool
	secureBootTemplateGUID string
	networkBootProtocol    uint16
	consoleMode            uint16
	pauseAfterBootFailure  bool
	bootSourceOrder        []string
}

// buildFirmwareCIMValues は CreateOrUpdateVmFirmware の要求パラメータを firmwareCIMValues に変換する。
// secureBootTemplate が未知のシンボル名の場合 ok=false を返す (書き込むべき GUID が分からないため)。
func buildFirmwareCIMValues(
	enableSecureBoot api.OnOffState,
	secureBootTemplate string,
	preferredNetworkBootProtocol api.IPProtocolPreference,
	consoleMode api.ConsoleModeType,
	pauseAfterBootFailure api.OnOffState,
	bootSourceOrder []string,
) (values firmwareCIMValues, ok bool) {
	protocol := hyperv.NetworkBootPreferredProtocolIPv4
	if preferredNetworkBootProtocol == api.IPProtocolPreference_IPv6 {
		protocol = hyperv.NetworkBootPreferredProtocolIPv6
	}
	templateGUID, resolvable := secureBootTemplateNameToGUID(secureBootTemplate)
	return firmwareCIMValues{
		secureBoot:             enableSecureBoot == api.OnOffState_On,
		secureBootTemplateGUID: templateGUID,
		networkBootProtocol:    protocol,
		consoleMode:            clampUint16(int(consoleMode)),
		pauseAfterBootFailure:  pauseAfterBootFailure == api.OnOffState_On,
		bootSourceOrder:        bootSourceOrder,
	}, resolvable
}

// firmwareWriteNoop は current (現行 CIM 値) と want (書き込み要求値) が完全一致するかを返す。
// 一致する場合は Set を省略できる (差分なしガード、homelab 既定運用の strict PS-0 維持)。
//
// BootSourceOrder は current (サーバが返す生の WMI 参照文字列、ホスト前置+バックスラッシュ区切りの
// 実機形式) と want (BootSourceRef が組み立てるクライアント側参照文字列、wmiObjectPath はホスト
// 前置なし+フォワードスラッシュ区切りの namespace) とで文字列表現が一致しない (Fable 指摘)。
// 生文字列比較では常に不一致になり差分なしガードが機能しないため、両辺を
// extractInstanceIDFromRef で素の InstanceID に正規化してから比較する。
func firmwareWriteNoop(current *hyperv.Msvm_VirtualSystemSettingData, want firmwareCIMValues) bool {
	return current.SecureBoot == want.secureBoot &&
		current.SecureBootTemplateId == want.secureBootTemplateGUID &&
		current.NetworkBootPreferredProtocol == want.networkBootProtocol &&
		current.ConsoleMode == want.consoleMode &&
		current.PauseAfterBootFailure == want.pauseAfterBootFailure &&
		stringSlicesEqual(normalizedBootSourceOrder(current.BootSourceOrder), normalizedBootSourceOrder(want.bootSourceOrder))
}

// normalizedBootSourceOrder は BootSourceOrder[] の各要素を素の InstanceID に正規化する
// (extractInstanceIDFromRef、firmwareWriteNoop の比較専用)。
func normalizedBootSourceOrder(refs []string) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, len(refs))
	for i, ref := range refs {
		out[i] = extractInstanceIDFromRef(ref)
	}
	return out
}

// firmwareZeroDowngrade は current → want が「非ゼロ→ゼロ」の遷移を含むかを返す。
//
// marshalEmbeddedInstance はゼロ値のフィールドを送信しない (CIM SettingData の「未指定=変更なし」
// 慣習) ため、この遷移は go-wsman では反映できず「成功報告なのに変わらない」恒常 diff になる
// (Slice A vm_processor の Fable C パターンと同型)。NetworkBootPreferredProtocol は有効値が
// 4096/4097 でどちらも非ゼロのため対象外 (常に表現可能)。
func firmwareZeroDowngrade(current *hyperv.Msvm_VirtualSystemSettingData, want firmwareCIMValues) bool {
	return (current.SecureBoot && !want.secureBoot) ||
		(current.PauseAfterBootFailure && !want.pauseAfterBootFailure) ||
		(current.ConsoleMode != hyperv.ConsoleModeDefault && want.consoleMode == hyperv.ConsoleModeDefault) ||
		(current.SecureBootTemplateId != "" && want.secureBootTemplateGUID == "") ||
		(len(current.BootSourceOrder) > 0 && len(want.bootSourceOrder) == 0)
}

// applyFirmwareSettings は want を sd に反映する。sd は呼び出し側が InstanceID のみを設定した
// 最小インスタンス (UpdateVm に送る「変更箇所 + InstanceID」だけの instance、他フィールドは
// ゼロ値のまま = 未指定として扱われる)。
func applyFirmwareSettings(sd *hyperv.Msvm_VirtualSystemSettingData, want firmwareCIMValues) {
	sd.SecureBoot = want.secureBoot
	sd.SecureBootTemplateId = want.secureBootTemplateGUID
	sd.NetworkBootPreferredProtocol = want.networkBootProtocol
	sd.ConsoleMode = want.consoleMode
	sd.PauseAfterBootFailure = want.pauseAfterBootFailure
	sd.BootSourceOrder = want.bootSourceOrder
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// CreateOrUpdateVmFirmware は VM の Gen2 ファームウェア設定を go-wsman 経由で書き込む。
//
// PS 版 (Set-VMFirmware) をシャドウイングする。GetSystemSettingData で現行設定を取得し (この取得は
// GetVmFirmware ではなく go-wsman を直接呼ぶ。GetVmFirmware は BootSourceOrder に File 型が
// 含まれる場合に PS へ委譲することがあり、書き込みが表現可能でも読み取り経路で PS が発火してしまう
// ため)、以下の順で処理する:
//
//  1. boot order 解決: bootOrders が非空なら NIC/DVD/HardDisk と突き合わせて WMI 参照文字列に変換
//     (resolveBootSourceRefsForVM)。解決失敗は go-wsman で表現できないケースとして PS へ委譲。
//  2. 差分なしガード: 現行値と要求値が完全一致なら Set を省略 (書き込み 0 件)。
//  3. ゼロ/false ダウングレードの PS 委譲: firmwareZeroDowngrade を参照。
//  4. UpdateVm (ModifySystemSettings) で書き戻し。InstanceID + firmware 関連フィールドのみを持つ
//     **最小インスタンス**を送る (bug_sweep_integration_test.go の実機実証パターンと同じ)。
//     current をまるごとコピーして一部だけ書き換えたインスタンスを送ると、firmware と無関係な
//     フィールド (VirtualNumaEnabled 等、round-trip 未検証) が ModifySystemSettings に
//     Exception (ErrorCode=32768「変数の種類が間違っています」) で拒否されることを実機で確認した。
func (c *ClientConfig) CreateOrUpdateVmFirmware(
	ctx context.Context,
	vmName string,
	bootOrders []api.Gen2BootOrder,
	enableSecureBoot api.OnOffState,
	secureBootTemplate string,
	preferredNetworkBootProtocol api.IPProtocolPreference,
	consoleMode api.ConsoleModeType,
	pauseAfterBootFailure api.OnOffState,
) error {
	guid, err := c.resolveVMGUID(ctx, vmName)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmFirmware %q: %w", vmName, err)
	}
	current, err := c.WsmanClient.GetSystemSettingData(ctx, guid)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmFirmware %q: get: %w", vmName, err)
	}

	delegate := func() error {
		return c.ClientConfig.CreateOrUpdateVmFirmware(ctx, vmName, bootOrders,
			enableSecureBoot, secureBootTemplate, preferredNetworkBootProtocol, consoleMode, pauseAfterBootFailure)
	}

	var bootSourceOrder []string
	if len(bootOrders) > 0 {
		bootSourceOrder, err = c.resolveBootSourceRefsForVM(ctx, vmName, bootOrders)
		if err != nil {
			log.Printf("[DEBUG][hyperv-wsman] CreateOrUpdateVmFirmware %q: boot order 解決に失敗、PS へ委譲します: %v", vmName, err)
			return delegate()
		}
	}

	want, templateResolvable := buildFirmwareCIMValues(
		enableSecureBoot, secureBootTemplate, preferredNetworkBootProtocol, consoleMode, pauseAfterBootFailure, bootSourceOrder)
	if !templateResolvable {
		log.Printf("[DEBUG][hyperv-wsman] CreateOrUpdateVmFirmware %q: SecureBootTemplate %q のGUIDが不明、PS へ委譲します", vmName, secureBootTemplate)
		return delegate()
	}

	if firmwareWriteNoop(current, want) {
		return nil
	}
	if firmwareZeroDowngrade(current, want) {
		log.Printf("[DEBUG][hyperv-wsman] CreateOrUpdateVmFirmware %q: ゼロ値ダウングレードを検出、PS へ委譲します", vmName)
		return delegate()
	}

	mod := &hyperv.Msvm_VirtualSystemSettingData{InstanceID: current.InstanceID}
	applyFirmwareSettings(mod, want)
	jobRef, err := c.WsmanClient.UpdateVm(ctx, mod)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmFirmware %q: set: %w", vmName, err)
	}
	if err := c.WsmanClient.WaitForJob(ctx, jobRef); err != nil {
		return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmFirmware %q: wait: %w", vmName, err)
	}
	return nil
}

// resolveBootSourceRefsForVM は VM の NIC/DVD/HardDisk 一覧を取得し、resolveBootSourceRefs に渡す
// (resolveBootOrdersForVM の書き込み版、resolveBootOrders の逆変換)。
func (c *ClientConfig) resolveBootSourceRefsForVM(ctx context.Context, vmName string, bootOrders []api.Gen2BootOrder) ([]string, error) {
	nicRefs, err := c.getNetworkAdapterRefs(ctx, vmName)
	if err != nil {
		return nil, err
	}
	dvdRefs, err := c.getDvdDriveRefs(ctx, vmName)
	if err != nil {
		return nil, err
	}
	diskRefs, err := c.getHardDiskDriveRefs(ctx, vmName)
	if err != nil {
		return nil, err
	}
	return resolveBootSourceRefs(c.WsmanClient.BootSourceRef, bootOrders, nicRefs, dvdRefs, diskRefs)
}

// CreateOrUpdateVmFirmwares は CreateOrUpdateVmFirmware への 1 件ラッパー (PS 版と同じ契約)。
func (c *ClientConfig) CreateOrUpdateVmFirmwares(ctx context.Context, vmName string, vmFirmwares []api.VmFirmware) error {
	if len(vmFirmwares) == 0 {
		return nil
	}
	if len(vmFirmwares) > 1 {
		return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmFirmwares %q: only 1 vm firmware setting allowed per a vm", vmName)
	}
	fw := vmFirmwares[0]
	return c.CreateOrUpdateVmFirmware(ctx, vmName, fw.BootOrders,
		fw.EnableSecureBoot, fw.SecureBootTemplate, fw.PreferredNetworkBootProtocol, fw.ConsoleMode, fw.PauseAfterBootFailure)
}
