package hyperv_wsman

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// intervalISO8601Pattern は WS-Man の GET/PULL レスポンスが返す datetime(interval) 型の実機書式に
// マッチする。実機確認 (2026-07-27、実機 VM の read): "P0DT0H30M0S"。ISO 8601 duration 形式
// (PnDTnHnMnS)。WS-Management が CIM datetime interval をレスポンス側で xs:duration に
// マッピングするための wire format と考えられる。
//
// 書き込み方向 (embedded instance) は未実装 (#102 参照)。CIM-XML の TYPE 属性が Go の
// string kind から一律 "string" と推論され (go-wsman hyperv/embedded.go の cimTypeName)、
// この MOF 上 datetime 型のプロパティを "string" 型として送ると DefineSystem が
// ErrorCode=32768 (Exception) で失敗することを実機確認済み。ISO 8601 / CIM ネイティブ形式
// (ddddddddHHMMSS.mmmmmm:000) いずれの文字列内容でも同一エラーになるため、原因は文字列書式
// ではなく TYPE 属性側。go-wsman 側に datetime 型を明示できる仕組み (cim タグ拡張等) が要る。
var intervalISO8601Pattern = regexp.MustCompile(`^P(\d+)DT(\d+)H(\d+)M(\d+)S$`)

// parseIntervalMinutes は datetime(interval) 型の文字列 (ISO 8601 形式、上記パターン参照) を
// 分単位の int32 に変換する。AutomaticCriticalErrorActionTimeout は Hyper-V が分単位に丸める
// 仕様 (MOF 記載) のため秒は通常 0 だが、端数があれば切り上げる (防御的)。空文字は 0 (未設定)
// として扱う。
func parseIntervalMinutes(s string) (int32, error) {
	if s == "" {
		return 0, nil
	}
	m := intervalISO8601Pattern.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("parseIntervalMinutes: unexpected format %q (want ISO 8601 PnDTnHnMnS)", s)
	}
	days, err1 := strconv.ParseInt(m[1], 10, 64)
	hours, err2 := strconv.ParseInt(m[2], 10, 64)
	minutes, err3 := strconv.ParseInt(m[3], 10, 64)
	seconds, err4 := strconv.ParseInt(m[4], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return 0, fmt.Errorf("parseIntervalMinutes: failed to parse numeric fields in %q", s)
	}
	total := days*24*60 + hours*60 + minutes
	if seconds > 0 {
		total++
	}
	return clampInt32FromInt64(total), nil
}

// clampInt32FromInt64 は int64 を int32 に安全に縮小する (上限/下限超過はクランプ)。
// 直接 int32(v) すると CodeQL が上限チェックなしの縮小変換 (high) として検出するため
// (clampUint32/clampInt32 と同じ理由)。
func clampInt32FromInt64(v int64) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}

// VmExists は go-wsman 経由で VM (表示名) の存在を確認する。
//
// FindComputerSystemByElementName が ErrVMNotFound を返した場合のみ「不在」とし、
// それ以外のエラー (通信失敗等) は伝播させる。PowerShell 版は全エラーを不在扱いに
// していたが、リゾルバの sentinel error により「不在」と「障害」を区別できる。
func (c *ClientConfig) VmExists(ctx context.Context, name string) (api.VmExists, error) {
	_, err := c.WsmanClient.FindComputerSystemByElementName(ctx, name)
	if err != nil {
		if errors.Is(err, hyperv.ErrVMNotFound) {
			return api.VmExists{Exists: false}, nil
		}
		return api.VmExists{}, fmt.Errorf("hyperv-wsman: VmExists %q: %w", name, err)
	}
	return api.VmExists{Exists: true}, nil
}

// GetVm は go-wsman 経由で VM の構成情報を取得する。
//
// 表示名→GUID を解決し、Realized の Msvm_VirtualSystemSettingData を取得して api.Vm に
// マッピングする。Memory/ProcessorCount は別途 Msvm_MemorySettingData /
// Msvm_ProcessorSettingData を取得して合成する (resource 層の read が
// vm.DynamicMemory/StaticMemory の排他を検証するため必須。未設定のままだと両方 false の
// ゼロ値になり「Either dynamic or static must be selected」で実機の全 VM read が失敗する、
// 実運用移行の実機検証で発見)。
func (c *ClientConfig) GetVm(ctx context.Context, name string) (api.Vm, error) {
	cs, err := c.WsmanClient.FindComputerSystemByElementName(ctx, name)
	if err != nil {
		// go-wsman の不在エラーは api 境界で api.ErrVMNotFound へ変換し、provider が
		// go-wsman に依存せず不在を判定できるようにする (VmExists と同じ方針)。
		if errors.Is(err, hyperv.ErrVMNotFound) {
			return api.Vm{}, fmt.Errorf("hyperv-wsman: GetVm %q: %w", name, api.ErrVMNotFound)
		}
		return api.Vm{}, fmt.Errorf("hyperv-wsman: GetVm %q: %w", name, err)
	}
	sd, err := c.WsmanClient.GetSystemSettingData(ctx, cs.Name)
	if err != nil {
		return api.Vm{}, fmt.Errorf("hyperv-wsman: GetVm %q: %w", name, err)
	}
	vm, err := vmFromSettingData(name, sd)
	if err != nil {
		return api.Vm{}, fmt.Errorf("hyperv-wsman: GetVm %q: %w", name, err)
	}

	timeout, err := parseIntervalMinutes(sd.AutomaticCriticalErrorActionTimeout)
	if err != nil {
		return api.Vm{}, fmt.Errorf("hyperv-wsman: GetVm %q: %w", name, err)
	}
	vm.AutomaticCriticalErrorActionTimeout = timeout

	mem, err := c.WsmanClient.GetMemorySettings(ctx, cs.Name)
	if err != nil {
		return api.Vm{}, fmt.Errorf("hyperv-wsman: GetVm %q: memory: %w", name, err)
	}
	applyMemoryToVm(&vm, mem)

	proc, err := c.WsmanClient.GetProcessorSettings(ctx, cs.Name)
	if err != nil {
		return api.Vm{}, fmt.Errorf("hyperv-wsman: GetVm %q: processor: %w", name, err)
	}
	vm.ProcessorCount = clampInt64(proc.VirtualQuantity)

	return vm, nil
}

