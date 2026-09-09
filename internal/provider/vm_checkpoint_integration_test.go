//go:build integration
// +build integration

package provider

// #115 の実機テスト: vm_checkpoint の go-wsman shadow を CRUD 一巡で検証する。
//
// 実行例:
//
//	HYPERV_HOST=<hyperv-host> HYPERV_USER=<user> HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	HYPERV_TEST_ALLOW_MUTATION=1 \
//	go test -tags integration ./internal/provider/ -run TestRealHostVmCheckpointWsman -v

import (
	"context"
	"os"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

func TestRealHostVmCheckpointWsman(t *testing.T) {
	if os.Getenv("HYPERV_TEST_ALLOW_MUTATION") == "" {
		t.Skip("HYPERV_TEST_ALLOW_MUTATION 未設定（VM 作成を伴う破壊的テスト）")
	}
	c := realHostConfigFromEnv(t)
	cc := newRealHostWsmanClientConfig(t, c)
	ctx := context.Background()

	const (
		vmName    = "tf-wsman-ckpt-crud"
		cpName    = "tf-checkpoint-alpha"
		cpName2   = "tf-checkpoint-beta"
		memByt    = 536870912
		defaultVM = `C:\ProgramData\Microsoft\Windows\Hyper-V`
	)
	_ = cc.DeleteVm(ctx, vmName)
	t.Cleanup(func() {
		_ = cc.DeleteVmCheckpoint(ctx, vmName, cpName)
		_ = cc.DeleteVmCheckpoint(ctx, vmName, cpName2)
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
		"ckpt-crud", 1,
		defaultVM, defaultVM, true, true,
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}

	// --- 1. 不在時は空を返す (PS 版が空 JSON を返す挙動とのパリティ) ---
	if got, err := cc.GetVmCheckpoint(ctx, vmName, cpName); err != nil {
		t.Fatalf("GetVmCheckpoint (不在): %v", err)
	} else if got.Name != "" {
		t.Fatalf("🔴 不在のはずが %+v", got)
	}
	t.Logf("① 不在時は空を返す")

	// --- 2. 作成 → 指定名になっていること (リネームの黙殺検出) ---
	if err := cc.CreateVmCheckpoint(ctx, vmName, cpName); err != nil {
		t.Fatalf("CreateVmCheckpoint: %v", err)
	}
	got, err := cc.GetVmCheckpoint(ctx, vmName, cpName)
	if err != nil {
		t.Fatalf("GetVmCheckpoint: %v", err)
	}
	t.Logf("② 作成後: Name=%q Id=%q ParentId=%q Type=%q CreationTime=%q",
		got.Name, got.Id, got.ParentId, got.CheckpointType, got.CreationTime)
	if got.Name != cpName {
		t.Fatalf("🔴 リネームが反映されていない: %q", got.Name)
	}
	if got.Id == "" {
		t.Fatal("🔴 Id が空")
	}
	if got.CreationTime == "" {
		t.Fatal("🔴 CreationTime が空")
	}
	if got.CheckpointType == "" {
		t.Fatal("🔴 CheckpointType が空")
	}

	// --- 3. 2 つ目を作ると親子関係が付くこと (ParentId の実機検証) ---
	if err := cc.CreateVmCheckpoint(ctx, vmName, cpName2); err != nil {
		t.Fatalf("CreateVmCheckpoint (2つ目): %v", err)
	}
	got2, err := cc.GetVmCheckpoint(ctx, vmName, cpName2)
	if err != nil {
		t.Fatalf("GetVmCheckpoint (2つ目): %v", err)
	}
	t.Logf("③ 2つ目: Name=%q Id=%q ParentId=%q", got2.Name, got2.Id, got2.ParentId)
	if got2.ParentId != got.Id {
		t.Fatalf("🔴 ParentId が 1 つ目の Id と一致しない: got %q, want %q", got2.ParentId, got.Id)
	}
	t.Logf("🎯 ParentId が親チェックポイントの Id を正しく指す")

	// --- 4. 同名の再作成は弾く ---
	if err := cc.CreateVmCheckpoint(ctx, vmName, cpName); err == nil {
		t.Fatal("🔴 同名チェックポイントの再作成はエラーになるべき")
	}
	t.Logf("④ 同名の再作成を拒否")

	// --- 5. 復元 (停止中) ---
	if err := cc.RestoreVmCheckpoint(ctx, vmName, cpName); err != nil {
		t.Fatalf("RestoreVmCheckpoint: %v", err)
	}
	t.Logf("⑤ 停止中 VM の復元 OK")

	// --- 5b. 稼働中 VM でも復元できるか ---
	//
	// restore_on_destroy はカオスエンジニアリング用途で、対象は稼働中 VM が前提。
	// ApplySnapshot の一次資料は Invalid State (32775) を戻り値に列挙しているが
	// 状態要件を明記していないため実機に問う (批判的レビュー指摘)。
	// OS 未インストールなのでブートは失敗するが VM の状態は Running になる。
	cs, err := cc.WsmanClient.FindComputerSystemByElementName(ctx, vmName)
	if err != nil {
		t.Fatalf("FindComputerSystemByElementName: %v", err)
	}
	if jr, err := cc.WsmanClient.StartVM(ctx, cs.Name); err != nil {
		t.Logf("⚠️ StartVM 不可のため稼働中復元の検証はスキップ: %v", err)
	} else {
		if err := cc.WsmanClient.WaitForJob(ctx, jr); err != nil {
			t.Fatalf("WaitForJob(StartVM): %v", err)
		}
		running, err := cc.WsmanClient.FindComputerSystemByElementName(ctx, vmName)
		if err != nil {
			t.Fatalf("Find (running): %v", err)
		}
		t.Logf("⑤b VM 状態 EnabledState=%d (2=Running)", running.EnabledState)

		// CIM の ApplySnapshot は稼働中 VM を受け付けない (種別に関係なく 32775)。
		// PS へ委譲して復元できること、かつ **スナップショット時点の状態に戻る**ことを見る。
		// Production チェックポイントは仕様上、復元後は停止状態になる。
		if err := cc.RestoreVmCheckpoint(ctx, vmName, cpName); err != nil {
			t.Fatalf("🔴 稼働中 VM の復元が失敗する。restore_on_destroy が実運用で使えない: %v", err)
		}
		restored, err := cc.WsmanClient.FindComputerSystemByElementName(ctx, vmName)
		if err != nil {
			t.Fatalf("Find (restored): %v", err)
		}
		t.Logf("🎯 稼働中 VM でも復元できる (PS 委譲)。復元後の EnabledState=%d", restored.EnabledState)
		if restored.EnabledState != hyperv.EnabledStateDisabled {
			t.Errorf("Production チェックポイントの復元後は停止状態のはず (got %d)", restored.EnabledState)
		}

		// 後片付け: 停止に戻す。
		if jr, err := cc.WsmanClient.TurnOffVM(ctx, cs.Name); err == nil {
			_ = cc.WsmanClient.WaitForJob(ctx, jr)
		}
	}

	// --- 6. 削除 → 不在になること ---
	if err := cc.DeleteVmCheckpoint(ctx, vmName, cpName2); err != nil {
		t.Fatalf("DeleteVmCheckpoint: %v", err)
	}
	if after, err := cc.GetVmCheckpoint(ctx, vmName, cpName2); err != nil {
		t.Fatalf("GetVmCheckpoint (削除後): %v", err)
	} else if after.Name != "" {
		t.Fatalf("🔴 削除したのに残っている: %+v", after)
	}
	t.Logf("⑥ 削除 OK")

	// --- 7. 不在の削除は冪等 ---
	if err := cc.DeleteVmCheckpoint(ctx, vmName, cpName2); err != nil {
		t.Fatalf("🔴 不在の削除は冪等であるべき: %v", err)
	}
	t.Logf("🎯 判定: vm_checkpoint の CIM 経路が CRUD 一巡で動作する")
}

// TestRealHostCheckpointStandardRestoreKeepsRunning は「Standard チェックポイントを
// 稼働中 VM に復元すると稼働状態が維持される」ことを固定する。
//
// これが CIM 経路と PS 経路のパリティの核心。CIM の ApplySnapshot は種別に関係なく
// 稼働中 VM を拒否する (32775) ため PS へ委譲しているが、もし将来「CIM 側で停止を
// 挟んで擬似的に実現する」実装に戻すと、**維持されるはずの稼働状態を黙って壊す**。
// 一次資料にこの挙動の記載は無く、実機観測だけが根拠なのでテストで固定する。
func TestRealHostCheckpointStandardRestoreKeepsRunning(t *testing.T) {
	if os.Getenv("HYPERV_TEST_ALLOW_MUTATION") == "" {
		t.Skip("HYPERV_TEST_ALLOW_MUTATION 未設定（VM 作成を伴う破壊的テスト）")
	}
	c := realHostConfigFromEnv(t)
	cc := newRealHostWsmanClientConfig(t, c)
	ctx := context.Background()

	const (
		vmName    = "tf-wsman-ckpt-std"
		cpName    = "std-running"
		memByt    = 536870912
		defaultVM = `C:\ProgramData\Microsoft\Windows\Hyper-V`
	)
	_ = cc.DeleteVm(ctx, vmName)
	t.Cleanup(func() {
		_ = cc.DeleteVmCheckpoint(ctx, vmName, cpName)
		if err := cc.DeleteVm(ctx, vmName); err != nil {
			t.Logf("cleanup DeleteVm: %v", err)
		}
	})

	// Standard (メモリ込み) のチェックポイントを取る VM。
	if err := cc.CreateVm(ctx, vmName,
		"", 1,
		api.CriticalErrorAction_Pause, 30,
		api.StartAction_Nothing, 0,
		api.StopAction_Save,
		api.CheckpointType_Standard,
		false, false, 536870912,
		api.OnOffState_Off, 134217728,
		memByt, memByt, memByt,
		"std-restore", 1,
		defaultVM, defaultVM, true, true,
	); err != nil {
		t.Fatalf("CreateVm: %v", err)
	}

	cs, err := cc.WsmanClient.FindComputerSystemByElementName(ctx, vmName)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if jr, err := cc.WsmanClient.StartVM(ctx, cs.Name); err != nil {
		t.Skipf("StartVM 不可のためスキップ: %v", err)
	} else if err := cc.WsmanClient.WaitForJob(ctx, jr); err != nil {
		t.Fatalf("WaitForJob(Start): %v", err)
	}

	// 稼働中にチェックポイントを取る。
	if err := cc.CreateVmCheckpoint(ctx, vmName, cpName); err != nil {
		t.Fatalf("CreateVmCheckpoint: %v", err)
	}
	got, err := cc.GetVmCheckpoint(ctx, vmName, cpName)
	if err != nil {
		t.Fatalf("GetVmCheckpoint: %v", err)
	}
	t.Logf("① 取得したチェックポイント: Type=%q", got.CheckpointType)
	if got.CheckpointType != "Standard" {
		t.Skipf("Standard で取れなかったため判定不能 (got %q)", got.CheckpointType)
	}

	// 稼働中のまま復元する。
	if err := cc.RestoreVmCheckpoint(ctx, vmName, cpName); err != nil {
		t.Fatalf("RestoreVmCheckpoint: %v", err)
	}
	after, err := cc.WsmanClient.FindComputerSystemByElementName(ctx, vmName)
	if err != nil {
		t.Fatalf("Find (after): %v", err)
	}
	t.Logf("② 復元後の EnabledState=%d (2=Running)", after.EnabledState)
	if after.EnabledState != hyperv.EnabledStateEnabled {
		t.Fatalf("🔴 Standard チェックポイントの復元で稼働状態が維持されていない (got %d)。"+
			"CIM 側で停止を挟む実装に戻っていないか", after.EnabledState)
	}
	t.Logf("🎯 判定: Standard チェックポイントの復元で稼働状態が維持される")

	if jr, err := cc.WsmanClient.TurnOffVM(ctx, cs.Name); err == nil {
		_ = cc.WsmanClient.WaitForJob(ctx, jr)
	}
}
