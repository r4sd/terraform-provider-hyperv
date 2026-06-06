package winrm_helper

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/masterzen/winrm"
	"github.com/taliesins/terraform-provider-hyperv/powershell"
)

// psSingleQuote は PowerShell の単一引用符文字列リテラル内へ安全に埋め込めるよう、
// 文字列中の ' を ” (2 個) にエスケープする。
//
// PowerShell では '...' リテラル内のリテラル単一引用符を ” で表す。エスケープを
// 怠るとリモートで任意コマンドが注入されうるため、ファイル操作コマンド構築の前提となる。
func psSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// buildRemoveFilesByPrefixCommand は dir 配下の、BaseName が prefix で始まる全ファイルを
// 削除する PowerShell ワンライナーを構築する。
//
// PowerShell 版 DeleteVhd (hyperv_winrm.deleteVhdTemplate) の
//
//	Get-ChildItem | Where BaseName.StartsWith(prefix) | Remove-Item -Force
//
// と同じ挙動を再現し、同 prefix の差分ディスク (.avhdx 等) も一括削除する。
// text/template エンジンを使わず fmt.Sprintf でワンライナー化する (案D の方針)。
//
// ディレクトリ・ファイルはいずれも -LiteralPath で渡し、glob メタ文字 ([ ] * ? 等) を
// 含むパスでも誤マッチしないようにする。dir / prefix は単一引用符をエスケープする。
func buildRemoveFilesByPrefixCommand(dir, prefix string) string {
	return fmt.Sprintf(
		"$ErrorActionPreference = 'Stop'; "+
			"Get-ChildItem -LiteralPath '%s' | "+
			"Where-Object { $_.BaseName.StartsWith('%s') } | "+
			"ForEach-Object { Remove-Item -LiteralPath $_.FullName -Force }",
		psSingleQuote(dir), psSingleQuote(prefix),
	)
}

// RemoveFilesByPrefix は dir 配下で BaseName が prefix で始まる全ファイルを WinRM
// (PowerShell) 経由で削除する、CIM 範囲外のファイル操作を担う薄ラッパー。
//
// go-wsman (CIM 専用) の責務外であるファイル削除を provider 側で完結させる
// (移行戦略の案D)。VHD の物理削除 + 差分ディスク一括削除に利用する。
func (c *ClientConfig) RemoveFilesByPrefix(ctx context.Context, dir string, prefix string) error {
	command := buildRemoveFilesByPrefixCommand(dir, prefix)

	winrmClient, err := c.WinRmClientPool.BorrowObject(ctx)
	if err != nil {
		return err
	}

	log.Printf("[DEBUG] RemoveFilesByPrefix dir=%q prefix=%q", dir, prefix)

	_, _, _, err = powershell.RunPowershell(winrmClient.(*winrm.Client), c.ElevatedUser, c.ElevatedPassword, c.Vars, command)

	err2 := c.WinRmClientPool.ReturnObject(ctx, winrmClient)
	if err != nil {
		return err
	}
	if err2 != nil {
		return err2
	}

	return nil
}
