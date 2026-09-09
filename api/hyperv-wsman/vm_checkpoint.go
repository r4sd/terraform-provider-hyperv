package hyperv_wsman

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// vmCheckpointLocks は VM 単位の作成ロック。
//
// CreateVmCheckpoint は「一覧 → 作成 → 一覧の差分で新規を特定」する。CreateSnapshot が
// ResultingSnapshot を返さない (go-wsman #125) ための方式だが、同一 VM に複数の
// hyperv_vm_checkpoint リソースがあると terraform が並列 (既定 10) に走らせるため、
// 差分が 2 件以上になって両方失敗しうる。プロセス内はロックで直列化する。
//
// 別プロセス (別の terraform 実行) との競合までは防げない。恒久策は go-wsman #125 で
// Msvm_AffectedJobElement から作成済みスナップショットを引くこと。
var vmCheckpointLocks sync.Map

func lockVmCheckpoint(vmName string) func() {
	v, _ := vmCheckpointLocks.LoadOrStore(vmName, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// checkpointFromSettingData は CIM のスナップショット SettingData を api.VmCheckpoint に変換する。
//
// PS 版 (Get-VMSnapshot) との対応は 2026-09-10 に実機ダンプで確定した:
//
//	PS .Id               → ConfigurationID (スナップショット固有の GUID。VM GUID とは別物)
//	PS .Name             → ElementName
//	PS .ParentSnapshotId → Parent (WMI オブジェクトパス) から GUID を抽出
//	PS .CreationTime     → CreationTime (ISO 8601)
//	PS .CheckpointType   → UserSnapshotType を api.CheckpointType 名へ変換
func checkpointFromSettingData(vmName string, sd *hyperv.Msvm_VirtualSystemSettingData) (api.VmCheckpoint, error) {
	ct, err := checkpointTypeFromUserSnapshotType(sd.UserSnapshotType)
	if err != nil {
		return api.VmCheckpoint{}, fmt.Errorf("checkpoint_type: %w", err)
	}
	return api.VmCheckpoint{
		VmName:         vmName,
		Name:           sd.ElementName,
		CheckpointType: api.CheckpointType_name[ct],
		Id:             sd.ConfigurationID,
		ParentId:       hyperv.ParentSnapshotID(sd.Parent),
		CreationTime:   sd.CreationTime,
	}, nil
}

// findVmCheckpointByName は表示名でチェックポイントを 1 件特定する。
//
// ElementName に一意性の保証は無い (Hyper-V の既定名は秒精度のため、同一秒に作った
// チェックポイントは名前が重複する。2026-09-10 実機確認)。**複数一致は黙って先頭を
// 返さずエラーにする** — 誤ったチェックポイントを削除/復元する事故を避けるため。
//
// 見つからない場合は (nil, nil) を返す。PS 版 GetVmCheckpoint がゼロ値を返す挙動に合わせる。
func findVmCheckpointByName(cps []*hyperv.Msvm_VirtualSystemSettingData, checkpointName string) (*hyperv.Msvm_VirtualSystemSettingData, error) {
	var found []*hyperv.Msvm_VirtualSystemSettingData
	for _, cp := range cps {
		if cp.ElementName == checkpointName {
			found = append(found, cp)
		}
	}
	switch len(found) {
	case 0:
		return nil, nil
	case 1:
		return found[0], nil
	default:
		ids := make([]string, 0, len(found))
		for _, cp := range found {
			ids = append(ids, cp.ConfigurationID)
		}
		return nil, fmt.Errorf("チェックポイント名 %q が %d 件に一致する (ID: %s)。名前では一意に特定できない",
			checkpointName, len(found), strings.Join(ids, ", "))
	}
}

// newCheckpointInstanceID は作成前後の一覧を突き合わせ、新しく増えた 1 件の InstanceID を返す。
//
// CreateSnapshot の ResultingSnapshot は非同期時 (実運用ではほぼ常に) 空を返すため
// (go-wsman #125)、差分で特定するしかない。増分が 1 件でない場合は同時実行等が疑われるので
// 黙って先頭を取らずエラーにする。
func newCheckpointInstanceID(before, after []*hyperv.Msvm_VirtualSystemSettingData) (string, error) {
	seen := make(map[string]struct{}, len(before))
	for _, cp := range before {
		seen[cp.InstanceID] = struct{}{}
	}
	var added []string
	for _, cp := range after {
		if _, ok := seen[cp.InstanceID]; !ok {
			added = append(added, cp.InstanceID)
		}
	}
	if len(added) != 1 {
		return "", fmt.Errorf("作成されたチェックポイントを特定できない (増分 %d 件。並行作成が疑われる)", len(added))
	}
	return added[0], nil
}

// CreateVmCheckpoint は go-wsman 経由でチェックポイントを作成し、指定名にリネームする。
//
// CreateSnapshot は名前を指定する経路を持たない (SnapshotSettings に ElementName が無い、
// MOF 確認済み) ため、作成 → 差分で特定 → ModifySystemSettings でリネーム、の 3 段になる。
func (c *ClientConfig) CreateVmCheckpoint(ctx context.Context, vmName string, checkpointName string) error {
	if checkpointName == "" {
		return fmt.Errorf("hyperv-wsman: CreateVmCheckpoint %q: checkpointName must not be empty", vmName)
	}
	unlock := lockVmCheckpoint(vmName)
	defer unlock()

	guid, err := c.resolveVMGUID(ctx, vmName)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVmCheckpoint %q: %w", vmName, err)
	}

	before, err := c.WsmanClient.ListVmCheckpoints(ctx, guid)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVmCheckpoint %q: list before: %w", vmName, err)
	}
	if existing, err := findVmCheckpointByName(before, checkpointName); err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVmCheckpoint %q: %w", vmName, err)
	} else if existing != nil {
		return fmt.Errorf("hyperv-wsman: CreateVmCheckpoint %q: チェックポイント %q は既に存在する", vmName, checkpointName)
	}

	res, err := c.WsmanClient.CreateVmCheckpoint(ctx, guid, hyperv.SnapshotTypeFull)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVmCheckpoint %q: create: %w", vmName, err)
	}
	if err := c.WsmanClient.WaitForJob(ctx, res.JobRef); err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVmCheckpoint %q: wait create: %w", vmName, err)
	}

	after, err := c.WsmanClient.ListVmCheckpoints(ctx, guid)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVmCheckpoint %q: list after: %w", vmName, err)
	}
	instanceID, err := newCheckpointInstanceID(before, after)
	if err != nil {
		// どれが自分の作ったものか特定できないので巻き戻せない。
		// 既定名のチェックポイントが実機に残る旨をエラーに含めて気付けるようにする。
		return fmt.Errorf("hyperv-wsman: CreateVmCheckpoint %q: %w "+
			"(作成されたチェックポイントは特定できないため実機に残っている可能性がある)", vmName, err)
	}

	// instanceID 確定後の失敗は、作ったチェックポイントを巻き戻してから返す。
	// PS 版 (Checkpoint-VM -SnapshotName) は原子的なので、放置するとパリティが崩れ、
	// 再 apply のたびに既定名のチェックポイントが 1 個ずつ増える。
	fail := func(format string, args ...interface{}) error {
		if jr, e := c.WsmanClient.DestroyVmCheckpoint(ctx, instanceID); e == nil {
			_ = c.WsmanClient.WaitForJob(ctx, jr)
		}
		return fmt.Errorf(format, args...)
	}

	jobRef, err := c.WsmanClient.RenameVmCheckpoint(ctx, instanceID, checkpointName)
	if err != nil {
		return fail("hyperv-wsman: CreateVmCheckpoint %q: rename: %w", vmName, err)
	}
	if err := c.WsmanClient.WaitForJob(ctx, jobRef); err != nil {
		return fail("hyperv-wsman: CreateVmCheckpoint %q: wait rename: %w", vmName, err)
	}

	// 成功報告を信用せず読み直す。リネームが黙殺されると、既定名のチェックポイントが
	// 残ったまま「作成成功」になり、次の Read で見つからず terraform が壊れる。
	verify, err := c.WsmanClient.ListVmCheckpoints(ctx, guid)
	if err != nil {
		return fail("hyperv-wsman: CreateVmCheckpoint %q: verify: %w", vmName, err)
	}
	renamed, err := findVmCheckpointByName(verify, checkpointName)
	if err != nil {
		return fail("hyperv-wsman: CreateVmCheckpoint %q: %w", vmName, err)
	}
	if renamed == nil {
		return fail("hyperv-wsman: CreateVmCheckpoint %q: リネームが反映されていない (%q が見つからない)", vmName, checkpointName)
	}
	return nil
}

