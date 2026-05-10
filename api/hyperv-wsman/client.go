// Package hyperv_wsman は go-wsman ライブラリを介して Hyper-V を操作するクライアント。
//
// PowerShell スクリプトに依存する hyperv_winrm パッケージの段階的な代替として導入する。
//
// 移行戦略:
//
//	hyperv_winrm.ClientConfig を埋め込むことで、既存の Client インターフェース実装は
//	全て promotion で継承される。go-wsman 経由に置き換えるメソッドだけ本パッケージで
//	同名定義してシャドウイングする。これにより段階移行が可能。
//
// feature flag:
//
//	プロバイダ設定 (HypervProviderConfig) または環境変数 HYPERV_USE_WSMAN により、
//	hyperv_winrm と本パッケージのどちらを使うかを切り替える。
package hyperv_wsman

import (
	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
	hyperv_winrm "github.com/taliesins/terraform-provider-hyperv/api/hyperv-winrm"
)

// ClientConfig は go-wsman + 既存 PowerShell 経由の両方を保持する。
//
// 埋め込みフィールド hyperv_winrm.ClientConfig により、未移行のメソッドは PowerShell
// 実装にフォールバックする。Phase B 以降で個別メソッドを本構造体に追加すると、
// その時点で go-wsman 経由に置き換わる (シャドウイング)。
type ClientConfig struct {
	*hyperv_winrm.ClientConfig

	// WsmanClient は go-wsman の Hyper-V クライアント。
	// Phase B 以降のメソッド実装で利用する。
	WsmanClient *hyperv.Client
}

// New は go-wsman 版の Provider を生成する。
//
// winrmConfig は既存メソッドのフォールバック用 (Phase E で削除予定)。
// wsmanClient は新たに使う go-wsman クライアント。
//
// 戻り値の Provider.Client は api.Client インターフェースを実装する。
// 未移行のメソッドは hyperv_winrm.ClientConfig が処理し、
// 移行済みメソッド (Phase B+) は本パッケージで定義された実装が使われる。
func New(winrmConfig *hyperv_winrm.ClientConfig, wsmanClient *hyperv.Client) (*api.Provider, error) {
	return &api.Provider{
		Client: &ClientConfig{
			ClientConfig: winrmConfig,
			WsmanClient:  wsmanClient,
		},
	}, nil
}
