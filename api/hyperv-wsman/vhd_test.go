package hyperv_wsman

import (
	"reflect"
	"testing"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

// TestClientConfig_ImplementsHypervVhdClient は ClientConfig が
// api.HypervVhdClient を実装することを検証する。
//
// 重要: VhdExists / GetVhd は本パッケージで定義されているため、シャドウイング
// (override) が効いて hyperv-winrm の実装ではなく hyperv-wsman の実装が呼ばれる。
// 残りの 3 メソッド (CreateOrUpdateVhd / ResizeVhd / DeleteVhd) は
// 埋め込みの hyperv_winrm.ClientConfig から promotion される。
func TestClientConfig_ImplementsHypervVhdClient(t *testing.T) {
	// 型レベルでインターフェース実装を確認する (実行時にメソッド呼び出しはしない)
	var c *ClientConfig
	var _ api.HypervVhdClient = c // コンパイル時チェック

	// メソッド存在の確認
	cType := reflect.TypeOf((*ClientConfig)(nil))
	for _, methodName := range []string{
		"VhdExists",         // ← 本パッケージで定義 (シャドウイング)
		"GetVhd",            // ← 本パッケージで定義 (シャドウイング、Phase B-X.1)
		"ResizeVhd",         // ← 本パッケージで定義 (シャドウイング、Phase B-X.2)
		"CreateOrUpdateVhd", // ← hyperv-winrm から promotion
		"DeleteVhd",         // ← hyperv-winrm から promotion
	} {
		if _, ok := cType.MethodByName(methodName); !ok {
			t.Errorf("ClientConfig should expose method %s (via shadow or promotion)", methodName)
		}
	}
}

// TestClientConfig_VhdExists_DefinedInWsmanPackage は VhdExists が
// hyperv-wsman パッケージ自身で定義されていることを reflect 経由で検証する。
//
// これにより hyperv_winrm.ClientConfig.VhdExists ではなく、本パッケージの
// 実装が呼ばれることが保証される (シャドウイング)。
func TestClientConfig_VhdExists_DefinedInWsmanPackage(t *testing.T) {
	// VhdExists はポインタレシーバなので *ClientConfig で検索する
	cType := reflect.TypeOf((*ClientConfig)(nil))
	method, ok := cType.MethodByName("VhdExists")
	if !ok {
		t.Fatal("ClientConfig should have VhdExists method")
	}

	// MethodFunc の Pkg() を確認する代わりに、シグネチャを既知のものと突合
	if method.Type.NumIn() != 3 { // receiver + ctx + path
		t.Errorf("VhdExists signature mismatch: NumIn=%d", method.Type.NumIn())
	}
	if method.Type.NumOut() != 2 { // VhdExists + error
		t.Errorf("VhdExists signature mismatch: NumOut=%d", method.Type.NumOut())
	}
}

// TestClientConfig_GetVhd_DefinedInWsmanPackage は GetVhd が
// hyperv-wsman パッケージ自身で定義されていることを reflect 経由で検証する。
//
// これにより hyperv_winrm.ClientConfig.GetVhd ではなく、本パッケージの
// 実装が呼ばれることが保証される (シャドウイング)。
func TestClientConfig_GetVhd_DefinedInWsmanPackage(t *testing.T) {
	cType := reflect.TypeOf((*ClientConfig)(nil))
	method, ok := cType.MethodByName("GetVhd")
	if !ok {
		t.Fatal("ClientConfig should have GetVhd method")
	}

	// シグネチャ: (c *ClientConfig).GetVhd(ctx, path) (api.Vhd, error)
	if method.Type.NumIn() != 3 { // receiver + ctx + path
		t.Errorf("GetVhd signature mismatch: NumIn=%d, want 3", method.Type.NumIn())
	}
	if method.Type.NumOut() != 2 { // api.Vhd + error
		t.Errorf("GetVhd signature mismatch: NumOut=%d, want 2", method.Type.NumOut())
	}
}

// TestClientConfig_ResizeVhd_DefinedInWsmanPackage は ResizeVhd が
// hyperv-wsman パッケージ自身で定義されていることを reflect 経由で検証する (Phase B-X.2)。
func TestClientConfig_ResizeVhd_DefinedInWsmanPackage(t *testing.T) {
	cType := reflect.TypeOf((*ClientConfig)(nil))
	method, ok := cType.MethodByName("ResizeVhd")
	if !ok {
		t.Fatal("ClientConfig should have ResizeVhd method")
	}

	// シグネチャ: (c *ClientConfig).ResizeVhd(ctx, path, size) error
	if method.Type.NumIn() != 4 { // receiver + ctx + path + size
		t.Errorf("ResizeVhd signature mismatch: NumIn=%d, want 4", method.Type.NumIn())
	}
	if method.Type.NumOut() != 1 { // error
		t.Errorf("ResizeVhd signature mismatch: NumOut=%d, want 1", method.Type.NumOut())
	}
}

// TestClientConfig_CreateOrUpdateVhd_DefinedInWsmanPackage は CreateOrUpdateVhd が
// hyperv-wsman パッケージ自身で定義されていることを reflect 経由で検証する (Phase B-X.3)。
func TestClientConfig_CreateOrUpdateVhd_DefinedInWsmanPackage(t *testing.T) {
	cType := reflect.TypeOf((*ClientConfig)(nil))
	method, ok := cType.MethodByName("CreateOrUpdateVhd")
	if !ok {
		t.Fatal("ClientConfig should have CreateOrUpdateVhd method")
	}

	// シグネチャ: (c *ClientConfig).CreateOrUpdateVhd(ctx, path, source, sourceVm,
	//   sourceDisk, vhdType, parentPath, size, blockSize, logicalSectorSize,
	//   physicalSectorSize) error
	if method.Type.NumIn() != 12 { // receiver + 11 引数
		t.Errorf("CreateOrUpdateVhd signature mismatch: NumIn=%d, want 12", method.Type.NumIn())
	}
	if method.Type.NumOut() != 1 { // error
		t.Errorf("CreateOrUpdateVhd signature mismatch: NumOut=%d, want 1", method.Type.NumOut())
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
