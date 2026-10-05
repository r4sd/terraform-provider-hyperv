package hyperv_wsman

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
)

// CreateVmDvdDrive の **path の有無でプリミティブを選ぶ配線** を httptest で固める。
//
// 🔴 純関数 (validateDvdOptions / mapDvdDriveRefs) は個別にテストしてあるが、
// `WsmanClient` が具象型のため**どちらの go-wsman メソッドを呼んだか**が単体で
// 検証できていなかった。批判的レビューで分岐を反転する変異 (`path == ""` → `path != ""`)
// が生存すると指摘されたため追加した。
//
// この変異が入ると **ISO 指定なのに空ドライブが作られ ISO が黙って載らない** (silent corruption)。
//
// 応答は**順序ではなく ResourceURI / Action でディスパッチ**する。順序に依存させると
// go-wsman 内部の呼び出し順が変わるたびにテストが壊れ、配線の検証という本来の目的から外れる。
const dvdWiringVMGUID = "11111111-aaaa-bbbb-cccc-000000000001"

var (
	dvdActionRe  = regexp.MustCompile(`<a:Action[^>]*>([^<]+)</a:Action>`)
	dvdResURIRe  = regexp.MustCompile(`<w:ResourceURI[^>]*>([^<]+)</w:ResourceURI>`)
	dvdLastSegRe = regexp.MustCompile(`([^/]+)$`)
)

func dvdLastSeg(s string) string {
	if m := dvdLastSegRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return s
}

