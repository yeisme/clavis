package bridge

import (
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"testing"
)

func TestTriggerRuntimeShortcutAndInvalidConfig(t *testing.T) {
	r := newTriggerRuntime()
	r.setShortcut("option+tab")
	if !r.shortcutMatches("tab", []string{"alt"}) {
		t.Fatal("expected option+tab match")
	}
	if r.shortcutMatches("tab", []string{"alt", "shift"}) {
		t.Fatal("extra modifier must not match")
	}
	r.setShortcut("not-a-real-accelerator")
	if r.shortcutMatches("tab", []string{"option"}) {
		t.Fatal("invalid accelerator must be disabled")
	}
}

func TestTriggerRuntimeDoubleShiftPrecedence(t *testing.T) {
	r := newTriggerRuntime()
	r.setSelection(config.SelectionBarConfig{Enabled: true, TriggerMode: "double_shift"})
	if r.doubleShift("shift", true, false, 100, false) || r.doubleShift("shift", false, false, 140, false) {
		t.Fatal("first tap fired")
	}
	if r.doubleShift("shift", true, false, 200, false) {
		t.Fatal("second down fired")
	}
	if !r.doubleShift("shift", false, false, 240, false) {
		t.Fatal("double shift did not fire")
	}
	r.setSelection(config.SelectionBarConfig{Enabled: true, TriggerMode: "double_shift"})
	r.doubleShift("shift", true, false, 300, false)
	r.doubleShift("shift", false, false, 320, false)
	r.doubleShift("shift", true, false, 350, true)
	if r.doubleShift("shift", false, false, 370, true) {
		t.Fatal("composition must suppress selection trigger")
	}
}

func TestTriggerPrecedence(t *testing.T) {
	if got := resolveTriggerPrecedence(true, true); got != host.TriggerInPlaceOptimize {
		t.Fatalf("optimize must outrank selection: %q", got)
	}
	if got := resolveTriggerPrecedence(false, true); got != host.TriggerSelectionQuickBar {
		t.Fatalf("selection precedence: %q", got)
	}
	if got := resolveTriggerPrecedence(false, false); got != "" {
		t.Fatalf("unexpected trigger: %q", got)
	}
}

func TestTriggerRuntimeSelectionDisabled(t *testing.T) {
	r := newTriggerRuntime()
	r.setSelection(config.SelectionBarConfig{Enabled: false, TriggerMode: "auto_select"})
	if r.doubleShift("shift", true, false, 1, false) || r.doubleShift("shift", false, false, 2, false) {
		t.Fatal("disabled selection fired")
	}
}

func TestBindingAppMatchesNormalizesWindowsExeSuffix(t *testing.T) {
	cases := []struct {
		name string
		ids  []string
		app  string
		want bool
	}{
		{"empty ids matches any app", nil, "Code.exe", true},
		{"wildcard matches process", []string{"*"}, "Code.exe", true},
		{"bare name matches .exe", []string{"code"}, "Code.exe", true},
		{".exe matches bare name", []string{"Code.exe"}, "code", true},
		{"mixed case .EXE", []string{"WindowsTerminal.EXE"}, "windowsterminal.exe", true},
		{"macOS bundle id exact match", []string{"com.apple.MobileSMS"}, "com.apple.MobileSMS", true},
		{"empty foreground rejected when restricted", []string{"Code.exe"}, "", false},
		{"unlisted app rejected", []string{"Code.exe"}, "notepad.exe", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bindingAppMatches(tc.ids, tc.app); got != tc.want {
				t.Fatalf("bindingAppMatches(%v, %q) = %v, want %v", tc.ids, tc.app, got, tc.want)
			}
		})
	}

	r := newTriggerRuntime()
	r.setKeyBindings([]config.KeyBinding{{
		Keys:         []string{"ctrl+alt+o"},
		Action:       "ai_optimize",
		AppBundleIDs: []string{"Code"},
	}})
	if act, ok := r.bindingFor("o", []string{"control", "option"}, "Code.exe"); !ok || act != "ai_optimize" {
		t.Fatalf("bindingFor with .exe process = (%q, %v), want (ai_optimize, true)", act, ok)
	}
}
