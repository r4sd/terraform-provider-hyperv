//go:build integration
// +build integration

package provider

// go-wsman 経由の CreateOrUpdateVmFirmware (WRITE) の実機統合テスト。
//
// 実行例:
//
//	HYPERV_HOST=10.0.0.100 HYPERV_USER=terraform HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	go test -tags integration ./internal/provider/ -run TestRealHostFirmwareWriteWsman -v

import (
	"context"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"
)

// newRealHostWsmanClientConfig は HYPERV_USE_WSMAN=1 (実 PS フォールバック付き) で
// *hyperv_wsman.ClientConfig を構築する。GetVmFirmware の READ 統合テストと異なり、本テストは
// ゼロ値ダウングレードの PS 委譲が実際に動くことまで検証するため、埋め込み winrm を nil にせず
// Config.Client() の通常経路 (getHypervProvider) をそのまま使う。
func newRealHostWsmanClientConfig(t *testing.T, c *Config) *hyperv_wsman.ClientConfig {
	t.Helper()
	t.Setenv("HYPERV_USE_WSMAN", "1")
	client, err := c.Client()
	if err != nil {
		t.Fatalf("Config.Client(): %v", err)
	}
	cc, ok := client.(*hyperv_wsman.ClientConfig)
	if !ok {
		t.Fatalf("Config.Client(): got %T, want *hyperv_wsman.ClientConfig (HYPERV_USE_WSMAN が有効か確認)", client)
	}
	return cc
}

