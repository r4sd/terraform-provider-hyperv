package api

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestVmStateMatchesHostEnum は api.VmState が実機の
// `Microsoft.HyperV.PowerShell.VMState` と**完全に一致**することを検証する。
//
// # なぜ fixture と突合するのか
//
// `api.VmState` は upstream (taliesins) が手で起こしたもので、**32783 以降が
// 2 つずれていた** (`Hibernated` / `ComponentServicing` が抜けており、
// それ以降の名前が全部 1 つずつ繰り上がっていた)。それが #175。
//
// 「値を書いて同じ値を assert する」形のテストでは、起こし間違いを直せない
// (テストも同じ間違いを写すだけ)。**コードとは独立した出どころ**である
// 実機の列挙と突き合わせることで、初めて誤りが落ちる。
//
// # 両方向を見る
//
//	fixture にあってコードに無い  → Windows が状態を増やした / 起こし漏れ
//	コードにあって fixture に無い → 架空の状態を持っている / 値のずれ
//
// 片方向だけだと、ずれ (同じ件数で名前と値の対応が違う) を検出できない。
func TestVmStateMatchesHostEnum(t *testing.T) {
	want := loadVmStateEnumFixture(t)

	// 空振り検出。パスが変わった・fixture が空になった場合に静かに通らないように。
	if len(want) < 25 {
		t.Fatalf("fixture のエントリが %d 件しかない。採取内容かパスが壊れている", len(want))
	}

	// fixture → コード
	for value, name := range want {
		got, ok := VmState_name[VmState(value)]
		if !ok {
			t.Errorf("%d (%s) が api.VmState_name に無い。実機の列挙にあるのでコードに足すこと", value, name)
			continue
		}
		if got != name {
			t.Errorf("%d の名前が %q、実機では %q。**値と名前の対応がずれている** (#175)", value, got, name)
		}
	}

	// コード → fixture
	for value, name := range VmState_name {
		wantName, ok := want[int(value)]
		if !ok {
			t.Errorf("api.VmState_name の %d (%s) が実機の列挙に無い。"+
				"架空の状態か値がずれている (#175)", int(value), name)
			continue
		}
		if wantName != name {
			t.Errorf("%d の名前が %q、実機では %q", int(value), name, wantName)
		}
	}

	if len(VmState_name) != len(want) {
		t.Errorf("api.VmState_name が %d 件、実機の列挙が %d 件", len(VmState_name), len(want))
	}
}

// TestVmStateValueMapCoversAllNames は VmState_value (小文字名 → 値) が
// VmState_name の全エントリを覆うことを検証する。
//
// `ToVmState` はこのマップを引くので、片方だけ足すと**名前で指定しても
// ゼロ値 (= 不明) になる**。足し忘れが静かに通る経路なので固定する。
func TestVmStateValueMapCoversAllNames(t *testing.T) {
	for value, name := range VmState_name {
		key := strings.ToLower(name)
		got, ok := VmState_value[key]
		if !ok {
			t.Errorf("VmState_value に %q が無い (VmState_name には %d としてある)", key, int(value))
			continue
		}
		if got != value {
			t.Errorf("VmState_value[%q] = %d, want %d", key, int(got), int(value))
		}
	}
	if len(VmState_value) != len(VmState_name) {
		t.Errorf("VmState_value が %d 件、VmState_name が %d 件", len(VmState_value), len(VmState_name))
	}
}

// loadVmStateEnumFixture は testdata/vmstate_enum.txt を 値 → 名前 で読み込む。
func loadVmStateEnumFixture(t *testing.T) map[int]string {
	t.Helper()
	path := filepath.Join("testdata", "vmstate_enum.txt")
	f, err := os.Open(path) //#nosec G304 -- testdata 内
	if err != nil {
		t.Fatalf("%s を開けない: %v", path, err)
	}
	defer f.Close()

	out := make(map[int]string)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		num, name, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("%s: 不正な行 %q (期待: <値> <名前>)", path, line)
		}
		v, convErr := strconv.Atoi(strings.TrimSpace(num))
		if convErr != nil {
			t.Fatalf("%s: 値を読めない %q: %v", path, line, convErr)
		}
		name = strings.TrimSpace(name)
		if prev, dup := out[v]; dup {
			t.Fatalf("%s: 値 %d が重複している (%q と %q)", path, v, prev, name)
		}
		out[v] = name
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("%s のスキャンに失敗: %v", path, err)
	}
	return out
}