// applyMemoryToVm は Msvm_MemorySettingData から api.Vm のメモリ関連フィールドを埋める。
// PS 版 (Get-VMMemory) と同じマッピング: DynamicMemory=DynamicMemoryEnabled /
// StaticMemory=!DynamicMemoryEnabled / MemoryStartupBytes=VirtualQuantity /
// MemoryMinimumBytes=Reservation / MemoryMaximumBytes=Limit (いずれも CIM は MB、api は byte)。
// static memory でも Reservation/Limit は CIM 上 VirtualQuantity と同値で返る (Hyper-V の実装)。
func applyMemoryToVm(vm *api.Vm, mem *hyperv.Msvm_MemorySettingData) {
	vm.DynamicMemory = mem.DynamicMemoryEnabled
	vm.StaticMemory = !mem.DynamicMemoryEnabled
	vm.MemoryStartupBytes = mbToBytes(mem.VirtualQuantity)
	vm.MemoryMinimumBytes = mbToBytes(mem.Reservation)
	vm.MemoryMaximumBytes = mbToBytes(mem.Limit)
}

// mbToBytes は CIM の MB 単位を byte に変換する。int64 化前に上限をクランプし、
// 縮小変換の上限チェックなし (CodeQL high) を回避する (clampInt64/clampUint32 と同じ理由)。
func mbToBytes(mb uint64) int64 {
	const maxMB = math.MaxInt64 / (1024 * 1024)
	if mb > maxMB {
		mb = maxMB
	}
	return int64(mb) * 1024 * 1024
}

// mbToBytesU64 は CIM の MB 単位を byte (uint64) に変換する。mbToBytes の uint64 版
// (HighMemoryMappedIoSpace 等 uint64 フィールド用)。1024*1024 倍後の桁あふれを避けるため
// 乗算前に MB 値の上限をクランプする (clampUint32 で uint32 へ絞るのはこの関数の戻り値=
// バイト値に対して乗算後に適用する。MB の時点で uint32 に絞ると桁あふれと無関係に値を破壊するため
// 誤り、必ず bytes 変換後に clampUint32 を掛けること)。
func mbToBytesU64(mb uint64) uint64 {
	const maxMB = math.MaxUint64 / (1024 * 1024)
	if mb > maxMB {
		mb = maxMB
	}
	return mb * 1024 * 1024
}

// bytesToMbU64 は byte 単位を CIM の MB 単位に変換する (mbToBytesU64 の逆変換、write 側)。
// MB 境界に揃っていない値は切り捨てる (#105、CreateVm/UpdateVm の書き込みで必要)。
func bytesToMbU64(bytes uint64) uint64 {
	return bytes / (1024 * 1024)
}

// vmFromSettingData は Msvm_VirtualSystemSettingData を api.Vm にマッピングする (純関数)。
//
// enum (CriticalErrorAction/StartAction/StopAction) は provider 側の整数値が CIM 値と
// 一致するよう定義されているため直接変換する (None=0/Pause=1、Nothing=2/Start=4 等)。
// uint16→int は拡大変換のため安全。
func vmFromSettingData(name string, sd *hyperv.Msvm_VirtualSystemSettingData) (api.Vm, error) {
	checkpointType, err := checkpointTypeFromUserSnapshotType(sd.UserSnapshotType)
	if err != nil {
		return api.Vm{}, fmt.Errorf("checkpoint_type: %w", err)
	}
	return api.Vm{
		Name:                         name,
		Path:                         sd.ConfigurationDataRoot,
		Generation:                   vmGenerationFromSubType(sd.VirtualSystemSubType),
		AutomaticCriticalErrorAction: api.CriticalErrorAction(sd.AutomaticCriticalErrorAction),
		AutomaticStartAction:         api.StartAction(sd.AutomaticStartupAction),
		AutomaticStopAction:          api.StopAction(sd.AutomaticShutdownAction),
		Notes:                        strings.Join(sd.Notes, "\n"),
		LockOnDisconnect:             lockOnDisconnectState(sd.LockOnDisconnect),
		GuestControlledCacheTypes:    sd.GuestControlledCacheTypes,
		// HighMmioGapSize/LowMmioGapSize は CIM 上 MB 単位だが api.Vm (PS版 Get-VM 由来) は
		// byte 単位のため変換が必要 (実運用移行の実機検証で発見、変換漏れのまま plan すると
		// 512(MB のまま)→536870912(正しい byte)の恒常 diff になる)。
		HighMemoryMappedIoSpace: mbToBytesU64(sd.HighMmioGapSize),
		LowMemoryMappedIoSpace:  clampUint32(mbToBytesU64(sd.LowMmioGapSize)),
		SnapshotFileLocation:    sd.SnapshotDataRoot,
		SmartPagingFilePath:     sd.SwapFileDataRoot,
		// UserSnapshotType(CIM) は Microsoft.HyperV.PowerShell.CheckpointType と数値が
		// 一致する定義のため直接変換 (#106、MOF 一次資料確認済み)。write は #125 で実装済み。
		CheckpointType: checkpointType,
		// #134: 未マッピングだったため read が常に false を返していた。
		AutomaticCheckpointsEnabled: sd.AutomaticSnapshotsEnabled,

		// 以下は本メソッドでは未設定 (ゼロ値):
		//   - Memory (MemoryStartupBytes/Min/Max, DynamicMemory, StaticMemory) / ProcessorCount /
		//     AutomaticCriticalErrorActionTimeout: 呼び出し側の GetVm が
		//     Msvm_MemorySettingData / Msvm_ProcessorSettingData を別途取得して合成、
		//     AutomaticCriticalErrorActionTimeout は sd.AutomaticCriticalErrorActionTimeout
		//     (CIM datetime/interval 文字列) を parseIntervalMinutes で分に変換する。
		//   - AutomaticStartDelay: 同じく CIM Duration 文字列パースが必要 (#133 で追跡。
		//     go-wsman types.go には AutomaticStartupActionDelay が既に存在する)。
	}, nil
}

