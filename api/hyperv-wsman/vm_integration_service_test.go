package hyperv_wsman

import (
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestClientConfig_ImplementsHypervVmIntegrationServiceClient は ClientConfig が
// api.HypervVmIntegrationServiceClient を実装し、無条件 PS だった Get/CreateOrUpdate が
// 本パッケージでシャドウイング (promotion ではなく直接定義) されていることを検証する。
// Enable/DisableVmIntegrationService (個別メソッド) は resource 層から呼ばれないため
// 埋め込み winrm から promotion されたままで良い。assertShadowedIn の詳細は
// vm_processor_test.go を参照。
func TestClientConfig_ImplementsHypervVmIntegrationServiceClient(t *testing.T) {
	var c *ClientConfig
	var _ api.HypervVmIntegrationServiceClient = c // コンパイル時チェック

	assertShadowedIn(t, "GetVmIntegrationServices", "vm_integration_service.go")
	assertShadowedIn(t, "CreateOrUpdateVmIntegrationServices", "vm_integration_service.go")
}

// TestCreateOrUpdateVmIntegrationServices_EmptyGuard は空リストが WsmanClient を触らず
// no-op で返ることを検証する (GPU/processor と同じ空ガード)。
func TestCreateOrUpdateVmIntegrationServices_EmptyGuard(t *testing.T) {
	c := &ClientConfig{} // WsmanClient も埋め込み winrm も nil
	if err := c.CreateOrUpdateVmIntegrationServices(t.Context(), "any-vm", nil); err != nil {
		t.Errorf("空リストは no-op であるべき: %v", err)
	}
	if err := c.CreateOrUpdateVmIntegrationServices(t.Context(), "any-vm", []api.VmIntegrationService{}); err != nil {
		t.Errorf("空スライスは no-op であるべき: %v", err)
	}
}
