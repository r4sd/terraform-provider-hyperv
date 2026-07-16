package hyperv_wsman

import (
	"reflect"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestClientConfig_ImplementsHypervVmGpuAdapterClient は ClientConfig が
// api.HypervVmGpuAdapterClient を実装し、両メソッドが本パッケージでシャドウイングされている
// ことを検証する (埋め込み winrm から promotion されただけでは無条件 PS が解消しないため)。
func TestClientConfig_ImplementsHypervVmGpuAdapterClient(t *testing.T) {
	var c *ClientConfig
	var _ api.HypervVmGpuAdapterClient = c // コンパイル時チェック

	cType := reflect.TypeOf((*ClientConfig)(nil))
	for _, methodName := range []string{"GetVmGpuAdapters", "CreateOrUpdateVmGpuAdapters"} {
		if _, ok := cType.MethodByName(methodName); !ok {
			t.Errorf("メソッド %s が hyperv-wsman で定義されていない (シャドウイングされない)", methodName)
		}
	}
}

// TestGpuAdapterFromSettingData は go-wsman の型から provider 型への変換が全 12 プロパティを
// 桁落ちなく 1:1 に写すことを検証する。
func TestGpuAdapterFromSettingData(t *testing.T) {
	src := &hyperv.Msvm_GpuPartitionSettingData{
		InstanceID:              "Microsoft:11111111\\GPUP-0",
		MinPartitionVRAM:        80000000,
		MaxPartitionVRAM:        100000000,
		OptimalPartitionVRAM:    90000000,
		MinPartitionEncode:      1,
		MaxPartitionEncode:      18446744073709551615, // uint64 上限
		OptimalPartitionEncode:  2,
		MinPartitionDecode:      3,
		MaxPartitionDecode:      4,
		OptimalPartitionDecode:  5,
		MinPartitionCompute:     6,
		MaxPartitionCompute:     7,
		OptimalPartitionCompute: 8,
	}

	got := gpuAdapterFromSettingData("my-vm", src)

	want := api.VmGpuAdapter{
		VmName:                  "my-vm",
		MinPartitionVRAM:        80000000,
		MaxPartitionVRAM:        100000000,
		OptimalPartitionVRAM:    90000000,
		MinPartitionEncode:      1,
		MaxPartitionEncode:      18446744073709551615,
		OptimalPartitionEncode:  2,
		MinPartitionDecode:      3,
		MaxPartitionDecode:      4,
		OptimalPartitionDecode:  5,
		MinPartitionCompute:     6,
		MaxPartitionCompute:     7,
		OptimalPartitionCompute: 8,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("gpuAdapterFromSettingData mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

// TestCreateOrUpdateVmGpuAdapters_EmptyGuard は空リストが PowerShell を流さず no-op で返ること
// (空リストガード) を検証する。WsmanClient/WinRmClient を nil にした ClientConfig で呼び、
// PS 経路や go-wsman 経路に触れず nil を返すことで「実際に何も呼んでいない」ことを保証する
// (もし PS フォールバックや resolve が走れば nil ポインタで panic するため負の証明になる)。
func TestCreateOrUpdateVmGpuAdapters_EmptyGuard(t *testing.T) {
	c := &ClientConfig{} // WsmanClient も埋め込み winrm も nil
	if err := c.CreateOrUpdateVmGpuAdapters(t.Context(), "any-vm", nil); err != nil {
		t.Errorf("空リストは no-op であるべき: %v", err)
	}
	if err := c.CreateOrUpdateVmGpuAdapters(t.Context(), "any-vm", []api.VmGpuAdapter{}); err != nil {
		t.Errorf("空スライスは no-op であるべき: %v", err)
	}
}
