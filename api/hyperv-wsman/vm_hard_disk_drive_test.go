package hyperv_wsman

import (
	"reflect"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestClientConfig_ImplementsHypervVmHardDiskDriveClient は ClientConfig が
// api.HypervVmHardDiskDriveClient を実装し、全メソッドが本パッケージで
// シャドウイングされていることを検証する。
func TestClientConfig_ImplementsHypervVmHardDiskDriveClient(t *testing.T) {
	var c *ClientConfig
	var _ api.HypervVmHardDiskDriveClient = c // コンパイル時チェック

	cType := reflect.TypeOf((*ClientConfig)(nil))
	for _, methodName := range []string{
		"CreateVmHardDiskDrive",
		"GetVmHardDiskDrives",
		"UpdateVmHardDiskDrive",
		"DeleteVmHardDiskDrive",
		"CreateOrUpdateVmHardDiskDrives",
	} {
		if _, ok := cType.MethodByName(methodName); !ok {
			t.Errorf("メソッド %s が hyperv-wsman で定義されていない (シャドウイングされない)", methodName)
		}
	}
}

func TestWsmanControllerType(t *testing.T) {
	tests := []struct {
		name    string
		in      api.ControllerType
		want    hyperv.ControllerType
		wantErr bool
	}{
		{"IDE", api.ControllerType_Ide, hyperv.ControllerTypeIDE, false},
		{"SCSI", api.ControllerType_Scsi, hyperv.ControllerTypeSCSI, false},
		{"unknown", api.ControllerType(99), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := wsmanControllerType(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err: got %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestUnsupportedHardDiskOptions は既定値なら nil、既定外の未対応オプションでは error を返すこと。
func TestUnsupportedHardDiskOptions(t *testing.T) {
	// スキーマ既定値 = すべて「未設定」= サポート対象。
	defaults := func() (uint32, string, bool, uint64, uint64, string, api.CacheAttributes) {
		return hardDiskDiskNumberUnset, hardDiskDefaultPool, false, 0, 0, hardDiskZeroQosPolicyGUID, api.CacheAttributes_Default
	}

	t.Run("既定値は許可", func(t *testing.T) {
		dn, rp, spr, maxio, minio, qos, cache := defaults()
		if err := unsupportedHardDiskOptions(dn, rp, spr, maxio, minio, qos, cache); err != nil {
			t.Errorf("既定値でエラー: %v", err)
		}
	})
	t.Run("空のプール名も許可", func(t *testing.T) {
		if err := unsupportedHardDiskOptions(hardDiskDiskNumberUnset, "", false, 0, 0, hardDiskZeroQosPolicyGUID, api.CacheAttributes_Default); err != nil {
			t.Errorf("空プール名でエラー: %v", err)
		}
	})

	unsupportedCases := []struct {
		name  string
		apply func(*uint32, *string, *bool, *uint64, *uint64, *string, *api.CacheAttributes)
	}{
		{"passthrough disk", func(dn *uint32, _ *string, _ *bool, _ *uint64, _ *uint64, _ *string, _ *api.CacheAttributes) { *dn = 3 }},
		{"custom pool", func(_ *uint32, rp *string, _ *bool, _ *uint64, _ *uint64, _ *string, _ *api.CacheAttributes) {
			*rp = "MyPool"
		}},
		{"persistent reservations", func(_ *uint32, _ *string, spr *bool, _ *uint64, _ *uint64, _ *string, _ *api.CacheAttributes) {
			*spr = true
		}},
		{"max iops", func(_ *uint32, _ *string, _ *bool, maxio *uint64, _ *uint64, _ *string, _ *api.CacheAttributes) {
			*maxio = 500
		}},
		{"min iops", func(_ *uint32, _ *string, _ *bool, _ *uint64, minio *uint64, _ *string, _ *api.CacheAttributes) {
			*minio = 100
		}},
		{"qos policy", func(_ *uint32, _ *string, _ *bool, _ *uint64, _ *uint64, qos *string, _ *api.CacheAttributes) {
			*qos = "11111111-0000-0000-0000-000000000000"
		}},
		{"cache attr", func(_ *uint32, _ *string, _ *bool, _ *uint64, _ *uint64, _ *string, cache *api.CacheAttributes) {
			*cache = api.CacheAttributes_WriteCacheEnabled
		}},
	}
	for _, tc := range unsupportedCases {
		t.Run(tc.name, func(t *testing.T) {
			dn, rp, spr, maxio, minio, qos, cache := defaults()
			tc.apply(&dn, &rp, &spr, &maxio, &minio, &qos, &cache)
			if err := unsupportedHardDiskOptions(dn, rp, spr, maxio, minio, qos, cache); err == nil {
				t.Errorf("%s は未対応なのでエラーになるべき", tc.name)
			}
		})
	}
}

// TestMapHardDiskDriveRefs は storage→drive→controller の逆引き結合を検証する。
func TestMapHardDiskDriveRefs(t *testing.T) {
	vm := "11111111-aaaa-bbbb-cccc-000000000001"
	scsiCtrl := &hyperv.Msvm_ResourceAllocationSettingData{InstanceID: `Microsoft:` + vm + `\SCSI-CTRL-0`}
	ideCtrl := &hyperv.Msvm_ResourceAllocationSettingData{InstanceID: `Microsoft:` + vm + `\IDE-CTRL-0`}

	// SCSI 位置5 の VHD と IDE 位置0 の VHD。golden 相当のデータを手組みする。
	scsiDrive := &hyperv.Msvm_ResourceAllocationSettingData{
		InstanceID: `Microsoft:` + vm + `\DISK-SCSI`, Parent: scsiCtrl.InstanceID, AddressOnParent: "5",
	}
	ideDrive := &hyperv.Msvm_ResourceAllocationSettingData{
		InstanceID: `Microsoft:` + vm + `\DISK-IDE`, Parent: ideCtrl.InstanceID, AddressOnParent: "0",
	}
	storages := []*hyperv.Msvm_StorageAllocationSettingData{
		{ResourceSubType: hyperv.ResourceSubTypeVirtualHardDisk, HostResource: `D:\VMs\boot.vhdx`, Parent: scsiDrive.InstanceID},
		{ResourceSubType: hyperv.ResourceSubTypeVirtualHardDisk, HostResource: `D:\VMs\data.vhdx`, Parent: ideDrive.InstanceID},
		// DVD/ISO は対象外 (除外されること)
		{ResourceSubType: hyperv.ResourceSubTypeVirtualCDDVDDisk, HostResource: `D:\ISOs\x.iso`, Parent: ideDrive.InstanceID},
	}

	got := mapHardDiskDriveRefs(vm, storages,
		[]*hyperv.Msvm_ResourceAllocationSettingData{scsiDrive, ideDrive},
		[]*hyperv.Msvm_ResourceAllocationSettingData{ideCtrl},
		[]*hyperv.Msvm_ResourceAllocationSettingData{scsiCtrl},
	)

	// VHD 2 本 (ISO 除外)。ソート順: IDE(0) が SCSI(1) より前。
	if len(got) != 2 {
		t.Fatalf("len: got %d, want 2 (ISO は除外)", len(got))
	}
	if got[0].drive.ControllerType != api.ControllerType_Ide || got[0].drive.Path != `D:\VMs\data.vhdx` {
		t.Errorf("got[0] want IDE/data.vhdx, got %v/%q", got[0].drive.ControllerType, got[0].drive.Path)
	}
	if got[1].drive.ControllerType != api.ControllerType_Scsi || got[1].drive.ControllerLocation != 5 {
		t.Errorf("got[1] want SCSI/loc5, got %v/%d", got[1].drive.ControllerType, got[1].drive.ControllerLocation)
	}
	if got[1].driveInstanceID != scsiDrive.InstanceID {
		t.Errorf("driveInstanceID: got %q, want %q", got[1].driveInstanceID, scsiDrive.InstanceID)
	}
	// 既定値 (unset sentinel) が埋まっていること。
	if got[0].drive.DiskNumber != hardDiskDiskNumberUnset || got[0].drive.ResourcePoolName != hardDiskDefaultPool {
		t.Errorf("既定値が未設定: DiskNumber=%d Pool=%q", got[0].drive.DiskNumber, got[0].drive.ResourcePoolName)
	}
}

// TestMapHardDiskDriveRefs_UnresolvedSkipped は親 Drive/Controller が特定できない storage をスキップ。
func TestMapHardDiskDriveRefs_UnresolvedSkipped(t *testing.T) {
	got := mapHardDiskDriveRefs("vm",
		[]*hyperv.Msvm_StorageAllocationSettingData{
			{ResourceSubType: hyperv.ResourceSubTypeVirtualHardDisk, HostResource: `D:\x.vhdx`, Parent: `Microsoft:vm\NO-SUCH-DRIVE`},
		},
		nil, nil, nil,
	)
	if len(got) != 0 {
		t.Errorf("親不明の storage はスキップされるべき; got %d", len(got))
	}
}

// TestPlanHardDiskDriveReconcile は集合差分の detach/attach 計画を検証する。
func TestPlanHardDiskDriveReconcile(t *testing.T) {
	mkRef := func(id string, ct api.ControllerType, num, loc int32, path string) hardDiskDriveRef {
		return hardDiskDriveRef{
			driveInstanceID: id,
			drive:           api.VmHardDiskDrive{ControllerType: ct, ControllerNumber: num, ControllerLocation: loc, Path: path},
		}
	}
	mkD := func(ct api.ControllerType, num, loc int32, path string) api.VmHardDiskDrive {
		return api.VmHardDiskDrive{ControllerType: ct, ControllerNumber: num, ControllerLocation: loc, Path: path}
	}

	t.Run("変化なし = 何もしない", func(t *testing.T) {
		cur := []hardDiskDriveRef{mkRef("d1", api.ControllerType_Scsi, 0, 0, `D:\a.vhdx`)}
		des := []api.VmHardDiskDrive{mkD(api.ControllerType_Scsi, 0, 0, `D:\a.vhdx`)}
		detach, attach := planHardDiskDriveReconcile(cur, des)
		if len(detach) != 0 || len(attach) != 0 {
			t.Errorf("変化なしのはず: detach=%v attach=%v", detach, attach)
		}
	})
	t.Run("パス大小違いは同一扱い", func(t *testing.T) {
		cur := []hardDiskDriveRef{mkRef("d1", api.ControllerType_Scsi, 0, 0, `D:\Boot.vhdx`)}
		des := []api.VmHardDiskDrive{mkD(api.ControllerType_Scsi, 0, 0, `d:\boot.vhdx`)}
		detach, attach := planHardDiskDriveReconcile(cur, des)
		if len(detach) != 0 || len(attach) != 0 {
			t.Errorf("大小違いは同一のはず: detach=%v attach=%v", detach, attach)
		}
	})
	t.Run("追加と削除", func(t *testing.T) {
		cur := []hardDiskDriveRef{mkRef("old", api.ControllerType_Ide, 0, 0, `D:\old.vhdx`)}
		des := []api.VmHardDiskDrive{mkD(api.ControllerType_Scsi, 0, 0, `D:\new.vhdx`)}
		detach, attach := planHardDiskDriveReconcile(cur, des)
		if len(detach) != 1 || detach[0] != "old" {
			t.Errorf("detach: got %v, want [old]", detach)
		}
		if len(attach) != 1 || attach[0].Path != `D:\new.vhdx` {
			t.Errorf("attach: got %v", attach)
		}
	})
	t.Run("Controller種別変更 = detach+attach", func(t *testing.T) {
		// 同じパスでも IDE→SCSI の移動は key が変わるので付け替え。
		cur := []hardDiskDriveRef{mkRef("ide0", api.ControllerType_Ide, 0, 0, `D:\boot.vhdx`)}
		des := []api.VmHardDiskDrive{mkD(api.ControllerType_Scsi, 0, 0, `D:\boot.vhdx`)}
		detach, attach := planHardDiskDriveReconcile(cur, des)
		if len(detach) != 1 || len(attach) != 1 {
			t.Errorf("種別変更は detach+attach: detach=%v attach=%v", detach, attach)
		}
	})
}
