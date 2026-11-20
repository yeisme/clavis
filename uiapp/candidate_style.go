package uiapp

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// CandidatePanelConfig is everything the candidate window reads: the light/dark mode and
// the orientation from [appearance], the look from [input_engine.style]. The global visual
// preset no longer restyles the candidate window.
type CandidatePanelConfig struct {
	Appearance config.AppearanceConfig
	Style      config.InputEngineStyleConfig
}

var (
	candidateStyleMu sync.RWMutex
	candidateStyleCf = config.DefaultInputEngineStyleConfig()
)

// SetCandidateStyle publishes [input_engine.style] for the zero-config entry points
// (InputCandidateView, PreferredCandidatePanelSize), next to SetCandidateAppearance.
func SetCandidateStyle(style config.InputEngineStyleConfig) {
	candidateStyleMu.Lock()
	candidateStyleCf = style
	candidateStyleMu.Unlock()
}

// CandidateStyle returns the published [input_engine.style].
func CandidateStyle() config.InputEngineStyleConfig {
	candidateStyleMu.RLock()
	defer candidateStyleMu.RUnlock()
	return candidateStyleCf
}

// CurrentCandidatePanelConfig is the published appearance and style together.
func CurrentCandidatePanelConfig() CandidatePanelConfig {
	return CandidatePanelConfig{Appearance: CandidateAppearance(), Style: CandidateStyle()}
}

// CandidatePanelCornerRadius is the published style's corner radius, for the native panel
// window (its mask is fixed when the window is created).
func CandidatePanelCornerRadius() int {
	return int(math.Round(float64(resolveCandidateStyle(CandidateStyle()).radius)))
}

type candidateMaterial int

const (
	materialSystem candidateMaterial = iota // the system's vibrancy material
	materialGlass                           // Liquid Glass
	materialPlain                           // a fill, no material
)

// candidateStyle is [input_engine.style] resolved against its preset: every number the size
// formula and the renderer share, plus the look. Only padding, the font size and the page
// indicator reach the formula; everything else is paint.
type candidateStyle struct {
	preset     string
	material   candidateMaterial
	radius     float32 // panel corners
	cellRadius float32 // selection corners, concentric-ish with the panel's
	pad        int     // panel inner padding, both axes
	fontSize   float32 // candidate text
	cjk, latin int     // width estimate per character at fontSize
	stripH     int     // horizontal strip and cell height
	rowH       int     // vertical row height
	fill       float32 // opacity of the theme background laid over (or instead of) the material
	border     bool    // hairline outline around the panel
	separator  bool    // visible pinyin/candidate separator (its 1px is always laid out)
	accent     string  // "#RRGGBB" override, "" = theme accent
	selected   string  // fill | tint | underline
	pageIndex  bool    // page indicator shown
}

// candidatePreset is the base look of each preset; resolveCandidateStyle applies the
// [input_engine.style] overrides on top.
func candidatePreset(name string) candidateStyle {
	base := candidateStyle{preset: config.CandidateStyleNative, material: materialSystem, radius: candidatePanelRadius, pad: candHorizPad, fontSize: candFontSize, border: true, separator: true, selected: config.CandidateSelectedFill, pageIndex: true}
	switch name {
	case config.CandidateStyleGlass:
		base.preset, base.material, base.radius, base.pad, base.selected = name, materialGlass, 14, 8, config.CandidateSelectedTint
	case config.CandidateStyleFlat:
		base.preset, base.material, base.radius, base.fill, base.border, base.separator, base.selected = name, materialPlain, 6, 1, false, false, config.CandidateSelectedUnderline
	case config.CandidateStyleCustom:
		base.preset, base.material, base.fill = name, materialPlain, 0.96
	}
	return base
}

