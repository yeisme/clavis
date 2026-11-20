package uiapp

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// Candidate window for the own input engine (docs/design/own-input-engine.md §4). Shown with
// ShowInactive so it never takes focus from the target app.
//
// It changes on every keystroke, so it stays quiet: one surface, a hairline between the
// pinyin and the candidates, small muted digits, and one clear selection. No keycaps,
// shadows or motion. The candidates set the width; the preedit never widens the window and
// is cut in the middle instead.
//
// The look comes from [input_engine.style] (candidate_style.go). A style only feeds
// parameters into the size formula below (padding, font size, page indicator); the
// formula and the renderer share them through candidateStyle and fitHorizontal, so the
// window sizes to fit under every preset and every custom value.

const (
	candidatePanelRadius = 10
	candFontSize         = 15 // default candidate text size

	// Horizontal strip geometry at the default style (must stay in 1:1 lockstep with
	// renderHorizontalCandidatePanel; candidateStyle.panelHeight is the styled form).
	candHorizPad         = 6
	candHorizRowGap      = 4
	candPreeditHeight    = 18
	candHairline         = 1
	candHorizStripHeight = 30
	candidatePanelHeight = candHorizPad*2 + candPreeditHeight + candHorizRowGap*2 + candHairline + candHorizStripHeight // 69

	// A page wider than the max cuts its longest candidates (candidateTextCap) instead of
	// widening the window past a short line of text.
	candidatePanelMinWidth = 160
	candSingleMinWidth     = 120
	// candCommittedInkMix pulls the committed pinyin run toward muted: 0 = pure text,
	// 1 = pure muted.
	candCommittedInkMix    = 0.35
	candidatePanelMaxWidth = 448

	// Vertical list geometry at the default style (must stay in 1:1 lockstep with
	// renderVerticalCandidatePanel). The padding is the style's, like the horizontal one.
	candVertPadY          = candHorizPad
	candVertGap           = 2
	candVertPreeditHeight = 18
	candVertRowHeight     = 28
	candVertNavHeight     = 16
	candVertMinWidth      = 148
	candVertMaxWidth      = 320
	candVertMinHeight     = 56
	candVertMaxHeight     = 460

	// Cell metrics shared by both orientations: digit, candidate text, optional AI ✦.
	candCellPadX       = 8
	candDigitWidth     = 8 // widest digit at typeMeta
	candInnerGap       = 4
	candHorizItemGap   = 2
	candAIMarkerWidth  = 10 // ✦ at typeMicro
	candNavWidth       = 18 // narrowest page indicator box
	candModeBadgeWidth = 28
)

// defaultCandidateStyle is the look an unconfigured window gets (preset native).
var defaultCandidateStyle = resolveCandidateStyle(config.DefaultInputEngineStyleConfig())

// PreferredCandidatePanelSize estimates the panel size for v from the published
// appearance and [input_engine.style] (SetCandidateAppearance / SetCandidateStyle).
func PreferredCandidatePanelSize(v inputengine.View) (width, height int) {
	return PreferredCandidatePanelSizeFor(v, CurrentCandidatePanelConfig())
}

// PreferredCandidatePanelSizeFor computes exact panel dimensions matching
// InputCandidateViewFor in both orientations, for cfg's style.
func PreferredCandidatePanelSizeFor(v inputengine.View, cfg CandidatePanelConfig) (width, height int) {
	st := resolveCandidateStyle(cfg.Style)
	if isCandidateVertical(cfg.Appearance) {
		return preferredVerticalCandidateSize(v, st)
	}
	return preferredHorizontalCandidateSize(v, st)
}

// The preedit takes no part in either estimate: however long the pinyin gets, it is cut
// in the middle to the width the candidates need.
func preferredHorizontalCandidateSize(v inputengine.View, st candidateStyle) (width, height int) {
	w := fitHorizontal(v, st).natural
	// A single candidate does not need the full minimum: the fixed floor left a lone
	// character rattling in a 160px box.
	floor := candidatePanelMinWidth
	if len(v.Candidates) <= 1 {
		floor = candSingleMinWidth
	}
	return min(max(w, floor), candidatePanelMaxWidth), st.panelHeight()
}

// candidateTextCap is the width the default style cuts candidate texts to, 0 when the page
// fits (see fitHorizontal).
func candidateTextCap(v inputengine.View) int { return fitHorizontal(v, defaultCandidateStyle).textCap }

// candidateTextWidth is a text's estimated width at the default style.
func candidateTextWidth(s string) int { return defaultCandidateStyle.textWidth(s) }

func preferredVerticalCandidateSize(v inputengine.View, st candidateStyle) (width, height int) {
	w := 0
	for _, cand := range v.Candidates {
		w = max(w, st.cellWidth(cand))
	}
	w = min(max(w+st.pad*2, candVertMinWidth), candVertMaxWidth)
	return w, verticalNaturalHeight(v, st, true)
}

