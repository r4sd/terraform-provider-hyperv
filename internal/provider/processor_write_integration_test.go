//go:build integration
// +build integration

package provider

// go-wsman 経由の vm_processor 書き込み (CreateOrUpdateVmProcessors) の実機統合テスト。
//
// 使い捨て Gen2 VM を作成し、provider 層の CreateOrUpdateVmProcessors を「VM 表示名」で呼んで:
//   - skip-guard: 現行 (既定) 値と同じ要求は Set を流さず no-op (差分なしガード)
//   - mutation: Maximum/Reserve/RelativeWeight を変更すると SetProcessorSettings が走り、
//     GetVmProcessors の読み戻しで単位変換込みに反映される (Maximum=Limit/1000 の往復)
//   - idempotency: 変更後に同値で再呼び出しすると再び no-op
// を検証する。VM はテスト後に DeleteVm で確実に削除する。
//
// 実行例:
//
//	HYPERV_HOST=10.0.0.100 HYPERV_USER=terraform HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	go test -tags integration ./internal/provider/ -run TestRealHostProcessorWriteWsman -v

import (
	"context"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"
)

func TestRealHostProcessorWriteWsman(t *testing.T) {
	c := realHostConfigFromEnv(t)
	wsmanClient, err := newWsmanClient(c)
	if err != nil {
		t.Fatalf("newWsmanClient: %v", err)
	}
	cc := &hyperv_wsman.ClientConfig{WsmanClient: wsmanClient}
	ctx := context.Background()

	const vmName = "tf-wsman-proc-write-test"
	_ = cc.DeleteVm(ctx, vmName) // 前回残骸を掃除
	t.Cleanup(func() {
		if err := cc.DeleteVm(ctx, vmName); err != nil {
			t.Logf("cleanup DeleteVm: %v", err)
		}
	})

	// 使い捨て Gen2 VM (512 MiB / 1 vCPU)。
	const memByt = 536870912
	if err := cc.CreateVm(ctx, vmName,
		"", 2,
		api.CriticalErrorAction_Pause, 0,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 0,
		api.OnOffState_Off, 0,
		memByt, memByt, memByt,
		"proc-write-test", 1,
		"", "", true, false,
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}

	getOne := func(label string) api.VmProcessor {
		t.Helper()
		procs, err := cc.GetVmProcessors(ctx, vmName)
		if err != nil {
			t.Fatalf("%s GetVmProcessors: %v", label, err)
		}
		if len(procs) != 1 {
			t.Fatalf("%s GetVmProcessors: got %d, want 1", label, len(procs))
		}
		return procs[0]
	}

	// baseline: 作成直後は Hyper-V 既定 (Maximum=100 / Reserve=0 / RelativeWeight=100)。
	baseline := getOne("baseline")
	t.Logf("baseline: Maximum=%d Reserve=%d RelativeWeight=%d", baseline.Maximum, baseline.Reserve, baseline.RelativeWeight)
	if baseline.Maximum != 100 {
		t.Errorf("baseline Maximum: got %d, want 100 (Hyper-V 既定)", baseline.Maximum)
	}

	// skip-guard: 現行値と同じ要求は Set を流さず no-op。値が変わらないこと。
	if err := cc.CreateOrUpdateVmProcessors(ctx, vmName, []api.VmProcessor{baseline}); err != nil {
		t.Fatalf("CreateOrUpdateVmProcessors(same): %v", err)
	}
	if got := getOne("after-noop"); !processorEq(got, baseline) {
		t.Errorf("skip-guard 後に値が変わった: got=%+v baseline=%+v", got, baseline)
	}

	// mutation: Maximum/Reserve/RelativeWeight を変更 → SetProcessorSettings が走る。
	want := baseline
	want.Maximum = 50
	want.Reserve = 10
	want.RelativeWeight = 200
	if err := cc.CreateOrUpdateVmProcessors(ctx, vmName, []api.VmProcessor{want}); err != nil {
		t.Fatalf("CreateOrUpdateVmProcessors(mutate): %v", err)
	}
	got := getOne("after-mutate")
	if got.Maximum != 50 {
		t.Errorf("Maximum: got %d, want 50 (Limit=50000 の読み戻し)", got.Maximum)
	}
	if got.Reserve != 10 {
		t.Errorf("Reserve: got %d, want 10 (Reservation=10000 の読み戻し)", got.Reserve)
	}
	if got.RelativeWeight != 200 {
		t.Errorf("RelativeWeight: got %d, want 200 (Weight 1:1)", got.RelativeWeight)
	}
	t.Logf("✅ mutation 反映: Maximum=%d Reserve=%d RelativeWeight=%d", got.Maximum, got.Reserve, got.RelativeWeight)

	// idempotency: 変更後に同値で再呼び出し → skip-guard で no-op、値も安定。
	if err := cc.CreateOrUpdateVmProcessors(ctx, vmName, []api.VmProcessor{want}); err != nil {
		t.Fatalf("CreateOrUpdateVmProcessors(idempotent): %v", err)
	}
	if got2 := getOne("after-idempotent"); !processorEq(got2, want) {
		t.Errorf("idempotent 呼び出しで値が変わった: got=%+v want=%+v", got2, want)
	}
	t.Logf("✅ skip-guard / mutation / idempotency すべて実機で確認")
}

// processorEq は VmName を除く全フィールドの一致を返す (テスト用の簡易比較)。
func processorEq(a, b api.VmProcessor) bool {
	a.VmName, b.VmName = "", ""
	return a == b
}
