package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/pkg/core/host"
)

func TestCandidatePanelBelowCaretAndFlip(t *testing.T) {
	displays := []mygo.Display{{Bounds: mygo.Rectangle{Width: 1920, Height: 1080}, WorkArea: mygo.Rectangle{Width: 1920, Height: 1040}}}
	size := bridge.Size{Width: 300, Height: 58}
	b := computeCandidatePanelBounds(&host.CursorAnchor{X: 100, Y: 200, Width: 2, Height: 20, Valid: true}, mygo.Point{}, size, displays)
	if b.Y != 224 || b.X != 100 {
		t.Fatalf("below caret: %+v", b)
	}
	b = computeCandidatePanelBounds(&host.CursorAnchor{X: 1900, Y: 1010, Width: 2, Height: 20, Valid: true}, mygo.Point{}, size, displays)
	if b.Y >= 1010 || b.X+b.Width > 1920 {
		t.Fatalf("flip/clamp: %+v", b)
	}
}

func TestAIDebouncerDropsStale(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	delivered := map[string][]string{}
	d := &aiDebouncer{
		delay: 20 * time.Millisecond,
		suggest: func(_ context.Context, raw, _ string) []string {
			mu.Lock()
			asked = append(asked, raw)
			mu.Unlock()
			return []string{"你好"}
		},
		deliver: func(raw string, w []string) { mu.Lock(); delivered[raw] = w; mu.Unlock() },
	}
	ctx := context.Background()
	d.Changed(ctx, "n", true) // too short: never asked
	d.Changed(ctx, "ni", true)
	d.Changed(ctx, "nih", true)
	d.Changed(ctx, "nihao", true)
	d.Changed(ctx, "nihao", true) // view-only change: no restart
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	if len(asked) != 1 || asked[0] != "nihao" || len(delivered["nihao"]) != 1 {
		t.Fatalf("asked=%v delivered=%v", asked, delivered)
	}
	mu.Unlock()
	d.Changed(ctx, "wo", true)
	d.Changed(ctx, "", false) // composition ended before debounce
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 1 {
		t.Fatalf("cancelled composition still queried: %v", asked)
	}
}
