package hyperv_wsman

import (
	"reflect"
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
			name:          "BootSourceOrder 非空→空 は非表現",
			current:       &hyperv.Msvm_VirtualSystemSettingData{BootSourceOrder: []string{"ref-1"}},
			want:          firmwareCIMValues{bootSourceOrder: nil},
			wantDowngrade: true,
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
