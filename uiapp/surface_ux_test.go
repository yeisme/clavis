package uiapp

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func TestSF09PromptEscapeComposingGuard(t *testing.T) {
	f := newInteractionFixture(t, nil)
	tpl := config.PromptTemplate{
		ID: "esc-guard", Trigger: "/eg", Title: "Escape Guard",
		Template: "Hello {{name}}",
		Slots: []config.PromptSlot{
			{Name: "name", Label: "Name", Kind: "text"},
		},
	}
	f.ctrl.OpenPromptComposer(tpl, engine.CapsuleContext{AppName: "Notes"})
	pw, ph := PreferredPromptComposerPanelSize(len(tpl.Slots))
	tt := ui.NewTester(f.ctrl.PromptComposerView, pw, ph)
	tt.Frame()

	f.ctrl.promptUI.isComposing = true
	if f.ctrl.surfaceEscape(SurfaceEscapePrompt) {
		t.Fatal("SF-09: surfaceEscape(Prompt) must not close while IME composing is active")
	}
	if !f.ctrl.PromptComposerOpen() {
		t.Fatal("SF-09: PromptComposer closed unexpectedly during IME preedit")
	}

	f.ctrl.promptUI.isComposing = false
	if !f.ctrl.surfaceEscape(SurfaceEscapePrompt) {
		t.Fatal("SF-09: surfaceEscape(Prompt) should close when not composing")
	}
	if f.ctrl.PromptComposerOpen() {
		t.Fatal("SF-09: PromptComposer still open after non-composing Escape")
	}
	if f.ctrl.surfaceEscape(SurfaceEscapeSettings) {
		t.Fatal("Settings Escape must never dismiss the window")
	}
}

func TestSF14AIInjectTargetAppChangedFallsBackToClipboard(t *testing.T) {
	doer := mockHTTPDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			return sseResponse("rewritten result"), nil
		},
	}
	f := newInteractionFixture(t, doer)
	f.ctrl.OpenOmnibox(config.TabAI, "rewrite this")
	f.ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "AI stream completion")

	// Simulate user ⌘Tab-ing to Safari before accepting AI output.
	f.bridge.mu.Lock()
	f.bridge.snapshot.BundleID = "com.apple.Safari"
	f.bridge.snapshot.ProcessName = "Safari"
	f.bridge.snapshot.AppName = "Safari"
	f.bridge.mu.Unlock()

	f.ctrl.Dispatch(engine.Event{Type: engine.EventAcceptAIOutput})
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return len(f.copiedTexts()) == 1 && st.Phase == engine.PhaseHUD && st.HUDMessage == i18n.T("ai.hud.target_changed_copied")
	}, "SF-14 fallback clipboard copy and HUD")

	if got := f.copiedTexts()[0]; got != "rewritten result" {
		t.Fatalf("SF-14 copied = %q, want %q", got, "rewritten result")
	}
	if inj := f.bridge.getInjected(); len(inj) != 0 {
		t.Fatalf("SF-14 injected into switched app unexpectedly: %+v", inj)
	}
	if st := f.ctrl.State(); st.Phase != engine.PhaseHUD || st.HUDMessage != i18n.T("ai.hud.target_changed_copied") {
		t.Fatalf("SF-14 HUD = (%s, %q), want HUD with %q", st.Phase, st.HUDMessage, i18n.T("ai.hud.target_changed_copied"))
	}

	// When the origin app stays frontmost, accept injects as normal.
	f2 := newInteractionFixture(t, doer)
	f2.ctrl.OpenOmnibox(config.TabAI, "rewrite again")
	f2.ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
	waitUntil(t, 2*time.Second, func() bool {
		st := f2.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "AI stream completion matching app")
	f2.ctrl.Dispatch(engine.Event{Type: engine.EventAcceptAIOutput})
	waitUntil(t, 2*time.Second, func() bool {
		return len(f2.bridge.getInjected()) == 1
	}, "matching app injection")
}

func TestSF15CornerRadiusShowsRestartHint(t *testing.T) {
	prev := i18n.Current()
	defer i18n.SetLanguage(prev)

	for _, tc := range []struct {
		name string
		lang i18n.Language
		want string
	}{
		{"en", i18n.English, "Window corner mask takes effect after restart"},
		{"zh", i18n.Chinese, "窗口边缘重启后生效"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i18n.SetLanguage(tc.lang)
			f := newInteractionFixture(t, nil)
			f.ctrl.OpenSettingsPage("appearance")
			tt := ui.NewTester(f.ctrl.SettingsView, 960, 640)
			tt.Frame()

			if tt.HasText(tc.want) {
				t.Fatalf("restart hint %q shown before corner radius change", tc.want)
			}
			// Simulate user changing corner radius via settings stepper action.
			before := f.ctrl.Config().Main.Appearance.CornerRadius
			_ = f.ctrl.UpdateMainConfig(func(m *config.MainConfig) {
				m.Appearance.Language = tc.name
				m.Appearance.CornerRadius = before + 2
			})
			seSet(f.ctrl.settingsEditor(), func(s *settingsEditorState) {
				s.cornerRadiusChanged = true
			})
			tt.Frame()
			if !strings.Contains(strings.Join(tt.Texts(), "\n"), tc.want) {
				t.Fatalf("SF-15 missing restart hint %q in texts=%v", tc.want, tt.Texts())
			}
		})
	}
}

func TestMacDefaultAccentIsSystemBlue(t *testing.T) {
	want := ui.Hex("#0A84FF")
	for _, dark := range []bool{false, true} {
		f := newInteractionFixture(t, nil)
		f.ctrl.OpenSettingsPage("appearance")
		tt := ui.NewTester(f.ctrl.SettingsView, 960, 640)
		tt.SetDark(dark)
		tt.Frame()
		_ = f.ctrl.Config()
	}
	if macAccent != want {
		t.Fatalf("macAccent = %+v, want #0A84FF (%+v)", macAccent, want)
	}
	if macLight.Accent != want {
		t.Fatalf("macLight.Accent = %+v, want #0A84FF", macLight.Accent)
	}
	if macDark.Accent != want {
		t.Fatalf("macDark.Accent = %+v, want #0A84FF", macDark.Accent)
	}
}
