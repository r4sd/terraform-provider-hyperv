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
	vmExistsCalls  int
}

func (c *orphanStubClient) VmExists(_ context.Context, _ string) (api.VmExists, error) {
	c.vmExistsCalls++
	if !c.createVmCalled {
		return api.VmExists{Exists: false}, nil // Create 冒頭の重複チェック
	}
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
	if client.vmExistsCalls != 2 {
		t.Fatalf("VmExists の呼び出しが %d 回。冒頭の重複チェックと失敗後の残存確認で 2 回のはず", client.vmExistsCalls)
	}
	if d.Id() == "" {
		t.Errorf("CreateVm が VM を残して失敗したのに ID が空。" +
			"SDK v2 はこの state を捨てるため実機の VM が孤児になる (#154)")
	}
}

// 負の対照。DefineSystem 自体が失敗して実機に VM が無いときに ID を入れてはいけない。
// 入れると state に存在しない VM が載り、次の apply の Delete が
// UpdateVmStatus → waitForStableVmState → ErrVMNotFound で落ちて詰む。
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
	if d.Id() == "" {
		t.Errorf("CreateVm 成功後にエラーで抜けたのに ID が空 (#154)")
	}
}

// 残存確認自体が失敗したときは ID を入れない。実機に VM が残っている可能性はあるが、
// 「存在しない VM を state に載せて次の apply の Delete を詰ませる」より
// 「孤児が残って import を案内される」方が復旧できる。
func TestMachineInstanceCreate_LeavesIdEmptyWhenVmExistsCheckFails(t *testing.T) {
	d := newOrphanTestData(t)
	client := &orphanStubClient{
		createVmErr:            errors.New("シミュレートした失敗"),
		vmExistsErrAfterCreate: errors.New("シミュレートした WinRM 断"),
	}

	diags := resourceHyperVMachineInstanceCreate(context.Background(), d, api.Client(client))

	if !diags.HasError() {
		t.Fatalf("CreateVm が失敗したのにエラーが返っていない: %+v", diags)
	}
	if client.vmExistsCalls != 2 {
		t.Fatalf("VmExists の呼び出しが %d 回。残存確認まで到達していない", client.vmExistsCalls)
	}
	if d.Id() != "" {
		t.Errorf("残存確認が失敗したのに ID %q が入った。実機に VM が無ければ次の apply の Delete が詰む", d.Id())
	}
}
