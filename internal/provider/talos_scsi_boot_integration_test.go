//go:build integration
// +build integration

package provider

// #63 「Gen2 SCSI boot 土台」の最終受け入れテスト。go-wsman で配線した SCSI 接続の
// DVD (Talos metal ISO) から、Gen2 VM が実際にブートすることを実機で確認する。
//
// 分担:
//   - go-wsman (#63 で作った経路 = テスト対象): Gen2 VM 作成 / AddScsiController /
//     AttachDVD(SCSI) / StartVM / TurnOffVM。
//   - PowerShell (歴戦の Set-VMFirmware = 検証セットアップ): SecureBoot Off と
//     FirstBootDevice=DVD の指定、および Heartbeat 統合サービスによるブート検出。
//     boot order / firmware は #63 のスコープ外 (provider vm_firmware の領分) であり、
//     WMI の BootSourceOrder を手組みすると golden 偽陽性を招くため OSS 相当の PS に委ねる。
//
// 「ブートした」判定は Hyper-V Heartbeat 統合サービスが Ok になること。Heartbeat は
// VMBus 越しでゲストカーネル + hv_utils が起動して初めて Ok になるため、SCSI 上の ISO から
// カーネルがロードされた証左になる (firmware 停止のままなら No Contact のまま)。
// NIC は付けない (Talos は maintenance mode で待機。Heartbeat は VMBus なので NIC 不要)。
// 稼働中の実クラスタ VM には一切触れない。VHD は作らないので残留物は無し。
//
// 実行例:
//
//	HYPERV_HOST=<hyperv-host> HYPERV_USER=<user> HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	HYPERV_TEST_ALLOW_MUTATION=1 \
//	go test -tags integration ./internal/provider/ -run TestRealHostTalosScsiBootWsman -v -timeout 400s

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	gowsman "github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"
)

