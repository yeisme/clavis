package uiapp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/storage"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/engine"
)

const (
	clipboardStatsBandHeight float32 = 54
	clipboardStripHeight     float32 = 6
	clipboardStripRadius     float32 = 3
	clipboardStripGap        float32 = 1.5
	clipboardSparkWidth      float32 = 72
	clipboardSparkHeight     float32 = 20
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
	band := ui.Column(c).Key("clipboard-stats-band").Label("clipboard-stats-band").FillWidth().Height(clipboardStatsBandHeight).Gap(space4).Padding(space4, space0)
	band.Children(func() {
		if state.err != nil {
			ui.Text(c, "剪贴板统计暂不可用").FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
			return
		}
		if !state.loaded || state.loading {
			ui.Text(c, "正在加载剪贴板统计…").FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
			return
		}
		segments := clipboardStatSegments(state.kinds, 240, state.filter)
		legend := ui.Row(c).Key("clipboard-stats-legend").Label("clipboard-stats-legend").FillWidth().Gap(space6).Height(typeMeta).Children(func() {
			for _, segment := range segments {
				ui.Text(c, clipboardKindLabel(segment.Kind)).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
			}
		})
		_ = legend
		bar := ui.Box(c).Key("clipboard-kind-strip").Label("clipboard-kind-strip").FillWidth().Height(clipboardStripHeight).Radius(clipboardStripRadius)
		bar.Draw(func(p *ui.Painter, r ui.Rect) {
			for i, segment := range segments {
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
			if ev.Kind != ui.InputPointerDown {
				return false
			}
			x := ev.X
			for _, segment := range segments {
				if x >= segment.X && x <= segment.X+segment.W {
					onFilter(segment.Kind)
					return true
				}
			}
			return false
		})
		count, bytes := clipboardStatsTotals(state.kinds)
		ui.Row(c).Key("clipboard-stats-count-row").Label("clipboard-stats-count-row").FillWidth().Height(clipboardSparkHeight).Gap(space8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf("%d 条 · %s", count, formatClipboardBytes(bytes))).Key("clipboard-stats-count").Label("clipboard-stats-count").FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
			if len(state.days) >= 7 && count >= 5 {
				renderClipboardSparkline(c, theme, state.days)
			}
		})
	})
}

func clipboardKindLabel(kind string) string {
	switch strings.ToLower(kind) {
	case string(clipboard.KindImage):
		return "图片"
	case string(clipboard.KindText):
		return "文本"
	case "file":
		return "文件"
	case "link":
		return "链接"
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
		var prevX, prevY float32
		for i, day := range days {
			x := r.X + float32(i)*r.W/float32(len(days)-1)
			y := r.Y + r.H - 2 - float32(day.Count)/float32(maxValue)*(r.H-4)
			if i > 0 {
				p.Line(prevX, prevY, x, y, 1.5, line)
			}
			if i == len(days)-1 {
				p.Fill(ui.Rect{X: x - 2, Y: y - 2, W: 4, H: 4}, ui.RGBA(10, 132, 255, 1), 2)
			}
			prevX, prevY = x, y
		}
	})
}
