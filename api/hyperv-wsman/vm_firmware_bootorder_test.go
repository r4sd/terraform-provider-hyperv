package hyperv_wsman

import (
	"reflect"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// テスト用の実機確認済み ID 形式 (2026-07-26、Gen2 シェル VM):
//   NIC ブートソース   : Microsoft:<VMGUID>\<PortGUID>\B          → 実 NIC = Microsoft:<VMGUID>\<PortGUID>
//   Drive ブートソース : Microsoft:<VMGUID>\<CtrlGUID>\0\0\D\B    → 実 Drive = Microsoft:<VMGUID>\<CtrlGUID>\0\0\D
// つまり BootSource.InstanceID は対象デバイスの InstanceID に "\B" を付けたものと一致する。

func TestResolveBootOrders(t *testing.T) {
	const vm = "11111111-aaaa-bbbb-cccc-000000000001"
	nicDeviceID := `Microsoft:` + vm + `\nic-guid`
	dvdDeviceID := `Microsoft:` + vm + `\ctrl-guid\0\0\D`
	diskDeviceID := `Microsoft:` + vm + `\ctrl-guid\0\1\D`

	bootSourceOrder := []string{
		`\\HOST\root\virtualization\v2:Msvm_BootSourceSettingData.InstanceID="` + nicDeviceID + `\B"`,
		`\\HOST\root\virtualization\v2:Msvm_BootSourceSettingData.InstanceID="` + dvdDeviceID + `\B"`,
		`\\HOST\root\virtualization\v2:Msvm_BootSourceSettingData.InstanceID="` + diskDeviceID + `\B"`,
	}
	bootSources := []*hyperv.Msvm_BootSourceSettingData{
		{InstanceID: nicDeviceID + `\B`, BootSourceType: hyperv.BootSourceTypeNetwork, BootSourceDescription: "EFI Network"},
		{InstanceID: dvdDeviceID + `\B`, BootSourceType: hyperv.BootSourceTypeDrive, BootSourceDescription: "EFI SCSI Device"},
		{InstanceID: diskDeviceID + `\B`, BootSourceType: hyperv.BootSourceTypeDrive, BootSourceDescription: "EFI SCSI Device"},
	}
	nicRefs := []networkAdapterRef{
		{portInstanceID: nicDeviceID, adapter: api.VmNetworkAdapter{Name: "eth0", SwitchName: "Internet-sw", DynamicMacAddress: true}},
	}
	dvdRefs := []dvdDriveRef{
		{driveInstanceID: dvdDeviceID, dvd: api.VmDvdDrive{ControllerNumber: 0, ControllerLocation: 0, Path: `H:\ISO\ubuntu.iso`}},
	}
	diskRefs := []hardDiskDriveRef{
		{driveInstanceID: diskDeviceID, drive: api.VmHardDiskDrive{ControllerNumber: 0, ControllerLocation: 1, Path: `D:\VMs\disk.vhdx`}},
	}

	got, err := resolveBootOrders(bootSourceOrder, bootSources, nicRefs, dvdRefs, diskRefs)
	if err != nil {
		t.Fatalf("resolveBootOrders: %v", err)
	}
	want := []api.Gen2BootOrder{
		{Type: api.Gen2BootType_NetworkAdapter, NetworkAdapterName: "eth0", SwitchName: "Internet-sw"},
		{Type: api.Gen2BootType_DvdDrive, ControllerNumber: 0, ControllerLocation: 0, Path: `H:\ISO\ubuntu.iso`},
		{Type: api.Gen2BootType_HardDiskDrive, ControllerNumber: 0, ControllerLocation: 1, Path: `D:\VMs\disk.vhdx`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolveBootOrders:\ngot  %+v\nwant %+v", got, want)
	}
}

// TestResolveBootOrders_StaticMac は静的 MAC の NIC がブートソースの場合、MacAddress が
// 引き継がれることを検証する。
func TestResolveBootOrders_StaticMac(t *testing.T) {
	const vm = "11111111-aaaa-bbbb-cccc-000000000001"
	nicDeviceID := `Microsoft:` + vm + `\nic-guid`
	bootSourceOrder := []string{
		`\\HOST\root\virtualization\v2:Msvm_BootSourceSettingData.InstanceID="` + nicDeviceID + `\B"`,
	}
	bootSources := []*hyperv.Msvm_BootSourceSettingData{
		{InstanceID: nicDeviceID + `\B`, BootSourceType: hyperv.BootSourceTypeNetwork},
	}
	nicRefs := []networkAdapterRef{
		{portInstanceID: nicDeviceID, adapter: api.VmNetworkAdapter{Name: "eth0", DynamicMacAddress: false, StaticMacAddress: "001122334455"}},
	}

	got, err := resolveBootOrders(bootSourceOrder, bootSources, nicRefs, nil, nil)
	if err != nil {
		t.Fatalf("resolveBootOrders: %v", err)
	}
	if len(got) != 1 || got[0].MacAddress != "001122334455" {
		t.Errorf("got: %+v", got)
	}
}

// TestResolveBootOrders_UnresolvedDevice は BootSource が実デバイス一覧に見つからない場合、
// silent drop せず明示エラーになることを検証する (DoD: 黙って成功報告する実装は禁止)。
func TestResolveBootOrders_UnresolvedDevice(t *testing.T) {
	const vm = "11111111-aaaa-bbbb-cccc-000000000001"
	nicDeviceID := `Microsoft:` + vm + `\nic-guid-missing`
	bootSourceOrder := []string{
		`\\HOST\root\virtualization\v2:Msvm_BootSourceSettingData.InstanceID="` + nicDeviceID + `\B"`,
	}
	bootSources := []*hyperv.Msvm_BootSourceSettingData{
		{InstanceID: nicDeviceID + `\B`, BootSourceType: hyperv.BootSourceTypeNetwork},
	}
	// nicRefs が空 → 対応する NIC が見つからない。
	_, err := resolveBootOrders(bootSourceOrder, bootSources, nil, nil, nil)
	if err == nil {
		t.Fatal("対応デバイスが見つからない場合は明示エラーになるべき")
	}
}

// TestResolveBootOrders_UnresolvedBootSource は BootSourceOrder のエントリに対応する
// Msvm_BootSourceSettingData 自体が見つからない場合、明示エラーになることを検証する。
func TestResolveBootOrders_UnresolvedBootSource(t *testing.T) {
	bootSourceOrder := []string{
		`\\HOST\root\virtualization\v2:Msvm_BootSourceSettingData.InstanceID="Microsoft:vm\missing\B"`,
	}
	_, err := resolveBootOrders(bootSourceOrder, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("対応する BootSourceSettingData が無い場合は明示エラーになるべき")
	}
}

// TestResolveBootOrders_FileTypeUnsupported は BootSourceType=File (Windows Boot Manager、OS
// インストール済み VM が持つ) の場合に silent drop せず明示エラーになることを検証する。実運用では
// このエラーを受けて GetVmFirmware が PS へ委譲する (OS インストール済み VM の firmware read は
// ほぼ確実に PS 委譲になる制約、vm_firmware_bootorder.go の doc コメント参照)。
func TestResolveBootOrders_FileTypeUnsupported(t *testing.T) {
	const vm = "11111111-aaaa-bbbb-cccc-000000000001"
	fileDeviceID := `Microsoft:` + vm + `\boot-mgr-guid`
	bootSourceOrder := []string{
		`\\HOST\root\virtualization\v2:Msvm_BootSourceSettingData.InstanceID="` + fileDeviceID + `\B"`,
	}
	bootSources := []*hyperv.Msvm_BootSourceSettingData{
		{InstanceID: fileDeviceID + `\B`, BootSourceType: hyperv.BootSourceTypeFile, BootSourceDescription: "Windows Boot Manager"},
	}
	_, err := resolveBootOrders(bootSourceOrder, bootSources, nil, nil, nil)
	if err == nil {
		t.Fatal("BootSourceType=File は未対応のため明示エラーになるべき")
	}
}

// TestResolveBootOrders_Empty は空リストで no-op (nil, no error) を返すことを検証する。
func TestResolveBootOrders_Empty(t *testing.T) {
	got, err := resolveBootOrders(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("resolveBootOrders: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got: %+v", got)
	}
}

// resolveBootSourceRefs (write 方向、resolveBootOrders の逆変換) のテスト。
// bootSourceRefFunc は BootSourceRef(deviceInstanceID) の呼び出しをテスト内で検証可能にするための
// 関数値 (go-wsman.Client を丸ごとモックする必要を避ける)。

func TestResolveBootSourceRefs(t *testing.T) {
	const vm = "11111111-aaaa-bbbb-cccc-000000000001"
	nicDeviceID := `Microsoft:` + vm + `\nic-guid`
	dvdDeviceID := `Microsoft:` + vm + `\ctrl-guid\0\0\D`
	diskDeviceID := `Microsoft:` + vm + `\ctrl-guid\0\1\D`

	nicRefs := []networkAdapterRef{
		{portInstanceID: nicDeviceID, adapter: api.VmNetworkAdapter{Name: "eth0", SwitchName: "Internet-sw"}},
	}
	dvdRefs := []dvdDriveRef{
		{driveInstanceID: dvdDeviceID, dvd: api.VmDvdDrive{ControllerNumber: 0, ControllerLocation: 0}},
	}
	diskRefs := []hardDiskDriveRef{
		{driveInstanceID: diskDeviceID, drive: api.VmHardDiskDrive{ControllerNumber: 0, ControllerLocation: 1}},
	}

	bootOrders := []api.Gen2BootOrder{
		{Type: api.Gen2BootType_NetworkAdapter, NetworkAdapterName: "eth0"},
		{Type: api.Gen2BootType_DvdDrive, ControllerNumber: 0, ControllerLocation: 0},
		{Type: api.Gen2BootType_HardDiskDrive, ControllerNumber: 0, ControllerLocation: 1},
	}

	fakeBootSourceRef := func(deviceInstanceID string) string {
		return "REF:" + deviceInstanceID
	}

	got, err := resolveBootSourceRefs(fakeBootSourceRef, bootOrders, nicRefs, dvdRefs, diskRefs)
	if err != nil {
		t.Fatalf("resolveBootSourceRefs: %v", err)
	}
	want := []string{"REF:" + nicDeviceID, "REF:" + dvdDeviceID, "REF:" + diskDeviceID}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolveBootSourceRefs:\ngot  %+v\nwant %+v", got, want)
	}
}

// TestResolveBootSourceRefs_UnresolvedNetworkAdapter は名前の一致する NIC が無い場合、
// silent drop せず明示エラーになることを検証する。
func TestResolveBootSourceRefs_UnresolvedNetworkAdapter(t *testing.T) {
	bootOrders := []api.Gen2BootOrder{
		{Type: api.Gen2BootType_NetworkAdapter, NetworkAdapterName: "missing"},
	}
	_, err := resolveBootSourceRefs(func(string) string { return "" }, bootOrders, nil, nil, nil)
	if err == nil {
		t.Fatal("対応する NIC が無い場合は明示エラーになるべき")
	}
}

// TestResolveBootSourceRefs_UnresolvedDrive は controller/location の一致する DVD/HardDisk が
// 無い場合、明示エラーになることを検証する。
func TestResolveBootSourceRefs_UnresolvedDrive(t *testing.T) {
	bootOrders := []api.Gen2BootOrder{
		{Type: api.Gen2BootType_DvdDrive, ControllerNumber: 9, ControllerLocation: 9},
	}
	_, err := resolveBootSourceRefs(func(string) string { return "" }, bootOrders, nil, nil, nil)
	if err == nil {
		t.Fatal("対応する Drive が無い場合は明示エラーになるべき")
	}
}

// TestResolveBootSourceRefs_Empty は空リストで no-op (nil, no error) を返すことを検証する。
func TestResolveBootSourceRefs_Empty(t *testing.T) {
	got, err := resolveBootSourceRefs(func(string) string { return "" }, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("resolveBootSourceRefs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got: %+v", got)
	}
}
