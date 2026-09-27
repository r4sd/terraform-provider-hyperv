package hyperv_wsman

import (
	"strings"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestConflictingMacAddressOptions は (dynamic_mac_address=true かつ static_mac_address≠"")
// を拒否することを検証する (#160)。
//
// なぜ拒否するのか — 実機で確かめた両経路の挙動 (2026-09-27):
//
//	CIM 経路: 静的 MAC を**黙って捨てる**  → static_mac_address 側で恒常 diff
//	PS 経路:  静的 MAC を**適用し** DynamicMacAddressEnabled=False にする
//	                                      → dynamic_mac_address 側で恒常 diff
//
// **壊れ方が違うだけで両経路とも壊れている。** どちらに委譲しても直らないので、
// 受け取った時点で落とす。apply のたびに VM が停止する恒常 diff を作るより、
// plan で止める方がよい。
func TestConflictingMacAddressOptions(t *testing.T) {
	base := func() api.VmNetworkAdapter {
		a := defaultVmNetworkAdapter()
		a.VmName = "vm1"
		a.Name = "eth0"
		a.SwitchName = "vSwitch"
		return a
	}

	t.Run("dynamic=true + static 指定 → 拒否", func(t *testing.T) {
		a := base()
		a.DynamicMacAddress = true
		a.StaticMacAddress = "00155D001122"
		err := conflictingMacAddressOptions(a)
		if err == nil {
			t.Fatal("拒否されるべき組み合わせが通った (#160)")
		}
		// 利用者が何を直せばよいか分かる文面か。
		for _, want := range []string{"dynamic_mac_address", "static_mac_address"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("エラー文に %q が無い: %v", want, err)
			}
		}
	})

	t.Run("dynamic=false + static 指定 → 許可", func(t *testing.T) {
		a := base()
		a.DynamicMacAddress = false
		a.StaticMacAddress = "00155D001122"
		if err := conflictingMacAddressOptions(a); err != nil {
			t.Errorf("静的 MAC の正しい指定でエラー: %v", err)
		}
	})

	t.Run("dynamic=true + static 空 → 許可", func(t *testing.T) {
		a := base()
		a.DynamicMacAddress = true
		a.StaticMacAddress = ""
		if err := conflictingMacAddressOptions(a); err != nil {
			t.Errorf("動的 MAC の既定でエラー: %v", err)
		}
	})

	t.Run("dynamic=false + static 空 → 許可", func(t *testing.T) {
		// PS 経路の既定挙動に任せる。ここで落とす理由は無い。
		a := base()
		a.DynamicMacAddress = false
		a.StaticMacAddress = ""
		if err := conflictingMacAddressOptions(a); err != nil {
			t.Errorf("両方未指定でエラー: %v", err)
		}
	})
}

// TestConflictingMacAddressOptions_WiredIntoValidation は
// unsupportedNetworkAdapterOptions からも弾かれることを固定する。
//
// 条件だけ実装して配線を忘れる型を防ぐ(実際に別 PR でやっている)。
func TestConflictingMacAddressOptions_WiredIntoValidation(t *testing.T) {
	a := defaultVmNetworkAdapter()
	a.VmName = "vm1"
	a.Name = "eth0"
	a.SwitchName = "vSwitch"
	a.DynamicMacAddress = true
	a.StaticMacAddress = "00155D001122"

	if err := unsupportedNetworkAdapterOptions(a); err == nil {
		t.Fatal("unsupportedNetworkAdapterOptions が矛盾する MAC 指定を通した。配線されていない (#160)")
	}
}
