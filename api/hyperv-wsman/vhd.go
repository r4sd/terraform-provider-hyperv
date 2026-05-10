package hyperv_wsman

import (
	"context"
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
