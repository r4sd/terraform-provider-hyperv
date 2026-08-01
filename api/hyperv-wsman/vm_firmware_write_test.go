package hyperv_wsman

import (
	"reflect"
	"strings"
	"testing"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

func TestBuildFirmwareCIMValues(t *testing.T) {
	values, ok := buildFirmwareCIMValues(
		api.OnOffState_On, "MicrosoftWindows", api.IPProtocolPreference_IPv6, api.ConsoleModeType_Com1, api.OnOffState_On,
		[]string{"ref-1"},
	)
	if !ok {
		t.Fatal("既知のテンプレート名は resolvable であるべき")
	}
	want := firmwareCIMValues{
		secureBoot:             true,
		secureBootTemplateGUID: secureBootTemplateMicrosoftWindowsGUID,
		networkBootProtocol:    hyperv.NetworkBootPreferredProtocolIPv6,
		consoleMode:            uint16(api.ConsoleModeType_Com1),
		pauseAfterBootFailure:  true,
		bootSourceOrder:        []string{"ref-1"},
	}
	if !reflect.DeepEqual(values, want) {
		t.Errorf("buildFirmwareCIMValues:\ngot  %+v\nwant %+v", values, want)
	}
}

func TestBuildFirmwareCIMValues_UnknownTemplate(t *testing.T) {
	_, ok := buildFirmwareCIMValues(
		api.OnOffState_Off, "SomeUnknownTemplate", api.IPProtocolPreference_IPv4, api.ConsoleModeType_Default, api.OnOffState_Off, nil,
	)
	if ok {
		t.Error("未知のテンプレート名は resolvable=false であるべき")
	}
}

func TestFirmwareWriteNoop(t *testing.T) {
	current := &hyperv.Msvm_VirtualSystemSettingData{
		SecureBoot:                   true,
		SecureBootTemplateId:         secureBootTemplateMicrosoftWindowsGUID,
		NetworkBootPreferredProtocol: hyperv.NetworkBootPreferredProtocolIPv4,
		ConsoleMode:                  hyperv.ConsoleModeDefault,
		PauseAfterBootFailure:        false,
		BootSourceOrder:              []string{"ref-1"},
	}
	same := firmwareCIMValues{
		secureBoot:             true,
		secureBootTemplateGUID: secureBootTemplateMicrosoftWindowsGUID,
		networkBootProtocol:    hyperv.NetworkBootPreferredProtocolIPv4,
		consoleMode:            hyperv.ConsoleModeDefault,
		pauseAfterBootFailure:  false,
		bootSourceOrder:        []string{"ref-1"},
	}
	if !firmwareWriteNoop(current, same) {
		t.Error("完全一致は no-op と判定されるべき")
	}

	diff := same
	diff.consoleMode = hyperv.ConsoleModeCOM1
	if firmwareWriteNoop(current, diff) {
		t.Error("差分がある場合は no-op と判定されるべきではない")
	}
}

// TestFirmwareWriteNoop_SecureBootTemplateIdCaseInsensitive は SecureBootTemplateId の GUID
// 表記の大文字小文字がホストによって揺れても (go-wsman #103 の case-sensitivity 事故と同種)、
// 同一テンプレートを指す限り no-op と判定されることを検証する (#100 既知ギャップ 2)。
func TestFirmwareWriteNoop_SecureBootTemplateIdCaseInsensitive(t *testing.T) {
	current := &hyperv.Msvm_VirtualSystemSettingData{
		SecureBootTemplateId:         strings.ToLower(secureBootTemplateMicrosoftWindowsGUID),
		NetworkBootPreferredProtocol: hyperv.NetworkBootPreferredProtocolIPv4,
		ConsoleMode:                  hyperv.ConsoleModeDefault,
	}
	want := firmwareCIMValues{
		secureBootTemplateGUID: secureBootTemplateMicrosoftWindowsGUID,
		networkBootProtocol:    hyperv.NetworkBootPreferredProtocolIPv4,
		consoleMode:            hyperv.ConsoleModeDefault,
	}
	if !firmwareWriteNoop(current, want) {
		t.Error("大文字小文字表記が違うだけの同一 GUID は no-op と判定されるべき")
	}

	wantDifferentTemplate := firmwareCIMValues{
		secureBootTemplateGUID: "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE",
		networkBootProtocol:    hyperv.NetworkBootPreferredProtocolIPv4,
		consoleMode:            hyperv.ConsoleModeDefault,
	}
	if firmwareWriteNoop(current, wantDifferentTemplate) {
		t.Error("異なる GUID は no-op と判定されるべきではない")
	}
}

// TestFirmwareWriteNoop_BootSourceOrderFormatMismatch は current.BootSourceOrder (サーバが返す
// 実機形式、ホスト前置+シングルバックスラッシュ) と want.bootSourceOrder (BootSourceRef=
// wmiObjectPath が組み立てるクライアント側形式、ホスト前置なし+ダブルバックスラッシュエスケープ+
// フォワードスラッシュ namespace) が、生文字列としては一致しなくても同じデバイスを指す限り
// no-op と判定されることを検証する (Fable 指摘: 正規化なしでは差分なしガードが常に false になり
// 機能しない)。
func TestFirmwareWriteNoop_BootSourceOrderFormatMismatch(t *testing.T) {
	current := &hyperv.Msvm_VirtualSystemSettingData{
		// サーバ実機形式 (シングルバックスラッシュ、ホスト前置あり)。
		BootSourceOrder: []string{
			`\\HOST\root\virtualization\v2:Msvm_BootSourceSettingData.InstanceID="Microsoft:vm-guid\nic-guid\B"`,
		},
	}
	want := firmwareCIMValues{
		// クライアント側 (BootSourceRef/wmiObjectPath) 形式 (ダブルバックスラッシュ、ホスト前置なし、
		// namespace がフォワードスラッシュ)。同じデバイスを指す。
		bootSourceOrder: []string{
			`root/virtualization/v2:Msvm_BootSourceSettingData.InstanceID="Microsoft:vm-guid\\nic-guid\\B"`,
		},
	}
	if !firmwareWriteNoop(current, want) {
		t.Error("形式が違うだけで同一デバイスを指す BootSourceOrder は no-op と判定されるべき")
	}

	wantDifferentDevice := firmwareCIMValues{
		bootSourceOrder: []string{
			`root/virtualization/v2:Msvm_BootSourceSettingData.InstanceID="Microsoft:vm-guid\\other-nic\\B"`,
		},
	}
	if firmwareWriteNoop(current, wantDifferentDevice) {
		t.Error("異なるデバイスを指す BootSourceOrder は no-op と判定されるべきではない")
	}
}

// TestFirmwareWriteNoop_EmptyBootSourceOrderIsNoChange は「要求値の BootSourceOrder が空」の場合に
// 現行値が非空でも差分なし (no-op) と判定されることを検証する (#99)。
//
// PS 版の実機挙動を確認したところ `Set-VMFirmware -BootOrder @()` はエラーにならず、既存の
// BootSourceOrder も変化しない = 空配列は「クリア」ではなく「指定なし」として扱われる。
// go-wsman 側もこれに合わせることで、vm_firmware 未指定の Gen2 create が PS 委譲されなくなる。
func TestFirmwareWriteNoop_EmptyBootSourceOrderIsNoChange(t *testing.T) {
	current := &hyperv.Msvm_VirtualSystemSettingData{
		NetworkBootPreferredProtocol: hyperv.NetworkBootPreferredProtocolIPv4,
		ConsoleMode:                  hyperv.ConsoleModeDefault,
		// create 時に NIC が先に追加され Hyper-V が自動登録した状態を模す。
		BootSourceOrder: []string{"ref-nic"},
	}
	want := firmwareCIMValues{
		networkBootProtocol: hyperv.NetworkBootPreferredProtocolIPv4,
		consoleMode:         hyperv.ConsoleModeDefault,
		bootSourceOrder:     nil, // config で vm_firmware 未指定 = 既定値 (空)
	}
	if !firmwareWriteNoop(current, want) {
		t.Error("要求値が空の BootSourceOrder は「指定なし」= 差分なしと判定されるべき (#99)")
	}

	// 非空同士は従来どおり比較する (指定があるなら差分を見る)。
	wantDifferent := want
	wantDifferent.bootSourceOrder = []string{"ref-other"}
	if firmwareWriteNoop(current, wantDifferent) {
		t.Error("非空で内容が異なる BootSourceOrder は差分ありと判定されるべき")
	}
}

// TestFirmwareZeroDowngrade は marshalEmbeddedInstance がゼロ値を送信しないために go-wsman では
// 表現できない「非ゼロ→ゼロ」遷移を正しく検出することを検証する (Slice A Fable C と同型のリスク)。
func TestFirmwareZeroDowngrade(t *testing.T) {
	tests := []struct {
		name          string
		current       *hyperv.Msvm_VirtualSystemSettingData
		want          firmwareCIMValues
		wantDowngrade bool
	}{
		{
			name:          "SecureBoot true→false は非表現",
			current:       &hyperv.Msvm_VirtualSystemSettingData{SecureBoot: true},
			want:          firmwareCIMValues{secureBoot: false},
			wantDowngrade: true,
		},
		{
			name:          "SecureBoot false→true は表現可能",
			current:       &hyperv.Msvm_VirtualSystemSettingData{SecureBoot: false},
			want:          firmwareCIMValues{secureBoot: true},
			wantDowngrade: false,
		},
		{
			name:          "PauseAfterBootFailure true→false は非表現",
			current:       &hyperv.Msvm_VirtualSystemSettingData{PauseAfterBootFailure: true},
			want:          firmwareCIMValues{pauseAfterBootFailure: false},
			wantDowngrade: true,
		},
		{
			name:          "ConsoleMode COM1→Default は非表現",
			current:       &hyperv.Msvm_VirtualSystemSettingData{ConsoleMode: hyperv.ConsoleModeCOM1},
			want:          firmwareCIMValues{consoleMode: hyperv.ConsoleModeDefault},
			wantDowngrade: true,
		},
		{
			name:          "ConsoleMode Default→COM1 は表現可能",
			current:       &hyperv.Msvm_VirtualSystemSettingData{ConsoleMode: hyperv.ConsoleModeDefault},
			want:          firmwareCIMValues{consoleMode: hyperv.ConsoleModeCOM1},
			wantDowngrade: false,
		},
		{
			name:          "SecureBootTemplateId 非空→空 は非表現",
			current:       &hyperv.Msvm_VirtualSystemSettingData{SecureBootTemplateId: secureBootTemplateMicrosoftWindowsGUID},
			want:          firmwareCIMValues{secureBootTemplateGUID: ""},
			wantDowngrade: true,
		},
		{
			// #99: PS の `Set-VMFirmware -BootOrder @()` が no-change であることを実機で確認したため、
			// 空の要求値は「クリア要求」ではなく「指定なし」。ダウングレード扱いしない。
			name:          "BootSourceOrder 非空→空 は「指定なし」でダウングレードではない",
			current:       &hyperv.Msvm_VirtualSystemSettingData{BootSourceOrder: []string{"ref-1"}},
			want:          firmwareCIMValues{bootSourceOrder: nil},
			wantDowngrade: false,
		},
		{
			name:          "全て変更なしはダウングレードではない",
			current:       &hyperv.Msvm_VirtualSystemSettingData{},
			want:          firmwareCIMValues{},
			wantDowngrade: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firmwareZeroDowngrade(tt.current, tt.want); got != tt.wantDowngrade {
				t.Errorf("firmwareZeroDowngrade: got %v, want %v", got, tt.wantDowngrade)
			}
		})
	}
}

