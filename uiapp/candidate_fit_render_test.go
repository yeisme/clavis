package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// TestCandidateHorizontalCutsOnlyPlannedTexts renders a page too wide for the
// horizontal strip (the snapshot page: five short words and a long AI
// suggestion) at the size the sizer gives it, and compares each candidate's
// drawn width with an unconstrained render. Only texts longer than the fit's
// textCap may be cut; a word that fits the cap must be drawn whole.
func TestCandidateHorizontalCutsOnlyPlannedTexts(t *testing.T) {
	view := inputengine.View{
		Composing: true,
		Preedit:   "shu'ru'fa",
		Candidates: []inputengine.Candidate{
			{Text: "输入法"}, {Text: "书如法"}, {Text: "输入"}, {Text: "舒"}, {Text: "输"},
			{Text: "输入法候选窗", AI: true},
		},
		HasNext: true,
	}
	cfg := CandidatePanelConfig{Appearance: config.DefaultAppearanceConfig()}
	cfg.Appearance.CandidateBarOrientation = "horizontal"
	defer i18n.SetLanguage(i18n.English)
	// Chinese draws the Han face (≈1.02 em a glyph), English the default
	// (≈0.99 em); the estimate and the window must hold for both, at 1× and 2×.
	for _, lang := range []i18n.Language{i18n.English, i18n.Chinese} {
		i18n.SetLanguage(lang)
		w, h := PreferredCandidatePanelSizeFor(view, cfg)
		st := resolveCandidateStyle(cfg.Style)
		fit := fitHorizontal(view, st)
		for _, scale := range []float32{1, 2} {
			fitted := ui.NewTester(func(c *ui.Context) { InputCandidateViewFor(c, view, cfg) }, w, h)
			fitted.SetScale(scale)
			fitted.Frame()
			wide := ui.NewTester(func(c *ui.Context) { InputCandidateViewFor(c, view, cfg) }, 1600, h)
			wide.SetScale(scale)
			wide.Frame()
			for _, cand := range view.Candidates {
				got, ok := fitted.Find(cand.Text)
				natural, ok2 := wide.Find(cand.Text)
				if !ok || !ok2 {
					t.Fatalf("%s: candidate %q not drawn (fitted=%v wide=%v)", lang, cand.Text, ok, ok2)
				}
				est := st.textWidth(cand.Text)
				planned := fit.textCap > 0 && est > fit.textCap
				if !planned && got.W+0.5 < natural.W {
					t.Errorf("%s %v×: %q drawn %.1fpx of its %.1fpx in a %dpx window although it fits the cap %d (estimate %d)",
						lang, scale, cand.Text, got.W, natural.W, w, fit.textCap, est)
				}
			}
		}
		t.Logf("%s: window %dx%d, cjk estimate %dpx at %vpt, textCap %d", lang, w, h, st.cjk, st.fontSize, fit.textCap)
	}
}