// checkpointTypeFromUserSnapshotType は CIM の UserSnapshotType を api.CheckpointType に
// 変換する。既知の値 (2-5) 以外は CIM 応答異常 (フィールド欠落によるゼロ値・未知の列挙値) として
// エラーを返す。ここを黙って通すと api.CheckpointType.String() が空文字列を返し、#106 の
// 恒常 drift (schema Default との不一致で毎 plan diff → apply 毎に VM 強制シャットダウン) が
// 無音で再発するため fail-loud にする (Fable 批判的レビュー指摘)。
// checkpointTypeFromUserSnapshotType / userSnapshotTypeFromCheckpointType は
// CIM の UserSnapshotType と api.CheckpointType を相互変換する。両者は #106 で MOF 突合済みの
// 数値一致 (2..5)。既知の値以外は黙って通さず fail-loud にする。
//
// userSnapshotTypeFromCheckpointType は書き込み方向。ゼロ値は「未指定」を意味し、
// 呼び出し側が送信をスキップする。
//
// api.CheckpointType は int (アーキ依存幅) なので uint16 へキャストせず、既知の値ごとに
// go-wsman の定数を返す。範囲検証を挟んでも静的解析はキャストの安全性を追えず、
// CodeQL の go/incorrect-integer-conversion に引っかかる。
func userSnapshotTypeFromCheckpointType(ct api.CheckpointType) (uint16, error) {
	switch ct {
	case api.CheckpointType_Disabled:
		return hyperv.UserSnapshotTypeDisable, nil
	case api.CheckpointType_Production:
		return hyperv.UserSnapshotTypeProductionFallbackToTest, nil
	case api.CheckpointType_ProductionOnly:
		return hyperv.UserSnapshotTypeProductionNoFallback, nil
	case api.CheckpointType_Standard:
		return hyperv.UserSnapshotTypeTest, nil
	default:
		return 0, fmt.Errorf("未知の checkpoint_type 値 %d (既知範囲は 2-5)", ct)
	}
}

func checkpointTypeFromUserSnapshotType(v uint16) (api.CheckpointType, error) {
	ct := api.CheckpointType(v)
	if _, ok := api.CheckpointType_name[ct]; !ok {
		return 0, fmt.Errorf("未知の UserSnapshotType 値 %d (既知範囲は 2-5)", v)
	}
	return ct, nil
}

// vmGenerationFromSubType は VirtualSystemSubType を Generation 番号に変換する。
func vmGenerationFromSubType(subType string) int {
	switch subType {
	case hyperv.VirtualSystemSubTypeGen1:
		return 1
	case hyperv.VirtualSystemSubTypeGen2:
		return 2
	default:
		return 0
	}
}

// lockOnDisconnectState は CIM の bool を api.OnOffState に変換する。
func lockOnDisconnectState(locked bool) api.OnOffState {
	if locked {
		return api.OnOffState_On
	}
	return api.OnOffState_Off
}

// clampUint32 は uint64 を uint32 に安全に縮小する (上限超過は MaxUint32 にクランプ)。
// 直接 uint32(v) すると CodeQL が上限チェックなしの縮小変換 (high) として検出するため。
func clampUint32(v uint64) uint32 {
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}

// DeleteVm は go-wsman 経由で VM (表示名) を削除する。
//
// 表示名→GUID を解決し、起動中なら強制電源断 (TurnOff) してから DestroySystem を呼ぶ。
// DestroySystem は Off 状態の VM でしか成功しないため、停止を先行させる必要がある。
// 非同期 Job はそれぞれ WaitForJob で完了を待つ。VM が存在しない場合は冪等に nil を返す
// (PowerShell 版の `Get-VM | Remove-VM` が空パイプでエラーにならない挙動と揃える)。
func (c *ClientConfig) DeleteVm(ctx context.Context, name string) error {
	cs, err := c.WsmanClient.FindComputerSystemByElementName(ctx, name)
	if err != nil {
		if errors.Is(err, hyperv.ErrVMNotFound) {
			return nil // 既に存在しない: 冪等に成功扱い
		}
		return fmt.Errorf("hyperv-wsman: DeleteVm %q: %w", name, err)
	}
	guid := cs.Name

	if needsTurnOff(cs.EnabledState) {
		jobRef, err := c.WsmanClient.TurnOffVM(ctx, guid)
		if err != nil {
			return fmt.Errorf("hyperv-wsman: DeleteVm %q: turn off: %w", name, err)
		}
		if err := c.WsmanClient.WaitForJob(ctx, jobRef); err != nil {
			return fmt.Errorf("hyperv-wsman: DeleteVm %q: wait turn off: %w", name, err)
		}
	}

	jobRef, err := c.WsmanClient.DestroySystem(ctx, guid)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: DeleteVm %q: destroy: %w", name, err)
	}
	if err := c.WsmanClient.WaitForJob(ctx, jobRef); err != nil {
		return fmt.Errorf("hyperv-wsman: DeleteVm %q: wait destroy: %w", name, err)
	}
	return nil
}

