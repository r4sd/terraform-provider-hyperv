package hyperv_wsman

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"text/template"

	winrm_helper "github.com/taliesins/terraform-provider-hyperv/api/winrm-helper"
)

// StrictNoPSClient は strict モード(PowerShell 呼び出し 0 件の検証)用の winrm_helper.Client 実装。
//
// go-wsman シャドウが未実装のメソッドは、埋め込み hyperv_winrm.ClientConfig から promotion されて
// この WinRmClient を通じて PowerShell を叩く。strict モードでは WinRmClient をこのスタブに差し替える
// ことで、homelab が使う経路で PS フォールバックが 1 件でも走ったら即座に fail-fast で検知できる
// (v2.0「Home-env PS-free」の完了判定=陽性証明の手段)。
//
// どのメソッドが呼ばれても error + カウンタ加算で返す。error はそのまま provider の操作エラーに
// なり、カウンタは「どのスクリプトが呼ばれたか」を含めて後から集計できる(fire-and-forget で
// error が握り潰される経路の検知漏れを防ぐ二重化)。スレッドセーフ。
type StrictNoPSClient struct {
	calls  atomic.Int64
	mu     sync.Mutex
	labels []string
}

// Calls は strict モード中に検知した PS フォールバック呼び出しの総数を返す。
func (s *StrictNoPSClient) Calls() int64 { return s.calls.Load() }

// Labels は検知した PS 呼び出しのラベル一覧(呼ばれた順)を返す。診断用。
func (s *StrictNoPSClient) Labels() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.labels))
	copy(out, s.labels)
	return out
}

func (s *StrictNoPSClient) fail(op string) error {
	s.calls.Add(1)
	s.mu.Lock()
	s.labels = append(s.labels, op)
	s.mu.Unlock()
	return fmt.Errorf("strict mode: PowerShell フォールバックが呼ばれました (%s)。go-wsman シャドウ未実装の経路です", op)
}

func scriptLabel(t *template.Template) string {
	if t == nil {
		return "<nil>"
	}
	return t.Name()
}

// --- winrm_helper.Client 実装(全メソッド fail-fast) ---

func (s *StrictNoPSClient) RunFireAndForgetScript(_ context.Context, script *template.Template, _ interface{}) error {
	return s.fail("RunFireAndForgetScript:" + scriptLabel(script))
}

func (s *StrictNoPSClient) RunScriptWithResult(_ context.Context, script *template.Template, _ interface{}, _ interface{}) error {
	return s.fail("RunScriptWithResult:" + scriptLabel(script))
}

func (s *StrictNoPSClient) UploadFile(_ context.Context, _ string, _ string) (string, error) {
	return "", s.fail("UploadFile")
}

func (s *StrictNoPSClient) UploadDirectory(_ context.Context, _ string, _ []string) (string, []string, error) {
	return "", nil, s.fail("UploadDirectory")
}

func (s *StrictNoPSClient) FileExists(_ context.Context, _ string) (bool, error) {
	return false, s.fail("FileExists")
}

func (s *StrictNoPSClient) DirectoryExists(_ context.Context, _ string) (bool, error) {
	return false, s.fail("DirectoryExists")
}

func (s *StrictNoPSClient) DeleteFileOrDirectory(_ context.Context, _ string) error {
	return s.fail("DeleteFileOrDirectory")
}

func (s *StrictNoPSClient) RemoveFilesByPrefix(_ context.Context, _ string, _ string) error {
	return s.fail("RemoveFilesByPrefix")
}

// コンパイル時に winrm_helper.Client を満たすことを保証する。
var _ winrm_helper.Client = (*StrictNoPSClient)(nil)
