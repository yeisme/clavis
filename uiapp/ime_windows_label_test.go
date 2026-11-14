package uiapp

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
)

func TestFormatIMEPillLabelConversionMode(t *testing.T) {
	cases := []struct {
		info host.InputSourceInfo
		want string
	}{
		// Windows Microsoft Pinyin: same HKL, label follows the conversion mode.
		{host.InputSourceInfo{ID: "0x08040804", Name: "中文(简体，中国)", Language: "zh-CN", Mode: host.InputModeNative}, "中"},
		{host.InputSourceInfo{ID: "0x08040804", Name: "中文(简体，中国)", Language: "zh-CN", Mode: host.InputModeASCII, IsASCII: true}, "EN"},
		{host.InputSourceInfo{ID: "0x04110411", Language: "ja-JP", Mode: host.InputModeNative}, "あ"},
		{host.InputSourceInfo{ID: "0x04090409", Name: "English (United States)", Language: "en-US", IsASCII: true}, "EN"},
		// macOS (no Mode) behavior unchanged.
		{host.InputSourceInfo{ID: "com.apple.keylayout.ABC", Name: "ABC", IsASCII: true}, "EN"},
		{host.InputSourceInfo{ID: "com.apple.inputmethod.SCIM.ITABC", Name: "Pinyin - Simplified"}, "中"},
		{host.InputSourceInfo{ID: "com.apple.keylayout.German", Name: "German"}, "German"},
	}
	for _, tc := range cases {
		if got := FormatIMEPillLabel(tc.info); got != tc.want {
			t.Fatalf("FormatIMEPillLabel(%+v) = %q, want %q", tc.info, got, tc.want)
		}
	}
	if got := FormatInputSourceStatus(cases[0].info); got != "中文(简体，中国) · 中" {
		t.Fatalf("status = %q", got)
	}
	if got := FormatInputSourceStatus(host.InputSourceInfo{ID: "com.apple.keylayout.ABC", Name: "ABC", IsASCII: true}); got != "ABC · EN" {
		t.Fatalf("status mac = %q", got)
	}
}

func TestIMERuleTargetMapping(t *testing.T) {
	if b, p := imeRuleTargetFields("Code.exe", "windows"); b != "" || p != "Code.exe" {
		t.Fatalf("windows Code.exe -> %q,%q", b, p)
	}
	if b, p := imeRuleTargetFields("com.apple.Terminal", "darwin"); b != "com.apple.Terminal" || p != "" {
		t.Fatalf("darwin bundle -> %q,%q", b, p)
	}
	if b, p := imeRuleTargetFields("WindowsTerminal", "darwin"); b != "" || p != "WindowsTerminal" {
		t.Fatalf("plain name -> %q,%q", b, p)
	}
	app := engine.InstalledAppEntry{Name: "Visual Studio Code", BundleID: `C:\Start Menu\Visual Studio Code.lnk`, Path: `C:\Users\u\AppData\Local\Programs\Microsoft VS Code\Code.exe`}
	if got := imeRuleTargetForApp(app, "windows"); got != "Code.exe" {
		t.Fatalf("windows app target = %q", got)
	}
	uwp := engine.InstalledAppEntry{Name: "Notepad", BundleID: "Microsoft.WindowsNotepad_8wekyb3d8bbwe!App", Path: `shell:AppsFolder\Microsoft.WindowsNotepad_8wekyb3d8bbwe!App`}
	if got := imeRuleTargetForApp(uwp, "windows"); got != "Notepad.exe" {
		t.Fatalf("uwp app target = %q", got)
	}
	mac := engine.InstalledAppEntry{Name: "Terminal", BundleID: "com.apple.Terminal"}
	if got := imeRuleTargetForApp(mac, "darwin"); got != "com.apple.Terminal" {
		t.Fatalf("mac app target = %q", got)
	}
}

func TestInputSourceOptionsReadableAndKeepsCurrent(t *testing.T) {
	labels, ids := inputSourceOptions([]host.InputSourceInfo{
		{ID: "0x08040804", Name: "中文(简体，中国)"},
		{ID: "0x04090409", Name: "英语(美国)"},
	}, "0x04110411")
	if len(labels) != 4 || labels[1] != "中文(简体，中国) (0x08040804)" || ids[3] != "0x04110411" || ids[0] != "" {
		t.Fatalf("labels=%v ids=%v", labels, ids)
	}
}
