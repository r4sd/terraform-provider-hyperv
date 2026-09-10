package hyperv_wsman

import (
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
)

// TestCheckpointFromSettingData は CIM → api.VmCheckpoint の写像を検証する。
//
// 対応は 2026-09-10 の実機ダンプで確定した。特に **ConfigurationID は
// スナップショット固有の GUID で、VM GUID (VirtualSystemIdentifier) とは別物**。
// fixture でこの 2 つを意図的に別値にしてあるので、取り違えるとテストが落ちる。
func TestCheckpointFromSettingData(t *testing.T) {
	const (
		vmGUID   = "11111111-aaaa-bbbb-cccc-000000000001"
		snapGUID = "33333333-aaaa-bbbb-cccc-000000000011"
		parGUID  = "44444444-aaaa-bbbb-cccc-000000000012"
	)
	sd := &hyperv.Msvm_VirtualSystemSettingData{
		InstanceID:              "Microsoft:" + snapGUID,
		ElementName:             "nightly",
		VirtualSystemIdentifier: vmGUID,
		VirtualSystemType:       hyperv.VirtualSystemTypeSnapshotRealized,
		ConfigurationID:         snapGUID,
		CreationTime:            "2026-09-09T16:20:31.444762Z",
		UserSnapshotType:        hyperv.UserSnapshotTypeTest, // 5 = Standard
		Parent:                  `\\HOST\root\virtualization\v2:Msvm_VirtualSystemSettingData.InstanceID="Microsoft:` + parGUID + `"`,
	}

	got, err := checkpointFromSettingData("vm-1", sd)
	if err != nil {
		t.Fatalf("checkpointFromSettingData: %v", err)
	}
	if got.VmName != "vm-1" {
		t.Errorf("VmName = %q", got.VmName)
	}
	if got.Name != "nightly" {
		t.Errorf("Name = %q, want nightly", got.Name)
	}
	if got.Id != snapGUID {
		t.Errorf("Id = %q, want %q (スナップショット GUID)", got.Id, snapGUID)
	}
	if got.Id == vmGUID {
		t.Error("Id に VM GUID が入っている。PS の Get-VMSnapshot.Id はスナップショット固有の GUID")
	}
	if got.ParentId != parGUID {
		t.Errorf("ParentId = %q, want %q", got.ParentId, parGUID)
	}
	if got.CreationTime != "2026-09-09T16:20:31.444762Z" {
		t.Errorf("CreationTime = %q", got.CreationTime)
	}
	if got.CheckpointType != "Standard" {
		t.Errorf("CheckpointType = %q, want Standard (UserSnapshotType=5)", got.CheckpointType)
	}

	// 親が無いチェックポイント (ツリーの根) は ParentId が空。
	sd.Parent = ""
	got, err = checkpointFromSettingData("vm-1", sd)
	if err != nil {
		t.Fatalf("checkpointFromSettingData (no parent): %v", err)
	}
	if got.ParentId != "" {
		t.Errorf("親なしの ParentId = %q, want 空", got.ParentId)
	}

	// 未知の UserSnapshotType は黙って通さない。
	sd.UserSnapshotType = 99
	if _, err := checkpointFromSettingData("vm-1", sd); err == nil {
		t.Error("未知の UserSnapshotType はエラーになるべき")
	}
}

// TestFindVmCheckpointByName は名前引きの曖昧さを明示的に扱うことを検証する。
//
// Hyper-V の既定名は秒精度のため、同一秒に作ったチェックポイントは ElementName が
// 重複する (2026-09-10 実機確認)。複数一致で黙って先頭を返すと、誤ったチェックポイントを
// 削除/復元する事故になる。
func TestFindVmCheckpointByName(t *testing.T) {
	cps := []*hyperv.Msvm_VirtualSystemSettingData{
		{ElementName: "a", ConfigurationID: "id-a", InstanceID: "Microsoft:id-a"},
		{ElementName: "dup", ConfigurationID: "id-b", InstanceID: "Microsoft:id-b"},
		{ElementName: "dup", ConfigurationID: "id-c", InstanceID: "Microsoft:id-c"},
	}

	t.Run("1 件一致", func(t *testing.T) {
		got, err := findVmCheckpointByName(cps, "a")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got == nil || got.ConfigurationID != "id-a" {
			t.Errorf("got %+v, want id-a", got)
		}
	})

	t.Run("不一致は (nil, nil)", func(t *testing.T) {
		got, err := findVmCheckpointByName(cps, "none")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})

	t.Run("複数一致はエラー", func(t *testing.T) {
		got, err := findVmCheckpointByName(cps, "dup")
		if err == nil {
			t.Fatal("複数一致はエラーになるべき (黙って先頭を返さない)")
		}
		if got != nil {
			t.Errorf("エラー時は nil を返すべき: %+v", got)
		}
	})
}

// TestNewCheckpointInstanceID は作成前後の差分から新規 1 件を特定できることを検証する。
//
// CreateSnapshot の ResultingSnapshot は非同期時に空を返す (go-wsman #125) ため、
// 差分で特定するしかない。増分が 1 件でない場合は黙って先頭を取らずエラーにする。
func TestNewCheckpointInstanceID(t *testing.T) {
	mk := func(ids ...string) []*hyperv.Msvm_VirtualSystemSettingData {
		out := make([]*hyperv.Msvm_VirtualSystemSettingData, 0, len(ids))
		for _, id := range ids {
			out = append(out, &hyperv.Msvm_VirtualSystemSettingData{InstanceID: id})
		}
		return out
	}
	cases := []struct {
		name    string
		before  []*hyperv.Msvm_VirtualSystemSettingData
		after   []*hyperv.Msvm_VirtualSystemSettingData
		want    string
		wantErr bool
	}{
		{"0 件 → 1 件", mk(), mk("x"), "x", false},
		{"1 件 → 2 件", mk("x"), mk("x", "y"), "y", false},
		{"増分なし", mk("x"), mk("x"), "", true},
		{"増分 2 件 (並行作成)", mk("x"), mk("x", "y", "z"), "", true},
		{"減っている", mk("x", "y"), mk("x"), "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := newCheckpointInstanceID(tc.before, tc.after)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("= %q, want %q", got, tc.want)
			}
		})
	}
}

// TestVmCheckpointShadowed は 4 メソッドが本パッケージで実際にシャドウされていることを
// 機械的に検証する。埋め込み promotion で PS 実装が呼ばれていると移行が完了していない。
func TestVmCheckpointShadowed(t *testing.T) {
	for _, m := range []string{"CreateVmCheckpoint", "GetVmCheckpoint", "DeleteVmCheckpoint", "RestoreVmCheckpoint"} {
		assertShadowedIn(t, m, "vm_checkpoint.go")
	}
}
