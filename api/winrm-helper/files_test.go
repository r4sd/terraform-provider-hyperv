package winrm_helper

import (
	"strings"
	"testing"
)

// TestPsSingleQuote は PowerShell 単一引用符文字列リテラル用のエスケープを検証する。
//
// PowerShell では文字列リテラル内の ' を ” (2 個) でエスケープする。これを誤ると
// リモートで任意コマンドが注入されうるため、データ変換ロジックとして必須テスト対象。
func TestPsSingleQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"引用符なしはそのまま", `C:\vms\disk`, `C:\vms\disk`},
		{"単一引用符を二重化", `O'Brien`, `O''Brien`},
		{"複数の引用符", `a'b'c`, `a''b''c`},
		{"空文字", ``, ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := psSingleQuote(tt.in); got != tt.want {
				t.Errorf("psSingleQuote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestBuildRemoveFilesByPrefixCommand は prefix マッチ削除の PowerShell ワンライナー
// 構築を検証する。
//
// PowerShell 版 DeleteVhd (hyperv_winrm.deleteVhdTemplate) と同じく
// 「BaseName が prefix で始まる全ファイルを削除」する挙動を再現する。差分ディスク
// (.avhdx 等) の一括削除可否を左右するため、コマンド文字列の正しさは必須テスト対象。
func TestBuildRemoveFilesByPrefixCommand(t *testing.T) {
	t.Run("標準ケースの完全一致", func(t *testing.T) {
		got := buildRemoveFilesByPrefixCommand(`C:\vms`, `disk`)
		want := `$ErrorActionPreference = 'Stop'; ` +
			`Get-ChildItem -LiteralPath 'C:\vms' | ` +
			`Where-Object { $_.BaseName.StartsWith('disk') } | ` +
			`ForEach-Object { Remove-Item -LiteralPath $_.FullName -Force }`
		if got != want {
			t.Errorf("buildRemoveFilesByPrefixCommand()\n got = %q\nwant = %q", got, want)
		}
	})

	t.Run("ディレクトリ/prefix の単一引用符はエスケープされる", func(t *testing.T) {
		got := buildRemoveFilesByPrefixCommand(`C:\v'ms`, `di'sk`)
		if !strings.Contains(got, `-LiteralPath 'C:\v''ms'`) {
			t.Errorf("ディレクトリの引用符がエスケープされていない: %q", got)
		}
		if !strings.Contains(got, `StartsWith('di''sk')`) {
			t.Errorf("prefix の引用符がエスケープされていない: %q", got)
		}
	})

	t.Run("エラー時に停止する設定を含む", func(t *testing.T) {
		got := buildRemoveFilesByPrefixCommand(`C:\vms`, `disk`)
		if !strings.Contains(got, `$ErrorActionPreference = 'Stop'`) {
			t.Errorf("ErrorActionPreference=Stop が含まれていない: %q", got)
		}
	})
}
