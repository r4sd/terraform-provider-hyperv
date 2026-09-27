package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// #154 の一連のテスト。
//
// なぜ ID の有無が効くのか (SDK / Terraform core の挙動):
//   - helper/schema の Resource.Apply は diags にエラーがあっても data.State() を返す。
//   - helper/schema の ResourceData.State() は ID が空だと nil を返す。
//     → ID 未設定のままエラーで抜けると Terraform は state に何も記録できない。
//   - tainted にするのは SDK ではなく Terraform core。node_resource_apply_instance.go の
//     maybeTainted が `change.Action == plans.Create && err != nil` で AsTainted() する。
//     → ID さえ入っていれば tainted として記録され、次の apply が destroy→recreate する。
//
// orphanStubClient は「実機に VM が残ったか」だけを操作できる api.Client。
// 埋め込みにしているので、ここで上書きしていないメソッドが呼ばれたら nil ポインタで
// 落ちる = 想定外の経路を通ったことがテスト失敗として現れる。
type orphanStubClient struct {
	api.Client

	// createVmErr が非 nil なら CreateVm がそれを返す (CreateVm 内部での失敗を再現)。
	createVmErr error
	// vmExistsAfterCreate は CreateVm 呼び出し後の VmExists の応答。
	// CreateVm が DefineSystem 成功後に失敗したケース (true) と、
	// DefineSystem 自体が失敗したケース (false) を撃ち分ける。
	vmExistsAfterCreate bool
	// vmExistsErrAfterCreate が非 nil なら CreateVm 後の VmExists がそれを返す。
	vmExistsErrAfterCreate error
	// subresourceErr が非 nil なら最初のサブリソース設定がそれを返す。
	subresourceErr error

	createVmCalled bool
	// vmExistsCheckedAfterCreate は CreateVm 後に残存確認が走ったか。回数ではなく
	// 「その経路を通ったか」を見る (冒頭の重複チェックの有無に結合させないため)。
	vmExistsCheckedAfterCreate bool
}

func (c *orphanStubClient) VmExists(_ context.Context, _ string) (api.VmExists, error) {
	if !c.createVmCalled {
		return api.VmExists{Exists: false}, nil // Create 冒頭の重複チェック
	}
	c.vmExistsCheckedAfterCreate = true
	if c.vmExistsErrAfterCreate != nil {
		return api.VmExists{}, c.vmExistsErrAfterCreate
	}
	return api.VmExists{Exists: c.vmExistsAfterCreate}, nil
}

func (c *orphanStubClient) CreateVm(
	_ context.Context, _ string, _ string, _ int,
	_ api.CriticalErrorAction, _ int32, _ api.StartAction, _ int32, _ api.StopAction,
	_ api.CheckpointType, _ bool, _ bool, _ uint64, _ api.OnOffState, _ uint32,
	_ int64, _ int64, _ int64, _ string, _ int64, _ string, _ string, _ bool, _ bool,
) error {
	c.createVmCalled = true
	return c.createVmErr
}

func (c *orphanStubClient) CreateOrUpdateVmProcessors(_ context.Context, _ string, _ []api.VmProcessor) error {
	return c.subresourceErr
}

func newOrphanTestData(t *testing.T) *schema.ResourceData {
	t.Helper()
	r := resourceHyperVMachineInstance()
	d := schema.TestResourceDataRaw(t, r.SchemaMap(), map[string]interface{}{
		"name":                 "test-vm",
		"generation":           2,
		"static_memory":        true,
		"dynamic_memory":       false,
		"memory_startup_bytes": 536870912,
	})
	d.MarkNewResource()
	return d
}

