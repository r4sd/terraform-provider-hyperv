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
	"github.com/taliesins/terraform-provider-hyperv/api"
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
			// ListIDEControllers / ListDvdDrives / ListSCSIControllers が同じクラスを引く。
			// go-wsman 側で ResourceSubType によるフィルタがかかるので、IDE コントローラ 1 件を
			// 返せば「IDE は 1 本、DVD と SCSI は 0 件」として解釈される。
			_, _ = w.Write([]byte(dvdWiringIDEControllerPull()))
		case action == "Pull" && res == "Msvm_StorageAllocationSettingData":
			// 既存のメディアは無し (ListAttachedStorage が空を返す)。
			_, _ = w.Write([]byte(dvdWiringEmptyStoragePull()))
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

// TestCreateOrUpdateVmDvdDrives_ResolveOnce は複数 attach でも VM GUID / 世代の解決が
// **1 回で済む**ことを検証する (#68 項目 1)。
//
// 以前は attach ごとに公開 CreateVmDvdDrive を呼んでいたため、
// resolveVMGUID (Msvm_ComputerSystem の Enumerate+Pull) と
// vmIsGen2 (Msvm_VirtualSystemSettingData の Enumerate+Pull) が N 回走っていた。
//
// 回数で見る。実装を見るのではなく**送ったリクエストの数**を数えることで、
// 「解決結果を引き回している」ことを外から固定できる。
func TestCreateOrUpdateVmDvdDrives_ResolveOnce(t *testing.T) {
	cc, bodies, done := dvdWiringServer(t)
	defer done()

	// 2 本の ISO を別 location に attach する。
	err := cc.CreateOrUpdateVmDvdDrives(context.Background(), "vm1", []api.VmDvdDrive{
		{ControllerNumber: 0, ControllerLocation: 0, Path: `H:\ISO\a.iso`},
		{ControllerNumber: 0, ControllerLocation: 1, Path: `H:\ISO\b.iso`},
	})
	if err != nil {
		t.Fatalf("CreateOrUpdateVmDvdDrives: %v", err)
	}

	// Msvm_ComputerSystem の Pull = resolveVMGUID の回数。
	// getDvdDriveRefs で 1 回 + attach 側で 1 回 = 2 回。attach ごとに増えないこと。
	// (集約前は attach 2 本で 3 回だった)
	const wantCSPulls = 2
	if got := dvdCountPulls(*bodies, "Msvm_ComputerSystem"); got != wantCSPulls {
		t.Errorf("resolveVMGUID 由来の Pull が %d 回 (want %d)。attach ごとに再解決していないか", got, wantCSPulls)
	}

	// Msvm_VirtualSystemSettingData の Pull は 2 つの出どころが混ざる:
	//
	//	vmIsGen2                      1 回 (VM ごと。集約前は attach ごとに 1 回)
	//	go-wsman の AddResourceSettings 4 回 (attach ごとに drive + storage の 2 回。provider からは減らせない)
	//
	// 合計 5 回。vmIsGen2 が attach ごとに戻ると 6 回になる。
	// **go-wsman 内部の分と分離できないので、合計を固定して退行を見る。**
	const wantSDPulls = 5
	if got := dvdCountPulls(*bodies, "Msvm_VirtualSystemSettingData"); got != wantSDPulls {
		t.Errorf("Msvm_VirtualSystemSettingData の Pull が %d 回 (want %d = vmIsGen2 1 + AddResourceSettings 4)。"+
			"6 回なら vmIsGen2 が attach ごとに走っている", got, wantSDPulls)
	}
	// 2 本とも attach されていること (回数だけ減って中身が欠けていないか)。
	all := strings.Join(*bodies, "\n")
	for _, iso := range []string{`H:\ISO\a.iso`, `H:\ISO\b.iso`} {
		if !strings.Contains(all, iso) {
			t.Errorf("%s を attach していない", iso)
		}
	}
}

