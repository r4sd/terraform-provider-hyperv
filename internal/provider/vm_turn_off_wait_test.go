package provider

import (
	"context"
	"errors"
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

	// updateCalls は UpdateVmStatus が呼ばれた回数と引数。
	updateCalls []api.VmState

	// getErr を設定すると GetVmStatus がそれを返す。
	getErr error
}

func (f *fakeVmStatusClient) GetVmStatus(_ context.Context, _ string) (api.VmStatus, error) {
	if f.getErr != nil {
		return api.VmStatus{}, f.getErr
	}
	i := f.calls
	f.calls++
	if i >= len(f.states) {
		i = len(f.states) - 1
	}
	return api.VmStatus{State: f.states[i]}, nil
}

func (f *fakeVmStatusClient) UpdateVmStatus(
	_ context.Context, _ string, _ uint32, _ uint32, state api.VmState, _ bool,
) error {
	f.updateCalls = append(f.updateCalls, state)
	return nil
}

// TestWaitForVmOff_StopsWhenAlreadyOff は Off なら何もせず抜けることを検証する。
func TestWaitForVmOff_StopsWhenAlreadyOff(t *testing.T) {
	f := &fakeVmStatusClient{states: []api.VmState{api.VmState_Off}}
	if err := waitForVmOff(context.Background(), f, "vm", 10, 1); err != nil {
		t.Fatalf("waitForVmOff: %v", err)
	}
	if len(f.updateCalls) != 0 {
		t.Errorf("Off なのに UpdateVmStatus を %d 回呼んだ", len(f.updateCalls))
	}
}

// TestWaitForVmOff_IssuesStopForRunning は Running なら停止を発行して Off を待つことを検証する。
func TestWaitForVmOff_IssuesStopForRunning(t *testing.T) {
	f := &fakeVmStatusClient{states: []api.VmState{api.VmState_Running, api.VmState_Off}}
	if err := waitForVmOff(context.Background(), f, "vm", 10, 1); err != nil {
		t.Fatalf("waitForVmOff: %v", err)
	}
	if len(f.updateCalls) != 1 || f.updateCalls[0] != api.VmState_Off {
		t.Errorf("停止の発行が期待と違う: %v", f.updateCalls)
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
	if len(f.updateCalls) != 0 {
		t.Errorf("Saved に対して停止を発行した (%v)。"+
			"発行する状態の集合を変えたなら doc も直すこと", f.updateCalls)
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
		// 待たずに即返ること (poll=5s なので待っていたら分かる)。
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
