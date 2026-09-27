package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

// TestValidateNetworkAdapterMacOptions は dynamic_mac_address と static_mac_address の
// 矛盾する組み合わせを **plan の時点で**落とすことを検証する (#160)。
//
// なぜ plan で落とすのか — apply 時(API 層)で落とすと状態が壊れたまま止まる:
//
//	Create: CreateVm 成功 → NIC で error → d.SetId に到達しない → **VM が孤児になる**
//	Update: network_adaptors の変更は VM の停止を伴う → NIC で error
//	        → 起動処理に到達しない → **VM が Off のまま残る**
//
// どちらも「恒常 diff で毎回 VM が停止する」より悪い。CustomizeDiff なら実機に
// 触る前に止まる。
//
// なぜ両経路とも落とすのか — 実機で確かめた挙動 (2026-09-27):
//
//	CIM 経路: 静的 MAC を黙って捨てる  → static_mac_address 側で恒常 diff
//	PS 経路:  静的 MAC を適用し DynamicMacAddressEnabled=False にする
//	                                   → dynamic_mac_address 側で恒常 diff
//
// **どちらの経路でも整合しない config** なので、経路を問わず落とす。
func TestValidateNetworkAdapterMacOptions(t *testing.T) {
	adapter := func(dynamic bool, static string) map[string]interface{} {
		return map[string]interface{}{
			"name":                "eth0",
			"dynamic_mac_address": dynamic,
			"static_mac_address":  static,
		}
	}

	t.Run("dynamic=true + static 指定 → 拒否", func(t *testing.T) {
		err := validateNetworkAdapterMacOptions([]interface{}{adapter(true, "00155D001122")})
		if err == nil {
			t.Fatal("拒否されるべき組み合わせが通った (#160)")
		}
		for _, want := range []string{"eth0", "dynamic_mac_address", "static_mac_address", "false"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("エラー文に %q が無い: %v", want, err)
			}
		}
		// 経路を問わず落とすので「PS 経路を使え」と案内してはいけない。
		if strings.Contains(err.Error(), "HYPERV_USE_WSMAN を外") {
			t.Errorf("誤った案内(PS 経路に逃がす)が入っている: %v", err)
		}
	})

	t.Run("dynamic=false + static 指定 → 許可", func(t *testing.T) {
		if err := validateNetworkAdapterMacOptions([]interface{}{adapter(false, "00155D001122")}); err != nil {
			t.Errorf("静的 MAC の正しい指定でエラー: %v", err)
		}
	})

	t.Run("dynamic=true + static 空 → 許可", func(t *testing.T) {
		if err := validateNetworkAdapterMacOptions([]interface{}{adapter(true, "")}); err != nil {
			t.Errorf("動的 MAC の既定でエラー: %v", err)
		}
	})

	t.Run("複数 NIC: 2 枚目が矛盾 → どの NIC か分かる", func(t *testing.T) {
		list := []interface{}{adapter(false, "00155D001122")}
		bad := adapter(true, "00155D003344")
		bad["name"] = "eth1"
		list = append(list, bad)

		err := validateNetworkAdapterMacOptions(list)
		if err == nil {
			t.Fatal("2 枚目の矛盾が見逃された")
		}
		if !strings.Contains(err.Error(), "eth1") {
			t.Errorf("どの NIC か分からないエラー文: %v", err)
		}
	})

	t.Run("NIC 無し → 許可", func(t *testing.T) {
		if err := validateNetworkAdapterMacOptions(nil); err != nil {
			t.Errorf("NIC 未指定でエラー: %v", err)
		}
	})
}

// TestCustomizeDiff_RejectsConflictingMac は **CustomizeDiff に配線されている**ことを固定する。
//
// 条件だけ実装して配線を忘れる型を防ぐ。配線を外しても純関数のテストは通るので、
// ここで CustomizeDiff 本体を呼ぶ。plan で止まることがこの PR の要点なので、
// ここが無防備だと修正の意味が失われる。
func TestCustomizeDiff_RejectsConflictingMac(t *testing.T) {
	r := resourceHyperVMachineInstance()

	t.Run("矛盾する指定は plan で落ちる", func(t *testing.T) {
		if _, err := r.Diff(context.Background(), nil, terraform.NewResourceConfigRaw(map[string]interface{}{
			"name":       "vm1",
			"generation": 2,
			"network_adaptors": []interface{}{map[string]interface{}{
				"name":                "eth0",
				"dynamic_mac_address": true,
				"static_mac_address":  "00155D001122",
			}},
		}), nil); err == nil {
			t.Fatal("CustomizeDiff が矛盾する MAC 指定を通した。配線されていない (#160)")
		} else if !strings.Contains(err.Error(), "dynamic_mac_address") {
			t.Errorf("別の理由で落ちている: %v", err)
		}
	})

	t.Run("正しい指定は plan を通る", func(t *testing.T) {
		if _, err := r.Diff(context.Background(), nil, terraform.NewResourceConfigRaw(map[string]interface{}{
			"name":       "vm1",
			"generation": 2,
			"network_adaptors": []interface{}{map[string]interface{}{
				"name":                "eth0",
				"dynamic_mac_address": false,
				"static_mac_address":  "00155D001122",
			}},
		}), nil); err != nil {
			t.Errorf("正しい指定で plan が落ちた: %v", err)
		}
	})

}
