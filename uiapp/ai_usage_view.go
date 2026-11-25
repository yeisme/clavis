package uiapp

import (
	"context"
	"math"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

const (
	aiUsageBandHeight    = 56
	aiUsageChartWidth    = 96
	aiUsageChartHeight   = 28
	aiUsageBarWidth      = 8
	aiUsageBarGap        = 6
	aiUsageChartBaseY    = 26
	aiUsageChartBaseline = 1
	aiUsageBarMaxHeight  = 24
)

func (c *AppController) ensureAIUsageRefresh() {
	c.aiUsageMu.Lock()
	if c.aiUsageLoaded || c.aiUsageRefreshStarted {
		c.aiUsageMu.Unlock()
		return
	}
	reader := c.aiUsageReader
	c.aiUsageRefreshStarted = true
	c.aiUsageMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var days []AIUsageDay
		var err error
		if reader == nil {
			err = context.Canceled
		} else {
			days, err = reader(ctx, 7)
		}
		c.aiUsageMu.Lock()
		c.aiUsageDays = append([]AIUsageDay(nil), days...)
		c.aiUsageErr = err
		c.aiUsageLoaded = true
		c.aiUsageRefreshStarted = false
		c.aiUsageMu.Unlock()
		c.mu.Lock()
		repaint := c.OnRepaint
		c.mu.Unlock()
		if repaint != nil {
			repaint()
		}
	}()
}

func (c *AppController) invalidateAIUsage() {
	c.aiUsageMu.Lock()
	c.aiUsageLoaded = false
	c.aiUsageMu.Unlock()
}

func (c *AppController) retryAIUsage() {
	c.aiUsageMu.Lock()
	c.aiUsageLoaded = false
	c.aiUsageErr = nil
	c.aiUsageMu.Unlock()
	c.ensureAIUsageRefresh()
}

func (c *AppController) aiUsageSnapshot() ([]AIUsageDay, error, bool, bool) {
	c.aiUsageMu.RLock()
	defer c.aiUsageMu.RUnlock()
	return append([]AIUsageDay(nil), c.aiUsageDays...), c.aiUsageErr, c.aiUsageLoaded, c.aiUsageRefreshStarted
}

func (a *AppController) renderAIUsageBand(c *ui.Context, theme *ui.Theme) {
	days, err, loaded, refreshing := a.aiUsageSnapshot()
	if !loaded {
		refreshing = true
	}
	ui.Row(c).Key("ai-usage-band").Label("ai-usage-band").FillWidth().Height(aiUsageBandHeight).Padding(space8, space0).Gap(space12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Width(92).Gap(2).AlignItems(ui.Start).Children(func() {
			ui.Text(c, aiUsageTodayLabel(days)).FontSize(typeBody).FontWeight(600).SingleLine()
			ui.Text(c, "今日").FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		})
		ui.Spacer(c)
		switch {
		case err != nil:
			kitInlineError(c, theme, "ai-usage-error", "AI 用量暂不可用", a.retryAIUsage)
		case !loaded || refreshing:
			kitInlineLoading(c, theme, "ai-usage-loading", "正在加载 AI 用量…")
		case aiUsageTotalRequests(days) == 0:
			ui.Text(c, "近 7 日无 AI 调用记录").Key("ai-usage-empty").FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		default:
			renderAIUsageChart(c, theme, days)
		}
	})
}

func aiUsageTodayLabel(days []AIUsageDay) string {
	if len(days) == 0 {
		return "— tok"
	}
	return formatTokenCount(aiUsageTokens(days[len(days)-1])) + " tok"
}

func aiUsageTotalTokens(days []AIUsageDay) int {
	total := 0
	for _, day := range days {
		total += aiUsageTokens(day)
	}
	return total
}

func aiUsageTotalRequests(days []AIUsageDay) int {
	total := 0
	for _, day := range days {
		total += day.Requests
	}
	return total
}

func aiUsageTokens(day AIUsageDay) int { return day.InputTokens + day.OutputTokens }

func formatTokenCount(value int) string {
	if value >= 1000000 {
		return formatCompactToken(value, 1000000, "M")
	}
	if value >= 1000 {
		return formatCompactToken(value, 1000, "k")
	}
	return fmtInt(value)
}

func formatCompactToken(value, divisor int, suffix string) string {
	whole := float64(value) / float64(divisor)
	if whole >= 100 {
		return fmtInt(int(math.Round(whole))) + suffix
	}
	return trimDecimal(whole) + suffix
}

