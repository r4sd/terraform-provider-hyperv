//go:build integration
// +build integration

package provider

// go-wsman 経由の integration_services 読み取り (#78) の実機統合テスト。
//
// provider 層の GetVmIntegrationServices を「VM 表示名」で呼び、無条件 PS を解消した go-wsman
// 経路 (resolveVMGUID→ListIntegrationServices) が:
//   - fault なく統合サービス状態を返す
//   - 表示名が PowerShell Get-VMIntegrationService.Name と一致する既知集合に収まる
// ことを非破壊で確認する (既存 VM への読み取りのみ、状態は変更しない)。
//
// 実行例:
//
//	HYPERV_HOST=10.0.0.100 HYPERV_USER=terraform HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	HYPERV_TEST_TARGET_VM_NAME=k8s-worker-01 \
//	go test -tags integration ./internal/provider/ -run TestRealHostIntegrationServicesReadWsman -v

import (
	"context"
	"os"
	"testing"

	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"
)

func TestRealHostIntegrationServicesReadWsman(t *testing.T) {
	vmName := os.Getenv("HYPERV_TEST_TARGET_VM_NAME")
	if vmName == "" {
		t.Skip("HYPERV_TEST_TARGET_VM_NAME 未設定 (読み取り対象の既存 VM 表示名)")
	}
	c := realHostConfigFromEnv(t)
	wsmanClient, err := newWsmanClient(c)
	if err != nil {
		t.Fatalf("newWsmanClient: %v", err)
	}
	cc := &hyperv_wsman.ClientConfig{WsmanClient: wsmanClient}
	ctx := context.Background()

	svcs, err := cc.GetVmIntegrationServices(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVmIntegrationServices(%q): %v", vmName, err)
	}
	for _, s := range svcs {
		if s.Name == "" {
			t.Errorf("統合サービスの Name が空 (前提崩れ)")
		}
		t.Logf("VM %q: 統合サービス %-25q Enabled=%v", vmName, s.Name, s.Enabled)
	}
	// 通常の VM は 6 つの統合サービスを持つ。0 件なら列挙 URI か VM GUID 絞り込みの前提崩れ。
	if len(svcs) == 0 {
		t.Errorf("統合サービスが 0 件 (前提崩れの可能性)")
	}
	// 表示名 (ElementName) は PS Name と同一だがホスト OS 言語にローカライズされる。英語ホストでは
	// 下記集合に収まる。非英語ホストでは別言語になるため「未知の名前」は失敗ではなく情報ログに留める。
	knownEnglish := map[string]bool{
		"Heartbeat": true, "Key-Value Pair Exchange": true, "Shutdown": true,
		"Time Synchronization": true, "VSS": true, "Guest Service Interface": true,
	}
	for _, s := range svcs {
		if !knownEnglish[s.Name] {
			t.Logf("注記: 統合サービス表示名 %q は英語既知集合に含まれない (ローカライズホストの可能性)", s.Name)
		}
	}
}