// verticalNaturalHeight is the list's height; clamped to the window's bounds when clamp is
// set (the renderer scrolls past candVertMaxHeight).
func verticalNaturalHeight(v inputengine.View, st candidateStyle, clamp bool) int {
	h := st.pad*2 + candVertPreeditHeight + candVertGap + candHairline
	if len(v.Candidates) > 0 {
		h += len(v.Candidates) * (candVertGap + st.rowH)
	}
	if st.pageIndex && candidateNavText(v) != "" {
		h += candVertGap + candVertNavHeight
	}
	if clamp {
		h = min(max(h, candVertMinHeight), candVertMaxHeight)
	}
	return h
}

// cellWidth is the natural width of one cell (a vertical row has the same parts).
func (st candidateStyle) cellWidth(cand inputengine.Candidate) int {
	w := candCellPadX*2 + candDigitWidth + candInnerGap + st.textWidth(cand.Text)
	if cand.AI {
		w += candInnerGap + candAIMarkerWidth
	}
	return w
}

// CandidateLabel is the visible text of one cell ("1 你好", AI suggestions marked ✦).
func CandidateLabel(i int, cand inputengine.Candidate) string {
	label := strconv.Itoa(i+1) + " " + cand.Text
	if cand.AI {
		label += " ✦"
	}
	return label
}

// InputCandidateView renders the preedit line plus the current candidate page with the
// published appearance and [input_engine.style].
func InputCandidateView(c *ui.Context, v inputengine.View) {
	InputCandidateViewFor(c, v, CurrentCandidatePanelConfig())
}

// InputCandidateViewFor renders the cursor-side IME candidate panel for cfg: light/dark and
// orientation from cfg.Appearance, everything visual from cfg.Style. Typing stays on the fast
// non-shader path: one material (or fill) for the surface, opaque ink, and no animation.
func InputCandidateViewFor(c *ui.Context, v inputengine.View, cfg CandidatePanelConfig) {
	st := resolveCandidateStyle(cfg.Style)
	theme := candidateTheme(resolveAppearanceTheme(c, cfg.Appearance.Theme), st)
	c.SetTheme(theme)
	prefs := c.Preferences()
	// The window repaints on every keystroke and the selection moves on every arrow key:
	// nothing in it animates, whatever the global motion setting says.
	c.DisableAnimations()

	if !prefs.ReduceTransparency {
		c.Root().Radius(st.radius)
	}

	vertical := isCandidateVertical(cfg.Appearance)
	gap := float32(candHorizRowGap)
	if vertical {
		gap = candVertGap
	}

	box := applyTextLocale(ui.Column(c).Fill().Padding(float32(st.pad)).Gap(gap).Clip())
	applyCandidatePanelSurface(c, box, theme, st)

	box.Children(func() {
		if vertical {
			renderVerticalCandidatePanel(c, theme, v, st)
		} else {
			renderHorizontalCandidatePanel(c, theme, v, st)
		}
	})
}

// applyCandidatePanelSurface paints the style's surface. Reduce Transparency always gets an
// opaque fill; text never takes the opacity, only the surface does.
func applyCandidatePanelSurface(c *ui.Context, box ui.Element, theme *ui.Theme, st candidateStyle) {
	prefs := c.Preferences()
	box.Radius(st.radius)
	switch {
	case prefs.ReduceTransparency:
		box.Background(theme.Background).Border(1, theme.Border)
	case st.material == materialPlain:
		box.Background(theme.Background.Alpha(st.fill))
		if st.border {
			box.Border(1, hairline(theme))
		}
	default:
		g := glass.Glass{Native: c.Vibrancy()}
		edge := float32(0.55)
		if st.material == materialGlass {
			g.Style, g.Clarity, edge = glass.Regular, candidateGlassClarity, 0.32
		}
		if st.fill > 0 {
			g.Tint = theme.Background.Alpha(st.fill)
		}
		box.Material(g)
		if st.border {
			box.Border(1, withAlpha(theme.Border, edge))
		}
	}
	if prefs.HighContrast {
		box.Border(1.5, theme.Border)
	}
}

// candidateGlassClarity is the glass preset's clarity, fixed so the global
// appearance.glass_clarity no longer changes the candidate window.
const candidateGlassClarity = 0.18

