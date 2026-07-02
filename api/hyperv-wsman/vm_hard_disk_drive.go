package hyperv_wsman

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// hard_disk_drives スキーマの「未設定」を表す既定値 (data_source/resource schema と一致)。
// これらの既定値は go-wsman が扱えなくても素通しし、既定以外が指定された時だけ拒否する。
const (
	hardDiskDiskNumberUnset   = uint32(4294967295) // MaxUint32 = passthrough 無効 (VHD パスモード)
	hardDiskDefaultPool       = "Primordial"       // 既定リソースプール
	hardDiskZeroQosPolicyGUID = "00000000-0000-0000-0000-000000000000"
)

// wsmanControllerType は provider の ControllerType を go-wsman の ControllerType に変換する。
func wsmanControllerType(ct api.ControllerType) (hyperv.ControllerType, error) {
	switch ct {
	case api.ControllerType_Ide:
		return hyperv.ControllerTypeIDE, nil
	case api.ControllerType_Scsi:
		return hyperv.ControllerTypeSCSI, nil
	default:
		return "", fmt.Errorf("unsupported controller type %d", ct)
	}
}

// unsupportedHardDiskOptions は go-wsman 経路が未対応のオプションが指定されていれば error を返す。
//
// silent drop を避けるため (rules: 未対応を黙って無視しない)、QoS・パススルーディスク・
// カスタムリソースプール・キャッシュ属性・永続予約が既定以外なら明示的に拒否する。これらは
// v2.1 以降で go-wsman に実装予定。VHD パス + IDE/SCSI 位置指定のみ本経路でサポートする。
func unsupportedHardDiskOptions(
	diskNumber uint32,
	resourcePoolName string,
	supportPersistentReservations bool,
	maximumIops uint64,
	minimumIops uint64,
	qosPolicyId string,
	overrideCacheAttributes api.CacheAttributes,
) error {
	var unsupported []string
	if diskNumber != hardDiskDiskNumberUnset {
		unsupported = append(unsupported, "disk_number (パススルー物理ディスク)")
	}
	if resourcePoolName != "" && resourcePoolName != hardDiskDefaultPool {
		unsupported = append(unsupported, "resource_pool_name")
	}
	if supportPersistentReservations {
		unsupported = append(unsupported, "support_persistent_reservations")
	}
	if maximumIops != 0 {
		unsupported = append(unsupported, "maximum_iops")
	}
	if minimumIops != 0 {
		unsupported = append(unsupported, "minimum_iops")
	}
	if qosPolicyId != "" && qosPolicyId != hardDiskZeroQosPolicyGUID {
		unsupported = append(unsupported, "qos_policy_id")
	}
	if overrideCacheAttributes != api.CacheAttributes_Default {
		unsupported = append(unsupported, "override_cache_attributes")
	}
	if len(unsupported) > 0 {
		return fmt.Errorf(
			"hyperv-wsman: hard_disk_drive のオプション %s は go-wsman 経路 (HYPERV_USE_WSMAN) では未対応です。"+
				"PowerShell 経路 (HYPERV_USE_WSMAN 未設定) を使うか、これらを既定値にしてください",
			strings.Join(unsupported, ", "))
	}
	return nil
}

// CreateVmHardDiskDrive は go-wsman 経由で VHD を IDE/SCSI Controller にアタッチする。
func (c *ClientConfig) CreateVmHardDiskDrive(
	ctx context.Context,
	vmName string,
	controllerType api.ControllerType,
	controllerNumber int32,
	controllerLocation int32,
	path string,
	diskNumber uint32,
	resourcePoolName string,
	supportPersistentReservations bool,
	maximumIops uint64,
	minimumIops uint64,
	qosPolicyId string,
	overrideCacheAttributes api.CacheAttributes,
) error {
	if err := unsupportedHardDiskOptions(diskNumber, resourcePoolName, supportPersistentReservations,
		maximumIops, minimumIops, qosPolicyId, overrideCacheAttributes); err != nil {
		return err
	}
	wsmanCT, err := wsmanControllerType(controllerType)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVmHardDiskDrive %q: %w", vmName, err)
	}
	_, err = c.WsmanClient.AttachVHD(ctx, vmName, hyperv.AttachVHDOptions{
		ControllerType:     wsmanCT,
		ControllerNumber:   int(controllerNumber),
		ControllerLocation: int(controllerLocation),
		Path:               path,
	})
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVmHardDiskDrive %q: %w", vmName, err)
	}
	return nil
}

