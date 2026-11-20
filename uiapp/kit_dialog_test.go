package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// TestDialogsShareOneSkeleton renders the prompt composer and the first-run
// guide for real and checks the shared skeleton: the footer buttons sit in
// one band of footerHeight at the bottom, in macOS order with the primary at
// the trailing edge dialogPadX from the window's right, and the composer's
// title sits in the headerHeight title bar with no accent fill behind it.
func TestDialogsShareOneSkeleton(t *testing.T) {
	f := newInteractionFixture(t, nil)
	tpl := config.DefaultPromptsConfig().Items[0]
	f.ctrl.OpenPromptComposer(tpl, engine.CapsuleContext{AppName: "Xcode"})
	cw, ch := PreferredPromptComposerPanelSize(len(tpl.Slots))
	composer := ui.NewTester(f.ctrl.PromptComposerView, cw, ch)
	composer.Frame()
	guide := ui.NewTester(func(c *ui.Context) { f.ctrl.renderOnboarding(c, "darwin") }, 520, 520)
	guide.Frame()

	trailingGap := float32(-1)
	for _, d := range []struct {
		name    string
		tt      *ui.Tester
		w, h    int
		buttons []string // footer order, leading → trailing; the last is primary
	}{
		{"prompt composer", composer, cw, ch, []string{i18n.T("common.copy"), i18n.T("prompts.cancel"), i18n.T("prompts.insert")}},
		{"onboarding", guide, 520, 520, []string{i18n.T("onboarding.footer.settings"), i18n.T("onboarding.footer.start")}},
	} {
		var prev ui.Rect
		for i, label := range d.buttons {
			r, ok := d.tt.Find(label)
			if !ok {
				t.Fatalf("%s: footer button %q missing; texts %q", d.name, label, d.tt.Texts())
			}
			if cy := r.Y + r.H/2; float32(d.h)-cy > footerHeight {
				t.Fatalf("%s: %q centered %.1fpx above the bottom, outside the %vpx footer", d.name, label, float32(d.h)-cy, footerHeight)
			}
			if i > 0 && r.X <= prev.X {
				t.Fatalf("%s: %q (x=%.1f) is not trailing %q (x=%.1f)", d.name, label, r.X, d.buttons[i-1], prev.X)
			}
			prev = r
		}
		// Find reports the button's label box, so the gap is dialogPadX plus
		// the button's own inset; what the skeleton pins is that it is the
		// same in every dialog.
		gap := float32(d.w) - (prev.X + prev.W)
		if gap < dialogPadX {
			t.Fatalf("%s: primary label ends %.1fpx from the right edge, inside dialogPadX=%v", d.name, gap, dialogPadX)
		}
		if trailingGap < 0 {
			trailingGap = gap
		} else if diff := gap - trailingGap; diff < -1.5 || diff > 1.5 {
			t.Fatalf("%s: primary ends %.1fpx from the right edge, the first dialog %.1fpx — not one skeleton", d.name, gap, trailingGap)
		}
		t.Logf("%s: footer order %q, primary %.1fpx from the right edge", d.name, d.buttons, gap)
	}

	title, ok := composer.Find(tpl.Title)
	if !ok {
		t.Fatalf("composer title %q missing", tpl.Title)
	}
	if cy := title.Y + title.H/2; cy > headerHeight {
		t.Fatalf("composer title centered at y=%.1f, outside the %vpx title bar", cy, headerHeight)
	}
	// The title bar used to open with an accent-filled "Prompt Composer"
	// chip; now nothing in it is painted in the accent.
	img := composer.Image()
	accent := ui.LightTheme().Accent
	for y := 2; y < int(headerHeight)-2; y++ {
		for x := 2; x < cw-2; x++ {
			if px := rgbaAt(img, x, y); colorClose(px, accent) || colorClose(px, selectionFill(ui.LightTheme()).Over(ui.LightTheme().Background)) {
				t.Fatalf("accent-colored pixel %v at (%d,%d) in the composer title bar", px, x, y)
			}
		}
	}
}

// TestSelectionBarFitsItsLabels renders the bar at the window size the host
// gives it and compares every label with its width in an unconstrained
// render: none may be cut (the old equal-width rows showed "Transl…").
// Icons and digits are neutral: no accent pixels anywhere in the bar.
func TestSelectionBarFitsItsLabels(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	actions := []string{"optimize_prompt", "explain", "translate", "polish", "copy"}
	for _, lang := range []i18n.Language{i18n.English, i18n.Chinese} {
		i18n.SetLanguage(lang)
		p := SelectionBarPayload{Text: "x", Actions: actions}
		appearance := config.DefaultAppearanceConfig()
		w, h := SelectionBarSize(p)
		fitted := ui.NewTester(func(c *ui.Context) { SelectionBarView(c, p, appearance) }, w, h)
		fitted.Frame()
		wide := ui.NewTester(func(c *ui.Context) { SelectionBarView(c, p, appearance) }, 1600, h)
		wide.Frame()
		for _, action := range actions {
			label := SelectionBarActionLabel(action)
			got, ok := fitted.Find(label)
			natural, ok2 := wide.Find(label)
			if !ok || !ok2 {
				t.Fatalf("%s: label %q missing (fitted=%v wide=%v)", lang, label, ok, ok2)
			}
			if got.W+0.5 < natural.W {
				t.Fatalf("%s: %q is %.1fpx wide in a %dpx bar, %.1fpx when unconstrained — cut", lang, label, got.W, w, natural.W)
			}
			if got.X+got.W > float32(w) {
				t.Fatalf("%s: %q ends at %.1f, past the %dpx bar", lang, label, got.X+got.W, w)
			}
		}
		img := fitted.Image()
		accent := ui.LightTheme().Accent
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if px := rgbaAt(img, x, y); colorClose(px, accent) {
					t.Fatalf("%s: accent pixel %v at (%d,%d): bar icons and digits must be neutral", lang, px, x, y)
				}
			}
		}
	}
}
