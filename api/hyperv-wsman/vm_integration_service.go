package hyperv_wsman

import (
	"context"
	"fmt"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// GetVmIntegrationServices は VM の統合サービス状態を go-wsman 経由で取得する。
//
// PS 版 (Get-VMIntegrationService) をシャドウイングし、Read の無条件 PowerShell 実行を解消する。
// go-wsman ListIntegrationServices が 6 つの Component SettingData を VM GUID で列挙し、
// ElementName (= PS の Name) と EnabledState (2=有効/3=無効) を返す。
//
// ElementName はホスト OS 言語にローカライズされるが、PS の Name と同一文字列を返すため、
// refresh/plan は PS 実装とロケールに関わらず同一結果になり差分を出さない (パリティ)。
func (c *ClientConfig) GetVmIntegrationServices(ctx context.Context, vmName string) ([]api.VmIntegrationService, error) {
	guid, err := c.resolveVMGUID(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: GetVmIntegrationServices %q: %w", vmName, err)
	}
	svcs, err := c.WsmanClient.ListIntegrationServices(ctx, guid)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: GetVmIntegrationServices %q: %w", vmName, err)
	}
	result := make([]api.VmIntegrationService, 0, len(svcs))
	for _, s := range svcs {
		result = append(result, api.VmIntegrationService{
			Name:    s.Name,
			Enabled: s.Enabled,
		})
	}
	return result, nil
}

// CreateOrUpdateVmIntegrationServices は VM の統合サービス群を go-wsman 経由で書き込む。
//
// PS 版 (Enable-VMIntegrationService / Disable-VMIntegrationService) をシャドウイングし、
// create/update の無条件 PowerShell 実行を解消する。`integration_services` は schema.TypeMap
// (ValidateFunc なし) なので Terraform config 上は任意の文字列キーを書ける。実際に受理される
// のは go-wsman の IntegrationServiceComponent と一致する 6 つの英語名 (Heartbeat / Key-Value
// Pair Exchange / Shutdown / Time Synchronization / VSS / Guest Service Interface) のみで、
// 未知の名前は go-wsman 側が fail-loud でエラーを返す (PS が未知の -Name を拒否するのと同じ
// 失敗クラス。config バリデーションでの事前拒否ではなく apply 時のエラーである点に注意)。
//
// 差分なしガード: GetIntegrationServiceEnabled (ロケール非依存) で現行値を確認し、要求値と
// 一致するなら Set をスキップする往復削減の最適化。strict モード (PS-0) はこのガードの成否とは
// 無関係に、本メソッドが常に go-wsman 経由で書き込む (PS に委譲しない) ことで既に成立している。
//
// Enable/Disable/CreateOrUpdate 個別メソッドは埋め込み hyperv_winrm.ClientConfig からの
// promotion では呼ばれない (resource 層は CreateOrUpdateVmIntegrationServices のみを呼ぶ) ため、
// この 1 メソッドのシャドウで無条件 PS 実行が解消される。
func (c *ClientConfig) CreateOrUpdateVmIntegrationServices(ctx context.Context, vmName string, integrationServices []api.VmIntegrationService) error {
	if len(integrationServices) == 0 {
		return nil
	}
	guid, err := c.resolveVMGUID(ctx, vmName)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmIntegrationServices %q: %w", vmName, err)
	}

	for _, svc := range integrationServices {
		component := hyperv.IntegrationServiceComponent(svc.Name)

		current, err := c.WsmanClient.GetIntegrationServiceEnabled(ctx, guid, component)
		if err != nil {
			return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmIntegrationServices %q: get %s: %w", vmName, svc.Name, err)
		}
		if current == svc.Enabled {
			continue // 差分なしガード: 既に望む状態
		}
		if err := c.WsmanClient.SetIntegrationServiceEnabled(ctx, guid, component, svc.Enabled); err != nil {
			return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmIntegrationServices %q: set %s: %w", vmName, svc.Name, err)
		}
	}
	return nil
}
