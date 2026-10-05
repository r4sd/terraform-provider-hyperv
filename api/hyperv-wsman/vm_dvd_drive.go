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

// dvd_drive スキーマの既定リソースプール。既定以外が来たら go-wsman 未対応として拒否する。
const dvdDefaultResourcePool = "Primordial"

// dvdDriveRef は 1 本の DVD (ISO マウント) と、その Detach に必要な InstanceID を束ねる。
//
// Detach は「Storage (SASD) 削除 → Drive (RASD) 削除」の 2 段が必須なため、Drive だけでなく
// Storage の InstanceID も保持する (子 SASD を残したまま Drive を消すと VMMS が拒否する #97)。
type dvdDriveRef struct {
	dvd               api.VmDvdDrive
	driveInstanceID   string // Msvm_ResourceAllocationSettingData (DVD Drive) の InstanceID
	storageInstanceID string // Msvm_StorageAllocationSettingData (ISO) の InstanceID
}

// validateDvdOptions は go-wsman 経路が未対応の DVD オプションを拒否する (silent drop 回避)。
// 現状は既定リソースプールのみサポートする (ISO パスの有無は問わない。#67 で空メディア対応)。
//
// path/pool の両方をここで検証するのが重要: Update / CreateOrUpdate は破壊操作 (detach) を
// 実行してから attach するため、未対応値の検出が attach まで遅れると「detach 済み・attach 拒否」
// の部分適用で state が乖離する。呼び出し側は必ず破壊操作の前に本関数で全件検証すること。
func validateDvdOptions(path, resourcePoolName string) error {
	if resourcePoolName != "" && resourcePoolName != dvdDefaultResourcePool {
		return fmt.Errorf(
			"hyperv-wsman: dvd_drive の resource_pool_name は go-wsman 経路 (HYPERV_USE_WSMAN) では未対応です。"+
				"PowerShell 経路を使うか、既定値 %q にしてください", dvdDefaultResourcePool)
	}
	// 空メディア (ISO 未指定 = メディアなし DVD ドライブ) は go-wsman AddDVDDrive で対応済 (#67)。
	// ここでは弾かない。CreateVmDvdDrive が path の有無で AttachDVD / AddDVDDrive を選ぶ。
	return nil
}

// vmIsGen2 は VM が Generation 2 (UEFI) かを VirtualSystemSubType で判定する。
// Gen2 は DVD/ディスクを SCSI に、Gen1 は IDE に接続するため、アタッチ先 Controller 種別の決定に使う。
func (c *ClientConfig) vmIsGen2(ctx context.Context, vmGUID string) (bool, error) {
	sd, err := c.WsmanClient.GetSystemSettingData(ctx, vmGUID)
	if err != nil {
		return false, fmt.Errorf("hyperv-wsman: 世代判定 %q: %w", vmGUID, err)
	}
	return sd.VirtualSystemSubType == hyperv.VirtualSystemSubTypeGen2, nil
}

// CreateVmDvdDrive は go-wsman 経由で DVD ドライブを追加する。
//
// path が非空なら ISO をマウントし (AttachDVD)、**空ならメディアなしドライブを作る**
// (AddDVDDrive、#67)。PS の Add-VMDvdDrive も -Path 省略で空ドライブを作るので、
// 経路によらず同じ結果になる。
//
// VmDvdDrive は controller 種別を持たないため、VM の世代からアタッチ先を決める
// (Gen2→SCSI / Gen1→IDE)。PowerShell の Add-VMDvdDrive と同じ自動選択。
func (c *ClientConfig) CreateVmDvdDrive(
	ctx context.Context,
	vmName string,
	controllerNumber int,
	controllerLocation int,
	path string,
	resourcePoolName string,
) error {
	if err := validateDvdOptions(path, resourcePoolName); err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVmDvdDrive %q: %w", vmName, err)
	}
	guid, gen2, err := c.resolveDvdTarget(ctx, vmName)
	if err != nil {
		return err
	}
	return c.attachDvdByGUID(ctx, vmName, guid, gen2, controllerNumber, controllerLocation, path)
}

// resolveDvdTarget は DVD 操作に必要な VM GUID と世代をまとめて解決する。
//
// どちらも **VM ごとに 1 回で足りる**。attach ごとに解決すると
// Msvm_ComputerSystem と Msvm_VirtualSystemSettingData の列挙が N 回走る (#68 項目 1)。
func (c *ClientConfig) resolveDvdTarget(ctx context.Context, vmName string) (guid string, gen2 bool, err error) {
	guid, err = c.resolveVMGUID(ctx, vmName)
	if err != nil {
		return "", false, fmt.Errorf("hyperv-wsman: DVD 操作 %q: %w", vmName, err)
	}
	gen2, err = c.vmIsGen2(ctx, guid)
	if err != nil {
		return "", false, fmt.Errorf("hyperv-wsman: DVD 操作 %q: %w", vmName, err)
	}
	return guid, gen2, nil
}