// dvdCountPulls は指定クラスの Pull リクエスト数を数える。
func dvdCountPulls(bodies []string, class string) int {
	n := 0
	for _, b := range bodies {
		act, res := "", ""
		if m := dvdActionRe.FindStringSubmatch(b); m != nil {
			act = dvdLastSeg(m[1])
		}
		if m := dvdResURIRe.FindStringSubmatch(b); m != nil {
			res = dvdLastSeg(m[1])
		}
		if act == "Pull" && res == class {
			n++
		}
	}
	return n
}

func dvdWiringEmptyStoragePull() string {
	return `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/PullResponse</a:Action></s:Header>
  <s:Body><e:PullResponse><e:Items/><e:EndOfSequence/></e:PullResponse></s:Body>
</s:Envelope>`
}

// TestCreateOrUpdateVmHardDiskDrives_ResolveOnce は disk 側でも VM GUID の解決が
// attach ごとに増えないことを検証する (#68 項目 1)。
//
// DVD と同じ dispatcher を使う。disk の attach は AttachVHD (drive + storage の
// AddResourceSettings) なので、Msvm_VirtualSystemSettingData の Pull は
// go-wsman 内部の分だけになる (disk には vmIsGen2 相当が無い)。
func TestCreateOrUpdateVmHardDiskDrives_ResolveOnce(t *testing.T) {
	cc, bodies, done := dvdWiringServer(t)
	defer done()

	err := cc.CreateOrUpdateVmHardDiskDrives(context.Background(), "vm1", []api.VmHardDiskDrive{
		// DiskNumber はゼロ値ではなく「未指定」のセンチネルを渡す
		// (ゼロ値だと unsupportedHardDiskOptions が パススルー物理ディスク指定として弾く)。
		{ControllerType: api.ControllerType_Ide, ControllerNumber: 0, ControllerLocation: 0,
			Path: `D:\VMs\a.vhdx`, DiskNumber: hardDiskDiskNumberUnset},
		{ControllerType: api.ControllerType_Ide, ControllerNumber: 0, ControllerLocation: 1,
			Path: `D:\VMs\b.vhdx`, DiskNumber: hardDiskDiskNumberUnset},
	})
	if err != nil {
		t.Fatalf("CreateOrUpdateVmHardDiskDrives: %v", err)
	}

	// getHardDiskDriveRefs で 1 回 + attach 側で 1 回 = 2 回。attach ごとに増えないこと。
	// (集約前は attach 2 本で 3 回)
	const wantCSPulls = 2
	if got := dvdCountPulls(*bodies, "Msvm_ComputerSystem"); got != wantCSPulls {
		t.Errorf("resolveVMGUID 由来の Pull が %d 回 (want %d)。attach ごとに再解決していないか", got, wantCSPulls)
	}
	// 2 本とも attach されていること。
	all := strings.Join(*bodies, "\n")
	for _, vhd := range []string{`D:\VMs\a.vhdx`, `D:\VMs\b.vhdx`} {
		if !strings.Contains(all, vhd) {
			t.Errorf("%s を attach していない", vhd)
		}
	}
}

// TestCreateOrUpdateVmHardDiskDrives_RejectsBadControllerTypeBeforeDetach は
// 不正な controller_type が **detach より前に** 弾かれることを検証する。
//
// attach 側で初めて弾くと、先行する detach だけが実機に適用されて部分適用になる。
func TestCreateOrUpdateVmHardDiskDrives_RejectsBadControllerTypeBeforeDetach(t *testing.T) {
	cc, bodies, done := dvdWiringServer(t)
	defer done()

	err := cc.CreateOrUpdateVmHardDiskDrives(context.Background(), "vm1", []api.VmHardDiskDrive{
		{ControllerType: api.ControllerType(99), ControllerNumber: 0, ControllerLocation: 0,
			Path: `D:\VMs\a.vhdx`, DiskNumber: hardDiskDiskNumberUnset},
	})
	if err == nil {
		t.Fatal("不正な controller_type がエラーにならない")
	}
	if !strings.Contains(err.Error(), "unsupported controller type") {
		t.Errorf("controller_type が原因と分かるエラーでない: %v", err)
	}
	// **1 件もリクエストを送っていないこと。** 送っていたら detach が走る余地がある。
	if len(*bodies) != 0 {
		t.Errorf("検証前にリクエストを %d 件送っている (部分適用の余地がある)", len(*bodies))
	}
}
