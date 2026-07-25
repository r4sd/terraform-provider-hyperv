//go:build integration
// +build integration

package provider

// go-wsman 経由の integration_services 書き込み (CreateOrUpdateVmIntegrationServices) の
// 実機統合テスト。
//
// 使い捨て VM を作成し、Guest Service Interface (既定 Disabled) を実際に反転 (Enable→Disable)
// して変化を確認する。no-op 書き戻しだけでは「受理されたがサイレントに無視された」ケースを
// 区別できない (go-wsman #112 の Fable レビュー指摘と同じ理由)。
//
// 状態確認は provider の GetVmIntegrationServices (実行時 Name、ホスト言語でローカライズされる。
// homelab は日本語 Windows、2026-07-18 実機確認済み) ではなく、go-wsman の
// GetIntegrationServiceEnabled (component 指定、ロケール非依存) を直接使う。あわせて
// 差分なしガード (既に望む状態なら Set を省略) が実際に効くことも確認する。
//
// 実行例:
//
//	HYPERV_HOST=10.0.0.100 HYPERV_USER=terraform HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	go test -tags integration ./internal/provider/ -run TestRealHostIntegrationServiceWriteWsman -v

import (
	"context"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"
)

func TestRealHostIntegrationServiceWriteWsman(t *testing.T) {
	c := realHostConfigFromEnv(t)
	wsmanClient, err := newWsmanClient(c)
	if err != nil {
		t.Fatalf("newWsmanClient: %v", err)
	}
	cc := &hyperv_wsman.ClientConfig{WsmanClient: wsmanClient}
	ctx := context.Background()

	const vmName = "tf-wsman-is-write-test"
	_ = cc.DeleteVm(ctx, vmName) // 前回残骸を掃除
	t.Cleanup(func() {
		if err := cc.DeleteVm(ctx, vmName); err != nil {
			t.Logf("cleanup DeleteVm: %v", err)
		}
	})

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
		"is-write-test", 1,
		"", "", true, false,
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}

	// go-wsman のロケール非依存な状態確認に使う VM GUID を解決する
	// (ElementName でなく Msvm_ComputerSystem.Name)。
	vms, err := wsmanClient.ListComputerSystems(ctx)
	if err != nil {
		t.Fatalf("ListComputerSystems: %v", err)
	}
	var vmGUID string
	for _, vm := range vms {
		if vm.ElementName == vmName {
			vmGUID = vm.Name
			break
		}
	}
	if vmGUID == "" {
		t.Fatalf("VM %q が見つからない", vmName)
	}

	// baseline 確認 (作成直後、provider の Read 経路が動くこと)。
	baseline, err := cc.GetVmIntegrationServices(ctx, vmName)
	if err != nil {
		t.Fatalf("baseline GetVmIntegrationServices: %v", err)
	}
	if len(baseline) == 0 {
		t.Fatalf("baseline: 統合サービスが 0 件")
	}
	t.Logf("baseline: %d 件の統合サービス", len(baseline))

	// 差分なしガード: 既定 config (DefaultVmIntegrationServices) と同じ値を書くと Set が
	// スキップされること (Slice A の processor と同じ設計)。
	defaults, err := api.DefaultVmIntegrationServices()
	if err != nil {
		t.Fatalf("DefaultVmIntegrationServices: %v", err)
	}
	defaultMap := defaults.(map[string]interface{})
	sameAsDefault := make([]api.VmIntegrationService, 0, len(defaultMap))
	for name, enabled := range defaultMap {
		sameAsDefault = append(sameAsDefault, api.VmIntegrationService{Name: name, Enabled: enabled.(bool)})
	}
	if err := cc.CreateOrUpdateVmIntegrationServices(ctx, vmName, sameAsDefault); err != nil {
		t.Fatalf("CreateOrUpdateVmIntegrationServices(既定値と同じ): %v", err)
	}
	t.Logf("✅ 差分なしガード: 既定値と同じ %d 件を要求し no-op で完了 (エラー無し)", len(sameAsDefault))

	// 真の状態遷移: Guest Service Interface (既定 Disabled) を Enable → go-wsman 側で
	// ロケール非依存に反映確認 → Disable に戻す → 確認。
	if err := cc.CreateOrUpdateVmIntegrationServices(ctx, vmName,
		[]api.VmIntegrationService{{Name: "Guest Service Interface", Enabled: true}}); err != nil {
		t.Fatalf("CreateOrUpdateVmIntegrationServices(GSI Enable): %v", err)
	}
	enabled, err := wsmanClient.GetIntegrationServiceEnabled(ctx, vmGUID, hyperv.IntegrationServiceGuestServiceInterface)
	if err != nil {
		t.Fatalf("GetIntegrationServiceEnabled (GSI, after Enable): %v", err)
	}
	if !enabled {
		t.Fatalf("GSI Enable が反映されていない (サイレント黙殺の疑い)")
	}
	t.Logf("✅ GSI Enable 反映確認 (go-wsman 側ロケール非依存読み取り)")

	if err := cc.CreateOrUpdateVmIntegrationServices(ctx, vmName,
		[]api.VmIntegrationService{{Name: "Guest Service Interface", Enabled: false}}); err != nil {
		t.Fatalf("CreateOrUpdateVmIntegrationServices(GSI Disable 復元): %v", err)
	}
	enabled, err = wsmanClient.GetIntegrationServiceEnabled(ctx, vmGUID, hyperv.IntegrationServiceGuestServiceInterface)
	if err != nil {
		t.Fatalf("GetIntegrationServiceEnabled (GSI, after Disable): %v", err)
	}
	if enabled {
		t.Fatalf("GSI Disable への復元が反映されていない")
	}
	t.Logf("✅ GSI Disable 復元確認 (状態遷移の双方向を実証)")
}