// attachDvdByGUID は解決済みの GUID / 世代を使って DVD ドライブを 1 本追加する。
//
// vmName はエラーメッセージ用 (go-wsman へは guid を渡す)。
// ensureScsiController は controllerNumber ごとに要るのでここに残す (冪等)。
func (c *ClientConfig) attachDvdByGUID(
	ctx context.Context,
	vmName, guid string,
	gen2 bool,
	controllerNumber int,
	controllerLocation int,
	path string,
) error {
	ct := hyperv.ControllerTypeIDE
	if gen2 {
		// go-wsman で作った Gen2 VM はシェル状態で SCSI Controller を持たない (#88) ため保証する。
		ct = hyperv.ControllerTypeSCSI
		if err := c.ensureScsiController(ctx, guid, int32(controllerNumber)); err != nil {
			return err
		}
	}
	// メディアなし (path 空) は Drive だけを追加する (#67)。AttachDVD は storage の紐付けまで
	// 行うので、空メディアには使えない (go-wsman 側で空 Path を拒否する)。
	if path == "" {
		if _, err := c.WsmanClient.AddDVDDrive(ctx, guid, hyperv.AddDVDDriveOptions{
			ControllerType:     ct,
			ControllerNumber:   controllerNumber,
			ControllerLocation: controllerLocation,
		}); err != nil {
			return fmt.Errorf("hyperv-wsman: CreateVmDvdDrive %q (メディアなし): %w", vmName, err)
		}
		return nil
	}
	// AttachDVD は内部で Drive/Storage の非同期 Job 完了まで待つ (go-wsman 側)。
	if _, err := c.WsmanClient.AttachDVD(ctx, guid, hyperv.AttachDVDOptions{
		ControllerType:     ct,
		ControllerNumber:   controllerNumber,
		ControllerLocation: controllerLocation,
		Path:               path,
	}); err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVmDvdDrive %q: %w", vmName, err)
	}
	return nil
}

// GetVmDvdDrives は VM にマウントされた ISO 一覧を返す (go-wsman 逆引き)。
func (c *ClientConfig) GetVmDvdDrives(ctx context.Context, vmName string) ([]api.VmDvdDrive, error) {
	refs, err := c.getDvdDriveRefs(ctx, vmName)
	if err != nil {
		return nil, err
	}
	result := make([]api.VmDvdDrive, 0, len(refs))
	for _, r := range refs {
		result = append(result, r.dvd)
	}
	return result, nil
}

// DeleteVmDvdDrive は index 番目の DVD を go-wsman 経由で Detach する (Storage→Drive 2 段削除)。
func (c *ClientConfig) DeleteVmDvdDrive(ctx context.Context, vmName string, index int) error {
	refs, err := c.getDvdDriveRefs(ctx, vmName)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(refs) {
		return fmt.Errorf("hyperv-wsman: DeleteVmDvdDrive %q: index %d out of range (VM has %d DVDs)", vmName, index, len(refs))
	}
	if _, err := c.WsmanClient.DetachStorage(ctx, refs[index].driveInstanceID, refs[index].storageInstanceID); err != nil {
		return fmt.Errorf("hyperv-wsman: DeleteVmDvdDrive %q: %w", vmName, err)
	}
	return nil
}

// UpdateVmDvdDrive は index 番目の DVD を所望の状態に置き換える (Detach してから再 Attach)。
func (c *ClientConfig) UpdateVmDvdDrive(
	ctx context.Context,
	vmName string,
	index int,
	controllerNumber int,
	controllerLocation int,
	path string,
	resourcePoolName string,
) error {
	// 破壊操作 (Delete) の前に検証する。attach 段階まで遅らせると Delete 成功後に Create が
	// 拒否して部分適用になる (ISO が外れたまま失敗)。
	if err := validateDvdOptions(path, resourcePoolName); err != nil {
		return fmt.Errorf("hyperv-wsman: UpdateVmDvdDrive %q: %w", vmName, err)
	}
	if err := c.DeleteVmDvdDrive(ctx, vmName, index); err != nil {
		return err
	}
	return c.CreateVmDvdDrive(ctx, vmName, controllerNumber, controllerLocation, path, resourcePoolName)
}

