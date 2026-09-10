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

// TestClientConfig_ImplementsHypervVmNetworkAdapterClient は ClientConfig が
// api.HypervVmNetworkAdapterClient を実装することを検証する。
//
// CreateVmNetworkAdapter/GetVmNetworkAdapters/UpdateVmNetworkAdapter/DeleteVmNetworkAdapter/
// CreateOrUpdateVmNetworkAdapters/WaitForVmNetworkAdaptersIps は本パッケージでシャドウイングする。
// WaitForVmNetworkAdaptersIps は全 NIC が wait_for_ips=false のとき PS を省き、それ以外は
// winrm 実装へ委譲する (#76)。
func TestClientConfig_ImplementsHypervVmNetworkAdapterClient(t *testing.T) {
	var c *ClientConfig
	var _ api.HypervVmNetworkAdapterClient = c // コンパイル時チェック

	cType := reflect.TypeOf((*ClientConfig)(nil))
	for _, methodName := range []string{
		"CreateVmNetworkAdapter",
		"GetVmNetworkAdapters",
		"UpdateVmNetworkAdapter",
		"DeleteVmNetworkAdapter",
		"CreateOrUpdateVmNetworkAdapters",
		"WaitForVmNetworkAdaptersIps",
	} {
		if _, ok := cType.MethodByName(methodName); !ok {
			t.Errorf("メソッド %s が hyperv-wsman で定義されていない (シャドウイングされない)", methodName)
		}
	}
}

// TestWaitForVmNetworkAdaptersIps_SkipsWhenAllFalse は #76 のスキップ判定を検証する。
//
// 全 NIC が wait_for_ips=false (または空リスト) なら PS を出さず nil を返す。1 つでも true が
// あれば埋め込んだ winrm 実装へ委譲する。委譲分岐では nil 埋め込みクライアントの参照で panic
// することを利用し、「スキップせず委譲した」ことを確定的に確認する。
func TestWaitForVmNetworkAdaptersIps_SkipsWhenAllFalse(t *testing.T) {
	ctx := context.Background()
	c := &ClientConfig{} // 埋め込み winrm も WsmanClient も nil

	t.Run("空リストはスキップ", func(t *testing.T) {
		if err := c.WaitForVmNetworkAdaptersIps(ctx, "vm", 0, 0, nil); err != nil {
			t.Errorf("空リストで PS を出さず nil を期待、got %v", err)
		}
	})

	t.Run("全 false はスキップ", func(t *testing.T) {
		waits := []api.VmNetworkAdapterWaitForIp{{WaitForIps: false}, {WaitForIps: false}}
		if err := c.WaitForVmNetworkAdaptersIps(ctx, "vm", 0, 0, waits); err != nil {
			t.Errorf("全 false で PS を出さず nil を期待、got %v", err)
		}
	})

	t.Run("1 つでも true なら winrm へ委譲する", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("wait_for_ips=true でスキップせず winrm 実装へ委譲することを期待 (nil クライアントで panic)")
			}
		}()
		waits := []api.VmNetworkAdapterWaitForIp{{WaitForIps: false}, {WaitForIps: true}}
		_ = c.WaitForVmNetworkAdaptersIps(ctx, "vm", 0, 0, waits)
	})
}

