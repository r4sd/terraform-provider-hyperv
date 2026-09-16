package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestNotesDiffSuppressWired は notes の DiffSuppressFunc が schema に配線されている
// ことを検証する。
//
// 関数単体のテスト (api.TestDiffSuppressNewlines) は配線を見ないため、
// schema から DiffSuppressFunc を外す変異が素通りしていた (批判的レビューで実証)。
// internal/provider のテストは全て integration タグ付きで既定では走らないので、
// タグなしのテストをここに置く。
func TestNotesDiffSuppressWired(t *testing.T) {
	s := resourceHyperVMachineInstance().Schema["notes"]
	if s == nil {
		t.Fatal("notes が schema に無い")
	}
	if s.DiffSuppressFunc == nil {
		t.Fatal("notes に DiffSuppressFunc が配線されていない。" +
			"config の CRLF が state の LF と差分になり apply のたびに VM が停止する (#145)")
	}
	if !s.DiffSuppressFunc("notes", "a\nb", "a\r\nb", nil) {
		t.Error("CRLF と LF が差分扱いになっている")
	}
	if s.DiffSuppressFunc("notes", "a\nb", "a\nc", nil) {
		t.Error("内容が違うのに抑制している")
	}
}

// TestSecureBootTemplateDiffSuppressWired は vm_firmware.secure_boot_template に
// DiffSuppressFunc が配線されていることを見る。
//
// 関数単体のテスト (api.TestDiffSuppressSecureBootTemplate) は配線を見ないため、
// schema から外す変異が素通りする (notes と同じ穴)。
func TestSecureBootTemplateDiffSuppressWired(t *testing.T) {
	firmware := resourceHyperVMachineInstance().Schema["vm_firmware"]
	if firmware == nil {
		t.Fatal("vm_firmware が schema に無い")
	}
	elem, ok := firmware.Elem.(*schema.Resource)
	if !ok {
		t.Fatalf("vm_firmware の Elem が *schema.Resource でない: %T", firmware.Elem)
	}
	s := elem.Schema["secure_boot_template"]
	if s == nil {
		t.Fatal("secure_boot_template が schema に無い")
	}
	if s.DiffSuppressFunc == nil {
		t.Fatal("secure_boot_template に DiffSuppressFunc が配線されていない。" +
			"config の小文字表記や GUID が state の正規名と差分になり、apply しても消えない (#119)")
	}
	if !s.DiffSuppressFunc("secure_boot_template", "MicrosoftWindows", "microsoftwindows", nil) {
		t.Error("大文字小文字だけの違いが差分扱いになっている")
	}
	if !s.DiffSuppressFunc("secure_boot_template", "MicrosoftWindows", api.SecureBootTemplateMicrosoftWindowsGUID, nil) {
		t.Error("正規名と GUID が差分扱いになっている")
	}
	if s.DiffSuppressFunc("secure_boot_template", "MicrosoftWindows", "MicrosoftUEFICertificateAuthority", nil) {
		t.Error("別テンプレートなのに抑制している")
	}
	if s.DiffSuppressFunc("secure_boot_template", "MicrosoftWindows", "SomeFutureTemplate", nil) {
		t.Error("未知の値なのに抑制している")
	}
}