// GetVmCheckpoint は表示名でチェックポイントを取得する。
// 見つからない場合はゼロ値を返す (PS 版が空 JSON を返す挙動に合わせる)。
func (c *ClientConfig) GetVmCheckpoint(ctx context.Context, vmName string, checkpointName string) (api.VmCheckpoint, error) {
	cp, err := c.lookupCheckpoint(ctx, vmName, checkpointName)
	if err != nil {
		return api.VmCheckpoint{}, fmt.Errorf("hyperv-wsman: GetVmCheckpoint %q: %w", vmName, err)
	}
	if cp == nil {
		return api.VmCheckpoint{}, nil
	}
	result, err := checkpointFromSettingData(vmName, cp)
	if err != nil {
		return api.VmCheckpoint{}, fmt.Errorf("hyperv-wsman: GetVmCheckpoint %q: %w", vmName, err)
	}
	return result, nil
}

// DeleteVmCheckpoint は表示名で特定したチェックポイントを削除する。
// 不在は冪等に成功扱いする (PS 版の Remove-VMSnapshot も同様)。
func (c *ClientConfig) DeleteVmCheckpoint(ctx context.Context, vmName string, checkpointName string) error {
	cp, err := c.lookupCheckpoint(ctx, vmName, checkpointName)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: DeleteVmCheckpoint %q: %w", vmName, err)
	}
	if cp == nil {
		return nil
	}
	jobRef, err := c.WsmanClient.DestroyVmCheckpoint(ctx, cp.InstanceID)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: DeleteVmCheckpoint %q: destroy: %w", vmName, err)
	}
	if err := c.WsmanClient.WaitForJob(ctx, jobRef); err != nil {
		return fmt.Errorf("hyperv-wsman: DeleteVmCheckpoint %q: wait: %w", vmName, err)
	}
	return nil
}

