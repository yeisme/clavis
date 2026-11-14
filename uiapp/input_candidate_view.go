package uiapp

import (
	"strconv"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// Candidate window for the own input engine (docs/design/own-input-engine.md §4). Shown with
// ShowInactive so it never takes focus from the target app.

const (
	candidatePanelRadius = 10

	// Horizontal strip geometry (must stay in 1:1 lockstep with renderHorizontalCandidatePanel).
	candHorizPadX        = 12
	candHorizPadY        = 8
	candHorizRowGap      = 6
	candPreeditHeight    = 16
	candHorizStripHeight = 34
	candidatePanelHeight = candHorizPadY*2 + candPreeditHeight + candHorizRowGap + candHorizStripHeight // 72

	candidatePanelMinWidth = 160
	candidatePanelMaxWidth = 760

	// Vertical list geometry (must stay in 1:1 lockstep with renderVerticalCandidatePanel).
	candVertPadX          = 10
	candVertPadY          = 8
	candVertGap           = 4
	candVertPreeditHeight = 18
	candVertRowHeight     = 34
	candVertRowPadX       = 6
	candVertNavHeight     = 18
	candVertMinWidth      = 148
	candVertMaxWidth      = 420
	candVertMinHeight     = 56
	candVertMaxHeight     = 460

	// Shared cell metrics for TactileKeycap3D + candidate text + optional AI ✦ chip.
	candKeycapWidth   = 30
	candInnerGap      = 6
	candCellPadX      = 6
	candHorizItemGap  = 10
	candRuneWidth     = 10 // per Latin width unit at FontSize 15
	candAIMarkerWidth = 28
	candNavBadgeWidth = 34
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

func preferredHorizontalCandidateSize(v inputengine.View) (width, height int) {
	preeditW := candHorizPadX*2 + textWidthUnits(v.Preedit)*8
	stripW := candHorizPadX * 2
	for i, cand := range v.Candidates {
		if i > 0 {
			stripW += candHorizItemGap
		}
		cellW := candCellPadX*2 + candKeycapWidth + candInnerGap + textWidthUnits(cand.Text)*candRuneWidth
		if cand.AI {
			cellW += candAIMarkerWidth
		}
		stripW += cellW
	}
	if v.HasPrev || v.HasNext {
		if len(v.Candidates) > 0 {
			stripW += candHorizItemGap
		}
		stripW += candNavBadgeWidth
	}
	w := max(preeditW, stripW)
	return min(max(w, candidatePanelMinWidth), candidatePanelMaxWidth), candidatePanelHeight
}

func preferredVerticalCandidateSize(v inputengine.View) (width, height int) {
	w := candVertPadX*2 + candVertRowPadX*2 + textWidthUnits(v.Preedit)*8
	for _, cand := range v.Candidates {
		rowW := candVertPadX*2 + candVertRowPadX*2 + candKeycapWidth + candInnerGap + textWidthUnits(cand.Text)*candRuneWidth
		if cand.AI {
			rowW += candAIMarkerWidth
		}
		w = max(w, rowW)
	}
	if v.HasPrev || v.HasNext {
		w = max(w, candVertPadX*2+64)
	}
	w = min(max(w, candVertMinWidth), candVertMaxWidth)

	h := candVertPadY*2 + candVertPreeditHeight
	if len(v.Candidates) > 0 {
		h += len(v.Candidates) * (candVertGap + candVertRowHeight)
	}
	if v.HasPrev || v.HasNext {
		h += candVertGap + candVertNavHeight
	}
	h = min(max(h, candVertMinHeight), candVertMaxHeight)
	return w, h
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
// material (with opaque ReduceTransparency fallback) and TactileKeycap3D adds
// spring keycap depth only when Spatial3DEnabled permits.
func InputCandidateViewFor(c *ui.Context, v inputengine.View, cfg config.AppearanceConfig) {
	theme := resolveAppearanceTheme(c, cfg.Theme)
	c.SetTheme(theme)
	prefs := c.Preferences()
	profile := appMotionProfile(c, cfg)
	DisableAnimationsFor(c, profile)
	effCfg := effectiveSpatialAppearance(c, cfg)

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
			renderVerticalCandidatePanel(c, theme, v, effCfg)
		} else {
			renderHorizontalCandidatePanel(c, theme, v, effCfg)
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

func renderHorizontalCandidatePanel(c *ui.Context, theme *ui.Theme, v inputengine.View, effCfg config.AppearanceConfig) {
	ui.Row(c).FillWidth().Height(candPreeditHeight).AlignItems(ui.Center).Children(func() {
		ui.Text(c, v.Preedit).FontSize(12).TextColor(theme.TextMuted).SingleLine()
	})

	ui.Row(c).FillWidth().Height(candHorizStripHeight).Gap(candHorizItemGap).AlignItems(ui.Center).Children(func() {
		for i, cand := range v.Candidates {
			active := i == v.Selected
			cell := ui.Row(c).
				Key("cand-h-"+strconv.Itoa(i)).
				Height(candHorizStripHeight).
				Padding(2, candCellPadX).
				Radius(8).
				Gap(candInnerGap).
				AlignItems(ui.Center)
			if active {
				cell.Background(withAlpha(theme.Accent, 0.12))
			}
			idx, item := i, cand
			cell.Children(func() {
				renderCandidateCellContent(c, theme, idx, item, active, effCfg, false)
			})
		}
		if nav := candidateNavText(v); nav != "" {
			ui.Box(c).Height(candHorizStripHeight).MinWidth(candNavBadgeWidth).Center().Children(func() {
				ui.Text(c, nav).FontSize(13).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
			})
		}
	})
}

func renderVerticalCandidatePanel(c *ui.Context, theme *ui.Theme, v inputengine.View, effCfg config.AppearanceConfig) {
	ui.Row(c).
		FillWidth().
		Height(candVertPreeditHeight).
		Padding(0, candVertRowPadX).
		AlignItems(ui.Center).
		Children(func() {
			ui.Text(c, v.Preedit).FontSize(12).TextColor(theme.TextMuted).SingleLine()
		})

	renderRows := func() {
		for i, cand := range v.Candidates {
			active := i == v.Selected
			row := ui.Row(c).
				Key("cand-v-"+strconv.Itoa(i)).
				FillWidth().
				Height(candVertRowHeight).
				Padding(2, candVertRowPadX).
				Radius(8).
				Gap(candInnerGap).
				AlignItems(ui.Center)
			if active {
				row.Background(withAlpha(theme.Accent, 0.14)).
					Border(1, withAlpha(theme.Accent, 0.35))
			}
			idx, item := i, cand
			row.Children(func() {
				renderCandidateCellContent(c, theme, idx, item, active, effCfg, true)
			})
		}
	}

	naturalH := candVertPadY*2 + candVertPreeditHeight + len(v.Candidates)*(candVertGap+candVertRowHeight)
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
			Padding(0, candVertRowPadX).
			AlignItems(ui.Center).
			Justify(ui.End).
			Children(func() {
				ui.Text(c, nav).FontSize(13).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
			})
	}
}

func renderCandidateCellContent(
	c *ui.Context,
	theme *ui.Theme,
	index int,
	cand inputengine.Candidate,
	active bool,
	effCfg config.AppearanceConfig,
	growText bool,
) {
	TactileKeycap3D(c, strconv.Itoa(index+1), active, active, effCfg)

	t := ui.Text(c, cand.Text).FontSize(15).SingleLine()
	if active {
		t.TextColor(theme.Accent).FontWeight(700)
	} else {
		t.TextColor(theme.Text).FontWeight(500)
	}
	if growText {
		t.Grow(1)
	}

	if cand.AI {
		aiBg := withAlpha(theme.Accent, 0.16)
		aiBorder := withAlpha(theme.Accent, 0.38)
		if active {
			aiBg = withAlpha(theme.Accent, 0.24)
			aiBorder = withAlpha(theme.Accent, 0.55)
		}
		ui.Box(c).
			Padding(1, 5).
			Radius(999).
			Background(aiBg).
			Border(1, aiBorder).
			Center().
			Children(func() {
				ui.Text(c, "✦").FontSize(11).FontWeight(700).TextColor(theme.Accent).SingleLine()
			})
	}
}

func candidateNavText(v inputengine.View) string {
	nav := ""
	if v.HasPrev {
		nav += "‹"
	}
	if v.HasNext {
		nav += "›"
	}
	return nav
}