func resolveCandidateStyle(s config.InputEngineStyleConfig) candidateStyle {
	if s.Preset == "" { // never configured (a zero struct): the default look, indicator on
		s = config.DefaultInputEngineStyleConfig()
	}
	st := candidatePreset(strings.TrimSpace(s.Preset))
	if v := finite(s.Radius); v > 0 {
		st.radius = float32(clampf(v, config.StyleRadiusMin, config.StyleRadiusMax))
	}
	if v := finite(s.Padding); v > 0 {
		st.pad = int(math.Round(clampf(v, config.StylePaddingMin, config.StylePaddingMax)))
	}
	if v := finite(s.FontSize); v > 0 {
		st.fontSize = float32(clampf(v, config.StyleFontSizeMin, config.StyleFontSizeMax))
	}
	if v := finite(s.Opacity); v > 0 {
		st.fill = float32(min(v, 1))
	}
	if config.IsHexColor(s.Accent) {
		st.accent = s.Accent
	}
	switch s.SelectedStyle {
	case config.CandidateSelectedFill, config.CandidateSelectedTint, config.CandidateSelectedUnderline:
		st.selected = s.SelectedStyle
	}
	st.pageIndex = s.ShowPageIndicator
	st.cellRadius = float32(clampf(float64(st.radius)/2, 2, 10))
	// Glyph estimates. A CJK glyph draws slightly narrower than its point size in this font
	// (22pt draws about 20.9px), so the estimate sits just above the drawn width: close
	// enough that waterFillCap decisions match what drawing does, high enough never to
	// undershoot. Overestimating here is not harmless — an inflated width makes the cap
	// check believe a candidate fits, and the cell then cuts it (页数 at 19pt was the case).
	px := int(math.Round(float64(st.fontSize)))
	st.cjk = px
	// Under Chinese the panel draws through applyTextLocale's Han face, whose glyphs are
	// wider than the em (measured 1.013–1.021 em from 12 to 24pt, against 0.95–0.993 em
	// for the default face). A 1 em estimate then undershoots by ~0.3px a glyph; on a full
	// page the strip ran a few px over the window and flex-shrink cut every word
	// ("输入法" drew as "输入…"). Round that face up instead.
	if i18n.IsChinese() {
		st.cjk = int(math.Ceil(float64(st.fontSize) * cjkLocaleAdvance))
	}
	st.latin = int(math.Ceil(float64(st.fontSize) * 0.55))
	st.stripH = max(candHorizStripHeight, px+15)
	st.rowH = max(candVertRowHeight, px+13)
	return st
}

// cjkLocaleAdvance bounds the Han face's glyph advance, in em, from above.
const cjkLocaleAdvance = 1.025

