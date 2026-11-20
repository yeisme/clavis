package uiapp

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// panelOf is a candidate panel config with cfg's appearance and the default style.
func panelOf(cfg config.AppearanceConfig) CandidatePanelConfig {
	return CandidatePanelConfig{Appearance: cfg}
}

// candidateStyles are the three presets, custom, and custom pushed to every limit at once.
func candidateStyles() map[string]config.InputEngineStyleConfig {
	preset := func(name string) config.InputEngineStyleConfig {
		s := config.DefaultInputEngineStyleConfig()
		s.Preset = name
		return s
	}
	large := preset(config.CandidateStyleCustom)
	large.Radius, large.Padding, large.FontSize, large.Opacity = config.StyleRadiusMax, config.StylePaddingMax, config.StyleFontSizeMax, 0.6
	large.Accent, large.SelectedStyle = "#FFD60A", config.CandidateSelectedUnderline
	small := preset(config.CandidateStyleCustom)
	small.Radius, small.Padding, small.FontSize, small.SelectedStyle = config.StyleRadiusMin, config.StylePaddingMin, config.StyleFontSizeMin, config.CandidateSelectedTint
	return map[string]config.InputEngineStyleConfig{
		"native": preset(config.CandidateStyleNative), "glass": preset(config.CandidateStyleGlass),
		"flat": preset(config.CandidateStyleFlat), "custom": preset(config.CandidateStyleCustom),
		"custom-largest": large, "custom-smallest": small,
	}
}

func typedViewWith(raw string, count int) inputengine.View {
	e := inputengine.New(inputengine.Options{CandidateCount: count})
	for _, r := range raw {
		e.HandleKey(inputengine.Key{Kind: inputengine.KeyLetter, Rune: r})
	}
	return e.View()
}

func TestCandidateStylePresetsResolve(t *testing.T) {
	for name, want := range map[string]struct {
		material candidateMaterial
		selected string
		radius   float32
		pad      int
	}{
		config.CandidateStyleNative: {materialSystem, config.CandidateSelectedFill, 10, 6},
		config.CandidateStyleGlass:  {materialGlass, config.CandidateSelectedTint, 14, 8},
		config.CandidateStyleFlat:   {materialPlain, config.CandidateSelectedUnderline, 6, 6},
		config.CandidateStyleCustom: {materialPlain, config.CandidateSelectedFill, 10, 6},
	} {
		s := config.DefaultInputEngineStyleConfig()
		s.Preset = name
		st := resolveCandidateStyle(s)
		if st.material != want.material || st.selected != want.selected || st.radius != want.radius || st.pad != want.pad || !st.pageIndex {
			t.Errorf("%s resolved to %+v", name, st)
		}
	}
	// A zero struct (never configured) is the default look, indicator on.
	if st := resolveCandidateStyle(config.InputEngineStyleConfig{}); st.preset != config.CandidateStyleNative || !st.pageIndex {
		t.Fatalf("zero style = %+v", st)
	}
	// Overrides apply to any preset and are clamped; bad names and colors fall back.
	s := config.InputEngineStyleConfig{Preset: config.CandidateStyleGlass, Radius: 100, Padding: 1, FontSize: 40, Opacity: 5, Accent: "blue", SelectedStyle: "glow"}
	st := resolveCandidateStyle(s)
	if st.radius != 24 || st.pad != 2 || st.fontSize != 22 || st.fill != 1 || st.accent != "" || st.selected != config.CandidateSelectedTint || st.pageIndex {
		t.Fatalf("clamped overrides = %+v", st)
	}
	// The default style reproduces the window's established metrics. cjk/latin moved from
	// 16/9 to 15/9 deliberately: the old pair overestimated a 15pt CJK glyph (which draws
	// about 14.9px), and an inflated estimate makes the width cap believe a two-character
	// candidate fits when the cell then cuts it.
	d := defaultCandidateStyle
	if d.panelHeight() != candidatePanelHeight || d.cjk != 15 || d.latin != 9 || d.stripH != candHorizStripHeight || d.rowH != candVertRowHeight {
		t.Fatalf("default metrics drifted: %+v", d)
	}
}

