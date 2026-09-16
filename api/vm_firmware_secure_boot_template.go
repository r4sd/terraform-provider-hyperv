package api

import (
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// Secure Boot テンプレートの固定識別子(全 Hyper-V 環境で共通、秘密情報ではない)。
// 実機で `Set-VMFirmware -SecureBootTemplate <名前>` を実行し、CIM 側の
// Msvm_VirtualSystemSettingData.SecureBootTemplateId を読んで対応を確認した(2026-08-01、#100)。
// PS が受け付けるシンボリック名はこの 3 種で、Windows ゲストは MicrosoftWindows、
// Linux ゲストは MicrosoftUEFICertificateAuthority を使うのが一般的。
const (
	SecureBootTemplateMicrosoftWindowsGUID     = "1734C6E8-3154-4DDA-BA5F-A874CC483422"
	SecureBootTemplateMicrosoftUEFICAGUID      = "272E7447-90A4-4563-A4B9-8E4AB00526CE"
	SecureBootTemplateOpenSourceShieldedVMGUID = "4292AE2B-EE2C-42B5-A969-DD8F8689F6F3"
)

// SecureBootTemplateGUIDToName は SecureBootTemplateId (実 GUID) から PS の -SecureBootTemplate が
// 受け付けるシンボリック名への逆引き表。CIM 経路 (api/hyperv-wsman) もこの表を使う
// (GUID を 2 か所に書くと片方だけ増えた時に誰も気付けないため、出どころはここ 1 つ)。
var SecureBootTemplateGUIDToName = map[string]string{
	SecureBootTemplateMicrosoftWindowsGUID:     "MicrosoftWindows",
	SecureBootTemplateMicrosoftUEFICAGUID:      "MicrosoftUEFICertificateAuthority",
	SecureBootTemplateOpenSourceShieldedVMGUID: "OpenSourceShieldedVM",
}

// CanonicalSecureBootTemplate は secure_boot_template に書ける表記(シンボリック名 / GUID、
// 大文字小文字は不問)を正規のシンボリック名へ解決する。
//
// 既知の 3 テンプレートのいずれでもない場合は known=false を返す。呼び出し側は
// **未知の値を既知のものと同一視してはいけない**。実機が拒否する値を plan 上で
// 「変更なし」に見せてしまうため。
func CanonicalSecureBootTemplate(value string) (canonical string, known bool) {
	if value == "" {
		return "", false
	}
	for guid, name := range SecureBootTemplateGUIDToName {
		if strings.EqualFold(guid, value) || strings.EqualFold(name, value) {
			return name, true
		}
	}
	return "", false
}

// DiffSuppressSecureBootTemplate は secure_boot_template の「同じテンプレートを指す別表記」を
// 差分として扱わない。
//
// Read は SecureBootTemplateId (GUID) を必ず正規のシンボリック名へ写して state に書くため、
// config に小文字表記や GUID を書いた環境では state と config が永久に食い違い、plan が
// 毎回差分を出すのに apply は何も変えない(書き込み側が EqualFold で比較しているため)という
// 恒常 diff ループになる(#119)。
//
// 抑止するのは**両側が既知テンプレートに解決でき、かつ同じもの**を指す場合だけ。片方でも
// 未知なら差分として見せる。未知の値は実機に拒否される可能性があり、それを plan 上で
// 隠すと「apply は通ったように見えて何も起きていない」より悪い状態になる。
func DiffSuppressSecureBootTemplate(key, old, new string, d *schema.ResourceData) bool {
	oldCanonical, oldKnown := CanonicalSecureBootTemplate(old)
	newCanonical, newKnown := CanonicalSecureBootTemplate(new)
	return oldKnown && newKnown && oldCanonical == newCanonical
}
