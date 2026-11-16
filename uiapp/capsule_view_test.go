package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
)

func TestOmniboxFooterTogglesTrackOpenState(t *testing.T) {
	ctrl := NewAppController(Options{})
	defer ctrl.Close()
	cfg := ctrl.Config()
	capsuleUI.reset(ctrl)
	if !ctrl.previewPaneVisible(cfg) {
		t.Fatal("preview pane should start from the appearance default")
	}
	if got := capsuleUI.togglePreview(ctrl, cfg.Main.Appearance.ShowPreviewPane); got {
		t.Fatal("preview toggle should close an initially open pane")
	}
	if ctrl.previewPaneVisible(cfg) {
		t.Fatal("preview pane remained open after toggle")
	}
	if ctrl.omniboxActionMenuOpen(engine.State{OmniboxItems: []engine.OmniboxItem{{Title: "one"}}}) {
		t.Fatal("action menu unexpectedly open")
	}
	ctrl.toggleOmniboxActionMenu()
	if !ctrl.omniboxActionMenuOpen(engine.State{OmniboxItems: []engine.OmniboxItem{{Title: "one"}}}) {
		t.Fatal("action menu did not open")
	}
}

func TestCapsuleHeaderRendersAtMinimumWidth(t *testing.T) {
	ctrl := NewAppController(Options{})
	defer ctrl.Close()
	cfg := ctrl.Config()
	phases := []engine.CapsulePhase{engine.PhaseLeader, engine.PhaseOmnibox, engine.PhaseAIStreaming, engine.PhaseHUD}
	for _, dark := range []bool{false, true} {
		for _, phase := range phases {
			t.Run(map[bool]string{false: "light", true: "dark"}[dark]+"/"+string(phase), func(t *testing.T) {
				state := engine.State{Phase: phase, OmniboxTab: config.TabActions, HUDMessage: "Ready", Context: engine.CapsuleContext{AppName: "TextEdit"}, AIMode: "ask"}
				tt := ui.NewTester(func(c *ui.Context) {
					c.SetTheme(map[bool]*ui.Theme{false: ui.LightTheme(), true: ui.DarkTheme()}[dark])
					ctrl.renderCapsuleHeader(c, c.Theme(), cfg, state, DiagnosticsSnapshot{ActiveInputSource: host.InputSourceInfo{ID: "abc", Name: "ABC"}})
				}, 420, int(headerHeight))
				tt.Frame()
				if shades := distinctShades(tt.Image()); shades < 4 {
					t.Fatalf("phase %s dark=%v rendered only %d shades", phase, dark, shades)
				}
			})
		}
	}
}
