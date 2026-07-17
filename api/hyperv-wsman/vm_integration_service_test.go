package hyperv_wsman

import (
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestClientConfig_ImplementsHypervVmIntegrationServiceClient は ClientConfig が
// api.HypervVmIntegrationServiceClient を実装し、無条件 PS だった GetVmIntegrationServices が
// 本パッケージでシャドウイング (promotion ではなく直接定義) されていることを検証する。
// Enable/Disable/CreateOrUpdate は埋め込み winrm から promotion される (書き込みは v2.1 まで
// PS フォールバック)。assertShadowedIn の詳細は vm_processor_test.go を参照。
func TestClientConfig_ImplementsHypervVmIntegrationServiceClient(t *testing.T) {
	var c *ClientConfig
	var _ api.HypervVmIntegrationServiceClient = c // コンパイル時チェック

	assertShadowedIn(t, "GetVmIntegrationServices", "vm_integration_service.go")
}
