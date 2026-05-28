package hyperv_wsman

import (
	"context"
	"fmt"
	"log"

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
