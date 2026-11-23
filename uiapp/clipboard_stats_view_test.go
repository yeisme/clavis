package uiapp

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/storage"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestClipboardStatsBandGeometryAndSparklineThreshold(t *testing.T) {
	state := clipboardStatsState{loaded: true, days: []storage.ClipboardDay{{Count: 1}, {Count: 2}, {Count: 3}, {Count: 4}, {Count: 5}, {Count: 6}, {Count: 7}}, kinds: []storage.ClipboardKindStat{{Kind: "image", Count: 2, Bytes: 2048}, {Kind: "text", Count: 5, Bytes: 1024}}}
	tt := ui.NewTester(func(c *ui.Context) { renderClipboardStatsBand(c, ui.LightTheme(), state, func(string) {}) }, 280, 60)
	tt.Frame()
	band, ok := tt.Find("clipboard-stats-band")
	if !ok || band.H != clipboardStatsBandHeight {
		t.Fatalf("band=%+v ok=%v, want height 54", band, ok)
	}
	strip, ok := tt.Find("clipboard-kind-strip")
	if !ok || strip.H != clipboardStripHeight {
		t.Fatalf("strip=%+v ok=%v, want height 6", strip, ok)
	}
	sparkline, sparkOK := tt.Find("clipboard-sparkline")
	legend, legendOK := tt.Find("clipboard-stats-legend")
	countRow, countRowOK := tt.Find("clipboard-stats-count-row")
	countText, countTextOK := tt.Find("clipboard-stats-count")
	if !sparkOK || !legendOK || !countRowOK || !countTextOK {
		t.Fatalf("missing stats geometry: legend=%+v sparkline=%+v countRow=%+v count=%+v", legend, sparkline, countRow, countText)
	}
	if sparkline.Y < band.Y || sparkline.Y+sparkline.H > band.Y+band.H {
		t.Fatalf("sparkline=%+v escapes band=%+v", sparkline, band)
	}
	strip, stripOK := tt.Find("clipboard-kind-strip")
	if !stripOK {
		t.Fatal("strip missing")
	}
	if legend.Y+legend.H > strip.Y || strip.Y+strip.H > countRow.Y {
		t.Fatalf("stats rows overlap: legend=%+v strip=%+v countRow=%+v", legend, strip, countRow)
	}
	centerDelta := float64((sparkline.Y + sparkline.H/2) - (countText.Y + countText.H/2))
	t.Logf("stats geometry: band=%+v legend=%+v strip=%+v countRow=%+v count=%+v sparkline=%+v centerDelta=%.2f", band, legend, strip, countRow, countText, sparkline, centerDelta)
	if math.Abs(centerDelta) > 1 {
		t.Fatalf("sparkline/count are not vertically centered: sparkline=%+v count=%+v", sparkline, countText)
	}
	state.kinds = []storage.ClipboardKindStat{{Kind: "image", Count: 2, Bytes: 2048}, {Kind: "text", Count: 2, Bytes: 1024}}
	state.days = []storage.ClipboardDay{{Count: 1}, {Count: 1}, {Count: 1}, {Count: 1}, {Count: 0}, {Count: 0}, {Count: 0}}
	tt = ui.NewTester(func(c *ui.Context) { renderClipboardStatsBand(c, ui.LightTheme(), state, func(string) {}) }, 280, 60)
	tt.Frame()
	if _, ok := tt.Find("clipboard-sparkline"); ok {
		t.Fatal("sparkline must be hidden below five total entries")
	}
}

func TestClipboardStatsBandAddsOnly18PointsToClipboardCapsule(t *testing.T) {
	appearance := config.DefaultConfig().Main.Appearance
	state := engine.State{Phase: engine.PhaseOmnibox, OmniboxTab: config.TabClipboard}
	_, baseHeight := PreferredCapsuleSize(state.Phase, appearance)
	_, clipboardHeight := PreferredCapsuleSizeForState(state, appearance)
	if got := clipboardHeight - baseHeight; got != int(clipboardStatsBandHeight) {
		t.Fatalf("clipboard height delta=%d, want %d; list area should lose no extra height", got, int(clipboardStatsBandHeight))
	}
}

func TestClipboardStatColorsUseBlueSelectionAndNeutralGray(t *testing.T) {
	light := ui.LightTheme()
	if got := clipboardStatColor(light, 0, true); got != ui.RGBA(10, 132, 255, 1) {
		t.Fatalf("selected color=%v, want #0A84FF", got)
	}
	if got := clipboardStatColor(light, 0, false); got == ui.RGBA(10, 132, 255, 1) {
		t.Fatal("unselected segment must remain neutral gray")
	}
}

func TestClipboardStatsRenderedSelectionUsesSystemBlue(t *testing.T) {
	state := clipboardStatsState{loaded: true, filter: "image", kinds: []storage.ClipboardKindStat{{Kind: "image", Count: 1}, {Kind: "text", Count: 3}}}
	tt := ui.NewTester(func(c *ui.Context) { renderClipboardStatsBand(c, ui.LightTheme(), state, func(string) {}) }, 280, 60)
	tt.Frame()
	strip, ok := tt.Find("clipboard-kind-strip")
	if !ok {
		t.Fatal("strip missing")
	}
	blue := ui.RGBA(10, 132, 255, 1)
	if !colorClose(rgbaAt(tt.Image(), int(strip.X+2), int(strip.Y+strip.H/2)), blue) {
		t.Fatalf("selected strip segment pixel=%v, want #0A84FF", rgbaAt(tt.Image(), int(strip.X+2), int(strip.Y+strip.H/2)))
	}
}

func TestClipboardStatsAsyncRefreshIsCached(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	store, err := storage.NewSQLiteClipboardStore(context.Background(), ":memory:", clipboard.NewPrivacyFilter())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctrl.ClipboardStore = store
	ctrl.ensureClipboardStatsRefresh()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, _ := ctrl.clipboardStatsSnapshot()
		if state.loaded {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("clipboard stats did not load asynchronously")
}

func TestClipboardStatsClickFiltersRows(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	state := clipboardStatsState{loaded: true, kinds: []storage.ClipboardKindStat{{Kind: "image", Count: 1}, {Kind: "text", Count: 2}}}
	called := ""
	tt := ui.NewTester(func(c *ui.Context) {
		renderClipboardStatsBand(c, ui.LightTheme(), state, func(kind string) { called = kind })
	}, 280, 60)
	tt.Frame()
	strip, ok := tt.Find("clipboard-kind-strip")
	if !ok {
		t.Fatal("strip missing")
	}
	tt.ClickAt(strip.X+strip.W/4, strip.Y+strip.H/2)
	if called == "" {
		t.Fatal("click did not select a type segment")
	}
}
