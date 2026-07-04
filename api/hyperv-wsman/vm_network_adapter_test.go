package hyperv_wsman

import (
	"reflect"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestClientConfig_ImplementsHypervVmNetworkAdapterClient は ClientConfig が
// api.HypervVmNetworkAdapterClient を実装することを検証する。
//
// CreateVmNetworkAdapter/GetVmNetworkAdapters/UpdateVmNetworkAdapter/DeleteVmNetworkAdapter/
// CreateOrUpdateVmNetworkAdapters は本パッケージでシャドウイングする。
// WaitForVmNetworkAdaptersIps は未実装で PowerShell にフォールバックする (意図的)。
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
	} {
		if _, ok := cType.MethodByName(methodName); !ok {
			t.Errorf("メソッド %s が hyperv-wsman で定義されていない (シャドウイングされない)", methodName)
		}
	}
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
