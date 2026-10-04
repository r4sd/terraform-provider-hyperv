package hyperv_wsman

import (
	"strings"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestResolveBootSourceRefs_PathOnly は controller_number / controller_location が
// 未指定 (-1) のときに Path で一意に絞り込めることを検証する (#100 項目 3)。
//
// PS 版スキーマは controller_number / controller_location の Default=-1 を許容し、
// path だけでデバイスを指定する運用ができる。CIM 側が完全一致しか見ないと
// -1 は実デバイスに一致せず必ずエラー → PS 委譲になり PS-0 が達成できない。
//
// **両方が指定されている場合の挙動は変えない。** Path を常にフィルタ条件に加えると、
// controller 位置は合っているが path の表記が違う既存 config を壊しうる。
func TestResolveBootSourceRefs_PathOnly(t *testing.T) {
	const vm = "11111111-aaaa-bbbb-cccc-000000000001"
	dvd0 := `Microsoft:` + vm + `\ctrl\0\0\D`
	disk1 := `Microsoft:` + vm + `\ctrl\0\1\D`
	disk2 := `Microsoft:` + vm + `\ctrl\1\0\D`

	dvdRefs := []dvdDriveRef{
		{driveInstanceID: dvd0, dvd: api.VmDvdDrive{
			ControllerNumber: 0, ControllerLocation: 0, Path: `H:\ISO\talos.iso`}},
	}
	diskRefs := []hardDiskDriveRef{
		{driveInstanceID: disk1, drive: api.VmHardDiskDrive{
			ControllerNumber: 0, ControllerLocation: 1, Path: `D:\VMs\boot.vhdx`}},
		{driveInstanceID: disk2, drive: api.VmHardDiskDrive{
			ControllerNumber: 1, ControllerLocation: 0, Path: `D:\VMs\data.vhdx`}},
	}
	ref := func(id string) string { return "REF:" + id }

	t.Run("DVD: path のみ (両方 -1)", func(t *testing.T) {
		got, err := resolveBootSourceRefs(ref, []api.Gen2BootOrder{{
			Type: api.Gen2BootType_DvdDrive, Path: `H:\ISO\talos.iso`,
			ControllerNumber: -1, ControllerLocation: -1,
		}}, nil, dvdRefs, diskRefs)
		if err != nil {
			t.Fatalf("resolveBootSourceRefs: %v", err)
		}
		if len(got) != 1 || got[0] != "REF:"+dvd0 {
			t.Errorf("got %+v, want [REF:%s]", got, dvd0)
		}
	})

	t.Run("HardDisk: path のみ (両方 -1)", func(t *testing.T) {
		got, err := resolveBootSourceRefs(ref, []api.Gen2BootOrder{{
			Type: api.Gen2BootType_HardDiskDrive, Path: `D:\VMs\data.vhdx`,
			ControllerNumber: -1, ControllerLocation: -1,
		}}, nil, dvdRefs, diskRefs)
		if err != nil {
			t.Fatalf("resolveBootSourceRefs: %v", err)
		}
		if len(got) != 1 || got[0] != "REF:"+disk2 {
			t.Errorf("got %+v, want [REF:%s]", got, disk2)
		}
	})

	t.Run("path は大文字小文字を無視する (Windows のパス)", func(t *testing.T) {
		got, err := resolveBootSourceRefs(ref, []api.Gen2BootOrder{{
			Type: api.Gen2BootType_HardDiskDrive, Path: `d:\vms\DATA.VHDX`,
			ControllerNumber: -1, ControllerLocation: -1,
		}}, nil, dvdRefs, diskRefs)
		if err != nil {
			t.Fatalf("resolveBootSourceRefs: %v", err)
		}
		if len(got) != 1 || got[0] != "REF:"+disk2 {
			t.Errorf("got %+v, want [REF:%s]", got, disk2)
		}
	})

	t.Run("片方だけ -1 のときは指定された側 + path で絞る", func(t *testing.T) {
		got, err := resolveBootSourceRefs(ref, []api.Gen2BootOrder{{
			Type: api.Gen2BootType_HardDiskDrive, Path: `D:\VMs\boot.vhdx`,
			ControllerNumber: 0, ControllerLocation: -1,
		}}, nil, dvdRefs, diskRefs)
		if err != nil {
			t.Fatalf("resolveBootSourceRefs: %v", err)
		}
		if len(got) != 1 || got[0] != "REF:"+disk1 {
			t.Errorf("got %+v, want [REF:%s]", got, disk1)
		}
	})

	t.Run("path が一致しなければ明示エラー", func(t *testing.T) {
		_, err := resolveBootSourceRefs(ref, []api.Gen2BootOrder{{
			Type: api.Gen2BootType_HardDiskDrive, Path: `D:\VMs\missing.vhdx`,
			ControllerNumber: -1, ControllerLocation: -1,
		}}, nil, dvdRefs, diskRefs)
		if err == nil {
			t.Fatal("一致するデバイスが無い場合は明示エラーになるべき")
		}
	})

	t.Run("何も指定が無ければ明示エラー", func(t *testing.T) {
		// 1 台しか無くても黙って選ばない。何を指しているのか決まっていない指定なので、
		// 推測で 1 台を掴むより PS へ委譲させる。
		_, err := resolveBootSourceRefs(ref, []api.Gen2BootOrder{{
			Type:             api.Gen2BootType_DvdDrive,
			ControllerNumber: -1, ControllerLocation: -1,
		}}, nil, dvdRefs, diskRefs)
		if err == nil {
			t.Fatal("path も controller も未指定なら明示エラーになるべき")
		}
	})

	t.Run("複数一致は明示エラー", func(t *testing.T) {
		dup := []hardDiskDriveRef{
			{driveInstanceID: "a", drive: api.VmHardDiskDrive{
				ControllerNumber: 0, ControllerLocation: 0, Path: `D:\VMs\same.vhdx`}},
			{driveInstanceID: "b", drive: api.VmHardDiskDrive{
				ControllerNumber: 1, ControllerLocation: 0, Path: `D:\VMs\same.vhdx`}},
		}
		_, err := resolveBootSourceRefs(ref, []api.Gen2BootOrder{{
			Type: api.Gen2BootType_HardDiskDrive, Path: `D:\VMs\same.vhdx`,
			ControllerNumber: -1, ControllerLocation: -1,
		}}, nil, nil, dup)
		if err == nil {
			t.Fatal("複数一致は一意に特定できないので明示エラーになるべき")
		}
		if !strings.Contains(err.Error(), "複数") {
			t.Errorf("曖昧さが分かるエラーでない: %v", err)
		}
	})

	t.Run("両方指定の既存経路は変わらない (path を無視する)", func(t *testing.T) {
		// controller 位置が合っていれば、path の表記が違っても従来どおり解決する。
		// ここを変えると既に動いている config を壊す。
		got, err := resolveBootSourceRefs(ref, []api.Gen2BootOrder{{
			Type: api.Gen2BootType_HardDiskDrive, Path: `D:\VMs\stale-name.vhdx`,
			ControllerNumber: 0, ControllerLocation: 1,
		}}, nil, dvdRefs, diskRefs)
		if err != nil {
			t.Fatalf("resolveBootSourceRefs: %v", err)
		}
		if len(got) != 1 || got[0] != "REF:"+disk1 {
			t.Errorf("got %+v, want [REF:%s]", got, disk1)
		}
	})
}
