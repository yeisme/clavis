package uiapp

import (
	"context"
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	lexiconStatsBandHeight  float32 = 52
	lexiconSparklineWidth   float32 = 96
	lexiconSparklineHeight  float32 = 20
	lexiconSparklineEndRoom float32 = 4
	lexiconSparklineLine    float32 = 1.5
	lexiconSparklineDot     float32 = 4
)

func (a *AppController) ensureLexiconHitsRefresh() {
	a.lexiconHitsMu.Lock()
	if a.lexiconHitsLoaded || a.lexiconHitsRefreshStarted {
		a.lexiconHitsMu.Unlock()
		return
	}
	reader := a.lexiconHitsReader
	a.lexiconHitsRefreshStarted = true
	a.lexiconHitsMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var days []LexiconHitDay
		var err error
		if reader == nil {
			err = context.Canceled
		} else {
			days, err = reader(ctx, 14)
		}
		a.lexiconHitsMu.Lock()
		a.lexiconHitDays = append([]LexiconHitDay(nil), days...)
		a.lexiconHitsErr = err
		a.lexiconHitsLoaded = true
		a.lexiconHitsRefreshStarted = false
		a.lexiconHitsMu.Unlock()
		a.mu.Lock()
		repaint := a.OnRepaint
		a.mu.Unlock()
		if repaint != nil {
			repaint()
		}
	}()
}

func (a *AppController) lexiconHitsSnapshot() ([]LexiconHitDay, error, bool, bool) {
	a.lexiconHitsMu.RLock()
	defer a.lexiconHitsMu.RUnlock()
	return append([]LexiconHitDay(nil), a.lexiconHitDays...), a.lexiconHitsErr, a.lexiconHitsLoaded, a.lexiconHitsRefreshStarted
}

type lexiconHitPoint struct {
	X, Y float32
	Day  int
}

func lexiconHitRate(day LexiconHitDay) (float32, bool) {
	total := day.Hits + day.Misses
	if total <= 0 {
		return 0, false
	}
	return float32(day.Hits) / float32(total), true
}

func lexiconHitPoints(days []LexiconHitDay) []lexiconHitPoint {
	if len(days) == 0 {
		return nil
	}
	count := len(days)
	if count > 14 {
		count = 14
		days = days[len(days)-count:]
	}
	points := make([]lexiconHitPoint, 0, count)
	maxX := lexiconSparklineWidth - lexiconSparklineEndRoom
	for i, day := range days {
		rate, ok := lexiconHitRate(day)
		if !ok {
			continue
		}
		x := float32(0)
		if count > 1 {
			x = maxX * float32(i) / float32(count-1)
		}
		y := lexiconSparklineHeight - lexiconSparklineDot/2 - rate*(lexiconSparklineHeight-lexiconSparklineDot)
		points = append(points, lexiconHitPoint{X: x, Y: y, Day: i})
	}
	return points
}

func lexiconTotalEvents(days []LexiconHitDay) int {
	total := 0
	for _, day := range days {
		total += day.Hits + day.Misses
	}
	return total
}

func lexiconOverallHitRate(days []LexiconHitDay) (int, bool) {
	hits, total := 0, 0
	for _, day := range days {
		hits += day.Hits
		total += day.Hits + day.Misses
	}
	if total == 0 {
		return 0, false
	}
	return int(math.Round(float64(hits) * 100 / float64(total))), true
}

func renderLexiconStatsBand(c *ui.Context, theme *ui.Theme, days []LexiconHitDay, loaded bool, err error, catalogCount int) {
	ui.Row(c).Key("lexicon-stats-band").Label("lexicon-stats-band").FillWidth().Height(lexiconStatsBandHeight).Padding(space8, space0).Gap(space12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Width(92).Gap(2).AlignItems(ui.Start).Children(func() {
			if rate, ok := lexiconOverallHitRate(days); ok {
				ui.Text(c, "命中 "+fmtInt(rate)+"%").FontSize(typeKPI).FontWeight(600).FontFeatures("tnum").SingleLine()
			} else {
				ui.Text(c, "已收录 "+fmtInt(catalogCount)+" 词").FontSize(typeKPI).FontWeight(600).FontFeatures("tnum").SingleLine()
			}
			ui.Text(c, "近 14 日").FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		})
		ui.Spacer(c)
		switch {
		case err != nil:
			ui.Text(c, "词库统计暂不可用").Key("lexicon-stats-error").FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		case !loaded:
			ui.Text(c, "正在加载词库统计…").Key("lexicon-stats-loading").FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		case lexiconTotalEvents(days) == 0:
			// No activity means no chart. The KPI remains the useful catalog count.
		default:
			renderLexiconSparkline(c, theme, days)
		}
	})
}

func renderLexiconSparkline(c *ui.Context, theme *ui.Theme, days []LexiconHitDay) {
	ui.Box(c).Key("lexicon-sparkline").Label("lexicon-sparkline").Width(lexiconSparklineWidth).Height(lexiconSparklineHeight).Draw(func(p *ui.Painter, r ui.Rect) {
		points := lexiconHitPoints(days)
		if len(points) == 0 {
			return
		}
		line := ui.RGBA(142, 142, 147, 0.55)
		if theme.Dark {
			line = ui.RGBA(161, 161, 166, 0.50)
		}
		for i := 1; i < len(points); i++ {
			prev, current := points[i-1], points[i]
			p.Line(r.X+prev.X, r.Y+prev.Y, r.X+current.X, r.Y+current.Y, lexiconSparklineLine, line)
		}
		last := points[len(points)-1]
		if last.Day == min(len(days), 14)-1 {
			p.Fill(ui.Rect{X: r.X + last.X - lexiconSparklineDot/2, Y: r.Y + last.Y - lexiconSparklineDot/2, W: lexiconSparklineDot, H: lexiconSparklineDot}, ui.Hex("#0A84FF"), lexiconSparklineDot/2)
		}
	}).Children(func() {
		for _, point := range lexiconHitPoints(days) {
			// The marker elements keep each valid observation inspectable by the
			// native tester. Only today's marker is painted; the line carries the
			// historical observations without adding visual dots.
			marker := ui.Box(c).Key("lexicon-spark-point-"+fmtInt(point.Day)).Label("lexicon-spark-point-"+fmtInt(point.Day)).Absolute().Left(point.X-0.5).Top(point.Y-0.5).Size(1, 1).Opacity(0)
			if point.Day == min(len(days), 14)-1 {
				marker = marker.Size(lexiconSparklineDot, lexiconSparklineDot).Left(point.X - lexiconSparklineDot/2).Top(point.Y - lexiconSparklineDot/2).Opacity(1).Draw(func(p *ui.Painter, r ui.Rect) {
					p.FillRadii(r, [4]float32{lexiconSparklineDot / 2, lexiconSparklineDot / 2, lexiconSparklineDot / 2, lexiconSparklineDot / 2}, ui.Hex("#0A84FF"))
				})
			}
		}
	})
}
