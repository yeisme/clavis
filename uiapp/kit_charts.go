package uiapp

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
)

// Data-surface chart kit.
//
// The hubs draw real charts with MyGo's headless chart package: scales and
// ticks come from chart.NewLinear / chart.NewBand, marks are elements placed
// by chart.Rect / chart.Point / chart.RuleY, the pointer and the keyboard
// drive chart.Select, and a small opaque tooltip is placed with chart.Anchor.
// This file adds only the look, which is deliberately plain:
//
//   - grayscale plus one accent: history marks use two neutral steps and the
//     system blue (#0A84FF) marks only today, the latest point, a running
//     state or the active filter;
//   - thin marks with a rounded data end on a square baseline, a 2dp gap in
//     the surface color between touching marks, recessive hairline gridlines
//     and a slightly stronger baseline;
//   - text is always an ink (theme.Text / TextMuted / secondaryInk), never a
//     series color, and a series is never identified by color alone: a legend,
//     an icon or a direct label always backs it.
//
// Everything in the first half of the file is pure (no ui.Context), so ticks,
// stacking, shares and segment widths are unit-tested without rendering.

// Geometry shared by every hub chart (DIPs unless noted).
const (
	chartDataRadius  float32 = 4   // rounded data end of a column or bar
	chartBlockRadius float32 = 2   // inner corner of two touching blocks
	chartGapSize     float32 = 2   // surface gap between touching marks
	chartRuleSize    float32 = 1   // hairline gridline and baseline
	chartLineSize    float32 = 2   // series line
	chartDotSize     float32 = 8   // end-dot of a line; marks must be >= 8
	chartDotRing     float32 = 2   // surface ring around a dot
	chartSoloDot     float32 = 4   // a lone observation between two gaps
	chartThinBarSize float32 = 8   // a segmented bar that sits beside its figures
	chartTallBarSize float32 = 22  // a segmented bar that carries its own figures (<= 28)
	chartSwatchSize  float32 = 8   // legend swatch
	chartChipSize    float32 = 16  // legend icon chip
	chartChipGlyph   float32 = 11  // glyph inside a chip
	chartMinBlock    float32 = 4   // a nonzero segment never gets thinner than this
	chartMinPlot     float32 = 40  // a plot area never gets shorter than this
	chartAxisBand    float32 = 18  // the bottom axis labels and their gap, below the area
	chartTopRoom     float32 = 7   // half a tick label: room above the plot for the highest one
	chartPlotGapX    float32 = 6   // between the left axis labels and the plot
	chartPlotGapY    float32 = 4   // between the plot and the bottom axis labels
	chartInlinePad   float32 = 6   // air on each side of a label printed inside a block
	chartTipGap      float32 = 6   // between a tooltip and the mark it explains
	chartRankBar     float32 = 6   // thickness of a ranked bar
	chartRankLabel   float32 = 64  // widest label column of a ranked list
	chartMinRankBar  float32 = 2   // a nonzero ranked bar never gets thinner than this
	chartRefBarWidth float32 = 160 // narrowest plausible segmented bar; label-fit decisions use it
	// chartGlyphEm is the conservative advance of one tabular digit or CJK
	// glyph, as a fraction of the font size.
	chartGlyphEm float32 = 0.62
	// chartColumnInner / chartColumnOuter are the band paddings of a column
	// chart, in fractions of a band's step: bars about half a step wide, with
	// a little air at both ends.
	chartColumnInner float32 = 0.5
	chartColumnOuter float32 = 0.1
	// A line chart's bands touch (each day owns an equal slice and its point
	// sits in the middle), with no extra air at the ends.
	chartLineInner float32 = 0
	chartLineOuter float32 = 0
	// chartHoverEdge is how near an edge (as a fraction of the plot) a
	// tooltip's anchor must be for the tooltip to open inward instead of
	// centred, so it never hangs outside its card.
	chartHoverEdge float32 = 0.28
	// chartGapMinSpan is the smallest fraction of a plot a stacked segment
	// must cover for the surface gap above it to be drawn; a thinner segment
	// would be eaten by its own gap.
	chartGapMinSpan float32 = 0.06
	// chartWhiteInkMin is the contrast at which white text is still preferred
	// on a mid-tone fill (the accent blue reads 3.7:1).
	chartWhiteInkMin = 3.5
)

// The few hard-coded colors of the data surfaces. Everything else is derived
// from the theme, so a palette swap carries over.
var (
	chartAccent      = ui.Hex("#0A84FF") // system blue: today, latest, running, filter
	chartStrongLight = ui.Hex("#8E8E93") // system gray: the stronger history step, light
	chartStrongDark  = ui.Hex("#98989D") // the stronger history step, dark
	chartInkNear     = ui.Hex("#1D1D1F") // near-black text inside a light fill
	chartInkWhite    = ui.Hex("#FFFFFF") // white text inside a dark fill
)

