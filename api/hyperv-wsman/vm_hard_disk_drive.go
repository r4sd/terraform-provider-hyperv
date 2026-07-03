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

// resolveVMGUID は VM の表示名 (ElementName) を go-wsman が要求する GUID
// (Msvm_ComputerSystem.Name) に解決する。
//
// go-wsman の各メソッドの vmName 引数は GUID 契約 (matchSettingDataVM が
// "Microsoft:<GUID>" 前方一致でフィルタする)。terraform から来る表示名をそのまま渡すと
// 列挙が silent に空を返すため、必ず本関数で解決してから go-wsman を呼ぶ。
func (c *ClientConfig) resolveVMGUID(ctx context.Context, name string) (string, error) {
	cs, err := c.WsmanClient.FindComputerSystemByElementName(ctx, name)
	if err != nil {
		return "", fmt.Errorf("resolve VM %q: %w", name, err)
	}
	return cs.Name, nil
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
	guid, err := c.resolveVMGUID(ctx, vmName)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVmHardDiskDrive %q: %w", vmName, err)
	}
	// go-wsman で作った VM はシェル状態で SCSI Controller を持たない (#88) ため、SCSI
	// アタッチ前に対象 Controller の存在を保証する。IDE は Gen1 に既定で存在するので対象外。
	if controllerType == api.ControllerType_Scsi {
		if err := c.ensureScsiController(ctx, guid, controllerNumber); err != nil {
			return err
		}
	}
	// AttachVHD は内部で Drive/Storage の非同期 Job 完了まで待つ (go-wsman 側)。
	if _, err := c.WsmanClient.AttachVHD(ctx, guid, hyperv.AttachVHDOptions{
		ControllerType:     wsmanCT,
		ControllerNumber:   int(controllerNumber),
		ControllerLocation: int(controllerLocation),
		Path:               path,
	}); err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVmHardDiskDrive %q: %w", vmName, err)
	}
	return nil
}

// ensureScsiController は controllerNumber 番目の SCSI Controller が存在することを保証する。
// vmGUID は解決済みの VM GUID (呼び出し側で resolveVMGUID 済み)。
//
// go-wsman の DefineSystem はシェル VM を作り SCSI Controller を持たない (#88) ため、必要数に
// 満たなければ AddScsiController で追加する。追加のたびに再列挙して件数を確認する (無限ループ防止)。
func (c *ClientConfig) ensureScsiController(ctx context.Context, vmGUID string, controllerNumber int32) error {
	controllers, err := c.WsmanClient.ListSCSIControllers(ctx, vmGUID)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: ensure SCSI controller %q: %w", vmGUID, err)
	}
	for int32(len(controllers)) <= controllerNumber {
		res, err := c.WsmanClient.AddScsiController(ctx, vmGUID)
		if err != nil {
			return fmt.Errorf("hyperv-wsman: add SCSI controller %q: %w", vmGUID, err)
		}
		if res.JobRef != "" {
			if err := c.WsmanClient.WaitForJob(ctx, res.JobRef); err != nil {
				return fmt.Errorf("hyperv-wsman: wait add SCSI controller %q: %w", vmGUID, err)
			}
		}
		next, err := c.WsmanClient.ListSCSIControllers(ctx, vmGUID)
		if err != nil {
			return fmt.Errorf("hyperv-wsman: ensure SCSI controller %q: %w", vmGUID, err)
		}
		if len(next) <= len(controllers) {
			return fmt.Errorf("hyperv-wsman: add SCSI controller %q: 追加後も件数が増えない (%d)", vmGUID, len(next))
		}
		controllers = next
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
	guid, err := c.resolveVMGUID(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: get hard disk drives %q: %w", vmName, err)
	}
	storages, err := c.WsmanClient.ListAttachedStorage(ctx, guid)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: list attached storage %q: %w", vmName, err)
	}
	drives, err := c.WsmanClient.ListDiskDrives(ctx, guid)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: list disk drives %q: %w", vmName, err)
	}
	ideCtrls, err := c.WsmanClient.ListIDEControllers(ctx, guid)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: list IDE controllers %q: %w", vmName, err)
	}
	scsiCtrls, err := c.WsmanClient.ListSCSIControllers(ctx, guid)
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
	// controller InstanceID → (種別, 番号)。
	// WS-Man の列挙順は無保証なので、InstanceID でソートしてから番号を振る。読み取りごとに番号が
	// 入れ替わると全 disk のキーが変わり誤 detach/attach を招くため決定的順序が必須 (H2)。
	// go-wsman 側 attachStorage も同じ InstanceID ソートで controllers[番号] を選ぶ (書き込みと一致)。
	sortByInstanceID(ideCtrls)
	sortByInstanceID(scsiCtrls)
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
		// パース失敗は握り潰さずスキップする (location=0 に化けると実在の 0 番 disk とキー衝突し
		// reconcile が誤判断するため、M4)。
		location, err := strconv.ParseInt(drive.AddressOnParent, 10, 32)
		if err != nil {
			continue
		}
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

