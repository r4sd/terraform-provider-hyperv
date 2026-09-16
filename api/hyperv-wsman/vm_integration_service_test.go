package hyperv_wsman

import (
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
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

// TestIntegrationServicesFromWsman は Read が state に書く Name が、ホスト OS 言語に
// ローカライズされる ElementName ではなく、ロケール非依存の Component になることを検証する (#98)。
//
// Terraform の `integration_services` は英語名をキーにした TypeMap で、書き込み側も英語名しか
// 受理しない。Read が ElementName を書くと、日本語ホストでは state のキーが config と食い違い、
// その state 由来の名前で apply すると unknown component で落ちる。
func TestIntegrationServicesFromWsman(t *testing.T) {
	// ElementName 側はローカライズされている想定。"キー値ペア交換" は実測値 (#98 の再現ログ)、
	// 他は実機の翻訳ではない合成文字列 (ここで主張したいのは Name が ElementName に依存しない
	// ことなので、英語名と一致しない文字列であればよい)。
	in := []hyperv.IntegrationService{
		{Component: hyperv.IntegrationServiceKeyValuePairExchange, Name: "キー値ペア交換", Enabled: true},
		{Component: hyperv.IntegrationServiceHeartbeat, Name: "[合成] Heartbeat のローカライズ名", Enabled: true},
		{Component: hyperv.IntegrationServiceGuestServiceInterface, Name: "[合成] GSI のローカライズ名", Enabled: false},
	}
	want := []api.VmIntegrationService{
		{Name: "Key-Value Pair Exchange", Enabled: true},
		{Name: "Heartbeat", Enabled: true},
		{Name: "Guest Service Interface", Enabled: false},
	}

	got := integrationServicesFromWsman(in)
	if len(got) != len(want) {
		t.Fatalf("len: got %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d]: got %+v, want %+v", i, got[i], want[i])
		}
	}

	// 変換した名前が書き込み側の受理する 6 種に収まること (Read→Write の round-trip が #98 の本体)。
	// CreateOrUpdateVmIntegrationServices は Name を IntegrationServiceComponent へキャストして
	// go-wsman に渡すので、ここに無い名前を書くと apply が unknown component で落ちる。
	writable := map[hyperv.IntegrationServiceComponent]bool{
		hyperv.IntegrationServiceHeartbeat:             true,
		hyperv.IntegrationServiceKeyValuePairExchange:  true,
		hyperv.IntegrationServiceShutdown:              true,
		hyperv.IntegrationServiceTimeSynchronization:   true,
		hyperv.IntegrationServiceVSS:                   true,
		hyperv.IntegrationServiceGuestServiceInterface: true,
	}
	for _, s := range got {
		if !writable[hyperv.IntegrationServiceComponent(s.Name)] {
			t.Errorf("Read が返した %q を書き込み側に渡せない", s.Name)
		}
	}
}