// chartOpaque drops any transparency: the surfaces a chart is cut out of
// (the card, the tooltip) are opaque, so a gap drawn in them truly separates.
func chartOpaque(c ui.Color) ui.Color {
	c.A = 255
	return c
}

// chartPalette is every color a hub chart paints with, resolved once per
// frame from the theme.
type chartPalette struct {
	strong     ui.Color // the stronger neutral step: output tokens, a single series
	soft       ui.Color // the lighter neutral step: input tokens
	accent     ui.Color // #0A84FF
	accentSoft ui.Color // the lighter blue step (today's secondary series)
	grid       ui.Color // recessive hairline gridlines
	baseline   ui.Color // the slightly stronger baseline
	surface    ui.Color // the card the chart is cut out of; gaps and rings use it
	tooltip    ui.Color // the opaque tooltip surface
	wash       ui.Color // the hovered column's backdrop
	label      ui.Color // axis labels and captions (a text ink)
	value      ui.Color // emphasised figures (a text ink)
}

func newChartPalette(t *ui.Theme) chartPalette {
	surface := chartOpaque(t.Surface)
	strong := chartStrongLight
	tooltip := chartOpaque(t.Background)
	if t.Dark {
		strong = chartStrongDark
		// A dark popover sits lighter than the card it floats over.
		tooltip = surface.Mix(chartOpaque(t.Text), alphaSubtle)
	}
	return chartPalette{
		strong:     strong,
		soft:       strong.Mix(surface, 0.55),
		accent:     chartAccent,
		accentSoft: chartAccent.Mix(surface, 0.50),
		grid:       hairline(t),
		baseline:   chartOpaque(t.Border),
		surface:    surface,
		tooltip:    tooltip,
		wash:       tileFill(t),
		label:      secondaryInk(t),
		value:      t.Text,
	}
}

// chartInkOn picks the text ink for a label printed inside fill: white or
// near-black, the same two in both modes. White wins whenever it reads at
// chartWhiteInkMin or better (so white sits on the accent blue, as it does
// across macOS); otherwise the better of the two.
func chartInkOn(fill ui.Color) ui.Color {
	white, near := contrastRatio(chartInkWhite, fill), contrastRatio(chartInkNear, fill)
	if white >= chartWhiteInkMin || white >= near {
		return chartInkWhite
	}
	return chartInkNear
}

// ---- pure helpers -----------------------------------------------------------

// chartShortDay renders "2026-11-19" as "11/19": month and day without
// padding, the same shape as clipboard.row.date, and the same in every
// language. A string that is not a date comes back unchanged, so a malformed
// row still labels its column.
func chartShortDay(day string) string {
	day = strings.TrimSpace(day)
	parts := strings.Split(day, "-")
	if len(parts) != 3 {
		return day
	}
	month, errM := strconv.Atoi(parts[1])
	dom, errD := strconv.Atoi(parts[2])
	if errM != nil || errD != nil || month < 1 || month > 12 || dom < 1 || dom > 31 {
		return day
	}
	return fmtInt(month) + "/" + fmtInt(dom)
}

// chartThinTicks keeps every n-th tick counting back from the last one, so a
// crowded axis shows fewer labels and the newest (today) always survives.
func chartThinTicks(ticks []chart.Tick, every int) []chart.Tick {
	if every <= 1 || len(ticks) == 0 {
		return ticks
	}
	out := make([]chart.Tick, 0, len(ticks)/every+1)
	for i := len(ticks) - 1; i >= 0; i -= every {
		out = append(out, ticks[i])
	}
	for l, r := 0, len(out)-1; l < r; l, r = l+1, r-1 {
		out[l], out[r] = out[r], out[l]
	}
	return out
}

// chartNiceScale is a zero-based linear scale rounded out to about want round
// ticks, with those ticks labelled by format. A maximum that is not a positive
// number leaves a 0..1 scale with a single "0" tick, so an all-zero chart still
// has its baseline.
func chartNiceScale(maxValue float64, want int, format func(float64) string) (chart.Linear, []chart.Tick) {
	if !(maxValue > 0) || math.IsInf(maxValue, 0) {
		flat := chart.Linear{Lo: 0, Hi: 1, Format: format}
		return flat, []chart.Tick{{At: 0, Value: 0, Label: format(0)}}
	}
	scale := chart.NewLinear(0, maxValue).Nice(want)
	scale.Format = format
	return scale, scale.Ticks(want)
}