func renderHorizontalCandidatePanel(c *ui.Context, theme *ui.Theme, v inputengine.View, st candidateStyle) {
	renderCandidatePreedit(c, theme, v, candPreeditHeight)
	renderCandidateSeparator(c, theme, st)

	fit := fitHorizontal(v, st)
	stripH := float32(st.stripH)
	ui.Row(c).FillWidth().Height(stripH).Gap(candHorizItemGap).AlignItems(ui.Center).Children(func() {
		for i, cand := range v.Candidates {
			active := i == v.Selected
			cell := ui.Row(c).
				Key("cand-h-"+strconv.Itoa(i)).
				Height(stripH).
				Padding(0, float32(fit.cellPad)).
				Radius(st.cellRadius).
				Gap(candInnerGap).
				AlignItems(ui.Center).
				Shrink(1).
				MinWidth(0)
			paintCandidateSelection(cell, theme, st, active)
			idx, item := i, cand
			cell.Children(func() {
				renderCandidateCellContent(c, theme, st, idx, item, active, false, fit.textCap)
				if active && st.selected == config.CandidateSelectedUnderline {
					renderCandidateUnderline(c, theme, fit.cellPad)
				}
			})
		}
		if fit.nav != "" {
			ui.Box(c).Grow(1)
			ui.Box(c).Width(float32(navWidth(fit.nav))).Height(stripH).Shrink(0).Center().Children(func() {
				ui.Text(c, fit.nav).FontSize(typeLabel).TextColor(theme.TextMuted).SingleLine()
			})
		}
	})
}

// renderCandidateSeparator is the 1px rule under the pinyin. A style without it keeps the
// pixel (transparent), so its geometry matches every other style's.
func renderCandidateSeparator(c *ui.Context, theme *ui.Theme, st candidateStyle) {
	line := ui.Box(c).FillWidth().Height(candHairline)
	if st.separator {
		line.Background(hairline(theme))
	}
}

// paintCandidateSelection marks the chosen cell. fill is a solid accent block, tint a light
// accent wash, underline a bar drawn by renderCandidateUnderline; none of them changes the
// cell's size.
func paintCandidateSelection(cell ui.Element, theme *ui.Theme, st candidateStyle, active bool) {
	if !active {
		return
	}
	switch st.selected {
	case config.CandidateSelectedTint:
		cell.Background(candidateTint(theme))
	case config.CandidateSelectedUnderline:
	default:
		cell.Background(theme.Accent)
	}
}

func candidateTint(theme *ui.Theme) ui.Color {
	if theme.Dark {
		return theme.Accent.Alpha(0.30)
	}
	return theme.Accent.Alpha(0.16)
}

// renderCandidateUnderline is the underline selection: an accent bar along the cell's
// bottom, out of the flow so the cell keeps its size.
func renderCandidateUnderline(c *ui.Context, theme *ui.Theme, inset int) {
	ui.Box(c).Absolute().Left(float32(inset)).Right(float32(inset)).Bottom(3).Height(2).Radius(1).Background(theme.Accent)
}

// renderCandidatePreedit is the pinyin line: muted, cut in the middle when it is longer
// than the candidates are wide, with the 中/英 badge at its end.
func renderCandidatePreedit(c *ui.Context, theme *ui.Theme, v inputengine.View, height float32) {
	ui.Row(c).
		FillWidth().
		Height(height).
		Padding(0, 0, 0, candCellPadX).
		Gap(candInnerGap).
		AlignItems(ui.Center).
		Children(func() {
			// The highlighted candidate may commit one syllable or the whole sentence;
			// underlining the syllables it would take makes Enter predictable.
			if head, tail := splitPreedit(v.Preedit, v.SelectedConsumed); head != "" && tail != "" {
				// Four contrast levels, weakest first: page indicator < pending pinyin <
				// committed pinyin < candidate text < selection fill. The committed run sits
				// between text and muted so it reads stronger than the rest of the pinyin but
				// never competes with the candidate it describes.
				ui.Text(c, head).
					FontSize(typeLabel).
					TextColor(theme.Text.Mix(theme.TextMuted, candCommittedInkMix)).
					SingleLine().
					Underline().
					Shrink(1).
					MinWidth(0)
				ui.Text(c, tail).
					FontSize(typeLabel).
					TextColor(theme.TextMuted).
					SingleLine().
					TruncateMiddle().
					Grow(1).
					Shrink(1).
					MinWidth(0)
			} else {
				ui.Text(c, v.Preedit).
					FontSize(typeLabel).
					TextColor(theme.TextMuted).
					SingleLine().
					TruncateMiddle().
					Grow(1).
					Shrink(1).
					MinWidth(0)
			}
			renderCandidateModeBadge(c, theme, v.ASCII)
		})
}

