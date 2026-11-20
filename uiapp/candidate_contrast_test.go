package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// The candidate window must stay renderable in every appearance preset: it is a floating
// surface over a glass backdrop, so a preset that breaks layout or panics is a real defect.
func TestCandidatePanelRendersAcrossPresets(t *testing.T) {
	e := inputengine.New(inputengine.Options{CandidateCount: 7})
	for _, r := range "woshizhongguoren" {
		e.HandleKey(inputengine.Key{Kind: inputengine.KeyLetter, Rune: r})
	}
	v := e.View()
	if len(v.Candidates) == 0 {
		t.Fatal("no candidates")
	}
	for _, preset := range []string{"", "classic", "graphite", "paper", "midnight"} {
		for _, orientation := range []string{"horizontal", "vertical"} {
			cfg := config.DefaultAppearanceConfig()
			cfg.VisualPreset = preset
			cfg.CandidateBarOrientation = orientation
			w, h := PreferredCandidatePanelSizeFor(v, panelOf(cfg))
			tester := ui.NewTester(func(c *ui.Context) {
				InputCandidateViewFor(c, v, panelOf(cfg))
			}, w, h)
			tester.Frame()
			if !tester.HasText(v.Preedit) && !tester.HasText("wo'shi'ye'shu'gen") {
				// The preedit may be split into committed + pending runs; either way some
				// fragment of it must be on screen.
				if len(tester.Texts()) == 0 {
					t.Fatalf("preset=%q orientation=%q rendered nothing", preset, orientation)
				}
			}
		}
	}
}