// chartTokenScale is the y scale of a token chart: 3-4 round ticks labelled
// with formatTokenCount, so the axis reads like every other token figure.
func chartTokenScale(maxTokens int) (chart.Linear, []chart.Tick) {
	return chartNiceScale(float64(maxTokens), 3, func(v float64) string {
		return formatTokenCount(int(math.Round(v)))
	})
}

// chartCountScale is the y scale of an item-count chart. Counts are whole
// numbers, so a small maximum gets just the baseline and a top tick instead of
// fractional steps.
func chartCountScale(maxCount int) (chart.Linear, []chart.Tick) {
	format := func(v float64) string { return fmtInt(int(math.Round(v))) }
	if maxCount <= 0 {
		return chartNiceScale(0, 2, format)
	}
	if maxCount <= 4 {
		top := float64(max(maxCount, 2))
		scale := chart.Linear{Lo: 0, Hi: top, Format: format}
		return scale, []chart.Tick{{At: 0, Value: 0, Label: format(0)}, {At: 1, Value: top, Label: format(top)}}
	}
	return chartNiceScale(float64(maxCount), 2, format)
}

// chartPercentTicks are the 0 / 50 / 100 % ticks of a rate axis.
func chartPercentTicks() []chart.Tick {
	return []chart.Tick{
		{At: 0, Value: 0, Label: "0%"},
		{At: 0.5, Value: 50, Label: "50%"},
		{At: 1, Value: 100, Label: "100%"},
	}
}

// chartBandSlot is the whole step of band i, the bar and the air around it:
// what the hover wash covers and what the pointer is "over".
func chartBandSlot(b chart.Band, i int) chart.Span {
	mid := b.Band(i).Mid()
	step := b.Width()
	if b.Inner < 1 {
		step /= 1 - b.Inner
	}
	return chart.Span{Lo: mid - step/2, Hi: mid + step/2}
}

// chartPercents rounds each value's share of the positive total to whole
// percents that add up to exactly 100 (largest remainder), so a legend never
// reads 33% + 33% + 33%. Non-positive values get 0.
func chartPercents(values []float64) []int {
	out := make([]int, len(values))
	total := 0.0
	for _, v := range values {
		if v > 0 {
			total += v
		}
	}
	if total <= 0 {
		return out
	}
	type remainder struct {
		index int
		frac  float64
	}
	rems := make([]remainder, 0, len(values))
	sum := 0
	for i, v := range values {
		if v <= 0 {
			continue
		}
		exact := v / total * 100
		out[i] = int(math.Floor(exact))
		sum += out[i]
		rems = append(rems, remainder{i, exact - math.Floor(exact)})
	}
	sort.SliceStable(rems, func(a, b int) bool { return rems[a].frac > rems[b].frac })
	for k := 0; sum < 100 && k < len(rems); k++ {
		out[rems[k].index]++
		sum++
	}
	return out
}

// chartSegmentWidths splits width among the positive values in proportion,
// with gap between neighbours and no segment thinner than minWidth (a thin one
// is held at the minimum and the rest share what is left, as a flex row does).
// Non-positive values get 0, and the widths plus the gaps add up to width.
func chartSegmentWidths(values []float64, width, gap, minWidth float32) []float32 {
	out := make([]float32, len(values))
	var live []int
	weight := 0.0
	for i, v := range values {
		if v > 0 && !math.IsInf(v, 0) {
			live = append(live, i)
			weight += v
		}
	}
	if len(live) == 0 || width <= 0 {
		return out
	}
	remaining := width - gap*float32(len(live)-1)
	if remaining <= 0 {
		return out
	}
	frozen := make([]bool, len(values))
	for {
		var thin []int
		for _, i := range live {
			if !frozen[i] && remaining*float32(values[i]/weight) < minWidth {
				thin = append(thin, i)
			}
		}
		if len(thin) == 0 {
			break
		}
		for _, i := range thin {
			frozen[i] = true
			out[i] = minWidth
			remaining -= minWidth
			weight -= values[i]
		}
		if weight <= 0 || remaining <= 0 {
			break
		}
	}
	for _, i := range live {
		if !frozen[i] && weight > 0 {
			out[i] = max(0, remaining) * float32(values[i]/weight)
		}
	}
	return out
}

// chartTextWidth is a deliberately generous estimate of a run of tabular
// digits or CJK glyphs, for deciding at build time whether a label fits.
func chartTextWidth(text string, size float32) float32 {
	return float32(utf8.RuneCountInString(text)) * size * chartGlyphEm
}

// chartInlineFits reports, per segment, whether its text fits inside it with
// air on both sides, judged at chartRefBarWidth (the narrowest bar the hubs
// lay out), so a label printed inside a block is never clipped at any width.
// A segment whose text does not fit prints none and leaves it to the legend.
func chartInlineFits(values []float64, texts []string, size float32) []bool {
	widths := chartSegmentWidths(values, chartRefBarWidth, chartGapSize, chartMinBlock)
	out := make([]bool, len(values))
	for i := range values {
		if i < len(texts) && texts[i] != "" && widths[i] > 0 {
			out[i] = widths[i] >= chartTextWidth(texts[i], size)+2*chartInlinePad
		}
	}
	return out
}

