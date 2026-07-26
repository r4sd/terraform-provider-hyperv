package hyperv_wsman

import (
	"fmt"
	"strings"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// resolveBootOrders は Msvm_VirtualSystemSettingData.BootSourceOrder[] を api.Gen2BootOrder の
// 順序付きリストに変換する。
//
// 実機確認済み (2026-07-26、Gen2 シェル VM、NIC 1台+SCSI Controller 1台+DVD 1台の1パターンのみ):
// Msvm_BootSourceSettingData.InstanceID は対象デバイス (NIC の
// Msvm_SyntheticEthernetPortSettingData、または Drive の Msvm_ResourceAllocationSettingData) の
// InstanceID に "\B" を付けたものと完全一致する (NIC: "Microsoft:<VM>\<PortGUID>\B"、DVD 経由 SCSI:
// "Microsoft:<VM>\<CtrlGUID>\0\0\D\B")。BootSourceType (Network/Drive) で経路を分け、Drive の場合は
// DVD/HardDisk のどちらかを driveInstanceID (CIM 上ホスト内一意なキー) で突き合わせて種別を決める
// (dvdRefs/diskRefs は ResourceSubType で構築時点から排他なので誤分類は構造上起きない)。
// **HardDiskDrive (VHD ブート) の相関は実機未検証**(go-wsman コードの対称性からの類推のみ)。
//
// File 型 (Windows Boot Manager、OS インストール済み Gen2 VM が持つ) は本関数では未対応で、
// resolveOneBootOrder が明示エラーを返し呼び出し側 (GetVmFirmware) が PS へ委譲する。つまり
// **OS インストール済みの Gen2 VM の firmware read は現状ほぼ確実に PS 委譲になる**(未インストールの
// 使い捨て VM 等、Network/Drive のみで構成される場合だけ go-wsman 側で完結する)。PS 版もこの
// File 型エントリを暗黙 drop する実装のため、委譲先の最終結果自体は変わらない。
//
// 対応するデバイスが見つからない場合は silent drop せず明示エラーにする (DoD: 黙って成功報告する
// 実装は禁止)。BootOrders が欠落したまま Terraform に「空」を返すと、次回 apply で意図しない
// BootSourceOrder のクリアを招く危険がある。
func resolveBootOrders(
	bootSourceOrder []string,
	bootSources []*hyperv.Msvm_BootSourceSettingData,
	nicRefs []networkAdapterRef,
	dvdRefs []dvdDriveRef,
	diskRefs []hardDiskDriveRef,
) ([]api.Gen2BootOrder, error) {
	if len(bootSourceOrder) == 0 {
		return nil, nil
	}

	bootSourceByID := make(map[string]*hyperv.Msvm_BootSourceSettingData, len(bootSources))
	for _, bs := range bootSources {
		bootSourceByID[bs.InstanceID] = bs
	}

	result := make([]api.Gen2BootOrder, 0, len(bootSourceOrder))
	for _, ref := range bootSourceOrder {
		bootOrder, err := resolveOneBootOrder(ref, bootSourceByID, nicRefs, dvdRefs, diskRefs)
		if err != nil {
			return nil, err
		}
		result = append(result, bootOrder)
	}
	return result, nil
}

// resolveOneBootOrder は BootSourceOrder[] の 1 エントリを解決する。
func resolveOneBootOrder(
	ref string,
	bootSourceByID map[string]*hyperv.Msvm_BootSourceSettingData,
	nicRefs []networkAdapterRef,
	dvdRefs []dvdDriveRef,
	diskRefs []hardDiskDriveRef,
) (api.Gen2BootOrder, error) {
	bootSourceID := extractInstanceIDFromRef(ref)
	bs, ok := bootSourceByID[bootSourceID]
	if !ok {
		return api.Gen2BootOrder{}, fmt.Errorf(
			"hyperv-wsman: BootSourceOrder のエントリ %q に対応する Msvm_BootSourceSettingData が見つかりません", bootSourceID)
	}
	deviceInstanceID := strings.TrimSuffix(bootSourceID, `\B`)

	switch bs.BootSourceType {
	case hyperv.BootSourceTypeNetwork:
		for _, r := range nicRefs {
			if r.portInstanceID != deviceInstanceID {
				continue
			}
			macAddress := ""
			if !r.adapter.DynamicMacAddress {
				macAddress = r.adapter.StaticMacAddress
			}
			return api.Gen2BootOrder{
				Type:               api.Gen2BootType_NetworkAdapter,
				NetworkAdapterName: r.adapter.Name,
				SwitchName:         r.adapter.SwitchName,
				MacAddress:         macAddress,
			}, nil
		}
		return api.Gen2BootOrder{}, fmt.Errorf(
			"hyperv-wsman: BootSource %q (Network) に対応する NIC が見つかりません", deviceInstanceID)

	case hyperv.BootSourceTypeDrive:
		for _, r := range dvdRefs {
			if r.driveInstanceID == deviceInstanceID {
				return api.Gen2BootOrder{
					Type:               api.Gen2BootType_DvdDrive,
					Path:               r.dvd.Path,
					ControllerNumber:   r.dvd.ControllerNumber,
					ControllerLocation: r.dvd.ControllerLocation,
				}, nil
			}
		}
		for _, r := range diskRefs {
			if r.driveInstanceID == deviceInstanceID {
				return api.Gen2BootOrder{
					Type:               api.Gen2BootType_HardDiskDrive,
					Path:               r.drive.Path,
					ControllerNumber:   int(r.drive.ControllerNumber),
					ControllerLocation: int(r.drive.ControllerLocation),
				}, nil
			}
		}
		return api.Gen2BootOrder{}, fmt.Errorf(
			"hyperv-wsman: BootSource %q (Drive) に対応する DVD/HardDisk が見つかりません", deviceInstanceID)

	default:
		return api.Gen2BootOrder{}, fmt.Errorf(
			"hyperv-wsman: BootSource %q の BootSourceType %d は未対応です (File/Unknown)", bootSourceID, bs.BootSourceType)
	}
}

// resolveBootSourceRefs は resolveBootOrders の逆変換で、api.Gen2BootOrder[] (書き込み要求) を
// Msvm_VirtualSystemSettingData.BootSourceOrder[] に書く WMI 参照文字列のリストに変換する。
// bootSourceRef は deviceInstanceID から参照文字列を組み立てる関数 (実体は
// hyperv.Client.BootSourceRef、テストでは差し替え可能にするため関数値で受ける)。
//
// NetworkAdapter は NetworkAdapterName で、DvdDrive/HardDiskDrive は
// ControllerNumber+ControllerLocation で対応デバイスを突き合わせる (resolveOneBootOrder の
// 読み取り側と対称)。対応するデバイスが見つからない場合は silent drop せず明示エラーにする
// (DoD: 黙って成功報告する実装は禁止)。
func resolveBootSourceRefs(
	bootSourceRef func(deviceInstanceID string) string,
	bootOrders []api.Gen2BootOrder,
	nicRefs []networkAdapterRef,
	dvdRefs []dvdDriveRef,
	diskRefs []hardDiskDriveRef,
) ([]string, error) {
	if len(bootOrders) == 0 {
		return nil, nil
	}

	result := make([]string, 0, len(bootOrders))
	for _, bo := range bootOrders {
		deviceID, err := resolveBootOrderDeviceID(bo, nicRefs, dvdRefs, diskRefs)
		if err != nil {
			return nil, err
		}
		result = append(result, bootSourceRef(deviceID))
	}
	return result, nil
}

// resolveBootOrderDeviceID は 1 件の api.Gen2BootOrder に対応するデバイスの InstanceID を返す。
func resolveBootOrderDeviceID(
	bo api.Gen2BootOrder,
	nicRefs []networkAdapterRef,
	dvdRefs []dvdDriveRef,
	diskRefs []hardDiskDriveRef,
) (string, error) {
	switch bo.Type {
	case api.Gen2BootType_NetworkAdapter:
		for _, r := range nicRefs {
			if r.adapter.Name == bo.NetworkAdapterName {
				return r.portInstanceID, nil
			}
		}
		return "", fmt.Errorf(
			"hyperv-wsman: boot order の NetworkAdapter %q に対応する NIC が見つかりません", bo.NetworkAdapterName)

	case api.Gen2BootType_DvdDrive:
		for _, r := range dvdRefs {
			if r.dvd.ControllerNumber == bo.ControllerNumber && r.dvd.ControllerLocation == bo.ControllerLocation {
				return r.driveInstanceID, nil
			}
		}
		return "", fmt.Errorf(
			"hyperv-wsman: boot order の DvdDrive (controller=%d location=%d) に対応するデバイスが見つかりません",
			bo.ControllerNumber, bo.ControllerLocation)

	case api.Gen2BootType_HardDiskDrive:
		for _, r := range diskRefs {
			if int(r.drive.ControllerNumber) == bo.ControllerNumber && int(r.drive.ControllerLocation) == bo.ControllerLocation {
				return r.driveInstanceID, nil
			}
		}
		return "", fmt.Errorf(
			"hyperv-wsman: boot order の HardDiskDrive (controller=%d location=%d) に対応するデバイスが見つかりません",
			bo.ControllerNumber, bo.ControllerLocation)

	default:
		return "", fmt.Errorf("hyperv-wsman: boot order の Type %v は未対応です", bo.Type)
	}
}
