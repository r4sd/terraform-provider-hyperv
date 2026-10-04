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
// ControllerNumber+ControllerLocation、または**どちらかが未指定なら Path** で
// 対応デバイスを突き合わせる (resolveDriveBootOrder 参照)。
// 対応するデバイスが見つからない場合は silent drop せず明示エラーにする
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
		// PS 版 (Get/Set-VMFirmware テンプレート) と同じく Name→SwitchName→MacAddress の順で絞り込む
		// (指定されたものだけをフィルタ条件にする)。Hyper-V の NIC 既定名は "Network Adapter" で
		// 同名 NIC が複数存在しうるため、Name だけの一致で決め打つと違う NIC を誤って書き込む危険が
		// ある (Fable 指摘、silent corruption)。一意に絞り込めない場合は明示エラーにして PS へ
		// 委譲する (DoD: 黙って成功報告する実装は禁止)。
		var matches []networkAdapterRef
		for _, r := range nicRefs {
			if r.adapter.Name != bo.NetworkAdapterName {
				continue
			}
			if bo.SwitchName != "" && r.adapter.SwitchName != bo.SwitchName {
				continue
			}
			if bo.MacAddress != "" {
				mac := ""
				if !r.adapter.DynamicMacAddress {
					mac = r.adapter.StaticMacAddress
				}
				if !strings.EqualFold(mac, bo.MacAddress) {
					continue
				}
			}
			matches = append(matches, r)
		}
		switch len(matches) {
		case 1:
			return matches[0].portInstanceID, nil
		case 0:
			return "", fmt.Errorf(
				"hyperv-wsman: boot order の NetworkAdapter %q (SwitchName=%q MacAddress=%q) に対応する NIC が見つかりません",
				bo.NetworkAdapterName, bo.SwitchName, bo.MacAddress)
		default:
			return "", fmt.Errorf(
				"hyperv-wsman: boot order の NetworkAdapter %q が複数 (%d件) の NIC に一致し一意に特定できません。SwitchName/MacAddress で絞り込んでください",
				bo.NetworkAdapterName, len(matches))
		}

	case api.Gen2BootType_DvdDrive:
		candidates := make([]driveCandidate, 0, len(dvdRefs))
		for _, r := range dvdRefs {
			candidates = append(candidates, driveCandidate{
				instanceID:         r.driveInstanceID,
				path:               r.dvd.Path,
				controllerNumber:   r.dvd.ControllerNumber,
				controllerLocation: r.dvd.ControllerLocation,
			})
		}
		return resolveDriveBootOrder("DvdDrive", bo, candidates)

	case api.Gen2BootType_HardDiskDrive:
		candidates := make([]driveCandidate, 0, len(diskRefs))
		for _, r := range diskRefs {
			candidates = append(candidates, driveCandidate{
				instanceID:         r.driveInstanceID,
				path:               r.drive.Path,
				controllerNumber:   int(r.drive.ControllerNumber),
				controllerLocation: int(r.drive.ControllerLocation),
			})
		}
		return resolveDriveBootOrder("HardDiskDrive", bo, candidates)

	default:
		return "", fmt.Errorf("hyperv-wsman: boot order の Type %v は未対応です", bo.Type)
	}
}

// bootOrderUnspecified は controller_number / controller_location の「未指定」の境界値。
//
// PS 版スキーマの Default が -1 で、**path だけでデバイスを指定する運用**を許容している。
//
// **この値「以下」を未指定として扱う。** PS テンプレート (api/hyperv-winrm/vm_firmware.go) が
// `-gt -1` で判定しており、-2 のような値も未指定になる。schema に負値のバリデーションが
// 無いので実際に書けてしまう。== で比較すると CIM 経路だけ「指定」扱いになり、
// 同じ config が経路で違う結果になる。
const bootOrderUnspecified = -1

// driveCandidate は boot order の突合用に DVD と HardDisk を同じ形で扱う中間表現。
type driveCandidate struct {
	instanceID         string
	path               string
	controllerNumber   int
	controllerLocation int
}

// resolveDriveBootOrder は 1 件の boot order に対応する Drive の InstanceID を返す。
//
// **controller_number / controller_location が両方指定されている場合は、
// その 2 つの完全一致のみで決める (path は見ない)。** CIM 経路の従来の挙動で、ここを変えて
// path も条件に加えると「controller 位置は合っているが path の表記が違う」既存 config を壊す。
//
// ⚠️ **ここは PS 経路と挙動が違う。** PS テンプレート (api/hyperv-winrm/vm_firmware.go) は
// path が非空なら**両方指定でも** `-ieq` で絞るため、path 不一致のエントリは 0 台になる。
// この差は本関数の導入前から存在する (旧実装も両方指定時は controller だけを見ていた)。
// 揃えるかどうかは #170 で追跡する。揃えると CIM 経路で現に動いている config が
// PS 委譲に落ちるため、本 Issue (#100) のスコープでは変えない。
//
// どちらかが未指定 (-1) のときに限り、指定された側 + path で絞り込む (#100 項目 3)。
// これが無いと -1 は実デバイスに一致せず必ずエラー → PS 委譲になり PS-0 が達成できない。
//
// path の比較は大文字小文字を無視する (Windows のファイルパスは大小を区別しない)。
//
// 絞り込めない場合は silent drop も「1 台だから選ぶ」もせず明示エラーにする。
// 何を指しているのか決まっていない指定で推測で 1 台を掴むより、PS へ委譲させる方が安全。
func resolveDriveBootOrder(kind string, bo api.Gen2BootOrder, candidates []driveCandidate) (string, error) {
	numberSpecified := bo.ControllerNumber > bootOrderUnspecified
	locationSpecified := bo.ControllerLocation > bootOrderUnspecified

	if numberSpecified && locationSpecified {
		for _, c := range candidates {
			if c.controllerNumber == bo.ControllerNumber && c.controllerLocation == bo.ControllerLocation {
				return c.instanceID, nil
			}
		}
		return "", fmt.Errorf(
			"hyperv-wsman: boot order の %s (controller=%d location=%d) に対応するデバイスが見つかりません",
			kind, bo.ControllerNumber, bo.ControllerLocation)
	}

	if bo.Path == "" && !numberSpecified && !locationSpecified {
		return "", fmt.Errorf(
			"hyperv-wsman: boot order の %s は path も controller_number/controller_location も"+
				"指定されていないため、どのデバイスを指すか決まりません", kind)
	}

	var matches []driveCandidate
	for _, c := range candidates {
		if numberSpecified && c.controllerNumber != bo.ControllerNumber {
			continue
		}
		if locationSpecified && c.controllerLocation != bo.ControllerLocation {
			continue
		}
		if bo.Path != "" && !strings.EqualFold(c.path, bo.Path) {
			continue
		}
		matches = append(matches, c)
	}
	switch len(matches) {
	case 1:
		return matches[0].instanceID, nil
	case 0:
		return "", fmt.Errorf(
			"hyperv-wsman: boot order の %s (path=%q controller=%d location=%d) に対応するデバイスが見つかりません",
			kind, bo.Path, bo.ControllerNumber, bo.ControllerLocation)
	default:
		return "", fmt.Errorf(
			"hyperv-wsman: boot order の %s (path=%q) が複数 (%d件) のデバイスに一致し一意に特定できません。"+
				"controller_number/controller_location で絞り込んでください",
			kind, bo.Path, len(matches))
	}
}