// chartLatestIndex is the last observation of a series, skipping the gaps
// (NaN), or -1 when it has none.
func chartLatestIndex(values []float64) int {
	for i := len(values) - 1; i >= 0; i-- {
		if !math.IsNaN(values[i]) {
			return i
		}
	}
	return -1
}

// chartRank is one row of a ranked bar list.
type chartRank struct {
	Label string
	Value int
	Frac  float64 // value / the largest value, 0..1
}

// ---- axes, legend, tooltip --------------------------------------------------

// chartAxisLabeler builds an axis tick label: a tabular micro figure in the
// recessive ink. The newest label of the bottom axis (today) is set in the
// primary ink and heavier, so the eye finds "now" without a color.
func chartAxisLabeler(c *ui.Context, pal chartPalette, newest int) func(chart.Side, chart.Tick) {
	return func(side chart.Side, t chart.Tick) {
		label := ui.Text(c, t.Label).NoWrap().FontSize(typeMicro).FontFeatures("tnum").TextColor(pal.label)
		if side == chart.Bottom && newest >= 0 && int(t.Value) == newest {
			label.TextColor(pal.value).FontWeight(600)
		}
	}
}

// chartLegendItem is one entry of a series legend: the mark's exact color and
// its name. The name is a text ink; the swatch carries the color.
type chartLegendItem struct {
	Label string
	Fill  ui.Color
}

// kitChartLegend is the row of swatch + name pairs that identifies two or more
// series (a single series needs none).
func kitChartLegend(c *ui.Context, pal chartPalette, key string, items []chartLegendItem) ui.Element {
	return ui.Row(c).Key(key).Label(key).Gap(space10).AlignItems(ui.Center).Shrink(0).Children(func() {
		for _, item := range items {
			ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(chartSwatchSize, chartSwatchSize).Radius(chartBlockRadius).Background(item.Fill).Shrink(0)
				ui.Text(c, item.Label).FontSize(typeMicro).TextColor(pal.label).NoWrap()
			})
		}
	})
}

// kitChartChip is an icon on a rounded tile filled with a mark's exact color:
// the swatch and the shape that identifies a part, in one 16dp square. The
// glyph's ink is white or near-black by the fill's luminance.
func kitChartChip(c *ui.Context, pal chartPalette, fill ui.Color, icon string) {
	ink := chartInkOn(fill.Over(pal.surface))
	ui.Box(c).Size(chartChipSize, chartChipSize).Radius(chartDataRadius).Background(fill).Center().Shrink(0).Children(func() {
		renderSemanticIcon(c, icon, chartChipGlyph, ink)
	})
}

// kitChartTooltip is the small opaque tooltip chart.Anchor places by a mark.
// It is a surface of its own (no translucency), with a hairline, a soft
// shadow and the gap that keeps it off the mark it explains.
func kitChartTooltip(c *ui.Context, theme *ui.Theme, pal chartPalette, body func()) {
	sx, sy, blur, spread, shadow := elevationShadow(theme, elev2)
	ui.Column(c).Padding(space6, space8).Gap(space2).Radius(radius8).
		Background(pal.tooltip).Border(space1, hairline(theme)).Shadow(sx, sy, blur, spread, shadow).
		Margin(space0, space0, chartTipGap, space0).PassThrough().Children(body)
}

// kitChartTipFigure is one tooltip row: the figure leads, the name follows
// (the legend's hierarchy inverted, because the reader already has the series
// and wants the number). A nil fill omits the swatch.
func kitChartTipFigure(c *ui.Context, pal chartPalette, fill *ui.Color, figure, name string) {
	ui.Row(c).Gap(space6).AlignItems(ui.Center).Children(func() {
		if fill != nil {
			ui.Box(c).Size(chartSwatchSize, chartSwatchSize).Radius(chartBlockRadius).Background(*fill).Shrink(0)
		}
		ui.Text(c, figure).NoWrap().FontSize(typeMeta).FontWeight(600).FontFeatures("tnum").TextColor(pal.value)
		if name != "" {
			ui.Text(c, name).NoWrap().FontSize(typeMicro).TextColor(pal.label)
		}
	})
}

// kitChartTipHeading is the tooltip's first line: the category (a date), in the
// recessive ink.
func kitChartTipHeading(c *ui.Context, pal chartPalette, text string) {
	ui.Text(c, text).NoWrap().FontSize(typeMicro).FontFeatures("tnum").TextColor(pal.label)
}