// CreateOrUpdateVmDvdDrives は所望の DVD 集合に収束させる (go-wsman)。
//
// go-wsman は attach/detach しか持たないため、集合差分で収束する: 現状にあって所望に無い ISO を
// Detach、所望にあって現状に無い ISO を Attach する。冪等。
func (c *ClientConfig) CreateOrUpdateVmDvdDrives(ctx context.Context, vmName string, dvdDrives []api.VmDvdDrive) error {
	// 未対応オプションは detach/attach を始める前に全件検証する。attach 段階で初めて弾くと、
	// 先行する detach だけが実機に適用されて部分適用・state 乖離になる (レビュー #66)。
	for _, d := range dvdDrives {
		if err := validateDvdOptions(d.Path, d.ResourcePoolName); err != nil {
			return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmDvdDrives %q: %w", vmName, err)
		}
	}
	current, err := c.getDvdDriveRefs(ctx, vmName)
	if err != nil {
		return err
	}
	toDetach, toAttach := planDvdDriveReconcile(current, dvdDrives)
	for _, r := range toDetach {
		if _, err := c.WsmanClient.DetachStorage(ctx, r.driveInstanceID, r.storageInstanceID); err != nil {
			return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmDvdDrives %q: detach: %w", vmName, err)
		}
	}
	if len(toAttach) == 0 {
		return nil
	}
	// GUID / 世代の解決は VM ごとに 1 回 (#68 項目 1)。
	guid, gen2, err := c.resolveDvdTarget(ctx, vmName)
	if err != nil {
		return err
	}
	for _, d := range toAttach {
		if err := c.attachDvdByGUID(ctx, vmName, guid, gen2, d.ControllerNumber, d.ControllerLocation, d.Path); err != nil {
			return err
		}
	}
	return nil
}

// getDvdDriveRefs は VM の DVD 一覧を go-wsman の逆引きで組み立てる (順序安定)。
//
// storage(ISO) → drive(Controller 内位置) → controller(IDE/SCSI 種別・番号) の 3 段結合で
// provider の VmDvdDrive を復元する。Detach 用に Drive/Storage の InstanceID も保持する。
func (c *ClientConfig) getDvdDriveRefs(ctx context.Context, vmName string) ([]dvdDriveRef, error) {
	guid, err := c.resolveVMGUID(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: get dvd drives %q: %w", vmName, err)
	}
	storages, err := c.WsmanClient.ListAttachedStorage(ctx, guid)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: list attached storage %q: %w", vmName, err)
	}
	dvdDrives, err := c.WsmanClient.ListDvdDrives(ctx, guid)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: list dvd drives %q: %w", vmName, err)
	}
	ideCtrls, err := c.WsmanClient.ListIDEControllers(ctx, guid)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: list IDE controllers %q: %w", vmName, err)
	}
	scsiCtrls, err := c.WsmanClient.ListSCSIControllers(ctx, guid)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: list SCSI controllers %q: %w", vmName, err)
	}
	return mapDvdDriveRefs(vmName, storages, dvdDrives, ideCtrls, scsiCtrls), nil
}