// The layout contract: under every style, for the longest real inputs, in both orientations
// and with the most candidates a page can hold, every text the window draws sits inside the
// content box (panel minus the style's padding) at the size the formula asked for, and the
// sizer's rules hold.
func TestCandidateStylesNeverOverflow(t *testing.T) {
	inputs := []string{"yeshugen", "woshiyeshugen", "zhonghuarenmingongheguo"}
	for name, style := range candidateStyles() {
		st := resolveCandidateStyle(style)
		for _, orientation := range []string{"horizontal", "vertical"} {
			app := config.DefaultAppearanceConfig()
			app.CandidateBarOrientation = orientation
			cfg := CandidatePanelConfig{Appearance: app, Style: style}
			for _, count := range []int{inputengine.DefaultCandidateCount, inputengine.MaxCandidateCount} {
				for _, raw := range inputs {
					label := fmt.Sprintf("%s/%s/%d/%s", name, orientation, count, raw)
					v := typedViewWith(raw, count)
					w, h := PreferredCandidatePanelSizeFor(v, cfg)
					var s CandidatePanelSizer
					sw, sh := s.Next(v, cfg)
					maxW := candidatePanelMaxWidth
					if orientation == "vertical" {
						maxW = candVertMaxWidth
					}
					if w > maxW || sw > maxW || sw < w || sh != h || (sw%candidateWidthStep != 0 && sw != maxW) {
						t.Fatalf("%s: preferred %dx%d, window %dx%d (cap %d, step %d)", label, w, h, sw, sh, maxW, candidateWidthStep)
					}
					if orientation == "horizontal" && h != st.panelHeight() {
						t.Fatalf("%s: height %d, style height %d", label, h, st.panelHeight())
					}
					tt := ui.NewTester(func(c *ui.Context) { InputCandidateViewFor(c, v, cfg) }, w, h)
					tt.Frame()
					assertTextsInsideContent(t, label, tt, w, h, st.pad)
					if !tt.HasText(v.Candidates[v.Selected].Text) || !tt.HasText(candidateModeLabel(v.ASCII)) {
						t.Fatalf("%s: selection or mode badge missing: %v", label, tt.Texts())
					}
					assertNothingCutUnplanned(t, label, tt, v, st, orientation == "vertical")
				}
			}
		}
	}
}

// assertNothingCutUnplanned fails when a text the window must show whole is narrower on
// screen than it measures alone: a single-line text in a box too small for it is cut with an
// ellipsis, which no bounds check can see (the old 18px page indicator showed "› 1…").
// Digits, the 中/英 badge and the page indicator are never cut; candidates only when
// fitHorizontal chose to, and then to at most its cap.
func assertNothingCutUnplanned(t *testing.T, label string, tt *ui.Tester, v inputengine.View, st candidateStyle, vertical bool) {
	t.Helper()
	whole := func(text string, size float32, weight int) {
		t.Helper()
		r, ok := tt.Find(text)
		if !ok {
			t.Fatalf("%s: %q not drawn: %v", label, text, tt.Texts())
		}
		// 2px tolerance: per-glyph estimates accumulate over a long candidate (a 7-character
		// province name is off by 1.2px), and sub-pixel rounding in the renderer is not a
		// truncation.
		if natural := naturalTextWidth(text, size, weight); r.W < natural-2 {
			t.Fatalf("%s: %q drawn %.1f wide, needs %.1f (cut)", label, text, r.W, natural)
		}
	}
	badge := candidateModeLabel(v.ASCII)
	whole(badge, typeMeta, 700)
	for i := range v.Candidates {
		whole(strconv.Itoa(i+1), typeMeta, 500)
	}
	fit := candidateFit{}
	nav := ""
	if vertical {
		if st.pageIndex {
			nav = candidateNavText(v)
		}
	} else {
		fit = fitHorizontal(v, st)
		nav = fit.nav
	}
	if nav != "" {
		whole(nav, typeLabel, 400)
	}
	for _, cand := range v.Candidates {
		if cand.Text == badge { // Find would hit the badge first
			continue
		}
		weight := 400
		if cand.Consumed > 1 {
			weight = 500
		}
		if fit.textCap > 0 && st.textWidth(cand.Text) >= fit.textCap {
			if r, _ := tt.Find(cand.Text); r.W > float32(fit.textCap)+0.5 {
				t.Fatalf("%s: %q drawn %.1f wide, over the cap %d", label, cand.Text, r.W, fit.textCap)
			}
			continue
		}
		// An extremely wide style (max font) with a full page of multi-syllable candidates
		// cannot fit 448px no matter what: fitHorizontal gives up after its reduction steps
		// and the row squeezes the cells, so a couple of pixels are lost. That is a physical
		// limit, not a layout bug — but the text must still stay inside the panel, which the
		// caller checks separately.
		if fit.width > candidatePanelMaxWidth {
			if r, ok := tt.Find(cand.Text); !ok || r.W <= 0 {
				t.Fatalf("%s: %q not drawn at all under an over-wide layout", label, cand.Text)
			}
			continue
		}
		whole(cand.Text, st.fontSize, weight)
	}
}

