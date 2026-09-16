package api

import "testing"

// TestCanonicalSecureBootTemplate は 3 つの既知テンプレートについて、シンボリック名・GUID・
// 大文字小文字の揺れがすべて同じ正規名へ解決され、未知の値は known=false になることを検証する。
func TestCanonicalSecureBootTemplate(t *testing.T) {
	tests := []struct {
		input     string
		want      string
		wantKnown bool
	}{
		{"MicrosoftWindows", "MicrosoftWindows", true},
		{"microsoftwindows", "MicrosoftWindows", true},
		{"MICROSOFTWINDOWS", "MicrosoftWindows", true},
		{"1734C6E8-3154-4DDA-BA5F-A874CC483422", "MicrosoftWindows", true},
		{"1734c6e8-3154-4dda-ba5f-a874cc483422", "MicrosoftWindows", true},
		{"MicrosoftUEFICertificateAuthority", "MicrosoftUEFICertificateAuthority", true},
		{"microsoftueficertificateauthority", "MicrosoftUEFICertificateAuthority", true},
		{"OpenSourceShieldedVM", "OpenSourceShieldedVM", true},
		// 未知のシンボル名・未知の GUID・空文字は正規化できない。
		{"SomeFutureTemplate", "", false},
		{"11111111-2222-3333-4444-555555555555", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, gotKnown := CanonicalSecureBootTemplate(tt.input)
		if got != tt.want || gotKnown != tt.wantKnown {
			t.Errorf("CanonicalSecureBootTemplate(%q): got (%q, %v), want (%q, %v)", tt.input, got, gotKnown, tt.want, tt.wantKnown)
		}
	}
}

// TestDiffSuppressSecureBootTemplate は「同じテンプレートを指す別表記」だけを抑止し、
// 未知の値は差分として見せ続けることを検証する。
//
// 抑止しないと Read が必ず正規名を書き戻すため、config に小文字や GUID を書いた環境で
// 恒常 diff ループになる (#119)。逆に未知の値まで抑止すると、実機が拒否する config を
// plan 上「変更なし」に見せてしまうので、known でない側は必ず差分として残す。
func TestDiffSuppressSecureBootTemplate(t *testing.T) {
	tests := []struct {
		name string
		old  string
		new  string
		want bool
	}{
		{"大文字小文字だけの違い", "MicrosoftWindows", "microsoftwindows", true},
		{"正規名と GUID", "MicrosoftWindows", "1734C6E8-3154-4DDA-BA5F-A874CC483422", true},
		{"正規名と小文字 GUID", "MicrosoftWindows", "1734c6e8-3154-4dda-ba5f-a874cc483422", true},
		{"GUID 同士の表記揺れ", "1734C6E8-3154-4DDA-BA5F-A874CC483422", "1734c6e8-3154-4dda-ba5f-a874cc483422", true},
		{"別テンプレートへの変更は見せる", "MicrosoftWindows", "MicrosoftUEFICertificateAuthority", false},
		{"未知の新値は見せる", "MicrosoftWindows", "SomeFutureTemplate", false},
		{"未知の現行値は見せる", "SomeFutureTemplate", "MicrosoftWindows", false},
		{"作成時 (現行値なし) は見せる", "", "MicrosoftWindows", false},
		// 両側が同一の未知 GUID。Terraform 側に差分が無いので実際には到達しないが、
		// 「未知は隠さない」契約をここで固定しておく。
		{"未知同士は一致していても抑止しない", "11111111-2222-3333-4444-555555555555", "11111111-2222-3333-4444-555555555555", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DiffSuppressSecureBootTemplate("vm_firmware.0.secure_boot_template", tt.old, tt.new, nil); got != tt.want {
				t.Errorf("DiffSuppressSecureBootTemplate(%q, %q): got %v, want %v", tt.old, tt.new, got, tt.want)
			}
		})
	}
}
