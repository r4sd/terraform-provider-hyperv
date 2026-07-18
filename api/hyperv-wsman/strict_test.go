package hyperv_wsman

import (
	"context"
	"strings"
	"testing"
	"text/template"

	winrm_helper "github.com/taliesins/terraform-provider-hyperv/api/winrm-helper"
)

// TestStrictNoPSClient_ImplementsInterface は StrictNoPSClient が winrm_helper.Client を
// 満たす (= WinRmClient として差し替え可能) ことをコンパイル時 + 実行時に確認する。
func TestStrictNoPSClient_ImplementsInterface(t *testing.T) {
	var _ winrm_helper.Client = &StrictNoPSClient{}
}

// TestStrictNoPSClient_FailsAndCounts は、どのメソッドが呼ばれても error を返し、
// カウンタとラベルが正しく積まれることを検証する。fire-and-forget の error 握り潰し経路も
// カウンタで検知できることの担保。
func TestStrictNoPSClient_FailsAndCounts(t *testing.T) {
	s := &StrictNoPSClient{}
	ctx := context.Background()
	tmpl := template.Must(template.New("GetVmProcessor").Parse("dummy"))

	// RunScriptWithResult は error を返し、ラベルにスクリプト名を含むこと。
	if err := s.RunScriptWithResult(ctx, tmpl, nil, nil); err == nil {
		t.Error("RunScriptWithResult は strict モードで error を返すべき")
	} else if !strings.Contains(err.Error(), "GetVmProcessor") {
		t.Errorf("error にスクリプト名が含まれるべき: %v", err)
	}

	if err := s.RunFireAndForgetScript(ctx, tmpl, nil); err == nil {
		t.Error("RunFireAndForgetScript は error を返すべき")
	}
	if _, err := s.UploadFile(ctx, "a", "b"); err == nil {
		t.Error("UploadFile は error を返すべき")
	}
	if _, _, err := s.UploadDirectory(ctx, "root", nil); err == nil {
		t.Error("UploadDirectory は error を返すべき")
	}
	if _, err := s.FileExists(ctx, "f"); err == nil {
		t.Error("FileExists は error を返すべき")
	}
	if _, err := s.DirectoryExists(ctx, "d"); err == nil {
		t.Error("DirectoryExists は error を返すべき")
	}
	if err := s.DeleteFileOrDirectory(ctx, "p"); err == nil {
		t.Error("DeleteFileOrDirectory は error を返すべき")
	}
	if err := s.RemoveFilesByPrefix(ctx, "d", "p"); err == nil {
		t.Error("RemoveFilesByPrefix は error を返すべき")
	}

	// 8 メソッド全てで 1 回ずつカウントされること。
	if got := s.Calls(); got != 8 {
		t.Errorf("Calls: got %d, want 8", got)
	}
	if got := len(s.Labels()); got != 8 {
		t.Errorf("Labels 長: got %d, want 8", got)
	}
}

// TestStrictNoPSClient_ZeroValue は未使用時 (PS が一度も呼ばれない=strict 合格) の初期状態が
// Calls()==0 であることを確認する。これが acc test の合格条件そのもの。
func TestStrictNoPSClient_ZeroValue(t *testing.T) {
	s := &StrictNoPSClient{}
	if s.Calls() != 0 {
		t.Errorf("初期 Calls: got %d, want 0", s.Calls())
	}
	if len(s.Labels()) != 0 {
		t.Errorf("初期 Labels: got %d, want 0", len(s.Labels()))
	}
}
