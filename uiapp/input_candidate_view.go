package uiapp

import (
	"strconv"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// Candidate window for the own input engine (docs/design/own-input-engine.md §4). Shown with
// ShowInactive so it never takes focus from the target app.

const (
	candidatePanelHeight   = 58
	candidatePanelMinWidth = 160
	candidatePanelMaxWidth = 720
	candidateCellPad       = 30 // index label + gap per candidate, px
	candidateRuneWidth     = 8  // per Latin width unit at FontSize 15
)

// PreferredCandidatePanelSize estimates the panel size for v.
func PreferredCandidatePanelSize(v inputengine.View) (width, height int) {
	w := 24 + textWidthUnits(v.Preedit)*7
	row := 24
	for _, cand := range v.Candidates {
		row += candidateCellPad + textWidthUnits(cand.Text)*candidateRuneWidth
		if cand.AI {
			row += 12
		}
	}
	if v.HasPrev || v.HasNext {
		row += 30
	}
	w = max(w, row)
	return min(max(w, candidatePanelMinWidth), candidatePanelMaxWidth), candidatePanelHeight
}

// CandidateLabel is the visible text of one cell ("1 你好", AI suggestions marked ✦).
func CandidateLabel(i int, cand inputengine.Candidate) string {
	label := strconv.Itoa(i+1) + " " + cand.Text
	if cand.AI {
		label += " ✦"
	}
	return label
}

// InputCandidateView renders the preedit line plus the current candidate page.
func InputCandidateView(c *ui.Context, v inputengine.View) {
	theme := c.Theme()
	prefs := c.Preferences()
	if !prefs.ReduceTransparency {
		c.Root().Radius(10)
	}
	box := applyTextLocale(ui.Column(c).Fill().Padding(6, 12).Gap(4).Clip())
	if !prefs.ReduceTransparency {
		box.Material(glass.Glass{Native: c.Vibrancy()})
		box.Border(1, withAlpha(theme.Border, 0.55))
	} else {
		box.Background(theme.Background).Border(1, theme.Border)
	}
	box.Children(func() {
		ui.Text(c, v.Preedit).FontSize(12).TextColor(theme.TextMuted).SingleLine()
		ui.Row(c).Gap(12).Children(func() {
			for i, cand := range v.Candidates {
				t := ui.Text(c, CandidateLabel(i, cand)).FontSize(15).SingleLine()
				if i == v.Selected {
					t.TextColor(theme.Accent).FontWeight(700)
				} else {
					t.TextColor(theme.Text)
				}
			}
			if v.HasPrev || v.HasNext {
				nav := ""
				if v.HasPrev {
					nav += "‹"
				}
				if v.HasNext {
					nav += "›"
				}
				ui.Text(c, nav).FontSize(13).TextColor(theme.TextMuted).SingleLine()
			}
		})
	})
}
