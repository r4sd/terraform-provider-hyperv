package hyperv_wsman

import (
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestClientConfig_ImplementsHypervVmDvdDriveClient は ClientConfig が
// api.HypervVmDvdDriveClient を実装し、全メソッドが本パッケージでシャドウイングされて
// いることを検証する (未シャドウなら PowerShell 経路にフォールバックしてしまう)。
func TestClientConfig_ImplementsHypervVmDvdDriveClient(t *testing.T) {
	var c *ClientConfig
	var _ api.HypervVmDvdDriveClient = c // コンパイル時チェック

	assertAllShadowedIn(t, "vm_dvd_drive.go",
		"CreateVmDvdDrive",
		"GetVmDvdDrives",
		"UpdateVmDvdDrive",
		"DeleteVmDvdDrive",
		"CreateOrUpdateVmDvdDrives",
	)
}

// TestValidateDvdOptions は未対応オプション(空 ISO パス・非既定リソースプール)を破壊操作の
// 前に弾くことを検証する。attach まで遅れると部分適用で state 乖離する (レビュー #66)。
func TestValidateDvdOptions(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		pool    string
		wantErr bool
	}{
		{"正常: ISO+既定プール", `H:\ISO\talos.iso`, "Primordial", false},
		{"正常: プール空文字も既定扱い", `H:\ISO\talos.iso`, "", false},
		// 空メディア (メディアなし DVD ドライブ) は #67 で対応済。弾かない。
		{"正常: 空メディア(パス空)", "", "Primordial", false},
		{"正常: 空メディア+プール空文字", "", "", false},
		{"拒否: 非既定プール", `H:\ISO\talos.iso`, "CustomPool", true},
		{"拒否: 空メディアでも非既定プールは弾く", "", "CustomPool", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDvdOptions(tt.path, tt.pool)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateDvdOptions(%q,%q): err=%v, wantErr=%v", tt.path, tt.pool, err, tt.wantErr)
			}
		})
	}
}

// TestMapDvdDriveRefs は ISO(Virtual CD/DVD Disk)のみ復元し、VHD を除外することを検証する。
func TestMapDvdDriveRefs(t *testing.T) {
	vm := "11111111-aaaa-bbbb-cccc-000000000001"
	scsiCtrl := &hyperv.Msvm_ResourceAllocationSettingData{InstanceID: `Microsoft:` + vm + `\SCSI-CTRL-0`}

	dvdDrive := &hyperv.Msvm_ResourceAllocationSettingData{
		InstanceID: `Microsoft:` + vm + `\DVD-SCSI`, Parent: scsiCtrl.InstanceID, AddressOnParent: "1",
	}
	storages := []*hyperv.Msvm_StorageAllocationSettingData{
		{ResourceSubType: hyperv.ResourceSubTypeVirtualCDDVDDisk, HostResource: `H:\ISO\talos.iso`, Parent: dvdDrive.InstanceID, InstanceID: `Microsoft:` + vm + `\ISO-0`},
		// VHD は対象外 (除外されること)
		{ResourceSubType: hyperv.ResourceSubTypeVirtualHardDisk, HostResource: `D:\VMs\boot.vhdx`, Parent: dvdDrive.InstanceID},
	}

	got := mapDvdDriveRefs(vm, storages,
		[]*hyperv.Msvm_ResourceAllocationSettingData{dvdDrive},
		nil,
		[]*hyperv.Msvm_ResourceAllocationSettingData{scsiCtrl},
	)

	if len(got) != 1 {
		t.Fatalf("len: got %d, want 1 (VHD は除外)", len(got))
	}
	if got[0].dvd.ControllerNumber != 0 || got[0].dvd.ControllerLocation != 1 {
		t.Errorf("controller: got num=%d loc=%d, want num=0 loc=1", got[0].dvd.ControllerNumber, got[0].dvd.ControllerLocation)
	}
	if got[0].dvd.Path != `H:\ISO\talos.iso` {
		t.Errorf("Path: got %q", got[0].dvd.Path)
	}
	// Detach に必要な両 InstanceID が取れていること (#97 の 2 段削除用)。
	if got[0].driveInstanceID != dvdDrive.InstanceID {
		t.Errorf("driveInstanceID: got %q", got[0].driveInstanceID)
	}
	if got[0].storageInstanceID != `Microsoft:`+vm+`\ISO-0` {
		t.Errorf("storageInstanceID: got %q", got[0].storageInstanceID)
	}
	if got[0].dvd.ResourcePoolName != dvdDefaultResourcePool {
		t.Errorf("ResourcePoolName: got %q, want %q", got[0].dvd.ResourcePoolName, dvdDefaultResourcePool)
	}
}