// chartTipAlign opens a tooltip toward the middle of the plot when its anchor
// is near an edge, so it never hangs outside the card.
func chartTipAlign(x float32) ui.Align {
	switch {
	case x < chartHoverEdge:
		return ui.Start
	case x > 1-chartHoverEdge:
		return ui.End
	}
	return ui.Center
}

// chartHairlineRule is a horizontal gridline across the plot at y (a fraction
// up it); the baseline is the same rule in the stronger color.
func chartHairlineRule(c *ui.Context, pal chartPalette, tick chart.Tick) {
	rule := chart.RuleY(c, tick.At, chartRuleSize).PassThrough()
	if tick.Value == 0 {
		rule.Background(pal.baseline)
		return
	}
	rule.Background(pal.grid)
}

// ---- stacked column chart ---------------------------------------------------

// chartColumn is one stack of a column chart: its axis label and its series
// values, bottom-up.
type chartColumn struct {
	Label  string
	Values []float64
}

// chartColumnsSpec describes a stacked column chart. The scale and its ticks
// come from chartNiceScale (or a sibling), so every number on the axis is a
// round value of the data's own range.
type chartColumnsSpec struct {
	Key        string                            // stable id of the plot; the plot area is Key+"-area"
	Label      string                            // what the plot shows, for assistive technology
	Columns    []chartColumn                     // oldest first; the last one is "now"
	Scale      chart.Linear                      // y scale, zero-based
	Ticks      []chart.Tick                      // y ticks (the gridlines)
	LabelEvery int                               // show every n-th x label, counting back from the last
	Fill       func(series, column int) ui.Color // exact color of a mark
	Describe   func(column int) string           // "11/19: ..." for the selection and screen readers
	Tooltip    func(column int)                  // builds the tooltip body; nil for none
	ValueLabel func(column int) string           // optional label above the newest column
}

// kitChartColumns draws a stacked column chart on a chart.Plot: round y ticks
// with hairline gridlines and a stronger baseline, bars no thicker than half a
// band with a rounded data end on a square baseline and a 2dp surface gap
// between stacked segments, the newest column coloured by Fill, and a hovered
// or keyboard-selected column backed by a wash and a tooltip.
func kitChartColumns(c *ui.Context, theme *ui.Theme, spec chartColumnsSpec) ui.Element {
	pal := newChartPalette(theme)
	n := len(spec.Columns)
	labels := make([]string, n)
	seriesCount := 0
	for i, col := range spec.Columns {
		labels[i] = col.Label
		seriesCount = max(seriesCount, len(col.Values))
	}
	x := chart.NewBand(labels...).Padding(chartColumnInner, chartColumnOuter)
	series := make([][]float64, seriesCount)
	for s := range series {
		series[s] = make([]float64, n)
		for i, col := range spec.Columns {
			if s < len(col.Values) {
				series[s][i] = col.Values[s]
			}
		}
	}
	stacked := chart.Stack(series, chart.StackStandard)
	xs := make([]float32, n)
	for i := range xs {
		xs[i] = x.At(i)
	}
	axes := chart.Axes{
		Left:   spec.Ticks,
		Bottom: chartThinTicks(x.Ticks(), spec.LabelEvery),
		Label:  chartAxisLabeler(c, pal, n-1),
	}
	plot := chart.Plot(c, axes, func(area ui.Element) {
		area.Key(spec.Key + "-area").Label(spec.Key + "-area")
		selected := ui.Local(area, "kit-chart-selected", func() int { return -1 })
		chart.Select(c, area, selected, xs, spec.Describe)

		for _, tick := range spec.Ticks {
			chartHairlineRule(c, pal, tick)
		}
		if *selected >= 0 && *selected < n {
			chart.Rect(c, chartBandSlot(x, *selected), chart.Span{Lo: 0, Hi: 1}).
				Background(pal.wash).Radius(radius4).PassThrough()
		}
		for i := range spec.Columns {
			top := -1 // the last non-empty segment carries the rounded data end
			for s := range stacked {
				if stacked[s][i].Hi-stacked[s][i].Lo > 0 {
					top = s
				}
			}
			for s := range stacked {
				seg := stacked[s][i]
				if seg.Hi-seg.Lo <= 0 {
					continue
				}
				id := spec.Key + "-bar-" + fmtInt(i) + "-" + fmtInt(s)
				// A bar is hidden from assistive technology: the plot itself
				// announces each day's whole reading (Select's describe), so a
				// screen reader never meets the bars' test ids.
				mark := chart.Rect(c, x.Band(i), spec.Scale.Span(seg.Lo, seg.Hi)).
					Key(id).Label(id).Background(spec.Fill(s, i)).PassThrough().AccessHidden(true)
				if s == top {
					mark.Radius(chartDataRadius, chartDataRadius, 0, 0)
				}
			}
			// The surface gap between two stacked segments is carved out of the
			// stack (not added to it), so the column's height stays exact.
			for s := 1; s < len(stacked); s++ {
				below, above := stacked[s-1][i], stacked[s][i]
				lowSpan := float32(spec.Scale.At(below.Hi) - spec.Scale.At(below.Lo))
				highSpan := float32(spec.Scale.At(above.Hi) - spec.Scale.At(above.Lo))
				if lowSpan < chartGapMinSpan || highSpan < chartGapMinSpan {
					continue
				}
				band := x.Band(i)
				ui.Box(c).Absolute().LeftPercent(100*band.Lo).WidthPercent(100*(band.Hi-band.Lo)).
					TopPercent(100*(1-spec.Scale.At(below.Hi))).Height(chartGapSize).
					Margin(-chartGapSize/2, 0, 0, 0).Background(pal.surface).PassThrough()
			}
		}
		if spec.ValueLabel != nil && n > 0 {
			if text := spec.ValueLabel(n - 1); text != "" {
				total := 0.0
				for s := range stacked {
					total = max(total, stacked[s][n-1].Hi)
				}
				chart.Anchor(c, x.At(n-1), spec.Scale.At(total), ui.Center, ui.End, func() {
					ui.Text(c, text).NoWrap().FontSize(typeMicro).FontWeight(600).FontFeatures("tnum").
						TextColor(pal.value).Margin(space0, space0, space2, space0)
				}).PassThrough()
			}
		}
		if i := *selected; i >= 0 && i < n && spec.Tooltip != nil {
			total := 0.0
			for s := range stacked {
				total = max(total, stacked[s][i].Hi)
			}
			chart.Anchor(c, xs[i], max(spec.Scale.At(total), 0.04), chartTipAlign(xs[i]), ui.End, func() {
				kitChartTooltip(c, theme, pal, func() { spec.Tooltip(i) })
			})
		}
	})
	plot.Key(spec.Key).Label(spec.Key).GapX(chartPlotGapX).GapY(chartPlotGapY).
		FillWidth().Grow(1).MinHeight(chartMinPlot+chartAxisBand).Margin(chartTopRoom, space0, space0, space0)
	if spec.Describe != nil && n > 0 {
		plot.Description(spec.Label)
	}
	return plot
}

