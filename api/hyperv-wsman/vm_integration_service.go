package hyperv_wsman

import (
	"context"
	"fmt"

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
//
// Enable/Disable/CreateOrUpdate (書き込み) は go-wsman 側にプリミティブ未実装のため、
// 埋め込み hyperv_winrm.ClientConfig から promotion されて PS 実装にフォールバックする。
// go-wsman で書き込みを実装したらここに同名メソッドを追加してシャドウイングする。
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