// needsTurnOff は EnabledState が「DestroySystem 前に停止が必要」な状態か判定する。
// DestroySystem は Off 状態でしか成功しないため、Off(3) 以外は一律停止が必要とみなす。
func needsTurnOff(state uint16) bool {
	return state != hyperv.EnabledStateDisabled
}

// CreateVm は go-wsman 経由で新規 VM を作成する。
//
// PowerShell 版 (New-VM + Set-VM 群) を CIM 化する。冪等性のため事前に存在確認し、
// DefineSystem で VM-level 設定込みの VM を作成 → Memory/Processor をリソース設定として
// 適用する。各非同期 Job は WaitForJob で完了を待つ。
//
// go-wsman の DefineSystem は「リソースを持たない VM」を作る (PowerShell New-VM のような
// 自動 NIC/DVD は付かない想定)。parity 担保のため自動生成 NIC は防御的に削除する
// (実際に生成されるかは Phase D 実機で確認)。
//
// 未適用:
//   - automaticStartDelay: CIM Duration 文字列のエンコードが要るが go-wsman の CIM 構造体に
//     未マッピング (#102 のスコープ外、homelab config も未使用で実害なし)。
//   - checkpointType / automaticCheckpointsEnabled: v2.1 (#46) に延期。
func (c *ClientConfig) CreateVm(
	ctx context.Context,
	name string,
	path string,
	generation int,
	automaticCriticalErrorAction api.CriticalErrorAction,
	automaticCriticalErrorActionTimeout int32,
	automaticStartAction api.StartAction,
	automaticStartDelay int32,
	automaticStopAction api.StopAction,
	checkpointType api.CheckpointType,
	dynamicMemory bool,
	guestControlledCacheTypes bool,
	highMemoryMappedIoSpace uint64,
	lockOnDisconnect api.OnOffState,
	lowMemoryMappedIoSpace uint32,
	memoryMaximumBytes int64,
	memoryMinimumBytes int64,
	memoryStartupBytes int64,
	notes string,
	processorCount int64,
	smartPagingFilePath string,
	snapshotFileLocation string,
	staticMemory bool,
	automaticCheckpointsEnabled bool,
) error {
	// 1. 冪等性: 既存なら作成しない (PowerShell 版の throw "VM already exists" 相当)。
	if _, err := c.WsmanClient.FindComputerSystemByElementName(ctx, name); err == nil {
		return fmt.Errorf("hyperv-wsman: CreateVm %q: VM already exists", name)
	} else if !errors.Is(err, hyperv.ErrVMNotFound) {
		return fmt.Errorf("hyperv-wsman: CreateVm %q: existence check: %w", name, err)
	}

	// 2. VM 本体を作成 (VM-level 設定込み)。
	sd, err := vmSettingDataForCreate(name, path, generation,
		automaticCriticalErrorAction, automaticStartAction, automaticStopAction,
		guestControlledCacheTypes, highMemoryMappedIoSpace, lockOnDisconnect,
		lowMemoryMappedIoSpace, notes, smartPagingFilePath, snapshotFileLocation,
		checkpointType, automaticCheckpointsEnabled)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVm %q: %w", name, err)
	}
	res, err := c.WsmanClient.DefineSystem(ctx, sd)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVm %q: define: %w", name, err)
	}
	if err := c.WsmanClient.WaitForJob(ctx, res.JobRef); err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVm %q: wait define: %w", name, err)
	}
	guid := res.ResultingSystem // Msvm_ComputerSystem.Name (以降のリソース操作は GUID で引く)

	// 3. 自動生成 NIC があれば削除 (残すと後続 NIC Phase で state ドリフトする)。
	adapters, err := c.WsmanClient.ListNetworkAdapters(ctx, guid)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVm %q: list adapters: %w", name, err)
	}
	for _, a := range adapters {
		jobRef, err := c.WsmanClient.RemoveNetworkAdapter(ctx, a.InstanceID)
		if err != nil {
			return fmt.Errorf("hyperv-wsman: CreateVm %q: remove adapter: %w", name, err)
		}
		if err := c.WsmanClient.WaitForJob(ctx, jobRef); err != nil {
			return fmt.Errorf("hyperv-wsman: CreateVm %q: wait remove adapter: %w", name, err)
		}
	}

	// 4. Memory: 既定設定を取得して上書き適用 (CIM は MB 単位)。
	mem, err := c.WsmanClient.GetMemorySettings(ctx, guid)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVm %q: get memory: %w", name, err)
	}
	applyMemorySettings(mem, staticMemory, dynamicMemory, memoryStartupBytes, memoryMinimumBytes, memoryMaximumBytes)
	if jobRef, err := c.WsmanClient.SetMemorySettings(ctx, mem); err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVm %q: set memory: %w", name, err)
	} else if err := c.WsmanClient.WaitForJob(ctx, jobRef); err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVm %q: wait memory: %w", name, err)
	}

	// 5. Processor: vCPU 数を設定。
	proc, err := c.WsmanClient.GetProcessorSettings(ctx, guid)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVm %q: get processor: %w", name, err)
	}
	if processorCount > 0 {
		proc.VirtualQuantity = uint64(processorCount)
	}
	if jobRef, err := c.WsmanClient.SetProcessorSettings(ctx, proc); err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVm %q: set processor: %w", name, err)
	} else if err := c.WsmanClient.WaitForJob(ctx, jobRef); err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVm %q: wait processor: %w", name, err)
	}

	// 6. ゼロ値ダウングレードの補正 (#143 / #141)。
	//
	// DefineSystem / SetMemorySettings は marshalEmbeddedInstance がゼロ値を送らないため、
	// false / 0 を要求してもホスト既定が残る。例:
	//
	//	static_memory = true                  → DynamicMemoryEnabled はホスト既定 (true) のまま
	//	automatic_checkpoints_enabled = false → ホスト既定 (クライアント Hyper-V は true) のまま
	//	automatic_critical_error_action = None → CIM 既定 (Pause) のまま
	//
	// UpdateVm は同じ問題を vmLevelZeroDowngrade で検知して PS へ委譲しているが、
	// create 側にはガードが無く **黙って違う構成の VM ができていた**。
	//
	// 作成後に読み直し、要求とのズレが残っていれば PS で補正する。ここで直さないと
	// 次の apply まで嘘の構成が残り、しかもその apply は VM 停止を伴う。
	// 根本解は go-wsman #135 (明示的にゼロ値を送る手段)。
	cur, err := c.WsmanClient.GetSystemSettingData(ctx, guid)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVm %q: 補正のための読み直し: %w", name, err)
	}
	curMem, err := c.WsmanClient.GetMemorySettings(ctx, guid)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVm %q: 補正のためのメモリ読み直し: %w", name, err)
	}
	if vmLevelZeroDowngrade(cur, curMem, vmLevelWant{
		criticalErrorAction:         automaticCriticalErrorAction,
		startAction:                 automaticStartAction,
		stopAction:                  automaticStopAction,
		guestControlledCacheTypes:   guestControlledCacheTypes,
		highMmioGapSize:             highMemoryMappedIoSpace,
		lockOnDisconnect:            lockOnDisconnect,
		lowMmioGapSize:              lowMemoryMappedIoSpace,
		notes:                       notes,
		smartPagingFilePath:         smartPagingFilePath,
		snapshotFileLocation:        snapshotFileLocation,
		staticMemory:                staticMemory,
		checkpointType:              checkpointType,
		automaticCheckpointsEnabled: automaticCheckpointsEnabled,
	}) {
		log.Printf("[DEBUG][hyperv-wsman] CreateVm %q: ゼロ値が反映されていないため PS で補正します", name)
		if err := c.ClientConfig.UpdateVm(ctx, name,
			automaticCriticalErrorAction, automaticCriticalErrorActionTimeout,
			automaticStartAction, automaticStartDelay, automaticStopAction,
			checkpointType, dynamicMemory, guestControlledCacheTypes,
			highMemoryMappedIoSpace, lockOnDisconnect, lowMemoryMappedIoSpace,
			memoryMaximumBytes, memoryMinimumBytes, memoryStartupBytes,
			notes, processorCount, smartPagingFilePath, snapshotFileLocation,
			staticMemory, automaticCheckpointsEnabled); err != nil {
			return fmt.Errorf("hyperv-wsman: CreateVm %q: ゼロ値の補正: %w", name, err)
		}
	}

	return nil
}

