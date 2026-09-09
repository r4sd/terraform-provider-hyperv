package hyperv_wsman

import (
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	hyperv_winrm "github.com/taliesins/terraform-provider-hyperv/api/hyperv-winrm"
)

// TestNewRejectsNilDependencies は New が nil 依存を構成時に弾くことを検証する。
//
// 埋め込みの hyperv_winrm.ClientConfig は「未移行メソッドの promoted 呼び出し」と
// 「CIM で表現できない変更の PS 委譲」の両方の宛先になる。前者は本パッケージを経由せず
// 直接飛ぶためメソッド側ではガードできない。nil のまま組み立てると委譲した瞬間に
// nil 参照 panic になり、plugin プロセスごと落ちる。
func TestNewRejectsNilDependencies(t *testing.T) {
	if _, err := New(nil, &hyperv.Client{}); err == nil {
		t.Error("winrmConfig=nil はエラーになるべき")
	}
	if _, err := New(&hyperv_winrm.ClientConfig{}, nil); err == nil {
		t.Error("wsmanClient=nil はエラーになるべき")
	}
	if _, err := New(&hyperv_winrm.ClientConfig{}, &hyperv.Client{}); err != nil {
		t.Errorf("正常な組み合わせでエラー: %v", err)
	}
}
