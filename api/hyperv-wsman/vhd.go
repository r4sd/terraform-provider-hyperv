package hyperv_wsman

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// VhdExists は go-wsman 経由で VHD/VHDX ファイルの存在を確認する。
//
// PowerShell 版 (hyperv_winrm.VhdExists) と挙動互換:
//   - 成功 → Exists: true
//   - エラー (ファイル不在 / 接続失敗 等) → Exists: false (エラーは debug log のみ)
//
// SOAP Fault のエラーコード解析は Phase B-2 以降で精緻化する。現状は
// PowerShell 版と完全に同じ挙動 (全エラーを「不在」として扱う) にしている。
func (c *ClientConfig) VhdExists(ctx context.Context, path string) (api.VhdExists, error) {
	_, err := c.WsmanClient.GetVirtualHardDisk(ctx, path)
	if err != nil {
		log.Printf("[DEBUG][hyperv-wsman] VhdExists GetVirtualHardDisk error (treating as not-found): %v", err)
		return api.VhdExists{Exists: false}, nil
	}
	return api.VhdExists{Exists: true}, nil
}

// ResizeVhd は go-wsman 経由で既存 VHD/VHDX のサイズ (MaxInternalSize) を変更する。
//
// PowerShell 版 (hyperv_winrm.ResizeVhd) は同期 (Resize-VHD がブロック完了) だが、
// CIM の Msvm_ImageManagementService.ResizeVirtualHardDisk は ReturnValue=0 で
// 同期完了、4096 で非同期 Job 開始。Hyper-V の通常挙動では Resize は同期完了する
// ことが多いため、現状は同期成功のみ素通し、非同期開始時は WARN ログのみ出して
// 待機しない (Phase B-X.X 等で Job 完了待ちヘルパーを別実装予定)。
//
// 制約 (CIM 仕様):
//   - Fixed VHD: 拡大のみ可
//   - Dynamic/Differencing: MaxInternalSize の縮小も可 (実データ末尾まで)
//   - VM へアタッチ中: オフライン状態のみ縮小可
func (c *ClientConfig) ResizeVhd(ctx context.Context, path string, size uint64) error {
	jobRef, err := c.WsmanClient.ResizeVirtualHardDisk(ctx, path, size)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: ResizeVhd %q: %w", path, err)
	}
	if jobRef != "" {
		// ReturnValue=4096 (非同期開始): provider 側は同期完了を期待するが、
		// 現状は Job 完了待ちヘルパー未実装のため WARN ログのみ。
		// 実用上は Resize は同期完了が多いためレアケース。問題化したら別 PR で実装。
		log.Printf("[WARN][hyperv-wsman] ResizeVhd started asynchronously (jobRef=%s), not waiting for completion. Path=%q Size=%d", jobRef, path, size)
	}
	return nil
}

// vhdFormatFromPath はファイル拡張子から CIM の DiskFormat 値を導出する。
//
// PowerShell の New-VHD は拡張子からフォーマットを推論するが、CIM の
// CreateVirtualHardDisk は明示的な Format (2=VHD, 3=VHDX, 4=VHDSet) を要求する。
// 拡張子で判定し、不明な場合は modern default の VHDX を返す。
func vhdFormatFromPath(path string) uint16 {
	switch {
	case strings.HasSuffix(strings.ToLower(path), ".vhdx"):
		return uint16(api.VhdFormat_VHDX)
	case strings.HasSuffix(strings.ToLower(path), ".vhds"):
		return uint16(api.VhdFormat_VHDSet)
	case strings.HasSuffix(strings.ToLower(path), ".vhd"):
		return uint16(api.VhdFormat_VHD)
	default:
		return uint16(api.VhdFormat_VHDX)
	}
}

// vhdDiskType は api.VhdType を CIM の DiskType (uint16) に安全に変換する。
//
// 直接 uint16(vhdType) すると、vhdType が provider 上流で strconv.Atoi 由来
// (アーキ依存幅 int) のため CodeQL が上限チェックなしの縮小変換 (high) として検出する。
// 既知 enum を switch で明示マッピングし、不明値は 0 (Unknown) にフォールバックする。
func vhdDiskType(t api.VhdType) uint16 {
	switch t {
	case api.VhdType_Fixed:
		return 2
	case api.VhdType_Dynamic:
		return 3
	case api.VhdType_Differencing:
		return 4
	default:
		return 0 // Unknown
	}
}