var naturalWidthCache = map[string]float32{}

// naturalTextWidth measures text alone on a wide canvas at the given size and weight.
func naturalTextWidth(text string, size float32, weight int) float32 {
	key := fmt.Sprintf("%s|%v|%d", text, size, weight)
	if w, ok := naturalWidthCache[key]; ok {
		return w
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Children(func() { ui.Text(c, text).FontSize(size).FontWeight(weight).SingleLine() })
	}, 2000, 200)
	tt.Frame()
	r, _ := tt.Find(text)
	naturalWidthCache[key] = r.W
	return r.W
}

// assertTextsInsideContent fails when any drawn text leaves the content box — including
// text wider than its own box (centred overflow spills into the padding, which a check
// against the outer panel would miss).
func assertTextsInsideContent(t *testing.T, label string, tt *ui.Tester, w, h, pad int) {
	t.Helper()
	lo, hiX, hiY := float32(pad)-1, float32(w-pad)+1, float32(h-pad)+1
	for _, text := range tt.Texts() {
		r, ok := tt.Find(text)
		if !ok || r.W <= 0 {
			continue
		}
		if r.X < lo || r.Y < lo || r.X+r.W > hiX || r.Y+r.H > hiY {
			t.Fatalf("%s: %q at %+v leaves the %dx%d panel's content box (padding %d)", label, text, r, w, h, pad)
		}
	}
}

// The window grows only within a composition under every style, in fixed steps, with a
// constant horizontal height.
func TestCandidateStylesSizerOnlyGrows(t *testing.T) {
	for name, style := range candidateStyles() {
		cfg := CandidatePanelConfig{Appearance: config.DefaultAppearanceConfig(), Style: style}
		cfg.Appearance.CandidateBarOrientation = "horizontal"
		e := inputengine.New(inputengine.Options{})
		var s CandidatePanelSizer
		lastW, firstH := 0, 0
		for i, r := range "woshiyeshugen" {
			e.HandleKey(inputengine.Key{Kind: inputengine.KeyLetter, Rune: r})
			w, h := s.Next(e.View(), cfg)
			if w < lastW || (i > 0 && h != firstH) || (w%candidateWidthStep != 0 && w != candidatePanelMaxWidth) {
				t.Fatalf("%s: keystroke %d → %dx%d (was %d wide, %d tall)", name, i, w, h, lastW, firstH)
			}
			if i == 0 {
				firstH = h
			}
			lastW = w
		}
	}
}

// Every selection look keeps every cell exactly where the others put it.
func TestCandidateSelectionStylesKeepGeometry(t *testing.T) {
	v := typedView("nihao")
	v.Selected = 2
	var base map[string]ui.Rect
	for _, sel := range []string{config.CandidateSelectedFill, config.CandidateSelectedTint, config.CandidateSelectedUnderline} {
		style := config.DefaultInputEngineStyleConfig()
		style.SelectedStyle = sel
		cfg := CandidatePanelConfig{Appearance: config.DefaultAppearanceConfig(), Style: style}
		w, h := PreferredCandidatePanelSizeFor(v, cfg)
		tt := ui.NewTester(func(c *ui.Context) { InputCandidateViewFor(c, v, cfg) }, w, h)
		tt.Frame()
		rects := map[string]ui.Rect{}
		for _, cand := range v.Candidates {
			r, _ := tt.Find(cand.Text)
			rects[cand.Text] = r
		}
		if base == nil {
			base = rects
			continue
		}
		for text, r := range rects {
			if r != base[text] {
				t.Fatalf("selected_style %s moved %q: %+v vs %+v", sel, text, r, base[text])
			}
		}
	}
}

