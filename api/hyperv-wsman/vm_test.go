package hyperv_wsman

import (
	"math"
	"reflect"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestClientConfig_ImplementsHypervVmClient は ClientConfig が api.HypervVmClient を
// 実装することを検証する。Phase C-1 完了後は VM CRUD 全メソッドが本パッケージで定義
// (シャドウイング) され、PowerShell 版を置き換える。
func TestClientConfig_ImplementsHypervVmClient(t *testing.T) {
	var c *ClientConfig
	var _ api.HypervVmClient = c // コンパイル時チェック

	cType := reflect.TypeOf((*ClientConfig)(nil))
	for _, methodName := range []string{
		"VmExists", // ← 本パッケージで定義 (シャドウイング、C-1.1)
		"GetVm",    // ← 本パッケージで定義 (シャドウイング、C-1.1)
		"CreateVm", // ← 本パッケージで定義 (シャドウイング、C-1.2)
		"UpdateVm", // ← 本パッケージで定義 (シャドウイング、C-1.3)
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
		UserSnapshotType:             hyperv.UserSnapshotTypeProductionNoFallback,
	}

	got, err := vmFromSettingData("test-vm", sd)
	if err != nil {
		t.Fatalf("vmFromSettingData: %v", err)
	}

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
	// HighMmioGapSize/LowMmioGapSize は CIM 上 MB、api.Vm は byte (実運用移行の実機検証で発見)。
	if got.HighMemoryMappedIoSpace != 512*1024*1024 {
		t.Errorf("HighMemoryMappedIoSpace = %d, want %d (512MiB)", got.HighMemoryMappedIoSpace, uint64(512*1024*1024))
	}
	if got.LowMemoryMappedIoSpace != 128*1024*1024 {
		t.Errorf("LowMemoryMappedIoSpace = %d, want %d (128MiB)", got.LowMemoryMappedIoSpace, uint32(128*1024*1024))
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
	// UserSnapshotType(CIM) → CheckpointType(api) は値が一致する定義のため直接変換 (#106)。
	if got.CheckpointType != api.CheckpointType_ProductionOnly {
		t.Errorf("CheckpointType = %v, want ProductionOnly", got.CheckpointType)
	}
}

// TestVmFromSettingData_InvalidUserSnapshotType は UserSnapshotType が既知の値
// (2-5) 以外の場合に fail-loud でエラーになることを検証する。CIM 応答異常
// (フィールド欠落によるゼロ値・想定外の列挙値) を、空文字列の checkpoint_type として
// 静かに #106 型の drift を再発させないためのガード (Fable 批判的レビュー指摘)。
func TestVmFromSettingData_InvalidUserSnapshotType(t *testing.T) {
	tests := []struct {
		name             string
		userSnapshotType uint16
	}{
		{"ゼロ値(フィールド欠落)", 0},
		{"既知範囲外(6)", 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sd := &hyperv.Msvm_VirtualSystemSettingData{UserSnapshotType: tt.userSnapshotType}
			if _, err := vmFromSettingData("test-vm", sd); err == nil {
				t.Errorf("vmFromSettingData(UserSnapshotType=%d): エラーを期待したが nil", tt.userSnapshotType)
			}
		})
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

// TestCreateVm_DefinedInWsmanPackage は CreateVm が本パッケージで定義されている
// (= シャドウイングが効き、PowerShell 版を置き換える) ことを確認する。
func TestCreateVm_DefinedInWsmanPackage(t *testing.T) {
	cType := reflect.TypeOf((*ClientConfig)(nil))
	if _, ok := cType.MethodByName("CreateVm"); !ok {
		t.Fatal("ClientConfig should have CreateVm method")
	}
}

// TestVmSubTypeFromGeneration は Generation 番号 → CIM VirtualSystemSubType 変換を検証する。
func TestVmSubTypeFromGeneration(t *testing.T) {
	tests := []struct {
		gen     int
		want    string
		wantErr bool
	}{
		{1, hyperv.VirtualSystemSubTypeGen1, false},
		{2, hyperv.VirtualSystemSubTypeGen2, false},
		{0, "", true},
		{3, "", true},
	}
	for _, tt := range tests {
		got, err := vmSubTypeFromGeneration(tt.gen)
		if (err != nil) != tt.wantErr {
			t.Errorf("vmSubTypeFromGeneration(%d) err=%v, wantErr=%v", tt.gen, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("vmSubTypeFromGeneration(%d)=%q, want %q", tt.gen, got, tt.want)
		}
	}
}

// TestEnumToUint16 は provider enum(int)→CIM uint16 の安全変換を検証する。
func TestEnumToUint16(t *testing.T) {
	tests := []struct {
		in   int
		want uint16
	}{
		{0, 0},
		{4, 4},
		{-1, 0}, // 負値は 0
		{math.MaxUint16, math.MaxUint16},
		{math.MaxUint16 + 1, 0}, // 範囲外は 0
	}
	for _, tt := range tests {
		if got := enumToUint16(tt.in); got != tt.want {
			t.Errorf("enumToUint16(%d)=%d, want %d", tt.in, got, tt.want)
		}
	}
}

// TestBytesToMB は バイト→MB 変換 (CIM Memory/MMIO は MB 単位) を検証する。
func TestBytesToMB(t *testing.T) {
	tests := []struct {
		bytes int64
		want  uint64
	}{
		{0, 0},
		{1048576, 1},       // 1 MiB
		{2147483648, 2048}, // 2 GiB
		{1048575, 0},       // 1 MiB 未満は切り捨て
		{-1, 0},            // 負値は 0
	}
	for _, tt := range tests {
		if got := bytesToMB(tt.bytes); got != tt.want {
			t.Errorf("bytesToMB(%d)=%d, want %d", tt.bytes, got, tt.want)
		}
	}
}

// TestApplyMemorySettings は static/dynamic メモリ設定の適用を検証する。
func TestApplyMemorySettings(t *testing.T) {
	t.Run("static は固定メモリ・Min/Max無視", func(t *testing.T) {
		m := &hyperv.Msvm_MemorySettingData{InstanceID: "x"}
		applyMemorySettings(m, true, false, 2147483648, 1073741824, 4294967296)
		if m.DynamicMemoryEnabled {
			t.Error("static: DynamicMemoryEnabled は false であるべき")
		}
		if m.VirtualQuantity != 2048 {
			t.Errorf("static: VirtualQuantity=%d, want 2048", m.VirtualQuantity)
		}
		if m.Reservation != 0 || m.Limit != 0 {
			t.Errorf("static: Min/Max は未設定であるべき (Reservation=%d Limit=%d)", m.Reservation, m.Limit)
		}
	})
	t.Run("dynamic は Reservation=Min / Limit=Max", func(t *testing.T) {
		m := &hyperv.Msvm_MemorySettingData{InstanceID: "x"}
		applyMemorySettings(m, false, true, 2147483648, 1073741824, 4294967296)
		if !m.DynamicMemoryEnabled {
			t.Error("dynamic: DynamicMemoryEnabled は true であるべき")
		}
		if m.VirtualQuantity != 2048 {
			t.Errorf("dynamic: VirtualQuantity=%d, want 2048", m.VirtualQuantity)
		}
		if m.Reservation != 1024 {
			t.Errorf("dynamic: Reservation=%d, want 1024", m.Reservation)
		}
		if m.Limit != 4096 {
			t.Errorf("dynamic: Limit=%d, want 4096", m.Limit)
		}
	})
}

// TestMbToBytes は bytesToMB の逆変換 (READ 側、GetVm が使う) を検証する。
func TestMbToBytes(t *testing.T) {
	tests := []struct {
		mb   uint64
		want int64
	}{
		{0, 0},
		{1, 1048576},       // 1 MiB
		{2048, 2147483648}, // 2 GiB
		{math.MaxUint64, math.MaxInt64 / (1024 * 1024) * (1024 * 1024)}, // 上限クランプ
	}
	for _, tt := range tests {
		if got := mbToBytes(tt.mb); got != tt.want {
			t.Errorf("mbToBytes(%d)=%d, want %d", tt.mb, got, tt.want)
		}
	}
}

// TestMbToBytesU64 は mbToBytes の uint64 版 (HighMemoryMappedIoSpace 等) を検証する。
func TestMbToBytesU64(t *testing.T) {
	tests := []struct {
		mb   uint64
		want uint64
	}{
		{0, 0},
		{512, 512 * 1024 * 1024},
		{math.MaxUint64, math.MaxUint64 / (1024 * 1024) * (1024 * 1024)}, // 上限クランプ
	}
	for _, tt := range tests {
		if got := mbToBytesU64(tt.mb); got != tt.want {
			t.Errorf("mbToBytesU64(%d)=%d, want %d", tt.mb, got, tt.want)
		}
	}
}

// TestApplyMemoryToVm は Msvm_MemorySettingData → api.Vm のメモリ関連フィールドへのマッピングを
// 検証する (実運用の実機検証で発見: このマッピングが無いと DynamicMemory/StaticMemory が両方
// false のゼロ値のままになり、resource read が「Either dynamic or static must be selected」で
// 実機の全 VM で失敗していた)。
func TestApplyMemoryToVm(t *testing.T) {
	t.Run("static (DynamicMemoryEnabled=false)", func(t *testing.T) {
		vm := &api.Vm{}
		applyMemoryToVm(vm, &hyperv.Msvm_MemorySettingData{
			VirtualQuantity: 8192, DynamicMemoryEnabled: false, Reservation: 8192, Limit: 8192,
		})
		if vm.DynamicMemory {
			t.Error("DynamicMemory は false であるべき")
		}
		if !vm.StaticMemory {
			t.Error("StaticMemory は true であるべき")
		}
		if vm.MemoryStartupBytes != 8*1024*1024*1024 {
			t.Errorf("MemoryStartupBytes=%d, want 8GiB", vm.MemoryStartupBytes)
		}
	})
	t.Run("dynamic (DynamicMemoryEnabled=true)", func(t *testing.T) {
		vm := &api.Vm{}
		applyMemoryToVm(vm, &hyperv.Msvm_MemorySettingData{
			VirtualQuantity: 2048, DynamicMemoryEnabled: true, Reservation: 1024, Limit: 4096,
		})
		if !vm.DynamicMemory {
			t.Error("DynamicMemory は true であるべき")
		}
		if vm.StaticMemory {
			t.Error("StaticMemory は false であるべき")
		}
		if vm.MemoryMinimumBytes != 1024*1024*1024 {
			t.Errorf("MemoryMinimumBytes=%d, want 1GiB", vm.MemoryMinimumBytes)
		}
		if vm.MemoryMaximumBytes != 4*1024*1024*1024 {
			t.Errorf("MemoryMaximumBytes=%d, want 4GiB", vm.MemoryMaximumBytes)
		}
	})
}

// TestParseIntervalMinutes は WS-Man の datetime(interval) 生文字列 (ISO 8601 duration 形式、
// 実機確認 2026-07-27: "P0DT0H30M0S") を分単位に変換することを検証する。
// MOF ドキュメント記載の COM/WMI ネイティブ形式 (ddddddddHHMMSS.mmmmmm:000) とは異なる実機実測値。
func TestParseIntervalMinutes(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int32
		wantErr bool
	}{
		{"実機確認値 30分", "P0DT0H30M0S", 30, false},
		{"0分 (即座に電源オフ)", "P0DT0H0M0S", 0, false},
		{"空文字はゼロ扱い", "", 0, false},
		{"時+分の合算 (1時間30分=90分)", "P0DT1H30M0S", 90, false},
		{"日+時+分の合算 (1日2時間3分=1563分)", "P1DT2H3M0S", 1*24*60 + 2*60 + 3, false},
		{"不正な書式はエラー", "not-a-duration", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIntervalMinutes(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseIntervalMinutes(%q): err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("parseIntervalMinutes(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// TestVmSettingDataForCreate は CreateVm パラメータ → Msvm_VirtualSystemSettingData
// マッピング (GetVm の vmFromSettingData の逆) を検証する。
func TestVmSettingDataForCreate(t *testing.T) {
	const (
		cfgPath  = `C:\hyperv\create-test`
		pagePath = `C:\hyperv\paging`
		snapPath = `C:\hyperv\snap`
	)
	sd, err := vmSettingDataForCreate(
		"vm1", cfgPath, 2,
		api.CriticalErrorAction_Pause, api.StartAction_Start, api.StopAction_Save,
		true, 512, api.OnOffState_On, 128,
		"note1\nnote2", pagePath, snapPath,
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if sd.ElementName != "vm1" {
		t.Errorf("ElementName=%q", sd.ElementName)
	}
	if sd.VirtualSystemSubType != hyperv.VirtualSystemSubTypeGen2 {
		t.Errorf("VirtualSystemSubType=%q", sd.VirtualSystemSubType)
	}
	if sd.ConfigurationDataRoot != cfgPath {
		t.Errorf("ConfigurationDataRoot=%q", sd.ConfigurationDataRoot)
	}
	if sd.AutomaticCriticalErrorAction != 1 { // Pause
		t.Errorf("AutomaticCriticalErrorAction=%d, want 1", sd.AutomaticCriticalErrorAction)
	}
	if sd.AutomaticStartupAction != 4 { // Start
		t.Errorf("AutomaticStartupAction=%d, want 4", sd.AutomaticStartupAction)
	}
	if sd.AutomaticShutdownAction != 3 { // Save
		t.Errorf("AutomaticShutdownAction=%d, want 3", sd.AutomaticShutdownAction)
	}
	if !sd.LockOnDisconnect {
		t.Error("LockOnDisconnect は true であるべき")
	}
	if !sd.GuestControlledCacheTypes {
		t.Error("GuestControlledCacheTypes は true であるべき")
	}
	if sd.HighMmioGapSize != 512 {
		t.Errorf("HighMmioGapSize=%d, want 512", sd.HighMmioGapSize)
	}
	if sd.LowMmioGapSize != 128 {
		t.Errorf("LowMmioGapSize=%d, want 128", sd.LowMmioGapSize)
	}
	if sd.SnapshotDataRoot != snapPath {
		t.Errorf("SnapshotDataRoot=%q", sd.SnapshotDataRoot)
	}
	if sd.SwapFileDataRoot != pagePath {
		t.Errorf("SwapFileDataRoot=%q", sd.SwapFileDataRoot)
	}
	if len(sd.Notes) != 2 || sd.Notes[0] != "note1" || sd.Notes[1] != "note2" {
		t.Errorf("Notes=%v, want [note1 note2]", sd.Notes)
	}

	if _, err := vmSettingDataForCreate("x", "", 9, 0, 0, 0, false, 0, api.OnOffState_Off, 0, "", "", ""); err == nil {
		t.Error("generation=9 はエラーになるべき")
	}
}

// TestUpdateVm_DefinedInWsmanPackage は UpdateVm が本パッケージで定義されている
// (= シャドウイングが効き、PowerShell 版を置き換える) ことを確認する。
func TestUpdateVm_DefinedInWsmanPackage(t *testing.T) {
	cType := reflect.TypeOf((*ClientConfig)(nil))
	if _, ok := cType.MethodByName("UpdateVm"); !ok {
		t.Fatal("ClientConfig should have UpdateVm method")
	}
}

// TestApplyVmLevelSettings は VM レベル可変フィールドの適用 (Create/Update 共有) を検証する。
//
// 既存 settings の InstanceID / SubType は保持し、可変フィールドのみ上書きすること。
// 空文字のパスは「変更なし」として既存値を維持する (CIM ModifySystemSettings の慣習)。
func TestApplyVmLevelSettings(t *testing.T) {
	const existingSnap = `C:\existing\snap`
	sd := &hyperv.Msvm_VirtualSystemSettingData{
		InstanceID:           "keep-me",
		VirtualSystemSubType: hyperv.VirtualSystemSubTypeGen2,
		SnapshotDataRoot:     existingSnap,
	}
	applyVmLevelSettings(sd,
		api.CriticalErrorAction_Pause, api.StartAction_Start, api.StopAction_Save,
		true, 256, api.OnOffState_On, 64,
		"n1\nn2", `C:\new\paging`, "") // snapshot="" → 既存維持

	if sd.InstanceID != "keep-me" {
		t.Errorf("InstanceID は保持されるべき: %q", sd.InstanceID)
	}
	if sd.VirtualSystemSubType != hyperv.VirtualSystemSubTypeGen2 {
		t.Errorf("VirtualSystemSubType は保持されるべき: %q", sd.VirtualSystemSubType)
	}
	if sd.AutomaticStartupAction != 4 || sd.AutomaticShutdownAction != 3 || sd.AutomaticCriticalErrorAction != 1 {
		t.Errorf("enum 適用ミス: start=%d stop=%d crit=%d",
			sd.AutomaticStartupAction, sd.AutomaticShutdownAction, sd.AutomaticCriticalErrorAction)
	}
	if !sd.LockOnDisconnect || !sd.GuestControlledCacheTypes {
		t.Error("bool フィールド適用ミス")
	}
	if sd.HighMmioGapSize != 256 || sd.LowMmioGapSize != 64 {
		t.Errorf("MMIO 適用ミス: high=%d low=%d", sd.HighMmioGapSize, sd.LowMmioGapSize)
	}
	if sd.SwapFileDataRoot != `C:\new\paging` {
		t.Errorf("SwapFileDataRoot=%q", sd.SwapFileDataRoot)
	}
	if sd.SnapshotDataRoot != existingSnap {
		t.Errorf("snapshot 空文字なら既存維持のはず: %q", sd.SnapshotDataRoot)
	}
	if len(sd.Notes) != 2 || sd.Notes[0] != "n1" || sd.Notes[1] != "n2" {
		t.Errorf("Notes=%v, want [n1 n2]", sd.Notes)
	}
}

// TestValidateCheckpointFieldsUnchanged は checkpointType/automaticCheckpointsEnabled の
// 変更要求を UpdateVm が黙って無視せず検知することを検証する (#106 Fable 批判的レビュー指摘、
// write 側が #46 まで未実装のため、変更要求は fail-loud にする必要がある)。
func TestValidateCheckpointFieldsUnchanged(t *testing.T) {
	cur := &hyperv.Msvm_VirtualSystemSettingData{
		UserSnapshotType:          hyperv.UserSnapshotTypeProductionFallbackToTest, // Production(3)
		AutomaticSnapshotsEnabled: false,
	}

	t.Run("変更なしなら nil", func(t *testing.T) {
		if err := validateCheckpointFieldsUnchanged(api.CheckpointType_Production, false, cur); err != nil {
			t.Errorf("変更なしのはずがエラー: %v", err)
		}
	})

	t.Run("checkpoint_type の変更要求はエラー", func(t *testing.T) {
		if err := validateCheckpointFieldsUnchanged(api.CheckpointType_Disabled, false, cur); err == nil {
			t.Error("checkpoint_type 変更要求はエラーになるべき (WS-Man write未実装)")
		}
	})

	t.Run("automatic_checkpoints_enabled の変更要求はエラー", func(t *testing.T) {
		if err := validateCheckpointFieldsUnchanged(api.CheckpointType_Production, true, cur); err == nil {
			t.Error("automatic_checkpoints_enabled 変更要求はエラーになるべき (WS-Man write未実装)")
		}
	})

	t.Run("現在値が不正(UserSnapshotType未知)ならエラー", func(t *testing.T) {
		bad := &hyperv.Msvm_VirtualSystemSettingData{UserSnapshotType: 0}
		if err := validateCheckpointFieldsUnchanged(api.CheckpointType_Production, false, bad); err == nil {
			t.Error("不正な現在値はエラーになるべき")
		}
	})
}
