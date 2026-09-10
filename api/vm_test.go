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

// TestDiffSuppressNewlines は改行コードの違いを差分とみなさないことを検証する。
//
// Hyper-V は Notes の CR を保持せず読み戻しが常に LF になる (実機確認)。
// config に CRLF を書くと state (LF) と一致せず恒常 diff になり、notes は
// hasChangesThatRequireVmToBeOff に含まれるため apply のたびに VM が停止する (#145)。
func TestDiffSuppressNewlines(t *testing.T) {
	cases := []struct {
		name     string
		old, new string
		want     bool
	}{
		{"CRLF と LF は同じ", "a\nb", "a\r\nb", true},
		{"CR と LF は同じ", "a\nb", "a\rb", true},
		{"完全一致", "a\nb", "a\nb", true},
		{"どちらも空", "", "", true},
		{"内容が違えば差分", "a\nb", "a\nc", false},
		{"行数が違えば差分", "a\nb", "a\nb\nc", false},
		{"空と非空は差分", "", "a", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DiffSuppressNewlines("notes", tc.old, tc.new, nil); got != tc.want {
				t.Errorf("DiffSuppressNewlines(%q, %q) = %v, want %v", tc.old, tc.new, got, tc.want)
			}
		})
	}
}