// ---- line chart -------------------------------------------------------------

// chartLineSpec describes a one-series line chart on a 0..1 rate axis.
type chartLineSpec struct {
	Key        string
	Label      string
	Labels     []string  // x label of each observation, oldest first
	Values     []float64 // rate 0..1 per observation; NaN where nothing was observed
	LabelEvery int
	Describe   func(i int) string
	Tooltip    func(i int) // builds the tooltip body; nil for none
}

// kitChartLine draws a 2dp neutral line on a 0-100% axis (ticks 0/50/100, the
// same hairlines and baseline as the columns). An observation-free day is a gap,
// never a zero: the line breaks there. A lone observation between two gaps is a
// small dot, so no real data is ever invisible, and only the latest observation
// carries the accent dot with its surface ring. A hovered or keyboard-selected
// day gets a hairline rule, a neutral dot and a tooltip.
func kitChartLine(c *ui.Context, theme *ui.Theme, spec chartLineSpec) ui.Element {
	pal := newChartPalette(theme)
	n := len(spec.Values)
	x := chart.NewBand(spec.Labels...).Padding(chartLineInner, chartLineOuter)
	y := chart.NewLinear(0, 1)
	xs := make([]float32, n)
	ys := make([]float32, n)
	for i, v := range spec.Values {
		xs[i] = x.At(i)
		ys[i] = float32(math.NaN())
		if !math.IsNaN(v) {
			ys[i] = y.At(v)
		}
	}
	latest := chartLatestIndex(spec.Values)
	ticks := chartPercentTicks()
	axes := chart.Axes{
		Left:   ticks,
		Bottom: chartThinTicks(x.Ticks(), spec.LabelEvery),
		Label:  chartAxisLabeler(c, pal, n-1),
	}
	plot := chart.Plot(c, axes, func(area ui.Element) {
		area.Key(spec.Key + "-area").Label(spec.Key + "-area")
		selected := ui.Local(area, "kit-chart-selected", func() int { return -1 })
		chart.Select(c, area, selected, xs, spec.Describe)

		for _, tick := range ticks {
			chartHairlineRule(c, pal, tick)
		}
		if i := *selected; i >= 0 && i < n {
			chart.RuleX(c, xs[i], chartRuleSize).Background(pal.baseline).PassThrough()
		}
		// The line itself paints after the gridlines (an absolute child sits
		// above them), so a gridline never lightens it.
		ui.Box(c).Absolute().Left(0).Right(0).Top(0).Bottom(0).PassThrough().Draw(func(p *ui.Painter, r ui.Rect) {
			p.StrokePath(chart.Line(r, xs, ys, chart.CurveLinear), chartLineSize, pal.strong)
			for i := range ys {
				if math.IsNaN(float64(ys[i])) {
					continue
				}
				lonely := (i == 0 || math.IsNaN(float64(ys[i-1]))) && (i == n-1 || math.IsNaN(float64(ys[i+1])))
				if lonely {
					p.Fill(ui.Rect{
						X: chart.X(r, xs[i]) - chartSoloDot/2, Y: chart.Y(r, ys[i]) - chartSoloDot/2,
						W: chartSoloDot, H: chartSoloDot,
					}, pal.strong, chartSoloDot/2)
				}
			}
		})
		if latest >= 0 {
			ring := chartDotSize + 2*chartDotRing
			chart.Point(c, xs[latest], ys[latest], ring, ring).
				Key(spec.Key + "-latest-ring").Radius(ring / 2).Background(pal.surface).PassThrough()
			chart.Point(c, xs[latest], ys[latest], chartDotSize, chartDotSize).
				Key(spec.Key + "-latest").Label(spec.Key + "-latest").Radius(chartDotSize / 2).Background(pal.accent).
				PassThrough().AccessHidden(true)
		}
		if i := *selected; i >= 0 && i < n {
			if !math.IsNaN(float64(ys[i])) && i != latest {
				ring := chartDotSize + 2*chartDotRing
				chart.Point(c, xs[i], ys[i], ring, ring).Radius(ring / 2).Background(pal.surface).PassThrough()
				chart.Point(c, xs[i], ys[i], chartDotSize, chartDotSize).
					Radius(chartDotSize / 2).Background(pal.value).PassThrough()
			}
			if spec.Tooltip != nil {
				at := float32(0.5)
				if !math.IsNaN(float64(ys[i])) {
					at = ys[i]
				}
				chart.Anchor(c, xs[i], at, chartTipAlign(xs[i]), ui.End, func() {
					kitChartTooltip(c, theme, pal, func() { spec.Tooltip(i) })
				})
			}
		}
	})
	plot.Key(spec.Key).Label(spec.Key).GapX(chartPlotGapX).GapY(chartPlotGapY).
		FillWidth().Grow(1).MinHeight(chartMinPlot+chartAxisBand).Margin(chartTopRoom, space0, space0, space0)
	if spec.Describe != nil && n > 0 {
		plot.Description(spec.Label)
	}
	return plot
}