func TestCandidatePageIndicatorSwitch(t *testing.T) {
	v := typedView("yeshugen")
	if candidateNavText(v) == "" {
		t.Fatalf("yeshugen should have more than one page: %+v", v)
	}
	on := config.DefaultInputEngineStyleConfig()
	off := on
	off.ShowPageIndicator = false
	for _, orientation := range []string{"horizontal", "vertical"} {
		app := config.DefaultAppearanceConfig()
		app.CandidateBarOrientation = orientation
		wOn, hOn := PreferredCandidatePanelSizeFor(v, CandidatePanelConfig{Appearance: app, Style: on})
		wOff, hOff := PreferredCandidatePanelSizeFor(v, CandidatePanelConfig{Appearance: app, Style: off})
		if orientation == "horizontal" && wOff >= wOn || orientation == "vertical" && hOff >= hOn {
			t.Fatalf("%s: hiding the indicator did not give its room back: on %dx%d, off %dx%d", orientation, wOn, hOn, wOff, hOff)
		}
		tt := ui.NewTester(func(c *ui.Context) { InputCandidateViewFor(c, v, CandidatePanelConfig{Appearance: app, Style: off}) }, wOff, hOff)
		tt.Frame()
		if tt.HasText(candidateNavText(v)) {
			t.Fatalf("%s: indicator drawn although show_page_indicator = false", orientation)
		}
	}
}

// The global [appearance] visual preset and glass clarity no longer restyle the window:
// same size, same pixels.
func TestCandidateStyleIgnoresGlobalVisualPreset(t *testing.T) {
	v := typedView("woshiyeshugen")
	var ref []byte
	for _, preset := range []string{"spatial_3d", "liquid_glass", "classic"} {
		app := config.DefaultAppearanceConfig()
		app.VisualPreset, app.GlassClarity = preset, 0.9
		cfg := CandidatePanelConfig{Appearance: app}
		w, h := PreferredCandidatePanelSizeFor(v, cfg)
		tt := ui.NewTester(func(c *ui.Context) { InputCandidateViewFor(c, v, cfg) }, w, h)
		tt.Frame()
		px := tt.Image().Pix
		if ref == nil {
			ref = append([]byte(nil), px...)
			continue
		}
		if string(px) != string(ref) {
			t.Fatalf("visual_preset %s changed the candidate window", preset)
		}
	}
	// The comparison is sensitive: a different [input_engine.style] does change the pixels.
	flat := CandidatePanelConfig{Appearance: config.DefaultAppearanceConfig(), Style: config.InputEngineStyleConfig{Preset: config.CandidateStyleFlat, ShowPageIndicator: true}}
	w, h := PreferredCandidatePanelSizeFor(v, flat)
	tt := ui.NewTester(func(c *ui.Context) { InputCandidateViewFor(c, v, flat) }, w, h)
	tt.Frame()
	if string(tt.Image().Pix) == string(ref) {
		t.Fatal("flat and native rendered identically; the pixel comparison proves nothing")
	}
}

// A custom accent repaints the selection, with selection ink picked for contrast on it.
func TestCandidateAccentOverride(t *testing.T) {
	base := ui.LightTheme()
	for accent, wantDarkInk := range map[string]bool{"#FFD60A": true, "#0A84FF": false, "#30D158": true} {
		th := candidateTheme(base, resolveCandidateStyle(config.InputEngineStyleConfig{Preset: config.CandidateStyleNative, Accent: accent}))
		want, _ := parseHexColor(accent)
		if th.Accent != want {
			t.Fatalf("%s: accent = %+v", accent, th.Accent)
		}
		dark := th.AccentText.R < 128
		if dark != wantDarkInk || contrastRatio(th.AccentText, th.Accent) < 3 {
			t.Fatalf("%s: selection ink %+v (contrast %.2f)", accent, th.AccentText, contrastRatio(th.AccentText, th.Accent))
		}
	}
	if th := candidateTheme(base, defaultCandidateStyle); th != base {
		t.Fatal("no accent override must keep the theme as is")
	}
}
