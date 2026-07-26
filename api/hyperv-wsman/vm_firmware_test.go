package hyperv_wsman

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	got := firmwareFromSystemSettingData("vm-1", settings)
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
	got := firmwareFromSystemSettingData("vm-2", settings)
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

// TestGetVmFirmware_DelegatesWhenBootSourceOrderNonEmpty は BootSourceOrder が非空 (ブート可能
// デバイスが付いている Gen2 VM) の場合に、silent drop や広すぎる拒否をせず埋め込み PS (winrm) 実装へ
// 委譲することを検証する (Fable レビュー指摘、#96)。委譲分岐では nil 埋め込みクライアントの参照で
// panic することを利用し、「BootOrders を無視して空扱いにする」のではなく確実に委譲したことを確認する
// (WaitForVmNetworkAdaptersIps と同じ確認手法)。
//
// GetSystemSettingData 自体は httptest サーバで実際に応答させ (WsmanClient は本物の *hyperv.Client)、
// BootSourceOrder が非空の Msvm_VirtualSystemSettingData を返す。
func TestGetVmFirmware_DelegatesWhenBootSourceOrderNonEmpty(t *testing.T) {
	const vmName = "vm-1"
	const vmGUID = "11111111-aaaa-bbbb-cccc-000000000001"
	const enumXML = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/EnumerateResponse</a:Action></s:Header>
  <s:Body><e:EnumerateResponse><e:EnumerationContext>uuid:ctx</e:EnumerationContext></e:EnumerateResponse></s:Body>
</s:Envelope>`
	computerSystemPullXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration" xmlns:p="http://schemas.microsoft.com/wbem/wsman/1/wmi/root/virtualization/v2/Msvm_ComputerSystem">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/PullResponse</a:Action></s:Header>
  <s:Body><e:PullResponse><e:Items>
    <p:Msvm_ComputerSystem>
      <p:Name>%s</p:Name>
      <p:ElementName>%s</p:ElementName>
    </p:Msvm_ComputerSystem>
  </e:Items><e:EndOfSequence/></e:PullResponse></s:Body>
</s:Envelope>`, vmGUID, vmName)
	settingDataPullXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration" xmlns:p="http://schemas.microsoft.com/wbem/wsman/1/wmi/root/virtualization/v2/Msvm_VirtualSystemSettingData">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/PullResponse</a:Action></s:Header>
  <s:Body><e:PullResponse><e:Items>
    <p:Msvm_VirtualSystemSettingData>
      <p:InstanceID>Microsoft:%s</p:InstanceID>
      <p:ElementName>%s</p:ElementName>
      <p:VirtualSystemIdentifier>%s</p:VirtualSystemIdentifier>
      <p:VirtualSystemType>Microsoft:Hyper-V:System:Realized</p:VirtualSystemType>
      <p:BootSourceOrder>\\HOST\root\virtualization\v2:Msvm_BootSourceSettingData.InstanceID="Microsoft:%s\nic-guid\B"</p:BootSourceOrder>
    </p:Msvm_VirtualSystemSettingData>
  </e:Items><e:EndOfSequence/></e:PullResponse></s:Body>
</s:Envelope>`, vmGUID, vmName, vmGUID, vmGUID)

	responses := []string{enumXML, computerSystemPullXML, enumXML, settingDataPullXML}
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
		_, _ = w.Write([]byte(responses[callCount]))
		callCount++
	}))
	defer server.Close()

	wsmanClient, err := hyperv.NewClient(server.URL)
	if err != nil {
		t.Fatalf("hyperv.NewClient: %v", err)
	}
	c := &ClientConfig{WsmanClient: wsmanClient} // 埋め込み winrm は nil

	defer func() {
		if r := recover(); r == nil {
			t.Error("BootSourceOrder 非空でスキップせず winrm 実装へ委譲することを期待 (nil クライアントで panic)")
		}
	}()
	_, _ = c.GetVmFirmware(context.Background(), vmName)
}