// UpdateVm は go-wsman 経由で既存 VM の構成を更新する。
//
// 現構成を取得 → VM レベルの可変フィールドを書き換え → ModifySystemSettings (go-wsman の
// UpdateVm) で反映 → Memory/Processor をリソース設定として更新する。各非同期 Job は
// WaitForJob で待つ。path/generation は作成後に変更できないため対象外 (api 契約でも除外)。
//
// CIM の ModifySystemSettings はゼロ値フィールドを「変更なし」とみなすため、各フィールドは
// 新しい値で上書きする (CreateVm と enum/メモリ変換ロジックを共有)。
//
// 未適用: automaticStartDelay (CreateVm と同じ理由、#102 スコープ外)。
//
// checkpointType は CIM の UserSnapshotType として書き込む (#125)。有効値が 2..5 で
// ゼロ値が無いため marshalEmbeddedInstance のゼロ値スキップに掛からない (実機確認)。
// automaticCheckpointsEnabled は true なら書けるが false はゼロ値のため送れないので、
// true→false は下記のゼロ値ダウングレード判定が拾って PS へ委譲する
// (根本解は go-wsman #135 の「明示的にゼロ値を送る手段」)。
//
// 非ゼロ→0 / true→false のダウングレードは marshalEmbeddedInstance がゼロ値を送らないため
// CIM で表現できない。vmLevelZeroDowngrade で検知し、書き込み前に PS 経路へ丸ごと委譲する
// (#132)。vm_processor / vm_firmware と同じ扱い。
func (c *ClientConfig) UpdateVm(
	ctx context.Context,
	name string,
	automaticCriticalErrorAction api.CriticalErrorAction,
	automaticCriticalErrorActionTimeout int32,
	automaticStartAction api.StartAction,
	automaticStartDelay int32,
	automaticStopAction api.StopAction,
	checkpointType api.CheckpointType,
	dynamicMemory bool,
	guestControlledCacheTypes bool,
	highMemoryMappedIoSpace uint64,
	lockOnDisconnect api.OnOffState,
	lowMemoryMappedIoSpace uint32,
	memoryMaximumBytes int64,
	memoryMinimumBytes int64,
	memoryStartupBytes int64,
	notes string,
	processorCount int64,
	smartPagingFilePath string,
	snapshotFileLocation string,
	staticMemory bool,
	automaticCheckpointsEnabled bool,
) error {
	cs, err := c.WsmanClient.FindComputerSystemByElementName(ctx, name)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVm %q: %w", name, err)
	}
	guid := cs.Name

	// 1. VM-level 設定: InstanceID を取得し、変更フィールドのみの最小 instance を
	//    ModifySystemSettings に渡す。GetSystemSettingData の全体を送り返すと read-only
	//    プロパティ (VirtualSystemType=Realized / CreationTime / BIOSGUID 等) でジョブが
	//    Exception になるため (実機 acc test で確認、libvirt 実証の最小 instance パターン)。
	cur, err := c.WsmanClient.GetSystemSettingData(ctx, guid)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVm %q: get settings: %w", name, err)
	}

	// メモリ設定は後段でも使うが、ゼロ値ダウングレード判定に現行の DynamicMemoryEnabled が
	// 要るため先に取得する。
	mem, err := c.WsmanClient.GetMemorySettings(ctx, guid)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVm %q: get memory: %w", name, err)
	}

	// ゼロ値ダウングレードは CIM で表現できないため、書き込みを 1 件も行わずに PS へ委譲する
	// (#132)。ここで委譲しないと「成功報告なのに実機は変わらない」恒常 diff になり、
	// apply のたびに VM が停止する。
	want := vmLevelWant{
		criticalErrorAction:         automaticCriticalErrorAction,
		startAction:                 automaticStartAction,
		stopAction:                  automaticStopAction,
		guestControlledCacheTypes:   guestControlledCacheTypes,
		highMmioGapSize:             highMemoryMappedIoSpace,
		lockOnDisconnect:            lockOnDisconnect,
		lowMmioGapSize:              lowMemoryMappedIoSpace,
		notes:                       notes,
		smartPagingFilePath:         smartPagingFilePath,
		snapshotFileLocation:        snapshotFileLocation,
		staticMemory:                staticMemory,
		checkpointType:              checkpointType,
		automaticCheckpointsEnabled: automaticCheckpointsEnabled,
	}
	if vmLevelZeroDowngrade(cur, mem, want) {
		log.Printf("[DEBUG][hyperv-wsman] UpdateVm %q: ゼロ値ダウングレードを検出、PS へ委譲します", name)
		return c.ClientConfig.UpdateVm(ctx, name,
			automaticCriticalErrorAction, automaticCriticalErrorActionTimeout,
			automaticStartAction, automaticStartDelay, automaticStopAction,
			checkpointType, dynamicMemory, guestControlledCacheTypes,
			highMemoryMappedIoSpace, lockOnDisconnect, lowMemoryMappedIoSpace,
			memoryMaximumBytes, memoryMinimumBytes, memoryStartupBytes,
			notes, processorCount, smartPagingFilePath, snapshotFileLocation,
			staticMemory, automaticCheckpointsEnabled)
	}

	sd := &hyperv.Msvm_VirtualSystemSettingData{InstanceID: cur.InstanceID}
	if err := applyVmLevelSettings(sd, want); err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVm %q: %w", name, err)
	}
	if jobRef, err := c.WsmanClient.UpdateVm(ctx, sd); err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVm %q: modify settings: %w", name, err)
	} else if err := c.WsmanClient.WaitForJob(ctx, jobRef); err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVm %q: wait modify: %w", name, err)
	}

	// 2. Memory (mem は判定用に取得済み)。
	applyMemorySettings(mem, staticMemory, dynamicMemory, memoryStartupBytes, memoryMinimumBytes, memoryMaximumBytes)
	if jobRef, err := c.WsmanClient.SetMemorySettings(ctx, mem); err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVm %q: set memory: %w", name, err)
	} else if err := c.WsmanClient.WaitForJob(ctx, jobRef); err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVm %q: wait memory: %w", name, err)
	}

	// 3. Processor。
	proc, err := c.WsmanClient.GetProcessorSettings(ctx, guid)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVm %q: get processor: %w", name, err)
	}
	if processorCount > 0 {
		proc.VirtualQuantity = uint64(processorCount)
	}
	if jobRef, err := c.WsmanClient.SetProcessorSettings(ctx, proc); err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVm %q: set processor: %w", name, err)
	} else if err := c.WsmanClient.WaitForJob(ctx, jobRef); err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVm %q: wait processor: %w", name, err)
	}

	return nil
}

