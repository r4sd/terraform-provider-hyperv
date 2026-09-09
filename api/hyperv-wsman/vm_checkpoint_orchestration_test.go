package hyperv_wsman

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
)

// CreateVmCheckpoint の 3 段構成 (作成 → 差分特定 → リネーム) を httptest で固める。
//
// 純関数 (checkpointFromSettingData / findVmCheckpointByName / newCheckpointInstanceID) は
// 個別にテストしてあるが、**それらを繋ぐ配線**は WsmanClient が具象型のため単体で
// 検証できていなかった。批判的レビューで引数の取り違え型の変異が 5/5 生存すると
// 指摘されたため、リクエストボディを捕捉して配線を直接検証する。

const ckptVMGUID = "11111111-aaaa-bbbb-cccc-000000000001"

func ckptEnumXML() string {
	return `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/EnumerateResponse</a:Action></s:Header>
  <s:Body><e:EnumerateResponse><e:EnumerationContext>ctx</e:EnumerationContext></e:EnumerateResponse></s:Body>
</s:Envelope>`
}

// ckptComputerSystemPull は FindComputerSystemByElementName 用の応答。
func ckptComputerSystemPull(vmName string) string {
	return fmt.Sprintf(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration" xmlns:p="http://schemas.microsoft.com/wbem/wsman/1/wmi/root/virtualization/v2/Msvm_ComputerSystem">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/PullResponse</a:Action></s:Header>
  <s:Body><e:PullResponse><e:Items>
    <p:Msvm_ComputerSystem><p:Name>%s</p:Name><p:ElementName>%s</p:ElementName><p:EnabledState>3</p:EnabledState></p:Msvm_ComputerSystem>
  </e:Items><e:EndOfSequence/></e:PullResponse></s:Body>
</s:Envelope>`, ckptVMGUID, vmName)
}

// ckptSnapshotPull は ListVmCheckpoints 用の応答。names の各要素が 1 チェックポイント。
func ckptSnapshotPull(names ...string) string {
	var items strings.Builder
	for i, n := range names {
		snapGUID := fmt.Sprintf("33333333-aaaa-bbbb-cccc-00000000%04d", i+1)
		fmt.Fprintf(&items, `<p:Msvm_VirtualSystemSettingData>
      <p:InstanceID>Microsoft:%s</p:InstanceID>
      <p:ElementName>%s</p:ElementName>
      <p:VirtualSystemIdentifier>%s</p:VirtualSystemIdentifier>
      <p:VirtualSystemType>Microsoft:Hyper-V:Snapshot:Realized</p:VirtualSystemType>
      <p:ConfigurationID>%s</p:ConfigurationID>
      <p:UserSnapshotType>5</p:UserSnapshotType>
    </p:Msvm_VirtualSystemSettingData>`, snapGUID, n, ckptVMGUID, snapGUID)
	}
	return fmt.Sprintf(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration" xmlns:p="http://schemas.microsoft.com/wbem/wsman/1/wmi/root/virtualization/v2/Msvm_VirtualSystemSettingData">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/PullResponse</a:Action></s:Header>
  <s:Body><e:PullResponse><e:Items>%s</e:Items><e:EndOfSequence/></e:PullResponse></s:Body>
</s:Envelope>`, items.String())
}

