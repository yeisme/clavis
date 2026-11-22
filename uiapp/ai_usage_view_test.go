package uiapp

import (
	"context"
	"image/color"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func testAIUsageDays(values ...int) []AIUsageDay {
	days := make([]AIUsageDay, len(values))
	for i, value := range values {
		days[i] = AIUsageDay{Day: "2026-11-" + fmtInt(i+1), InputTokens: value}
	}
	return days
}

func TestAIUsageChartGeometryAndTodayColor(t *testing.T) {
	days := testAIUsageDays(100, 200, 300, 400, 500, 600, 700)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(ui.LightTheme())
		renderAIUsageChart(c, ui.LightTheme(), days)
	}, 120, 40)
	chart, ok := tt.Find("ai-usage-chart")
	if !ok {
		t.Fatal("chart not found")
	}
	if chart.W != aiUsageChartWidth || chart.H != aiUsageChartHeight {
		t.Fatalf("chart rect = %+v, want 96x28", chart)
	}
	var bars []ui.Rect
	for i := 0; i < 7; i++ {
		bar, ok := tt.Find(fmtKey("ai-usage-bar-", i))
		if !ok {
			t.Fatalf("bar %d not found", i)
		}
		bars = append(bars, bar)
		if bar.W != aiUsageBarWidth {
			t.Fatalf("bar %d width = %.1f, want 8", i, bar.W)
		}
	}
	for i := 1; i < len(bars); i++ {
		if got := bars[i].X - bars[i-1].X; got != aiUsageBarWidth+aiUsageBarGap {
			t.Fatalf("bar gap %d = %.1f, want 14", i, got)
		}
	}
	if got := bars[6].X + bars[6].W - bars[0].X; got != 92 {
		t.Fatalf("bar group width = %.1f, want 92", got)
	}
	if bars[6].H != aiUsageBarMaxHeight || bars[0].H < 2 {
		t.Fatalf("bar heights = first %.1f, today %.1f", bars[0].H, bars[6].H)
	}
	image := tt.Image()
	blue := image.At(int(bars[6].X+bars[6].W/2), int(bars[6].Y+bars[6].H/2))
	if !sameRGBA(blue, color.RGBA{R: 10, G: 132, B: 255, A: 255}) {
		t.Fatalf("today color = %#v, want #0A84FF", blue)
	}
	gray := image.At(int(bars[0].X+bars[0].W/2), int(bars[0].Y+bars[0].H/2))
	if sameRGBA(gray, color.RGBA{R: 10, G: 132, B: 255, A: 255}) {
		t.Fatalf("history color unexpectedly equals today: %#v", gray)
	}
}

func TestAIUsageChartZeroUsesTwoPointPlaceholders(t *testing.T) {
	bars := aiUsageBarGeometryFor(testAIUsageDays(0, 0, 0, 0, 0, 0, 0))
	if len(bars) != 7 {
		t.Fatalf("bars = %d, want 7", len(bars))
	}
	for i, bar := range bars {
		if bar.W != 8 || bar.H != 2 {
			t.Fatalf("bar %d = %+v, want width 8 height 2", i, bar)
		}
	}
}

func TestAIUsageBandEmptyState(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.OpenOmnibox(config.TabAI, "")
	ctrl.aiUsageMu.Lock()
	ctrl.aiUsageLoaded = true
	ctrl.aiUsageDays = testAIUsageDays(0, 0, 0, 0, 0, 0, 0)
	ctrl.aiUsageMu.Unlock()
	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	if !tt.HasText("近 7 日无 AI 调用记录") {
		t.Fatalf("empty AI usage state missing: %v", tt.Texts())
	}
}

func TestAIUsageBandUses56PointHeight(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.aiUsageMu.Lock()
	ctrl.aiUsageLoaded = true
	ctrl.aiUsageDays = testAIUsageDays(1, 2, 3, 4, 5, 6, 7)
	ctrl.aiUsageMu.Unlock()

	tt := ui.NewTester(func(c *ui.Context) {
		ctrl.renderAIUsageBand(c, ui.LightTheme())
	}, 240, aiUsageBandHeight+16)
	tt.Frame()
	band, ok := tt.Find("ai-usage-band")
	if !ok {
		t.Fatal("AI usage band not found")
	}
	if band.H != aiUsageBandHeight {
		t.Fatalf("AI usage band height = %.1f, want 56", band.H)
	}
}

func TestAIUsageRefreshIsAsyncAndCached(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls int
	ctrl.aiUsageReader = func(ctx context.Context, n int) ([]AIUsageDay, error) {
		calls++
		if n != 7 {
			t.Fatalf("usage window = %d, want 7", n)
		}
		close(started)
		select {
		case <-release:
			return testAIUsageDays(1, 2, 3, 4, 5, 6, 7), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ctrl.ensureAIUsageRefresh()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("usage reader did not start asynchronously")
	}
	ctrl.aiUsageMu.RLock()
	loaded := ctrl.aiUsageLoaded
	ctrl.aiUsageMu.RUnlock()
	if loaded {
		t.Fatal("usage cache marked loaded before reader returned")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ctrl.aiUsageMu.RLock()
		loaded = ctrl.aiUsageLoaded
		ctrl.aiUsageMu.RUnlock()
		if loaded {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !loaded {
		t.Fatal("usage result was not cached")
	}
	ctrl.ensureAIUsageRefresh()
	if calls != 1 {
		t.Fatalf("reader calls = %d, want cached one-shot refresh", calls)
	}
}

func sameRGBA(got color.Color, want color.RGBA) bool {
	r, g, b, a := got.RGBA()
	return uint8(r>>8) == want.R && uint8(g>>8) == want.G && uint8(b>>8) == want.B && uint8(a>>8) == want.A
}
