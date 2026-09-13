package provider

import "testing"

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
