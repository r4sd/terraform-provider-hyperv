package hyperv_wsman

import (
	"reflect"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestClientConfig_ImplementsHypervVmFirmwareClient は ClientConfig が
// api.HypervVmFirmwareClient を実装し、無条件 PS だった GetVmFirmware/GetVmFirmwares が
// 本パッケージでシャドウイング (promotion ではなく直接定義) されていることを検証する。
// CreateOrUpdate/GetNoVmFirmwares は埋め込み winrm から promotion されるため、ここでは検証しない
// (WRITE 側は Slice D 継続)。
func TestClientConfig_ImplementsHypervVmFirmwareClient(t *testing.T) {
	var c *ClientConfig
	var _ api.HypervVmFirmwareClient = c // コンパイル時チェック

	assertShadowedIn(t, "GetVmFirmware", "vm_firmware.go")
	assertShadowedIn(t, "GetVmFirmwares", "vm_firmware.go")
}

func TestSecureBootTemplateIdToName(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{"MicrosoftWindows (実機確認済み)", "1734C6E8-3154-4DDA-BA5F-A874CC483422", "MicrosoftWindows"},
		{"未知のGUIDはそのまま返す", "00000000-0000-0000-0000-000000000000", "00000000-0000-0000-0000-000000000000"},
		{"空文字はそのまま返す", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := secureBootTemplateIdToName(tt.id); got != tt.want {
				t.Errorf("secureBootTemplateIdToName(%q): got %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestFirmwareFromSystemSettingData(t *testing.T) {
	settings := &hyperv.Msvm_VirtualSystemSettingData{
		SecureBoot:                   true,
		SecureBootTemplateId:         "1734C6E8-3154-4DDA-BA5F-A874CC483422",
		NetworkBootPreferredProtocol: hyperv.NetworkBootPreferredProtocolIPv6,
		ConsoleMode:                  hyperv.ConsoleModeCOM1,
		PauseAfterBootFailure:        true,
		BootSourceOrder:              nil,
	}
	got, err := firmwareFromSystemSettingData("vm-1", settings)
	if err != nil {
		t.Fatalf("firmwareFromSystemSettingData: %v", err)
	}
	want := api.VmFirmware{
		VmName:                       "vm-1",
		BootOrders:                   nil,
		EnableSecureBoot:             api.OnOffState_On,
		SecureBootTemplate:           "MicrosoftWindows",
		PreferredNetworkBootProtocol: api.IPProtocolPreference_IPv6,
		ConsoleMode:                  api.ConsoleModeType_Com1,
		PauseAfterBootFailure:        api.OnOffState_On,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("firmwareFromSystemSettingData:\ngot  %+v\nwant %+v", got, want)
	}
}

// TestFirmwareFromSystemSettingData_Defaults は SecureBoot=false / IPv4 / ConsoleMode=Default /
// PauseAfterBootFailure=false (ゼロ値側) の変換を検証する。
func TestFirmwareFromSystemSettingData_Defaults(t *testing.T) {
	settings := &hyperv.Msvm_VirtualSystemSettingData{
		SecureBoot:                   false,
		SecureBootTemplateId:         "",
		NetworkBootPreferredProtocol: hyperv.NetworkBootPreferredProtocolIPv4,
		ConsoleMode:                  hyperv.ConsoleModeDefault,
		PauseAfterBootFailure:        false,
	}
	got, err := firmwareFromSystemSettingData("vm-2", settings)
	if err != nil {
		t.Fatalf("firmwareFromSystemSettingData: %v", err)
	}
	if got.EnableSecureBoot != api.OnOffState_Off {
		t.Errorf("EnableSecureBoot: got %v, want Off", got.EnableSecureBoot)
	}
	if got.PreferredNetworkBootProtocol != api.IPProtocolPreference_IPv4 {
		t.Errorf("PreferredNetworkBootProtocol: got %v, want IPv4", got.PreferredNetworkBootProtocol)
	}
	if got.ConsoleMode != api.ConsoleModeType_Default {
		t.Errorf("ConsoleMode: got %v, want Default", got.ConsoleMode)
	}
	if got.PauseAfterBootFailure != api.OnOffState_Off {
		t.Errorf("PauseAfterBootFailure: got %v, want Off", got.PauseAfterBootFailure)
	}
}

// TestFirmwareFromSystemSettingData_BootOrderUnsupported は BootSourceOrder が非空の場合に
// silent drop せず明示エラーになることを検証する (DoD: 黙って成功報告する実装は禁止)。
func TestFirmwareFromSystemSettingData_BootOrderUnsupported(t *testing.T) {
	settings := &hyperv.Msvm_VirtualSystemSettingData{
		BootSourceOrder: []string{`\\HOST\root\virtualization\v2:Msvm_BootSourceSettingData.InstanceID="Microsoft:vm-guid\nic-guid\B"`},
	}
	_, err := firmwareFromSystemSettingData("vm-3", settings)
	if err == nil {
		t.Fatal("BootSourceOrder が非空なら明示エラーになるべき (go-wsman 経路は boot_order 未対応)")
	}
}
