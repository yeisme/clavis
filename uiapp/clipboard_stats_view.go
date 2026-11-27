package uiapp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/storage"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

const (
	clipboardStatsBandHeight float32 = 28
	clipboardStripHeight     float32 = 6
	clipboardStripRadius     float32 = 3
	clipboardStripGap        float32 = 1.5
	clipboardSparkWidth      float32 = 52
	clipboardSparkHeight     float32 = 16
)

// clipboardStatsState is a snapshot copied by value into the render path, so it must not
// carry a mutex: copylocks would flag it and a copied RWMutex is a real hazard. The live
// guard is AppController.clipboardStatsMu; this struct only ever holds a point-in-time copy
// (its fields are read under that outer lock and then handed off).
type clipboardStatsState struct {
	loaded, loading bool
	days            []storage.ClipboardDay
	kinds           []storage.ClipboardKindStat
	err             error
	filter          string
}

func (a *AppController) ensureClipboardStatsRefresh() {
	a.clipboardStatsMu.Lock()
	if a.clipboardStats.loaded || a.clipboardStats.loading {
		a.clipboardStatsMu.Unlock()
		return
	}
	a.clipboardStats.loading = true
	a.clipboardStatsMu.Unlock()
	reader, ok := a.ClipboardStore.(clipboardStatsReader)
	if !ok {
		a.finishClipboardStats(nil, nil, fmt.Errorf("clipboard statistics unavailable"))
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		days, dayErr := reader.ClipboardDaily(ctx, 7)
		kinds, kindErr := reader.ClipboardKindStats(ctx)
		if dayErr == nil {
			dayErr = kindErr
		}
		a.finishClipboardStats(days, kinds, dayErr)
	}()
}

func (a *AppController) finishClipboardStats(days []storage.ClipboardDay, kinds []storage.ClipboardKindStat, err error) {
	a.clipboardStatsMu.Lock()
	a.clipboardStats.days = append([]storage.ClipboardDay(nil), days...)
	a.clipboardStats.kinds = append([]storage.ClipboardKindStat(nil), kinds...)
	a.clipboardStats.err = err
	a.clipboardStats.loaded = true
	a.clipboardStats.loading = false
	a.clipboardStatsMu.Unlock()
	a.mu.Lock()
	repaint := a.OnRepaint
	a.mu.Unlock()
	if repaint != nil {
		repaint()
	}
}

func (a *AppController) invalidateClipboardStats() {
	a.clipboardStatsMu.Lock()
	a.clipboardStats.loaded = false
	a.clipboardStatsMu.Unlock()
}

func (a *AppController) retryClipboardStats() {
	a.clipboardStatsMu.Lock()
	a.clipboardStats.loaded = false
	a.clipboardStats.err = nil
	a.clipboardStatsMu.Unlock()
	a.ensureClipboardStatsRefresh()
}

func (a *AppController) clipboardStatsSnapshot() (clipboardStatsState, bool) {
	a.clipboardStatsMu.RLock()
	defer a.clipboardStatsMu.RUnlock()
	s := clipboardStatsState{loaded: a.clipboardStats.loaded, loading: a.clipboardStats.loading, err: a.clipboardStats.err, filter: a.clipboardStats.filter}
	s.days = append([]storage.ClipboardDay(nil), a.clipboardStats.days...)
	s.kinds = append([]storage.ClipboardKindStat(nil), a.clipboardStats.kinds...)
	return s, a.clipboardStats.loading
}

func (a *AppController) clipboardStatsFilter() string {
	a.clipboardStatsMu.RLock()
	defer a.clipboardStatsMu.RUnlock()
	return a.clipboardStats.filter
}

func clipboardItemMatchesKind(item engine.OmniboxItem, kind string) bool {
	if item.ClipboardMedia == nil || !item.ClipboardMedia.IsMedia() {
		return strings.EqualFold(kind, string(clipboard.KindText))
	}
	return strings.EqualFold(item.ClipboardMedia.Kind, kind)
}

