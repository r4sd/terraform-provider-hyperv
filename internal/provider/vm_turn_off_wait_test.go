package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

// fakeVmStatusClient は waitForVmOff が必要とする 2 メソッドだけを持つスタブ。
//
// `api.Client` は多数のインターフェースの合成なので、全部を実装せずに済むよう
// waitForVmOff 側が `api.HypervVmStatusClient` だけを要求するようにしてある。
type fakeVmStatusClient struct {
	// states は GetVmStatus が返す状態を順に消費する。
	// 尽きたら最後の値を繰り返す (= その状態で止まったまま、を表現する)。
	states []api.VmState
	calls  int

	// getErr / updateErr を設定するとそのエラーを返す。
	getErr    error
	updateErr error

	// getDelay は GetVmStatus 自体が時間を食うケース (WinRM の往復) を表現する。
	getDelay time.Duration

	// updateDelay / stateAfterUpdate で「停止発行が長くかかって成功した」を表現する。
	updateDelay      time.Duration
	stateAfterUpdate *api.VmState

	// updateArgs は UpdateVmStatus に渡された引数を記録する。
	updateArgs []fakeUpdateArgs
}

type fakeUpdateArgs struct {
	timeout, pollPeriod uint32
	state               api.VmState
	turnOff             bool
}

func (f *fakeVmStatusClient) GetVmStatus(_ context.Context, _ string) (api.VmStatus, error) {
	if f.getErr != nil {
		return api.VmStatus{}, f.getErr
	}
	if f.getDelay > 0 {
		time.Sleep(f.getDelay)
	}
	i := f.calls
	f.calls++
	if i >= len(f.states) {
		i = len(f.states) - 1
	}
	return api.VmStatus{State: f.states[i]}, nil
}

func (f *fakeVmStatusClient) UpdateVmStatus(
	_ context.Context, _ string, timeout uint32, pollPeriod uint32, state api.VmState, turnOff bool,
) error {
	f.updateArgs = append(f.updateArgs, fakeUpdateArgs{timeout, pollPeriod, state, turnOff})
	if f.updateErr != nil {
		return f.updateErr
	}
	if f.updateDelay > 0 {
		time.Sleep(f.updateDelay)
	}
	if f.stateAfterUpdate != nil {
		// 以降の GetVmStatus はこの状態を返す。
		f.states = []api.VmState{*f.stateAfterUpdate}
		f.calls = 0
	}
	return nil
}

// stopStates は waitForVmOff が停止要求を発行する状態。canRequestVmOff と対で見る。
var stopStates = []api.VmState{api.VmState_Other, api.VmState_Running, api.VmState_Paused}

// TestWaitForVmOff_StopsWhenAlreadyOff は Off なら何もせず抜けることを検証する。
func TestWaitForVmOff_StopsWhenAlreadyOff(t *testing.T) {
	f := &fakeVmStatusClient{states: []api.VmState{api.VmState_Off}}
	if err := waitForVmOff(context.Background(), f, "vm", 10, 1); err != nil {
		t.Fatalf("waitForVmOff: %v", err)
	}
	if len(f.updateArgs) != 0 {
		t.Errorf("Off なのに UpdateVmStatus を %d 回呼んだ", len(f.updateArgs))
	}
}

// TestWaitForVmOff_IssuesStopForRunning は Running なら停止を発行して Off を待つことを検証する。
func TestWaitForVmOff_IssuesStopForRunning(t *testing.T) {
	f := &fakeVmStatusClient{states: []api.VmState{api.VmState_Running, api.VmState_Off}}
	if err := waitForVmOff(context.Background(), f, "vm", 10, 1); err != nil {
		t.Fatalf("waitForVmOff: %v", err)
	}
	if len(f.updateArgs) != 1 || f.updateArgs[0].state != api.VmState_Off {
		t.Errorf("停止の発行が期待と違う: %v", f.updateArgs)
	}
}

