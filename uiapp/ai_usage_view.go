package uiapp

import (
	"context"
	"math"
	"time"

	"github.com/egoist/mygo/ui"
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
			ui.Text(c, "AI 用量暂不可用").Key("ai-usage-error").FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		case !loaded || refreshing:
			ui.Text(c, "正在加载 AI 用量…").Key("ai-usage-loading").FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
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

func fmtKey(prefix string, index int) string { return prefix + fmtInt(index) }
