//go:build integration
// +build integration

package provider

// go-wsman 経由の vm_status (start/stop) 配線 (#70 / Phase C-8) の実機統合テスト。
//
// 使い捨て Gen2 VM を作り、UpdateVmStatus で start→Running / stop→Off を一周し、
// GetVmStatus の逆引きで状態を検証する。graceful shutdown(turnOff=false)も別途確認する。
//
// 実行例:
//
//	HYPERV_HOST=10.0.0.100 HYPERV_USER=terraform HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	HYPERV_TEST_ALLOW_MUTATION=1 \
//	go test -tags integration ./internal/provider/ -run TestRealHostVmStatusWsman -v -timeout 300s

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"
)

func TestRealHostVmStatusWsman(t *testing.T) {
	if os.Getenv("HYPERV_TEST_ALLOW_MUTATION") == "" {
		t.Skip("HYPERV_TEST_ALLOW_MUTATION 未設定（VM 作成・起動を伴う破壊的テスト）")
	}
	c := realHostConfigFromEnv(t)
	wsmanClient, err := newWsmanClient(c)
	if err != nil {
		t.Fatalf("newWsmanClient: %v", err)
	}
	cc := &hyperv_wsman.ClientConfig{WsmanClient: wsmanClient}
	_, runPS := newRealHostHelper(t, c)
	ctx := context.Background()

	const vmName = "tf-wsman-vmstatus-test"
	isoPath := os.Getenv("HYPERV_TEST_TALOS_ISO")
	if isoPath == "" {
		isoPath = `H:\ISO\metal-amd64.iso`
	}

	_ = cc.DeleteVm(ctx, vmName)
	t.Cleanup(func() {
		_ = runPS("Stop-VM -Name '" + vmName + "' -TurnOff -Force -ErrorAction SilentlyContinue")
		if err := cc.DeleteVm(ctx, vmName); err != nil {
			t.Logf("cleanup DeleteVm: %v", err)
		}
	})

	// 使い捨て Gen2 VM (2 GiB。graceful shutdown 検証で Talos を起動する)。
	const memByt = 2147483648
	if err := cc.CreateVm(ctx, vmName,
		"", 2,
		api.CriticalErrorAction_Pause, 0,
		api.StartAction_Nothing, 0,
		api.StopAction_TurnOff,
		api.CheckpointType_Production,
		false, false, 0,
		api.OnOffState_Off, 0,
		memByt, memByt, memByt,
		"vmstatus-test", 1,
		"", "", true, false,
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}

	assertState := func(label string, want api.VmState) {
		got, err := cc.GetVmStatus(ctx, vmName)
		if err != nil {
			t.Fatalf("GetVmStatus (%s): %v", label, err)
		}
		t.Logf("[%s] State=%d (want %d)", label, got.State, want)
		if got.State != want {
			t.Fatalf("[%s] State: got %d, want %d", label, got.State, want)
		}
	}

	// 1. 作成直後は Off。
	assertState("作成直後", api.VmState_Off)

	// 2. start → Running。
	if err := cc.UpdateVmStatus(ctx, vmName, 120, 2, api.VmState_Running, false); err != nil {
		t.Fatalf("UpdateVmStatus(Running): %v", err)
	}
	assertState("start 後", api.VmState_Running)

	// 3. 冪等: 既に Running への再 start は no-op で成功。
	if err := cc.UpdateVmStatus(ctx, vmName, 120, 2, api.VmState_Running, false); err != nil {
		t.Fatalf("UpdateVmStatus(Running) 冪等再適用: %v", err)
	}
	assertState("再 start(冪等)後", api.VmState_Running)

	// 4. stop (turnOff=true, 強制電源断) → Off。
	if err := cc.UpdateVmStatus(ctx, vmName, 120, 2, api.VmState_Off, true); err != nil {
		t.Fatalf("UpdateVmStatus(Off, turnOff): %v", err)
	}
	assertState("turnOff 後", api.VmState_Off)

	// 5. graceful shutdown 検証: Talos を起動→heartbeat 待ち→shutdown(turnOff=false)。
	//    Integration Services 経由なので、Talos の hv_utils が上がってから要求する。
	t.Run("graceful_shutdown", func(t *testing.T) {
		if out := strings.TrimSpace(runPS("if (Test-Path -LiteralPath '" + isoPath + "') {'yes'} else {'no'}")); out != "yes" {
			t.Skipf("ISO 無し(%s)のため graceful shutdown 検証はスキップ", isoPath)
		}
		guid, err := wsmanClient.FindComputerSystemByElementName(ctx, vmName)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		_ = runPS("Set-VMFirmware -VMName '" + vmName + "' -EnableSecureBoot Off")
		// ISO を SCSI にマウントして起動 (Talos maintenance mode で IS/heartbeat が上がる)。
		if err := cc.CreateOrUpdateVmDvdDrives(ctx, vmName, []api.VmDvdDrive{
			{VmName: vmName, ControllerNumber: 0, ControllerLocation: 0, Path: isoPath, ResourcePoolName: "Primordial"},
		}); err != nil {
			t.Fatalf("mount ISO: %v", err)
		}
		if err := cc.UpdateVmStatus(ctx, vmName, 120, 2, api.VmState_Running, false); err != nil {
			t.Fatalf("start: %v", err)
		}
		// heartbeat が Ok になるまで待つ (ゲスト IS 起動)。
		hbCmd := "(Get-VMIntegrationService -VMName '" + vmName + "' -Name Heartbeat).PrimaryStatusDescription"
		booted := false
		for i := 0; i < 24; i++ {
			if strings.EqualFold(strings.TrimSpace(runPS(hbCmd)), "OK") {
				booted = true
				break
			}
			runPS("Start-Sleep -Seconds 5")
		}
		if !booted {
			t.Skip("Talos の Heartbeat が上がらず graceful shutdown は検証不能(環境要因)。turnOff 経路は検証済")
		}
		// graceful shutdown 要求 (turnOff=false)。
		if err := cc.UpdateVmStatus(ctx, vmName, 120, 2, api.VmState_Off, false); err != nil {
			t.Fatalf("graceful shutdown 失敗: %v (ShutdownVM=RequestStateChange(4) が Hyper-V で機能しない可能性)", err)
		}
		_ = guid
		assertState("graceful shutdown 後", api.VmState_Off)
		t.Log("[graceful] OK: ShutdownVM(Integration Services 経由)で Off へ遷移")
	})
}