// #154 の本体。CreateVm は DefineSystem 成功後にも失敗しうる (CIM 経路: 自動生成 NIC の
// 削除 / メモリ / CPU / ゼロ値補正の PS 委譲、PS 経路: New-VM 後の Set-VM 群)。
// このとき実機には VM が残るので、ID を入れて state に紐付けないと孤児になる。
func TestMachineInstanceCreate_SetsIdWhenCreateVmFailsButVmExists(t *testing.T) {
	d := newOrphanTestData(t)
	client := &orphanStubClient{
		createVmErr:         errors.New("シミュレートした失敗 (ゼロ値補正の PS 委譲)"),
		vmExistsAfterCreate: true,
	}

	diags := resourceHyperVMachineInstanceCreate(context.Background(), d, api.Client(client))

	if !diags.HasError() {
		t.Fatalf("CreateVm が失敗したのにエラーが返っていない: %+v", diags)
	}
	if !client.vmExistsCheckedAfterCreate {
		t.Fatalf("CreateVm 失敗後の残存確認が走っていない")
	}
	if d.Id() != "test-vm" {
		t.Errorf("ID が %q。CreateVm が VM を残して失敗したときは VM 名が入るべき。"+
			"空だと state に何も記録されず実機の VM が孤児になる (#154)", d.Id())
	}
}

// 負の対照。DefineSystem 自体が失敗して「実機に VM が無い」と確定したときは ID を入れない。
//
// 既定の refresh が走る限り、載せてしまっても Read の不在分岐が state から外すので
// 自己修復する。効くのは `-refresh=false` で apply したときで、そのとき Delete が
// UpdateVmStatus で落ちて state rm が要る。窓は狭いが、不在が分かっているものを
// わざわざ載せる理由が無い。
func TestMachineInstanceCreate_LeavesIdEmptyWhenVmWasNotCreated(t *testing.T) {
	d := newOrphanTestData(t)
	client := &orphanStubClient{
		createVmErr:         errors.New("シミュレートした失敗 (DefineSystem 自体)"),
		vmExistsAfterCreate: false,
	}

	diags := resourceHyperVMachineInstanceCreate(context.Background(), d, api.Client(client))

	if !diags.HasError() {
		t.Fatalf("CreateVm が失敗したのにエラーが返っていない: %+v", diags)
	}
	if d.Id() != "" {
		t.Errorf("実機に VM が無いのに ID %q が入った。次の apply の Delete が詰む", d.Id())
	}
}

// CreateVm 成功後のサブリソース設定で失敗した場合も、実機には VM が残るので ID が要る。
// (#154 本文には無いが同じ性質の窓。PR #150 のレビュー指摘 3 に対応)
func TestMachineInstanceCreate_SetsIdWhenSubresourceFails(t *testing.T) {
	d := newOrphanTestData(t)
	client := &orphanStubClient{
		subresourceErr: errors.New("シミュレートした失敗 (processor 設定)"),
	}

	diags := resourceHyperVMachineInstanceCreate(context.Background(), d, api.Client(client))

	if !client.createVmCalled {
		t.Fatalf("CreateVm が呼ばれていない。テストが想定した経路を通っていない")
	}
	if !diags.HasError() {
		t.Fatalf("サブリソース失敗なのにエラーが返っていない: %+v", diags)
	}
	if d.Id() != "test-vm" {
		t.Errorf("ID が %q。CreateVm 成功後にエラーで抜けたときも VM 名が入るべき (#154)", d.Id())
	}
}

// 残存確認自体が失敗したときは ID を入れる(載せる側に倒す)。
//
// VM が実在すれば tainted で自動 replace され、実在しなければ次の refresh の Read が
// state から外して自動 create になる。どちらも自己修復する。載せないと VM が実在した
// 場合に孤児が残り、「already exists、import せよ」で止まって必ず手作業になる。
func TestMachineInstanceCreate_SetsIdWhenVmExistsCheckFails(t *testing.T) {
	d := newOrphanTestData(t)
	client := &orphanStubClient{
		createVmErr:            errors.New("シミュレートした失敗"),
		vmExistsErrAfterCreate: errors.New("シミュレートした WinRM 断"),
	}

	diags := resourceHyperVMachineInstanceCreate(context.Background(), d, api.Client(client))

	if !diags.HasError() {
		t.Fatalf("CreateVm が失敗したのにエラーが返っていない: %+v", diags)
	}
	if !client.vmExistsCheckedAfterCreate {
		t.Fatalf("CreateVm 失敗後の残存確認が走っていない")
	}
	if d.Id() != "test-vm" {
		t.Errorf("ID が %q。存在を確認できないときは載せる側に倒す", d.Id())
	}
}