func trimDecimal(value float64) string {
	text := fmtFloat(value, 1)
	if len(text) > 0 && text[len(text)-1] == '0' {
		text = text[:len(text)-2]
	}
	return text
}

func fmtInt(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits [32]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}

func fmtFloat(value float64, decimals int) string {
	if decimals == 1 {
		whole := int(value)
		fraction := int(math.Round((value - float64(whole)) * 10))
		if fraction == 10 {
			whole++
			fraction = 0
		}
		return fmtInt(whole) + "." + string(byte('0'+fraction))
	}
	return fmtInt(int(math.Round(value)))
}

type aiUsageBarGeometry struct {
	X, Y, W, H float32
}

func aiUsageBarGeometryFor(days []AIUsageDay) []aiUsageBarGeometry {
	values := make([]int, 7)
	start := 0
	if len(days) > len(values) {
		start = len(days) - len(values)
	}
	for i := start; i < len(days) && i-start < len(values); i++ {
		values[i-start] = aiUsageTokens(days[i])
	}
	maxValue := 0
	for _, value := range values {
		if value > maxValue {
			maxValue = value
		}
	}
	bars := make([]aiUsageBarGeometry, len(values))
	for i, value := range values {
		height := 2
		if maxValue > 0 && value > 0 {
			height = int(math.Round(float64(aiUsageBarMaxHeight) * float64(value) / float64(maxValue)))
			if height < 2 {
				height = 2
			}
		}
		bars[i] = aiUsageBarGeometry{
			X: float32(2 + i*(aiUsageBarWidth+aiUsageBarGap)),
			Y: float32(aiUsageChartBaseY - height),
			W: aiUsageBarWidth,
			H: float32(height),
		}
	}
	return bars
}

func aiUsageHistoryColor(theme *ui.Theme) ui.Color {
	if theme.Dark {
		return ui.RGBA(161, 161, 166, 0.32)
	}
	return ui.RGBA(142, 142, 147, 0.38)
}

func aiUsageBarColor(theme *ui.Theme, today bool) ui.Color {
	if today {
		return ui.Hex("#0A84FF")
	}
	return aiUsageHistoryColor(theme)
}

func renderAIUsageChart(c *ui.Context, theme *ui.Theme, days []AIUsageDay) {
	ui.Row(c).Key("ai-usage-chart").Label("ai-usage-chart").Width(aiUsageChartWidth).Height(aiUsageChartHeight).Padding(space0, space2, space2, space2).Gap(aiUsageBarGap).AlignItems(ui.End).Draw(func(p *ui.Painter, r ui.Rect) {
		baseline := ui.Rect{X: r.X + 2, Y: r.Y + aiUsageChartBaseY, W: 92, H: aiUsageChartBaseline}
		baselineColor := ui.RGBA(0, 0, 0, 0.09)
		if theme.Dark {
			baselineColor = ui.RGBA(255, 255, 255, 0.11)
		}
		p.Fill(baseline, baselineColor, 0)
	}).Children(func() {
		for i, bar := range aiUsageBarGeometryFor(days) {
			bar := bar
			ui.Box(c).Key(fmtKey("ai-usage-bar-", i)).Label(fmtKey("ai-usage-bar-", i)).Width(bar.W).Height(bar.H).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
				p.FillRadii(r, [4]float32{2, 2, 0, 0}, aiUsageBarColor(theme, i == 6))
			})
		}
	})
}

// The Stats hub plots the last aiUsageHubDays days as a stacked column chart.
// Tokens are the spend measure: this codebase never stores a price (see
// internal/storage/learning_store.go), so no currency figure is ever shown.
const aiUsageHubDays = 7

// Stack order of a usage column, bottom-up: output carries the stronger
// neutral step at the baseline, input the lighter one above it.
const (
	aiSeriesOutput = 0
	aiSeriesInput  = 1
)

// aiUsageLastDays keeps the newest n days of an oldest-first window.
func aiUsageLastDays(days []AIUsageDay, n int) []AIUsageDay {
	if len(days) > n {
		return days[len(days)-n:]
	}
	return days
}

// aiUsageSeriesNames are the legend and tooltip names of the two token series.
// Output tokens are the model's result, so the output series takes ai.view.result
// ("Result" / "结果"); the only other "Output" in the tables is the workflow
// setting's, whose Chinese reads "output mode".
func aiUsageSeriesNames() (input, output string) {
	return i18n.T("settings.field.input"), i18n.T("ai.view.result")
}