// TestWaitForVmOff_TimesOutOnSaved は **#180 の本題**。
//
// 🔴 `Saved` は安定状態で、放置しても Off にならない。しかも
// `waitForVmOff` は Other / Running / Paused 以外では**停止を発行しない**ので、
// 誰も Off にしない。timeout が無いと**永久に抜けない**。
//
// terraform から見ると「apply が応答を返さなくなる」。
func TestWaitForVmOff_TimesOutOnSaved(t *testing.T) {
	f := &fakeVmStatusClient{states: []api.VmState{api.VmState_Saved}}
	start := time.Now()
	err := waitForVmOff(context.Background(), f, "vm", 1, 1) // timeout 1s / poll 1s
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Saved のまま抜けられないはずだが nil が返った")
	}
	if !strings.Contains(err.Error(), "Saved") {
		t.Errorf("エラーに詰まった状態が入っていない: %v", err)
	}
	// 🔴 **無限ループしていないこと**を時間で見る。
	if elapsed > 10*time.Second {
		t.Errorf("timeout=1s なのに %v かかった。上限が効いていない", elapsed)
	}
	if len(f.updateArgs) != 0 {
		t.Errorf("Saved に対して停止を発行した (%v)。"+
			"発行する状態の集合を変えたなら doc も直すこと", f.updateArgs)
	}
}

// TestWaitForVmOff_ErrorsOnCritical は Critical 状態では即エラーになることを検証する
// (待っても回復しないので、人が介入する必要がある)。
func TestWaitForVmOff_ErrorsOnCritical(t *testing.T) {
	for _, st := range []api.VmState{
		api.VmState_RunningCritical,
		api.VmState_FastSavingCritical, // #175 で値が直った端の値
	} {
		f := &fakeVmStatusClient{states: []api.VmState{st}}
		start := time.Now()
		// 🔴 timeout を短くしておく。長くすると Critical の判定を潰す変異が
		// 「テスト自体のタイムアウト」で落ちて原因が分からなくなる。
		err := waitForVmOff(context.Background(), f, "vm", 3, 1)
		if err == nil {
			t.Errorf("%v でエラーにならなかった", st)
			continue
		}
		if !strings.Contains(err.Error(), "manually") {
			t.Errorf("%v のエラーが手動復旧を促していない: %v", st, err)
		}
		// 待たずに即返ること (poll=1s なので待っていたら経過時間で分かる)。
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("%v で %v 待った。Critical は即返すべき", st, elapsed)
		}
	}
}

// TestWaitForVmOff_RespectsContext は ctx のキャンセルで抜けることを検証する。
//
// 以前は time.Sleep が ctx を見ていなかったので、terraform 側のキャンセルが効かなかった。
func TestWaitForVmOff_RespectsContext(t *testing.T) {
	f := &fakeVmStatusClient{states: []api.VmState{api.VmState_Saved}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 即キャンセル

	start := time.Now()
	// 🔴 poll を短くしておく。長くすると ctx を見ない実装 (time.Sleep) が
	// 「テスト自体のタイムアウト」で落ちて原因が分からなくなる。
	// それでも「即返る」ことは下の elapsed で見られる。
	err := waitForVmOff(ctx, f, "vm", 10, 8) // timeout 10 秒 / poll 8 秒
	if err == nil {
		t.Fatal("キャンセル済み ctx で nil が返った")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("ctx のキャンセルが伝わっていない: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("キャンセルなのに %v かかった (ctx を見ずに待っている)", elapsed)
	}
}

// TestResolveTurnOffWait は 0 以下が既定値に倒れることを検証する。
//
// 🔴 **前の版は定数の符号しか見ておらず空虚だった** (`resolveTurnOffWait` を呼んで
// いなかったので、0 → 既定値のフォールバックを消す変異が生存した)。
// 変換関数に切り出して**実際に 0 を渡す**形にした。
//
// 0 が素通りすると timeout が 0 になり deadline が「今」になるので待たなくなる。
// pollPeriod が 0 なら busy loop。
func TestResolveTurnOffWait(t *testing.T) {
	tests := []struct {
		name                  string
		timeoutSec, pollSec   uint32
		wantTimeout, wantPoll time.Duration
	}{
		{"指定あり", 30, 5, 30 * time.Second, 5 * time.Second},
		{"timeout=0 は既定へ", 0, 5, defaultTurnOffTimeout, 5 * time.Second},
		{"poll=0 は既定へ", 30, 0, 30 * time.Second, defaultTurnOffPollPeriod},
		{"両方 0 は既定へ", 0, 0, defaultTurnOffTimeout, defaultTurnOffPollPeriod},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timeout, poll := resolveTurnOffWait(tt.timeoutSec, tt.pollSec)
			if timeout != tt.wantTimeout {
				t.Errorf("timeout = %v, want %v", timeout, tt.wantTimeout)
			}
			if poll != tt.wantPoll {
				t.Errorf("pollPeriod = %v, want %v", poll, tt.wantPoll)
			}
		})
	}

	// 既定値そのものが有限で正であること (ここが 0 以下だとフォールバックしても壊れる)。
	if defaultTurnOffTimeout <= 0 {
		t.Errorf("defaultTurnOffTimeout が %v。0 以下だと上限が無い", defaultTurnOffTimeout)
	}
	if defaultTurnOffPollPeriod <= 0 {
		t.Errorf("defaultTurnOffPollPeriod が %v。0 以下だと busy loop になる", defaultTurnOffPollPeriod)
	}
}

