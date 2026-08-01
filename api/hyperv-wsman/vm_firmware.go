package hyperv_wsman

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// Secure Boot テンプレートの固定識別子(全 Hyper-V 環境で共通、秘密情報ではない)。
// 実機で `Set-VMFirmware -SecureBootTemplate <名前>` を実行し、CIM 側の
// Msvm_VirtualSystemSettingData.SecureBootTemplateId を読んで対応を確認した(2026-08-01、#100)。
// PS が受け付けるシンボリック名はこの 3 種で、Windows ゲストは MicrosoftWindows、
// Linux ゲストは MicrosoftUEFICertificateAuthority を使うのが一般的。
const (
	secureBootTemplateMicrosoftWindowsGUID     = "1734C6E8-3154-4DDA-BA5F-A874CC483422"
	secureBootTemplateMicrosoftUEFICAGUID      = "272E7447-90A4-4563-A4B9-8E4AB00526CE"
	secureBootTemplateOpenSourceShieldedVMGUID = "4292AE2B-EE2C-42B5-A969-DD8F8689F6F3"
)

// secureBootTemplateGUIDToName は Msvm_VirtualSystemSettingData.SecureBootTemplateId (実 GUID) から
// PS の -SecureBootTemplate が受け付けるシンボリック名への逆引き表。未知の GUID は
// secureBootTemplateIdToName が GUID 文字列のままフォールバックする。
var secureBootTemplateGUIDToName = map[string]string{
	secureBootTemplateMicrosoftWindowsGUID:     "MicrosoftWindows",
	secureBootTemplateMicrosoftUEFICAGUID:      "MicrosoftUEFICertificateAuthority",
	secureBootTemplateOpenSourceShieldedVMGUID: "OpenSourceShieldedVM",
}

// secureBootTemplateIdToName は既知の GUID ならシンボリック名を、未知/空なら入力をそのまま返す。
// GUID の大文字小文字表記はホストによって揺れうる (#100) ため EqualFold で照合する。
func secureBootTemplateIdToName(guid string) string {
	for g, n := range secureBootTemplateGUIDToName {
		if strings.EqualFold(g, guid) {
			return n
		}
	}
	return guid
}

// secureBootTemplateNameToGUID は secureBootTemplateIdToName の逆変換 (書き込み用)。
// 空文字は「テンプレート未指定」として ""/true (zero 値、firmwareZeroDowngrade が現行値との比較で
// 判断する)。既知の GUID そのものが入力された場合は入力表記のまま通す (state からの再入力等。
// 比較側が EqualFold なので表記を正規化する必要はなく、余計な書き換えを避ける)。
// それ以外の未知のシンボル名・未知の GUID は ok=false を返し、呼び出し側が PS 委譲を判断する
// 材料にする (どの GUID を書けばよいか分からないため、当て推量で GUID を発行しない)。
func secureBootTemplateNameToGUID(name string) (guid string, ok bool) {
	if name == "" {
		return "", true
	}
	for g, n := range secureBootTemplateGUIDToName {
		if strings.EqualFold(g, name) {
			return name, true // 既知 GUID の入力: 表記をそのまま保つ
		}
		if strings.EqualFold(n, name) {
			return g, true
		}
	}
	return "", false
}

// firmwareFromSystemSettingData は Msvm_VirtualSystemSettingData から api.VmFirmware (スカラー部分) を
// 組み立てる純関数。BootOrders は常に nil を返す (呼び出し側の GetVmFirmware が
// resolveBootOrdersForVM で別途解決し、成功すれば上書きする)。
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
		// strict モードで PS フォールバックが発火した際に原因を追えるよう理由を残す
		// (Fable レビュー指摘、#97)。
		log.Printf("[DEBUG][hyperv-wsman] GetVmFirmware %q: BootSourceOrder 相関解決に失敗、PS へ委譲します: %v", vmName, err)
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