// TestRealHostFirmwareWriteWsman は CreateOrUpdateVmFirmware の go-wsman 書き込み経路を実機で検証する。
//
// 2 段階で確認する:
//  1. 表現可能な変更 (ConsoleMode Default→COM1、PreferredNetworkBootProtocol IPv4→IPv6、
//     BootOrders の並び替え) を書き込み、再読み取りで反映されていることを確認する。特に BootOrders は
//     resolveBootSourceRefs (書き込み) → resolveBootOrders (読み取り) の round-trip が実機で正しく
//     機能するかが Slice D READ 時点の未検証事項だった (advisor 指摘)。
//  2. ゼロ値ダウングレード (ConsoleMode COM1→Default) を書き込み、PS (embedded winrm) への委譲が
//     黙って失敗せず実際に反映されることを確認する (Slice A Fable C と同型のリグレッションが
//     ここでも起きていないことの実機証跡)。
func TestRealHostFirmwareWriteWsman(t *testing.T) {
	c := realHostConfigFromEnv(t)
	cc := newRealHostWsmanClientConfig(t, c)
	wsmanClient := cc.WsmanClient
	ctx := context.Background()

	const vmName = "tf-wsman-firmware-write-test"
	_ = cc.DeleteVm(ctx, vmName)
	t.Cleanup(func() {
		if err := cc.DeleteVm(ctx, vmName); err != nil {
			t.Logf("cleanup DeleteVm: %v", err)
		}
	})

	const memByt = 536870912
	if err := cc.CreateVm(ctx, vmName,
		"", 2, // Generation 2
		api.CriticalErrorAction_Pause, 0,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 0,
		api.OnOffState_Off, 0,
		memByt, memByt, memByt,
		"firmware-write-test", 1,
		"", "", true, false,
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}

	vm, err := wsmanClient.FindComputerSystemByElementName(ctx, vmName)
	if err != nil {
		t.Fatalf("FindComputerSystemByElementName: %v", err)
	}
	vmGUID := vm.Name

	// NIC (スイッチ接続なし、go-wsman #114 の影響を避ける)。
	nicRes, err := wsmanClient.AddNetworkAdapter(ctx, vmGUID, hyperv.NetworkAdapterOptions{ElementName: "eth0-writetest"})
	if err != nil {
		t.Fatalf("AddNetworkAdapter: %v", err)
	}
	if nicRes.JobRef != "" {
		if err := wsmanClient.WaitForJob(ctx, nicRes.JobRef); err != nil {
			t.Fatalf("WaitForJob(AddNetworkAdapter): %v", err)
		}
	}

	scsiRes, err := wsmanClient.AddScsiController(ctx, vmGUID)
	if err != nil {
		t.Fatalf("AddScsiController: %v", err)
	}
	if scsiRes.JobRef != "" {
		if err := wsmanClient.WaitForJob(ctx, scsiRes.JobRef); err != nil {
			t.Fatalf("WaitForJob(AddScsiController): %v", err)
		}
	}

	const isoPath = `H:\ISO\ubuntu-24.04.4-live-server-amd64.iso`
	dvdRes, err := wsmanClient.AttachDVD(ctx, vmGUID, hyperv.AttachDVDOptions{
		ControllerType: hyperv.ControllerTypeSCSI, ControllerNumber: 0, ControllerLocation: 0, Path: isoPath,
	})
	if err != nil {
		t.Fatalf("AttachDVD: %v", err)
	}
	if dvdRes.JobRef != "" {
		if err := wsmanClient.WaitForJob(ctx, dvdRes.JobRef); err != nil {
			t.Fatalf("WaitForJob(AttachDVD): %v", err)
		}
	}

	baseline, err := cc.GetVmFirmware(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVmFirmware (baseline): %v", err)
	}
	if len(baseline.BootOrders) != 2 {
		t.Fatalf("baseline BootOrders件数: got %d, want 2 (NIC+DVD)", len(baseline.BootOrders))
	}
	t.Logf("baseline.BootOrders: %+v", baseline.BootOrders)

	// 並び替え (逆順) にして、書き込みが実際に順序へ反映されるかを確認する。
	reordered := []api.Gen2BootOrder{baseline.BootOrders[1], baseline.BootOrders[0]}

	// --- 1. 表現可能な変更 ---
	if err := cc.CreateOrUpdateVmFirmware(ctx, vmName,
		reordered,
		baseline.EnableSecureBoot,      // 変更なし (On のまま)
		baseline.SecureBootTemplate,    // 変更なし (MicrosoftWindows のまま)
		api.IPProtocolPreference_IPv6,  // IPv4→IPv6 (両方非ゼロ、常に表現可能)
		api.ConsoleModeType_Com1,       // Default(0)→COM1(非ゼロ)、表現可能
		baseline.PauseAfterBootFailure, // 変更なし (Off のまま)
	); err != nil {
		t.Fatalf("CreateOrUpdateVmFirmware (representable changes): %v", err)
	}

	afterWrite, err := cc.GetVmFirmware(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVmFirmware (after write): %v", err)
	}
	t.Logf("afterWrite: %+v", afterWrite)

	if afterWrite.PreferredNetworkBootProtocol != api.IPProtocolPreference_IPv6 {
		t.Errorf("PreferredNetworkBootProtocol: got %v, want IPv6", afterWrite.PreferredNetworkBootProtocol)
	}
	if afterWrite.ConsoleMode != api.ConsoleModeType_Com1 {
		t.Errorf("ConsoleMode: got %v, want COM1", afterWrite.ConsoleMode)
	}
	if afterWrite.EnableSecureBoot != baseline.EnableSecureBoot {
		t.Errorf("EnableSecureBoot: got %v, want unchanged (%v)", afterWrite.EnableSecureBoot, baseline.EnableSecureBoot)
	}
	if len(afterWrite.BootOrders) != 2 {
		t.Fatalf("afterWrite BootOrders件数: got %d, want 2", len(afterWrite.BootOrders))
	}
	if afterWrite.BootOrders[0].Type != reordered[0].Type || afterWrite.BootOrders[1].Type != reordered[1].Type {
		t.Errorf("BootOrders の並び替えが反映されていない: got %+v, want順序 %+v", afterWrite.BootOrders, reordered)
	} else {
		t.Logf("✅ BootOrders の書き込み→読み取り round-trip を実機で確認 (resolveBootSourceRefs⇄resolveBootOrders)")
	}
	t.Logf("✅ 表現可能な変更 (ConsoleMode/PreferredNetworkBootProtocol/BootOrders並び替え) を go-wsman 経由で確認")

	// --- 2. ゼロ値ダウングレード (ConsoleMode COM1→Default) は PS へ委譲され、黙って失敗しない ---
	if err := cc.CreateOrUpdateVmFirmware(ctx, vmName,
		reordered,
		afterWrite.EnableSecureBoot,
		afterWrite.SecureBootTemplate,
		afterWrite.PreferredNetworkBootProtocol,
		api.ConsoleModeType_Default, // COM1→Default (非ゼロ→ゼロ、go-wsman では非表現)
		afterWrite.PauseAfterBootFailure,
	); err != nil {
		t.Fatalf("CreateOrUpdateVmFirmware (zero downgrade, PS委譲想定): %v", err)
	}

	afterDowngrade, err := cc.GetVmFirmware(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVmFirmware (after downgrade): %v", err)
	}
	if afterDowngrade.ConsoleMode != api.ConsoleModeType_Default {
		t.Errorf("ConsoleMode (PS委譲後): got %v, want Default。PS 委譲が黙って失敗し変更が反映されていない可能性あり", afterDowngrade.ConsoleMode)
	} else {
		t.Logf("✅ ゼロ値ダウングレードの PS 委譲が実際に反映されることを確認 (silent-drop していない)")
	}
}
