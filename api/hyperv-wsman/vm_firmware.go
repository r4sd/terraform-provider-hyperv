package hyperv_wsman

import (
	"context"
	"fmt"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// secureBootTemplateGUIDToName は Msvm_VirtualSystemSettingData.SecureBootTemplateId (実 GUID) から
// PS の -SecureBootTemplate が受け付けるシンボリック名への逆引き表。
//
// 実機確認 (2026-07-26、Gen2 シェル VM の既定値): "MicrosoftWindows" = 1734C6E8-3154-4DDA-BA5F-
// A874CC483422 の 1 件のみ確認済み。他のテンプレート (MicrosoftUEFICertificateAuthority /
// OpenSourceShieldedVM 等) の GUID は未確認のため表に含めない (一次資料検証ルール: 確認できていない
// 値をコードに埋め込まない)。未知の GUID は secureBootTemplateIdToName が GUID 文字列のままフォール
// バックする。
var secureBootTemplateGUIDToName = map[string]string{
	"1734C6E8-3154-4DDA-BA5F-A874CC483422": "MicrosoftWindows",
}

// secureBootTemplateIdToName は既知の GUID ならシンボリック名を、未知/空なら入力をそのまま返す。
func secureBootTemplateIdToName(guid string) string {
	if name, ok := secureBootTemplateGUIDToName[guid]; ok {
		return name
	}
	return guid
}

// firmwareFromSystemSettingData は Msvm_VirtualSystemSettingData から api.VmFirmware (スカラー部分) を
// 組み立てる純関数。BootSourceOrder が非空 (Gen2 VM にブート可能デバイスが付いている場合、config で
// boot_order を明示していなくても Hyper-V が自動生成する) の場合の扱いは呼び出し側 (GetVmFirmware) の
// 責務とし、ここでは常に BootOrders=nil の api.VmFirmware を返す (silent drop ではなく、非空ケースを
// 呼び出し側が PS 委譲で処理する前提。Fable レビュー指摘: デバイス付き Gen2 VM 全般の Read を壊す
// 「広すぎる拒否」を避けるため、ここでの明示エラーは撤回した)。
func firmwareFromSystemSettingData(vmName string, settings *hyperv.Msvm_VirtualSystemSettingData) api.VmFirmware {
	enableSecureBoot := api.OnOffState_Off
	if settings.SecureBoot {
		enableSecureBoot = api.OnOffState_On
	}

	preferredProtocol := api.IPProtocolPreference_IPv4
	if settings.NetworkBootPreferredProtocol == hyperv.NetworkBootPreferredProtocolIPv6 {
		preferredProtocol = api.IPProtocolPreference_IPv6
	}

	pauseAfterBootFailure := api.OnOffState_Off
	if settings.PauseAfterBootFailure {
		pauseAfterBootFailure = api.OnOffState_On
	}

	return api.VmFirmware{
		VmName:                       vmName,
		BootOrders:                   nil,
		EnableSecureBoot:             enableSecureBoot,
		SecureBootTemplate:           secureBootTemplateIdToName(settings.SecureBootTemplateId),
		PreferredNetworkBootProtocol: preferredProtocol,
		ConsoleMode:                  api.ConsoleModeType(settings.ConsoleMode),
		PauseAfterBootFailure:        pauseAfterBootFailure,
	}
}

// GetVmFirmware は VM の Gen2 ファームウェア設定を go-wsman 経由で取得する。
//
// PS 版 (Get-VMFirmware) をシャドウイングする。Gen1 VM は呼び出し側 (resource 層) が
// generation>1 ガードで既に除外しているため、本メソッドは Gen2 VM のみを想定する。
//
// BootSourceOrder が非空の場合、NIC/DVD/HardDisk の一覧を取得して resolveBootOrders で相関解決する
// (実機確認済みの対応関係、vm_firmware_bootorder.go 参照)。相関が失敗する場合 (未知の BootSourceType
// や、参照先デバイスが現在の一覧に見つからない等の想定外ケース) は PS (埋め込み winrm) に委譲する
// (Slice A の processor と同じ「go-wsman で表現できないケースは PS フォールバック」設計)。
func (c *ClientConfig) GetVmFirmware(ctx context.Context, vmName string) (api.VmFirmware, error) {
	guid, err := c.resolveVMGUID(ctx, vmName)
	if err != nil {
		return api.VmFirmware{}, fmt.Errorf("hyperv-wsman: GetVmFirmware %q: %w", vmName, err)
	}
	settings, err := c.WsmanClient.GetSystemSettingData(ctx, guid)
	if err != nil {
		return api.VmFirmware{}, fmt.Errorf("hyperv-wsman: GetVmFirmware %q: %w", vmName, err)
	}

	firmware := firmwareFromSystemSettingData(vmName, settings)
	if len(settings.BootSourceOrder) == 0 {
		return firmware, nil
	}

	bootOrders, err := c.resolveBootOrdersForVM(ctx, vmName, guid, settings.BootSourceOrder)
	if err != nil {
		return c.ClientConfig.GetVmFirmware(ctx, vmName)
	}
	firmware.BootOrders = bootOrders
	return firmware, nil
}

// resolveBootOrdersForVM は VM の NIC/DVD/HardDisk 一覧と Msvm_BootSourceSettingData を取得し、
// resolveBootOrders に渡すための材料を揃える。
func (c *ClientConfig) resolveBootOrdersForVM(ctx context.Context, vmName, guid string, bootSourceOrder []string) ([]api.Gen2BootOrder, error) {
	bootSources, err := c.WsmanClient.ListBootSources(ctx, guid)
	if err != nil {
		return nil, err
	}
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
	return resolveBootOrders(bootSourceOrder, bootSources, nicRefs, dvdRefs, diskRefs)
}

// GetVmFirmwares は GetVmFirmware の結果を 1 件のスライスにラップする (PS 版と同じ契約)。
func (c *ClientConfig) GetVmFirmwares(ctx context.Context, vmName string) ([]api.VmFirmware, error) {
	firmware, err := c.GetVmFirmware(ctx, vmName)
	if err != nil {
		return nil, err
	}
	return []api.VmFirmware{firmware}, nil
}