// aiUsageColumns turns days into stacked columns: output at the bottom, input
// on top, each labelled with its short date.
func aiUsageColumns(days []AIUsageDay) []chartColumn {
	cols := make([]chartColumn, len(days))
	for i, day := range days {
		cols[i] = chartColumn{
			Label:  chartShortDay(day.Day),
			Values: []float64{float64(day.OutputTokens), float64(day.InputTokens)},
		}
	}
	return cols
}

// aiUsageMaxTokens is the tallest stack of the window, the top of the y scale.
func aiUsageMaxTokens(days []AIUsageDay) int {
	top := 0
	for _, day := range days {
		top = max(top, aiUsageTokens(day))
	}
	return top
}

// aiUsageDayFill is the exact color of one mark. History uses two neutral
// steps (output stronger, input lighter); today, the newest column, uses the
// accent for output and a lighter blue step for input.
func aiUsageDayFill(pal chartPalette, series int, today bool) ui.Color {
	switch {
	case today && series == aiSeriesOutput:
		return pal.accent
	case today:
		return pal.accentSoft
	case series == aiSeriesOutput:
		return pal.strong
	}
	return pal.soft
}

// aiUsageDescribe is the plain-text reading of one day, for the selection
// announcement and screen readers: "11/19: Input 27.4k, Output 9.8k, 12 req".
func aiUsageDescribe(day AIUsageDay) string {
	input, output := aiUsageSeriesNames()
	return chartShortDay(day.Day) + ": " + input + " " + formatTokenCount(day.InputTokens) + ", " +
		output + " " + formatTokenCount(day.OutputTokens) + ", " + fmtInt(day.Requests) + " req"
}

// renderAIUsageHubChart is the AI card of the Stats hub: today's tokens and the
// seven-day total above a seven-day stacked column chart (input + output
// tokens per day) with a legend. Every figure comes from the cached usage
// snapshot; nothing is estimated.
func renderAIUsageHubChart(c *ui.Context, theme *ui.Theme, days []AIUsageDay) {
	days = aiUsageLastDays(days, aiUsageHubDays)
	if len(days) == 0 {
		return
	}
	pal := newChartPalette(theme)
	inputName, outputName := aiUsageSeriesNames()
	scale, ticks := chartTokenScale(aiUsageMaxTokens(days))
	last := len(days) - 1
	fill := func(series, column int) ui.Color { return aiUsageDayFill(pal, series, column == last) }

	ui.Column(c).Key("stats-ai-body").FillWidth().Grow(1).MinHeight(space0).Gap(space6).Children(func() {
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.End).Children(func() {
			ui.Column(c).Gap(space2).Children(func() {
				ui.Row(c).Gap(space6).AlignItems(ui.Center).Children(func() {
					// The accent dot is today's key: the same blue as today's column.
					ui.Box(c).Size(chartSwatchSize, chartSwatchSize).Radius(radiusPill).Background(pal.accent).Shrink(0)
					ui.Text(c, aiUsageTodayLabel(days)).Key("stats-ai-today").
						FontSize(typeKPI).FontWeight(600).FontFeatures("tnum").TextColor(theme.Text).SingleLine()
					ui.Text(c, chartShortDay(days[last].Day)).FontSize(typeMicro).FontFeatures("tnum").TextColor(pal.label).SingleLine()
				})
				ui.Text(c, i18n.Tf("stats.card.ai.summary", formatTokenCount(aiUsageTotalTokens(days)), aiUsageTotalRequests(days))).
					FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
			})
			ui.Spacer(c)
			kitChartLegend(c, pal, "stats-ai-legend", []chartLegendItem{
				{Label: inputName, Fill: pal.soft},
				{Label: outputName, Fill: pal.strong},
			})
		})
		kitChartColumns(c, theme, chartColumnsSpec{
			Key:        "stats-ai-chart",
			Label:      i18n.T("stats.card.ai.title"),
			Columns:    aiUsageColumns(days),
			Scale:      scale,
			Ticks:      ticks,
			LabelEvery: 1,
			Fill:       fill,
			Describe:   func(i int) string { return aiUsageDescribe(days[i]) },
			Tooltip: func(i int) {
				day := days[i]
				kitChartTipHeading(c, pal, chartShortDay(day.Day)+" · "+fmtInt(day.Requests)+" req")
				in, out := fill(aiSeriesInput, i), fill(aiSeriesOutput, i)
				kitChartTipFigure(c, pal, &in, formatTokenCount(day.InputTokens), inputName)
				kitChartTipFigure(c, pal, &out, formatTokenCount(day.OutputTokens), outputName)
			},
		})
	})
}

func fmtKey(prefix string, index int) string { return prefix + fmtInt(index) }