type clipboardStatSegment struct {
	Kind  string
	Count int
	Bytes int64
	X, W  float32
}

func clipboardKindOrder(kind string) int {
	switch strings.ToLower(kind) {
	case string(clipboard.KindImage):
		return 0
	case string(clipboard.KindText):
		return 1
	case "file":
		return 2
	case "link":
		return 3
	default:
		return 4
	}
}

func clipboardStatSegments(stats []storage.ClipboardKindStat, width float32, selected string) []clipboardStatSegment {
	if width <= 0 {
		return nil
	}
	total := int64(0)
	for _, stat := range stats {
		if stat.Count > 0 {
			total += int64(stat.Count)
		}
	}
	if total == 0 {
		return nil
	}
	ordered := append([]storage.ClipboardKindStat(nil), stats...)
	for i := range ordered {
		for j := i + 1; j < len(ordered); j++ {
			if clipboardKindOrder(ordered[j].Kind) < clipboardKindOrder(ordered[i].Kind) {
				ordered[i], ordered[j] = ordered[j], ordered[i]
			}
		}
	}
	segments := make([]clipboardStatSegment, 0, len(ordered))
	gap := clipboardStripGap
	visible := 0
	for _, stat := range ordered {
		if stat.Count > 0 {
			visible++
		}
	}
	usable := width - float32(max(0, visible-1))*gap
	x := float32(0)
	for _, stat := range ordered {
		if stat.Count <= 0 {
			continue
		}
		w := usable * float32(stat.Count) / float32(total)
		if w < 4 {
			w = 4
		}
		segments = append(segments, clipboardStatSegment{Kind: stat.Kind, Count: stat.Count, Bytes: stat.Bytes, X: x, W: w})
		x += w + gap
	}
	if len(segments) > 0 {
		segments[len(segments)-1].W = max(4, width-segments[len(segments)-1].X)
	}
	_ = selected
	return segments
}

func clipboardStatColor(theme *ui.Theme, index int, selected bool) ui.Color {
	if selected {
		return ui.RGBA(10, 132, 255, 1)
	}
	alpha := []float32{0.65, 0.38, 0.22, 0.12}[min(index, 3)]
	if theme.Dark {
		return ui.RGBA(245, 245, 247, darkClipboardStatAlpha(index))
	}
	return ui.RGBA(29, 29, 31, alpha)
}

func darkClipboardStatAlpha(index int) float32 {
	switch min(index, 3) {
	case 0:
		return 0.70
	case 1:
		return 0.42
	case 2:
		return 0.24
	default:
		return 0.12
	}
}

func renderClipboardStatsBand(c *ui.Context, theme *ui.Theme, state clipboardStatsState, onFilter func(string)) {
	renderClipboardStatsBandWithRetry(c, theme, state, onFilter, nil)
}