// TestMapDvdDriveRefs_Gen1IDE は Gen1 の IDE 上の DVD で controller 番号が IDE index に
// なることを検証する (homelab controlplane の dvd_drives{controller_number=1} 相当)。
func TestMapDvdDriveRefs_Gen1IDE(t *testing.T) {
	vm := "vm1"
	ide0 := &hyperv.Msvm_ResourceAllocationSettingData{InstanceID: `Microsoft:` + vm + `\IDE-0`}
	ide1 := &hyperv.Msvm_ResourceAllocationSettingData{InstanceID: `Microsoft:` + vm + `\IDE-1`}
	dvdOnIde1 := &hyperv.Msvm_ResourceAllocationSettingData{
		InstanceID: `Microsoft:` + vm + `\DVD-IDE1`, Parent: ide1.InstanceID, AddressOnParent: "0",
	}
	storages := []*hyperv.Msvm_StorageAllocationSettingData{
		{ResourceSubType: hyperv.ResourceSubTypeVirtualCDDVDDisk, HostResource: `H:\ISO\talos.iso`, Parent: dvdOnIde1.InstanceID},
	}
	got := mapDvdDriveRefs(vm, storages,
		[]*hyperv.Msvm_ResourceAllocationSettingData{dvdOnIde1},
		[]*hyperv.Msvm_ResourceAllocationSettingData{ide0, ide1},
		nil,
	)
	if len(got) != 1 {
		t.Fatalf("len: got %d, want 1", len(got))
	}
	if got[0].dvd.ControllerNumber != 1 {
		t.Errorf("ControllerNumber: got %d, want 1 (IDE-1)", got[0].dvd.ControllerNumber)
	}
}

// TestPlanDvdDriveReconcile は集合差分の detach/attach 計画を検証する。
func TestPlanDvdDriveReconcile(t *testing.T) {
	mkRef := func(num, loc int, path string) dvdDriveRef {
		return dvdDriveRef{
			driveInstanceID:   path + "-drive",
			storageInstanceID: path + "-storage",
			dvd:               api.VmDvdDrive{ControllerNumber: num, ControllerLocation: loc, Path: path},
		}
	}
	mkD := func(num, loc int, path string) api.VmDvdDrive {
		return api.VmDvdDrive{ControllerNumber: num, ControllerLocation: loc, Path: path}
	}

	t.Run("変化なし", func(t *testing.T) {
		cur := []dvdDriveRef{mkRef(0, 1, `H:\ISO\talos.iso`)}
		des := []api.VmDvdDrive{mkD(0, 1, `H:\ISO\talos.iso`)}
		detach, attach := planDvdDriveReconcile(cur, des)
		if len(detach) != 0 || len(attach) != 0 {
			t.Errorf("変化なしのはず: detach=%v attach=%v", detach, attach)
		}
	})
	t.Run("パス大小違いは同一", func(t *testing.T) {
		cur := []dvdDriveRef{mkRef(0, 1, `H:\ISO\Talos.iso`)}
		des := []api.VmDvdDrive{mkD(0, 1, `h:\iso\talos.iso`)}
		detach, attach := planDvdDriveReconcile(cur, des)
		if len(detach) != 0 || len(attach) != 0 {
			t.Errorf("大小違いは同一のはず: detach=%v attach=%v", detach, attach)
		}
	})
	t.Run("ISO差し替え = detach+attach", func(t *testing.T) {
		cur := []dvdDriveRef{mkRef(0, 1, `H:\ISO\old.iso`)}
		des := []api.VmDvdDrive{mkD(0, 1, `H:\ISO\new.iso`)}
		detach, attach := planDvdDriveReconcile(cur, des)
		if len(detach) != 1 || detach[0].storageInstanceID != `H:\ISO\old.iso-storage` {
			t.Errorf("detach: got %v", detach)
		}
		if len(attach) != 1 || attach[0].Path != `H:\ISO\new.iso` {
			t.Errorf("attach: got %v", attach)
		}
	})
	t.Run("boot後デタッチ = detachのみ", func(t *testing.T) {
		// Talos boot 後に ISO を外す (desired 空) → detach 1 本、attach なし。
		cur := []dvdDriveRef{mkRef(0, 1, `H:\ISO\talos.iso`)}
		detach, attach := planDvdDriveReconcile(cur, nil)
		if len(detach) != 1 || len(attach) != 0 {
			t.Errorf("boot後デタッチは detach のみ: detach=%v attach=%v", detach, attach)
		}
	})
}

