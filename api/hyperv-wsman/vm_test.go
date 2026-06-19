package hyperv_wsman

import (
	"math"
	"reflect"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestClientConfig_ImplementsHypervVmClient は ClientConfig が api.HypervVmClient を
// 実装することを検証する。VmExists / GetVm / DeleteVm は本パッケージで定義 (シャドウイング)、
// CreateVm / UpdateVm は hyperv-winrm から promotion される。
func TestClientConfig_ImplementsHypervVmClient(t *testing.T) {
	var c *ClientConfig
	var _ api.HypervVmClient = c // コンパイル時チェック

	cType := reflect.TypeOf((*ClientConfig)(nil))
	for _, methodName := range []string{
		"VmExists", // ← 本パッケージで定義 (シャドウイング、C-1.1)
		"GetVm",    // ← 本パッケージで定義 (シャドウイング、C-1.1)
		"CreateVm", // ← promotion (C-1.2 で移行予定)
		"UpdateVm", // ← promotion (C-1.3 で移行予定)
		"DeleteVm", // ← 本パッケージで定義 (シャドウイング、C-1.4)
	} {
		if _, ok := cType.MethodByName(methodName); !ok {
			t.Errorf("ClientConfig should expose method %s (via shadow or promotion)", methodName)
		}
	}
}

// TestVmExists_DefinedInWsmanPackage は VmExists が本パッケージで定義されている
// (= シャドウイングが効く) ことをシグネチャで確認する。
func TestVmExists_DefinedInWsmanPackage(t *testing.T) {
	cType := reflect.TypeOf((*ClientConfig)(nil))
	method, ok := cType.MethodByName("VmExists")
	if !ok {
		t.Fatal("ClientConfig should have VmExists method")
	}
	if method.Type.NumIn() != 3 { // receiver + ctx + name
		t.Errorf("VmExists: NumIn = %d, want 3", method.Type.NumIn())
	}
}

// TestVmGenerationFromSubType は VirtualSystemSubType から Generation 番号への変換を検証する。
func TestVmGenerationFromSubType(t *testing.T) {
	tests := []struct {
		subType string
		want    int
	}{
		{hyperv.VirtualSystemSubTypeGen1, 1},
		{hyperv.VirtualSystemSubTypeGen2, 2},
		{"", 0},
		{"Microsoft:Hyper-V:SubType:99", 0},
	}
	for _, tt := range tests {
		if got := vmGenerationFromSubType(tt.subType); got != tt.want {
			t.Errorf("vmGenerationFromSubType(%q) = %d, want %d", tt.subType, got, tt.want)
		}
	}
}

// TestLockOnDisconnectState は bool から api.OnOffState への変換を検証する。
func TestLockOnDisconnectState(t *testing.T) {
	if got := lockOnDisconnectState(true); got != api.OnOffState_On {
		t.Errorf("lockOnDisconnectState(true) = %v, want On", got)
	}
	if got := lockOnDisconnectState(false); got != api.OnOffState_Off {
		t.Errorf("lockOnDisconnectState(false) = %v, want Off", got)
	}
}

// TestClampUint32 は uint64→uint32 の安全な縮小変換を検証する。
func TestClampUint32(t *testing.T) {
	tests := []struct {
		in   uint64
		want uint32
	}{
		{0, 0},
		{128, 128},
		{math.MaxUint32, math.MaxUint32},
		{math.MaxUint32 + 1, math.MaxUint32}, // オーバーフローは上限でクランプ
		{math.MaxUint64, math.MaxUint32},
	}
	for _, tt := range tests {
		if got := clampUint32(tt.in); got != tt.want {
			t.Errorf("clampUint32(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// TestVmFromSettingData は Msvm_VirtualSystemSettingData → api.Vm マッピングの中核を検証する。
//
// enum は provider の整数値が CIM 値と一致するため直接変換される (Pause=1, Start=4, Save=3)。
func TestVmFromSettingData(t *testing.T) {
	sd := &hyperv.Msvm_VirtualSystemSettingData{
		VirtualSystemSubType:         hyperv.VirtualSystemSubTypeGen2,
		AutomaticCriticalErrorAction: 1, // Pause
		AutomaticStartupAction:       4, // Start
		AutomaticShutdownAction:      3, // Save
		Notes:                        []string{"line1", "line2"},
		LockOnDisconnect:             true,
		GuestControlledCacheTypes:    true,
		HighMmioGapSize:              512,
		LowMmioGapSize:               128,
		ConfigurationDataRoot:        `C:\vms\test`,
		SnapshotDataRoot:             `C:\vms\snap`,
		SwapFileDataRoot:             `C:\vms\swap`,
	}

	got := vmFromSettingData("test-vm", sd)

	if got.Name != "test-vm" {
		t.Errorf("Name = %q, want test-vm", got.Name)
	}
	if got.Generation != 2 {
		t.Errorf("Generation = %d, want 2", got.Generation)
	}
	if got.AutomaticCriticalErrorAction != api.CriticalErrorAction_Pause {
		t.Errorf("AutomaticCriticalErrorAction = %v, want Pause", got.AutomaticCriticalErrorAction)
	}
	if got.AutomaticStartAction != api.StartAction_Start {
		t.Errorf("AutomaticStartAction = %v, want Start", got.AutomaticStartAction)
	}
	if got.AutomaticStopAction != api.StopAction_Save {
		t.Errorf("AutomaticStopAction = %v, want Save", got.AutomaticStopAction)
	}
	if got.Notes != "line1\nline2" {
		t.Errorf("Notes = %q, want line1\\nline2", got.Notes)
	}
	if got.LockOnDisconnect != api.OnOffState_On {
		t.Errorf("LockOnDisconnect = %v, want On", got.LockOnDisconnect)
	}
	if !got.GuestControlledCacheTypes {
		t.Error("GuestControlledCacheTypes = false, want true")
	}
	if got.HighMemoryMappedIoSpace != 512 {
		t.Errorf("HighMemoryMappedIoSpace = %d, want 512", got.HighMemoryMappedIoSpace)
	}
	if got.LowMemoryMappedIoSpace != 128 {
		t.Errorf("LowMemoryMappedIoSpace = %d, want 128", got.LowMemoryMappedIoSpace)
	}
	if got.Path != `C:\vms\test` {
		t.Errorf("Path = %q", got.Path)
	}
	if got.SnapshotFileLocation != `C:\vms\snap` {
		t.Errorf("SnapshotFileLocation = %q", got.SnapshotFileLocation)
	}
	if got.SmartPagingFilePath != `C:\vms\swap` {
		t.Errorf("SmartPagingFilePath = %q", got.SmartPagingFilePath)
	}
}

// TestDeleteVm_DefinedInWsmanPackage は DeleteVm が本パッケージで定義されている
// (= シャドウイングが効き、PowerShell 版を置き換える) ことをシグネチャで確認する。
func TestDeleteVm_DefinedInWsmanPackage(t *testing.T) {
	cType := reflect.TypeOf((*ClientConfig)(nil))
	method, ok := cType.MethodByName("DeleteVm")
	if !ok {
		t.Fatal("ClientConfig should have DeleteVm method")
	}
	if method.Type.NumIn() != 3 { // receiver + ctx + name
		t.Errorf("DeleteVm: NumIn = %d, want 3", method.Type.NumIn())
	}
}

// TestNeedsTurnOff は EnabledState から「削除前に停止が必要か」の判定を検証する。
//
// DestroySystem は起動中 (= Off 以外) の VM では失敗するため、Off(3) 以外は
// すべて停止が必要と判定する (Running/Paused/Saved/Unknown を一律カバー)。
func TestNeedsTurnOff(t *testing.T) {
	tests := []struct {
		name  string
		state uint16
		want  bool
	}{
		{"Off は停止不要", hyperv.EnabledStateDisabled, false},
		{"Running は停止必要", hyperv.EnabledStateEnabled, true},
		{"Paused は停止必要", hyperv.EnabledStatePaused, true},
		{"Saved は停止必要", hyperv.EnabledStateSaved, true},
		{"Unknown は停止必要 (安全側)", hyperv.EnabledStateUnknown, true},
	}
	for _, tt := range tests {
		if got := needsTurnOff(tt.state); got != tt.want {
			t.Errorf("needsTurnOff(%d) [%s] = %v, want %v", tt.state, tt.name, got, tt.want)
		}
	}
}