// dvdWiringServer は ResourceURI / Action で応答を選ぶスタブサーバ。
func dvdWiringServer(t *testing.T) (*ClientConfig, *[]string, func()) {
	t.Helper()
	bodies := make([]string, 0, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		bodies = append(bodies, body)

		action, res := "", ""
		if m := dvdActionRe.FindStringSubmatch(body); m != nil {
			action = dvdLastSeg(m[1])
		}
		if m := dvdResURIRe.FindStringSubmatch(body); m != nil {
			res = dvdLastSeg(m[1])
		}

		w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
		switch {
		case action == "Enumerate":
			_, _ = w.Write([]byte(dvdWiringEnum()))
		case action == "Pull" && res == "Msvm_ComputerSystem":
			_, _ = w.Write([]byte(dvdWiringComputerSystemPull()))
		case action == "Pull" && res == "Msvm_VirtualSystemSettingData":
			_, _ = w.Write([]byte(dvdWiringSettingDataPull()))
		case action == "Pull" && res == "Msvm_ResourceAllocationSettingData":
			_, _ = w.Write([]byte(dvdWiringIDEControllerPull()))
		case strings.HasSuffix(action, "AddResourceSettings"):
			_, _ = w.Write([]byte(dvdWiringAddResponse()))
		case action == "Get" && res == "Msvm_ConcreteJob":
			_, _ = w.Write([]byte(dvdWiringJobCompleted()))
		default:
			t.Errorf("想定外のリクエスト: action=%q resource=%q", action, res)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	wsmanClient, err := hyperv.NewClient(srv.URL)
	if err != nil {
		srv.Close()
		t.Fatalf("hyperv.NewClient: %v", err)
	}
	return &ClientConfig{WsmanClient: wsmanClient}, &bodies, srv.Close
}

// TestCreateVmDvdDrive_Wiring は path の有無で送る embedded instance が変わることを検証する。
func TestCreateVmDvdDrive_Wiring(t *testing.T) {
	const iso = `H:\ISO\talos.iso`

	t.Run("ISO 指定: Storage の紐付けを送る", func(t *testing.T) {
		cc, bodies, done := dvdWiringServer(t)
		defer done()
		if err := cc.CreateVmDvdDrive(context.Background(), "vm1", 0, 0, iso, ""); err != nil {
			t.Fatalf("CreateVmDvdDrive: %v", err)
		}
		all := strings.Join(*bodies, "\n")
		if !strings.Contains(all, "Msvm_StorageAllocationSettingData") {
			t.Error("ISO 指定なのに Storage の紐付けを送っていない (空ドライブが作られ ISO が載らない)")
		}
		if !strings.Contains(all, iso) {
			t.Errorf("ISO パス %q を送っていない", iso)
		}
	})

	t.Run("path 空: Storage の紐付けを送らない", func(t *testing.T) {
		cc, bodies, done := dvdWiringServer(t)
		defer done()
		if err := cc.CreateVmDvdDrive(context.Background(), "vm1", 0, 0, "", ""); err != nil {
			t.Fatalf("CreateVmDvdDrive: %v", err)
		}
		all := strings.Join(*bodies, "\n")
		if strings.Contains(all, "Msvm_StorageAllocationSettingData") {
			t.Error("メディアなしなのに Storage の紐付けを送っている")
		}
		// Drive 自体は送っていること (何も送らないで成功したのでは意味がない)。
		if !strings.Contains(all, hyperv.ResourceSubTypeSyntheticDVDDrive) {
			t.Error("DVD Drive の追加を送っていない")
		}
	})
}

func dvdWiringEnum() string {
	return `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/EnumerateResponse</a:Action></s:Header>
  <s:Body><e:EnumerateResponse><e:EnumerationContext>ctx</e:EnumerationContext></e:EnumerateResponse></s:Body>
</s:Envelope>`
}

func dvdWiringComputerSystemPull() string {
	return `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration" xmlns:p="http://schemas.microsoft.com/wbem/wsman/1/wmi/root/virtualization/v2/Msvm_ComputerSystem">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/PullResponse</a:Action></s:Header>
  <s:Body><e:PullResponse><e:Items>
    <p:Msvm_ComputerSystem><p:Name>` + dvdWiringVMGUID + `</p:Name><p:ElementName>vm1</p:ElementName><p:EnabledState>3</p:EnabledState></p:Msvm_ComputerSystem>
  </e:Items><e:EndOfSequence/></e:PullResponse></s:Body>
</s:Envelope>`
}

// Gen1 (IDE) として返す。Gen2 だと ensureScsiController が走り検証対象が増える。
func dvdWiringSettingDataPull() string {
	return `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration" xmlns:p="http://schemas.microsoft.com/wbem/wsman/1/wmi/root/virtualization/v2/Msvm_VirtualSystemSettingData">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/PullResponse</a:Action></s:Header>
  <s:Body><e:PullResponse><e:Items>
    <p:Msvm_VirtualSystemSettingData>
      <p:InstanceID>Microsoft:` + dvdWiringVMGUID + `</p:InstanceID>
      <p:VirtualSystemIdentifier>` + dvdWiringVMGUID + `</p:VirtualSystemIdentifier>
      <p:VirtualSystemType>Microsoft:Hyper-V:System:Realized</p:VirtualSystemType>
      <p:VirtualSystemSubType>Microsoft:Hyper-V:SubType:1</p:VirtualSystemSubType>
      <p:ConfigurationID>` + dvdWiringVMGUID + `</p:ConfigurationID>
    </p:Msvm_VirtualSystemSettingData>
  </e:Items><e:EndOfSequence/></e:PullResponse></s:Body>
</s:Envelope>`
}

func dvdWiringIDEControllerPull() string {
	return `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration" xmlns:p="http://schemas.microsoft.com/wbem/wsman/1/wmi/root/virtualization/v2/Msvm_ResourceAllocationSettingData">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/PullResponse</a:Action></s:Header>
  <s:Body><e:PullResponse><e:Items>
    <p:Msvm_ResourceAllocationSettingData>
      <p:InstanceID>Microsoft:` + dvdWiringVMGUID + `\IDE-CTRL-0</p:InstanceID>
      <p:ResourceType>5</p:ResourceType>
      <p:ResourceSubType>Microsoft:Hyper-V:Emulated IDE Controller</p:ResourceSubType>
      <p:Address>0</p:Address>
    </p:Msvm_ResourceAllocationSettingData>
  </e:Items><e:EndOfSequence/></e:PullResponse></s:Body>
</s:Envelope>`
}

func dvdWiringAddResponse() string {
	return `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:w="http://schemas.dmtf.org/wbem/wsman/1/wsman.xsd" xmlns:p="http://schemas.microsoft.com/wbem/wsman/1/wmi/root/virtualization/v2/Msvm_VirtualSystemManagementService">
  <s:Header><a:Action>http://schemas.microsoft.com/wbem/wsman/1/wmi/root/virtualization/v2/Msvm_VirtualSystemManagementService/AddResourceSettingsResponse</a:Action></s:Header>
  <s:Body><p:AddResourceSettings_OUTPUT>
    <p:ResultingResourceSettings>
      <a:Address>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</a:Address>
      <a:ReferenceParameters>
        <w:ResourceURI>http://schemas.microsoft.com/wbem/wsman/1/wmi/root/virtualization/v2/Msvm_ResourceAllocationSettingData</w:ResourceURI>
        <w:SelectorSet><w:Selector Name="InstanceID">Microsoft:` + dvdWiringVMGUID + `\NEW-DVD-0</w:Selector></w:SelectorSet>
      </a:ReferenceParameters>
    </p:ResultingResourceSettings>
    <p:ReturnValue>0</p:ReturnValue>
  </p:AddResourceSettings_OUTPUT></s:Body>
</s:Envelope>`
}

func dvdWiringJobCompleted() string {
	return `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:p="http://schemas.microsoft.com/wbem/wsman/1/wmi/root/virtualization/v2/Msvm_ConcreteJob">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/transfer/GetResponse</a:Action></s:Header>
  <s:Body><p:Msvm_ConcreteJob><p:InstanceID>job-1</p:InstanceID><p:JobState>7</p:JobState></p:Msvm_ConcreteJob></s:Body>
</s:Envelope>`
}
