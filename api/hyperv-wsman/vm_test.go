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
		// GuestControlledCacheTypes と AutomaticSnapshotsEnabled は値を **意図的に分ける**。
		// 同値だと bool 同士の取り違え (AutomaticCheckpointsEnabled に
		// GuestControlledCacheTypes を代入する等) をテストが区別できない。
		GuestControlledCacheTypes: false,
		HighMmioGapSize:           512,
		LowMmioGapSize:            128,
		ConfigurationDataRoot:     `C:\vms\test`,
		SnapshotDataRoot:          `C:\vms\snap`,
		SwapFileDataRoot:          `C:\vms\swap`,
		UserSnapshotType:          hyperv.UserSnapshotTypeProductionNoFallback,
		AutomaticSnapshotsEnabled: true,
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
	// #134: 未マッピングで常に false を返していた。実機既定が true のため、
	// これが抜けると「read は false / 実機は true」の恒常 diff になる。
	if !got.AutomaticCheckpointsEnabled {
		t.Error("AutomaticCheckpointsEnabled = false, want true (#134)")
	}
	if got.LockOnDisconnect != api.OnOffState_On {
		t.Errorf("LockOnDisconnect = %v, want On", got.LockOnDisconnect)
	}
	if got.GuestControlledCacheTypes {
		t.Error("GuestControlledCacheTypes = true, want false")
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

// TestBytesToMbU64 は mbToBytesU64 の逆変換 (write 側、#105) を検証する。
func TestBytesToMbU64(t *testing.T) {
	tests := []struct {
		bytes uint64
		want  uint64
	}{
		{0, 0},
		{512 * 1024 * 1024, 512},
		{128 * 1024 * 1024, 128},
		{100, 0}, // MB 境界未満は切り捨て
	}
	for _, tt := range tests {
		if got := bytesToMbU64(tt.bytes); got != tt.want {
			t.Errorf("bytesToMbU64(%d)=%d, want %d", tt.bytes, got, tt.want)
		}
	}
}

// TestMbBytesRoundTrip は mbToBytesU64 (read) と bytesToMbU64 (write) が MB 境界に揃った値で
// 厳密に往復することを検証する (DoD: 単位変換は round-trip test で固定する)。
func TestMbBytesRoundTrip(t *testing.T) {
	for _, mb := range []uint64{0, 1, 128, 512, 3584, 4096} {
		bytes := mbToBytesU64(mb)
		if got := bytesToMbU64(bytes); got != mb {
			t.Errorf("round-trip: mb=%d → bytes=%d → mb=%d (want %d)", mb, bytes, got, mb)
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
		true, 512*1024*1024, api.OnOffState_On, 128*1024*1024,
		"note1\nnote2", pagePath, snapPath,
		// checkpointType は他の enum 引数と異なる値、automaticCheckpointsEnabled は
		// guestControlledCacheTypes(true) と **異なる値** にして取り違えを検出可能にする。
		api.CheckpointType_Production, false,
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
	// Notes は 1 要素に改行を含める形が正しい。複数要素で送ると Hyper-V が先頭以外を
	// 捨てるため、以前の「改行で分割して 2 要素」はバグを仕様として固定していた (#145)。
	if len(sd.Notes) != 1 || sd.Notes[0] != "note1\nnote2" {
		t.Errorf("Notes=%v, want [\"note1\\nnote2\"] (1 要素)", sd.Notes)
	}
	// #125: create 経路で checkpoint 系が配線されていること。
	if sd.UserSnapshotType != 3 { // Production
		t.Errorf("UserSnapshotType=%d, want 3 (Production)", sd.UserSnapshotType)
	}
	// guestControlledCacheTypes=true を渡しているので、取り違えていればここが true になる。
	if sd.AutomaticSnapshotsEnabled {
		t.Error("AutomaticSnapshotsEnabled=true, want false (引数の取り違え?)")
	}

	// 逆の組み合わせでも配線を確認する (bool 2 つの値を入れ替える)。
	sd2, err := vmSettingDataForCreate(
		"vm2", cfgPath, 2,
		api.CriticalErrorAction_Pause, api.StartAction_Start, api.StopAction_Save,
		false, 512*1024*1024, api.OnOffState_On, 128*1024*1024,
		"", pagePath, snapPath,
		api.CheckpointType_Standard, true,
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if sd2.UserSnapshotType != 5 { // Standard
		t.Errorf("sd2.UserSnapshotType=%d, want 5 (Standard)", sd2.UserSnapshotType)
	}
	if !sd2.AutomaticSnapshotsEnabled {
		t.Error("sd2.AutomaticSnapshotsEnabled=false, want true")
	}
	if sd2.GuestControlledCacheTypes {
		t.Error("sd2.GuestControlledCacheTypes=true, want false (取り違え?)")
	}

	if _, err := vmSettingDataForCreate("x", "", 9, 0, 0, 0, false, 0, api.OnOffState_Off, 0, "", "", "", 0, false); err == nil {
		t.Error("generation=9 はエラーになるべき")
	}
	// 範囲外の checkpoint_type は黙って通さない (#125)。
	if _, err := vmSettingDataForCreate("x", "", 2, 0, 0, 0, false, 0, api.OnOffState_Off, 0, "", "", "", api.CheckpointType(99), false); err == nil {
		t.Error("checkpoint_type=99 はエラーになるべき")
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
	if err := applyVmLevelSettings(sd, vmLevelWant{
		criticalErrorAction:       api.CriticalErrorAction_Pause,
		startAction:               api.StartAction_Start,
		stopAction:                api.StopAction_Save,
		guestControlledCacheTypes: true,
		highMmioGapSize:           256 * 1024 * 1024,
		lockOnDisconnect:          api.OnOffState_On,
		lowMmioGapSize:            64 * 1024 * 1024,
		notes:                     "n1\nn2",
		smartPagingFilePath:       `C:\new\paging`,
		snapshotFileLocation:      "", // 既存維持
	}); err != nil {
		t.Fatalf("applyVmLevelSettings: %v", err)
	}

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
	if len(sd.Notes) != 1 || sd.Notes[0] != "n1\nn2" {
		t.Errorf("Notes=%v, want [\"n1\\nn2\"] (1 要素、#145)", sd.Notes)
	}
}

// TestVmLevelZeroDowngrade は VM レベル設定・メモリ設定の「非ゼロ→0 / true→false」検出を検証する。
// これらは marshalEmbeddedInstance のゼロ値非送信により CIM で表現できず、PS 委譲が必要になる。
//
// フィクスチャを 2 種類持つのが要点。「現行が非ゼロ」のケースだけだと、判定式から
// 「現行が非ゼロ」の連言を落としても全ケースが通ってしまい、テストがトートロジーになる
// (Fable 批判的レビューでミューテーション 9/9 生存を実証)。schema 既定は
// lock_on_disconnect=Off / guest_controlled_cache_types=false / static_memory 系なので、
// 「現行もゼロなら委譲しない」が壊れると **全ての UpdateVm が PS へ委譲され PS-0 が黙って崩壊する**。
func TestVmLevelZeroDowngrade(t *testing.T) {
	// 現行 A: 全フィールドが非ゼロ / true。ここからゼロへ下げる要求は委譲対象。
	curNonZero := &hyperv.Msvm_VirtualSystemSettingData{
		AutomaticCriticalErrorAction: uint16(api.CriticalErrorAction_Pause),
		AutomaticStartupAction:       uint16(api.StartAction_StartIfRunning),
		AutomaticShutdownAction:      uint16(api.StopAction_Save),
		GuestControlledCacheTypes:    true,
		LockOnDisconnect:             true,
		HighMmioGapSize:              512,
		LowMmioGapSize:               128,
		Notes:                        []string{"memo"},
		SwapFileDataRoot:             `C:\paging`,
		SnapshotDataRoot:             `C:\snap`,
		AutomaticSnapshotsEnabled:    true,
	}
	memDynamic := &hyperv.Msvm_MemorySettingData{DynamicMemoryEnabled: true}

	// 現行 B: schema 既定に近い「もともとゼロ / false」の VM。
	// 同じゼロ値を要求してもダウングレードではないので委譲してはいけない。
	curZero := &hyperv.Msvm_VirtualSystemSettingData{
		AutomaticCriticalErrorAction: uint16(api.CriticalErrorAction_None),
		AutomaticStartupAction:       uint16(api.StartAction_Nothing),
		AutomaticShutdownAction:      uint16(api.StopAction_TurnOff),
		GuestControlledCacheTypes:    false,
		LockOnDisconnect:             false,
		HighMmioGapSize:              0,
		LowMmioGapSize:               0,
		Notes:                        nil,
		SwapFileDataRoot:             "",
		SnapshotDataRoot:             "",
	}
	memStatic := &hyperv.Msvm_MemorySettingData{DynamicMemoryEnabled: false}

	// 現行 A と一致する要求 (何も下げない)。
	wantMatchingNonZero := func() vmLevelWant {
		return vmLevelWant{
			criticalErrorAction:       api.CriticalErrorAction_Pause,
			startAction:               api.StartAction_StartIfRunning,
			stopAction:                api.StopAction_Save,
			guestControlledCacheTypes: true,
			highMmioGapSize:           512 * 1024 * 1024,
			lockOnDisconnect:          api.OnOffState_On,
			lowMmioGapSize:            128 * 1024 * 1024,
			notes:                     "memo",
			smartPagingFilePath:       `C:\paging`,
			snapshotFileLocation:      `C:\snap`,
			staticMemory:              false,
			// checkpoint 系。UserSnapshotType はゼロ値が無いのでダウングレード対象外。
			checkpointType:              api.CheckpointType_Production,
			automaticCheckpointsEnabled: true,
		}
	}

	// 現行 B と一致する要求 (全てゼロ / false)。
	wantMatchingZero := func() vmLevelWant {
		return vmLevelWant{
			criticalErrorAction:         api.CriticalErrorAction_None,
			startAction:                 api.StartAction_Nothing,
			stopAction:                  api.StopAction_TurnOff,
			guestControlledCacheTypes:   false,
			highMmioGapSize:             0,
			lockOnDisconnect:            api.OnOffState_Off,
			lowMmioGapSize:              0,
			notes:                       "",
			smartPagingFilePath:         "",
			snapshotFileLocation:        "",
			staticMemory:                true,
			checkpointType:              api.CheckpointType_Production,
			automaticCheckpointsEnabled: false,
		}
	}

	cases := []struct {
		name string
		cur  *hyperv.Msvm_VirtualSystemSettingData
		mem  *hyperv.Msvm_MemorySettingData
		base func() vmLevelWant
		mut  func(*vmLevelWant)
		down bool
	}{
		// --- 現行が非ゼロ: ゼロへ下げる要求は委譲する ---
		{"A: 変更なし", curNonZero, memDynamic, wantMatchingNonZero, func(*vmLevelWant) {}, false},
		{"A: criticalErrorAction Pause→None", curNonZero, memDynamic, wantMatchingNonZero, func(w *vmLevelWant) { w.criticalErrorAction = api.CriticalErrorAction_None }, true},
		{"A: lockOnDisconnect On→Off", curNonZero, memDynamic, wantMatchingNonZero, func(w *vmLevelWant) { w.lockOnDisconnect = api.OnOffState_Off }, true},
		{"A: guestControlledCacheTypes true→false", curNonZero, memDynamic, wantMatchingNonZero, func(w *vmLevelWant) { w.guestControlledCacheTypes = false }, true},
		{"A: notes 非空→空", curNonZero, memDynamic, wantMatchingNonZero, func(w *vmLevelWant) { w.notes = "" }, true},
		{"A: highMmioGapSize 非ゼロ→0", curNonZero, memDynamic, wantMatchingNonZero, func(w *vmLevelWant) { w.highMmioGapSize = 0 }, true},
		{"A: lowMmioGapSize 非ゼロ→0", curNonZero, memDynamic, wantMatchingNonZero, func(w *vmLevelWant) { w.lowMmioGapSize = 0 }, true},
		{"A: dynamic→static", curNonZero, memDynamic, wantMatchingNonZero, func(w *vmLevelWant) { w.staticMemory = true }, true},
		{"A: automaticCheckpointsEnabled true→false", curNonZero, memDynamic, wantMatchingNonZero, func(w *vmLevelWant) { w.automaticCheckpointsEnabled = false }, true},
		// checkpoint_type は有効値 2..5 でゼロ値が無いため、どの値へ変えても送信できる。
		{"A: checkpointType 変更はダウングレードでない", curNonZero, memDynamic, wantMatchingNonZero, func(w *vmLevelWant) { w.checkpointType = api.CheckpointType_Standard }, false},
		// パス系の空は「消す」ではなく「指定なし」(#99 と同じ意味論)。委譲しない。
		{"A: smartPagingFilePath 空 = 指定なし", curNonZero, memDynamic, wantMatchingNonZero, func(w *vmLevelWant) { w.smartPagingFilePath = "" }, false},
		{"A: snapshotFileLocation 空 = 指定なし", curNonZero, memDynamic, wantMatchingNonZero, func(w *vmLevelWant) { w.snapshotFileLocation = "" }, false},
		// ダウングレードでない変更
		{"A: notes を別の非空へ", curNonZero, memDynamic, wantMatchingNonZero, func(w *vmLevelWant) { w.notes = "other" }, false},
		{"A: highMmioGapSize を増やす", curNonZero, memDynamic, wantMatchingNonZero, func(w *vmLevelWant) { w.highMmioGapSize = 1024 * 1024 * 1024 }, false},

		// --- 現行もゼロ: 同じゼロ値の要求は委譲しない (PS-0 を守る) ---
		// このブロックが無いと判定式から「現行が非ゼロ」の連言を落としても検出できない。
		{"B: 全てゼロ同士 (既定 VM の no-op apply)", curZero, memStatic, wantMatchingZero, func(*vmLevelWant) {}, false},
		{"B: criticalErrorAction None のまま", curZero, memStatic, wantMatchingZero, func(w *vmLevelWant) { w.criticalErrorAction = api.CriticalErrorAction_None }, false},
		{"B: lockOnDisconnect Off のまま", curZero, memStatic, wantMatchingZero, func(w *vmLevelWant) { w.lockOnDisconnect = api.OnOffState_Off }, false},
		{"B: guestControlledCacheTypes false のまま", curZero, memStatic, wantMatchingZero, func(w *vmLevelWant) { w.guestControlledCacheTypes = false }, false},
		{"B: notes 空のまま", curZero, memStatic, wantMatchingZero, func(w *vmLevelWant) { w.notes = "" }, false},
		{"B: MMIO 0 のまま", curZero, memStatic, wantMatchingZero, func(w *vmLevelWant) { w.highMmioGapSize, w.lowMmioGapSize = 0, 0 }, false},
		{"B: 既に static のまま", curZero, memStatic, wantMatchingZero, func(w *vmLevelWant) { w.staticMemory = true }, false},
		// 現行ゼロから上げる方向は当然委譲不要
		{"B: automaticCheckpointsEnabled false のまま", curZero, memStatic, wantMatchingZero, func(w *vmLevelWant) { w.automaticCheckpointsEnabled = false }, false},
		{"B: automaticCheckpointsEnabled false→true", curZero, memStatic, wantMatchingZero, func(w *vmLevelWant) { w.automaticCheckpointsEnabled = true }, false},
		{"B: notes 空→非空", curZero, memStatic, wantMatchingZero, func(w *vmLevelWant) { w.notes = "new" }, false},
		{"B: lockOnDisconnect Off→On", curZero, memStatic, wantMatchingZero, func(w *vmLevelWant) { w.lockOnDisconnect = api.OnOffState_On }, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := tc.base()
			tc.mut(&w)
			if got := vmLevelZeroDowngrade(tc.cur, tc.mem, w); got != tc.down {
				t.Errorf("vmLevelZeroDowngrade = %v, want %v", got, tc.down)
			}
		})
	}
}

// TestUserSnapshotTypeFromCheckpointType は api.CheckpointType → CIM UserSnapshotType の
// 変換を検証する。両者は #106 で MOF 突合済みの数値一致だが、範囲外を黙って通すと
// Hyper-V 側で不定の挙動になるため検証を挟む。
func TestUserSnapshotTypeFromCheckpointType(t *testing.T) {
	cases := []struct {
		name    string
		in      api.CheckpointType
		want    uint16
		wantErr bool
	}{
		{"Disabled", api.CheckpointType_Disabled, 2, false},
		{"Production", api.CheckpointType_Production, 3, false},
		{"ProductionOnly", api.CheckpointType_ProductionOnly, 4, false},
		{"Standard", api.CheckpointType_Standard, 5, false},
		{"ゼロ値は無効 (未設定と区別できない)", api.CheckpointType(0), 0, true},
		{"範囲外 1", api.CheckpointType(1), 0, true},
		{"範囲外 6", api.CheckpointType(6), 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := userSnapshotTypeFromCheckpointType(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("= %d, want %d", got, tc.want)
			}
		})
	}
}

// TestApplyVmLevelSettingsWritesCheckpointFields は applyVmLevelSettings が
// checkpoint 系フィールドを実際に SettingData へ書くことを検証する (#125)。
//
// UserSnapshotType は有効値が 2..5 でゼロ値が無いため、marshalEmbeddedInstance の
// ゼロ値スキップに掛からず常に送信される (実機確認済み)。
func TestApplyVmLevelSettingsWritesCheckpointFields(t *testing.T) {
	sd := &hyperv.Msvm_VirtualSystemSettingData{}
	if err := applyVmLevelSettings(sd, vmLevelWant{
		criticalErrorAction:         api.CriticalErrorAction_Pause,
		startAction:                 api.StartAction_Nothing,
		stopAction:                  api.StopAction_TurnOff,
		checkpointType:              api.CheckpointType_Production,
		automaticCheckpointsEnabled: true,
	}); err != nil {
		t.Fatalf("applyVmLevelSettings: %v", err)
	}
	if sd.UserSnapshotType != 3 {
		t.Errorf("UserSnapshotType = %d, want 3 (Production)", sd.UserSnapshotType)
	}
	if !sd.AutomaticSnapshotsEnabled {
		t.Error("AutomaticSnapshotsEnabled = false, want true")
	}

	// 未指定 (ゼロ値) の CheckpointType は書かない。既存 VM の設定を壊さないため。
	// あわせて automaticCheckpointsEnabled=false が定数 true で潰されていないことも見る。
	sd2 := &hyperv.Msvm_VirtualSystemSettingData{}
	if err := applyVmLevelSettings(sd2, vmLevelWant{checkpointType: api.CheckpointType(0)}); err != nil {
		t.Fatalf("applyVmLevelSettings: %v", err)
	}
	if sd2.UserSnapshotType != 0 {
		t.Errorf("未指定時の UserSnapshotType = %d, want 0 (送らない)", sd2.UserSnapshotType)
	}
	if sd2.AutomaticSnapshotsEnabled {
		t.Error("AutomaticSnapshotsEnabled = true, want false (定数で潰されていないか)")
	}

	// bool 2 つが互いに独立して配線されていること
	// (guestControlledCacheTypes と automaticCheckpointsEnabled の混線検出)。
	sd3 := &hyperv.Msvm_VirtualSystemSettingData{}
	if err := applyVmLevelSettings(sd3, vmLevelWant{
		guestControlledCacheTypes:   false,
		automaticCheckpointsEnabled: true,
	}); err != nil {
		t.Fatalf("applyVmLevelSettings: %v", err)
	}
	if sd3.GuestControlledCacheTypes {
		t.Error("GuestControlledCacheTypes = true, want false (混線?)")
	}
	if !sd3.AutomaticSnapshotsEnabled {
		t.Error("AutomaticSnapshotsEnabled = false, want true")
	}
}

// TestApplyVmLevelSettingsNotesSingleElement は複数行 notes が 1 要素で送られることを検証する。
//
// Hyper-V の Notes は MOF 上 string[] だが実質単一値で、複数要素を送ると先頭以外が
// 捨てられる (実機確認)。分割して送っていたため複数行 notes が 1 行目だけになり、
// read は実値を返すので恒常 diff + apply のたびに VM 停止になっていた (#145)。
func TestApplyVmLevelSettingsNotesSingleElement(t *testing.T) {
	cases := []struct {
		name  string
		notes string
		want  []string
	}{
		{"単一行", "hello", []string{"hello"}},
		{"複数行は 1 要素に改行を含める", "alpha\nbeta\ngamma", []string{"alpha\nbeta\ngamma"}},
		{"空は送らない", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sd := &hyperv.Msvm_VirtualSystemSettingData{}
			if err := applyVmLevelSettings(sd, vmLevelWant{notes: tc.notes}); err != nil {
				t.Fatalf("applyVmLevelSettings: %v", err)
			}
			if len(sd.Notes) != len(tc.want) {
				t.Fatalf("Notes = %v (%d 要素), want %v (%d 要素)。"+
					"複数要素で送ると Hyper-V が先頭以外を捨てる", sd.Notes, len(sd.Notes), tc.want, len(tc.want))
			}
			for i := range tc.want {
				if sd.Notes[i] != tc.want[i] {
					t.Errorf("Notes[%d] = %q, want %q", i, sd.Notes[i], tc.want[i])
				}
			}
		})
	}

	// read との round-trip。1 要素なら Join は恒等になる。
	sd := &hyperv.Msvm_VirtualSystemSettingData{}
	const multi = "alpha\nbeta\ngamma"
	if err := applyVmLevelSettings(sd, vmLevelWant{notes: multi}); err != nil {
		t.Fatalf("applyVmLevelSettings: %v", err)
	}
	sd.VirtualSystemSubType = hyperv.VirtualSystemSubTypeGen1
	sd.UserSnapshotType = hyperv.UserSnapshotTypeProductionFallbackToTest
	got, err := vmFromSettingData("vm-1", sd)
	if err != nil {
		t.Fatalf("vmFromSettingData: %v", err)
	}
	if got.Notes != multi {
		t.Errorf("round-trip 不一致: got %q, want %q", got.Notes, multi)
	}
}