func renderClipboardStatsBandWithRetry(c *ui.Context, theme *ui.Theme, state clipboardStatsState, onFilter func(string), onRetry func()) {
	band := ui.Row(c).Key("clipboard-stats-band").Label("clipboard-stats-band").FillWidth().Height(clipboardStatsBandHeight).Gap(space4).AlignItems(ui.Center)
	band.Children(func() {
		if state.err != nil {
			kitInlineError(c, theme, "clipboard-stats-error", clipboardStatsErrorText(), onRetry)
			return
		}
		filterChips := []struct {
			kind  string
			label string
		}{
			{"", i18n.T("clipboard.filter.all")},
			{"pinned", i18n.T("clipboard.filter.pinned")},
			{"text", i18n.T("clipboard.filter.text")},
			{"image", i18n.T("clipboard.filter.image")},
			{"file", i18n.T("clipboard.filter.file")},
			{"link", i18n.T("clipboard.filter.link")},
		}
		ui.Row(c).Key("clipboard-filter-bar").Gap(space2).AlignItems(ui.Center).Shrink(0).Children(func() {
			for _, chip := range filterChips {
				fc := chip
				selected := strings.EqualFold(state.filter, fc.kind)
				btn := ui.ButtonBase(c).Key("clipboard-filter:"+fc.kind).Label(fc.label).
					Padding(space1, space4).Radius(radiusPill).FocusRing(false)
				fg := secondaryInk(theme)
				if selected {
					btn.Background(theme.Accent)
					fg = theme.AccentText
				} else {
					btn.Background(tileFill(theme)).Border(space1, hairline(theme))
				}
				btn.Children(func() {
					ui.Text(c, fc.label).FontSize(typeMicro).FontWeight(600).TextColor(fg).SingleLine()
				})
				if btn.Clicked() && onFilter != nil {
					onFilter(fc.kind)
				}
			}
		})

		ui.Spacer(c)

		if !state.loaded || state.loading {
			kitInlineLoading(c, theme, "clipboard-stats-loading", clipboardStatsLoadingText())
			return
		}

		stripWidth := float32(32)
		count, bytes := clipboardStatsTotals(state.kinds)
		ui.Text(c, clipboardStatsCountLabel(count, bytes)).Key("clipboard-stats-count").Label("clipboard-stats-count").
			FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine().Shrink(0)
		bar := ui.Box(c).Key("clipboard-kind-strip").Label("clipboard-kind-strip").Width(stripWidth).Height(clipboardStripHeight).Radius(clipboardStripRadius).Shrink(0)
		bar.Draw(func(p *ui.Painter, r ui.Rect) {
			w := r.W
			if w <= 0 {
				w = stripWidth
			}
			drawSegs := clipboardStatSegments(state.kinds, w, state.filter)
			for i, segment := range drawSegs {
				selected := strings.EqualFold(state.filter, segment.Kind)
				color := clipboardStatColor(theme, i, selected)
				if selected {
					p.FillRadii(ui.Rect{X: r.X + segment.X, Y: r.Y, W: segment.W, H: r.H}, [4]float32{clipboardStripRadius, clipboardStripRadius, clipboardStripRadius, clipboardStripRadius}, color)
				} else {
					p.Fill(ui.Rect{X: r.X + segment.X, Y: r.Y, W: segment.W, H: r.H}, color, clipboardStripRadius)
				}
			}
		})
		bar.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind != ui.InputPointerDown || onFilter == nil {
				return false
			}
			x := ev.X
			for _, segment := range clipboardStatSegments(state.kinds, stripWidth, state.filter) {
				if x >= segment.X && x <= segment.X+segment.W {
					onFilter(segment.Kind)
					return true
				}
			}
			return false
		})
	})
}

func clipboardStatsErrorText() string {
	if i18n.IsChinese() {
		return "剪贴板统计暂不可用"
	}
	return "Clipboard stats unavailable"
}

func clipboardStatsLoadingText() string {
	if i18n.IsChinese() {
		return "正在加载剪贴板统计…"
	}
	return "Loading clipboard stats…"
}

func clipboardStatsCountLabel(count int, bytes int64) string {
	if i18n.IsChinese() {
		return fmt.Sprintf("%d 条 · %s", count, formatClipboardBytes(bytes))
	}
	if count == 1 {
		return fmt.Sprintf("1 item · %s", formatClipboardBytes(bytes))
	}
	return fmt.Sprintf("%d items · %s", count, formatClipboardBytes(bytes))
}