// TestWaitForVmOff_IssuesStopForEveryStoppableState は**停止を発行する状態すべて**で
// 発行されることを検証する。
//
// 🔴 以前は Running だけ見ていたので、集合から Paused / Other を外す変異が生存していた。
func TestWaitForVmOff_IssuesStopForEveryStoppableState(t *testing.T) {
	for _, st := range stopStates {
		t.Run(st.String(), func(t *testing.T) {
			off := api.VmState_Off
			f := &fakeVmStatusClient{states: []api.VmState{st}, stateAfterUpdate: &off}
			if err := waitForVmOff(context.Background(), f, "vm", 10, 1); err != nil {
				t.Fatalf("waitForVmOff: %v", err)
			}
			if len(f.updateArgs) != 1 {
				t.Fatalf("%v に対する停止の発行が %d 回 (want 1)", st, len(f.updateArgs))
			}
			if f.updateArgs[0].state != api.VmState_Off {
				t.Errorf("発行した目標状態が %v (want Off)", f.updateArgs[0].state)
			}
		})
	}
}

// TestCanRequestVmOff は停止を発行する状態の集合そのものを固定する。
//
// 集合を変えると waitForVmOff の doc と PS 経路の挙動 (Saved→Off は throw) の
// 確認が必要になるので、意図しない変更を落とす。
func TestCanRequestVmOff(t *testing.T) {
	for _, st := range stopStates {
		if !canRequestVmOff(st) {
			t.Errorf("%v は停止を発行する対象のはず", st)
		}
	}
	// 代表的な「発行しない」状態。ここに足すときは doc も直すこと。
	for _, st := range []api.VmState{
		api.VmState_Off, api.VmState_Saved, api.VmState_FastSaved,
		api.VmState_Hibernated, api.VmState_ComponentServicing,
		api.VmState_Starting, api.VmState_Stopping, api.VmState_Saving,
		api.VmState_RunningCritical,
	} {
		if canRequestVmOff(st) {
			t.Errorf("%v に停止を発行してはいけない (集合を変えたなら doc も直すこと)", st)
		}
	}
}