// ckptInvokeOK は同期成功 (ReturnValue=0、Job 参照なし) の Invoke 応答。
func ckptInvokeOK(method string) string {
	return fmt.Sprintf(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:p="http://schemas.microsoft.com/wbem/wsman/1/wmi/root/virtualization/v2/Msvm_VirtualSystemSnapshotService">
  <s:Header><a:Action>urn:%s</a:Action></s:Header>
  <s:Body><p:%s_OUTPUT><p:ReturnValue>0</p:ReturnValue></p:%s_OUTPUT></s:Body>
</s:Envelope>`, method, method, method)
}

// ckptServer は応答列を順に返し、リクエストボディを記録する httptest サーバを立てる。
//
// 返す closeFn は **応答を使い切ったこと** も検証する。使い切りを見ないと、
// 「巻き戻しの DestroySnapshot 応答を用意したが実装が呼ばない」という変異が
// 素通りする (批判的レビューで実証された)。
func ckptServer(t *testing.T, responses []string) (*ClientConfig, *[]string, func()) {
	t.Helper()
	bodies := make([]string, 0, len(responses))
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if n >= len(responses) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
		_, _ = w.Write([]byte(responses[n]))
		n++
	}))
	wsmanClient, err := hyperv.NewClient(srv.URL)
	if err != nil {
		srv.Close()
		t.Fatalf("hyperv.NewClient: %v", err)
	}
	closeFn := func() {
		srv.Close()
		if n != len(responses) {
			t.Errorf("用意した応答 %d 件のうち %d 件しか消費されていない (期待した呼び出しが行われていない)", len(responses), n)
		}
	}
	return &ClientConfig{WsmanClient: wsmanClient}, &bodies, closeFn
}

// ckptInvokeMethods はリクエストボディから Invoke したメソッド名を出現順に返す。
//
// go-wsman の ParseInvokeResponse は _OUTPUT の要素名を検証しないため、
// **Invoke 同士の応答がずれても検出できない**。呼び出し側の順序は
// リクエストを見るしかない (批判的レビュー指摘)。
func ckptInvokeMethods(bodies []string) []string {
	known := []string{"CreateSnapshot", "ModifySystemSettings", "DestroySnapshot", "ApplySnapshot"}
	var out []string
	for _, b := range bodies {
		for _, m := range known {
			// SOAP Action ヘッダに URI 末尾として現れる。
			if strings.Contains(b, "/"+m) || strings.Contains(b, ":"+m) {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

func assertInvokeSequence(t *testing.T, bodies []string, want ...string) {
	t.Helper()
	got := ckptInvokeMethods(bodies)
	if len(got) != len(want) {
		t.Fatalf("Invoke 列が違う: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Invoke 列が違う: got %v, want %v", got, want)
		}
	}
}

func TestCreateVmCheckpointOrchestration(t *testing.T) {
	const vmName = "vm-1"
	const cpName = "nightly"

	t.Run("正常系: 作成 → 差分特定 → リネームの配線", func(t *testing.T) {
		c, bodies, closeFn := ckptServer(t, []string{
			ckptEnumXML(), ckptComputerSystemPull(vmName), // resolveVMGUID
			ckptEnumXML(), ckptSnapshotPull("existing"), // before (1 件)
			ckptInvokeOK("CreateSnapshot"),
			ckptEnumXML(), ckptSnapshotPull("existing", "vm-1 - (2026/09/10)"), // after (2 件)
			ckptInvokeOK("ModifySystemSettings"),                // rename
			ckptEnumXML(), ckptSnapshotPull("existing", cpName), // verify
		})
		defer closeFn()

		if err := c.CreateVmCheckpoint(context.Background(), vmName, cpName); err != nil {
			t.Fatalf("CreateVmCheckpoint: %v", err)
		}

		// リネーム要求の中身を検証する。引数を取り違えると InstanceID と ElementName が
		// 入れ替わるが、応答は固定なのでエラーにならない = ボディを見ないと検出できない。
		var rename string
		for _, b := range *bodies {
			if strings.Contains(b, "ModifySystemSettings") {
				rename = b
			}
		}
		if rename == "" {
			t.Fatal("ModifySystemSettings のリクエストが無い")
		}
		assertInvokeSequence(t, *bodies, "CreateSnapshot", "ModifySystemSettings")
		// after で増えたのは 2 件目 (…0002)。それを対象にリネームしているはず。
		//
		// ⚠️ 単なる Contains では引数を取り違えても両方の文字列が本文に現れるため
		// 検出できない (批判的レビュー指摘の M11 が生存した)。**どのプロパティに
		// どちらが入っているか**を見る。
		wantInstance := `<PROPERTY NAME="InstanceID" TYPE="string"><VALUE>Microsoft:33333333-aaaa-bbbb-cccc-000000000002</VALUE>`
		if !strings.Contains(rename, wantInstance) {
			t.Errorf("InstanceID に差分で特定した ID が入っていない (引数の取り違え?):\n%s", rename)
		}
		wantElement := `<PROPERTY NAME="ElementName" TYPE="string"><VALUE>` + cpName + `</VALUE>`
		if !strings.Contains(rename, wantElement) {
			t.Errorf("ElementName に %q が入っていない (引数の取り違え?):\n%s", cpName, rename)
		}
	})

	t.Run("リネームが黙殺されたらエラーにする", func(t *testing.T) {
		// verify の一覧に指定名が現れない = リネームが効いていない。
		// ここを検出できないと「作成成功」なのに次の Read で見つからず terraform が壊れる。
		c, bodies, closeFn := ckptServer(t, []string{
			ckptEnumXML(), ckptComputerSystemPull(vmName),
			ckptEnumXML(), ckptSnapshotPull(),
			ckptInvokeOK("CreateSnapshot"),
			ckptEnumXML(), ckptSnapshotPull("vm-1 - (2026/09/10)"),
			ckptInvokeOK("ModifySystemSettings"),
			ckptEnumXML(), ckptSnapshotPull("vm-1 - (2026/09/10)"), // 既定名のまま
			ckptInvokeOK("DestroySnapshot"), // 巻き戻し
		})
		defer closeFn()

		err := c.CreateVmCheckpoint(context.Background(), vmName, cpName)
		if err == nil {
			t.Fatal("リネームが反映されていないのにエラーにならない")
		}
		if !strings.Contains(err.Error(), "リネームが反映されていない") {
			t.Errorf("想定外のエラー: %v", err)
		}
		// 作ったチェックポイントを巻き戻すこと。放置すると再 apply のたびに増える。
		assertInvokeSequence(t, *bodies, "CreateSnapshot", "ModifySystemSettings", "DestroySnapshot")
	})

	t.Run("同名が既にあれば作成しない", func(t *testing.T) {
		c, bodies, closeFn := ckptServer(t, []string{
			ckptEnumXML(), ckptComputerSystemPull(vmName),
			ckptEnumXML(), ckptSnapshotPull(cpName),
		})
		defer closeFn()

		if err := c.CreateVmCheckpoint(context.Background(), vmName, cpName); err == nil {
			t.Fatal("同名が既に存在するのにエラーにならない")
		}
		for _, b := range *bodies {
			if strings.Contains(b, "CreateSnapshot") {
				t.Error("CreateSnapshot を呼んではいけない")
			}
		}
	})
}

func TestGetVmCheckpointLookupWiring(t *testing.T) {
	// vmName と checkpointName を取り違えると、VM 解決に checkpointName が渡って
	// 「VM 不在」になり、ゼロ値が返る。両者を明確に別の値にして検出する。
	const vmName = "vm-1"
	const cpName = "nightly"

	c, _, closeFn := ckptServer(t, []string{
		ckptEnumXML(), ckptComputerSystemPull(vmName),
		ckptEnumXML(), ckptSnapshotPull(cpName),
	})
	defer closeFn()

	got, err := c.GetVmCheckpoint(context.Background(), vmName, cpName)
	if err != nil {
		t.Fatalf("GetVmCheckpoint: %v", err)
	}
	if got.Name != cpName {
		t.Fatalf("Name = %q, want %q (vmName と checkpointName の取り違え?)", got.Name, cpName)
	}
	if got.VmName != vmName {
		t.Errorf("VmName = %q, want %q", got.VmName, vmName)
	}
	// 補足: VM 名の絞り込みは Go 側 (enumerateFiltered) で行われるためリクエストには
	// 現れない。引数を取り違えると FindComputerSystemByElementName("nightly") が
	// 一致せず ErrVMNotFound → ゼロ値になるので、上の Name 検証で検出できる。
}

func TestDeleteVmCheckpointIdempotent(t *testing.T) {
	// 不在の削除は成功扱い。terraform の destroy は同じリソースに対して
	// 複数回呼ばれうるため、2 回目がエラーになると詰まる。
	c, bodies, closeFn := ckptServer(t, []string{
		ckptEnumXML(), ckptComputerSystemPull("vm-1"),
		ckptEnumXML(), ckptSnapshotPull("other"), // 対象が無い
	})
	defer closeFn()

	if err := c.DeleteVmCheckpoint(context.Background(), "vm-1", "nightly"); err != nil {
		t.Fatalf("不在の削除はエラーにしない: %v", err)
	}
	for _, b := range *bodies {
		if strings.Contains(b, "DestroySnapshot") {
			t.Error("不在なのに DestroySnapshot を呼んでいる")
		}
	}
}

func TestCheckpointOpsWhenVmMissing(t *testing.T) {
	// VM ごと外部削除された場合、Get はゼロ値・Delete は成功にする。
	// PS 版 (Get-VMSnapshot -ErrorAction SilentlyContinue) と同じ挙動で、
	// ここでエラーにすると refresh/plan が失敗して terraform が動かせなくなる。
	emptyCS := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:e="http://schemas.xmlsoap.org/ws/2004/09/enumeration">
  <s:Header><a:Action>http://schemas.xmlsoap.org/ws/2004/09/enumeration/PullResponse</a:Action></s:Header>
  <s:Body><e:PullResponse><e:Items/><e:EndOfSequence/></e:PullResponse></s:Body>
</s:Envelope>`

	t.Run("Get はゼロ値", func(t *testing.T) {
		c, _, closeFn := ckptServer(t, []string{ckptEnumXML(), emptyCS})
		defer closeFn()
		got, err := c.GetVmCheckpoint(context.Background(), "gone", "nightly")
		if err != nil {
			t.Fatalf("VM 不在でエラーにしてはいけない: %v", err)
		}
		if got.Name != "" {
			t.Errorf("got %+v, want ゼロ値", got)
		}
	})

	t.Run("Delete は成功", func(t *testing.T) {
		c, _, closeFn := ckptServer(t, []string{ckptEnumXML(), emptyCS})
		defer closeFn()
		if err := c.DeleteVmCheckpoint(context.Background(), "gone", "nightly"); err != nil {
			t.Errorf("VM 不在でエラーにしてはいけない: %v", err)
		}
	})
}

func TestRestoreVmCheckpointDelegatesWhenRunning(t *testing.T) {
	// CIM の ApplySnapshot は種別に関係なく稼働中 VM を拒否する (実機で 32775)。
	// PS は「スナップショット時点の状態に戻す」ので委譲する必要がある。
	// 委譲を落とすと Standard チェックポイントで維持されるはずの稼働状態を黙って壊す。
	//
	// 埋め込み winrm を nil にして「委譲したら panic」で検出する
	// (vm_firmware_test.go と同じ規約)。
	const vmName = "vm-1"
	const cpName = "nightly"

	runningCS := strings.Replace(ckptComputerSystemPull(vmName),
		"<p:EnabledState>3</p:EnabledState>", "<p:EnabledState>2</p:EnabledState>", 1)

	c, bodies, closeFn := ckptServer(t, []string{
		ckptEnumXML(), ckptComputerSystemPull(vmName), // lookupCheckpoint の VM 解決
		ckptEnumXML(), ckptSnapshotPull(cpName), // 一覧
		ckptEnumXML(), runningCS, // 状態確認 → Running
	})
	defer closeFn()

	defer func() {
		if r := recover(); r == nil {
			t.Error("稼働中 VM では PS へ委譲することを期待 (nil クライアントで panic)")
		}
		// ApplySnapshot を呼んではいけない。呼んでいたら CIM で処理しようとしている。
		for _, b := range *bodies {
			if strings.Contains(b, "ApplySnapshot") {
				t.Error("稼働中なのに ApplySnapshot を呼んでいる")
			}
		}
	}()
	_ = c.RestoreVmCheckpoint(context.Background(), vmName, cpName)
}

func TestRestoreVmCheckpointUsesCimWhenStopped(t *testing.T) {
	// 停止中は CIM でそのまま復元する (委譲しない = PS-0 を保つ)。
	const vmName = "vm-1"
	const cpName = "nightly"

	c, bodies, closeFn := ckptServer(t, []string{
		ckptEnumXML(), ckptComputerSystemPull(vmName),
		ckptEnumXML(), ckptSnapshotPull(cpName),
		ckptEnumXML(), ckptComputerSystemPull(vmName), // EnabledState=3 (Off)
		ckptInvokeOK("ApplySnapshot"),
	})
	defer closeFn()

	if err := c.RestoreVmCheckpoint(context.Background(), vmName, cpName); err != nil {
		t.Fatalf("RestoreVmCheckpoint: %v", err)
	}
	assertInvokeSequence(t, *bodies, "ApplySnapshot")
}
