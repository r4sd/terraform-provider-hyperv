//go:build integration
// +build integration

package provider

// go-wsman 経由の VM CRUD (Phase C-1) の実機統合テスト。
//
// 実 Hyper-V ホストに対して Create→Exists→Get→Update→Get→Delete→Exists の一周を回し、
// CIM (DefineSystem / ModifySystemSettings / DestroySystem 等) 経路が PowerShell 版と
// 同等に機能することを検証する。ユニットテストでは WsmanClient が具象型のため検証できない
// オーケストレーション全体 (Phase D) を実機で確認するのが目的。
//
// integration タグ + 接続用環境変数が揃ったときのみ実行される (未設定なら Skip)。
//
// 実行例:
//
//	HYPERV_HOST=10.0.0.100 HYPERV_USER=terraform HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	go test -tags integration ./internal/provider/ -run TestRealHostVmCrudWsman -v

import (
	"context"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"
)

func TestRealHostVmCrudWsman(t *testing.T) {
	c := realHostConfigFromEnv(t)
	wsmanClient, err := newWsmanClient(c)
	if err != nil {
		t.Fatalf("newWsmanClient: %v", err)
	}
	// CRUD メソッドは WsmanClient のみ使う (winrm 埋め込みは不要)。
	cc := &hyperv_wsman.ClientConfig{WsmanClient: wsmanClient}
	ctx := context.Background()

	const (
		name   = "tf-wsman-crud-test"
		memByt = 536870912 // 512 MiB (起動時メモリ)
	)

	// 後始末: 失敗時も含めテスト VM を確実に削除 (DeleteVm は不在で冪等)。
	t.Cleanup(func() {
		if err := cc.DeleteVm(ctx, name); err != nil {
			t.Logf("cleanup DeleteVm: %v", err)
		}
	})
	// 前回の残骸があれば先に消す。
	_ = cc.DeleteVm(ctx, name)

	// --- 1. Create ---
	if err := cc.CreateVm(ctx, name,
		"",                            // path (既定の場所)
		2,                             // generation (Gen2)
		api.CriticalErrorAction_Pause, // automaticCriticalErrorAction
		0,                             // automaticCriticalErrorActionTimeout
		api.StartAction_Nothing,       // automaticStartAction
		0,                             // automaticStartDelay
		api.StopAction_Save,           // automaticStopAction
		api.CheckpointType_Production, // checkpointType (未適用)
		false,                         // dynamicMemory
		false,                         // guestControlledCacheTypes
		0,                             // highMemoryMappedIoSpace
		api.OnOffState_Off,            // lockOnDisconnect
		0,                             // lowMemoryMappedIoSpace
		memByt,                        // memoryMaximumBytes
		memByt,                        // memoryMinimumBytes
		memByt,                        // memoryStartupBytes
		"phase-d-create",              // notes
		1,                             // processorCount
		"",                            // smartPagingFilePath
		"",                            // snapshotFileLocation
		true,                          // staticMemory
		false,                         // automaticCheckpointsEnabled
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}
	t.Logf("CreateVm OK: %s", name)

	// --- 2. Exists → true ---
	ex, err := cc.VmExists(ctx, name)
	if err != nil {
		t.Fatalf("VmExists(after create): %v", err)
	}
	if !ex.Exists {
		t.Fatal("VmExists after Create = false, want true")
	}

	// --- 3. Get → 構成検証 (Generation / Notes / enum) ---
	vm, err := cc.GetVm(ctx, name)
	if err != nil {
		t.Fatalf("GetVm: %v", err)
	}
	if vm.Generation != 2 {
		t.Errorf("Generation = %d, want 2", vm.Generation)
	}
	if vm.Notes != "phase-d-create" {
		t.Errorf("Notes = %q, want phase-d-create", vm.Notes)
	}
	if vm.AutomaticStartAction != api.StartAction_Nothing {
		t.Errorf("AutomaticStartAction = %v, want Nothing", vm.AutomaticStartAction)
	}
	t.Logf("GetVm OK: gen=%d notes=%q start=%v stop=%v crit=%v",
		vm.Generation, vm.Notes, vm.AutomaticStartAction, vm.AutomaticStopAction, vm.AutomaticCriticalErrorAction)

	// --- 4. Update → VM-level / Memory / Processor を変更 ---
	if err := cc.UpdateVm(ctx, name,
		api.CriticalErrorAction_Pause, // (None=0 はゼロ値省略で変更不可のため Pause 維持)
		0,
		api.StartAction_Start, // 変更: Nothing → Start
		0,
		api.StopAction_ShutDown, // 変更: Save → ShutDown
		api.CheckpointType_Production,
		false,
		false,
		0,
		api.OnOffState_On, // 変更: lockOnDisconnect Off → On
		0,
		memByt, memByt, memByt,
		"phase-d-update", // 変更: Notes
		2,                // 変更: processorCount 1 → 2
		"", "",
		true,
		false,
	); err != nil {
		t.Fatalf("UpdateVm: %v", err)
	}

	// --- 5. Get → 更新反映を検証 ---
	vm2, err := cc.GetVm(ctx, name)
	if err != nil {
		t.Fatalf("GetVm(after update): %v", err)
	}
	if vm2.Notes != "phase-d-update" {
		t.Errorf("Notes after Update = %q, want phase-d-update", vm2.Notes)
	}
	if vm2.AutomaticStartAction != api.StartAction_Start {
		t.Errorf("AutomaticStartAction after Update = %v, want Start", vm2.AutomaticStartAction)
	}
	if vm2.AutomaticStopAction != api.StopAction_ShutDown {
		t.Errorf("AutomaticStopAction after Update = %v, want ShutDown", vm2.AutomaticStopAction)
	}
	if vm2.LockOnDisconnect != api.OnOffState_On {
		t.Errorf("LockOnDisconnect after Update = %v, want On", vm2.LockOnDisconnect)
	}
	t.Logf("UpdateVm OK: notes=%q start=%v stop=%v lock=%v",
		vm2.Notes, vm2.AutomaticStartAction, vm2.AutomaticStopAction, vm2.LockOnDisconnect)

	// --- 6. Delete → Exists false ---
	if err := cc.DeleteVm(ctx, name); err != nil {
		t.Fatalf("DeleteVm: %v", err)
	}
	ex2, err := cc.VmExists(ctx, name)
	if err != nil {
		t.Fatalf("VmExists(after delete): %v", err)
	}
	if ex2.Exists {
		t.Error("VmExists after Delete = true, want false")
	}
	t.Logf("DeleteVm OK: %s removed", name)
}
