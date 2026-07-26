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
// 組み立てる純関数。
//
// BootOrders (Gen2BootOrder) は未対応: BootSourceOrder[] の各要素 (Msvm_BootSourceSettingData への
// EPR) を実デバイス (NIC/Drive) に逆引きする相関ロジックが go-wsman 側にまだ無い (Slice D 継続)。
// 黙って空を返すと「boot_order を設定したのに refresh で消えた」ように見える危険な silent drop に
// なるため (DoD: 黙って成功報告する実装は禁止)、BootSourceOrder が非空なら明示エラーで拒否する。
func firmwareFromSystemSettingData(vmName string, settings *hyperv.Msvm_VirtualSystemSettingData) (api.VmFirmware, error) {
	if len(settings.BootSourceOrder) > 0 {
		return api.VmFirmware{}, fmt.Errorf(
			"vm_firmware.boot_order は go-wsman 経路 (HYPERV_USE_WSMAN) では未対応です。" +
				"PowerShell 経路 (HYPERV_USE_WSMAN 未設定) を使ってください")
	}

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
	}, nil
}

// GetVmFirmware は VM の Gen2 ファームウェア設定を go-wsman 経由で取得する (BootOrders 除く)。
//
// PS 版 (Get-VMFirmware) をシャドウイングする。Gen1 VM は呼び出し側 (resource 層) が
// generation>1 ガードで既に除外しているため、本メソッドは Gen2 VM のみを想定する。
func (c *ClientConfig) GetVmFirmware(ctx context.Context, vmName string) (api.VmFirmware, error) {
	guid, err := c.resolveVMGUID(ctx, vmName)
	if err != nil {
		return api.VmFirmware{}, fmt.Errorf("hyperv-wsman: GetVmFirmware %q: %w", vmName, err)
	}
	settings, err := c.WsmanClient.GetSystemSettingData(ctx, guid)
	if err != nil {
		return api.VmFirmware{}, fmt.Errorf("hyperv-wsman: GetVmFirmware %q: %w", vmName, err)
	}
	firmware, err := firmwareFromSystemSettingData(vmName, settings)
	if err != nil {
		return api.VmFirmware{}, fmt.Errorf("hyperv-wsman: GetVmFirmware %q: %w", vmName, err)
	}
	return firmware, nil
}

// GetVmFirmwares は GetVmFirmware の結果を 1 件のスライスにラップする (PS 版と同じ契約)。
func (c *ClientConfig) GetVmFirmwares(ctx context.Context, vmName string) ([]api.VmFirmware, error) {
	firmware, err := c.GetVmFirmware(ctx, vmName)
	if err != nil {
		return nil, err
	}
	return []api.VmFirmware{firmware}, nil
}
