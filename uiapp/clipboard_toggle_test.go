package uiapp

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func TestController_ClipboardToggleOpensClipboardTabAndCloses(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)

	ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerClipboardToggle})
	st := ctrl.State()
	if st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabClipboard {
		t.Fatalf("expected Omnibox on Clipboard tab, got phase=%s tab=%s", st.Phase, st.OmniboxTab)
	}

	ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerClipboardToggle})
	if ctrl.State().Phase != engine.PhaseHidden {
		t.Fatalf("second clipboard toggle should dismiss, got %s", ctrl.State().Phase)
	}

	// From another Omnibox tab the hotkey jumps to Clipboard instead of closing.
	ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerCapsuleToggle})
	if st := ctrl.State(); st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabActions {
		t.Fatalf("expected Actions tab via capsule toggle, got phase=%s tab=%s", st.Phase, st.OmniboxTab)
	}
	ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerClipboardToggle})
	if st := ctrl.State(); st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabClipboard {
		t.Fatalf("expected switch to Clipboard tab, got phase=%s tab=%s", st.Phase, st.OmniboxTab)
	}
	ctrl.Dispatch(engine.Event{Type: engine.EventDismiss})
}

func TestClipboardShortcutI18nKeys(t *testing.T) {
	prev := i18n.Current()
	defer i18n.SetLanguage(prev)
	keys := []string{
		"settings.trigger.clipboard_shortcut",
		"diag.trigger.clipboard_shortcut.invalid",
		"tray.clipboard_history",
	}
	values := map[i18n.Language]map[string]string{}
	for _, lang := range []i18n.Language{i18n.English, i18n.Chinese} {
		i18n.SetLanguage(lang)
		values[lang] = map[string]string{}
		for _, key := range keys {
			got := i18n.T(key)
			if got == "" || got == key {
				t.Fatalf("missing %s translation for %q", lang, key)
			}
			values[lang][key] = got
		}
	}
	// zh must be a real translation, not the English fallback.
	for _, key := range keys {
		if values[i18n.English][key] == values[i18n.Chinese][key] {
			t.Fatalf("zh-CN translation for %q falls back to English", key)
		}
	}
	if missing := i18n.MissingKeys(); len(missing) > 0 {
		t.Fatalf("i18n tables drifted: %v", missing)
	}
}

func TestShortcutRecorderUsesRuntimeKeyNames(t *testing.T) {
	cases := []struct {
		key  ui.Key
		want string
	}{
		{ui.KeyBackspace, "backspace"},
		{ui.KeyDelete, "forwarddelete"},
		{ui.KeyPageUp, "pageup"},
		{ui.KeyPageDown, "pagedown"},
		{ui.KeyF1, "f1"},
		{ui.KeyF12, "f12"},
		{ui.KeyMinus, "minus"},
		{ui.KeySemicolon, "semicolon"},
		{ui.KeySlash, "slash"},
	}
	for _, tc := range cases {
		got, ok := shortcutFromKey(tc.key, ui.Cmd|ui.Shift)
		if !ok || got != "command+shift+"+tc.want {
			t.Errorf("shortcutFromKey(%v) = %q, %v; want command+shift+%s", tc.key, got, ok, tc.want)
		}
	}
}

func TestSettingsCenter_ClipboardShortcutRecorder(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()
	if !tt.HasText("Clipboard History Shortcut") || !tt.HasText("clipboard_shortcut: alt+shift+c") {
		t.Fatalf("expected clipboard_shortcut row on General & Triggers, got %v", tt.Texts())
	}

	// Arm only the clipboard recorder; the capsule shortcut must stay untouched.
	seSet(ctrl.settingsEditor(), func(s *settingsEditorState) { s.recordingShortcut = "clipboard_shortcut" })
	tt.Frame()
	tt.Frame()
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyV)
	tt.Frame()
	tt.Frame()
	got := ctrl.Config().Main.Trigger
	if got.ClipboardShortcut == "alt+shift+c" || got.ClipboardShortcut == "" {
		t.Fatalf("clipboard_shortcut not recorded, got %q", got.ClipboardShortcut)
	}
	if got.CapsuleShortcut != "option+space" {
		t.Fatalf("capsule_shortcut changed to %q while recording clipboard_shortcut", got.CapsuleShortcut)
	}
	if body := readConfigFile(t, ctrl, config.FileMain); !strings.Contains(body, `clipboard_shortcut = "`+got.ClipboardShortcut+`"`) {
		t.Fatalf("config.toml does not persist clipboard_shortcut:\n%s", body)
	}
}
