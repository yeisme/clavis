package engine_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestExternalClipboardSearch_KeystrokePreservesVisibleAndAppliesResults(t *testing.T) {
	cfg := config.DefaultConfig()
	sm := engine.NewStateMachine(cfg)
	sm.SetExternalClipboardSearch(true)

	mirror := []engine.OmniboxClipboardEntry{
		{ID: "recent-1", Content: "first recent item", SourceAppName: "Notes"},
		{ID: "recent-2", Content: "second recent item", SourceAppName: "Terminal"},
	}
	_ = sm.Handle(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: mirror})
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})

	st := sm.State()
	if len(st.OmniboxItems) != 2 {
		t.Fatalf("expected 2 initial items, got %d", len(st.OmniboxItems))
	}

	// Typing keeps visible items responsive instead of flashing empty or full-scanning.
	_ = sm.Handle(engine.Event{Type: engine.EventUpdateQuery, Query: "old"})
	st = sm.State()
	if st.OmniboxQuery != "old" || len(st.OmniboxItems) != 2 {
		t.Fatalf("expected responsive visible list while debouncing, got query=%q items=%d", st.OmniboxQuery, len(st.OmniboxItems))
	}

	// Stale result for an old query is dropped inside Reduce.
	_ = sm.Handle(engine.Event{
		Type:  engine.EventSetClipboardSearchResults,
		Query: "o",
		ClipboardEntries: []engine.OmniboxClipboardEntry{
			{ID: "stale", Content: "stale entry"},
		},
	})
	if got := sm.State().OmniboxItems[0].ClipboardID; got != "recent-1" {
		t.Fatalf("expected stale result dropped, got %q", got)
	}

	// Matching search results (e.g. item #180 outside the 50-item mirror) apply cleanly.
	_ = sm.Handle(engine.Event{
		Type:  engine.EventSetClipboardSearchResults,
		Query: "old",
		ClipboardEntries: []engine.OmniboxClipboardEntry{
			{ID: "hist-180", Content: "old archived token", SourceAppName: "iTerm2"},
		},
	})
	st = sm.State()
	if len(st.OmniboxItems) != 1 || st.OmniboxItems[0].ClipboardID != "hist-180" {
		t.Fatalf("expected searched item hist-180, got %+v", st.OmniboxItems)
	}

	// Pinning an item outside the bounded mirror still updates search entries & emits effect.
	pinEff := sm.Handle(engine.Event{Type: engine.EventTogglePinSelectedClipboardItem})
	if len(pinEff) != 1 || pinEff[0].Type != engine.EffectPinClipboardItem || pinEff[0].ClipboardID != "hist-180" || !pinEff[0].IsPinned {
		t.Fatalf("expected pin effect for hist-180, got %+v", pinEff)
	}
	if !sm.State().ClipboardSearchEntries[0].IsPinned {
		t.Fatal("expected search entry marked pinned optimistically")
	}

	// Clearing query via Backspace immediately restores the bounded mirror.
	_ = sm.Handle(engine.Event{Type: engine.EventBackspace}) // "ol"
	_ = sm.Handle(engine.Event{Type: engine.EventBackspace}) // "o"
	_ = sm.Handle(engine.Event{Type: engine.EventBackspace}) // ""
	st = sm.State()
	if st.OmniboxQuery != "" || len(st.OmniboxItems) != 2 || st.OmniboxItems[0].ClipboardID != "recent-1" {
		t.Fatalf("expected recent mirror restored on empty query, got query=%q items=%+v", st.OmniboxQuery, st.OmniboxItems)
	}
}

func TestFirstNonEmptyLine_MultiLineAndLargePayload(t *testing.T) {
	cfg := config.DefaultConfig()
	sm := engine.NewStateMachine(cfg)
	large := "\n   \n\tfirst line here\r\n" + strings.Repeat("body line content\n", 500)
	_ = sm.Handle(engine.Event{
		Type: engine.EventSetClipboardEntries,
		ClipboardEntries: []engine.OmniboxClipboardEntry{
			{ID: "big", Content: large, SourceAppName: "Notes"},
			{ID: "single", Content: "  hello world  ", SourceAppName: "Notes"},
			{ID: "blank", Content: "   \n\t  ", SourceAppName: "Notes"},
		},
	})
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	items := sm.State().OmniboxItems
	if items[0].Title != "first line here" {
		t.Fatalf("large multiline title = %q, want %q", items[0].Title, "first line here")
	}
	if items[1].Title != "hello world" {
		t.Fatalf("single line title = %q, want %q", items[1].Title, "hello world")
	}
	if items[2].Title != "   \n\t  " {
		t.Fatalf("blank title = %q", items[2].Title)
	}
}

func makeBenchmarkClipboardEntries(n, payloadBytes int) []engine.OmniboxClipboardEntry {
	pad := strings.Repeat("lorem ipsum dolor sit amet clipboard payload ", (payloadBytes/45)+1)
	if len(pad) > payloadBytes {
		pad = pad[:payloadBytes]
	}
	entries := make([]engine.OmniboxClipboardEntry, n)
	for i := range n {
		content := fmt.Sprintf("entry-%03d title line\n%s", i, pad)
		if i == n-1 {
			content = fmt.Sprintf("entry-%03d needle target\n%s", i, pad)
		}
		entries[i] = engine.OmniboxClipboardEntry{
			ID:            fmt.Sprintf("clip-%d", i),
			Content:       content,
			SourceAppName: "Notes",
		}
	}
	return entries
}

func BenchmarkClipboardKeystrokeFilter(b *testing.B) {
	cfg := config.DefaultConfig()
	allEntries := makeBenchmarkClipboardEntries(200, 4096)
	boundedEntries := allEntries[:50]

	b.Run("OldFullScan_200x4KB", func(b *testing.B) {
		sm := engine.NewStateMachine(cfg)
		_ = sm.Handle(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: allEntries})
		_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
		queries := []string{"n", "ne", "nee", "need", "needl", "needle"}
		b.ReportAllocs()
		b.ResetTimer()
		for i := range b.N {
			_ = sm.Handle(engine.Event{Type: engine.EventUpdateQuery, Query: queries[i%len(queries)]})
		}
	})

	b.Run("NewExternalSearchKeystroke_50Mirror", func(b *testing.B) {
		sm := engine.NewStateMachine(cfg)
		sm.SetExternalClipboardSearch(true)
		_ = sm.Handle(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: boundedEntries})
		_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
		queries := []string{"n", "ne", "nee", "need", "needl", "needle"}
		b.ReportAllocs()
		b.ResetTimer()
		for i := range b.N {
			_ = sm.Handle(engine.Event{Type: engine.EventUpdateQuery, Query: queries[i%len(queries)]})
		}
	})
}