// TestApplyFirmwareSettings_RoundTrip は apply がフィールドを漏れなく反映することを検証する
// (単位変換なしの直接代入だが、フィールド追加時の反映漏れを防ぐための round-trip 確認)。
func TestApplyFirmwareSettings_RoundTrip(t *testing.T) {
	sd := &hyperv.Msvm_VirtualSystemSettingData{InstanceID: "keep-me"}
	want := firmwareCIMValues{
		secureBoot:             true,
		secureBootTemplateGUID: secureBootTemplateMicrosoftWindowsGUID,
		networkBootProtocol:    hyperv.NetworkBootPreferredProtocolIPv6,
		consoleMode:            hyperv.ConsoleModeCOM2,
		pauseAfterBootFailure:  true,
		bootSourceOrder:        []string{"ref-1", "ref-2"},
	}
	applyFirmwareSettings(sd, want)

	if sd.InstanceID != "keep-me" {
		t.Errorf("InstanceID は保持されるべき: got %q", sd.InstanceID)
	}
	if !sd.SecureBoot || sd.SecureBootTemplateId != want.secureBootTemplateGUID ||
		sd.NetworkBootPreferredProtocol != want.networkBootProtocol ||
		sd.ConsoleMode != want.consoleMode || !sd.PauseAfterBootFailure ||
		!reflect.DeepEqual(sd.BootSourceOrder, want.bootSourceOrder) {
		t.Errorf("applyFirmwareSettings: got %+v", sd)
	}
}

func TestCreateOrUpdateVmFirmwares_Guards(t *testing.T) {
	c := &ClientConfig{} // WsmanClient も埋め込み winrm も nil
	if err := c.CreateOrUpdateVmFirmwares(t.Context(), "vm", nil); err != nil {
		t.Errorf("空リストは no-op であるべき: %v", err)
	}
	if err := c.CreateOrUpdateVmFirmwares(t.Context(), "vm", []api.VmFirmware{}); err != nil {
		t.Errorf("空スライスは no-op であるべき: %v", err)
	}
	err := c.CreateOrUpdateVmFirmwares(t.Context(), "vm", []api.VmFirmware{{}, {}})
	if err == nil {
		t.Error("2 件以上はエラーを返すべき")
	}
}
