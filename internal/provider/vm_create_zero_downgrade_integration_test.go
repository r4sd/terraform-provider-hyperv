//go:build integration
// +build integration

package provider

// #143 / #141 の実機回帰テスト: create 時のゼロ値が実際に反映されることを検証する。
//
// 修正前は DefineSystem / SetMemorySettings がゼロ値を送れないため、
// static_memory=true と書いても **動的メモリの VM ができていた**。
// read は実値を返すので恒常 diff になり、次の apply で VM 停止を伴って修正される。
//
// 実行例:
//
//	HYPERV_HOST=... HYPERV_TEST_ALLOW_MUTATION=1 \
//	go test -tags integration ./internal/provider/ -run TestRealHostCreateZeroDowngrade -v

import (
	"context"
	"os"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

func TestRealHostCreateZeroDowngrade(t *testing.T) {
	if os.Getenv("HYPERV_TEST_ALLOW_MUTATION") == "" {
		t.Skip("HYPERV_TEST_ALLOW_MUTATION 未設定（VM 作成を伴う破壊的テスト）")
	}
	c := realHostConfigFromEnv(t)
	cc := newRealHostWsmanClientConfig(t, c)
	ctx := context.Background()

	const (
		vmName    = "tf-wsman-create-zero"
		memByt    = 536870912
		defaultVM = `C:\ProgramData\Microsoft\Windows\Hyper-V`
	)
	_ = cc.DeleteVm(ctx, vmName)
	t.Cleanup(func() {
		if err := cc.DeleteVm(ctx, vmName); err != nil {
			t.Logf("cleanup DeleteVm: %v", err)
		}
	})

	// schema 既定に近い「ゼロ値だらけ」の要求で作る。
	//   static_memory = true                   → DynamicMemoryEnabled=false (ゼロ値)
	//   automatic_checkpoints_enabled = false  → ゼロ値
	//   automatic_critical_error_action = None → 0 (ゼロ値)
	//   lock_on_disconnect = Off               → false (ゼロ値)
	if err := cc.CreateVm(ctx, vmName,
		"", 1,
		api.CriticalErrorAction_None, 30,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 536870912,
		api.OnOffState_Off, 134217728,
		memByt, memByt, memByt,
		"create-zero", 1,
		defaultVM, defaultVM, true, false,
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}

	got, err := cc.GetVm(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVm: %v", err)
	}
	t.Logf("① 作成直後: StaticMemory=%v DynamicMemory=%v AutomaticCheckpointsEnabled=%v CriticalErrorAction=%v",
		got.StaticMemory, got.DynamicMemory, got.AutomaticCheckpointsEnabled, got.AutomaticCriticalErrorAction)

	// #143 の本体。修正前はここが false (= 動的メモリのまま) だった。
	if !got.StaticMemory {
		t.Errorf("🔴 static_memory=true で作ったのに動的メモリになっている (#143)")
	}
	// #141 の本体。
	if got.AutomaticCheckpointsEnabled {
		t.Errorf("🔴 automatic_checkpoints_enabled=false で作ったのに true になっている (#141)")
	}
	// VM レベルのゼロ値。
	if got.AutomaticCriticalErrorAction != api.CriticalErrorAction_None {
		t.Errorf("🔴 automatic_critical_error_action=None で作ったのに %v になっている",
			got.AutomaticCriticalErrorAction)
	}
	if got.LockOnDisconnect != api.OnOffState_Off {
		t.Errorf("🔴 lock_on_disconnect=Off で作ったのに %v になっている", got.LockOnDisconnect)
	}
	t.Logf("🎯 判定: create 時のゼロ値が実際に反映される")
}