// hardDiskDriveRef は 1 本の VHD アタッチと、その Detach に必要な Drive InstanceID を束ねる。
type hardDiskDriveRef struct {
	drive           api.VmHardDiskDrive
	driveInstanceID string // Msvm_ResourceAllocationSettingData (Disk Drive) の InstanceID
}

// getHardDiskDriveRefs は VM の VHD 一覧を go-wsman の逆引きで組み立てる (順序安定)。
//
// storage(ファイル) → drive(Controller内位置) → controller(IDE/SCSI種別・番号) の 3 段結合で
// provider の VmHardDiskDrive を復元する。Detach 用に Drive の InstanceID も保持する。
func (c *ClientConfig) getHardDiskDriveRefs(ctx context.Context, vmName string) ([]hardDiskDriveRef, error) {
	storages, err := c.WsmanClient.ListAttachedStorage(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: list attached storage %q: %w", vmName, err)
	}
	drives, err := c.WsmanClient.ListDiskDrives(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: list disk drives %q: %w", vmName, err)
	}
	ideCtrls, err := c.WsmanClient.ListIDEControllers(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: list IDE controllers %q: %w", vmName, err)
	}
	scsiCtrls, err := c.WsmanClient.ListSCSIControllers(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: list SCSI controllers %q: %w", vmName, err)
	}
	return mapHardDiskDriveRefs(vmName, storages, drives, ideCtrls, scsiCtrls), nil
}

// GetVmHardDiskDrives は VM にアタッチされた VHD 一覧を返す (go-wsman 逆引き)。
func (c *ClientConfig) GetVmHardDiskDrives(ctx context.Context, vmName string) ([]api.VmHardDiskDrive, error) {
	refs, err := c.getHardDiskDriveRefs(ctx, vmName)
	if err != nil {
		return nil, err
	}
	result := make([]api.VmHardDiskDrive, 0, len(refs))
	for _, r := range refs {
		result = append(result, r.drive)
	}
	return result, nil
}

// DeleteVmHardDiskDrive は index 番目の VHD を go-wsman 経由で Detach する。
//
// index は GetVmHardDiskDrives が返す順序に対応する。逆引きで Drive InstanceID を解決して
// DetachStorage を呼ぶ (Drive を消すと紐づく Storage も連鎖削除される)。
func (c *ClientConfig) DeleteVmHardDiskDrive(ctx context.Context, vmName string, index int) error {
	refs, err := c.getHardDiskDriveRefs(ctx, vmName)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(refs) {
		return fmt.Errorf("hyperv-wsman: DeleteVmHardDiskDrive %q: index %d out of range (VM has %d disks)", vmName, index, len(refs))
	}
	if _, err := c.WsmanClient.DetachStorage(ctx, refs[index].driveInstanceID); err != nil {
		return fmt.Errorf("hyperv-wsman: DeleteVmHardDiskDrive %q: %w", vmName, err)
	}
	return nil
}

// UpdateVmHardDiskDrive は index 番目の VHD を所望の状態に置き換える。
//
// go-wsman には Controller/位置の in-place 変更が無いため、既存 Drive を Detach してから
// 新しい設定で AttachVHD する (再作成)。
func (c *ClientConfig) UpdateVmHardDiskDrive(
	ctx context.Context,
	vmName string,
	index int,
	controllerType api.ControllerType,
	controllerNumber int32,
	controllerLocation int32,
	path string,
	diskNumber uint32,
	resourcePoolName string,
	supportPersistentReservations bool,
	maximumIops uint64,
	minimumIops uint64,
	qosPolicyId string,
	overrideCacheAttributes api.CacheAttributes,
) error {
	if err := unsupportedHardDiskOptions(diskNumber, resourcePoolName, supportPersistentReservations,
		maximumIops, minimumIops, qosPolicyId, overrideCacheAttributes); err != nil {
		return err
	}
	if err := c.DeleteVmHardDiskDrive(ctx, vmName, index); err != nil {
		return err
	}
	return c.CreateVmHardDiskDrive(ctx, vmName, controllerType, controllerNumber, controllerLocation,
		path, diskNumber, resourcePoolName, supportPersistentReservations, maximumIops, minimumIops,
		qosPolicyId, overrideCacheAttributes)
}

