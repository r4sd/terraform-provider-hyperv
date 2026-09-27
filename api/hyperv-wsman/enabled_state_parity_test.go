package hyperv_wsman

import (
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestEnabledStateParity は provider の VmState と go-wsman の EnabledState 定数が
// 一致することを固定する (go-wsman #102 / #171)。
//
// **この 2 つは独立に導かれている。**
//
//	api.VmState_*          : upstream (taliesins) が PowerShell の VMState 列挙から起こしたもの
//	hyperv.EnabledState*   : go-wsman が Msvm_ComputerSystem の実機観測から起こしたもの
//
// 出自が違うので、**一致すること自体が実証拠**になる。片方だけ変えたら落ちる。
//
// 単なる変更検知器(値を書いて同じ値を assert する)と違い、ここは
// **別々に決まった 2 つの事実を突き合わせている**点に意味がある。
//
// 🔴 過去に go-wsman 側が CIM ドキュメントの 32768/32769 を入れていて食い違っていた。
// 実機は 9 / 6 を返す(2026-07-09 / 2026-09-27 確認)。
func TestEnabledStateParity(t *testing.T) {
	tests := []struct {
		name     string
		provider api.VmState
		gowsman  uint16
	}{
		{"Running / Enabled", api.VmState_Running, hyperv.EnabledStateEnabled},
		{"Off / Disabled", api.VmState_Off, hyperv.EnabledStateDisabled},
		{"Paused", api.VmState_Paused, hyperv.EnabledStatePaused},
		{"Saved", api.VmState_Saved, hyperv.EnabledStateSaved},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if uint16(tt.provider) != tt.gowsman {
				t.Errorf("provider の VmState=%d と go-wsman の EnabledState=%d が食い違う。"+
					"どちらかが実機と合っていない", tt.provider, tt.gowsman)
			}
		})
	}
}

// TestEnabledStateToVmStateUsesSharedValues は enabledStateToVmState が
// 生の uint16 を直接キャストしている前提を固定する。
//
// この関数は api.VmState(s) で変換しており、上のパリティが崩れると
// **黙って違う状態に化ける**(エラーにならない)。パリティテストとセットで意味を持つ。
func TestEnabledStateToVmStateUsesSharedValues(t *testing.T) {
	cases := map[uint16]api.VmState{
		hyperv.EnabledStateEnabled:  api.VmState_Running,
		hyperv.EnabledStateDisabled: api.VmState_Off,
		hyperv.EnabledStatePaused:   api.VmState_Paused,
		hyperv.EnabledStateSaved:    api.VmState_Saved,
	}
	for in, want := range cases {
		if got := enabledStateToVmState(in); got != want {
			t.Errorf("enabledStateToVmState(%d) = %v, want %v", in, got, want)
		}
	}
}
