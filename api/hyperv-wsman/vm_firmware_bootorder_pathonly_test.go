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

	// ⚠️ 上の「片方だけ -1」だけでは controller フィルタを固定できない。
	// fixture 内で path が既に一意なので、フィルタを外しても同じ 1 台に落ちてしまう
	// (実際に変異が 2 本生き残った)。**同じ path を別の controller 位置に 2 台置き、
	// 各フィルタが個別に必要になる形**にして初めて固定できる。
	t.Run("片側指定: 各 controller フィルタが個別に効く", func(t *testing.T) {
		const samePath = `D:\VMs\same.vhdx`
		same := []hardDiskDriveRef{
			{driveInstanceID: "ctrl0-loc0", drive: api.VmHardDiskDrive{
				ControllerNumber: 0, ControllerLocation: 0, Path: samePath}},
			{driveInstanceID: "ctrl1-loc1", drive: api.VmHardDiskDrive{
				ControllerNumber: 1, ControllerLocation: 1, Path: samePath}},
		}

		cases := []struct {
			name   string
			order  api.Gen2BootOrder
			wantID string
		}{
			{
				// number フィルタを外すと 2 台一致 → 曖昧エラーになる
				name: "number のみ指定 + path",
				order: api.Gen2BootOrder{
					Type: api.Gen2BootType_HardDiskDrive, Path: samePath,
					ControllerNumber: 1, ControllerLocation: -1},
				wantID: "ctrl1-loc1",
			},
			{
				// location フィルタを外すと 2 台一致 → 曖昧エラーになる
				name: "location のみ指定 + path",
				order: api.Gen2BootOrder{
					Type: api.Gen2BootType_HardDiskDrive, Path: samePath,
					ControllerNumber: -1, ControllerLocation: 0},
				wantID: "ctrl0-loc0",
			},
			{
				// path を書かず controller 片側だけで絞るケース
				name: "number のみ指定 (path なし)",
				order: api.Gen2BootOrder{
					Type:             api.Gen2BootType_HardDiskDrive,
					ControllerNumber: 1, ControllerLocation: -1},
				wantID: "ctrl1-loc1",
			},
			{
				name: "location のみ指定 (path なし)",
				order: api.Gen2BootOrder{
					Type:             api.Gen2BootType_HardDiskDrive,
					ControllerNumber: -1, ControllerLocation: 0},
				wantID: "ctrl0-loc0",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, err := resolveBootSourceRefs(ref, []api.Gen2BootOrder{tc.order}, nil, nil, same)
				if err != nil {
					t.Fatalf("resolveBootSourceRefs: %v", err)
				}
				if len(got) != 1 || got[0] != "REF:"+tc.wantID {
					t.Errorf("got %+v, want [REF:%s]", got, tc.wantID)
				}
			})
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

	t.Run("-1 以外の負値も未指定として扱う (PS の -gt -1 と同じ)", func(t *testing.T) {
		// schema に負値のバリデーションが無いので -2 も書けてしまう。
		// == で比較すると CIM 経路だけ「指定」扱いになり、同じ config が経路で違う結果になる。
		got, err := resolveBootSourceRefs(ref, []api.Gen2BootOrder{{
			Type: api.Gen2BootType_HardDiskDrive, Path: `D:\VMs\data.vhdx`,
			ControllerNumber: -2, ControllerLocation: -5,
		}}, nil, dvdRefs, diskRefs)
		if err != nil {
			t.Fatalf("resolveBootSourceRefs: %v", err)
		}
		if len(got) != 1 || got[0] != "REF:"+disk2 {
			t.Errorf("got %+v, want [REF:%s]", got, disk2)
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
