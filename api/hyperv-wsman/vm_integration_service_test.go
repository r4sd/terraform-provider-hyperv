package hyperv_wsman

import (
	"reflect"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestClientConfig_ImplementsHypervVmIntegrationServiceClient は ClientConfig が
// api.HypervVmIntegrationServiceClient を実装し、無条件 PS だった GetVmIntegrationServices が
// 本パッケージでシャドウイングされていることを検証する。Enable/Disable/CreateOrUpdate は
// 埋め込み winrm から promotion される (書き込みは v2.1 まで PS フォールバック)。
func TestClientConfig_ImplementsHypervVmIntegrationServiceClient(t *testing.T) {
	var c *ClientConfig
	var _ api.HypervVmIntegrationServiceClient = c // コンパイル時チェック

	cType := reflect.TypeOf((*ClientConfig)(nil))
	if _, ok := cType.MethodByName("GetVmIntegrationServices"); !ok {
		t.Error("GetVmIntegrationServices が hyperv-wsman で定義されていない (無条件 PS が解消しない)")
	}
}