// TestUnsupportedNetworkAdapterOptions は既定 NIC は許可、未対応フィールドが既定外なら error。
func TestUnsupportedNetworkAdapterOptions(t *testing.T) {
	base := func() api.VmNetworkAdapter {
		a := defaultVmNetworkAdapter()
		a.Name = "eth0"
		a.SwitchName = "vSwitch"
		return a
	}

	t.Run("既定 + 名前/スイッチ/動的MACは許可", func(t *testing.T) {
		if err := unsupportedNetworkAdapterOptions(base()); err != nil {
			t.Errorf("基本 NIC でエラー: %v", err)
		}
	})
	t.Run("静的MACは許可", func(t *testing.T) {
		a := base()
		a.DynamicMacAddress = false
		a.StaticMacAddress = "00155D001122"
		if err := unsupportedNetworkAdapterOptions(a); err != nil {
			t.Errorf("静的MACでエラー: %v", err)
		}
	})

	cases := []struct {
		name  string
		apply func(*api.VmNetworkAdapter)
	}{
		{"management_os", func(a *api.VmNetworkAdapter) { a.ManagementOs = true }},
		{"is_legacy", func(a *api.VmNetworkAdapter) { a.IsLegacy = true }},
		{"mac_address_spoofing", func(a *api.VmNetworkAdapter) { a.MacAddressSpoofing = api.OnOffState_On }},
		{"port_mirroring", func(a *api.VmNetworkAdapter) { a.PortMirroring = api.PortMirroring(2) }},
		{"vmq_weight", func(a *api.VmNetworkAdapter) { a.VmqWeight = 0 }},
		{"iov_queue_pairs", func(a *api.VmNetworkAdapter) { a.IovQueuePairsRequested = 4 }},
		{"iov_weight", func(a *api.VmNetworkAdapter) { a.IovWeight = 0 }},
		{"maximum_bandwidth", func(a *api.VmNetworkAdapter) { a.MaximumBandwidth = 1000 }},
		{"allow_teaming", func(a *api.VmNetworkAdapter) { a.AllowTeaming = api.OnOffState_Off }},
		{"vrss_enabled", func(a *api.VmNetworkAdapter) { a.VrssEnabled = false }},
		{"vmmq_queue_pairs", func(a *api.VmNetworkAdapter) { a.VmmqQueuePairs = 8 }},
		{"vlan_access", func(a *api.VmNetworkAdapter) { a.VlanAccess = true }},
		{"mandatory_feature_id", func(a *api.VmNetworkAdapter) { a.MandatoryFeatureId = []string{"x"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := base()
			tc.apply(&a)
			if err := unsupportedNetworkAdapterOptions(a); err == nil {
				t.Errorf("%s は未対応なのでエラーになるべき", tc.name)
			}
		})
	}
}

// TestMapNetworkAdapterRefs は port→allocation→switch の逆引き結合を検証する。
func TestMapNetworkAdapterRefs(t *testing.T) {
	vm := "11111111-aaaa-bbbb-cccc-000000000001"
	portID := `Microsoft:` + vm + `\NIC-001`
	switchGUID := "aaaaaaaa-1111-2222-3333-444444444444"

	ports := []*hyperv.Msvm_SyntheticEthernetPortSettingData{
		{InstanceID: portID, ElementName: "eth0", StaticMacAddress: false, Address: "00155D012345"},
	}
	allocs := []*hyperv.Msvm_EthernetPortAllocationSettingData{
		{Parent: portID, HostResource: `Microsoft:VirtualSwitch\` + switchGUID},
	}
	switches := []*hyperv.Msvm_VirtualEthernetSwitch{
		{Name: switchGUID, ElementName: "vSwitch-Lab"},
	}

	got := mapNetworkAdapterRefs(vm, ports, allocs, switches)
	if len(got) != 1 {
		t.Fatalf("len: got %d, want 1", len(got))
	}
	a := got[0].adapter
	if a.Name != "eth0" {
		t.Errorf("Name: %q", a.Name)
	}
	if a.SwitchName != "vSwitch-Lab" {
		t.Errorf("SwitchName: got %q, want vSwitch-Lab (allocation→switch 解決)", a.SwitchName)
	}
	if !a.DynamicMacAddress {
		t.Errorf("DynamicMacAddress should be true (StaticMacAddress=false)")
	}
	if a.Index != 0 {
		t.Errorf("Index: %d", a.Index)
	}
	if got[0].portInstanceID != portID {
		t.Errorf("portInstanceID: %q", got[0].portInstanceID)
	}
	// 未対応フィールドが既定値で埋まっていること (差分防止)。
	if a.VmqWeight != 100 || a.AllowTeaming != api.OnOffState_On || !a.VrssEnabled {
		t.Errorf("既定値が未反映: VmqWeight=%d AllowTeaming=%d Vrss=%v", a.VmqWeight, a.AllowTeaming, a.VrssEnabled)
	}
}

func TestResolveSwitchName(t *testing.T) {
	portID := `Microsoft:vm\NIC-1`
	guid := "gg-1111"
	allocs := []*hyperv.Msvm_EthernetPortAllocationSettingData{
		{Parent: portID, HostResource: `Microsoft:VirtualSwitch\` + guid},
	}
	switches := []*hyperv.Msvm_VirtualEthernetSwitch{{Name: guid, ElementName: "Lab"}}

	if got := resolveSwitchName(portID, allocs, switches); got != "Lab" {
		t.Errorf("解決: got %q, want Lab", got)
	}
	// 接続なし (allocation が別 NIC) → 空。
	if got := resolveSwitchName(`Microsoft:vm\NIC-OTHER`, allocs, switches); got != "" {
		t.Errorf("未接続は空のはず: got %q", got)
	}
}

func TestPlanNetworkAdapterReconcile(t *testing.T) {
	mkRef := func(id, name, sw string) networkAdapterRef {
		a := defaultVmNetworkAdapter()
		a.Name, a.SwitchName = name, sw
		return networkAdapterRef{adapter: a, portInstanceID: id}
	}
	mkD := func(name, sw string) api.VmNetworkAdapter {
		a := defaultVmNetworkAdapter()
		a.Name, a.SwitchName = name, sw
		return a
	}

	t.Run("変化なし", func(t *testing.T) {
		cur := []networkAdapterRef{mkRef("p1", "eth0", "vSwitch")}
		des := []api.VmNetworkAdapter{mkD("eth0", "vSwitch")}
		rm, add := planNetworkAdapterReconcile(cur, des)
		if len(rm) != 0 || len(add) != 0 {
			t.Errorf("変化なしのはず: rm=%v add=%v", rm, add)
		}
	})
	t.Run("スイッチ付け替え = remove+add", func(t *testing.T) {
		cur := []networkAdapterRef{mkRef("p1", "eth0", "old")}
		des := []api.VmNetworkAdapter{mkD("eth0", "new")}
		rm, add := planNetworkAdapterReconcile(cur, des)
		if len(rm) != 1 || rm[0] != "p1" {
			t.Errorf("remove: got %v, want [p1]", rm)
		}
		if len(add) != 1 || add[0].SwitchName != "new" {
			t.Errorf("add: got %v", add)
		}
	})
	// H1: 同一キー (同名+同スイッチ+dynamic) が複数あっても multiset で過不足を計算する。
	t.Run("同一キー1本→2本 = add1", func(t *testing.T) {
		cur := []networkAdapterRef{mkRef("p1", "eth0", "vSwitch")}
		des := []api.VmNetworkAdapter{mkD("eth0", "vSwitch"), mkD("eth0", "vSwitch")}
		rm, add := planNetworkAdapterReconcile(cur, des)
		if len(rm) != 0 || len(add) != 1 {
			t.Errorf("1本→2本は add1 のはず: rm=%v add=%d", rm, len(add))
		}
	})
	t.Run("同一キー2本→1本 = remove1", func(t *testing.T) {
		cur := []networkAdapterRef{mkRef("p1", "eth0", "vSwitch"), mkRef("p2", "eth0", "vSwitch")}
		des := []api.VmNetworkAdapter{mkD("eth0", "vSwitch")}
		rm, add := planNetworkAdapterReconcile(cur, des)
		if len(rm) != 1 || len(add) != 0 {
			t.Errorf("2本→1本は remove1 のはず: rm=%d add=%v", len(rm), add)
		}
	})
}

// TestNormalizeMac_KeyEquivalence は MAC 区切り形式の違いが同一キーになることを検証する (M2)。
func TestNormalizeMac_KeyEquivalence(t *testing.T) {
	mk := func(mac string) api.VmNetworkAdapter {
		a := defaultVmNetworkAdapter()
		a.Name, a.SwitchName = "eth0", "vSwitch"
		a.DynamicMacAddress = false
		a.StaticMacAddress = mac
		return a
	}
	formats := []string{"00:15:5D:00:11:22", "00-15-5D-00-11-22", "00155D001122", "00155d001122"}
	want := networkAdapterKey(mk(formats[0]))
	for _, f := range formats[1:] {
		if got := networkAdapterKey(mk(f)); got != want {
			t.Errorf("MAC %q のキーが不一致: got %q want %q", f, got, want)
		}
	}
}

// TestMapNetworkAdapterRefs_ElementNameOrder は NIC が ElementName 順に並ぶことを検証する (H3)。
func TestMapNetworkAdapterRefs_ElementNameOrder(t *testing.T) {
	vm := "vm1"
	// InstanceID 順では zzz が aaa より後だが、ElementName 順で並べたい。
	ports := []*hyperv.Msvm_SyntheticEthernetPortSettingData{
		{InstanceID: `Microsoft:` + vm + `\aaa`, ElementName: "zeta"},
		{InstanceID: `Microsoft:` + vm + `\zzz`, ElementName: "alpha"},
	}
	got := mapNetworkAdapterRefs(vm, ports, nil, nil)
	if len(got) != 2 {
		t.Fatalf("len: got %d, want 2", len(got))
	}
	if got[0].adapter.Name != "alpha" || got[1].adapter.Name != "zeta" {
		t.Errorf("ElementName 順のはず: got [%s, %s]", got[0].adapter.Name, got[1].adapter.Name)
	}
	if got[0].adapter.Index != 0 || got[1].adapter.Index != 1 {
		t.Errorf("Index が順序どおりでない")
	}
}

// TestSortAdaptersByConfigOrder は read 結果を config の並びへ寄せることを検証する。
//
// mapNetworkAdapterRefs は ElementName の辞書順で返すが、network_adaptors は
// schema.TypeList で **位置**で差分を取る。config の並びが辞書順でないと
// 恒常 diff になり、planNetworkAdapterReconcile は multiset 差分なので no-op となり、
// apply のたびに VM が停止して何も変わらないループになる (#135)。
//
// PowerShell 経路は作成順 (= config 順) を返すため、CIM 経路でだけ発現する差だった。
func TestSortAdaptersByConfigOrder(t *testing.T) {
	mk := func(names ...string) []api.VmNetworkAdapter {
		out := make([]api.VmNetworkAdapter, 0, len(names))
		for _, n := range names {
			out = append(out, api.VmNetworkAdapter{Name: n})
		}
		return out
	}
	cfg := func(names ...string) []api.VmNetworkAdapterWaitForIp {
		out := make([]api.VmNetworkAdapterWaitForIp, 0, len(names))
		for _, n := range names {
			out = append(out, api.VmNetworkAdapterWaitForIp{Name: n})
		}
		return out
	}
	names := func(a []api.VmNetworkAdapter) []string {
		out := make([]string, 0, len(a))
		for _, x := range a {
			out = append(out, x.Name)
		}
		return out
	}
	eq := func(t *testing.T, got []api.VmNetworkAdapter, want ...string) {
		t.Helper()
		g := names(got)
		if len(g) != len(want) {
			t.Fatalf("got %v, want %v", g, want)
		}
		for i := range want {
			if g[i] != want[i] {
				t.Fatalf("got %v, want %v", g, want)
			}
		}
	}

	t.Run("config 順へ並べ替える", func(t *testing.T) {
		// read は辞書順 [External, Internal]、config は [Internal, External]。
		got := sortAdaptersByConfigOrder(mk("External", "Internal"), cfg("Internal", "External"))
		eq(t, got, "Internal", "External")
	})

	t.Run("config に無い NIC は末尾に辞書順で残す", func(t *testing.T) {
		// 外部で足された NIC を落とすと state から消えてしまう。
		got := sortAdaptersByConfigOrder(mk("Alpha", "External", "Internal"), cfg("Internal"))
		eq(t, got, "Internal", "Alpha", "External")
	})

	t.Run("config が空なら元の順序を保つ", func(t *testing.T) {
		got := sortAdaptersByConfigOrder(mk("External", "Internal"), nil)
		eq(t, got, "External", "Internal")
	})

	t.Run("同名 NIC が複数あっても落とさない", func(t *testing.T) {
		// 同名 NIC は本経路では非対応だが、read が数を減らすと state が壊れる。
		got := sortAdaptersByConfigOrder(mk("dup", "dup", "other"), cfg("other", "dup"))
		if len(got) != 3 {
			t.Fatalf("NIC が %d 件に減っている: %v", len(got), names(got))
		}
		if got[0].Name != "other" {
			t.Errorf("config 先頭が反映されていない: %v", names(got))
		}
	})
}

// TestGetVmNetworkAdaptersUsesConfigOrder は GetVmNetworkAdapters が config 順ソートを
// **実際に呼んでいる**ことを検証する。
//
// 純関数のテストだけでは配線を見ないため、ソートの呼び出しを外す変異が素通りしていた
// (#145 で同型の穴を踏んだのと同じ)。
func TestGetVmNetworkAdaptersUsesConfigOrder(t *testing.T) {
	const vmGUID = "11111111-aaaa-bbbb-cccc-000000000001"
	const vmName = "vm-1"

	enumXML := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/EnumerateResponse</a:Action></s:Header>
  <s:Body><e:EnumerateResponse><e:EnumerationContext>ctx</e:EnumerationContext></e:EnumerateResponse></s:Body>
</s:Envelope>`
	csPull := fmt.Sprintf(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration" xmlns:p="http://schemas.microsoft.com/wbem/wsman/1/wmi/root/virtualization/v2/Msvm_ComputerSystem">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/PullResponse</a:Action></s:Header>
  <s:Body><e:PullResponse><e:Items>
    <p:Msvm_ComputerSystem><p:Name>%s</p:Name><p:ElementName>%s</p:ElementName><p:EnabledState>3</p:EnabledState></p:Msvm_ComputerSystem>
  </e:Items><e:EndOfSequence/></e:PullResponse></s:Body>
</s:Envelope>`, vmGUID, vmName)
	// 辞書順で External → Internal を返す。config は逆順。
	portPull := fmt.Sprintf(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration" xmlns:p="http://schemas.microsoft.com/wbem/wsman/1/wmi/root/virtualization/v2/Msvm_SyntheticEthernetPortSettingData">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/PullResponse</a:Action></s:Header>
  <s:Body><e:PullResponse><e:Items>
    <p:Msvm_SyntheticEthernetPortSettingData><p:InstanceID>Microsoft:%s\\A</p:InstanceID><p:ElementName>External</p:ElementName></p:Msvm_SyntheticEthernetPortSettingData>
    <p:Msvm_SyntheticEthernetPortSettingData><p:InstanceID>Microsoft:%s\\B</p:InstanceID><p:ElementName>Internal</p:ElementName></p:Msvm_SyntheticEthernetPortSettingData>
  </e:Items><e:EndOfSequence/></e:PullResponse></s:Body>
</s:Envelope>`, vmGUID, vmGUID)
	emptyPull := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/PullResponse</a:Action></s:Header>
  <s:Body><e:PullResponse><e:Items/><e:EndOfSequence/></e:PullResponse></s:Body>
</s:Envelope>`

	responses := []string{
		enumXML, csPull, // resolveVMGUID
		enumXML, portPull, // ports
		enumXML, emptyPull, // allocations
		enumXML, emptyPull, // switches
	}
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n >= len(responses) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
		_, _ = w.Write([]byte(responses[n]))
		n++
	}))
	defer srv.Close()

	wsmanClient, err := hyperv.NewClient(srv.URL)
	if err != nil {
		t.Fatalf("hyperv.NewClient: %v", err)
	}
	c := &ClientConfig{WsmanClient: wsmanClient}

	got, err := c.GetVmNetworkAdapters(context.Background(), vmName,
		[]api.VmNetworkAdapterWaitForIp{{Name: "Internal"}, {Name: "External"}})
	if err != nil {
		t.Fatalf("GetVmNetworkAdapters: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d 件, want 2", len(got))
	}
	if got[0].Name != "Internal" || got[1].Name != "External" {
		t.Errorf("config 順になっていない: [%s %s], want [Internal External]。"+
			"ソートが呼ばれていないと辞書順のままになる (#135)", got[0].Name, got[1].Name)
	}
}
