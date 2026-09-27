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
// **壊れ方が違うだけで両経路とも壊れている。** どちらに委譲しても直らない。
//
// ⚠️ ここは**二重防御**。一次の防御は provider の CustomizeDiff
// (validateNetworkAdapterMacOptions) にあり、通常の apply はそこで止まる。
// この層で落ちると Create では VM が孤児に、Update では VM が Off のまま残るので、
// ここを一次防御にしてはいけない。
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
		// PS 経路へ逃がす案内をしないことも固定する(実機では PS でも恒常 diff になる)。
		if strings.Contains(err.Error(), "HYPERV_USE_WSMAN を外") {
			t.Errorf("誤った案内(PS 経路に逃がす)が入っている: %v", err)
		}
		for _, want := range []string{"dynamic_mac_address", "static_mac_address", "PowerShell"} {
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

// TestConflictingMacAddressOptions_CheckedBeforeUnsupported は、未対応オプションと
// 矛盾する MAC 指定を**同時に**与えたとき、返るのが MAC 側のメッセージであることを固定する。
//
// 案内先が違うため、どちらが返るかに意味がある。未対応オプションのメッセージは
// 「PowerShell 経路を使え」と案内するが、MAC の矛盾は PS 経路でも直らない。
//
// ⚠️ **このテストは呼び出し順序を守っていない。** 未対応判定は add() でスライスに
// 貯めて最後に 1 回返す形なので、conflictingMacAddressOptions を前に置いても後ろに
// 置いても結果は同じ(変異で確認済み)。守れているのは「MAC 側が返る」という
// 結果だけで、実装を add() ベースから早期 return に変えたら意味が変わる。
func TestConflictingMacAddressOptions_CheckedBeforeUnsupported(t *testing.T) {
	a := defaultVmNetworkAdapter()
	a.VmName = "vm1"
	a.Name = "eth0"
	a.SwitchName = "vSwitch"
	a.DynamicMacAddress = true
	a.StaticMacAddress = "00155D001122"
	a.MacAddressSpoofing = api.OnOffState_On // 未対応オプションも同時に指定

	err := unsupportedNetworkAdapterOptions(a)
	if err == nil {
		t.Fatal("どちらの理由でも落ちるべき入力が通った")
	}
	if !strings.Contains(err.Error(), "dynamic_mac_address") {
		t.Errorf("MAC の矛盾より先に未対応オプションのメッセージが返っている。"+
			"利用者が『PS 経路を使え』と誤って案内される: %v", err)
	}
}
