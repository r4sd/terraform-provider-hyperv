//go:build integration
// +build integration

package provider

// go-wsman 経由の vm_processor 読み取り (#79) の実機統合テスト。
//
// provider 層の GetVmProcessors を「VM 表示名」で呼び、無条件 PS を解消した go-wsman 経路
// (resolveVMGUID→GetProcessorSettings + 単位変換) が:
//   - fault なく CPU 設定を返す
//   - Maximum (=Limit/1000) が 0..100 の百分率範囲に収まる (単位変換の健全性)
// ことを非破壊で確認する (既存 VM への読み取りのみ)。
//
// 実行例:
//
//	HYPERV_HOST=10.0.0.100 HYPERV_USER=terraform HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	HYPERV_TEST_TARGET_VM_NAME=k8s-worker-01 \
//	go test -tags integration ./internal/provider/ -run TestRealHostProcessorReadWsman -v

import (
	"context"
	"os"
	"testing"

	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"
)

func TestRealHostProcessorReadWsman(t *testing.T) {
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

	procs, err := cc.GetVmProcessors(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVmProcessors(%q): %v", vmName, err)
	}
	if len(procs) != 1 {
		t.Fatalf("GetVmProcessors: got %d 件, want 1", len(procs))
	}
	p := procs[0]
	t.Logf("VM %q: Maximum=%d%% Reserve=%d%% Weight=%d HwThreadsPerCore=%d NumaNode=%d NumaSocket=%d HostResProtection=%v CompatMigration=%v",
		vmName, p.Maximum, p.Reserve, p.RelativeWeight, p.HwThreadCountPerCore,
		p.MaximumCountPerNumaNode, p.MaximumCountPerNumaSocket, p.EnableHostResourceProtection,
		p.CompatibilityForMigrationEnabled)

	// 単位変換の健全性: Maximum/Reserve は 0..100 の百分率に収まるはず (percent/1000 変換)。
	if p.Maximum < 0 || p.Maximum > 100 {
		t.Errorf("Maximum=%d は 0..100 の範囲外 (Limit/1000 の単位変換が誤っている可能性)", p.Maximum)
	}
	if p.Reserve < 0 || p.Reserve > 100 {
		t.Errorf("Reserve=%d は 0..100 の範囲外 (Reservation/1000 の単位変換が誤っている可能性)", p.Reserve)
	}
}
