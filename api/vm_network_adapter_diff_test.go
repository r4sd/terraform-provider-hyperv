package api

import "testing"

// TestDiffSuppressVmStaticMacAddress は static_mac_address の diff 抑止を検証する。
//
// 🔴 **実機は MAC を大文字で保存して返す。** 2026-10-05 実機確認 (使い捨て VM):
//
//	経路   送信            読み戻し
//	CIM    00155d0a0b0c →  00155D0A0B0C
//	PS     00155d0a0b0c →  00155D0A0B0C
//
// つまり config を小文字で書くと state は大文字になる。厳密比較だと diff が残り続け、
// network_adaptors は hasChangesThatRequireVmToBeOff に含まれるので
// **apply のたびに VM が停止する** (#165)。
//
// 区切り文字の違い (コロン・ハイフン) は抑止しない。書き込み側の normalizeMac は
// 区切りを落とすが、それを抑止対象に含めると「本当に違う MAC」まで抑止しかねないので、
// 大小文字だけを無視する。実機で確認したのは大小文字の差だけ。
func TestDiffSuppressVmStaticMacAddress(t *testing.T) {
	tests := []struct {
		name string
		old  string // state (実機由来 = 大文字)
		new  string // config
		want bool   // true なら diff を抑止
	}{
		{"config 未設定なら常に抑止", "00155D0A0B0C", "", true},
		{"完全一致", "00155D0A0B0C", "00155D0A0B0C", true},
		{"config が小文字 (実機は大文字で返す)", "00155D0A0B0C", "00155d0a0b0c", true},
		{"config が混在", "00155D0A0B0C", "00155d0A0b0C", true},
		{"state が小文字・config が大文字", "00155d0a0b0c", "00155D0A0B0C", true},
		{"違う MAC は抑止しない", "00155D0A0B0C", "00155D0A0B0D", false},
		{"長さが違えば抑止しない", "00155D0A0B0C", "00155D0A0B0", false},
		{"区切り文字の違いは抑止しない", "00155D0A0B0C", "00-15-5D-0A-0B-0C", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DiffSuppressVmStaticMacAddress("static_mac_address", tt.old, tt.new, nil)
			if got != tt.want {
				t.Errorf("old=%q new=%q: got %v, want %v", tt.old, tt.new, got, tt.want)
			}
		})
	}
}
