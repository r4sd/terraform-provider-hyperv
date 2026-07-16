//go:build integration
// +build integration

package provider

// go-wsman 経由の gpu_adapter 読み取り + 空リストガード (#77) の実機統合テスト。
//
// provider 層の GetVmGpuAdapters / CreateOrUpdateVmGpuAdapters を「VM 表示名」で呼び、
// GPU 未使用の homelab で:
//   - Read が空スライス・no-error を返す (無条件 PS の解消、resolveVMGUID→ListGpuAdapters が効く)
//   - Create(空リスト) が PS を流さず no-op で返る (空リストガード)
// ことを非破壊で確認する (既存 VM への読み取り + 空 Create のみ、GPU 割当は変更しない)。
//
// 実行例:
//
//	HYPERV_HOST=10.0.0.100 HYPERV_USER=terraform HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	HYPERV_TEST_TARGET_VM_NAME=k8s-worker-01 \
//	go test -tags integration ./internal/provider/ -run TestRealHostGpuReadWsman -v

import (
	"context"
	"os"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"
)

func TestRealHostGpuReadWsman(t *testing.T) {
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

	// Read: 表示名で GPU パーティションが解決・取得でき、GPU 未使用ホストで空を返すこと。
	// (旧 PS 実装は無条件 PS。新実装は resolveVMGUID→ListGpuAdapters が能力定義を除外して空。)
	gpus, err := cc.GetVmGpuAdapters(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVmGpuAdapters(%q): %v", vmName, err)
	}
	t.Logf("VM %q: GpuAdapters = %d", vmName, len(gpus))
	if len(gpus) != 0 {
		// homelab は GPU 未使用。0 件でなければテスト前提 (または能力定義の除外) を見直す。
		t.Errorf("GPU 未使用ホストで GpuAdapters = %d, want 0 (能力定義混入 or 前提崩れ)", len(gpus))
	}

	// Create(空リスト): 空リストガードで PS を流さず no-op で返ること (非破壊)。
	if err := cc.CreateOrUpdateVmGpuAdapters(ctx, vmName, []api.VmGpuAdapter{}); err != nil {
		t.Errorf("CreateOrUpdateVmGpuAdapters(空): no-op であるべき: %v", err)
	}
}