// extractInstanceIDFromRef は go-wsman が返す親参照から InstanceID を取り出す。
//
// 実 Hyper-V の Parent/HostResource は WMI オブジェクトパス
//
//	\\HOST\root\...\v2:Msvm_...SettingData.InstanceID="Microsoft:GUID\\...\\0"
//
// 形式で、参照内の InstanceID 値はバックスラッシュが二重エスケープ (\\) される。一方 List 系が
// 返す InstanceID は単一バックスラッシュ (\) なので、`.InstanceID="..."` を抽出して \\→\ に戻し、
// 突き合わせできるようにする。ref が既に素の InstanceID (golden 等) の場合はそのまま返す。
func extractInstanceIDFromRef(ref string) string {
	const marker = `.InstanceID="`
	i := strings.Index(ref, marker)
	if i < 0 {
		return ref
	}
	rest := ref[i+len(marker):]
	if j := strings.LastIndex(rest, `"`); j >= 0 {
		rest = rest[:j]
	}
	return strings.ReplaceAll(rest, `\\`, `\`)
}

// matchRefKey は EPR/参照文字列 ref に対応するマップのキーを返す (無ければ空文字)。
//
// 実 Hyper-V の Parent は WMI オブジェクトパス (InstanceID 二重エスケープ) のため、
// extractInstanceIDFromRef で素の InstanceID に正規化してから突き合わせる。
func matchRefKey[V any](ref string, m map[string]V) string {
	id := extractInstanceIDFromRef(ref)
	if _, ok := m[id]; ok {
		return id
	}
	// フォールバック: 抽出後も一致しない場合の包含一致 (golden / 想定外形式のため)。
	for k := range m {
		if k != "" && strings.Contains(id, k) {
			return k
		}
	}
	return ""
}

// sortByInstanceID は Controller 一覧を InstanceID で安定ソートする (番号採番の決定性のため)。
func sortByInstanceID(items []*hyperv.Msvm_ResourceAllocationSettingData) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].InstanceID < items[j].InstanceID
	})
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

// isAvhdx はパスが差分ディスク (チェックポイントの .avhdx) かどうかを返す。
func isAvhdx(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".avhdx")
}

// diskSlotKey は disk の物理スロット (種別, Controller番号, 位置) を表す (パスを含めない)。
func diskSlotKey(d api.VmHardDiskDrive) string {
	return fmt.Sprintf("%d/%d/%d", d.ControllerType, d.ControllerNumber, d.ControllerLocation)
}

// planHardDiskDriveReconcile は現状 refs を所望 desired に収束させる detach/attach 計画を返す。
//
// 現状にあって所望に無いものを Detach (Drive InstanceID)、所望にあって現状に無いものを Attach。
//
// チェックポイント保護 (M3): VM にスナップショットがあると Get は差分ディスク (.avhdx) のパスを返すが
// config は基底 (.vhdx) を持つため、素朴にキー比較すると detach→attach してスナップショットチェーンを
// 破壊する。基底↔差分の対応はパス単体からは一意に判定できない (基底名にアンダースコアを含み得る) ため、
// .avhdx が占めるスロット (種別/番号/位置) は「触らない」= detach もそのスロットへの attach もしない、
// という保守的な扱いにする。チェックポイント運用の本格対応は別途 (#46 の v2.1)。
//
// 純関数なので table-driven test で検証できる。
func planHardDiskDriveReconcile(current []hardDiskDriveRef, desired []api.VmHardDiskDrive) (toDetach []string, toAttach []api.VmHardDiskDrive) {
	// .avhdx が占めるスロットを収集 (このスロットは detach/attach 対象外)。
	checkpointSlots := make(map[string]struct{})
	for _, r := range current {
		if isAvhdx(r.drive.Path) {
			checkpointSlots[diskSlotKey(r.drive)] = struct{}{}
		}
	}
	currentKeys := make(map[string]string, len(current)) // key → driveInstanceID (.avhdx は除外)
	for _, r := range current {
		if isAvhdx(r.drive.Path) {
			continue
		}
		currentKeys[hardDiskDriveKey(r.drive)] = r.driveInstanceID
	}
	desiredKeys := make(map[string]struct{}, len(desired))
	for _, d := range desired {
		desiredKeys[hardDiskDriveKey(d)] = struct{}{}
	}
	for _, r := range current {
		if isAvhdx(r.drive.Path) {
			continue // チェックポイントの差分ディスクは detach しない
		}
		if _, ok := desiredKeys[hardDiskDriveKey(r.drive)]; !ok {
			toDetach = append(toDetach, r.driveInstanceID)
		}
	}
	for _, d := range desired {
		if _, ok := checkpointSlots[diskSlotKey(d)]; ok {
			continue // チェックポイントが占めるスロットには attach しない
		}
		if _, ok := currentKeys[hardDiskDriveKey(d)]; !ok {
			toAttach = append(toAttach, d)
		}
	}
	return toDetach, toAttach
}
