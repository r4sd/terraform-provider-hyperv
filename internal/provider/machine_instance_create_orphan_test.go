package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// orphanStubClient は「CreateVm は成功するが後続のサブリソース設定が失敗する」状況だけを
// 再現する api.Client。埋め込みにしているので、ここで上書きしていないメソッドが呼ばれたら
// nil ポインタで落ちる = 想定外の経路を通ったことがテスト失敗として現れる。
type orphanStubClient struct {
	api.Client
	createVmCalled bool
}

func (c *orphanStubClient) VmExists(_ context.Context, _ string) (api.VmExists, error) {
	return api.VmExists{Exists: false}, nil
}

func (c *orphanStubClient) CreateVm(
	_ context.Context, _ string, _ string, _ int,
	_ api.CriticalErrorAction, _ int32, _ api.StartAction, _ int32, _ api.StopAction,
	_ api.CheckpointType, _ bool, _ bool, _ uint64, _ api.OnOffState, _ uint32,
	_ int64, _ int64, _ int64, _ string, _ int64, _ string, _ string, _ bool, _ bool,
) error {
	c.createVmCalled = true
	return nil
}

// CreateOrUpdateVmProcessors が CreateVm 直後の最初のサブリソース呼び出し。
// ここで失敗させると、実機には VM が残るが Create はエラーで抜ける。
func (c *orphanStubClient) CreateOrUpdateVmProcessors(_ context.Context, _ string, _ []api.VmProcessor) error {
	return errors.New("シミュレートした失敗 (ゼロ値補正など)")
}

// TestMachineInstanceCreate_SetsIdBeforeSubresources は #154 の回帰テスト。
//
// SDK v2 の Resource.Apply は diags にエラーがあっても data.State() を返し、
// ResourceData.State() は ID が空なら nil を返す。つまり CreateVm 成功後に
// SetId しないままエラーを返すと、Terraform は何も記録できず**実機の VM だけが残る**
// (次の apply は VmExists に引っかかって "already exists、import せよ" で停止)。
//
// ID さえ入っていれば tainted として記録され、次の apply が destroy→recreate する。
// これは PS 経路 / CIM 経路のどちらでも同じ resource Create を通るため、両経路に効く。
func TestMachineInstanceCreate_SetsIdBeforeSubresources(t *testing.T) {
	r := resourceHyperVMachineInstance()
	d := schema.TestResourceDataRaw(t, r.SchemaMap(), map[string]interface{}{
		"name":                 "test-vm",
		"generation":           2,
		"static_memory":        true,
		"dynamic_memory":       false,
		"memory_startup_bytes": 536870912,
	})
	d.MarkNewResource()

	client := &orphanStubClient{}
	diags := resourceHyperVMachineInstanceCreate(context.Background(), d, api.Client(client))

	if !client.createVmCalled {
		t.Fatalf("CreateVm が呼ばれていない。テストが想定した経路を通っていない")
	}
	if !diags.HasError() {
		t.Fatalf("サブリソース失敗なのにエラーが返っていない: %+v", diags)
	}
	if d.Id() == "" {
		t.Errorf("CreateVm 成功後にエラーで抜けたのに ID が空。" +
			"SDK v2 はこの state を捨てるため実機の VM が孤児になる (#154)")
	}
}