func finite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func clampf(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// panelHeight is the horizontal panel's fixed height for this style.
func (st candidateStyle) panelHeight() int {
	return st.pad*2 + candPreeditHeight + candHorizRowGap*2 + candHairline + st.stripH
}

func (st candidateStyle) textWidth(s string) int {
	w := 0
	for _, r := range s {
		if runeWidthUnits(r) == 2 {
			w += st.cjk
		} else {
			w += st.latin
		}
	}
	return w
}

// navWidth estimates the page indicator ("‹ › 2/9") at typeLabel glyph by glyph, rounded up
// from what is drawn (‹› 5.3px, digits up to 7.6, "/" 3.7, space 3.4). The old fixed 18px
// box was narrower than "› 1/9" (25.6px), so the text spilled into the panel's padding.
func navWidth(text string) int {
	if text == "" {
		return 0
	}
	w := 0
	for _, r := range text {
		switch {
		case r == '‹' || r == '›':
			w += 6
		case r >= '0' && r <= '9':
			w += 8
		case r == '/':
			w += 5
		case r == ' ':
			w += 4
		default:
			w += 9
		}
	}
	return max(candNavWidth, w)
}

// candidateFit is how one horizontal page is laid out: the formula and the renderer both
// take it from fitHorizontal, so they cannot disagree.
type candidateFit struct {
	natural int    // width with nothing cut: what the window asks for, up to the cap
	width   int    // width after the steps below; <= candidatePanelMaxWidth unless nothing could give
	textCap int    // candidate texts cut to this width, 0 = uncut
	cellPad int    // horizontal padding inside each cell
	nav     string // page indicator text, "" = hidden
}

// fitHorizontal fits a page into candidatePanelMaxWidth. When it is too wide it gives up, in
// order: the longest texts (down to one common width, short ones untouched), the cell
// padding, the page numbers (arrows stay), and finally the indicator. Single characters are
// never cut: an ellipsis would replace the only glyph.
func fitHorizontal(v inputengine.View, st candidateStyle) candidateFit {
	f := candidateFit{cellPad: candCellPadX}
	if st.pageIndex {
		f.nav = candidateNavText(v)
	}
	measure := func() int {
		w := st.pad * 2
		for i, cand := range v.Candidates {
			if i > 0 {
				w += candHorizItemGap
			}
			tw := st.textWidth(cand.Text)
			if f.textCap > 0 {
				tw = min(tw, f.textCap)
			}
			w += f.cellPad*2 + candDigitWidth + candInnerGap + tw
			if cand.AI {
				w += candInnerGap + candAIMarkerWidth
			}
		}
		if f.nav != "" {
			w += candHorizItemGap*2 + navWidth(f.nav) // gaps around the spacer that pins it right
		}
		return w
	}
	f.width = measure()
	f.natural = f.width
	if f.width <= candidatePanelMaxWidth {
		return f
	}
	f.textCap = waterFillCap(v, st, f.width-candidatePanelMaxWidth)
	if f.width = measure(); f.width <= candidatePanelMaxWidth {
		return f
	}
	f.cellPad = candCellPadX / 2
	if f.width = measure(); f.width <= candidatePanelMaxWidth {
		return f
	}
	if f.nav != "" {
		f.nav = candidateNavArrows(v)
		if f.width = measure(); f.width <= candidatePanelMaxWidth {
			return f
		}
		f.nav = ""
		f.width = measure()
	}
	return f
}

// waterFillCap is the common width the longest texts are cut to so that excess px are
// freed: the longest give up room first and short ones keep their full width.
func waterFillCap(v inputengine.View, st candidateStyle, excess int) int {
	minCap := 2 * st.cjk // a character and the ellipsis
	widths := make([]int, len(v.Candidates))
	for i, cand := range v.Candidates {
		widths[i] = st.textWidth(cand.Text)
	}
	slices.SortFunc(widths, func(a, b int) int { return b - a })
	sum := 0
	for k := 1; k <= len(widths); k++ {
		sum += widths[k-1]
		// The -2 is slack: textWidth estimates glyphs from style metrics while drawing uses
		// real font metrics, and a cap that lands exactly on a candidate's estimated width
		// leaves it a pixel or two short when drawn (页数 at 19pt, medium weight).
		capW := (sum-excess)/k - 2 // cutting the k longest to capW frees at least excess
		if k == len(widths) || capW >= widths[k] {
			return max(capW, minCap)
		}
	}
	return minCap
}

// candidateNavArrows is the indicator without page numbers, for a page too wide for them.
func candidateNavArrows(v inputengine.View) string {
	switch {
	case v.HasPrev && v.HasNext:
		return "‹ ›"
	case v.HasPrev:
		return "‹"
	case v.HasNext:
		return "›"
	}
	return strconv.Itoa(v.Page + 1)
}

// candidateTheme is the theme the panel paints with: the style's accent, if any, replaces the
// theme's. Selection ink stays white, as on macOS selected rows, while it reads at 3:1 on
// the accent; a light accent (yellow, green) gets near-black ink instead.
func candidateTheme(base *ui.Theme, st candidateStyle) *ui.Theme {
	accent, ok := parseHexColor(st.accent)
	if !ok {
		return base
	}
	t := *base
	t.Accent = accent
	t.AccentText = ui.RGB(255, 255, 255)
	if contrastRatio(t.AccentText, accent) < minGlyphContrast {
		t.AccentText = ui.RGB(24, 24, 27)
	}
	return &t
}

// parseHexColor reads "#RGB" / "#RRGGBB" without ui.Hex's panic on bad input.
func parseHexColor(s string) (ui.Color, bool) {
	if !config.IsHexColor(s) {
		return ui.Color{}, false
	}
	h := s[1:]
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return ui.Color{}, false
	}
	return ui.RGB(uint8(n>>16), uint8(n>>8), uint8(n)), true
}
