package hyperv_wsman

import (
	"reflect"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestClientConfig_ImplementsHypervVmStatusClient は ClientConfig が api.HypervVmStatusClient を
// 実装し、両メソッドが本パッケージでシャドウイングされていることを検証する。
func TestClientConfig_ImplementsHypervVmStatusClient(t *testing.T) {
	var c *ClientConfig
	var _ api.HypervVmStatusClient = c // コンパイル時チェック

	cType := reflect.TypeOf((*ClientConfig)(nil))
	for _, methodName := range []string{"GetVmStatus", "UpdateVmStatus"} {
		if _, ok := cType.MethodByName(methodName); !ok {
			t.Errorf("メソッド %s が hyperv-wsman で定義されていない (シャドウイングされない)", methodName)
		}
	}
}

// TestEnabledStateToVmState は Msvm_ComputerSystem.EnabledState → provider VmState の変換を検証する。
// 値は実機ダンプ準拠 (Running=2 / Off=3 / Saved=6 / Paused=9)。go-wsman の CIM 標準定数
// (Paused=32768/Saved=32769) は Msvm_ComputerSystem では返らないので Other に落ちる。
func TestEnabledStateToVmState(t *testing.T) {
	tests := []struct {
		name string
		in   uint16
		want api.VmState
	}{
		{"2→Running", 2, api.VmState_Running},
		{"3→Off", 3, api.VmState_Off},
		{"9→Paused(実機値)", 9, api.VmState_Paused},
		{"6→Saved(実機値)", 6, api.VmState_Saved},
		{"0(Unknown)→Other", 0, api.VmState_Other},
		{"32768(CIM Paused, 実機では来ない)→Other", 32768, api.VmState_Other},
		{"4(Stopping遷移中)→Other", 4, api.VmState_Other},
		{"未知値→Other", 12345, api.VmState_Other},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := enabledStateToVmState(tt.in); got != tt.want {
				t.Errorf("enabledStateToVmState(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestIsStableEnabledState は安定状態(Running=2/Off=3/Saved=6/Paused=9)と遷移中/未知を区別する
// ことを検証する。状態変更の前に安定まで待つ判定の土台 (遷移中への RequestStateChange 拒否・
// 再発行スパム防止)。値は実機ダンプ準拠。
func TestIsStableEnabledState(t *testing.T) {
	for _, s := range []uint16{2, 3, 6, 9} { // Running/Off/Saved/Paused
		if !isStableEnabledState(s) {
			t.Errorf("EnabledState=%d は安定のはず", s)
		}
	}
	// 遷移中 (Stopping=4 / Starting=10 / Reset=11 / Saving=32773 / Pausing=32776 / Resuming=32777) と
	// Unknown(0)、実機では来ない CIM 値(32768/32769) は不安定扱い。
	for _, s := range []uint16{0, 4, 10, 11, 32768, 32769, 32773, 32776, 32777} {
		if isStableEnabledState(s) {
			t.Errorf("EnabledState=%d は遷移中/未知(不安定)のはず", s)
		}
	}
}

// TestVmStatusWaitOpts は timeout/pollPeriod=0 で既定(オプション無し)、正値でオプション付与を検証する。
func TestVmStatusWaitOpts(t *testing.T) {
	if got := vmStatusWaitOpts(0, 0); len(got) != 0 {
		t.Errorf("0/0 は既定(オプション無し)のはず, got %d opts", len(got))
	}
	if got := vmStatusWaitOpts(300, 5); len(got) != 2 {
		t.Errorf("timeout+pollPeriod 両方正なら 2 opts, got %d", len(got))
	}
	if got := vmStatusWaitOpts(300, 0); len(got) != 1 {
		t.Errorf("timeout のみなら 1 opt, got %d", len(got))
	}
}
