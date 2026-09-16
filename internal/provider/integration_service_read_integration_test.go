//go:build integration
// +build integration

package provider

// go-wsman 経由の integration_services 読み取り (#78) の実機統合テスト。
//
// provider 層の GetVmIntegrationServices を「VM 表示名」で呼び、無条件 PS を解消した go-wsman
// 経路 (resolveVMGUID→ListIntegrationServices) が:
//   - fault なく統合サービス状態を返す
//   - 名前がロケール非依存の英語 6 種に収まる (#98。ローカライズされるホストでも同じ)
// ことを非破壊で確認する (既存 VM への読み取りのみ、状態は変更しない)。
//
// 実行例:
//
//	HYPERV_HOST=<hyperv-host> HYPERV_USER=<user> HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	HYPERV_TEST_TARGET_VM_NAME=<既存VMの表示名> \
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
	// #98 以降、Name は ElementName (ローカライズされる) ではなく go-wsman の Component
	// (ロケール非依存) を写したもの。ホスト言語に関わらず下記 6 種に収まるので、外れたら失敗。
	// この homelab は日本語ロケールなので、ここが緑なら正規化が効いている陽性証明になる。
	knownEnglish := map[string]bool{
		"Heartbeat": true, "Key-Value Pair Exchange": true, "Shutdown": true,
		"Time Synchronization": true, "VSS": true, "Guest Service Interface": true,
	}
	for _, s := range svcs {
		if !knownEnglish[s.Name] {
			t.Errorf("統合サービス名 %q がロケール非依存の英語 6 種に含まれない (正規化漏れ)", s.Name)
		}
	}
}
