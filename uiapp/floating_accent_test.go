package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// accentPixels counts pixels within [x0,x1)×[y0,y1) close to accent.
func accentPixels(tt *ui.Tester, x0, y0, x1, y1 int, accent ui.Color) int {
	img := tt.Image()
	b := img.Bounds()
	n := 0
	for y := max(y0, b.Min.Y); y < min(y1, b.Max.Y); y++ {
		for x := max(x0, b.Min.X); x < min(x1, b.Max.X); x++ {
			if colorClose(rgbaAt(img, x, y), accent) {
				n++
			}
		}
	}
	return n
}

// TestFloatingSurfacesKeepAccentForSelection renders the floating windows for
// real and reads pixels: the accent marks selection only. The Leader title
// bar, the layer pill and an unselected AI suggestion's ✦ carry no accent;
// the IME pill's live segment and the chosen candidate still do.
func TestFloatingSurfacesKeepAccentForSelection(t *testing.T) {
	accent := ui.LightTheme().Accent

	f := newInteractionFixture(t, nil)
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader, Context: &engine.CapsuleContext{AppName: "Xcode"}})
	lw, lh := PreferredCapsuleSizeForState(f.ctrl.State(), f.ctrl.Config().Main.Appearance)
	leader := ui.NewTester(f.ctrl.CapsuleView, lw, lh)
	leader.Frame()
	if _, ok := leader.Find("Clavis"); !ok {
		t.Fatalf("Leader title bar lost its brand; texts %q", leader.Texts())
	}
	if n := accentPixels(leader, 0, 0, lw, int(headerHeight), accent); n != 0 {
		t.Fatalf("Leader title bar paints %d accent pixels; the brand chip must be neutral", n)
	}

	pill := func(label string) *ui.Tester {
		w, h := PreferredPillSizeForLabel(label)
		tt := ui.NewTester(func(c *ui.Context) { IMEPillView(c, label) }, w, h)
		tt.Frame()
		return tt
	}
	if lp := pill("LAYER • NAV"); accentPixels(lp, 0, 0, 1000, 1000, accent) != 0 {
		t.Fatal("layer pill paints its icon in the accent; it is status, not selection")
	}
	if ip := pill("中"); accentPixels(ip, 0, 0, 1000, 1000, accent) == 0 {
		t.Fatal("IME pill lost the accent on its live segment (that one is selection)")
	}

	view := inputengine.View{
		Composing: true, Preedit: "shu'ru'fa",
		Candidates: []inputengine.Candidate{{Text: "输入法"}, {Text: "书如法"}, {Text: "输入法候选窗", AI: true}},
		Selected:   0,
	}
	cfg := CandidatePanelConfig{}
	cfg.Appearance.CandidateBarOrientation = "vertical"
	cw, ch := PreferredCandidatePanelSizeFor(view, cfg)
	cand := ui.NewTester(func(c *ui.Context) { InputCandidateViewFor(c, view, cfg) }, cw, ch)
	cand.Frame()
	mark, ok := cand.Find("✦")
	if !ok {
		t.Fatalf("AI marker missing; texts %q", cand.Texts())
	}
	if n := accentPixels(cand, int(mark.X), int(mark.Y), int(mark.X+mark.W)+1, int(mark.Y+mark.H)+1, accent); n != 0 {
		t.Fatalf("unselected AI suggestion's ✦ paints %d accent pixels", n)
	}
	first, _ := cand.Find("输入法")
	if accentPixels(cand, 0, int(first.Y), cw, int(first.Y+first.H), accent) == 0 {
		t.Fatal("chosen candidate lost its solid accent fill")
	}
}
