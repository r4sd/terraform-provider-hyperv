//go:build integration
// +build integration

package provider

// go-wsman 経由の disk/nic 読み取り (#63 B) の実機統合テスト。
//
// provider 層の GetVmHardDiskDrives / GetVmNetworkAdapters を「VM 表示名」で呼び、
// 実 VM のディスク/NIC が返ることを確認する (非破壊、既存 VM への読み取りのみ)。
//
// 目的は Fable レビューが指摘した C1 (表示名→GUID 解決) の実機検証。go-wsman の vmName は
// GUID 契約のため、表示名を直渡ししていた旧実装では silent に 0 件を返していた。
// 本テストで「表示名で呼んで disk が返る」= resolveVMGUID が効いていることを確認する。
//
// 実行例:
//
//	HYPERV_HOST=<hyperv-host> HYPERV_USER=<user> HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	HYPERV_TEST_TARGET_VM_NAME=<既存VMの表示名> \
//	go test -tags integration ./internal/provider/ -run TestRealHostDiskNicReadWsman -v

import (
	"context"
	"os"
	"testing"

	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"
)

func TestRealHostDiskNicReadWsman(t *testing.T) {
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

	// C1 検証: 表示名で disk が解決・取得できること (旧実装は GUID 契約違反で silent 0 件)。
	disks, err := cc.GetVmHardDiskDrives(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVmHardDiskDrives(%q): %v", vmName, err)
	}
	t.Logf("VM %q: HardDiskDrives = %d", vmName, len(disks))
	for i, d := range disks {
		t.Logf("  disk[%d] type=%s ctrl=%d/%d path=%s", i, d.ControllerType, d.ControllerNumber, d.ControllerLocation, d.Path)
	}
	if len(disks) == 0 {
		t.Errorf("表示名で HardDiskDrives が 0 件 = C1 (表示名→GUID 解決) が効いていない疑い")
	}

	// NIC も同様に表示名で解決・取得できること。
	nics, err := cc.GetVmNetworkAdapters(ctx, vmName, nil)
	if err != nil {
		t.Fatalf("GetVmNetworkAdapters(%q): %v", vmName, err)
	}
	t.Logf("VM %q: NetworkAdapters = %d", vmName, len(nics))
	for i, n := range nics {
		t.Logf("  nic[%d] name=%q switch=%q", i, n.Name, n.SwitchName)
	}
}
