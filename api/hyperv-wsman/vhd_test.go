package hyperv_wsman

import (
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestClientConfig_ImplementsHypervVhdClient は ClientConfig が
// api.HypervVhdClient を実装することを検証する。
//
// 重要: VHD クライアントの全 5 メソッドが本パッケージで定義されているため、
// シャドウイング (override) が効いて hyperv-winrm の実装ではなく hyperv-wsman の
// 実装が呼ばれる。Phase B-X.4 (DeleteVhd) 完了により VHD CRUD は全て移行済み。
func TestClientConfig_ImplementsHypervVhdClient(t *testing.T) {
	// 型レベルでインターフェース実装を確認する (実行時にメソッド呼び出しはしない)
	var c *ClientConfig
	var _ api.HypervVhdClient = c // コンパイル時チェック

	assertAllShadowedIn(t, "vhd.go",
		"VhdExists",
		"GetVhd",
		"ResizeVhd",
		"CreateOrUpdateVhd",
		"DeleteVhd",
	)
}

// TestVhdDeletePrefix は VHD パスから「削除対象ディレクトリ + prefix」への分解を検証する。
//
// PowerShell 版 (hyperv_winrm.deleteVhdTemplate) の targetName 抽出 (= 末尾拡張子を
// 除いたファイル名) を再現する。prefix を誤ると差分ディスクの削除漏れ / 意図しない
// ファイル削除につながるため、データ変換ロジックとして必須テスト対象。
func TestVhdDeletePrefix(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantDir    string
		wantPrefix string
	}{
		{"標準 vhdx", `C:\vms\disk.vhdx`, `C:\vms`, `disk`},
		{"複数ドット (末尾拡張子のみ除去)", `C:\vms\my.disk.vhdx`, `C:\vms`, `my.disk`},
		{"ディレクトリなし", `disk.vhdx`, ``, `disk`},
		{"拡張子なし", `C:\vms\disk`, `C:\vms`, `disk`},
		{"スラッシュ区切り", `C:/vms/disk.vhdx`, `C:/vms`, `disk`},
		{"avhdx (差分ディスク)", `D:\hv\base.avhdx`, `D:\hv`, `base`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDir, gotPrefix := vhdDeletePrefix(tt.path)
			if gotDir != tt.wantDir || gotPrefix != tt.wantPrefix {
				t.Errorf("vhdDeletePrefix(%q) = (%q, %q), want (%q, %q)",
					tt.path, gotDir, gotPrefix, tt.wantDir, tt.wantPrefix)
			}
		})
	}
}

// TestVhdDiskType は api.VhdType → CIM DiskType (uint16) の安全マッピングを検証する。
//
// 直接 uint16 変換 (CodeQL go/incorrect-integer-conversion) を避けるための switch
// マッピングが正しい CIM 値を返すことを保証する (データ変換ロジック = 必須テスト対象)。
func TestVhdDiskType(t *testing.T) {
	tests := []struct {
		name string
		in   api.VhdType
		want uint16
	}{
		{"Fixed", api.VhdType_Fixed, 2},
		{"Dynamic", api.VhdType_Dynamic, 3},
		{"Differencing", api.VhdType_Differencing, 4},
		{"Unknown", api.VhdType_Unknown, 0},
		{"範囲外 → Unknown(0)", api.VhdType(9999), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vhdDiskType(tt.in); got != tt.want {
				t.Errorf("vhdDiskType(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestVhdFormatFromPath は拡張子から CIM DiskFormat 値への変換ロジックを検証する。
//
// PowerShell の New-VHD は拡張子推論するが CIM は明示 Format を要求するため、
// この変換が VHD/VHDX 作成の正しさを左右する (データ変換ロジック = 必須テスト対象)。
func TestVhdFormatFromPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want uint16
	}{
		{"vhdx 小文字", `C:\vms\disk.vhdx`, uint16(api.VhdFormat_VHDX)},
		{"vhdx 大文字混在", `C:\VMs\Disk.VHDX`, uint16(api.VhdFormat_VHDX)},
		{"vhd 小文字", `C:\vms\disk.vhd`, uint16(api.VhdFormat_VHD)},
		{"vhd 大文字", `C:\vms\DISK.VHD`, uint16(api.VhdFormat_VHD)},
		{"vhds (VHDSet)", `C:\vms\disk.vhds`, uint16(api.VhdFormat_VHDSet)},
		{"拡張子なし → VHDX default", `C:\vms\disk`, uint16(api.VhdFormat_VHDX)},
		{"空文字 → VHDX default", "", uint16(api.VhdFormat_VHDX)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vhdFormatFromPath(tt.path); got != tt.want {
				t.Errorf("vhdFormatFromPath(%q) = %d, want %d", tt.path, got, tt.want)
			}
		})
	}
}
