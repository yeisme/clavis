package uiapp

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

const (
	lexiconStatsBandHeight  float32 = 52
	lexiconSparklineWidth   float32 = 96
	lexiconSparklineHeight  float32 = 20
	lexiconSparklineEndRoom float32 = 4
	lexiconSparklineLine    float32 = 1.5
	lexiconSparklineDot     float32 = 4
)

func (a *AppController) ensureLexiconTopWordsRefresh() {
	a.lexiconTopWordsMu.Lock()
	if a.lexiconTopWordsLoaded || a.lexiconTopWordsRefreshing {
		a.lexiconTopWordsMu.Unlock()
		return
	}
	reader := a.lexiconTopWordsReader
	a.lexiconTopWordsRefreshing = true
	a.lexiconTopWordsMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var words []LexiconTopWord
		var err error
		if reader == nil {
			err = context.Canceled
		} else {
			words, err = reader(ctx, 5)
		}
		a.lexiconTopWordsMu.Lock()
		a.lexiconTopWords = append([]LexiconTopWord(nil), words...)
		a.lexiconTopWordsErr = err
		a.lexiconTopWordsLoaded = true
		a.lexiconTopWordsRefreshing = false
		a.lexiconTopWordsMu.Unlock()
		a.mu.Lock()
		repaint := a.OnRepaint
		a.mu.Unlock()
		if repaint != nil {
			repaint()
		}
	}()
}

func (a *AppController) lexiconTopWordsSnapshot() ([]LexiconTopWord, error, bool) {
	a.lexiconTopWordsMu.RLock()
	defer a.lexiconTopWordsMu.RUnlock()
	return append([]LexiconTopWord(nil), a.lexiconTopWords...), a.lexiconTopWordsErr, a.lexiconTopWordsLoaded
}

func lexiconTopWordMap(words []LexiconTopWord) (map[string]int, int) {
	out := make(map[string]int, len(words))
	maxUse := 0
	for _, word := range words {
		if word.Word == "" || word.UseCount <= 0 {
			continue
		}
		out[word.Word] = word.UseCount
		if word.UseCount > maxUse {
			maxUse = word.UseCount
		}
	}
	return out, maxUse
}

func lexiconMicrobarWidth(useCount, maxUse int) float32 {
	if useCount <= 0 || maxUse <= 0 {
		return 0
	}
	width := float32(44) * float32(useCount) / float32(maxUse)
	if width < 2 {
		return 2
	}
	return width
}

func lexiconMicrobarColor(useCount, maxUse int) ui.Color {
	if useCount == maxUse && maxUse > 0 {
		return ui.Hex("#0A84FF")
	}
	levels := []float32{0.65, 0.38, 0.22, 0.12}
	level := 3
	if maxUse > 0 {
		ratio := float32(useCount) / float32(maxUse)
		switch {
		case ratio >= 0.75:
			level = 0
		case ratio >= 0.50:
			level = 1
		case ratio >= 0.25:
			level = 2
		}
	}
	return ui.Hex("#8E8E93").Alpha(levels[level])
}

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

func (a *AppController) retryLexiconHits() {
	a.lexiconHitsMu.Lock()
	a.lexiconHitsLoaded = false
	a.lexiconHitsErr = nil
	a.lexiconHitsMu.Unlock()
	a.ensureLexiconHitsRefresh()
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
	renderLexiconStatsBandWithRetry(c, theme, days, loaded, err, catalogCount, nil)
}