// CreateOrUpdateVmHardDiskDrives は所望の VHD 集合に収束させる (go-wsman)。
//
// go-wsman は attach/detach しか持たないため、in-place 更新ではなく集合差分で収束する:
// 現状にあって所望に無い VHD を Detach、所望にあって現状に無い VHD を Attach する。
// Controller 種別変更も「旧 Detach + 新 Attach」で自然に処理される。冪等。
func (c *ClientConfig) CreateOrUpdateVmHardDiskDrives(ctx context.Context, vmName string, hardDiskDrives []api.VmHardDiskDrive) error {
	// 未対応オプションは適用前に全件チェックし、部分適用を避ける。
	for _, d := range hardDiskDrives {
		if err := unsupportedHardDiskOptions(d.DiskNumber, d.ResourcePoolName, d.SupportPersistentReservations,
			d.MaximumIops, d.MinimumIops, d.QosPolicyId, d.OverrideCacheAttributes); err != nil {
			return err
		}
	}
	current, err := c.getHardDiskDriveRefs(ctx, vmName)
	if err != nil {
		return err
	}
	toDetach, toAttach := planHardDiskDriveReconcile(current, hardDiskDrives)
	for _, driveInstanceID := range toDetach {
		if _, err := c.WsmanClient.DetachStorage(ctx, driveInstanceID); err != nil {
			return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmHardDiskDrives %q: detach: %w", vmName, err)
		}
	}
	for _, d := range toAttach {
		if err := c.CreateVmHardDiskDrive(ctx, vmName, d.ControllerType, d.ControllerNumber, d.ControllerLocation,
			d.Path, d.DiskNumber, d.ResourcePoolName, d.SupportPersistentReservations, d.MaximumIops, d.MinimumIops,
			d.QosPolicyId, d.OverrideCacheAttributes); err != nil {
			return err
		}
	}
	return nil
}

// --- 純関数 (table-driven test 対象) ---

// mapHardDiskDriveRefs は go-wsman の storage/drive/controller 一覧を結合して VmHardDiskDrive を復元する。
//
// storage.Parent → drive.InstanceID、drive.Parent → controller.InstanceID の 2 段で親を辿り、
// controller が IDE/SCSI どちらの一覧に居るかで種別を、一覧内の順序で controller 番号を決める。
// VHD (Virtual Hard Disk) のみ対象とし、DVD/ISO は除外する。結果は (種別, 番号, 位置) で安定ソート。
func mapHardDiskDriveRefs(
	vmName string,
	storages []*hyperv.Msvm_StorageAllocationSettingData,
	drives []*hyperv.Msvm_ResourceAllocationSettingData,
	ideCtrls []*hyperv.Msvm_ResourceAllocationSettingData,
	scsiCtrls []*hyperv.Msvm_ResourceAllocationSettingData,
) []hardDiskDriveRef {
	// drive InstanceID → drive
	driveByID := make(map[string]*hyperv.Msvm_ResourceAllocationSettingData, len(drives))
	for _, d := range drives {
		driveByID[d.InstanceID] = d
	}
	// controller InstanceID → (種別, 番号)
	type ctrlInfo struct {
		ct     api.ControllerType
		number int32
	}
	ctrlByID := make(map[string]ctrlInfo)
	for i, cc := range ideCtrls {
		ctrlByID[cc.InstanceID] = ctrlInfo{ct: api.ControllerType_Ide, number: int32(i)}
	}
	for i, cc := range scsiCtrls {
		ctrlByID[cc.InstanceID] = ctrlInfo{ct: api.ControllerType_Scsi, number: int32(i)}
	}

	refs := make([]hardDiskDriveRef, 0, len(storages))
	for _, s := range storages {
		if s.ResourceSubType != hyperv.ResourceSubTypeVirtualHardDisk {
			continue // DVD/ISO は対象外
		}
		drive := driveByID[matchRefKey(s.Parent, driveByID)]
		if drive == nil {
			continue // 親 Drive が特定できない (整合しないデータ) はスキップ
		}
		ci, ok := ctrlByID[matchRefKey(drive.Parent, ctrlByID)]
		if !ok {
			continue // 親 Controller が特定できない
		}
		// AddressOnParent は Controller 内 location (IDE 0-1 / SCSI 0-63)。int32 に収まる範囲で
		// パースする (ParseInt bitSize=32 で上限保証、CodeQL go/incorrect-integer-conversion 対策)。
		location, _ := strconv.ParseInt(drive.AddressOnParent, 10, 32)
		refs = append(refs, hardDiskDriveRef{
			driveInstanceID: drive.InstanceID,
			drive: api.VmHardDiskDrive{
				VmName:                        vmName,
				ControllerType:                ci.ct,
				ControllerNumber:              ci.number,
				ControllerLocation:            int32(location), // ParseInt(bitSize=32) 済みで安全
				Path:                          s.HostResource,
				DiskNumber:                    hardDiskDiskNumberUnset,
				ResourcePoolName:              hardDiskDefaultPool,
				SupportPersistentReservations: false,
				MaximumIops:                   0,
				MinimumIops:                   0,
				QosPolicyId:                   hardDiskZeroQosPolicyGUID,
				OverrideCacheAttributes:       api.CacheAttributes_Default,
			},
		})
	}
	sortHardDiskDriveRefs(refs)
	return refs
}

