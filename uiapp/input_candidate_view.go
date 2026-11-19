package uiapp

import (
	"slices"
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
// pinyin and the candidates, small muted digits, and a single solid accent fill on the
// chosen candidate. No keycaps, shadows or motion. The candidates set the width; the preedit
// never widens the window and is cut in the middle instead.

const (
	candidatePanelRadius = 10

	// Horizontal strip geometry (must stay in 1:1 lockstep with renderHorizontalCandidatePanel).
	candHorizPadX        = 6
	candHorizPadY        = 6
	candHorizRowGap      = 4
	candPreeditHeight    = 18
	candHairline         = 1
	candHorizStripHeight = 30
	candidatePanelHeight = candHorizPadY*2 + candPreeditHeight + candHorizRowGap*2 + candHairline + candHorizStripHeight // 69

	// A page wider than the max cuts its longest candidates (candidateTextCap) instead of
	// widening the window past a short line of text.
	candidatePanelMinWidth = 160
	candSingleMinWidth    = 120
	// candCommittedInkMix pulls the committed pinyin run toward muted: 0 = pure text,
	// 1 = pure muted.
	candCommittedInkMix = 0.35
	candidatePanelMaxWidth = 448

	// Vertical list geometry (must stay in 1:1 lockstep with renderVerticalCandidatePanel).
	candVertPadX          = 6
	candVertPadY          = 6
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
	candCJKWidth       = 16 // one CJK character at FontSize 15
	candLatinWidth     = 9  // one Latin character at FontSize 15
	candAIMarkerWidth  = 10 // ✦ at typeMicro
	candNavWidth       = 18
	candModeBadgeWidth = 28
	// candMinTextCap keeps at least a character and the ellipsis of a cut candidate.
	candMinTextCap = 2 * candCJKWidth
)

// PreferredCandidatePanelSize estimates the panel size for v using the active
// AppearanceConfig (published via SetCandidateAppearance / CapsuleView).
func PreferredCandidatePanelSize(v inputengine.View) (width, height int) {
	return PreferredCandidatePanelSizeFor(v, CandidateAppearance())
}

// PreferredCandidatePanelSizeFor computes exact panel dimensions matching
// InputCandidateViewFor in both horizontal and vertical orientations.
func PreferredCandidatePanelSizeFor(v inputengine.View, cfg config.AppearanceConfig) (width, height int) {
	if isCandidateVertical(cfg) {
		return preferredVerticalCandidateSize(v)
	}
	return preferredHorizontalCandidateSize(v)
}

// The preedit takes no part in either estimate: however long the pinyin gets, it is cut
// in the middle to the width the candidates need.
func preferredHorizontalCandidateSize(v inputengine.View) (width, height int) {
	w := naturalHorizontalCandidateWidth(v)
	// A single candidate does not need the full minimum: the fixed floor left a lone
	// character rattling in a 160px box.
	floor := candidatePanelMinWidth
	if len(v.Candidates) <= 1 {
		floor = candSingleMinWidth
	}
	return min(max(w, floor), candidatePanelMaxWidth), candidatePanelHeight
}

// naturalHorizontalCandidateWidth is the strip's width with every candidate uncut.
func naturalHorizontalCandidateWidth(v inputengine.View) int {
	w := candHorizPadX * 2
	for i, cand := range v.Candidates {
		if i > 0 {
			w += candHorizItemGap
		}
		w += candidateCellWidth(cand)
	}
	if candidateNavText(v) != "" {
		w += candHorizItemGap*2 + candNavWidth // gaps around the spacer that pins it right
	}
	return w
}

// candidateTextCap is the width the candidate texts are cut to when the page is wider than
// the panel may grow, 0 when it fits. The longest texts give up room first, down to one
// common cap, and short ones keep their full width: shrinking every cell a little instead
// would end each of them, a two-character word included, with an ellipsis.
func candidateTextCap(v inputengine.View) int {
	excess := naturalHorizontalCandidateWidth(v) - candidatePanelMaxWidth
	if excess <= 0 {
		return 0
	}
	widths := make([]int, len(v.Candidates))
	for i, cand := range v.Candidates {
		widths[i] = candidateTextWidth(cand.Text)
	}
	slices.SortFunc(widths, func(a, b int) int { return b - a })
	sum := 0
	for k := 1; k <= len(widths); k++ {
		sum += widths[k-1]
		capW := (sum - excess) / k // cutting the k longest to capW frees at least excess
		if k == len(widths) || capW >= widths[k] {
			return max(capW, candMinTextCap)
		}
	}
	return candMinTextCap
}

func preferredVerticalCandidateSize(v inputengine.View) (width, height int) {
	w := 0
	for _, cand := range v.Candidates {
		w = max(w, candidateCellWidth(cand))
	}
	w = min(max(w+candVertPadX*2, candVertMinWidth), candVertMaxWidth)

	h := candVertPadY*2 + candVertPreeditHeight + candVertGap + candHairline
	if len(v.Candidates) > 0 {
		h += len(v.Candidates) * (candVertGap + candVertRowHeight)
	}
	if v.HasPrev || v.HasNext {
		h += candVertGap + candVertNavHeight
	}
	h = min(max(h, candVertMinHeight), candVertMaxHeight)
	return w, h
}

// candidateCellWidth is the natural width of one cell (a vertical row has the same parts).
func candidateCellWidth(cand inputengine.Candidate) int {
	w := candCellPadX*2 + candDigitWidth + candInnerGap + candidateTextWidth(cand.Text)
	if cand.AI {
		w += candInnerGap + candAIMarkerWidth
	}
	return w
}

func candidateTextWidth(s string) int {
	w := 0
	for _, r := range s {
		if runeWidthUnits(r) == 2 {
			w += candCJKWidth
		} else {
			w += candLatinWidth
		}
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

// InputCandidateView renders the preedit line plus the current candidate page
// using the active AppearanceConfig.
func InputCandidateView(c *ui.Context, v inputengine.View) {
	InputCandidateViewFor(c, v, CandidateAppearance())
}

// InputCandidateViewFor renders the cursor-side IME candidate panel for cfg.
// Typing stays on the fast non-shader path: GlassSurface handles backdrop
// material (with opaque ReduceTransparency fallback); the content itself is
// opaque ink and fills and never animates.
func InputCandidateViewFor(c *ui.Context, v inputengine.View, cfg config.AppearanceConfig) {
	theme := resolveAppearanceTheme(c, cfg.Theme)
	c.SetTheme(theme)
	prefs := c.Preferences()
	profile := appMotionProfile(c, cfg)
	DisableAnimationsFor(c, profile)

	if !prefs.ReduceTransparency {
		c.Root().Radius(candidatePanelRadius)
	}

	vertical := isCandidateVertical(cfg)
	padY, padX, gap := float32(candHorizPadY), float32(candHorizPadX), float32(candHorizRowGap)
	if vertical {
		padY, padX, gap = float32(candVertPadY), float32(candVertPadX), float32(candVertGap)
	}

	box := applyTextLocale(ui.Column(c).Fill().Padding(padY, padX).Gap(gap).Clip())
	applyCandidatePanelSurface(c, box, theme, cfg)

	box.Children(func() {
		if vertical {
			renderVerticalCandidatePanel(c, theme, v)
		} else {
			renderHorizontalCandidatePanel(c, theme, v)
		}
	})
}

func applyCandidatePanelSurface(c *ui.Context, box ui.Element, theme *ui.Theme, cfg config.AppearanceConfig) {
	prefs := c.Preferences()
	if isClassicPreset(cfg) {
		box.Radius(candidatePanelRadius)
		if !prefs.ReduceTransparency {
			box.Material(glass.Glass{Native: c.Vibrancy()})
			box.Border(1, withAlpha(theme.Border, 0.55))
		} else {
			box.Background(theme.Background).Border(1, theme.Border)
		}
		if prefs.HighContrast {
			box.Border(1.5, theme.Border)
		}
		return
	}

	GlassSurface(c, box, cfg, GlassOpts{
		Native: c.Vibrancy(),
		Radius: candidatePanelRadius,
	})
	if prefs.HighContrast {
		box.Border(1.5, theme.Border)
	}
}

func renderHorizontalCandidatePanel(c *ui.Context, theme *ui.Theme, v inputengine.View) {
	renderCandidatePreedit(c, theme, v, candPreeditHeight)
	ui.Box(c).FillWidth().Height(candHairline).Background(hairline(theme))

	textCap := candidateTextCap(v)
	ui.Row(c).FillWidth().Height(candHorizStripHeight).Gap(candHorizItemGap).AlignItems(ui.Center).Children(func() {
		for i, cand := range v.Candidates {
			active := i == v.Selected
			cell := ui.Row(c).
				Key("cand-h-"+strconv.Itoa(i)).
				Height(candHorizStripHeight).
				Padding(0, candCellPadX).
				Radius(radiusKeycap).
				Gap(candInnerGap).
				AlignItems(ui.Center).
				Shrink(1).
				MinWidth(0)
			if active {
				cell.Background(theme.Accent)
			}
			idx, item := i, cand
			cell.Children(func() {
				renderCandidateCellContent(c, theme, idx, item, active, false, textCap)
			})
		}
		if nav := candidateNavText(v); nav != "" {
			ui.Box(c).Grow(1)
			ui.Box(c).Width(candNavWidth).Height(candHorizStripHeight).Shrink(0).Center().Children(func() {
				ui.Text(c, nav).FontSize(typeLabel).TextColor(theme.TextMuted).SingleLine()
			})
		}
	})
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

func renderVerticalCandidatePanel(c *ui.Context, theme *ui.Theme, v inputengine.View) {
	renderCandidatePreedit(c, theme, v, candVertPreeditHeight)
	ui.Box(c).FillWidth().Height(candHairline).Background(hairline(theme))

	renderRows := func() {
		for i, cand := range v.Candidates {
			active := i == v.Selected
			row := ui.Row(c).
				Key("cand-v-"+strconv.Itoa(i)).
				FillWidth().
				Height(candVertRowHeight).
				Padding(0, candCellPadX).
				Radius(radiusKeycap).
				Gap(candInnerGap).
				AlignItems(ui.Center)
			if active {
				row.Background(theme.Accent)
			}
			idx, item := i, cand
			row.Children(func() {
				renderCandidateCellContent(c, theme, idx, item, active, true, 0)
			})
		}
	}

	naturalH := candVertPadY*2 + candVertPreeditHeight + candVertGap + candHairline + len(v.Candidates)*(candVertGap+candVertRowHeight)
	if v.HasPrev || v.HasNext {
		naturalH += candVertGap + candVertNavHeight
	}
	if naturalH > candVertMaxHeight {
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Gap(candVertGap).Children(renderRows)
		})
	} else {
		renderRows()
	}

	if nav := candidateNavText(v); nav != "" {
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
// textCap when non-zero), and ✦ for an AI suggestion. On the chosen cell everything takes
// AccentText over the solid accent fill, so the choice reads at a glance without changing
// any width.
func renderCandidateCellContent(c *ui.Context, theme *ui.Theme, index int, cand inputengine.Candidate, active, growText bool, textCap int) {
	digitInk, textInk, markInk := theme.TextMuted, theme.Text, theme.Accent
	if active {
		digitInk, textInk, markInk = theme.AccentText, theme.AccentText, theme.AccentText
	}
	ui.Text(c, strconv.Itoa(index+1)).
		FontSize(typeMeta).
		FontWeight(500).
		TextColor(digitInk).
		SingleLine().
		MinWidth(candDigitWidth).
		Shrink(0)

	t := ui.Text(c, cand.Text).FontSize(15).TextColor(textInk).SingleLine().Shrink(1).MinWidth(0)
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
