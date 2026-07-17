package hyperv_wsman

import (
	"math"
	"reflect"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestClientConfig_ImplementsHypervVmProcessorClient は ClientConfig が
// api.HypervVmProcessorClient を実装し、無条件 PS だった GetVmProcessors が本パッケージで
// シャドウイングされていることを検証する。Create/Update は埋め込み winrm から promotion される。
func TestClientConfig_ImplementsHypervVmProcessorClient(t *testing.T) {
	var c *ClientConfig
	var _ api.HypervVmProcessorClient = c // コンパイル時チェック

	cType := reflect.TypeOf((*ClientConfig)(nil))
	if _, ok := cType.MethodByName("GetVmProcessors"); !ok {
		t.Error("GetVmProcessors が hyperv-wsman で定義されていない (無条件 PS が解消しない)")
	}
}

// TestProcessorFromSettingData は CIM → provider の単位変換が正しいことを検証する。
// 特に Limit/Reservation の percent/1000 → percent 変換と、Weight の 1:1 (無変換) を固定する。
func TestProcessorFromSettingData(t *testing.T) {
	src := &hyperv.Msvm_ProcessorSettingData{
		VirtualQuantity:                4,
		Limit:                          75000, // 75%
		Reservation:                    25000, // 25%
		Weight:                         200,   // RelativeWeight と 1:1
		LimitProcessorFeatures:         true,  // → CompatibilityForMigrationEnabled
		LimitCPUID:                     true,  // → CompatibilityForOlderOperatingSystemsEnabled
		ExposeVirtualizationExtensions: true,
		HwThreadsPerCore:               2,
		MaxProcessorsPerNumaNode:       8,
		MaxNumaNodesPerSocket:          1,
		EnableHostResourceProtection:   true,
	}

	got := processorFromSettingData("my-vm", src)

	want := api.VmProcessor{
		VmName:                           "my-vm",
		CompatibilityForMigrationEnabled: true,
		CompatibilityForOlderOperatingSystemsEnabled: true,
		HwThreadCountPerCore:                         2,
		Maximum:                                      75, // 75000 / 1000
		Reserve:                                      25, // 25000 / 1000
		RelativeWeight:                               200,
		MaximumCountPerNumaNode:                      8,
		MaximumCountPerNumaSocket:                    1,
		EnableHostResourceProtection:                 true,
		ExposeVirtualizationExtensions:               true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("processorFromSettingData mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

// TestProcessorFromSettingData_Defaults は Hyper-V 既定値 (Limit=100000=100%, Reserve=0,
// Weight=100) が provider の既定 (Maximum=100, Reserve=0, RelativeWeight=100) に写ることを
// 確認する。homelab の大半はこのデフォルト値なので、恒常 diff が出ないことの担保になる。
func TestProcessorFromSettingData_Defaults(t *testing.T) {
	src := &hyperv.Msvm_ProcessorSettingData{
		VirtualQuantity: 2,
		Limit:           100000,
		Reservation:     0,
		Weight:          100,
	}
	got := processorFromSettingData("vm", src)
	if got.Maximum != 100 {
		t.Errorf("Maximum: got %d, want 100", got.Maximum)
	}
	if got.Reserve != 0 {
		t.Errorf("Reserve: got %d, want 0", got.Reserve)
	}
	if got.RelativeWeight != 100 {
		t.Errorf("RelativeWeight: got %d, want 100", got.RelativeWeight)
	}
}

// TestClampInt は縮小変換の上限丸めを検証する (gosec G115 回避の防御が機能すること)。
func TestClampInt(t *testing.T) {
	if got := clampInt64(math.MaxUint64); got != math.MaxInt64 {
		t.Errorf("clampInt64(MaxUint64): got %d, want MaxInt64", got)
	}
	if got := clampInt64(42); got != 42 {
		t.Errorf("clampInt64(42): got %d, want 42", got)
	}
	if got := clampInt32(math.MaxUint64); got != math.MaxInt32 {
		t.Errorf("clampInt32(MaxUint64): got %d, want MaxInt32", got)
	}
	if got := clampInt32(7); got != 7 {
		t.Errorf("clampInt32(7): got %d, want 7", got)
	}
}