// ---- ranked bars ------------------------------------------------------------

// kitChartRankedBars draws one thin horizontal bar per row, longest first, each
// with its value printed at the bar's tip. All bars share one baseline, one
// neutral color (a ranking is one series; blue is not used for "the biggest")
// and one scale: every row reserves the same width for its value label, so a
// bar's length is exactly its value's share of the longest and rows can be
// compared by eye.
func kitChartRankedBars(c *ui.Context, theme *ui.Theme, key string, rows []chartRank) ui.Element {
	pal := newChartPalette(theme)
	labelW, valueW := float32(0), float32(0)
	for _, row := range rows {
		w, _ := c.MeasureText(0, ui.Span{Text: row.Label, Size: typeMicro})
		labelW = max(labelW, min(w, chartRankLabel))
		v, _ := c.MeasureText(0, ui.Span{Text: fmtInt(row.Value), Size: typeMicro, Features: "tnum"})
		valueW = max(valueW, v)
	}
	return ui.Column(c).Key(key).Label(key).FillWidth().Gap(space4).Children(func() {
		for i, row := range rows {
			id := key + "-row-" + fmtInt(i)
			ui.Row(c).Key(id).Label(id).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, row.Label).FontSize(typeMicro).TextColor(pal.label).SingleLine().Width(labelW + space2).Shrink(0)
				ui.Row(c).Grow(1).MinWidth(0).Gap(space4).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Key(id+"-bar").Label(id+"-bar").Basis(0).Grow(float32(row.Frac)).
						MinWidth(chartMinRankBar).Height(chartRankBar).
						Radius(0, chartDataRadius, chartDataRadius, 0).Background(pal.strong)
					ui.Text(c, fmtInt(row.Value)).FontSize(typeMicro).FontFeatures("tnum").TextColor(pal.value).NoWrap().
						Width(valueW + space2).Shrink(0)
					ui.Box(c).Basis(0).Grow(float32(1 - row.Frac))
				})
			})
		}
	})
}

// ---- segmented bar ----------------------------------------------------------

