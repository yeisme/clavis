package bridge

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

func TestParseHKLID(t *testing.T) {
	cases := []struct {
		in       string
		want     uint64
		langOnly bool
		wantErr  bool
	}{
		{"0x04090409", 0x04090409, false, false},
		{"08040804", 0x08040804, false, false},
		{" 0X0804 ", 0x0804, true, false},
		{"0xE0200804", 0xE0200804, false, false},
		{"", 0, false, true},
		{"com.apple.keylayout.ABC", 0, false, true},
	}
	for _, tc := range cases {
		got, langOnly, err := ParseHKLID(tc.in)
		if (err != nil) != tc.wantErr {
			t.Fatalf("ParseHKLID(%q) err=%v, wantErr=%v", tc.in, err, tc.wantErr)
		}
		if err == nil && (got != tc.want || langOnly != tc.langOnly) {
			t.Fatalf("ParseHKLID(%q) = %#x,%v want %#x,%v", tc.in, got, langOnly, tc.want, tc.langOnly)
		}
	}
	if FormatHKLID(0x04090409) != config.WindowsDefaultInputSourceID {
		t.Fatalf("FormatHKLID mismatch with WindowsDefaultInputSourceID")
	}
}

func TestResolveInstalledHKL(t *testing.T) {
	installed := []uint64{0x08040804, 0x04090409}
	if h, err := resolveInstalledHKL(installed, "0x0409"); err != nil || h != 0x04090409 {
		t.Fatalf("langID resolve = %#x, %v", h, err)
	}
	if h, err := resolveInstalledHKL(installed, "0x08040804"); err != nil || h != 0x08040804 {
		t.Fatalf("full resolve = %#x, %v", h, err)
	}
	if _, err := resolveInstalledHKL([]uint64{0x08040804}, config.WindowsDefaultInputSourceID); err == nil {
		t.Fatalf("expected not-installed error so callers fall back to a mode switch")
	}
}

func TestWinInputSourceInfoUsesConversionMode(t *testing.T) {
	pinyin := uint64(0x08040804)
	native := winInputSourceInfo(pinyin, "", winIMEState{HKL: pinyin, ConvKnown: true, Open: true, Native: true})
	if native.Mode != host.InputModeNative || native.IsASCII || native.Language != "zh-CN" || native.Name != "HKL 0x0804" {
		t.Fatalf("native pinyin = %+v", native)
	}
	eng := winInputSourceInfo(pinyin, "中文(简体，中国)", winIMEState{HKL: pinyin, ConvKnown: true, Open: true, Native: false})
	if eng.Mode != host.InputModeASCII || !eng.IsASCII || eng.Name != "中文(简体，中国)" {
		t.Fatalf("english-mode pinyin = %+v", eng)
	}
	closed := winInputSourceInfo(pinyin, "", winIMEState{HKL: pinyin, ConvKnown: true, Open: false, Native: true})
	if closed.Mode != host.InputModeASCII {
		t.Fatalf("closed IME should be ASCII: %+v", closed)
	}
	unknown := winInputSourceInfo(pinyin, "", winIMEState{HKL: pinyin})
	if unknown.Mode != "" || unknown.IsASCII {
		t.Fatalf("unknown conversion state = %+v", unknown)
	}
	us := winInputSourceInfo(0x04090409, "", winIMEState{HKL: 0x04090409, ConvKnown: true})
	if us.Mode != "" || !us.IsASCII || us.ID != "0x04090409" {
		t.Fatalf("en-US = %+v", us)
	}
	// Non-English, non-CJK layouts are not ASCII and never get a conversion mode.
	de := winInputSourceInfo(0x04070407, "", winIMEState{HKL: 0x04070407, ConvKnown: true})
	if de.Mode != "" || de.IsASCII {
		t.Fatalf("de-DE = %+v", de)
	}
}