// TestIsCriticalVmState は Critical 系**全 12 件**を固定する。
//
// 🔴 以前は 2 件しか見ていなかったので、1 件だけ落とす変異が生存していた。
func TestIsCriticalVmState(t *testing.T) {
	critical := []api.VmState{
		api.VmState_RunningCritical, api.VmState_OffCritical,
		api.VmState_StoppingCritical, api.VmState_SavedCritical,
		api.VmState_PausedCritical, api.VmState_StartingCritical,
		api.VmState_ResetCritical, api.VmState_SavingCritical,
		api.VmState_PausingCritical, api.VmState_ResumingCritical,
		api.VmState_FastSavedCritical, api.VmState_FastSavingCritical,
	}
	if len(critical) != 12 {
		t.Fatalf("Critical の一覧が %d 件 (want 12)", len(critical))
	}
	for _, st := range critical {
		if !isCriticalVmState(st) {
			t.Errorf("%v が Critical と判定されない", st)
		}
	}
	// Critical ではない代表値。api.VmState_name 全体を回して Critical 以外が
	// false であることも見る (列挙が増えたときに拾う)。
	criticalSet := make(map[api.VmState]bool, len(critical))
	for _, st := range critical {
		criticalSet[st] = true
	}
	for st := range api.VmState_name {
		if got := isCriticalVmState(st); got != criticalSet[st] {
			t.Errorf("isCriticalVmState(%v) = %v, want %v", st, got, criticalSet[st])
		}
	}
}

// TestWaitForVmOff_PropagatesGetError は GetVmStatus のエラーをそのまま返すことを検証する。
//
// 🔴 以前はフェイクに getErr フィールドがあるのに**どのテストも使っていなかった**
// (死にフィクスチャ)。エラーを無視する変異が生存していた。
func TestWaitForVmOff_PropagatesGetError(t *testing.T) {
	want := errors.New("get boom")
	f := &fakeVmStatusClient{states: []api.VmState{api.VmState_Running}, getErr: want}
	err := waitForVmOff(context.Background(), f, "vm", 10, 1)
	if !errors.Is(err, want) {
		t.Errorf("GetVmStatus のエラーが伝わっていない: %v", err)
	}
}

// TestWaitForVmOff_PropagatesUpdateError は UpdateVmStatus のエラーをそのまま返すことを検証する。
func TestWaitForVmOff_PropagatesUpdateError(t *testing.T) {
	want := errors.New("update boom")
	f := &fakeVmStatusClient{states: []api.VmState{api.VmState_Running}, updateErr: want}
	err := waitForVmOff(context.Background(), f, "vm", 10, 1)
	if !errors.Is(err, want) {
		t.Errorf("UpdateVmStatus のエラーが伝わっていない: %v", err)
	}
}

// TestWaitForVmOff_SlowStopThatSucceeds は **リグレッション検査** (#180 のレビュー指摘)。
//
// 🔴 停止の発行は同期で長くかかる (CIM 経路は waitForStableVmState + WaitForJob で
// 正常系でも最大 2×timeout、PS 経路はゲスト OS のシャットダウン待ち)。
//
// deadline の判定を**発行の後**に置くと、「発行が長引いたが成功して VM は Off に
// なった」ケースで古い状態を見て timeout エラーにしてしまう。
// 旧実装は発行後に再取得していたので成功していたので、これは退行になる。
func TestWaitForVmOff_SlowStopThatSucceeds(t *testing.T) {
	off := api.VmState_Off
	f := &fakeVmStatusClient{
		states:           []api.VmState{api.VmState_Running},
		updateDelay:      1500 * time.Millisecond, // timeout より長くかかる
		stateAfterUpdate: &off,
	}
	// timeout=1s なので、発行の後に判定すると必ず超過している。
	if err := waitForVmOff(context.Background(), f, "vm", 1, 1); err != nil {
		t.Errorf("停止が長引いたが成功したケースで失敗した (deadline を発行の後に"+
			"判定している可能性): %v", err)
	}
}

