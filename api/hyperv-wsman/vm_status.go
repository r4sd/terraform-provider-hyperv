package hyperv_wsman

import (
	"context"
	"fmt"
	"time"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// vm_status の状態遷移待ちの既定 (呼び出し側が 0 を渡した時に使う)。
const (
	defaultVmStatusPollPeriod = 2 * time.Second
	defaultVmStatusTimeout    = 5 * time.Minute
)

// enabledStateToVmState は Msvm_ComputerSystem.EnabledState を provider の VmState に変換する。
//
// 実機ダンプ (2026-07-09) で確認したとおり、Hyper-V の Msvm_ComputerSystem.EnabledState は
// **PowerShell の VMState 列挙値をそのまま返す** (Running=2 / Off=3 / Saved=6 / Paused=9)。
// go-wsman の EnabledStatePaused(32768)/EnabledStateSaved(32769) は CIM 標準値で、実機の
// Msvm_ComputerSystem とは食い違う (go-wsman 側の定数バグ = go-wsman#102。修正後は定数に戻す)。このため本関数は
// go-wsman の Paused/Saved 定数を使わず、実測した VMState 値で直接マップする。
// 遷移値 (Stopping=4/Starting=10/Saving=32773 等) や未知は Other。UpdateVmStatus は事前に安定状態
// まで待つので、状態変更の判断が遷移値に依存することはない (GetVmStatus の読み取りが遷移中に当たった時のみ Other)。
func enabledStateToVmState(s uint16) api.VmState {
	switch api.VmState(s) {
	case api.VmState_Running, api.VmState_Off, api.VmState_Paused, api.VmState_Saved:
		return api.VmState(s)
	default:
		return api.VmState_Other
	}
}

// isStableEnabledState は EnabledState が遷移中でない安定状態 (Running/Off/Paused/Saved) かを返す。
// 遷移中 (Starting/Stopping/Saving/Pausing/Resuming 等) の VM に RequestStateChange を撃つと
// Hyper-V が Invalid state で拒否するため、状態変更の前に安定するまで待つ判定に使う。
// 値は実機ダンプ準拠 (enabledStateToVmState と同じく Msvm_ComputerSystem は VMState 値を返す)。
func isStableEnabledState(s uint16) bool {
	switch api.VmState(s) {
	case api.VmState_Running, api.VmState_Off, api.VmState_Paused, api.VmState_Saved:
		return true
	default:
		return false
	}
}

// GetVmStatus は VM の現在の電源状態を返す (go-wsman EnabledState 逆引き)。
func (c *ClientConfig) GetVmStatus(ctx context.Context, vmName string) (api.VmStatus, error) {
	cs, err := c.WsmanClient.FindComputerSystemByElementName(ctx, vmName)
	if err != nil {
		return api.VmStatus{}, fmt.Errorf("hyperv-wsman: GetVmStatus %q: %w", vmName, err)
	}
	return api.VmStatus{State: enabledStateToVmState(cs.EnabledState)}, nil
}

// UpdateVmStatus は VM を目標状態に遷移させる。
//
// terraform で設定可能な目標状態は running/off のみ (api.VmState_SettableValue)。
// PowerShell 版 (Set-VMState) と揃え、状態変更の前に安定状態まで待ってから発行する:
//   - running          → StartVM (Off/Saved/Paused からの起動・再開)
//   - off (Running から) + turnOff  → TurnOffVM / !turnOff → ShutdownVM (ゲスト OS シャットダウン)
//   - off (Paused/Saved から)       → TurnOffVM 固定 (凍結/保存ゲストは graceful 不可、winrm の
//     `Stop-VM -Force` 相当。winrm は Saved→Off を throw するが、こちらは強制停止で対応する)
//
// 既に目標状態なら通信せず no-op (冪等)。timeout/pollPeriod(秒) は安定待ち・遷移完了待ちに使う。
func (c *ClientConfig) UpdateVmStatus(
	ctx context.Context,
	vmName string,
	timeout uint32,
	pollPeriod uint32,
	state api.VmState,
	turnOff bool,
) error {
	// 状態変更の前に安定状態まで待つ (遷移中への RequestStateChange 拒否・呼び出し側ループの
	// 再発行スパムを防ぐ。winrm の Wait-IsInFinalTransitionState 相当)。
	cs, err := c.waitForStableVmState(ctx, vmName, timeout, pollPeriod)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVmStatus %q: %w", vmName, err)
	}
	current := enabledStateToVmState(cs.EnabledState)
	if current == state {
		return nil // 既に目標状態 (冪等)
	}
	guid := cs.Name

	var jobRef string
	switch state {
	case api.VmState_Running:
		jobRef, err = c.WsmanClient.StartVM(ctx, guid)
	case api.VmState_Off:
		// graceful シャットダウンは Running からのみ意味を持つ。Paused/Saved のゲストは
		// 応答できないため強制停止にフォールバックする (winrm の Stop-VM -Force と同じ効果)。
		if turnOff || current == api.VmState_Paused || current == api.VmState_Saved {
			jobRef, err = c.WsmanClient.TurnOffVM(ctx, guid)
		} else {
			jobRef, err = c.WsmanClient.ShutdownVM(ctx, guid)
		}
	default:
		return fmt.Errorf("hyperv-wsman: UpdateVmStatus %q: 目標状態 %q は go-wsman 経路では未対応 (running/off のみ設定可)",
			vmName, api.VmState_name[state])
	}
	if err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVmStatus %q: 状態変更要求 (現在=%s 目標=%s): %w",
			vmName, api.VmState_name[current], api.VmState_name[state], err)
	}
	if err := c.WsmanClient.WaitForJob(ctx, jobRef, vmStatusWaitOpts(timeout, pollPeriod)...); err != nil {
		// graceful シャットダウンは Integration Services が要る。未応答なら手掛かりを添える。
		if state == api.VmState_Off && !turnOff && current == api.VmState_Running {
			return fmt.Errorf("hyperv-wsman: UpdateVmStatus %q: ゲスト OS シャットダウン待ち (Integration Services 未応答なら turn_off_on_destroy=true を検討): %w", vmName, err)
		}
		return fmt.Errorf("hyperv-wsman: UpdateVmStatus %q: 遷移待ち: %w", vmName, err)
	}
	return nil
}

