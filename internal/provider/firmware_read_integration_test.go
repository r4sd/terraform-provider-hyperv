//go:build integration
// +build integration

package provider

// go-wsman 経由の GetVmFirmware (READ のみ、BootOrders 除く) の実機統合テスト。
//
// 使い捨て Gen2 VM を作成し、既定のファームウェア設定 (SecureBoot=On、SecureBootTemplate=
// MicrosoftWindows、PreferredNetworkBootProtocol=IPv4、ConsoleMode=Default、
// PauseAfterBootFailure=Off) が正しく読めることを確認する。BootOrders は未対応 (Slice D 継続)、
// デバイス無しの VM なら BootSourceOrder が空なのでエラーにならないことも合わせて確認する。
//
// 実行例:
//
//	HYPERV_HOST=<hyperv-host> HYPERV_USER=<user> HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	go test -tags integration ./internal/provider/ -run TestRealHostFirmwareReadWsman -v

import (
	"context"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"
)

func TestRealHostFirmwareReadWsman(t *testing.T) {
	c := realHostConfigFromEnv(t)
	wsmanClient, err := newWsmanClient(c)
	if err != nil {
		t.Fatalf("newWsmanClient: %v", err)
	}
	cc := &hyperv_wsman.ClientConfig{WsmanClient: wsmanClient}
	ctx := context.Background()

	const vmName = "tf-wsman-firmware-read-test"
	_ = cc.DeleteVm(ctx, vmName) // 前回残骸を掃除
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
		"firmware-read-test", 1,
		"", "", true, false,
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}

	firmware, err := cc.GetVmFirmware(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVmFirmware: %v", err)
	}
	t.Logf("firmware: %+v", firmware)

	if firmware.EnableSecureBoot != api.OnOffState_On {
		t.Errorf("EnableSecureBoot: got %v, want On (Gen2 既定)", firmware.EnableSecureBoot)
	}
	if firmware.SecureBootTemplate != "MicrosoftWindows" {
		t.Errorf("SecureBootTemplate: got %q, want \"MicrosoftWindows\"", firmware.SecureBootTemplate)
	}
	if firmware.PreferredNetworkBootProtocol != api.IPProtocolPreference_IPv4 {
		t.Errorf("PreferredNetworkBootProtocol: got %v, want IPv4 (既定)", firmware.PreferredNetworkBootProtocol)
	}
	if firmware.ConsoleMode != api.ConsoleModeType_Default {
		t.Errorf("ConsoleMode: got %v, want Default", firmware.ConsoleMode)
	}
	if firmware.PauseAfterBootFailure != api.OnOffState_Off {
		t.Errorf("PauseAfterBootFailure: got %v, want Off", firmware.PauseAfterBootFailure)
	}
	t.Logf("✅ Gen2 VM の既定ファームウェア設定を go-wsman 経由で確認")

	firmwares, err := cc.GetVmFirmwares(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVmFirmwares: %v", err)
	}
	if len(firmwares) != 1 {
		t.Fatalf("GetVmFirmwares: got %d件, want 1", len(firmwares))
	}
	t.Logf("✅ GetVmFirmwares (1件ラップ) も確認")
}

// TestRealHostFirmwareBootOrderWsman は BootSourceOrder→Gen2BootOrder の相関ロジックを実機で検証する。
// NIC + SCSI Controller + DVD (Ubuntu ISO) を付けた Gen2 VM で GetVmFirmware を呼び、
// BootOrders に NetworkAdapter と DvdDrive が正しく解決されることを確認する。
func TestRealHostFirmwareBootOrderWsman(t *testing.T) {
	c := realHostConfigFromEnv(t)
	wsmanClient, err := newWsmanClient(c)
	if err != nil {
		t.Fatalf("newWsmanClient: %v", err)
	}
	cc := &hyperv_wsman.ClientConfig{WsmanClient: wsmanClient}
	ctx := context.Background()

	const vmName = "tf-wsman-firmware-bootorder-test"
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
		"firmware-bootorder-test", 1,
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
	nicRes, err := wsmanClient.AddNetworkAdapter(ctx, vmGUID, hyperv.NetworkAdapterOptions{ElementName: "eth0-boottest"})
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

	firmware, err := cc.GetVmFirmware(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVmFirmware: %v", err)
	}
	t.Logf("firmware.BootOrders: %+v", firmware.BootOrders)

	if len(firmware.BootOrders) != 2 {
		t.Fatalf("BootOrders件数: got %d, want 2 (NIC+DVD)。相関解決に失敗し PS 委譲が発生した可能性あり", len(firmware.BootOrders))
	}
	var haveNIC, haveDVD bool
	for _, b := range firmware.BootOrders {
		switch b.Type {
		case api.Gen2BootType_NetworkAdapter:
			haveNIC = true
			if b.NetworkAdapterName != "eth0-boottest" {
				t.Errorf("NetworkAdapterName: got %q, want \"eth0-boottest\"", b.NetworkAdapterName)
			}
		case api.Gen2BootType_DvdDrive:
			haveDVD = true
			if b.Path != isoPath {
				t.Errorf("DvdDrive Path: got %q, want %q", b.Path, isoPath)
			}
		}
	}
	if !haveNIC || !haveDVD {
		t.Errorf("NIC/DVD 両方の BootOrder が解決されるべき: haveNIC=%v haveDVD=%v", haveNIC, haveDVD)
	}
	t.Logf("✅ BootSourceOrder→Gen2BootOrder 相関解決を実機で確認")
}
