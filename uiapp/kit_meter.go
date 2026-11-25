package uiapp

import (
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/storage"
)

// A quota meter is the smallest honest chart: how much of a real limit a real
// measure uses — clipboard image bytes against the media quota, history rows
// against the history cap. It follows the anchor: a neutral gray fill on a
// hairline track, turning system orange (with a warning glyph, never color
// alone) once the measure reaches quotaMeterWarnAt of its limit; the reading
// stays in text ink with tabular figures.
const (
	quotaMeterTrackHeight float32 = space6
	quotaMeterTrackRadius float32 = space6 / 2
	quotaMeterLabelWidth  float32 = 132
	quotaMeterWarnAt      float64 = 0.9
)

// quotaMeterRatio is used/limit clamped to [0, 1]; a missing limit reads 0.
func quotaMeterRatio(used, limit float64) float64 {
	if limit <= 0 || used <= 0 {
		return 0
	}
	return min(used/limit, 1)
}

// quotaMeterFill is the fill color of a meter at ratio r.
func quotaMeterFill(t *ui.Theme, r float64) ui.Color {
	if r >= quotaMeterWarnAt {
		return kitStateWarningColor(t)
	}
	if t.Dark {
		return ui.Hex("#A1A1A6")
	}
	return ui.Hex("#8E8E93")
}

// kitQuotaMeter draws one meter row: the measure's glyph, the track filled to
// used/limit, then the reading ("61.4 MB / 512 MB").
func kitQuotaMeter(c *ui.Context, t *ui.Theme, key, icon, reading string, used, limit float64) {
	r := quotaMeterRatio(used, limit)
	ui.Row(c).Key(key).Label(key).FillWidth().Height(space20).Gap(space8).AlignItems(ui.Center).Children(func() {
		glyph := secondaryInk(t)
		if r >= quotaMeterWarnAt {
			icon, glyph = "exclamationmark.triangle", kitStateWarningColor(t)
		}
		renderSemanticIcon(c, icon, typeLabel, glyph)
		ui.Box(c).Grow(1).MinWidth(0).Height(quotaMeterTrackHeight).Radius(quotaMeterTrackRadius).
			Background(tileFill(t)).Border(space1, hairline(t)).Draw(func(p *ui.Painter, rect ui.Rect) {
			if r <= 0 {
				return
			}
			w := max(float32(r)*rect.W, quotaMeterTrackHeight)
			p.Fill(ui.Rect{X: rect.X, Y: rect.Y, W: w, H: rect.H}, quotaMeterFill(t, r), quotaMeterTrackRadius)
		})
		ui.Text(c, reading).FontSize(typeMicro).FontWeight(600).FontFeatures("tnum").TextColor(t.Text).
			SingleLine().Width(quotaMeterLabelWidth).Shrink(0).TextAlign(ui.End)
	})
}

// clipboardKindBytes is the stored size of one clipboard kind.
func clipboardKindBytes(stats []storage.ClipboardKindStat, kind string) int64 {
	var total int64
	for _, s := range stats {
		if s.Kind == kind {
			total += s.Bytes
		}
	}
	return total
}