func renderVerticalCandidatePanel(c *ui.Context, theme *ui.Theme, v inputengine.View, st candidateStyle) {
	renderCandidatePreedit(c, theme, v, candVertPreeditHeight)
	renderCandidateSeparator(c, theme, st)

	rowH := float32(st.rowH)
	renderRows := func() {
		for i, cand := range v.Candidates {
			active := i == v.Selected
			row := ui.Row(c).
				Key("cand-v-"+strconv.Itoa(i)).
				FillWidth().
				Height(rowH).
				Padding(0, candCellPadX).
				Radius(st.cellRadius).
				Gap(candInnerGap).
				AlignItems(ui.Center)
			paintCandidateSelection(row, theme, st, active)
			idx, item := i, cand
			row.Children(func() {
				renderCandidateCellContent(c, theme, st, idx, item, active, true, 0)
				if active && st.selected == config.CandidateSelectedUnderline {
					renderCandidateUnderline(c, theme, candCellPadX)
				}
			})
		}
	}

	if verticalNaturalHeight(v, st, false) > candVertMaxHeight {
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Gap(candVertGap).Children(renderRows)
		})
	} else {
		renderRows()
	}

	if nav := candidateNavText(v); st.pageIndex && nav != "" {
		ui.Row(c).
			FillWidth().
			Height(candVertNavHeight).
			Padding(0, candCellPadX).
			AlignItems(ui.Center).
			Justify(ui.End).
			Children(func() {
				ui.Text(c, nav).FontSize(typeLabel).TextColor(theme.TextMuted).SingleLine()
			})
	}
}

func renderCandidateModeBadge(c *ui.Context, theme *ui.Theme, ascii bool) {
	label := candidateModeLabel(ascii)
	ui.Box(c).
		MinWidth(candModeBadgeWidth).
		Padding(space1, space6).
		Radius(radiusPill).
		Background(withAlpha(theme.TextMuted, 0.12)).
		Center().
		Children(func() {
			ui.Text(c, label).FontSize(typeMeta).FontWeight(700).TextColor(theme.TextMuted).SingleLine()
		})
}

func candidateModeLabel(ascii bool) string {
	if ascii {
		return "英"
	}
	return "中"
}

// renderCandidateCellContent draws one candidate: a small muted digit, the text (cut to
// textCap when non-zero), and ✦ for an AI suggestion. On a solid fill everything takes
// AccentText; on a tint or underline the text keeps its ink and the digit takes the accent.
// No selection changes any width.
func renderCandidateCellContent(c *ui.Context, theme *ui.Theme, st candidateStyle, index int, cand inputengine.Candidate, active, growText bool, textCap int) {
	// The ✦ of an AI suggestion is told by its shape; on a resting cell it is
	// muted like the digit, so the accent stays the selection's alone.
	digitInk, textInk, markInk := theme.TextMuted, theme.Text, theme.TextMuted
	if active {
		switch st.selected {
		case config.CandidateSelectedTint, config.CandidateSelectedUnderline:
			digitInk = readableOn(theme.Accent, theme.Text, theme.Background, minGlyphContrast)
		default:
			digitInk, textInk, markInk = theme.AccentText, theme.AccentText, theme.AccentText
		}
	}
	ui.Text(c, strconv.Itoa(index+1)).
		FontSize(typeMeta).
		FontWeight(500).
		TextColor(digitInk).
		SingleLine().
		MinWidth(candDigitWidth).
		Shrink(0)

	t := ui.Text(c, cand.Text).FontSize(st.fontSize).TextColor(textInk).SingleLine().Shrink(1).MinWidth(0)
	// A multi-syllable candidate is a different kind of answer from a single character: it
	// commits a whole phrase. Told apart by weight alone — a badge or icon here would be
	// noise in a window that repaints on every keystroke.
	if cand.Consumed > 1 {
		t.FontWeight(500)
	}
	if growText {
		t.Grow(1)
	}
	if textCap > 0 {
		t.MaxWidth(float32(textCap))
	}

	if cand.AI {
		ui.Text(c, "✦").FontSize(typeMicro).TextColor(markInk).SingleLine().MinWidth(candAIMarkerWidth).Shrink(0)
	}
}

// splitPreedit divides the segmented pinyin after the first n syllables. head is empty when
// n does not isolate a proper prefix (0, or every syllable), so callers render one plain run.
func splitPreedit(preedit string, n int) (head, tail string) {
	if n <= 0 || preedit == "" {
		return "", preedit
	}
	segs := strings.Split(preedit, "'")
	if n >= len(segs) {
		return "", preedit
	}
	return strings.Join(segs[:n], "'"), strings.Join(segs[n:], "'")
}

func candidateNavText(v inputengine.View) string {
	if !v.HasPrev && !v.HasNext {
		return ""
	}
	// The window shows one page and nothing else says how many there are, so a typist who
	// typed eight letters had no cue that the sentence answer sat on page 2 of 9.
	arrows := ""
	if v.HasPrev {
		arrows += "‹ "
	}
	if v.HasNext {
		arrows += "›"
	}
	label := strconv.Itoa(v.Page + 1)
	if v.PageCount > 1 {
		label += "/" + strconv.Itoa(v.PageCount)
	}
	if arrows == "" {
		return label
	}
	return strings.TrimSpace(arrows) + " " + label
}