// chartPart is one block of a segmented bar.
type chartPart struct {
	Key   string   // stable id of the block
	Value float64  // its size; non-positive blocks draw nothing
	Fill  ui.Color // exact color of the block
	Text  string   // printed inside the block when it fits ("" prints none)
	Tip   string   // native tooltip and accessible description: name, amount, share
}

// chartBarSpec describes one whole split into parts.
type chartBarSpec struct {
	Key    string
	Parts  []chartPart
	Height float32 // chartThinBarSize or chartTallBarSize
}

// kitChartSegmentBar draws one whole split into parts: widths proportional to
// the values (a flex row), a 2dp surface gap between blocks, none thinner than
// chartMinBlock, rounded outer ends. A part at zero draws nothing. A tall bar
// prints each part's Text inside it, in white or near-black by the fill, only
// where chartInlineFits says it fits; a thin bar prints nothing.
func kitChartSegmentBar(c *ui.Context, spec chartBarSpec) ui.Element {
	values := make([]float64, len(spec.Parts))
	texts := make([]string, len(spec.Parts))
	for i, part := range spec.Parts {
		values[i] = part.Value
		texts[i] = part.Text
	}
	fits := chartInlineFits(values, texts, typeMeta)
	var live []int
	for i, part := range spec.Parts {
		if part.Value > 0 {
			live = append(live, i)
		}
	}
	endRadius := chartDataRadius
	if spec.Height <= chartThinBarSize {
		endRadius = spec.Height / 2 // thin bars are fully rounded
	}
	// The bar carries one reading of every part (name, amount, share) for
	// assistive technology; the blocks themselves are hidden from it, so the
	// counts printed inside them are not read twice.
	var reading []string
	for _, i := range live {
		if tip := spec.Parts[i].Tip; tip != "" {
			reading = append(reading, tip)
		}
	}
	return ui.Row(c).Key(spec.Key).Label(spec.Key).Description(strings.Join(reading, "; ")).
		FillWidth().Height(spec.Height).Gap(chartGapSize).Children(func() {
		for pos, i := range live {
			part := spec.Parts[i]
			first, last := pos == 0, pos == len(live)-1
			tl, tr, br, bl := chartBlockRadius, chartBlockRadius, chartBlockRadius, chartBlockRadius
			if first {
				tl, bl = endRadius, endRadius
			}
			if last {
				tr, br = endRadius, endRadius
			}
			block := ui.Box(c).Key(part.Key).Label(part.Key).Basis(0).Grow(float32(part.Value)).Height(spec.Height).
				MinWidth(chartMinBlock).Radius(tl, tr, br, bl).Background(part.Fill).Center().AccessHidden(true)
			if part.Tip != "" {
				block.Tooltip(part.Tip)
			}
			if fits[i] {
				ink := chartInkOn(part.Fill)
				block.Children(func() {
					ui.Text(c, part.Text).NoWrap().FontSize(typeMeta).FontWeight(600).FontFeatures("tnum").TextColor(ink)
				})
			}
		}
	})
}

// chartKeyCell is one right-aligned figure of a key row.
type chartKeyCell struct {
	Text   string
	Strong bool // the primary ink, otherwise the recessive one
}

// chartKeyWidths measures the widest figure of each column across rows, so the
// figures of consecutive rows line up (right-aligned, tabular).
func chartKeyWidths(c *ui.Context, rows [][]chartKeyCell) []float32 {
	var widths []float32
	for _, row := range rows {
		for col, cell := range row {
			for len(widths) <= col {
				widths = append(widths, 0)
			}
			w, _ := c.MeasureText(0, ui.Span{Text: cell.Text, Size: typeMicro, Features: "tnum"})
			widths[col] = max(widths[col], w+space2)
		}
	}
	return widths
}

// kitChartKeyRow is one legend row of a composition: the icon chip in the
// part's exact color, its name, then right-aligned figures at widths shared by
// all rows, so the figures of consecutive rows line up.
func kitChartKeyRow(c *ui.Context, pal chartPalette, key string, fill ui.Color, icon, name string, cells []chartKeyCell, widths []float32) {
	row := ui.Row(c).Key(key).Label(key).FillWidth().Gap(space6).AlignItems(ui.Center)
	row.Children(func() {
		kitChartChip(c, pal, fill, icon)
		ui.Text(c, name).FontSize(typeMicro).TextColor(pal.label).SingleLine().Grow(1).MinWidth(0)
		for col, cell := range cells {
			ink := pal.label
			if cell.Strong {
				ink = pal.value
			}
			ui.Text(c, cell.Text).FontSize(typeMicro).FontFeatures("tnum").TextColor(ink).NoWrap().
				Width(widths[col]).TextAlign(ui.End).Shrink(0)
		}
	})
}