// vmSubTypeFromGeneration は Generation 番号を CIM VirtualSystemSubType に変換する。
func vmSubTypeFromGeneration(generation int) (string, error) {
	switch generation {
	case 1:
		return hyperv.VirtualSystemSubTypeGen1, nil
	case 2:
		return hyperv.VirtualSystemSubTypeGen2, nil
	default:
		return "", fmt.Errorf("unsupported generation %d (1 か 2)", generation)
	}
}

// enumToUint16 は provider enum (int) を CIM uint16 に安全変換する。
// 範囲外は 0 (既定 = None/Nothing) に倒す。enum 値は 0-4 想定だが、直接 uint16() すると
// gosec G115 が桁あふれ変換として検出するため (clampUint32 と同趣旨)。
func enumToUint16(v int) uint16 {
	if v < 0 || v > math.MaxUint16 {
		return 0
	}
	return uint16(v)
}

// bytesToMB はバイトを MB に変換する (CIM の Memory/MMIO は MB 単位)。負値・端数は切り捨て。
func bytesToMB(b int64) uint64 {
	if b <= 0 {
		return 0
	}
	return uint64(b) / (1024 * 1024)
}

// applyMemorySettings は static/dynamic に応じて Msvm_MemorySettingData を上書きする。
//
// VirtualQuantity は起動時メモリ(MB)。static は固定メモリ (Min/Max 無視)、dynamic は
// Reservation=最小 / Limit=最大 を設定する。PowerShell 版の StaticMemory/DynamicMemory
// 排他ロジックに対応。
func applyMemorySettings(m *hyperv.Msvm_MemorySettingData, staticMemory, dynamicMemory bool, startupBytes, minBytes, maxBytes int64) {
	m.VirtualQuantity = bytesToMB(startupBytes)
	if staticMemory {
		m.DynamicMemoryEnabled = false
		return
	}
	m.DynamicMemoryEnabled = dynamicMemory
	if dynamicMemory {
		m.Reservation = bytesToMB(minBytes)
		m.Limit = bytesToMB(maxBytes)
	}
}