// mapDvdDriveRefs は go-wsman の storage/drive/controller 一覧を結合して VmDvdDrive を復元する。
//
// 🔴 **Drive (RASD) 起点で回し、storage を left join する** (#67)。
// 以前は storage(ISO) 起点だったため、**子 SASD を持たないメディア無しドライブが
// GetVmDvdDrives に不可視**だった。その状態でゲストが eject したり PS / Hyper-V マネージャーが
// 空ドライブを作ると、reconcile が「現状なし」と誤認して同じ AddressOnParent へ再 attach し、
// VMMS のアドレス衝突で apply が恒久失敗する (空ドライブは provider 経由で detach もできない)。
//
// dvdDrive.Parent → controller.InstanceID で親を辿り、storage.Parent → dvdDrive.InstanceID で
// メディアを引き当てる。メディア無しは Path="" / storageInstanceID="" で表現する
// (storageInstanceID が空なら DetachStorage は Drive 単独削除に分岐する)。
//
// VmDvdDrive は controller 種別を持たないが、VM は Gen1(IDE のみ)/Gen2(SCSI のみ) で種別が一意に
// 決まるため、Controller の一覧内 index をそのまま controller 番号として報告できる。
// メディアとして採るのは ISO (Virtual CD/DVD Disk) のみで、DVD ドライブに VHD が紐づいていても
// 無視する (Path は空のまま)。結果は (番号, 位置) で安定ソート。
//
// dvdDrives は呼び出し側 (go-wsman の ListDvdDrives) が Synthetic DVD Drive で絞っている。
func mapDvdDriveRefs(
	vmName string,
	storages []*hyperv.Msvm_StorageAllocationSettingData,
	dvdDrives []*hyperv.Msvm_ResourceAllocationSettingData,
	ideCtrls []*hyperv.Msvm_ResourceAllocationSettingData,
	scsiCtrls []*hyperv.Msvm_ResourceAllocationSettingData,
) []dvdDriveRef {
	driveByID := make(map[string]*hyperv.Msvm_ResourceAllocationSettingData, len(dvdDrives))
	for _, d := range dvdDrives {
		driveByID[d.InstanceID] = d
	}
	// controller InstanceID → 番号。WS-Man の列挙順は無保証なので InstanceID でソートしてから
	// 番号を振る (disk と同じ決定的順序。読み取りごとに番号が入れ替わると誤 detach を招く)。
	sortByInstanceID(ideCtrls)
	sortByInstanceID(scsiCtrls)
	ctrlByID := make(map[string]int)
	for i, cc := range ideCtrls {
		ctrlByID[cc.InstanceID] = i
	}
	for i, cc := range scsiCtrls {
		ctrlByID[cc.InstanceID] = i
	}

	// drive InstanceID → その drive に紐づく ISO。メディア無しはここに現れない。
	// 1 ドライブに複数の CD/DVD storage が付くことは実機では起きないが、起きた場合に
	// 読み取りごとに結果が入れ替わらないよう InstanceID 最小のものを採る (決定的順序)。
	mediaByDrive := make(map[string]*hyperv.Msvm_StorageAllocationSettingData, len(storages))
	for _, st := range storages {
		if st.ResourceSubType != hyperv.ResourceSubTypeVirtualCDDVDDisk {
			continue // DVD ドライブに紐づく VHD 等はメディアとして採らない
		}
		key := matchRefKey(st.Parent, driveByID)
		if key == "" {
			continue // 親 DVD Drive が特定できない
		}
		if cur, ok := mediaByDrive[key]; ok && cur.InstanceID <= st.InstanceID {
			continue
		}
		mediaByDrive[key] = st
	}

	refs := make([]dvdDriveRef, 0, len(dvdDrives))
	for _, drive := range dvdDrives {
		number, ok := ctrlByID[matchRefKey(drive.Parent, ctrlByID)]
		if !ok {
			continue // 親 Controller が特定できない
		}
		// パース失敗は握り潰さずスキップ (location=0 に化けると 0 番と衝突し reconcile が誤判断)。
		location, err := strconv.Atoi(drive.AddressOnParent)
		if err != nil {
			continue
		}
		// メディア無しは Path="" / storageInstanceID="" のまま。
		var path, storageID string
		if st := mediaByDrive[drive.InstanceID]; st != nil {
			path = st.HostResource
			storageID = st.InstanceID
		}
		refs = append(refs, dvdDriveRef{
			driveInstanceID:   drive.InstanceID,
			storageInstanceID: storageID,
			dvd: api.VmDvdDrive{
				VmName:             vmName,
				ControllerNumber:   number,
				ControllerLocation: location,
				Path:               path,
				ResourcePoolName:   dvdDefaultResourcePool,
			},
		})
	}
	sortDvdDriveRefs(refs)
	return refs
}

// sortDvdDriveRefs は (controller 番号, 位置) で安定ソートする。
func sortDvdDriveRefs(refs []dvdDriveRef) {
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].dvd.ControllerNumber != refs[j].dvd.ControllerNumber {
			return refs[i].dvd.ControllerNumber < refs[j].dvd.ControllerNumber
		}
		return refs[i].dvd.ControllerLocation < refs[j].dvd.ControllerLocation
	})
}

// dvdDriveKey は DVD の同一性キー (controller 番号 / 位置 / ISO パス)。パスは大小無視 (Windows 慣習)。
func dvdDriveKey(d api.VmDvdDrive) string {
	return fmt.Sprintf("%d/%d/%s", d.ControllerNumber, d.ControllerLocation, strings.ToLower(d.Path))
}

// planDvdDriveReconcile は現状 refs を所望 desired に収束させる detach/attach 計画を返す。
// 現状にあって所望に無いものを Detach (Storage/Drive の 2 段削除に両 InstanceID が要る)、
// 所望にあって現状に無いものを Attach。純関数なので table-driven test で検証できる。
func planDvdDriveReconcile(current []dvdDriveRef, desired []api.VmDvdDrive) (toDetach []dvdDriveRef, toAttach []api.VmDvdDrive) {
	currentKeys := make(map[string]struct{}, len(current))
	for _, r := range current {
		currentKeys[dvdDriveKey(r.dvd)] = struct{}{}
	}
	desiredKeys := make(map[string]struct{}, len(desired))
	for _, d := range desired {
		desiredKeys[dvdDriveKey(d)] = struct{}{}
	}
	for _, r := range current {
		if _, ok := desiredKeys[dvdDriveKey(r.dvd)]; !ok {
			toDetach = append(toDetach, r)
		}
	}
	for _, d := range desired {
		if _, ok := currentKeys[dvdDriveKey(d)]; !ok {
			toAttach = append(toAttach, d)
		}
	}
	return toDetach, toAttach
}
