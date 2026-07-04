//go:build integration
// +build integration

package provider

// go-wsman 経由の SCSI ディスク write 経路 (#63 B) の実機統合テスト。
//
// 使い捨て Gen2 VM を作り、go-wsman #89 修正で動くようになった CreateVirtualHardDisk で VHD を
// 作成、provider の CreateOrUpdateVmHardDiskDrives (表示名→resolveVMGUID→ensureScsiController→
// AttachVHD) で SCSI にアタッチ、GetVmHardDiskDrives の逆引きで検証、DeleteVmHardDiskDrive で
// デタッチまで一周する。稼働中 VM は触らない。go-wsman は CIM 専用でファイル削除できないため
// 作成した VHD は残留する (パスをログ出力)。HYPERV_TEST_ALLOW_MUTATION=1 で有効。
//
// 実行例:
//
//	HYPERV_HOST=10.0.0.100 HYPERV_USER=terraform HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	HYPERV_TEST_ALLOW_MUTATION=1 \
//	go test -tags integration ./internal/provider/ -run TestRealHostScsiDiskWriteWsman -v

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	gowsman "github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"
)

func TestRealHostScsiDiskWriteWsman(t *testing.T) {
	if os.Getenv("HYPERV_TEST_ALLOW_MUTATION") == "" {
		t.Skip("HYPERV_TEST_ALLOW_MUTATION 未設定（VM/VHD 作成を伴う破壊的テスト）")
	}
	c := realHostConfigFromEnv(t)
	wsmanClient, err := newWsmanClient(c)
	if err != nil {
		t.Fatalf("newWsmanClient: %v", err)
	}
	cc := &hyperv_wsman.ClientConfig{WsmanClient: wsmanClient}
	ctx := context.Background()

	const vmName = "tf-wsman-scsi-write-test"
	vhdDir := os.Getenv("HYPERV_TEST_VHD_DIR")
	if vhdDir == "" {
		vhdDir = `D:\Hyper-V`
	}
	vhdPath := fmt.Sprintf(`%s\tf-wsman-scsi-write-%d.vhdx`, vhdDir, time.Now().UnixNano())

	// 前回残骸を消し、テスト後は VM を確実に削除 (VHD ファイルは残留)。
	_ = cc.DeleteVm(ctx, vmName)
	t.Cleanup(func() {
		if err := cc.DeleteVm(ctx, vmName); err != nil {
			t.Logf("cleanup DeleteVm: %v", err)
		}
	})

	// 1. 使い捨て Gen2 VM を実体化 (provider の CreateVm。メモリ/CPU/世代を持つ)。
	const memByt = 536870912 // 512 MiB
	if err := cc.CreateVm(ctx, vmName,
		"", 2, // path(既定), generation=2(Gen2)
		api.CriticalErrorAction_Pause, 0,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 0,
		api.OnOffState_Off, 0,
		memByt, memByt, memByt,
		"scsi-write-test", 1,
		"", "", true, false,
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}

	// 2. VHD 作成 (#89 修正で実機動作)。
	if _, err := wsmanClient.CreateVirtualHardDisk(ctx, &gowsman.Msvm_VirtualHardDiskSettingData{
		Path:              vhdPath,
		VirtualDiskFormat: gowsman.VHDFormatVHDX,
		VirtualDiskType:   gowsman.VHDTypeDynamic,
		MaxInternalSize:   1 << 30, // 1 GiB
	}); err != nil {
		t.Fatalf("CreateVirtualHardDisk: %v", err)
	}
	t.Logf("VHD 作成 (残留・手動削除要): %s", vhdPath)

	// 3. provider の disk 配線で SCSI にアタッチ (表示名指定)。
	//    シェル VM には SCSI Controller が無いので ensureScsiController が追加してから AttachVHD。
	//    未対応オプションのガードを通すため既定値 (DiskNumber=MaxUint32 / Primordial / ゼロ GUID) を明示。
	desired := []api.VmHardDiskDrive{{
		ControllerType:          api.ControllerType_Scsi,
		ControllerNumber:        0,
		ControllerLocation:      0,
		Path:                    vhdPath,
		DiskNumber:              4294967295,
		ResourcePoolName:        "Primordial",
		QosPolicyId:             "00000000-0000-0000-0000-000000000000",
		OverrideCacheAttributes: api.CacheAttributes_Default,
	}}
	if err := cc.CreateOrUpdateVmHardDiskDrives(ctx, vmName, desired); err != nil {
		t.Fatalf("CreateOrUpdateVmHardDiskDrives: %v", err)
	}

	// 4. 逆引き Get で SCSI に VHD が付いたことを検証。
	got, err := cc.GetVmHardDiskDrives(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVmHardDiskDrives: %v", err)
	}
	t.Logf("attach 後: disks=%d", len(got))
	if len(got) != 1 {
		t.Fatalf("SCSI ディスク 1 本のはず, got %d", len(got))
	}
	if got[0].ControllerType != api.ControllerType_Scsi {
		t.Errorf("ControllerType: got %v, want Scsi", got[0].ControllerType)
	}
	if got[0].Path != vhdPath {
		t.Errorf("Path: got %q, want %q", got[0].Path, vhdPath)
	}

	// 5. detach 検証 (#97): DeleteVmHardDiskDrive で Storage→Drive の 2 段削除を行い、
	//    逆引き Get が 0 本になることを確認する。子 SASD を残したまま Drive を消すと
	//    実機は 0x80041001 で失敗するため、ここが通れば 2 段削除が正しいことの実証になる。
	if err := cc.DeleteVmHardDiskDrive(ctx, vmName, 0); err != nil {
		t.Fatalf("DeleteVmHardDiskDrive: %v", err)
	}
	after, err := cc.GetVmHardDiskDrives(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVmHardDiskDrives (detach 後): %v", err)
	}
	t.Logf("detach 後: disks=%d", len(after))
	if len(after) != 0 {
		t.Fatalf("detach 後は 0 本のはず, got %d", len(after))
	}
	// VM の後始末は t.Cleanup の DeleteVm が担う (VHD ファイルは CIM では消せず残留)。
}
