//go:build integration
// +build integration

package provider

// #125 / #134 の実機回帰テスト。
//
// 修正前の挙動:
//   - CreateVm は checkpoint_type を黙って未適用にし、ホスト既定 (Standard) の VM ができる
//   - read は AutomaticCheckpointsEnabled を常に false で返す (未マッピング)
//   - その結果、後続の UpdateVm が validateCheckpointFieldsUnchanged で必ず弾かれる
//
// 実行例:
//
//	HYPERV_HOST=<hyperv-host> HYPERV_USER=<user> HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	go test -tags integration ./internal/provider/ -run TestRealHostCheckpointFields -v

import (
	"context"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

func TestRealHostCheckpointFields(t *testing.T) {
	c := realHostConfigFromEnv(t)
	cc := newRealHostWsmanClientConfig(t, c)
	ctx := context.Background()

	const vmName = "tf-wsman-ckpt-test"
	const memByt = 536870912
	const defaultVMPath = `C:\ProgramData\Microsoft\Windows\Hyper-V`
	_ = cc.DeleteVm(ctx, vmName)
	t.Cleanup(func() {
		if err := cc.DeleteVm(ctx, vmName); err != nil {
			t.Logf("cleanup DeleteVm: %v", err)
		}
	})

	// --- 1. schema 既定 (Production / false) で作成する ---
	if err := cc.CreateVm(ctx, vmName,
		"", 1,
		api.CriticalErrorAction_Pause, 30,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 0,
		api.OnOffState_Off, 0,
		memByt, memByt, memByt,
		"ckpt-test", 1,
		defaultVMPath, defaultVMPath, true, false,
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}

	created, err := cc.GetVm(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVm (created): %v", err)
	}
	t.Logf("① 作成直後: CheckpointType=%v AutomaticCheckpointsEnabled=%v",
		created.CheckpointType, created.AutomaticCheckpointsEnabled)

	// #125: 修正前はホスト既定の Standard になっていた。
	if created.CheckpointType != api.CheckpointType_Production {
		t.Fatalf("🔴 判定: checkpoint_type が反映されていない (got %v, want Production)", created.CheckpointType)
	}
	t.Logf("🎯 checkpoint_type が create で反映される (#125)")

	// #134: read が実値を返すこと。false は CIM で送れないため、ホスト既定がそのまま残る。
	//
	// AutomaticCheckpointsEnabled のホスト既定はクライアント Hyper-V (Windows 10/11) で true、
	// Windows Server では false。true のホストでのみ「false へのダウングレード」を検証できるので、
	// ここで分岐する。false のホストで Fatal にすると偽陽性になる。
	if !created.AutomaticCheckpointsEnabled {
		t.Skip("ホスト既定が false (Windows Server 等) のため、false ダウングレードの検証は不可")
	}
	t.Logf("🎯 read が AutomaticCheckpointsEnabled の実値 (true) を返す (#134)")

	// --- 2. automatic_checkpoints_enabled=false へ更新する ---
	// false はゼロ値で CIM 送信できないため、ゼロ値ダウングレードとして PS へ委譲される。
	if err := cc.UpdateVm(ctx, vmName,
		api.CriticalErrorAction_Pause, 30,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 0,
		api.OnOffState_Off, 0,
		memByt, memByt, memByt,
		"ckpt-test", 1,
		defaultVMPath, defaultVMPath, true, false,
	); err != nil {
		t.Fatalf("UpdateVm: %v", err)
	}

	updated, err := cc.GetVm(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVm (updated): %v", err)
	}
	t.Logf("② 更新後: CheckpointType=%v AutomaticCheckpointsEnabled=%v",
		updated.CheckpointType, updated.AutomaticCheckpointsEnabled)
	if updated.AutomaticCheckpointsEnabled {
		t.Fatalf("🔴 判定: 黙殺。UpdateVm は成功したのに true のまま")
	}
	if updated.CheckpointType != api.CheckpointType_Production {
		t.Fatalf("🔴 判定: 委譲の過程で checkpoint_type が壊れた (got %v)", updated.CheckpointType)
	}
	t.Logf("🎯 判定: automatic_checkpoints_enabled=false が PS 委譲で反映され、checkpoint_type も保たれる")

	// --- 3. checkpoint_type を CIM 経路で変更できること ---
	if err := cc.UpdateVm(ctx, vmName,
		api.CriticalErrorAction_Pause, 30,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Standard, // Production → Standard
		false, false, 0,
		api.OnOffState_Off, 0,
		memByt, memByt, memByt,
		"ckpt-test", 1,
		defaultVMPath, defaultVMPath, true, false,
	); err != nil {
		t.Fatalf("UpdateVm (checkpoint_type): %v", err)
	}
	final, err := cc.GetVm(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVm (final): %v", err)
	}
	t.Logf("③ checkpoint_type 変更後: %v", final.CheckpointType)
	if final.CheckpointType != api.CheckpointType_Standard {
		t.Fatalf("🔴 判定: checkpoint_type の変更が反映されていない (got %v)", final.CheckpointType)
	}
	t.Logf("🎯 判定: checkpoint_type を update で変更できる")
}
