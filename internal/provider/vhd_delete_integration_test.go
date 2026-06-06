//go:build integration
// +build integration

package provider

// DeleteVhd (Phase B-X.4 / 案D) の実機統合テスト。
//
// go-wsman 移行後の VHD 削除は CIM 範囲外のファイル操作のため、provider 側の
// WinRM 薄ラッパー winrm_helper.RemoveFilesByPrefix が担う。本テストは実 Hyper-V
// ホストに対して以下を検証する:
//   - 本体 VHD (.vhdx) が削除される
//   - 同 prefix の差分ディスク (.avhdx 等) も一括削除される
//   - prefix が一致しないファイルは温存される
//
// integration タグ + 接続用環境変数が揃ったときのみ実行される (未設定なら Skip)。
//
// 実行例:
//
//	HYPERV_HOST=... HYPERV_USER=... HYPERV_PASSWORD=... \
//	HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true \
//	go test -tags integration ./internal/provider/ -run TestRealHostDeleteVhdPrefixCleanup -v
//
// VHD を作成・削除するディレクトリは HYPERV_TEST_VHD_DIR で上書きできる
// (既定値 D:\Hyper-V)。

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	pool "github.com/jolestar/go-commons-pool/v2"
	winrm "github.com/masterzen/winrm"
	winrm_helper "github.com/taliesins/terraform-provider-hyperv/api/winrm-helper"
)

func realHostConfigFromEnv(t *testing.T) *Config {
	t.Helper()
	port, _ := strconv.Atoi(os.Getenv("HYPERV_PORT"))
	if port == 0 {
		port = 5986
	}
	c := &Config{
		User:     os.Getenv("HYPERV_USER"),
		Password: os.Getenv("HYPERV_PASSWORD"),
		Host:     os.Getenv("HYPERV_HOST"),
		Port:     port,
		HTTPS:    os.Getenv("HYPERV_HTTPS") == "true",
		Insecure: os.Getenv("HYPERV_INSECURE") == "true",
		NTLM:     os.Getenv("HYPERV_USE_NTLM") == "true",
		Timeout:  "60s",
	}
	if c.Host == "" || c.User == "" || c.Password == "" {
		t.Skip("HYPERV_HOST / HYPERV_USER / HYPERV_PASSWORD 未設定のためスキップ")
	}
	return c
}

// newRealHostHelper は実ホスト接続済みの winrm_helper.ClientConfig と、任意の
// PowerShell を直接実行する関数 (fixture 作成 / Test-Path 検証用) を返す。
func newRealHostHelper(t *testing.T, c *Config) (*winrm_helper.ClientConfig, func(string) string) {
	t.Helper()
	ctx := context.Background()
	factory := pool.NewPooledObjectFactorySimple(
		func(context.Context) (interface{}, error) {
			return GetWinrmClient(c)
		})
	p := pool.NewObjectPoolWithDefaultConfig(ctx, factory)

	helper := &winrm_helper.ClientConfig{
		WinRmClientPool:  p,
		Vars:             "",
		ElevatedUser:     c.User,
		ElevatedPassword: c.Password,
	}

	// powershell.RunPowershell (スクリプトアップロード方式) ではなく、winrm の
	// RunPSWithContextWithString (powershell -EncodedCommand 直接実行) を使い、
	// fixture の準備・検証を DeleteVhd 本体の経路と切り分ける。
	runPS := func(command string) string {
		obj, err := p.BorrowObject(ctx)
		if err != nil {
			t.Fatalf("BorrowObject: %v", err)
		}
		defer p.ReturnObject(ctx, obj)
		stdout, stderr, _, err := obj.(*winrm.Client).RunPSWithContextWithString(ctx, command, "")
		if err != nil {
			t.Fatalf("RunPSWithContextWithString(%q): %v\nstderr=%s", command, err, stderr)
		}
		return strings.TrimSpace(stdout)
	}

	return helper, runPS
}

func TestRealHostDeleteVhdPrefixCleanup(t *testing.T) {
	c := realHostConfigFromEnv(t)
	helper, runPS := newRealHostHelper(t, c)
	ctx := context.Background()

	dir := os.Getenv("HYPERV_TEST_VHD_DIR")
	if dir == "" {
		dir = `D:\Hyper-V`
	}
	prefix := "tfdelcheck"
	base := dir + `\` + prefix + ".vhdx"           // 削除対象 (本体)
	diff := dir + `\` + prefix + ".avhdx"          // 同 prefix の差分ディスク (一括削除されるべき)
	control := dir + `\keepme_` + prefix + ".vhdx" // prefix 不一致 (温存されるべき)

	testPath := func(p string) bool {
		return runPS(fmt.Sprintf(`if (Test-Path -LiteralPath '%s') { 'True' } else { 'False' }`, p)) == "True"
	}
	newFile := func(p string) {
		runPS(fmt.Sprintf(`New-Item -ItemType File -Force -Path '%s' | Out-Null`, p))
	}

	// 後始末 (失敗時も含め残骸を消す)
	t.Cleanup(func() {
		runPS(fmt.Sprintf(`Remove-Item -LiteralPath '%s','%s','%s' -Force -ErrorAction SilentlyContinue`, base, diff, control))
	})

	// fixture: 3 ファイル作成
	newFile(base)
	newFile(diff)
	newFile(control)
	if !testPath(base) || !testPath(diff) || !testPath(control) {
		t.Fatalf("fixture 作成失敗: base=%v diff=%v control=%v", testPath(base), testPath(diff), testPath(control))
	}
	t.Logf("fixture OK: %s / %s / %s", base, diff, control)

	// 実行: DeleteVhd が内部で使う薄ラッパー (prefix マッチ削除)
	if err := helper.RemoveFilesByPrefix(ctx, dir, prefix); err != nil {
		t.Fatalf("RemoveFilesByPrefix: %v", err)
	}

	// 検証: base + diff は消え、control は残る
	if testPath(base) {
		t.Errorf("本体 VHD が削除されていない: %s", base)
	}
	if testPath(diff) {
		t.Errorf("差分ディスクが削除されていない: %s", diff)
	}
	if !testPath(control) {
		t.Errorf("prefix 不一致ファイルが誤って削除された: %s", control)
	}
	if !testPath(base) && !testPath(diff) && testPath(control) {
		t.Logf("検証成功: 本体+差分ディスク削除、非マッチ温存")
	}
}