func TestIMEChangeTrackerReportsManualSwitches(t *testing.T) {
	var tr imeChangeTracker
	zh := host.InputSourceInfo{ID: "0x08040804", Mode: host.InputModeNative}
	en := host.InputSourceInfo{ID: "0x08040804", Mode: host.InputModeASCII, IsASCII: true}
	us := host.InputSourceInfo{ID: "0x04090409", IsASCII: true}
	steps := []struct {
		info host.InputSourceInfo
		want bool
	}{
		{zh, false}, // priming
		{zh, false},
		{en, true}, // Shift toggles Microsoft Pinyin to 英 (same HKL)
		{en, false},
		{us, true}, // Win+Space to en-US
		{zh, true},
	}
	for i, s := range steps {
		if got := tr.Observe(s.info); got != s.want {
			t.Fatalf("step %d Observe(%+v) = %v, want %v", i, s.info, got, s.want)
		}
	}
}

func TestWinIMEModeSatisfiedAndTargetHKL(t *testing.T) {
	if winIMEModeSatisfied(winIMEState{}, false) {
		t.Fatalf("unknown state must not count as satisfied")
	}
	if !winIMEModeSatisfied(winIMEState{ConvKnown: true, Open: false, Native: true}, false) {
		t.Fatalf("closed IME satisfies ASCII")
	}
	if winIMEModeSatisfied(winIMEState{ConvKnown: true, Open: false, Native: true}, true) {
		t.Fatalf("closed IME does not satisfy native")
	}
	installed := []uint64{0x08040804, 0x08090809, 0x04090409}
	if h, ok := winTargetHKLForMode(installed, false, "0x04090409"); !ok || h != 0x04090409 {
		t.Fatalf("preferred ASCII = %#x", h)
	}
	if h, ok := winTargetHKLForMode(installed, false, ""); !ok || h != 0x08090809 {
		t.Fatalf("first English = %#x", h)
	}
	if h, ok := winTargetHKLForMode(installed, true, ""); !ok || h != 0x08040804 {
		t.Fatalf("native = %#x", h)
	}
	if _, ok := winTargetHKLForMode([]uint64{0x08040804}, false, ""); ok {
		t.Fatalf("no English layout installed")
	}
}

// Windows default rules match foreground apps by process name only (no bundle IDs), and restore
// native conversion mode on leave even though Microsoft Pinyin keeps the same HKL.
func TestIMEEngineWindowsProcessNameRulesRestoreNative(t *testing.T) {
	eng := NewIMESwitchDecisionEngine(true, config.DefaultIMERulesConfigFor("windows"))
	pinyin := "0x08040804"

	d := eng.EvaluateAppActivationWithState(AppIdentity{ProcessName: "notepad.exe"}, pinyin, true)
	if d.ShouldSwitch {
		t.Fatalf("unmatched app should not switch: %+v", d)
	}
	d = eng.EvaluateAppActivationWithState(AppIdentity{ProcessName: "WindowsTerminal.exe", WindowTitle: "pwsh"}, pinyin, true)
	if !d.ShouldSwitch || d.TargetMode != config.IMEModeASCII || d.TargetInputSourceID != "" {
		t.Fatalf("WindowsTerminal.exe should switch to ASCII mode only: %+v", d)
	}
	d = eng.EvaluateAppActivationWithState(AppIdentity{ProcessName: "notepad.exe"}, pinyin, false)
	if !d.ShouldSwitch || d.TargetMode != config.IMEModeNative || !d.Restored {
		t.Fatalf("leaving should restore native mode: %+v", d)
	}
	// Case-insensitive, ".exe" optional.
	d = eng.EvaluateAppActivationWithState(AppIdentity{ProcessName: "code"}, pinyin, false)
	if !d.ShouldSwitch || d.TargetMode != config.IMEModeASCII {
		t.Fatalf("code should match Code.exe: %+v", d)
	}
	// Was already English when entering: no native restore.
	d = eng.EvaluateAppActivationWithState(AppIdentity{ProcessName: "notepad.exe"}, pinyin, false)
	if d.ShouldSwitch {
		t.Fatalf("no restore expected when the app was entered in English mode: %+v", d)
	}
}
