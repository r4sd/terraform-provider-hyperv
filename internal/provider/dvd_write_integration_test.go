//go:build integration
// +build integration

package provider

// go-wsman 経由の DVD (ISO マウント) 配線 (#64) の実機統合テスト。
//
// 使い捨て Gen2 VM を作り、provider の CreateOrUpdateVmDvdDrives で Talos ISO を SCSI に
// マウント、GetVmDvdDrives の逆引きで検証、desired を空にして「boot 後デタッチ」相当の
// detach まで一周する (Storage→Drive の 2 段削除 #97 経由)。稼働中 VM は触らない。
//
// 実行例:
//
//	HYPERV_HOST=10.0.0.100 HYPERV_USER=terraform HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	HYPERV_TEST_ALLOW_MUTATION=1 \
//	go test -tags integration ./internal/provider/ -run TestRealHostDvdWriteWsman -v

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"
)

func TestRealHostDvdWriteWsman(t *testing.T) {
	if os.Getenv("HYPERV_TEST_ALLOW_MUTATION") == "" {
		t.Skip("HYPERV_TEST_ALLOW_MUTATION 未設定（VM 作成を伴う破壊的テスト）")
	}
	c := realHostConfigFromEnv(t)
	wsmanClient, err := newWsmanClient(c)
	if err != nil {
		t.Fatalf("newWsmanClient: %v", err)
	}
	cc := &hyperv_wsman.ClientConfig{WsmanClient: wsmanClient}
	_, runPS := newRealHostHelper(t, c) // ISO 存在確認用
	ctx := context.Background()

	const vmName = "tf-wsman-dvd-write-test"
	isoPath := os.Getenv("HYPERV_TEST_TALOS_ISO")
	if isoPath == "" {
		isoPath = `H:\ISO\metal-amd64.iso`
	}
	if out := strings.TrimSpace(runPS("if (Test-Path -LiteralPath '" + isoPath + "') {'yes'} else {'no'}")); out != "yes" {
		t.Skipf("ISO %q が実機に無い (out=%q)", isoPath, out)
	}

	_ = cc.DeleteVm(ctx, vmName)
	t.Cleanup(func() {
		if err := cc.DeleteVm(ctx, vmName); err != nil {
			t.Logf("cleanup DeleteVm: %v", err)
		}
	})

	// 1. 使い捨て Gen2 VM。
	const memByt = 536870912 // 512 MiB (起動しないので最小で可)
	if err := cc.CreateVm(ctx, vmName,
		"", 2,
		api.CriticalErrorAction_Pause, 0,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 0,
		api.OnOffState_Off, 0,
		memByt, memByt, memByt,
		"dvd-write-test", 1,
		"", "", true, false,
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}

	// 2. provider の dvd 配線で Talos ISO を SCSI にマウント (Gen2 → 自動で SCSI)。
	desired := []api.VmDvdDrive{{
		VmName:             vmName,
		ControllerNumber:   0,
		ControllerLocation: 0,
		Path:               isoPath,
		ResourcePoolName:   "Primordial",
	}}
	if err := cc.CreateOrUpdateVmDvdDrives(ctx, vmName, desired); err != nil {
		t.Fatalf("CreateOrUpdateVmDvdDrives: %v", err)
	}

	// 3. 逆引き Get で ISO が付いたことを検証。
	got, err := cc.GetVmDvdDrives(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVmDvdDrives: %v", err)
	}
	t.Logf("mount 後: dvds=%d", len(got))
	if len(got) != 1 {
		t.Fatalf("DVD 1 本のはず, got %d", len(got))
	}
	if !strings.EqualFold(got[0].Path, isoPath) {
		t.Errorf("Path: got %q, want %q", got[0].Path, isoPath)
	}

	// 4. 冪等性: 同じ desired を再適用しても変化しない (mount→mount で 1 本のまま)。
	if err := cc.CreateOrUpdateVmDvdDrives(ctx, vmName, desired); err != nil {
		t.Fatalf("CreateOrUpdateVmDvdDrives (再適用): %v", err)
	}
	if again, err := cc.GetVmDvdDrives(ctx, vmName); err != nil || len(again) != 1 {
		t.Fatalf("冪等でない: err=%v len=%d (want 1)", err, len(again))
	}

	// 5. boot 後デタッチ相当: desired を空にして detach (Storage→Drive 2 段削除 #97)。
	if err := cc.CreateOrUpdateVmDvdDrives(ctx, vmName, nil); err != nil {
		t.Fatalf("CreateOrUpdateVmDvdDrives (detach): %v", err)
	}
	after, err := cc.GetVmDvdDrives(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVmDvdDrives (detach 後): %v", err)
	}
	t.Logf("detach 後: dvds=%d", len(after))
	if len(after) != 0 {
		t.Fatalf("detach 後は 0 本のはず, got %d", len(after))
	}
}