// RestoreVmCheckpoint は表示名で特定したチェックポイントを VM に適用する。
func (c *ClientConfig) RestoreVmCheckpoint(ctx context.Context, vmName string, checkpointName string) error {
	cp, err := c.lookupCheckpoint(ctx, vmName, checkpointName)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: RestoreVmCheckpoint %q: %w", vmName, err)
	}
	if cp == nil {
		return fmt.Errorf("hyperv-wsman: RestoreVmCheckpoint %q: チェックポイント %q が見つからない", vmName, checkpointName)
	}
	// ApplySnapshot は稼働中 VM を受け付けず ReturnValue=32775 (Invalid State) を返す
	// (実機確認)。PS 版の Restore-VMSnapshot は稼働中でも成功し、VM はスナップショット
	// 時点の状態になる (実機で Running → 復元 → Off を確認) ので、内部で停止を挟んで
	// いるとみられる。パリティのため同じ挙動にする。
	//
	// 復元は現在の状態を捨てる操作なので、強制停止で意味論は崩れない。
	// restore_on_destroy はカオスエンジニアリング用途で対象が稼働中 VM のため、
	// ここが無いとリソース本来の使い道で動かない。
	guid, err := c.resolveVMGUID(ctx, vmName)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: RestoreVmCheckpoint %q: %w", vmName, err)
	}
	cs, err := c.WsmanClient.FindComputerSystemByElementName(ctx, vmName)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: RestoreVmCheckpoint %q: get state: %w", vmName, err)
	}
	if cs.EnabledState != hyperv.EnabledStateDisabled {
		offJob, err := c.WsmanClient.TurnOffVM(ctx, guid)
		if err != nil {
			return fmt.Errorf("hyperv-wsman: RestoreVmCheckpoint %q: 復元前の停止: %w", vmName, err)
		}
		if err := c.WsmanClient.WaitForJob(ctx, offJob); err != nil {
			return fmt.Errorf("hyperv-wsman: RestoreVmCheckpoint %q: 復元前の停止待ち: %w", vmName, err)
		}
	}

	jobRef, err := c.WsmanClient.ApplyVmCheckpoint(ctx, cp.InstanceID)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: RestoreVmCheckpoint %q: apply: %w", vmName, err)
	}
	if err := c.WsmanClient.WaitForJob(ctx, jobRef); err != nil {
		return fmt.Errorf("hyperv-wsman: RestoreVmCheckpoint %q: wait: %w", vmName, err)
	}
	return nil
}

// lookupCheckpoint は VM 解決 → 一覧 → 名前一致の共通処理。
func (c *ClientConfig) lookupCheckpoint(ctx context.Context, vmName, checkpointName string) (*hyperv.Msvm_VirtualSystemSettingData, error) {
	guid, err := c.resolveVMGUID(ctx, vmName)
	if err != nil {
		// VM ごと消えている場合、チェックポイントも当然存在しない。PS 版
		// (Get-VMSnapshot -ErrorAction SilentlyContinue) は空を返し、resource 層が
		// state から除去する。ここでエラーにすると refresh/plan が失敗して
		// 「VM を手で消したら terraform が動かせない」状態になる。
		if errors.Is(err, hyperv.ErrVMNotFound) {
			return nil, nil
		}
		return nil, err
	}
	cps, err := c.WsmanClient.ListVmCheckpoints(ctx, guid)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	return findVmCheckpointByName(cps, checkpointName)
}