func clipboardKindLabel(kind string) string {
	zh := i18n.IsChinese()
	switch strings.ToLower(kind) {
	case string(clipboard.KindImage):
		if zh {
			return "图片"
		}
		return "Images"
	case string(clipboard.KindText):
		if zh {
			return "文本"
		}
		return "Text"
	case "file":
		if zh {
			return "文件"
		}
		return "Files"
	case "link":
		if zh {
			return "链接"
		}
		return "Links"
	case "video":
		// Videos have a label in the media vocabulary already; reuse it rather
		// than spell a second one.
		return MediaCategoryLabel(MediaCategoryVideo)
	default:
		return kind
	}
}
func clipboardStatsTotals(stats []storage.ClipboardKindStat) (int, int64) {
	var count int
	var bytes int64
	for _, stat := range stats {
		count += stat.Count
		bytes += stat.Bytes
	}
	return count, bytes
}
func formatClipboardBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	if value < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(value)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(value)/(1024*1024))
}

func renderClipboardSparkline(c *ui.Context, theme *ui.Theme, days []storage.ClipboardDay) {
	ui.Box(c).Key("clipboard-sparkline").Label("clipboard-sparkline").Width(clipboardSparkWidth).Height(clipboardSparkHeight).Draw(func(p *ui.Painter, r ui.Rect) {
		if len(days) < 2 {
			return
		}
		maxValue := 1
		for _, day := range days {
			if day.Count > maxValue {
				maxValue = day.Count
			}
		}
		line := ui.RGBA(110, 110, 115, 0.85)
		if theme.Dark {
			line = ui.RGBA(161, 161, 166, 0.85)
		}
		pts := make([][2]float32, 0, len(days))
		usableW := max(r.W-4, 1)
		for i, day := range days {
			x := r.X + 2 + float32(i)*usableW/float32(len(days)-1)
			y := r.Y + r.H - 2 - float32(day.Count)/float32(maxValue)*(r.H-4)
			pts = append(pts, [2]float32{x, y})
		}
		p.StrokePolyline(pts, 1.5, line)
		last := pts[len(pts)-1]
		p.Fill(ui.Rect{X: last[0] - 2, Y: last[1] - 2, W: 4, H: 4}, ui.RGBA(10, 132, 255, 1), 2)
	})
}

// The Stats hub's clipboard card: a 100% composition bar by kind with a legend
// row per kind, and a seven-day column chart of captures. The icons are the
// ones the clipboard list rows use (engine.clipboardMediaIcon for media,
// clipboardContentIcon for text), so a kind reads the same in both places.

// clipboardHubDays is the capture window the card plots.
const clipboardHubDays = 7

// clipboardKindIcon is the list-row icon of a stored kind.
func clipboardKindIcon(kind string) string {
	switch strings.ToLower(kind) {
	case string(clipboard.KindImage):
		return "photo"
	case string(clipboard.KindVideo):
		return "film"
	case "file":
		return "doc"
	case "link":
		return "link"
	}
	return "doc.text"
}

