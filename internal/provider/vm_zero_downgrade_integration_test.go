//go:build integration
// +build integration

package provider

// #132 の実機回帰テスト: VM レベル設定のゼロ値ダウングレード (非空 notes → 空) が
// 実際に反映されることを確認する。
//
// go-wsman の marshalEmbeddedInstance はゼロ値フィールドを送らないため、CIM 経路のままでは
// この変更は ModifySystemSettings に乗らず「成功報告なのに実機は変わらない」になる。
// vmLevelZeroDowngrade が検知して PS 経路へ委譲することで初めて反映される。
//
// 修正前はこのテストが「notes が変わっていない」で落ちる。
//
// 実行例:
//
//	HYPERV_HOST=<hyperv-host> HYPERV_USER=<user> HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	go test -tags integration ./internal/provider/ -run TestRealHostVmLevelZeroDowngrade -v

import (
	"context"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

func TestRealHostVmLevelZeroDowngrade(t *testing.T) {
	c := realHostConfigFromEnv(t)
	cc := newRealHostWsmanClientConfig(t, c)
	ctx := context.Background()

	const vmName = "tf-wsman-zerodown-test"
	const defaultVMPath = `C:\ProgramData\Microsoft\Windows\Hyper-V`
	const memByt = 536870912
	_ = cc.DeleteVm(ctx, vmName)
	t.Cleanup(func() {
		if err := cc.DeleteVm(ctx, vmName); err != nil {
			t.Logf("cleanup DeleteVm: %v", err)
		}
	})

	if err := cc.CreateVm(ctx, vmName,
		"", 1, // homelab に合わせて Generation 1
		// timeout は schema 既定 (30)。0 のままだと委譲先の Set-VM が
		// ParameterArgumentValidationError で落ちる。
		api.CriticalErrorAction_Pause, 30,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 0,
		api.OnOffState_Off, 0,
		memByt, memByt, memByt,
		"before-downgrade", 1,
		// パスは schema 既定を渡す。空は「指定なし」の意味であり、Set-VM も空文字を
		// 受け付けない (実機確認)。
		// automatic_checkpoints_enabled も同じ理由で実機既定 (true) に合わせる。
		// DefineSystem で作った VM はホスト既定を引き継ぐため、schema 既定の false を
		// 渡すと validateCheckpointFieldsUnchanged で弾かれる (#125 / #134)。
		// 実クラスタの VM は PS 作成で False なので、この差は go-wsman 作成 VM 固有。
		defaultVMPath, defaultVMPath, true, true,
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}

	before, err := cc.GetVm(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVm (before): %v", err)
	}
	t.Logf("BEFORE Notes=%q", before.Notes)
	if before.Notes == "" {
		t.Fatalf("前提が崩れている: 作成直後の Notes が空。以降の判定が無意味になる")
	}

	// 本題: notes を空にする = ゼロ値ダウングレード。
	if err := cc.UpdateVm(ctx, vmName,
		// timeout は schema 既定 (30)。0 のままだと委譲先の Set-VM が
		// ParameterArgumentValidationError で落ちる。
		api.CriticalErrorAction_Pause, 30,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 0,
		api.OnOffState_Off, 0,
		memByt, memByt, memByt,
		"", 1,
		defaultVMPath, defaultVMPath, true, true,
	); err != nil {
		t.Fatalf("UpdateVm: %v", err)
	}

	// 成功報告を信用せず読み直す。
	after, err := cc.GetVm(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVm (after): %v", err)
	}
	t.Logf("AFTER  Notes=%q", after.Notes)
	if after.Notes != "" {
		t.Fatalf("🔴 判定: 黙殺。UpdateVm は成功したのに Notes が消えていない (got %q)", after.Notes)
	}
	t.Logf("🎯 判定: ゼロ値ダウングレードが PS 委譲で実際に反映される")
}
