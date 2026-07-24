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
// PS-0 が成立する条件 (v2.0「Home-env PS-free」の合格条件2 の正確な範囲):
//   - VM が Gen1 であること (Gen2 は GetVmFirmwares が未シャドウ=PS のまま)。
//   - 全 network_adapter が wait_for_ips=false であること。wait_for_ips のスキーマ default は
//     true で、1 つでも true だと read の WaitForVmNetworkAdaptersIps が PS へ委譲する (#76)。
//
// 書き込み系 (create/update の processor/IS/firmware) は該当ブロックを config が持つ場合に PS
// フォールバックのため、full-lifecycle の PS-0 は別途 write シャドウ実装後になる。
//
// 実行例 (Gen1 VM を指定):
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
	strict := &hyperv_wsman.StrictNoPSClient{}
	cc := &hyperv_wsman.ClientConfig{
		ClientConfig: &hyperv_winrm.ClientConfig{WinRmClient: strict},
		WsmanClient:  wsmanClient,
	}
	ctx := context.Background()

	mustNoPS := func(label string, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: エラー (PS フォールバックの可能性): %v", label, err)
		}
	}

	// --- hyperv_machine_instance の Read 経路を再現 (firmware/wait は下で世代・条件別に扱う) ---
	exists, err := cc.VmExists(ctx, vmName)
	mustNoPS("VmExists", err)
	if err == nil && !exists.Exists {
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

	// wait_for_ips=false (全 NIC false 相当) は #76 で PS をスキップするはず。空スライスではなく
	// 明示的に WaitForIps=false のエントリを渡すことで、シャドウのスキップ判定を実際に通す。
	mustNoPS("WaitForVmNetworkAdaptersIps(all-false)",
		cc.WaitForVmNetworkAdaptersIps(ctx, vmName, 0, 0, []api.VmNetworkAdapterWaitForIp{{Name: "", WaitForIps: false}}))

	// firmware: Gen1 は GetNoVmFirmwares=純 Go no-op で PS を踏まない。Gen2 は GetVmFirmwares が
	// 未シャドウ=PS のため、ここでは main の strict には載せず、下の negative control で別スタブで扱う。
	if vm.Generation <= 1 {
		_ = cc.GetNoVmFirmwares(ctx) // no-op、PS を踏まない
	}

	// --- 合格条件: 上記 Read 経路 (Gen 非依存部 + wait_for_ips=false) の PS フォールバック 0 件 ---
	if calls := strict.Calls(); calls != 0 {
		t.Errorf("Read 経路で PS フォールバックが %d 件走った (strict 不合格): %v", calls, strict.Labels())
	} else {
		t.Logf("✅ VM %q (generation=%d) の Read 経路 (wait_for_ips=false 前提) は PS 呼び出し 0 件 (strict 合格)", vmName, vm.Generation)
	}
	if vm.Generation > 1 {
		t.Logf("⚠️ generation=%d: GetVmFirmwares が未シャドウ=PS のため、Gen2 の refresh は PS-0 未達 (firmware write/read シャドウは v2.1)", vm.Generation)
	}

	// --- negative control 1: strict ハーネスの検出力 (未シャドウ GetVmFirmwares は必ず発火) ---
	negFw := &hyperv_wsman.StrictNoPSClient{}
	negFwCC := &hyperv_wsman.ClientConfig{
		ClientConfig: &hyperv_winrm.ClientConfig{WinRmClient: negFw},
		WsmanClient:  wsmanClient,
	}
	if _, err := negFwCC.GetVmFirmwares(ctx, vmName); err == nil {
		t.Error("negative control: 未シャドウの GetVmFirmwares は strict で error になるべき (検出力の証明)")
	}
	if negFw.Calls() == 0 {
		t.Error("negative control: strict スタブが GetVmFirmwares の PS 呼び出しを検知できていない")
	} else {
		t.Logf("✅ negative control(firmware): GetVmFirmwares が PS として検知された (%v)", negFw.Labels())
	}

	// --- negative control 2: wait_for_ips=true は PS へ委譲する (制限の実証 + 検出力) ---
	// これにより「wait_for_ips=false なら PS-0」の逆も抑えられ、上の all-false アサーションが
	// 恒真でないことが担保される。
	negWait := &hyperv_wsman.StrictNoPSClient{}
	negWaitCC := &hyperv_wsman.ClientConfig{
		ClientConfig: &hyperv_winrm.ClientConfig{WinRmClient: negWait},
		WsmanClient:  wsmanClient,
	}
	err = negWaitCC.WaitForVmNetworkAdaptersIps(ctx, vmName, 0, 0, []api.VmNetworkAdapterWaitForIp{{Name: "", WaitForIps: true}})
	if err == nil {
		t.Error("negative control: wait_for_ips=true は WaitForVmNetworkAdaptersIps を PS へ委譲し strict で error になるべき")
	}
	if negWait.Calls() == 0 {
		t.Error("negative control: wait_for_ips=true で PS 委譲が検知できていない (all-false アサーションが恒真の疑い)")
	} else {
		t.Logf("✅ negative control(wait_for_ips=true): PS 委譲が検知された (%v)", negWait.Labels())
	}
}