// clipboardKindsOrdered keeps the kinds that have entries, in the order the
// clipboard filter lists them (clipboardKindOrder), ties by name, so the bar,
// the legend and the filter agree on what comes first.
func clipboardKindsOrdered(stats []storage.ClipboardKindStat) []storage.ClipboardKindStat {
	out := make([]storage.ClipboardKindStat, 0, len(stats))
	for _, stat := range stats {
		if stat.Count > 0 {
			out = append(out, stat)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		oi, oj := clipboardKindOrder(out[i].Kind), clipboardKindOrder(out[j].Kind)
		if oi != oj {
			return oi < oj
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// clipboardCaptureColumns are the captures per day as single-series columns,
// oldest first, labelled with their short date.
func clipboardCaptureColumns(days []storage.ClipboardDay) []chartColumn {
	if len(days) > clipboardHubDays {
		days = days[len(days)-clipboardHubDays:]
	}
	cols := make([]chartColumn, len(days))
	for i, day := range days {
		cols[i] = chartColumn{Label: chartShortDay(day.Day), Values: []float64{float64(day.Count)}}
	}
	return cols
}

// clipboardCapturesMax is the tallest day of the window, the top of the y scale.
func clipboardCapturesMax(days []storage.ClipboardDay) int {
	top := 0
	for _, day := range days {
		top = max(top, day.Count)
	}
	return top
}

// renderClipboardHubChart is the body of the clipboard card once its snapshot
// is loaded and holds entries.
func renderClipboardHubChart(c *ui.Context, theme *ui.Theme, st clipboardStatsState) {
	pal := newChartPalette(theme)
	kinds := clipboardKindsOrdered(st.kinds)
	count, bytes := clipboardStatsTotals(st.kinds)
	values := make([]float64, len(kinds))
	for i, kind := range kinds {
		values[i] = float64(kind.Count)
	}
	percents := chartPercents(values)
	fills := make([]ui.Color, len(kinds))
	parts := make([]chartPart, len(kinds))
	cells := make([][]chartKeyCell, len(kinds))
	for i, kind := range kinds {
		fills[i] = clipboardStatColor(theme, i, strings.EqualFold(st.filter, kind.Kind))
		name := clipboardKindLabel(kind.Kind)
		size := formatClipboardBytes(kind.Bytes)
		parts[i] = chartPart{
			Key:   "stats-clip-seg-" + strings.ToLower(kind.Kind),
			Value: values[i],
			Fill:  fills[i],
			Tip:   name + " · " + fmtInt(kind.Count) + " · " + size + " · " + fmtInt(percents[i]) + "%",
		}
		cells[i] = []chartKeyCell{{Text: fmtInt(kind.Count), Strong: true}, {Text: size}}
	}
	widths := chartKeyWidths(c, cells)
	scale, ticks := chartCountScale(clipboardCapturesMax(st.days))
	captures := clipboardCaptureColumns(st.days)
	last := len(captures) - 1
	fill := func(_, column int) ui.Color {
		if column == last {
			return pal.accent
		}
		return pal.strong
	}

	ui.Column(c).Key("stats-clip-body").FillWidth().Grow(1).MinHeight(space0).Gap(space6).Children(func() {
		ui.Text(c, clipboardStatsCountLabel(count, bytes)).Key("stats-clip-total").
			FontSize(typeBody).FontWeight(600).FontFeatures("tnum").TextColor(theme.Text).SingleLine()
		kitChartSegmentBar(c, chartBarSpec{Key: "stats-clip-bar", Parts: parts, Height: chartThinBarSize})
		ui.Row(c).FillWidth().Grow(1).MinHeight(space0).Gap(space12).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Key("stats-clip-legend").Label("stats-clip-legend").Basis(0).Grow(11).MinWidth(space0).Gap(space4).Children(func() {
				for i, kind := range kinds {
					kitChartKeyRow(c, pal, "stats-clip-key-"+strings.ToLower(kind.Kind), fills[i],
						clipboardKindIcon(kind.Kind), clipboardKindLabel(kind.Kind), cells[i], widths)
				}
			})
			ui.Column(c).Basis(0).Grow(9).MinWidth(space0).MinHeight(space0).Gap(space2).Children(func() {
				if len(captures) == 0 {
					return
				}
				ui.Text(c, "7d").FontSize(typeMicro).TextColor(pal.label).NoWrap()
				kitChartColumns(c, theme, chartColumnsSpec{
					Key:        "stats-clip-chart",
					Label:      i18n.T("stats.card.clipboard.title"),
					Columns:    captures,
					Scale:      scale,
					Ticks:      ticks,
					LabelEvery: 3,
					Fill:       fill,
					Describe: func(i int) string {
						return captures[i].Label + ": " + fmtInt(int(captures[i].Values[0]))
					},
					Tooltip: func(i int) {
						swatch := fill(0, i)
						kitChartTipHeading(c, pal, captures[i].Label)
						kitChartTipFigure(c, pal, &swatch, fmtInt(int(captures[i].Values[0])), "")
					},
					ValueLabel: func(i int) string { return fmtInt(int(captures[i].Values[0])) },
				})
			})
		})
	})
}
