package hyperv_wsman

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

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
// マッピングする。VM レベルの設定 (世代・自動アクション・Notes 等) のみを対象とし、
// Memory/CPU は別途 Msvm_MemorySettingData / Msvm_ProcessorSettingData が必要なため
// 本メソッドでは設定しない (vmFromSettingData の注記参照)。
func (c *ClientConfig) GetVm(ctx context.Context, name string) (api.Vm, error) {
	cs, err := c.WsmanClient.FindComputerSystemByElementName(ctx, name)
	if err != nil {
		return api.Vm{}, fmt.Errorf("hyperv-wsman: GetVm %q: %w", name, err)
	}
	sd, err := c.WsmanClient.GetSystemSettingData(ctx, cs.Name)
	if err != nil {
		return api.Vm{}, fmt.Errorf("hyperv-wsman: GetVm %q: %w", name, err)
	}
	return vmFromSettingData(name, sd), nil
}

// vmFromSettingData は Msvm_VirtualSystemSettingData を api.Vm にマッピングする (純関数)。
//
// enum (CriticalErrorAction/StartAction/StopAction) は provider 側の整数値が CIM 値と
// 一致するよう定義されているため直接変換する (None=0/Pause=1、Nothing=2/Start=4 等)。
// uint16→int は拡大変換のため安全。
func vmFromSettingData(name string, sd *hyperv.Msvm_VirtualSystemSettingData) api.Vm {
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
		HighMemoryMappedIoSpace:      sd.HighMmioGapSize,
		LowMemoryMappedIoSpace:       clampUint32(sd.LowMmioGapSize),
		SnapshotFileLocation:         sd.SnapshotDataRoot,
		SmartPagingFilePath:          sd.SwapFileDataRoot,

		// 以下は本メソッドでは未設定 (ゼロ値):
		//   - Memory/CPU (MemoryStartupBytes/Min/Max, DynamicMemory, StaticMemory,
		//     ProcessorCount): Msvm_MemorySettingData / Msvm_ProcessorSettingData が
		//     別途必要 (CreateVm/C-2 で対応)。
		//   - AutomaticStartDelay / AutomaticCriticalErrorActionTimeout: CIM の
		//     Duration/interval 文字列パースが必要。実機が返す実書式に合わせるため
		//     acc test (Phase D) で実データ確認後に実装する。
		//   - CheckpointType / AutomaticCheckpointsEnabled: v2.1 (#46) に延期。
	}
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
