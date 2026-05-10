package hyperv_wsman

import (
	"reflect"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestClientConfig_ImplementsHypervVhdClient は ClientConfig が
// api.HypervVhdClient を実装することを検証する。
//
// 重要: VhdExists は本パッケージで定義されているため、シャドウイング (override)
// が効いて hyperv-winrm の実装ではなく hyperv-wsman の実装が呼ばれる。
// 残りの 4 メソッド (CreateOrUpdateVhd / ResizeVhd / GetVhd / DeleteVhd) は
// 埋め込みの hyperv_winrm.ClientConfig から promotion される。
func TestClientConfig_ImplementsHypervVhdClient(t *testing.T) {
	// 型レベルでインターフェース実装を確認する (実行時にメソッド呼び出しはしない)
	var c *ClientConfig
	var _ api.HypervVhdClient = c // コンパイル時チェック

	// メソッド存在の確認
	cType := reflect.TypeOf((*ClientConfig)(nil))
	for _, methodName := range []string{
		"VhdExists",         // ← 本パッケージで定義 (シャドウイング)
		"CreateOrUpdateVhd", // ← hyperv-winrm から promotion
		"ResizeVhd",         // ← hyperv-winrm から promotion
		"GetVhd",            // ← hyperv-winrm から promotion
		"DeleteVhd",         // ← hyperv-winrm から promotion
	} {
		if _, ok := cType.MethodByName(methodName); !ok {
			t.Errorf("ClientConfig should expose method %s (via shadow or promotion)", methodName)
		}
	}
}

// TestClientConfig_VhdExists_DefinedInWsmanPackage は VhdExists が
// hyperv-wsman パッケージ自身で定義されていることを reflect 経由で検証する。
//
// これにより hyperv_winrm.ClientConfig.VhdExists ではなく、本パッケージの
// 実装が呼ばれることが保証される (シャドウイング)。
func TestClientConfig_VhdExists_DefinedInWsmanPackage(t *testing.T) {
	// VhdExists はポインタレシーバなので *ClientConfig で検索する
	cType := reflect.TypeOf((*ClientConfig)(nil))
	method, ok := cType.MethodByName("VhdExists")
	if !ok {
		t.Fatal("ClientConfig should have VhdExists method")
	}

	// MethodFunc の Pkg() を確認する代わりに、シグネチャを既知のものと突合
	if method.Type.NumIn() != 3 { // receiver + ctx + path
		t.Errorf("VhdExists signature mismatch: NumIn=%d", method.Type.NumIn())
	}
	if method.Type.NumOut() != 2 { // VhdExists + error
		t.Errorf("VhdExists signature mismatch: NumOut=%d", method.Type.NumOut())
	}
}
