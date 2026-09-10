package api

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// emptyResourceData は vm_firmware が未設定の ResourceData を作る。
func emptyResourceData(t *testing.T) *schema.ResourceData {
	t.Helper()
	r := &schema.Resource{
		Schema: map[string]*schema.Schema{
			"vm_firmware": {
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Resource{Schema: map[string]*schema.Schema{}},
			},
		},
	}
	return r.TestResourceData()
}

// TestExpandVmFirmwaresDefaults は config に vm_firmware が無いときの既定値を固定する。
//
// full_lifecycle_strict_integration_test.go の「空 BootOrders」leg (#118) は
// ResourceData を作れないため同じ値を手で構成している。**その値がここからズレると
// テストが #99 の発火条件を踏まなくなる**ので、既定値そのものを固定して検出する。
func TestExpandVmFirmwaresDefaults(t *testing.T) {
	got, err := ExpandVmFirmwares(emptyResourceData(t))
	if err != nil {
		t.Fatalf("ExpandVmFirmwares: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d 件, want 1", len(got))
	}
	d := got[0]

	// #99 の発火条件。ここが非空になると strict テストが空要求の経路を通らなくなる。
	if len(d.BootOrders) != 0 {
		t.Errorf("BootOrders = %v, want 空 (#99 / #118 の発火条件)", d.BootOrders)
	}
	if d.EnableSecureBoot != OnOffState_On {
		t.Errorf("EnableSecureBoot = %v, want On", d.EnableSecureBoot)
	}
	if d.SecureBootTemplate != "MicrosoftWindows" {
		t.Errorf("SecureBootTemplate = %q, want MicrosoftWindows", d.SecureBootTemplate)
	}
	if d.PreferredNetworkBootProtocol != IPProtocolPreference_IPv4 {
		t.Errorf("PreferredNetworkBootProtocol = %v, want IPv4", d.PreferredNetworkBootProtocol)
	}
	if d.ConsoleMode != ConsoleModeType_Default {
		t.Errorf("ConsoleMode = %v, want Default", d.ConsoleMode)
	}
	if d.PauseAfterBootFailure != OnOffState_Off {
		t.Errorf("PauseAfterBootFailure = %v, want Off", d.PauseAfterBootFailure)
	}
}