// CreateOrUpdateVhd は go-wsman 経由で VHD/VHDX を作成または更新する。
//
// PowerShell 版 (hyperv_winrm.CreateOrUpdateVhd) の挙動を分岐ごとに再現:
//   - source 指定 (ファイルコピー) / sourceVm 指定 (VM ディスクキャプチャ):
//     CIM の範囲外 (ファイル操作・Convert-VHD) のため、埋め込みの PowerShell 実装に
//     フォールバックする (戦略確定: 案 D)。
//   - 既存 VHD あり: サイズ差があれば Resize、なければ no-op。
//   - 新規作成 (通常 / 差分ディスク): Msvm_ImageManagementService.CreateVirtualHardDisk。
//
// 非同期 Job (ReturnValue=4096) は現状 WARN ログのみで待機しない (Resize と同様)。
func (c *ClientConfig) CreateOrUpdateVhd(ctx context.Context, path string, source string, sourceVm string, sourceDisk int, vhdType api.VhdType, parentPath string, size uint64, blockSize uint32, logicalSectorSize uint32, physicalSectorSize uint32) error {
	// 案 D: source コピー / VM ディスクキャプチャは CIM 範囲外 → PowerShell 経路へ委譲。
	if source != "" || sourceVm != "" {
		// 注意: 本関数は source (URL 形式で認証情報を埋め込める) を扱うため、
		// CodeQL go/clear-text-logging が caller 由来の値のログ出力を secret 漏洩として
		// 検出する。caller 由来の path は出さない。
		log.Printf("[DEBUG][hyperv-wsman] CreateOrUpdateVhd: source/sourceVm 指定のため PowerShell 経路にフォールバック")
		return c.ClientConfig.CreateOrUpdateVhd(ctx, path, source, sourceVm, sourceDisk, vhdType, parentPath, size, blockSize, logicalSectorSize, physicalSectorSize)
	}

	// 既存チェック: あればサイズ更新のみ (PowerShell の path-exists 分岐に対応)。
	// GetVirtualHardDisk が成功 = 既存あり。エラーは「不在」扱い (VhdExists と同セマンティクス)。
	if existing, err := c.WsmanClient.GetVirtualHardDisk(ctx, path); err == nil && existing != nil {
		if size > 0 && existing.MaxInternalSize != size {
			return c.ResizeVhd(ctx, path, size)
		}
		return nil
	}

	// 新規作成。DiskFormat は拡張子から導出、DiskType は vhdType (parentPath 指定時は差分)。
	// vhdType は provider 上流で strconv.Atoi 由来 (アーキ依存幅 int) のため、直接
	// uint16(vhdType) すると CodeQL go/incorrect-integer-conversion (上限チェックなし
	// 縮小変換) を high 検出する。既知 enum のみ switch で安全に uint16 化する。
	diskType := vhdDiskType(vhdType)
	if parentPath != "" {
		diskType = vhdDiskType(api.VhdType_Differencing)
	}

	jobRef, err := c.WsmanClient.CreateVirtualHardDisk(ctx, &hyperv.Msvm_VirtualHardDiskSettingData{
		Path:               path,
		ParentPath:         parentPath,
		MaxInternalSize:    size,
		BlockSize:          blockSize,
		LogicalSectorSize:  logicalSectorSize,
		PhysicalSectorSize: physicalSectorSize,
		VirtualDiskFormat:  vhdFormatFromPath(path),
		VirtualDiskType:    diskType,
	})
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateOrUpdateVhd %q: %w", path, err)
	}
	if jobRef != "" {
		// caller 由来の path は出さず、サーバー生成の jobRef のみログする (clear-text-logging 対策)。
		log.Printf("[WARN][hyperv-wsman] CreateOrUpdateVhd started asynchronously (jobRef=%s), not waiting for completion.", jobRef)
	}
	return nil
}

// GetVhd は go-wsman 経由で VHD/VHDX の構成情報を取得する。
//
// PowerShell 版 (hyperv_winrm.GetVhd) と互換性のあるサブセット。Msvm_VirtualHardDiskSettingData
// (CIM) で取得できる構成情報のみ返し、物理状態 (FileSize/Attached/DiskNumber/
// FragmentationPercentage 等) は未設定のまま (ゼロ値)。
//
// provider 側 (`resource_hyperv_vhd.go` / `data_source_hyperv_vhd.go`) で実際に
// 参照されているフィールドは Path / VhdType / ParentPath / Size / BlockSize /
// LogicalSectorSize / PhysicalSectorSize の 7 件のみで、いずれも本実装でカバー済。
func (c *ClientConfig) GetVhd(ctx context.Context, path string) (api.Vhd, error) {
	sd, err := c.WsmanClient.GetVirtualHardDisk(ctx, path)
	if err != nil {
		return api.Vhd{}, fmt.Errorf("hyperv-wsman: GetVhd %q: %w", path, err)
	}

	// CIM 値 (uint16) は api.VhdType / api.VhdFormat と完全一致 (Fixed=2/Dynamic=3/Differencing=4、
	// VHD=2/VHDX=3/VHDSet=4)。直接キャストで安全に変換できる。
	return api.Vhd{
		Path:               sd.Path,
		BlockSize:          sd.BlockSize,
		LogicalSectorSize:  sd.LogicalSectorSize,
		PhysicalSectorSize: sd.PhysicalSectorSize,
		ParentPath:         sd.ParentPath,
		Size:               sd.MaxInternalSize, // CIM の論理サイズ
		VhdType:            api.VhdType(sd.VirtualDiskType),
		VhdFormat:          api.VhdFormat(sd.VirtualDiskFormat),
		// FileSize / MinimumSize / Attached / DiskNumber / Number /
		// FragmentationPercentage / Alignment / DiskIdentifier は CIM の
		// Msvm_VirtualHardDiskSettingData では取得できないためゼロ値。
		// provider 側で未参照のため互換性に影響なし。
	}, nil
}
