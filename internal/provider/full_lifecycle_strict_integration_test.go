//go:build integration
// +build integration

package provider

// go-wsman 経由の VM フルライフサイクル (Slice E, #80) の strict モード実機統合テスト。
//
// Create→NIC追加→Processor/Firmware の既定値書き込み→各種 Read→Destroy の一周を、埋め込み
// WinRmClient を fail-fast スタブ (StrictNoPSClient) に差し替えた ClientConfig で実行し、
// homelab の既定運用 (Gen2 VM、NIC はスイッチ接続なし、wait_for_ips=false、processor/firmware は
// 既定値のまま) が PowerShell フォールバックを 1 件も呼ばないこと (PS-0) を陽性証明する。
// IntegrationServices は既知の問題 (#98、下記) によりこの Slice では対象外。
//
// Slice D (firmware read/write shadow) 完了により、strict_read_integration_test.go の
// コメントにあった「Gen2 は GetVmFirmwares 未シャドウのため PS-0 未達」という制約は解消された
// (BootSourceOrder が File 型を含まず解決可能な限り)。本テストはその解消を実機で確認する。
//
// NIC はスイッチ接続なしで追加する (go-wsman #114: 新規VM直後のスイッチ接続が ErrorCode=32773 で
// 失敗する既知バグのため、本テストのスコープ外として回避する)。
//
// negative control 必須 (DoD §4): 恒真アサーションを避けるため、strict ハーネスが実際に
// PS フォールバックを検知できることを別の strict インスタンスで証明する。
//   1. firmware のゼロ値ダウングレード書き込み (ConsoleMode COM1→Default) は PS へ委譲される。
//   2. wait_for_ips=true は WaitForVmNetworkAdaptersIps を PS へ委譲する (#76)。
//
// 実行例:
//
//	HYPERV_HOST=10.0.0.100 HYPERV_USER=terraform HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	go test -tags integration ./internal/provider/ -run TestRealHostFullLifecycleStrictPS0 -v

import (
	"context"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
	hyperv_winrm "github.com/taliesins/terraform-provider-hyperv/api/hyperv-winrm"
	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"
)

