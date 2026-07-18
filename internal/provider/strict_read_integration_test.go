//go:build integration
// +build integration

package provider

// strict モード (#80) の実機統合テスト: 既存 homelab VM の Read 経路 (refresh/plan 相当) が
// PowerShell フォールバックを 1 件も呼ばない (PS-0) ことを陽性証明する。
//
// hyperv_machine_instance の Read が呼ぶ全 client メソッドを、埋め込み WinRmClient を
// fail-fast スタブ (StrictNoPSClient) に差し替えた ClientConfig で実行する。どこかで
// go-wsman シャドウ未実装の経路 (= PS フォールバック) が走れば、スタブが error を返し
// Calls() が加算されるので即検知できる。
//
// これは v2.0「Home-env PS-free」の合格条件2 (PS 時代作成の既存 VM の refresh/plan が
// PS-0 かつ no-changes) の実機裏付け。書き込み系 (create/update の processor/IS/firmware) は
// まだ PS フォールバックのため、full-lifecycle の PS-0 は別途 write シャドウ実装後になる。
//
// 実行例:
//
//	HYPERV_HOST=10.0.0.100 HYPERV_USER=terraform HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	HYPERV_TEST_TARGET_VM_NAME=k8s-worker-01 \
//	go test -tags integration ./internal/provider/ -run TestRealHostStrictReadNoPS -v

import (
	"context"
	"os"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
	hyperv_winrm "github.com/taliesins/terraform-provider-hyperv/api/hyperv-winrm"
	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"
)

func TestRealHostStrictReadNoPS(t *testing.T) {
	vmName := os.Getenv("HYPERV_TEST_TARGET_VM_NAME")
	if vmName == "" {
		t.Skip("HYPERV_TEST_TARGET_VM_NAME 未設定 (読み取り対象の既存 VM 表示名)")
	}
	c := realHostConfigFromEnv(t)
	wsmanClient, err := newWsmanClient(c)
	if err != nil {
		t.Fatalf("newWsmanClient: %v", err)
	}

	// 埋め込み WinRmClient を fail-fast スタブに差し替えた strict ClientConfig。
	// go-wsman シャドウ未実装のメソッドは promotion でこのスタブを叩き、即 error + カウントされる。
	strict := &hyperv_wsman.StrictNoPSClient{}
	cc := &hyperv_wsman.ClientConfig{
		ClientConfig: &hyperv_winrm.ClientConfig{WinRmClient: strict},
		WsmanClient:  wsmanClient,
	}
	ctx := context.Background()

	// mustNoPS はメソッド呼び出しがエラーなく完了し、かつ strict スタブが未発火であることを確認する。
	// PS フォールバックが走ると err に strict error が伝播するか、fire-and-forget なら Calls() が増える。
	mustNoPS := func(label string, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: エラー (PS フォールバックの可能性): %v", label, err)
		}
	}

	// --- hyperv_machine_instance の Read 経路を忠実に再現 ---
	exists, err := cc.VmExists(ctx, vmName)
	mustNoPS("VmExists", err)
	if err == nil && !bool(exists.Exists) {
		t.Fatalf("VM %q が存在しない。既存 VM 名を指定すること", vmName)
	}

	vm, err := cc.GetVm(ctx, vmName)
	mustNoPS("GetVm", err)
	t.Logf("VM %q: generation=%d", vmName, vm.Generation)

	_, err = cc.GetVmProcessors(ctx, vmName)
	mustNoPS("GetVmProcessors", err)

	_, err = cc.GetVmIntegrationServices(ctx, vmName)
	mustNoPS("GetVmIntegrationServices", err)

	_, err = cc.GetVmNetworkAdapters(ctx, vmName, nil)
	mustNoPS("GetVmNetworkAdapters", err)

	_, err = cc.GetVmHardDiskDrives(ctx, vmName)
	mustNoPS("GetVmHardDiskDrives", err)

	_, err = cc.GetVmDvdDrives(ctx, vmName)
	mustNoPS("GetVmDvdDrives", err)

	_, err = cc.GetVmGpuAdapters(ctx, vmName)
	mustNoPS("GetVmGpuAdapters", err)

	_, err = cc.GetVmStatus(ctx, vmName)
	mustNoPS("GetVmStatus", err)

	// firmware: 世代分岐 (resource と同じ)。Gen1 は GetNoVmFirmwares=純 Go no-op で PS を踏まない。
	// Gen2 は GetVmFirmwares が現状 PS フォールバック → strict でここが発火する (= Gen2 は未達)。
	if vm.Generation > 1 {
		_, err = cc.GetVmFirmwares(ctx, vmName)
		mustNoPS("GetVmFirmwares (Gen2)", err)
	} else {
		_ = cc.GetNoVmFirmwares(ctx) // no-op、PS を踏まない
	}

	// wait_for_ips 全 false 相当 (nil) は #76 で PS スキップされるはず。
	mustNoPS("WaitForVmNetworkAdaptersIps(nil)",
		cc.WaitForVmNetworkAdaptersIps(ctx, vmName, 0, 0, []api.VmNetworkAdapterWaitForIp{}))

	// --- 合格条件: PS フォールバック 0 件 ---
	if calls := strict.Calls(); calls != 0 {
		t.Errorf("Read 経路で PS フォールバックが %d 件走った (strict 不合格): %v", calls, strict.Labels())
	} else {
		t.Logf("✅ VM %q の Read 経路 (generation=%d) は PowerShell 呼び出し 0 件 (strict 合格)", vmName, vm.Generation)
	}

	// --- negative control: strict ハーネスに検出力があることの確認 ---
	// 既知の PS フォールバックメソッド (GetVmFirmwares は未シャドウ) を新しい strict スタブ経由で呼び、
	// 確実に発火する (Calls()>0 かつ error) ことを確認する。これで「0 件だった」のが検出力不足の
	// 偽陽性ではなく、本当に PS を踏んでいないことの裏返しになる。
	negStrict := &hyperv_wsman.StrictNoPSClient{}
	negCC := &hyperv_wsman.ClientConfig{
		ClientConfig: &hyperv_winrm.ClientConfig{WinRmClient: negStrict},
		WsmanClient:  wsmanClient,
	}
	if _, err := negCC.GetVmFirmwares(ctx, vmName); err == nil {
		t.Error("negative control: 未シャドウの GetVmFirmwares は strict で error になるべき (検出力の証明)")
	}
	if negStrict.Calls() == 0 {
		t.Error("negative control: strict スタブが PS 呼び出しを検知できていない (ハーネスの検出力不足)")
	} else {
		t.Logf("✅ negative control: GetVmFirmwares が PS フォールバックとして検知された (%v)", negStrict.Labels())
	}
}
