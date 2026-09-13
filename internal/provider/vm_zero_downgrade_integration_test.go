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

// TestRealHostMultilineNotes は複数行 notes が round-trip することを実機で検証する (#145)。
//
// Hyper-V の Notes は MOF 上 string[] だが実質単一値で、複数要素を送ると先頭以外が
// 捨てられる。修正前は改行で分割して送っていたため 1 行目だけになり、read は実値を
// 返すので恒常 diff + apply のたびに VM 停止という #106 型のループになっていた。
func TestRealHostMultilineNotes(t *testing.T) {
	c := realHostConfigFromEnv(t)
	cc := newRealHostWsmanClientConfig(t, c)
	ctx := context.Background()

	const vmName = "tf-wsman-notes-test"
	const memByt = 536870912
	const defaultVMPath = `C:\ProgramData\Microsoft\Windows\Hyper-V`
	const multiline = "alpha\nbeta\ngamma"

	_ = cc.DeleteVm(ctx, vmName)
	t.Cleanup(func() {
		if err := cc.DeleteVm(ctx, vmName); err != nil {
			t.Logf("cleanup DeleteVm: %v", err)
		}
	})

	if err := cc.CreateVm(ctx, vmName,
		"", 1,
		api.CriticalErrorAction_Pause, 30,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 536870912,
		api.OnOffState_Off, 134217728,
		memByt, memByt, memByt,
		multiline, 1,
		defaultVMPath, defaultVMPath, true, true,
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}

	got, err := cc.GetVm(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVm: %v", err)
	}
	t.Logf("① create 後の Notes = %q", got.Notes)
	if got.Notes != multiline {
		t.Fatalf("🔴 create で複数行 notes が失われている: got %q, want %q", got.Notes, multiline)
	}

	// update でも同じこと。
	const updated = "one\ntwo\nthree\nfour"
	if err := cc.UpdateVm(ctx, vmName,
		api.CriticalErrorAction_Pause, 30,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 536870912,
		api.OnOffState_Off, 134217728,
		memByt, memByt, memByt,
		updated, 1,
		defaultVMPath, defaultVMPath, true, true,
	); err != nil {
		t.Fatalf("UpdateVm: %v", err)
	}
	after, err := cc.GetVm(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVm (after): %v", err)
	}
	t.Logf("② update 後の Notes = %q", after.Notes)
	if after.Notes != updated {
		t.Fatalf("🔴 update で複数行 notes が失われている: got %q, want %q", after.Notes, updated)
	}
	// --- CRLF の round-trip ---
	//
	// Windows のテキストは CRLF が標準で、Hyper-V マネージャーで手入力した notes も
	// CRLF になる。go-wsman は CR を文字参照 (&#xD;) にエスケープして送るが、
	// **読み戻しで CR が保持されるかは WinRM の応答形式次第** (Go の XML デコーダは
	// 生の \r を \n へ正規化し、文字参照の CR は保持する)。
	// 崩れる場合は「送信 CRLF → 読み戻し LF」で恒常 diff になるため実機に問う。
	const crlf = "one\r\ntwo\r\nthree"
	if err := cc.UpdateVm(ctx, vmName,
		api.CriticalErrorAction_Pause, 30,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 536870912,
		api.OnOffState_Off, 134217728,
		memByt, memByt, memByt,
		crlf, 1,
		defaultVMPath, defaultVMPath, true, true,
	); err != nil {
		t.Fatalf("UpdateVm (CRLF): %v", err)
	}
	gotCRLF, err := cc.GetVm(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVm (CRLF): %v", err)
	}
	t.Logf("③ CRLF 送信後の Notes = %q", gotCRLF.Notes)
	// 設計判断: Hyper-V は CR を保持しないので **送信側で LF へ正規化**し、
	// config 側の CRLF は schema の DiffSuppressFunc が吸収する。
	// したがって読み戻しは LF になるのが正しい。
	const wantLF = "one\ntwo\nthree"
	if gotCRLF.Notes != wantLF {
		t.Fatalf("🔴 got %q, want %q (送信時に LF へ正規化されるはず)", gotCRLF.Notes, wantLF)
	}
	if !api.DiffSuppressNotes("notes", gotCRLF.Notes, crlf, nil) {
		t.Errorf("🔴 config の CRLF と state の LF が差分扱いになる。恒常 diff になる")
	}
	t.Logf("🎯 CRLF は LF へ正規化され、DiffSuppress が config 側の CRLF を吸収する")

	// --- 末尾改行 (HCL の heredoc 相当) ---
	//
	// `<<-EOT ... EOT` は末尾に改行が付く。Hyper-V が末尾をトリムすると
	// state と config が食い違い、DiffSuppress では吸収できない (行数が変わるため)。
	// 実機で「トリムされない」ことを確認済みだが、退行を検出できるよう固定する。
	const trailing = "line1\nline2\n"
	if err := cc.UpdateVm(ctx, vmName,
		api.CriticalErrorAction_Pause, 30,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Production,
		false, false, 536870912,
		api.OnOffState_Off, 134217728,
		memByt, memByt, memByt,
		trailing, 1,
		defaultVMPath, defaultVMPath, true, true,
	); err != nil {
		t.Fatalf("UpdateVm (末尾改行): %v", err)
	}
	gotTrailing, err := cc.GetVm(ctx, vmName)
	if err != nil {
		t.Fatalf("GetVm (末尾改行): %v", err)
	}
	t.Logf("④ 末尾改行つき送信後の Notes = %q", gotTrailing.Notes)
	// 設計判断: 末尾改行は CIM 読み取りで落ちるので送信側で除去し、
	// config 側の末尾改行は DiffSuppress が吸収する。
	const wantTrimmed = "line1\nline2"
	if gotTrailing.Notes != wantTrimmed {
		t.Fatalf("🔴 got %q, want %q (送信時に末尾改行が除去されるはず)", gotTrailing.Notes, wantTrimmed)
	}
	if !api.DiffSuppressNotes("notes", gotTrailing.Notes, trailing, nil) {
		t.Errorf("🔴 heredoc の末尾改行が差分扱いになる。恒常 diff になる")
	}
	t.Logf("🎯 末尾改行は除去され、DiffSuppress が config 側の末尾改行を吸収する")

	t.Logf("🎯 判定: 複数行 notes が create / update とも round-trip する")
}