// TestMapDvdDriveRefs_EmptyMedia はメディア無し DVD ドライブが読み取りに現れることを検証する (#67)。
//
// 🔴 **以前は storage(ISO) 起点のループだったため、子 SASD を持たないドライブが
// GetVmDvdDrives に不可視だった。** その状態でゲストが eject したり PS / Hyper-V マネージャーが
// 空ドライブを作ると、reconcile が「現状なし」と誤認して同じ AddressOnParent へ再 attach し、
// VMMS のアドレス衝突で apply が恒久失敗する。空ドライブは provider 経由で detach もできない。
//
// Drive RASD 起点にして storage を left join することで、空ドライブは Path="" で表現される。
func TestMapDvdDriveRefs_EmptyMedia(t *testing.T) {
	vm := "vm1"
	ide0 := &hyperv.Msvm_ResourceAllocationSettingData{InstanceID: `Microsoft:` + vm + `\IDE-0`}
	ide1 := &hyperv.Msvm_ResourceAllocationSettingData{InstanceID: `Microsoft:` + vm + `\IDE-1`}
	// ISO 入り
	withISO := &hyperv.Msvm_ResourceAllocationSettingData{
		InstanceID: `Microsoft:` + vm + `\DVD-A`, Parent: ide0.InstanceID, AddressOnParent: "0",
	}
	// メディア無し (子 SASD を持たない)
	empty := &hyperv.Msvm_ResourceAllocationSettingData{
		InstanceID: `Microsoft:` + vm + `\DVD-B`, Parent: ide1.InstanceID, AddressOnParent: "1",
	}
	storages := []*hyperv.Msvm_StorageAllocationSettingData{
		{ResourceSubType: hyperv.ResourceSubTypeVirtualCDDVDDisk,
			HostResource: `H:\ISO\talos.iso`, Parent: withISO.InstanceID,
			InstanceID: `Microsoft:` + vm + `\ISO-0`},
		// DVD ドライブに紐づく VHD は対象外 (既存ガード)
		{ResourceSubType: hyperv.ResourceSubTypeVirtualHardDisk,
			HostResource: `D:\VMs\boot.vhdx`, Parent: withISO.InstanceID},
	}

	got := mapDvdDriveRefs(vm, storages,
		[]*hyperv.Msvm_ResourceAllocationSettingData{withISO, empty},
		[]*hyperv.Msvm_ResourceAllocationSettingData{ide0, ide1},
		nil,
	)

	if len(got) != 2 {
		t.Fatalf("len: got %d, want 2 (メディア無しも含む)", len(got))
	}

	// (controller 番号, 位置) でソートされるので [0]=IDE-0/0, [1]=IDE-1/1
	if got[0].dvd.ControllerNumber != 0 || got[0].dvd.ControllerLocation != 0 {
		t.Errorf("got[0] の位置: %d/%d, want 0/0", got[0].dvd.ControllerNumber, got[0].dvd.ControllerLocation)
	}
	if got[0].dvd.Path != `H:\ISO\talos.iso` {
		t.Errorf("got[0].Path: got %q, want ISO パス", got[0].dvd.Path)
	}
	if got[0].storageInstanceID == "" {
		t.Error("got[0].storageInstanceID: ISO があるので非空であるべき")
	}

	if got[1].dvd.ControllerNumber != 1 || got[1].dvd.ControllerLocation != 1 {
		t.Errorf("got[1] の位置: %d/%d, want 1/1", got[1].dvd.ControllerNumber, got[1].dvd.ControllerLocation)
	}
	// メディア無しは Path 空。
	if got[1].dvd.Path != "" {
		t.Errorf("got[1].Path: got %q, want \"\" (メディア無し)", got[1].dvd.Path)
	}
	// storage が無いので storageInstanceID も空。DetachStorage はこれで Drive 単独削除に分岐する。
	if got[1].storageInstanceID != "" {
		t.Errorf("got[1].storageInstanceID: got %q, want \"\" (Drive 単独削除に分岐させる)", got[1].storageInstanceID)
	}
	if got[1].driveInstanceID != empty.InstanceID {
		t.Errorf("got[1].driveInstanceID: got %q, want %q", got[1].driveInstanceID, empty.InstanceID)
	}
}

// TestMapDvdDriveRefs_VHDOnDvdDriveIgnored は DVD ドライブに VHD が紐づいている場合、
// それを ISO として読まないことを検証する。
//
// Drive 起点に変えたので「storage の subtype で弾く」ガードが効き続けているかを別途固定する。
// 効いていないと VHD のパスが DVD の Path として state に入る。
func TestMapDvdDriveRefs_VHDOnDvdDriveIgnored(t *testing.T) {
	vm := "vm1"
	ide0 := &hyperv.Msvm_ResourceAllocationSettingData{InstanceID: `Microsoft:` + vm + `\IDE-0`}
	drive := &hyperv.Msvm_ResourceAllocationSettingData{
		InstanceID: `Microsoft:` + vm + `\DVD-A`, Parent: ide0.InstanceID, AddressOnParent: "0",
	}
	storages := []*hyperv.Msvm_StorageAllocationSettingData{
		{ResourceSubType: hyperv.ResourceSubTypeVirtualHardDisk,
			HostResource: `D:\VMs\boot.vhdx`, Parent: drive.InstanceID,
			InstanceID: `Microsoft:` + vm + `\VHD-0`},
	}
	got := mapDvdDriveRefs(vm, storages,
		[]*hyperv.Msvm_ResourceAllocationSettingData{drive},
		[]*hyperv.Msvm_ResourceAllocationSettingData{ide0}, nil)

	if len(got) != 1 {
		t.Fatalf("len: got %d, want 1 (ドライブ自体は見える)", len(got))
	}
	if got[0].dvd.Path != "" {
		t.Errorf("VHD のパスを DVD の Path として読んでいる: %q", got[0].dvd.Path)
	}
	if got[0].storageInstanceID != "" {
		t.Errorf("VHD の storage を紐付けている: %q", got[0].storageInstanceID)
	}
}
