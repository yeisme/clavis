package uiapp

import (
	"context"
	"image/color"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func testLexiconHitDays(days ...LexiconHitDay) []LexiconHitDay {
	if len(days) == 0 {
		for i := 0; i < 14; i++ {
			days = append(days, LexiconHitDay{Day: fmtInt(i)})
		}
	}
	return days
}

func TestLexiconMicrobarGeometryAndColors(t *testing.T) {
	if got := lexiconMicrobarWidth(1, 100); got != 2 {
		t.Fatalf("minimum width=%v, want 2", got)
	}
	if got := lexiconMicrobarWidth(5, 10); got != 22 {
		t.Fatalf("half width=%v, want 22", got)
	}
	if got := lexiconMicrobarWidth(10, 10); got != 44 {
		t.Fatalf("maximum width=%v, want 44", got)
	}
	words, maxUse := lexiconTopWordMap([]LexiconTopWord{{Word: "a", UseCount: 10}, {Word: "b", UseCount: 5}, {Word: "zero", UseCount: 0}})
	if maxUse != 10 || words["zero"] != 0 {
		t.Fatalf("top word map=%v max=%d", words, maxUse)
	}
	if got := lexiconMicrobarColor(10, 10); got != ui.Hex("#0A84FF") {
		t.Fatalf("maximum color=%v, want #0A84FF", got)
	}
	if lexiconMicrobarColor(5, 10) == lexiconMicrobarColor(1, 10) {
		t.Fatal("gray microbar levels should vary by relative frequency")
	}
	if _, max := lexiconTopWordMap([]LexiconTopWord{{Word: "zero", UseCount: 0}}); max != 0 {
		t.Fatalf("all-zero max=%d, want 0", max)
	}
}

func TestLexiconStatsBandAndSparklineGeometry(t *testing.T) {
	days := testLexiconHitDays()
	for i := range days {
		days[i].Hits = i + 1
		days[i].Misses = 14 - i
	}
	tt := ui.NewTester(func(c *ui.Context) {
		renderLexiconStatsBand(c, ui.LightTheme(), days, true, nil, 42)
	}, 240, 68)
	tt.Frame()
	band, ok := tt.Find("lexicon-stats-band")
	if !ok || band.H != lexiconStatsBandHeight {
		t.Fatalf("band=%+v ok=%v, want height 52", band, ok)
	}
	chart, ok := tt.Find("lexicon-sparkline")
	if !ok || chart.W != lexiconSparklineWidth || chart.H != lexiconSparklineHeight {
		t.Fatalf("sparkline=%+v ok=%v, want 96x20", chart, ok)
	}
	points := make([]ui.Rect, 14)
	for i := 0; i < 14; i++ {
		point, ok := tt.Find("lexicon-spark-point-" + fmtInt(i))
		if !ok {
			t.Fatalf("valid point %d missing", i)
		}
		points[i] = point
	}
	geometry := lexiconHitPoints(days)
	for i := 1; i < len(geometry); i++ {
		if got := geometry[i].X - geometry[i-1].X; got < 7 || got > 8 {
			t.Fatalf("point geometry spacing %d = %.2f, want about %.2f", i, got, float32(lexiconSparklineWidth-lexiconSparklineEndRoom)/13)
		}
	}
	last, ok := tt.Find("lexicon-spark-point-13")
	if !ok {
		t.Fatal("today point missing")
	}
	blue := color.RGBA{R: 10, G: 132, B: 255, A: 255}
	if !sameRGBA(tt.Image().At(int(last.X+last.W/2), int(last.Y+last.H/2)), blue) {
		t.Fatalf("today endpoint=%#v, want #0A84FF", tt.Image().At(int(last.X+last.W/2), int(last.Y+last.H/2)))
	}
}

func TestLexiconSparklineSkipsZeroActivityDays(t *testing.T) {
	days := testLexiconHitDays()
	for i := range days {
		days[i].Hits = 1
		days[i].Misses = 1
	}
	days[2] = LexiconHitDay{Day: "zero"}
	days[7] = LexiconHitDay{Day: "zero"}
	tt := ui.NewTester(func(c *ui.Context) {
		renderLexiconStatsBand(c, ui.LightTheme(), days, true, nil, 42)
	}, 240, 68)
	tt.Frame()
	if _, ok := tt.Find("lexicon-sparkline"); !ok {
		t.Fatal("sparkline missing with partial hit data")
	}
	for _, index := range []int{2, 7} {
		if _, ok := tt.Find("lexicon-spark-point-" + fmtInt(index)); ok {
			t.Fatalf("zero-activity day %d rendered as a point", index)
		}
	}
	if _, ok := tt.Find("lexicon-spark-point-13"); !ok {
		t.Fatal("today endpoint missing")
	}
}

func TestLexiconStatsBandHidesSparklineWithoutActivity(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		renderLexiconStatsBand(c, ui.LightTheme(), testLexiconHitDays(), true, nil, 42)
	}, 240, 68)
	tt.Frame()
	if _, ok := tt.Find("lexicon-sparkline"); ok {
		t.Fatal("sparkline rendered with no hit or miss data")
	}
	if !tt.HasText("已收录 42 词") {
		t.Fatalf("catalog fallback missing: %v", tt.Texts())
	}
}

func TestLexiconTopWordsRefreshIsAsyncAndCached(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	calls := 0
	ctrl.lexiconTopWordsReader = func(ctx context.Context, n int) ([]LexiconTopWord, error) {
		calls++
		if n != 5 {
			t.Fatalf("top window = %d, want 5", n)
		}
		close(started)
		select {
		case <-release:
			return []LexiconTopWord{{Word: "alpha", UseCount: 4}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ctrl.ensureLexiconTopWordsRefresh()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("top words reader did not start asynchronously")
	}
	ctrl.lexiconTopWordsMu.RLock()
	loaded := ctrl.lexiconTopWordsLoaded
	ctrl.lexiconTopWordsMu.RUnlock()
	if loaded {
		t.Fatal("top cache marked loaded before reader returned")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ctrl.lexiconTopWordsMu.RLock()
		loaded = ctrl.lexiconTopWordsLoaded
		ctrl.lexiconTopWordsMu.RUnlock()
		if loaded {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !loaded {
		t.Fatal("top result was not cached")
	}
	ctrl.ensureLexiconTopWordsRefresh()
	if calls != 1 {
		t.Fatalf("reader calls = %d, want cached one-shot refresh", calls)
	}
}

func TestLexiconHitsRefreshIsAsyncAndCached(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls int
	ctrl.lexiconHitsReader = func(ctx context.Context, n int) ([]LexiconHitDay, error) {
		calls++
		if n != 14 {
			t.Fatalf("hit window = %d, want 14", n)
		}
		close(started)
		select {
		case <-release:
			return testLexiconHitDays(LexiconHitDay{Hits: 1, Misses: 1}), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ctrl.ensureLexiconHitsRefresh()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("lexicon reader did not start asynchronously")
	}
	ctrl.lexiconHitsMu.RLock()
	loaded := ctrl.lexiconHitsLoaded
	ctrl.lexiconHitsMu.RUnlock()
	if loaded {
		t.Fatal("hit cache marked loaded before reader returned")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ctrl.lexiconHitsMu.RLock()
		loaded = ctrl.lexiconHitsLoaded
		ctrl.lexiconHitsMu.RUnlock()
		if loaded {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !loaded {
		t.Fatal("hit result was not cached")
	}
	ctrl.ensureLexiconHitsRefresh()
	if calls != 1 {
		t.Fatalf("reader calls = %d, want cached one-shot refresh", calls)
	}
}