// matchRefKey は EPR/参照文字列 ref に対応するマップのキーを返す (無ければ空文字)。
//
// go-wsman の Unmarshal は Parent(EPR) から末尾の非空テキスト = InstanceID を取り出すため、
// 通常は ref がそのままキーに一致する。実機で EPR ラッパーが残る場合に備え、包含一致も見る。
func matchRefKey[V any](ref string, m map[string]V) string {
	if _, ok := m[ref]; ok {
		return ref
	}
	for k := range m {
		if k != "" && strings.Contains(ref, k) {
			return k
		}
	}
	return ""
}

// sortHardDiskDriveRefs は (種別, Controller番号, 位置) で安定ソートする (index の決定性のため)。
func sortHardDiskDriveRefs(refs []hardDiskDriveRef) {
	sort.SliceStable(refs, func(i, j int) bool {
		a, b := refs[i].drive, refs[j].drive
		if a.ControllerType != b.ControllerType {
			return a.ControllerType < b.ControllerType
		}
		if a.ControllerNumber != b.ControllerNumber {
			return a.ControllerNumber < b.ControllerNumber
		}
		return a.ControllerLocation < b.ControllerLocation
	})
}

// hardDiskDriveKey は VHD の同一性を (種別, Controller番号, 位置, パス) で表す。
// パスは Windows の大小非依存を考慮して小文字化する。
func hardDiskDriveKey(d api.VmHardDiskDrive) string {
	return fmt.Sprintf("%d/%d/%d/%s", d.ControllerType, d.ControllerNumber, d.ControllerLocation, strings.ToLower(d.Path))
}

// planHardDiskDriveReconcile は現状 refs を所望 desired に収束させる detach/attach 計画を返す。
//
// 現状にあって所望に無いものを Detach (Drive InstanceID)、所望にあって現状に無いものを Attach。
// 純関数なので table-driven test で検証できる。
func planHardDiskDriveReconcile(current []hardDiskDriveRef, desired []api.VmHardDiskDrive) (toDetach []string, toAttach []api.VmHardDiskDrive) {
	currentKeys := make(map[string]string, len(current)) // key → driveInstanceID
	for _, r := range current {
		currentKeys[hardDiskDriveKey(r.drive)] = r.driveInstanceID
	}
	desiredKeys := make(map[string]struct{}, len(desired))
	for _, d := range desired {
		desiredKeys[hardDiskDriveKey(d)] = struct{}{}
	}
	for _, r := range current {
		if _, ok := desiredKeys[hardDiskDriveKey(r.drive)]; !ok {
			toDetach = append(toDetach, r.driveInstanceID)
		}
	}
	for _, d := range desired {
		if _, ok := currentKeys[hardDiskDriveKey(d)]; !ok {
			toAttach = append(toAttach, d)
		}
	}
	return toDetach, toAttach
}
