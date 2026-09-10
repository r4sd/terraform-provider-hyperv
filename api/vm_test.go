package api

import (
	"encoding/json"
	"testing"
)

func TestSerializeVm(t *testing.T) {
	vmJson, err := json.Marshal(Vm{
		Name:  "test",
		Notes: "test notes",
	})

	if err != nil {
		t.Errorf("Unable to deserialize vm: %s", err.Error())
	}

	vmJsonString := string(vmJson)

	if vmJsonString == "" {
		t.Errorf("Unable to deserialize vm: %s", err.Error())
	}
}

func TestDeserializeVm(t *testing.T) {
	var vmJson = `
{
    "Name":  "TestMachine",
    "Generation":  2
}
`

	var vm Vm
	err := json.Unmarshal([]byte(vmJson), &vm)
	if err != nil {
		t.Errorf("Unable to deserialize vm: %s", err.Error())
	}
}

// TestDiffSuppressNotes は改行コードの違いを差分とみなさないことを検証する。
//
// Hyper-V は Notes の CR を保持せず読み戻しが常に LF になる (実機確認)。
// config に CRLF を書くと state (LF) と一致せず恒常 diff になり、notes は
// hasChangesThatRequireVmToBeOff に含まれるため apply のたびに VM が停止する (#145)。
func TestDiffSuppressNotes(t *testing.T) {
	cases := []struct {
		name     string
		old, new string
		want     bool
	}{
		{"CRLF と LF は同じ", "a\nb", "a\r\nb", true},
		{"CR と LF は同じ", "a\nb", "a\rb", true},
		{"完全一致", "a\nb", "a\nb", true},
		{"どちらも空", "", "", true},
		// 末尾改行は CIM 読み取りで落ちる。HCL の heredoc が必ず付けるため実運用で踏みやすい。
		{"末尾改行の有無は同じ", "a\nb", "a\nb\n", true},
		{"末尾改行が複数でも同じ", "a\nb", "a\nb\n\n", true},
		{"CRLF + 末尾改行", "a\nb", "a\r\nb\r\n", true},
		{"内容が違えば差分", "a\nb", "a\nc", false},
		{"途中の空行は差分として残る", "a\nb", "a\n\nb", false},
		{"行数が違えば差分", "a\nb", "a\nb\nc", false},
		{"空と非空は差分", "", "a", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DiffSuppressNotes("notes", tc.old, tc.new, nil); got != tc.want {
				t.Errorf("DiffSuppressNotes(%q, %q) = %v, want %v", tc.old, tc.new, got, tc.want)
			}
		})
	}
}
