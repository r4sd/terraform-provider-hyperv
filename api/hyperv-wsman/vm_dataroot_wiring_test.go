package hyperv_wsman

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
)

// snapshot_file_location / smart_paging_file_path が **実際に wire に乗る**ことを固める (#177)。
//
// 🔴 go-wsman の `clearReadOnlyForModify` がこの 2 つをゼロ値にしていたため、
// provider が設定した値は ModifySystemSettings に届かず**黙って捨てられていた**
// (go-wsman #195)。`applyVmLevelSettings` の単体テストは「struct に載った」までしか
// 見られないので、**go-wsman を通した後も残っていること**をここで見る。
//
// このテストは provider → go-wsman → wire の配線を 1 本で通す。
// go-wsman 側が再び同じ型のバグを入れたら、bump した瞬間にここが落ちる。
func TestUpdateVmSendsDataRootsOnWire(t *testing.T) {
	const (
		wantSnap = `C:\wiring-snap`
		wantSwap = `C:\wiring-swap`
		instID   = "Microsoft:11111111-aaaa-bbbb-cccc-000000000099"
	)
	if wantSnap == wantSwap {
		t.Fatal("検査値が同一。取り違えを検出できない")
	}

	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
		_, _ = w.Write([]byte(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:p="http://schemas.microsoft.com/wbem/wsman/1/wmi/root/virtualization/v2/Msvm_VirtualSystemManagementService">
  <s:Body><p:ModifySystemSettings_OUTPUT><p:ReturnValue>0</p:ReturnValue></p:ModifySystemSettings_OUTPUT></s:Body>
</s:Envelope>`))
	}))
	defer srv.Close()

	client, err := hyperv.NewClient(srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	sd := &hyperv.Msvm_VirtualSystemSettingData{InstanceID: instID}
	if err := applyVmLevelSettings(sd, vmLevelWant{
		snapshotFileLocation: wantSnap,
		smartPagingFilePath:  wantSwap,
	}); err != nil {
		t.Fatalf("applyVmLevelSettings: %v", err)
	}
	// 前段の確認。ここが壊れていると後段の検査が何を見ているか分からなくなる。
	if sd.SnapshotDataRoot != wantSnap || sd.SwapFileDataRoot != wantSwap {
		t.Fatalf("applyVmLevelSettings が struct に載せていない: snap=%q swap=%q",
			sd.SnapshotDataRoot, sd.SwapFileDataRoot)
	}

	if _, err := client.UpdateVm(context.Background(), sd); err != nil {
		t.Fatalf("UpdateVm: %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("リクエストが %d 本 (want 1)", len(bodies))
	}

	// 🔴 **プロパティ名と値の対応**で見る。本文に両方の文字列が出ているだけだと、
	// 2 つを入れ替えても通ってしまう。
	for _, c := range []struct{ prop, want string }{
		{"SnapshotDataRoot", wantSnap},
		{"SwapFileDataRoot", wantSwap},
	} {
		got, ok := wireProperty(bodies[0], c.prop)
		if !ok {
			t.Errorf("%s が wire に乗っていない。go-wsman がクリアしている可能性がある "+
				"(go-wsman #195 と同型)\n%s", c.prop, bodies[0])
			continue
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q (取り違え)", c.prop, got, c.want)
		}
	}
}

// wireProperty は CIM-XML の INSTANCE から 1 プロパティの値を取り出す。
//
// ModifySystemSettings の SystemSettings は embedded instance なので
// `<PROPERTY NAME="X" ...><VALUE>v</VALUE></PROPERTY>` の形で乗る。
// XML エスケープを戻してから返す (`\` は素で乗るが `"` 等は実体参照になる)。
func wireProperty(body, prop string) (string, bool) {
	re := regexp.MustCompile(`<PROPERTY NAME="` + regexp.QuoteMeta(prop) +
		`"[^>]*>\s*<VALUE>([^<]*)</VALUE>`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return "", false
	}
	v := m[1]
	for _, p := range [][2]string{{"&amp;", "&"}, {"&lt;", "<"}, {"&gt;", ">"},
		{"&#34;", `"`}, {"&quot;", `"`}, {"&#39;", "'"}} {
		v = strings.ReplaceAll(v, p[0], p[1])
	}
	return v, true
}
