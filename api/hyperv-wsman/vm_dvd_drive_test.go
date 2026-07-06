package hyperv_wsman

import (
	"reflect"
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

	cType := reflect.TypeOf((*ClientConfig)(nil))
	for _, methodName := range []string{
		"CreateVmDvdDrive",
		"GetVmDvdDrives",
		"UpdateVmDvdDrive",
		"DeleteVmDvdDrive",
		"CreateOrUpdateVmDvdDrives",
	} {
		if _, ok := cType.MethodByName(methodName); !ok {
			t.Errorf("メソッド %s が hyperv-wsman で定義されていない (シャドウイングされない)", methodName)
		}
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