// vmSettingDataForCreate は CreateVm パラメータから DefineSystem 用の
// Msvm_VirtualSystemSettingData を構築する (GetVm の vmFromSettingData の逆方向)。
//
// enum は provider 整数値が CIM 値と一致するため直接変換する (GetVm と同じ前提、MOF 突合済み)。
// ゼロ値フィールドは marshalEmbeddedInstance が省略し CIM 既定になる。
func vmSettingDataForCreate(
	name, path string, generation int,
	criticalErrorAction api.CriticalErrorAction,
	startAction api.StartAction,
	stopAction api.StopAction,
	guestControlledCacheTypes bool,
	highMmioGapSize uint64,
	lockOnDisconnect api.OnOffState,
	lowMmioGapSize uint32,
	notes, smartPagingFilePath, snapshotFileLocation string,
	checkpointType api.CheckpointType,
	automaticCheckpointsEnabled bool,
) (*hyperv.Msvm_VirtualSystemSettingData, error) {
	subType, err := vmSubTypeFromGeneration(generation)
	if err != nil {
		return nil, err
	}
	sd := &hyperv.Msvm_VirtualSystemSettingData{
		ElementName:           name,
		VirtualSystemSubType:  subType,
		ConfigurationDataRoot: path,
	}
	if err := applyVmLevelSettings(sd, vmLevelWant{
		criticalErrorAction:         criticalErrorAction,
		startAction:                 startAction,
		stopAction:                  stopAction,
		guestControlledCacheTypes:   guestControlledCacheTypes,
		highMmioGapSize:             highMmioGapSize,
		lockOnDisconnect:            lockOnDisconnect,
		lowMmioGapSize:              lowMmioGapSize,
		notes:                       notes,
		smartPagingFilePath:         smartPagingFilePath,
		snapshotFileLocation:        snapshotFileLocation,
		checkpointType:              checkpointType,
		automaticCheckpointsEnabled: automaticCheckpointsEnabled,
	}); err != nil {
		return nil, err
	}
	return sd, nil
}

// vmLevelWant は Create/Update が要求する VM レベル設定 + メモリ方式をまとめたもの。
// applyVmLevelSettings の入力と vmLevelZeroDowngrade の判定材料を兼ねる。
// 同型の引数 (bool 複数・string 複数) が位置指定で並ぶと取り違えてもコンパイルが通るため、
// 構造体で受け渡す。
type vmLevelWant struct {
	criticalErrorAction       api.CriticalErrorAction
	startAction               api.StartAction
	stopAction                api.StopAction
	guestControlledCacheTypes bool
	highMmioGapSize           uint64
	lockOnDisconnect          api.OnOffState
	lowMmioGapSize            uint32
	notes                     string
	// smartPagingFilePath / snapshotFileLocation は現状 vmLevelZeroDowngrade の判定に
	// 使わない (空 = 指定なし)。UpdateVm の引数と 1:1 に保つため構造体には残す。
	smartPagingFilePath  string
	snapshotFileLocation string
	staticMemory         bool
	// checkpoint 系。UserSnapshotType は有効値が 2..5 でゼロ値が無いため
	// marshalEmbeddedInstance のゼロ値スキップに掛からず、そのまま送信できる (実機確認)。
	// automaticCheckpointsEnabled は false がゼロ値のため送れず、true→false は PS 委譲になる
	// (go-wsman #135 が根本解)。
	checkpointType              api.CheckpointType
	automaticCheckpointsEnabled bool
}

