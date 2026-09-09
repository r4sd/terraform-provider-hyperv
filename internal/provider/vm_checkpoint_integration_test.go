//go:build integration
// +build integration

package provider

// #115 の実機テスト: vm_checkpoint の go-wsman shadow を CRUD 一巡で検証する。
//
// 実行例:
//
//	HYPERV_HOST=<hyperv-host> HYPERV_USER=<user> HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	HYPERV_TEST_ALLOW_MUTATION=1 \
//	go test -tags integration ./internal/provider/ -run TestRealHostVmCheckpointWsman -v

import (
	"context"
	"os"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

func TestRealHostVmCheckpointWsman(t *testing.T) {
	if os.Getenv("HYPERV_TEST_ALLOW_MUTATION") == "" {
		t.Skip("HYPERV_TEST_ALLOW_MUTATION 未設定（VM 作成を伴う破壊的テスト）")
	}
	c := realHostConfigFromEnv(t)
	cc := newRealHostWsmanClientConfig(t, c)
	ctx := context.Background()

	const (
		vmName    = "tf-wsman-ckpt-crud"
		cpName    = "tf-checkpoint-alpha"
		cpName2   = "tf-checkpoint-beta"
		memByt    = 536870912
		defaultVM = `C:\ProgramData\Microsoft\Windows\Hyper-V`
	)
	_ = cc.DeleteVm(ctx, vmName)
	t.Cleanup(func() {
		_ = cc.DeleteVmCheckpoint(ctx, vmName, cpName)
		_ = cc.DeleteVmCheckpoint(ctx, vmName, cpName2)
		if err := cc.DeleteVm(ctx, vmName); err != nil {
			t.Logf("cleanup DeleteVm: %v", err)
		}
	})

	if err := cc.CreateVm(ctx, vmName,
		"", 1,
		api.CriticalErrorAction_Pause, 30,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 536870912,
		api.OnOffState_Off, 134217728,
		memByt, memByt, memByt,
		"ckpt-crud", 1,
		defaultVM, defaultVM, true, true,
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}

	// --- 1. 不在時は空を返す (PS 版が空 JSON を返す挙動とのパリティ) ---
	if got, err := cc.GetVmCheckpoint(ctx, vmName, cpName); err != nil {
		t.Fatalf("GetVmCheckpoint (不在): %v", err)
	} else if got.Name != "" {
		t.Fatalf("🔴 不在のはずが %+v", got)
	}
	t.Logf("① 不在時は空を返す")

	// --- 2. 作成 → 指定名になっていること (リネームの黙殺検出) ---
	if err := cc.CreateVmCheckpoint(ctx, vmName, cpName); err != nil {
		t.Fatalf("CreateVmCheckpoint: %v", err)
	}
	got, err := cc.GetVmCheckpoint(ctx, vmName, cpName)
	if err != nil {
		t.Fatalf("GetVmCheckpoint: %v", err)
	}
	t.Logf("② 作成後: Name=%q Id=%q ParentId=%q Type=%q CreationTime=%q",
		got.Name, got.Id, got.ParentId, got.CheckpointType, got.CreationTime)
	if got.Name != cpName {
		t.Fatalf("🔴 リネームが反映されていない: %q", got.Name)
	}
	if got.Id == "" {
		t.Fatal("🔴 Id が空")
	}
	if got.CreationTime == "" {
		t.Fatal("🔴 CreationTime が空")
	}
	if got.CheckpointType == "" {
		t.Fatal("🔴 CheckpointType が空")
	}

	// --- 3. 2 つ目を作ると親子関係が付くこと (ParentId の実機検証) ---
	if err := cc.CreateVmCheckpoint(ctx, vmName, cpName2); err != nil {
		t.Fatalf("CreateVmCheckpoint (2つ目): %v", err)
	}
	got2, err := cc.GetVmCheckpoint(ctx, vmName, cpName2)
	if err != nil {
		t.Fatalf("GetVmCheckpoint (2つ目): %v", err)
	}
	t.Logf("③ 2つ目: Name=%q Id=%q ParentId=%q", got2.Name, got2.Id, got2.ParentId)
	if got2.ParentId != got.Id {
		t.Fatalf("🔴 ParentId が 1 つ目の Id と一致しない: got %q, want %q", got2.ParentId, got.Id)
	}
	t.Logf("🎯 ParentId が親チェックポイントの Id を正しく指す")

	// --- 4. 同名の再作成は弾く ---
	if err := cc.CreateVmCheckpoint(ctx, vmName, cpName); err == nil {
		t.Fatal("🔴 同名チェックポイントの再作成はエラーになるべき")
	}
	t.Logf("④ 同名の再作成を拒否")

	// --- 5. 復元 ---
	if err := cc.RestoreVmCheckpoint(ctx, vmName, cpName); err != nil {
		t.Fatalf("RestoreVmCheckpoint: %v", err)
	}
	t.Logf("⑤ 復元 OK")

	// --- 6. 削除 → 不在になること ---
	if err := cc.DeleteVmCheckpoint(ctx, vmName, cpName2); err != nil {
		t.Fatalf("DeleteVmCheckpoint: %v", err)
	}
	if after, err := cc.GetVmCheckpoint(ctx, vmName, cpName2); err != nil {
		t.Fatalf("GetVmCheckpoint (削除後): %v", err)
	} else if after.Name != "" {
		t.Fatalf("🔴 削除したのに残っている: %+v", after)
	}
	t.Logf("⑥ 削除 OK")

	// --- 7. 不在の削除は冪等 ---
	if err := cc.DeleteVmCheckpoint(ctx, vmName, cpName2); err != nil {
		t.Fatalf("🔴 不在の削除は冪等であるべき: %v", err)
	}
	t.Logf("🎯 判定: vm_checkpoint の CIM 経路が CRUD 一巡で動作する")
}