func TestRealHostFullLifecycleStrictPS0(t *testing.T) {
	c := realHostConfigFromEnv(t)
	wsmanClient, err := newWsmanClient(c)
	if err != nil {
		t.Fatalf("newWsmanClient: %v", err)
	}

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

	const (
		vmName = "tf-wsman-full-lifecycle-test"
		memByt = 536870912
	)
	// strict でない普通の ClientConfig で前回残骸を掃除 (DeleteVm 自体は shadow 済みなので不要だが、
	// cleanup 経路のエラーを strict カウンタに混ぜないため専用インスタンスを使う)。
	cleanupCC := &hyperv_wsman.ClientConfig{WsmanClient: wsmanClient}
	_ = cleanupCC.DeleteVm(ctx, vmName)
	t.Cleanup(func() {
		if err := cleanupCC.DeleteVm(ctx, vmName); err != nil {
			t.Logf("cleanup DeleteVm: %v", err)
		}
	})

	// --- 1. Create (Gen2) ---
	mustNoPS("CreateVm", cc.CreateVm(ctx, vmName,
		"", 2,
		api.CriticalErrorAction_Pause, 0,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 0,
		api.OnOffState_Off, 0,
		memByt, memByt, memByt,
		"full-lifecycle-test", 1,
		"", "", true, false,
	))

	vm, err := cc.GetVm(ctx, vmName)
	mustNoPS("GetVm", err)
	if vm.Generation != 2 {
		t.Fatalf("Generation: got %d, want 2", vm.Generation)
	}

	// --- 2. NIC (スイッチ接続なし、go-wsman #114 回避) ---
	// go-wsman 経路は NIC 本体+スイッチ接続+MAC のみ対応 (unsupportedNetworkAdapterOptions)。
	// それ以外のフィールドは schema 既定値のまま渡す必要がある (Go のゼロ値と既定値が異なる
	// OnOffState 等が多いため、ゼロ値のまま渡すと「未対応オプション」エラーになる)。
	mustNoPS("CreateOrUpdateVmNetworkAdapters", cc.CreateOrUpdateVmNetworkAdapters(ctx, vmName, []api.VmNetworkAdapter{
		defaultLifecycleNetworkAdapter("eth0-lifecycle"),
	}))

	// --- 3. Processor: 既定値のまま書き戻す (差分なしガードで PS-0) ---
	procs, err := cc.GetVmProcessors(ctx, vmName)
	mustNoPS("GetVmProcessors", err)
	if len(procs) == 1 {
		mustNoPS("CreateOrUpdateVmProcessors(no-op)", cc.CreateOrUpdateVmProcessors(ctx, vmName, procs))
	}

	// --- 4. IntegrationServices ---
	// GetVmIntegrationServices の結果をそのまま CreateOrUpdateVmIntegrationServices に渡す
	// round-trip は非英語ロケールの Hyper-V ホストで失敗する既知の問題 (#98、ElementName が
	// ローカライズされる一方 go-wsman の component 解決は英語 canonical 名のみ受理するため)。
	// この Slice ではフルライフサイクルの PS-0 証明に IS 書き込みは必須ではないため、
	// 既知の問題を踏まないよう本テストではこの leg を省略する。
	_, err = cc.GetVmIntegrationServices(ctx, vmName)
	mustNoPS("GetVmIntegrationServices", err)

	// --- 5. Firmware: 既定値のまま書き戻す (差分なしガードで PS-0) ---
	fw, err := cc.GetVmFirmware(ctx, vmName)
	mustNoPS("GetVmFirmware", err)
	mustNoPS("CreateOrUpdateVmFirmware(no-op)", cc.CreateOrUpdateVmFirmware(ctx, vmName,
		fw.BootOrders, fw.EnableSecureBoot, fw.SecureBootTemplate,
		fw.PreferredNetworkBootProtocol, fw.ConsoleMode, fw.PauseAfterBootFailure))

	// --- 6. Read 経路 (refresh/plan 相当) ---
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
	mustNoPS("WaitForVmNetworkAdaptersIps(all-false)",
		cc.WaitForVmNetworkAdaptersIps(ctx, vmName, 0, 0, []api.VmNetworkAdapterWaitForIp{{Name: "eth0-lifecycle", WaitForIps: false}}))

	// --- 合格条件: ここまで (Destroy 以外の全ライフサイクル) で PS フォールバック 0 件 ---
	if calls := strict.Calls(); calls != 0 {
		t.Errorf("フルライフサイクルで PS フォールバックが %d 件走った (strict 不合格): %v", calls, strict.Labels())
	} else {
		t.Logf("✅ Gen2 VM のフルライフサイクル (create→NIC→processor/firmware 既定値→read) は PS 呼び出し 0 件 (strict 合格)")
	}

	// --- negative control (VM が生きている間に実行。Destroy は最後にまとめて行う) ---

	// negative control 1: firmware ゼロ値ダウングレードは PS へ委譲する ---
	negCC := &hyperv_wsman.ClientConfig{WsmanClient: wsmanClient} // 通常経路で ConsoleMode=COM1 を作る
	if err := negCC.CreateOrUpdateVmFirmware(ctx, vmName,
		fw.BootOrders, fw.EnableSecureBoot, fw.SecureBootTemplate,
		fw.PreferredNetworkBootProtocol, api.ConsoleModeType_Com1, fw.PauseAfterBootFailure); err != nil {
		t.Skipf("negative control 前提の ConsoleMode=COM1 設定に失敗: %v", err)
	}
	negFw := &hyperv_wsman.StrictNoPSClient{}
	negFwCC := &hyperv_wsman.ClientConfig{
		ClientConfig: &hyperv_winrm.ClientConfig{WinRmClient: negFw},
		WsmanClient:  wsmanClient,
	}
	err = negFwCC.CreateOrUpdateVmFirmware(ctx, vmName,
		fw.BootOrders, fw.EnableSecureBoot, fw.SecureBootTemplate,
		fw.PreferredNetworkBootProtocol, api.ConsoleModeType_Default, fw.PauseAfterBootFailure)
	if err == nil {
		t.Error("negative control: ConsoleMode COM1→Default (ゼロ値ダウングレード) は strict で error になるべき")
	}
	if negFw.Calls() == 0 {
		t.Error("negative control: strict スタブが firmware のゼロ値ダウングレード委譲を検知できていない")
	} else {
		t.Logf("✅ negative control(firmware zero-downgrade): PS 委譲が検知された (%v)", negFw.Labels())
	}

	// --- negative control 2: wait_for_ips=true は PS へ委譲する (#76) ---
	negWait := &hyperv_wsman.StrictNoPSClient{}
	negWaitCC := &hyperv_wsman.ClientConfig{
		ClientConfig: &hyperv_winrm.ClientConfig{WinRmClient: negWait},
		WsmanClient:  wsmanClient,
	}
	err = negWaitCC.WaitForVmNetworkAdaptersIps(ctx, vmName, 0, 0, []api.VmNetworkAdapterWaitForIp{{Name: "eth0-lifecycle", WaitForIps: true}})
	if err == nil {
		t.Error("negative control: wait_for_ips=true は PS へ委譲し strict で error になるべき")
	}
	if negWait.Calls() == 0 {
		t.Error("negative control: strict スタブが wait_for_ips=true の PS 委譲を検知できていない")
	} else {
		t.Logf("✅ negative control(wait_for_ips=true): PS 委譲が検知された (%v)", negWait.Labels())
	}

	// --- Destroy (strict カウンタ集計後の後始末、strict でなくてよい) ---
	if err := cleanupCC.DeleteVm(ctx, vmName); err != nil {
		t.Fatalf("DeleteVm: %v", err)
	}
	ex, err := cleanupCC.VmExists(ctx, vmName)
	if err != nil {
		t.Fatalf("VmExists(after delete): %v", err)
	}
	if ex.Exists {
		t.Error("DeleteVm 後も VmExists=true")
	}
}

// defaultLifecycleNetworkAdapter は go-wsman 経路 (unsupportedNetworkAdapterOptions) が受理する
// 既定値で NIC を組み立てる。Go のゼロ値と Hyper-V の既定値が異なるフィールド (OnOffState 系は
// ゼロ値が On になる等) があるため、Name 以外は明示的に既定値を書く必要がある。
func defaultLifecycleNetworkAdapter(name string) api.VmNetworkAdapter {
	return api.VmNetworkAdapter{
		Name:                                   name,
		DynamicMacAddress:                      true,
		MacAddressSpoofing:                     api.OnOffState_Off,
		DhcpGuard:                              api.OnOffState_Off,
		RouterGuard:                            api.OnOffState_Off,
		PortMirroring:                          api.PortMirroring_None,
		IeeePriorityTag:                        api.OnOffState_Off,
		VmqWeight:                              100,
		IovQueuePairsRequested:                 1,
		IovInterruptModeration:                 api.IovInterruptModerationValue_Off,
		IovWeight:                              100,
		IpsecOffloadMaximumSecurityAssociation: 512,
		AllowTeaming:                           api.OnOffState_On,
		DeviceNaming:                           api.OnOffState_Off,
		FixSpeed10G:                            api.OnOffState_Off,
		VrssEnabled:                            true,
		VmmqQueuePairs:                         16,
	}
}