func renderLexiconStatsBandWithRetry(c *ui.Context, theme *ui.Theme, days []LexiconHitDay, loaded bool, err error, catalogCount int, onRetry func()) {
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
			kitInlineError(c, theme, "lexicon-stats-error", "词库统计暂不可用", onRetry)
		case !loaded:
			kitInlineLoading(c, theme, "lexicon-stats-loading", "正在加载词库统计…")
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
		pts := make([][2]float32, 0, len(points))
		for _, pt := range points {
			pts = append(pts, [2]float32{r.X + pt.X, r.Y + pt.Y})
		}
		if len(pts) >= 2 {
			p.StrokePolyline(pts, lexiconSparklineLine, line)
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

// The Stats hub's lexicon card: a 14-day hit-rate line (0-100%) beside the
// top-5 words as ranked bars. Both come from the cached snapshots only.

// lexiconHubDays is the window of the hit-rate line, the reader's own.
const lexiconHubDays = 14

// lexiconHubTopWords is how many ranked words the card lists.
const lexiconHubTopWords = 5

// lexiconLastDays keeps the newest n days of an oldest-first window.
func lexiconLastDays(days []LexiconHitDay, n int) []LexiconHitDay {
	if len(days) > n {
		return days[len(days)-n:]
	}
	return days
}

// lexiconHitSeries is the hit rate (0..1) of each day, and NaN for a day with
// no events: such a day is a gap in the line, never a zero.
func lexiconHitSeries(days []LexiconHitDay) []float64 {
	out := make([]float64, len(days))
	for i, day := range days {
		if rate, ok := lexiconHitRate(day); ok {
			out[i] = float64(rate)
		} else {
			out[i] = math.NaN()
		}
	}
	return out
}

// lexiconRanks are the words to list, most used first, each with its length
// relative to the busiest: empty words and non-positive counts are dropped.
func lexiconRanks(words []LexiconTopWord, limit int) []chartRank {
	rows := make([]chartRank, 0, len(words))
	top := 0
	for _, word := range words {
		label := strings.TrimSpace(word.Word)
		if label == "" || word.UseCount <= 0 {
			continue
		}
		rows = append(rows, chartRank{Label: label, Value: word.UseCount})
		top = max(top, word.UseCount)
		if limit > 0 && len(rows) == limit {
			break
		}
	}
	for i := range rows {
		rows[i].Frac = float64(rows[i].Value) / float64(top)
	}
	return rows
}

// lexiconPercent is a day's hit rate as a whole percent.
func lexiconPercent(day LexiconHitDay) (int, bool) {
	rate, ok := lexiconHitRate(day)
	return int(math.Round(float64(rate) * 100)), ok
}

// lexiconTopCaption is the heading of the ranked list: the "Top: %s" caption
// without its colon, in whichever language is active.
func lexiconTopCaption() string {
	return strings.TrimRight(i18n.Tf("stats.card.lexicon.top_words", ""), ":  ：")
}

// renderLexiconHubChart is the body of the lexicon card once its hit window is
// loaded: the headline rate, the line chart, and the ranked words.
func renderLexiconHubChart(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig, days []LexiconHitDay, topWords []LexiconTopWord, topLoaded bool) {
	pal := newChartPalette(theme)
	days = lexiconLastDays(days, lexiconHubDays)
	hasEvents := lexiconTotalEvents(days) > 0
	ranks := lexiconRanks(topWords, lexiconHubTopWords)

	ui.Row(c).Key("stats-lex-body").FillWidth().Grow(1).MinHeight(space0).Gap(space12).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Basis(0).Grow(13).MinWidth(space0).MinHeight(space0).Gap(space6).Children(func() {
			ui.Column(c).Gap(space2).Children(func() {
				if rate, ok := lexiconOverallHitRate(days); ok {
					ui.Text(c, i18n.Tf("stats.card.lexicon.hit_rate", rate)).Key("stats-lex-rate").
						FontSize(typeKPI).FontWeight(600).FontFeatures("tnum").TextColor(theme.Text).SingleLine()
				} else {
					ui.Text(c, i18n.Tf("stats.card.lexicon.terms_count", len(cfg.Glossary.Terms))).
						FontSize(typeBody).FontWeight(600).FontFeatures("tnum").TextColor(theme.Text).SingleLine()
				}
				ui.Text(c, i18n.Tf("stats.card.lexicon.summary", len(cfg.Glossary.Terms), len(cfg.Main.InputEngine.UserWords))).
					FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
			})
			if !hasEvents {
				// No events means no line: the headline above is the catalog size.
				ui.Text(c, i18n.T("stats.status.no_records")).FontSize(typeMeta).TextColor(pal.label).SingleLine()
				return
			}
			labels := make([]string, len(days))
			for i, day := range days {
				labels[i] = chartShortDay(day.Day)
			}
			kitChartLine(c, theme, chartLineSpec{
				Key:        "stats-lex-chart",
				Label:      i18n.T("stats.card.lexicon.title"),
				Labels:     labels,
				Values:     lexiconHitSeries(days),
				LabelEvery: 4,
				Describe: func(i int) string {
					if pct, ok := lexiconPercent(days[i]); ok {
						return labels[i] + ": " + i18n.Tf("stats.card.lexicon.hit_rate", pct) + " (" + fmtInt(days[i].Hits) + " / " + fmtInt(days[i].Hits+days[i].Misses) + ")"
					}
					return labels[i] + ": " + i18n.T("stats.status.no_records")
				},
				Tooltip: func(i int) {
					kitChartTipHeading(c, pal, labels[i])
					if pct, ok := lexiconPercent(days[i]); ok {
						kitChartTipFigure(c, pal, nil, i18n.Tf("stats.card.lexicon.hit_rate", pct), "")
						kitChartTipHeading(c, pal, fmtInt(days[i].Hits)+" / "+fmtInt(days[i].Hits+days[i].Misses))
						return
					}
					kitChartTipHeading(c, pal, i18n.T("stats.status.no_records"))
				},
			})
		})
		ui.Column(c).Basis(0).Grow(10).MinWidth(space0).MinHeight(space0).Gap(space6).Children(func() {
			ui.Text(c, lexiconTopCaption()).FontSize(typeMicro).TextColor(pal.label).NoWrap()
			if topLoaded && len(ranks) > 0 {
				kitChartRankedBars(c, theme, "stats-lex-top", ranks)
				return
			}
			ui.Text(c, i18n.T("stats.status.no_records")).FontSize(typeMeta).TextColor(pal.label).SingleLine()
		})
	})
}