// vmLevelZeroDowngrade は要求が現行に対して「非ゼロ→0」または「true→false」の遷移を含むかを返す。
//
// go-wsman の marshalEmbeddedInstance はゼロ値フィールドを送らないため、これらの変更は
// ModifySystemSettings に乗らない。そのまま処理すると「成功報告なのに実機は変わらない」
// = 恒常 diff になり、hasChangesThatRequireVmToBeOff 経由で apply のたびに VM が停止する (#132)。
// vm_processor の hasZeroDowngrade / vm_firmware_write の firmwareZeroDowngrade と同じ役割で、
// VM レベルにだけ欠けていた。
//
// notes / smartPagingFilePath / snapshotFileLocation は applyVmLevelSettings が
// 空文字を明示的にスキップするため、値の消去も同じく反映されない。
func vmLevelZeroDowngrade(cur *hyperv.Msvm_VirtualSystemSettingData, curMem *hyperv.Msvm_MemorySettingData, want vmLevelWant) bool {
	if cur == nil {
		// 現行が読めない場合は判定できないので安全側 (PS 委譲) に倒す。
		return true
	}
	zeroEnum := func(w int, c uint16) bool { return enumToUint16(w) == 0 && c != 0 }
	if zeroEnum(int(want.criticalErrorAction), cur.AutomaticCriticalErrorAction) ||
		zeroEnum(int(want.startAction), cur.AutomaticStartupAction) ||
		zeroEnum(int(want.stopAction), cur.AutomaticShutdownAction) {
		return true
	}
	if !want.guestControlledCacheTypes && cur.GuestControlledCacheTypes {
		return true
	}
	if want.lockOnDisconnect != api.OnOffState_On && cur.LockOnDisconnect {
		return true
	}
	if bytesToMbU64(want.highMmioGapSize) == 0 && cur.HighMmioGapSize != 0 {
		return true
	}
	if bytesToMbU64(uint64(want.lowMmioGapSize)) == 0 && cur.LowMmioGapSize != 0 {
		return true
	}
	if want.notes == "" && strings.Join(cur.Notes, "\n") != "" {
		return true
	}
	// smartPagingFilePath / snapshotFileLocation は判定に含めない。
	// これらの空文字は「消す」ではなく「指定なし」を意味する (#99 で BootSourceOrder について
	// 実機で確定させたのと同じ意味論で、applyVmLevelSettings も空をスキップしている)。
	// schema 既定も非空 (C:\ProgramData\Microsoft\Windows\Hyper-V) のため resource 層から
	// 空が来ることは無い。加えて委譲先の Set-VM 自体が空文字を受け付けず
	// ParameterArgumentValidationError になる (実機確認) ので、委譲しても救えない。
	// automatic_checkpoints_enabled の true→false も false がゼロ値のため送れない。
	if !want.automaticCheckpointsEnabled && cur.AutomaticSnapshotsEnabled {
		return true
	}
	// applyMemorySettings は staticMemory 時に DynamicMemoryEnabled=false を代入するが、
	// これもゼロ値のため送られない。動的→静的の切り替えは PS でしか表現できない。
	if want.staticMemory && curMem != nil && curMem.DynamicMemoryEnabled {
		return true
	}
	return false
}

// applyVmLevelSettings は VM レベルの可変フィールドを settings に適用する (Create/Update 共有)。
//
// enum は provider 整数値 = CIM 値 (GetVm/CreateVm と同前提)。空文字のパスと未指定のゼロ値は
// marshalEmbeddedInstance が省略するため、Update (ModifySystemSettings) では「未指定=変更なし」
// になる (CIM SettingData の慣習)。InstanceID 等の既存値は呼び出し側が保持する。
//
// AutomaticCriticalErrorActionTimeout の書き込みは未実装 (#102、intervalISO8601Pattern の
// コメント参照)。
func applyVmLevelSettings(sd *hyperv.Msvm_VirtualSystemSettingData, want vmLevelWant) error {
	sd.AutomaticStartupAction = enumToUint16(int(want.startAction))
	sd.AutomaticShutdownAction = enumToUint16(int(want.stopAction))
	sd.AutomaticCriticalErrorAction = enumToUint16(int(want.criticalErrorAction))
	sd.LockOnDisconnect = want.lockOnDisconnect == api.OnOffState_On
	sd.GuestControlledCacheTypes = want.guestControlledCacheTypes
	// HighMmioGapSize/LowMmioGapSize は CIM 上 MB 単位だが引数は api.Vm 由来の byte 単位
	// (read 側 mbToBytesU64 の逆変換)。変換漏れのまま送信すると Hyper-V が
	// ErrorCode=32773「無効な値」で CreateVm/UpdateVm を拒否する (#105)。
	sd.HighMmioGapSize = bytesToMbU64(want.highMmioGapSize)
	sd.LowMmioGapSize = bytesToMbU64(uint64(want.lowMmioGapSize))
	if want.snapshotFileLocation != "" {
		sd.SnapshotDataRoot = want.snapshotFileLocation
	}
	if want.smartPagingFilePath != "" {
		sd.SwapFileDataRoot = want.smartPagingFilePath
	}
	if want.notes != "" {
		sd.Notes = strings.Split(want.notes, "\n")
	}
	// checkpoint_type (#125)。ゼロ値は「未指定」なので送らない。
	if want.checkpointType != 0 {
		ust, err := userSnapshotTypeFromCheckpointType(want.checkpointType)
		if err != nil {
			return err
		}
		sd.UserSnapshotType = ust
	}
	// automatic_checkpoints_enabled。true は送れるが false はゼロ値のため落ちる。
	// true→false は vmLevelZeroDowngrade が拾って PS へ委譲する。
	sd.AutomaticSnapshotsEnabled = want.automaticCheckpointsEnabled
	return nil
}
