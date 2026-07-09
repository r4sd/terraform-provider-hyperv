package hyperv_wsman

import (
	"context"
	"fmt"
	"time"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// enabledStateToVmState は CIM EnabledState (Msvm_ComputerSystem) を provider の VmState に変換する。
//
// provider の VmState は PowerShell の VMState 列挙値で、CIM EnabledState とは値が一部異なる
// (Paused: CIM 32768 → provider 9、Saved: CIM 32769 → provider 6)。遷移中や未知の状態は Other。
func enabledStateToVmState(s uint16) api.VmState {
	switch s {
	case hyperv.EnabledStateEnabled:
		return api.VmState_Running
	case hyperv.EnabledStateDisabled:
		return api.VmState_Off
	case hyperv.EnabledStatePaused:
		return api.VmState_Paused
	case hyperv.EnabledStateSaved:
		return api.VmState_Saved
	default:
		return api.VmState_Other
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
//   - running          → StartVM (Paused/Saved からの再開も RequestStateChange(Enabled) で成立)
//   - off + turnOff     → TurnOffVM (強制電源断)
//   - off + !turnOff    → ShutdownVM (ゲスト OS シャットダウン、Integration Services 必須)
//
// 既に目標状態なら通信せず no-op (冪等)。timeout/pollPeriod(秒) は状態遷移 Job の待機に使う。
func (c *ClientConfig) UpdateVmStatus(
	ctx context.Context,
	vmName string,
	timeout uint32,
	pollPeriod uint32,
	state api.VmState,
	turnOff bool,
) error {
	cs, err := c.WsmanClient.FindComputerSystemByElementName(ctx, vmName)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVmStatus %q: %w", vmName, err)
	}
	// 既に目標状態なら何もしない (RequestStateChange を無駄に呼ばず、冪等な再 apply を安全にする)。
	if enabledStateToVmState(cs.EnabledState) == state {
		return nil
	}
	guid := cs.Name

	var jobRef string
	switch state {
	case api.VmState_Running:
		jobRef, err = c.WsmanClient.StartVM(ctx, guid)
	case api.VmState_Off:
		if turnOff {
			jobRef, err = c.WsmanClient.TurnOffVM(ctx, guid)
		} else {
			jobRef, err = c.WsmanClient.ShutdownVM(ctx, guid)
		}
	default:
		return fmt.Errorf("hyperv-wsman: UpdateVmStatus %q: 目標状態 %q は go-wsman 経路では未対応 (running/off のみ設定可)",
			vmName, api.VmState_name[state])
	}
	if err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVmStatus %q: 状態変更要求: %w", vmName, err)
	}
	return c.WsmanClient.WaitForJob(ctx, jobRef, vmStatusWaitOpts(timeout, pollPeriod)...)
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