// TestWaitForVmOff_TimeoutMessageDistinguishesStoppable は timeout 時の文面が
// **停止を発行したかどうかで分かれる**ことを検証する。
//
// 🔴 以前は無条件に「この状態では停止要求が発行されない」と書いていた。
// Running で timeout したときは発行されているので嘘になり、
// Saved のときは「timeout を延ばせ」が**何も変えない助言**だった。
func TestWaitForVmOff_TimeoutMessageDistinguishesStoppable(t *testing.T) {
	// 発行する状態: 発行したが Off にならなかった旨
	fRun := &fakeVmStatusClient{states: []api.VmState{api.VmState_Running}}
	errRun := waitForVmOff(context.Background(), fRun, "vm", 1, 1)
	if errRun == nil {
		t.Fatal("Running のまま Off にならないので timeout するはず")
	}
	if !strings.Contains(errRun.Error(), "停止要求は発行した") {
		t.Errorf("発行した旨が文面に無い: %v", errRun)
	}
	// 🔴 文面の前提そのものを見る。発行ゼロで「発行した」と書いていたら嘘になる。
	if len(fRun.updateArgs) == 0 {
		t.Error("「停止要求は発行した」と書いているのに一度も発行していない")
	}

	// 発行しない状態: 待っても変わらない旨
	fSaved := &fakeVmStatusClient{states: []api.VmState{api.VmState_Saved}}
	errSaved := waitForVmOff(context.Background(), fSaved, "vm", 1, 1)
	if errSaved == nil {
		t.Fatal("Saved のまま抜けられないので timeout するはず")
	}
	if !strings.Contains(errSaved.Error(), "停止要求が発行されない") {
		t.Errorf("発行されない旨が文面に無い: %v", errSaved)
	}
	if strings.Contains(errSaved.Error(), "停止要求は発行した") {
		t.Errorf("Saved なのに「発行した」と書いている: %v", errSaved)
	}
}

// TestWaitForVmOff_PassesRawWaitArgsDownstream は UpdateVmStatus に
// **解決前の生値**を渡すことを固定する。
//
// 下流 (CIM / PS) はそれぞれ独自の既定値を持っており、ここで解決値
// (resolveTurnOffWait の結果) に差し替えると挙動が変わる。旧実装も生値を渡していた。
//
// 🔴 **生値 0 のケースが要る。** 非ゼロの値だけで試すと resolveTurnOffWait は
// 恒等変換になるので、「解決値を渡す」変異が生き残る (最初それで生存させた)。
func TestWaitForVmOff_PassesRawWaitArgsDownstream(t *testing.T) {
	cases := []struct{ timeout, poll uint32 }{
		{7, 3}, // 非ゼロ: そのまま渡る
		{0, 0}, // 🔴 ここが変異を落とす。解決値は (120, 2) なので 0 と区別できる
		{0, 3}, // timeout だけ 0
		{7, 0}, // poll だけ 0
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("timeout=%d,poll=%d", c.timeout, c.poll), func(t *testing.T) {
			off := api.VmState_Off
			f := &fakeVmStatusClient{states: []api.VmState{api.VmState_Running}, stateAfterUpdate: &off}
			if err := waitForVmOff(context.Background(), f, "vm", c.timeout, c.poll); err != nil {
				t.Fatalf("waitForVmOff: %v", err)
			}
			if len(f.updateArgs) != 1 {
				t.Fatalf("停止の発行が %d 回", len(f.updateArgs))
			}
			got := f.updateArgs[0]
			if got.timeout != c.timeout || got.pollPeriod != c.poll {
				t.Errorf("下流に渡した値が (%d, %d)、want (%d, %d)。解決値に差し替えると"+
					"下流の既定値の扱いが変わる (CIM は 5 分、PS は独自)",
					got.timeout, got.pollPeriod, c.timeout, c.poll)
			}
			if got.turnOff {
				t.Error("turnOff=true を渡している (旧実装は false)")
			}
		})
	}
}