func TestRealHostTalosScsiBootWsman(t *testing.T) {
	if os.Getenv("HYPERV_TEST_ALLOW_MUTATION") == "" {
		t.Skip("HYPERV_TEST_ALLOW_MUTATION 未設定（VM 作成・起動を伴う破壊的テスト）")
	}
	c := realHostConfigFromEnv(t)
	wsmanClient, err := newWsmanClient(c)
	if err != nil {
		t.Fatalf("newWsmanClient: %v", err)
	}
	cc := &hyperv_wsman.ClientConfig{WsmanClient: wsmanClient}
	_, runPS := newRealHostHelper(t, c) // firmware 設定と Heartbeat 読み取り用
	ctx := context.Background()

	const vmName = "tf-wsman-talos-scsi-boot-test"
	isoPath := os.Getenv("HYPERV_TEST_TALOS_ISO")
	if isoPath == "" {
		isoPath = `H:\ISO\metal-amd64.iso` // homelab の talos_iso_path と一致
	}

	// ISO が実機に無ければスキップ (テスト対象外の前提条件)。
	if out := strings.TrimSpace(runPS(fmt.Sprintf(`if (Test-Path -LiteralPath '%s') {'yes'} else {'no'}`, isoPath))); out != "yes" {
		t.Skipf("Talos ISO %q が実機に存在しない (out=%q)", isoPath, out)
	}

	// 前回残骸を消し、テスト後は確実に停止 → 削除する。
	_, _ = wsmanClient.TurnOffVM(ctx, vmName) // 表示名でなく GUID 前提なので不在時は無害に失敗
	_ = cc.DeleteVm(ctx, vmName)
	t.Cleanup(func() {
		_ = runPS(fmt.Sprintf(`Stop-VM -Name '%s' -TurnOff -Force -ErrorAction SilentlyContinue`, vmName))
		if err := cc.DeleteVm(ctx, vmName); err != nil {
			t.Logf("cleanup DeleteVm: %v", err)
		}
	})

	// 1. 使い捨て Gen2 VM を作成 (2 GiB。Talos は 512 MiB では起動しない)。
	const memByt = 2147483648 // 2 GiB
	if err := cc.CreateVm(ctx, vmName,
		"", 2, // path(既定), generation=2
		api.CriticalErrorAction_Pause, 0,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 0,
		api.OnOffState_Off, 0,
		memByt, memByt, memByt,
		"talos-scsi-boot-test", 2,
		"", "", true, false,
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}

	// 表示名 → GUID (go-wsman の storage/state 操作は GUID を要求する)。
	cs, err := wsmanClient.FindComputerSystemByElementName(ctx, vmName)
	if err != nil {
		t.Fatalf("FindComputerSystemByElementName: %v", err)
	}
	guid := cs.Name

	// 2. SCSI Controller を追加 (DefineSystem のシェル VM は Gen2 でも SCSI を持たない #88)。
	scsi, err := wsmanClient.AddScsiController(ctx, guid)
	if err != nil {
		t.Fatalf("AddScsiController: %v", err)
	}
	if err := wsmanClient.WaitForJob(ctx, scsi.JobRef); err != nil {
		t.Fatalf("AddScsiController wait: %v", err)
	}

	// 3. Talos ISO を SCSI の DVD としてマウント (#63 のテスト対象経路)。
	if _, err := wsmanClient.AttachDVD(ctx, guid, gowsman.AttachDVDOptions{
		ControllerType:     gowsman.ControllerTypeSCSI,
		ControllerNumber:   0,
		ControllerLocation: 0,
		Path:               isoPath,
	}); err != nil {
		t.Fatalf("AttachDVD: %v", err)
	}

	// 4. firmware: SecureBoot Off + 最初のブートデバイスを DVD に (VM 停止中に設定)。
	//    Talos は Microsoft UEFI テンプレートでは検証に失敗するため SecureBoot Off が必須。
	if out := runPS(fmt.Sprintf(
		`Set-VMFirmware -VMName '%s' -EnableSecureBoot Off; `+
			`$d = Get-VMDvdDrive -VMName '%s'; `+
			`Set-VMFirmware -VMName '%s' -FirstBootDevice $d; `+
			`(Get-VMFirmware -VMName '%s').BootOrder[0].Device.GetType().Name`,
		vmName, vmName, vmName, vmName)); !strings.Contains(out, "DvdDrive") {
		t.Fatalf("Set-VMFirmware で FirstBootDevice=DVD にできていない: %q", out)
	}

	// 5. 起動 (go-wsman StartVM = RequestStateChange(Enabled))。
	jobRef, err := wsmanClient.StartVM(ctx, guid)
	if err != nil {
		t.Fatalf("StartVM: %v", err)
	}
	if jobRef != "" {
		if err := wsmanClient.WaitForJob(ctx, jobRef); err != nil {
			t.Fatalf("StartVM wait: %v", err)
		}
	}
	t.Logf("VM 起動要求完了。Heartbeat を待機する…")

	// 6. Heartbeat が Ok になるまでポーリング (ゲストカーネル + hv_utils 起動 = SCSI ISO から boot 成功)。
	const bootTimeout = 240 * time.Second
	heartbeatCmd := fmt.Sprintf(`(Get-VMIntegrationService -VMName '%s' -Name Heartbeat).PrimaryStatusDescription`, vmName)
	stateCmd := fmt.Sprintf(`(Get-VM -Name '%s').State`, vmName)
	deadline := time.Now().Add(bootTimeout)
	var lastHB, lastState string
	booted := false
	for time.Now().Before(deadline) {
		lastHB = strings.TrimSpace(runPS(heartbeatCmd))
		lastState = strings.TrimSpace(runPS(stateCmd))
		t.Logf("[boot 待機] state=%s heartbeat=%q", lastState, lastHB)
		if strings.EqualFold(lastHB, "OK") {
			booted = true
			break
		}
		time.Sleep(10 * time.Second)
	}
	if !booted {
		t.Fatalf("Talos が SCSI ISO からブートしなかった (timeout %s): state=%s heartbeat=%q", bootTimeout, lastState, lastHB)
	}

	uptime := strings.TrimSpace(runPS(fmt.Sprintf(`(Get-VM -Name '%s').Uptime.ToString()`, vmName)))
	t.Logf("✅ Talos が SCSI 接続の ISO からブート (Heartbeat=OK, uptime=%s)。#63 SCSI boot 土台の実機実証。", uptime)

	// 7. 停止 (go-wsman TurnOffVM)。後始末は t.Cleanup が DeleteVm を担う。
	if _, err := wsmanClient.TurnOffVM(ctx, guid); err != nil {
		t.Logf("TurnOffVM: %v", err)
	}
}
