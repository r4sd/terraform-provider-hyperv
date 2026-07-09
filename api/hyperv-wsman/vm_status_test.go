package hyperv_wsman

import (
	"reflect"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
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

// TestEnabledStateToVmState は CIM EnabledState → provider VmState の変換を検証する。
// Paused/Saved は CIM 値(32768/32769)と provider 値(9/6)が異なるので特に重要。
func TestEnabledStateToVmState(t *testing.T) {
	tests := []struct {
		name string
		in   uint16
		want api.VmState
	}{
		{"Enabled→Running", hyperv.EnabledStateEnabled, api.VmState_Running},
		{"Disabled→Off", hyperv.EnabledStateDisabled, api.VmState_Off},
		{"Paused→Paused(9)", hyperv.EnabledStatePaused, api.VmState_Paused},
		{"Saved→Saved(6)", hyperv.EnabledStateSaved, api.VmState_Saved},
		{"Unknown→Other", hyperv.EnabledStateUnknown, api.VmState_Other},
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