// TestWaitForVmOff_IssuesStopEvenWhenDeadlineAlreadyPassed は、**1 回目のポーリングで
// 既に deadline を超えていても停止を発行する**ことを検証する。
//
// 🔴 deadline の判定を発行の前に移したとき、`GetVmStatus` の往復が timeout より
// 長いと **発行ゼロのままエラー**になるパスが生まれた。schema に ValidateFunc が
// 無いので timeout=1 や 0 は設定できてしまう。旧実装は必ず 1 回は発行していたので、
// 発行しないのは挙動変更。
func TestWaitForVmOff_IssuesStopEvenWhenDeadlineAlreadyPassed(t *testing.T) {
	t.Run("発行して成功する", func(t *testing.T) {
		off := api.VmState_Off
		f := &fakeVmStatusClient{
			states:           []api.VmState{api.VmState_Running},
			getDelay:         1100 * time.Millisecond, // timeout より長い
			stateAfterUpdate: &off,
		}
		if err := waitForVmOff(context.Background(), f, "vm", 1, 1); err != nil {
			t.Errorf("1 回目で deadline 超過でも発行して成功するはず: %v", err)
		}
		if len(f.updateArgs) == 0 {
			t.Error("停止を一度も発行せずに抜けた")
		}
	})

	t.Run("発行しても Off にならない", func(t *testing.T) {
		f := &fakeVmStatusClient{
			states:   []api.VmState{api.VmState_Running},
			getDelay: 1100 * time.Millisecond,
		}
		err := waitForVmOff(context.Background(), f, "vm", 1, 1)
		if err == nil {
			t.Fatal("Off にならないので timeout するはず")
		}
		if len(f.updateArgs) == 0 {
			t.Error("停止を一度も発行せずに timeout した")
		}
		// 文面は「発行したか」で分岐する。状態で分岐させると発行ゼロでも
		// 「発行した」と書いてしまう。
		if !strings.Contains(err.Error(), "停止要求は発行した") {
			t.Errorf("発行済みなのに文面が合っていない: %v", err)
		}
	})
}

// TestWaitForVmOff_ReissuesStopOnEachPoll は、Off にならない間は**ポーリングごとに
// 停止を再発行する**ことを固定する。
//
// 旧実装 (#181 以前の turnOffVmIfOn) がそうだったので、既存の契約として残す。
// 「1 回だけ発行して以降は待つだけ」に変えると、発行が取りこぼされたケースで
// 回復しなくなる。
func TestWaitForVmOff_ReissuesStopOnEachPoll(t *testing.T) {
	// Off にならないまま timeout させる。timeout=3 / poll=1 なので 2 回以上回る。
	f := &fakeVmStatusClient{states: []api.VmState{api.VmState_Running}}
	if err := waitForVmOff(context.Background(), f, "vm", 3, 1); err == nil {
		t.Fatal("Off にならないので timeout するはず")
	}
	if len(f.updateArgs) < 2 {
		t.Errorf("停止の発行が %d 回。Off にならない間は毎回発行するはず", len(f.updateArgs))
	}
}

// TestWaitForVmOff_MessageBranchesOnIssuedNotState は、timeout の文面が
// **発行したかどうか**で分岐し、**その時点の状態**では分岐しないことを検証する。
//
// 停止を発行した後に発行対象外の状態 (Saved 等) へ移ることがある。
// 状態で分岐させると、発行済みなのに「この状態では停止要求が発行されない。
// timeout を延ばしても変わらない」という誤った助言になる。
func TestWaitForVmOff_MessageBranchesOnIssuedNotState(t *testing.T) {
	// 1 周目 Running (発行する) → 2 周目 Saved (発行対象外) で timeout。
	f := &fakeVmStatusClient{states: []api.VmState{api.VmState_Running, api.VmState_Saved}}
	err := waitForVmOff(context.Background(), f, "vm", 1, 2)
	if err == nil {
		t.Fatal("Off にならないので timeout するはず")
	}
	if len(f.updateArgs) != 1 {
		t.Fatalf("停止の発行が %d 回 (want 1)", len(f.updateArgs))
	}
	if !strings.Contains(err.Error(), "停止要求は発行した") {
		t.Errorf("発行済みなのに状態で分岐している: %v", err)
	}
	if strings.Contains(err.Error(), "停止要求が発行されない") {
		t.Errorf("発行済みなのに「発行されない」と書いている: %v", err)
	}
}