// waitForStableVmState は VM が安定状態 (遷移中でない) になるまで待って ComputerSystem を返す。
// timeout/pollPeriod(秒) が 0 なら既定を使う。
func (c *ClientConfig) waitForStableVmState(ctx context.Context, vmName string, timeout, pollPeriod uint32) (*hyperv.Msvm_ComputerSystem, error) {
	poll := defaultVmStatusPollPeriod
	if pollPeriod > 0 {
		poll = time.Duration(pollPeriod) * time.Second
	}
	deadline := defaultVmStatusTimeout
	if timeout > 0 {
		deadline = time.Duration(timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	for {
		cs, err := c.WsmanClient.FindComputerSystemByElementName(ctx, vmName)
		if err != nil {
			return nil, err
		}
		if isStableEnabledState(cs.EnabledState) {
			return cs, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("安定状態待ちタイムアウト (EnabledState=%d): %w", cs.EnabledState, ctx.Err())
		case <-time.After(poll):
		}
	}
}

// vmStatusWaitOpts は timeout/pollPeriod(秒) を WaitForJob のオプションに変換する。0 は go-wsman 既定。
func vmStatusWaitOpts(timeout, pollPeriod uint32) []hyperv.WaitOption {
	var opts []hyperv.WaitOption
	if timeout > 0 {
		opts = append(opts, hyperv.WithJobTimeout(time.Duration(timeout)*time.Second))
	}
	if pollPeriod > 0 {
		opts = append(opts, hyperv.WithPollInterval(time.Duration(pollPeriod)*time.Second))
	}
	return opts
}
