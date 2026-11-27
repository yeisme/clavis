package uiapp

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func TestRowMetaCacheHitsInvalidationAndBounds(t *testing.T) {
	defer i18n.SetLanguage(i18n.Current())
	i18n.SetLanguage(i18n.English)

	cache := newRowMetaCache(8)
	now := time.Date(2026, 11, 24, 15, 10, 0, 0, time.Local)
	item := engine.OmniboxItem{
		ID:                 "clipboard:1",
		Title:              "SELECT 1",
		Subtitle:           "Clipboard",
		Kind:               engine.ItemClipboardEntry,
		ClipboardID:        "1",
		ClipboardContent:   "SELECT 1\nFROM users\nLIMIT 5;\n",
		ClipboardSourceApp: "TablePlus",
		ClipboardCopiedAt:  now.Add(-2 * time.Minute),
	}

	m1 := cache.getOrCompute(item, now, i18n.English)
	if m1.rowKey != "omnibox-row:clipboard:1" || m1.iconName != "curlybraces" || m1.lineCount != 3 {
		t.Fatalf("unexpected row meta: %+v", m1)
	}
	if want := clipboardRowMeta(item, now); m1.subtitle != want {
		t.Fatalf("subtitle = %q, want %q", m1.subtitle, want)
	}

	// Same item within same minute bucket must hit cache.
	m2 := cache.getOrCompute(item, now.Add(20*time.Second), i18n.English)
	if m2 != m1 {
		t.Fatalf("cached meta mismatch: %+v vs %+v", m2, m1)
	}
	hits, misses := cache.stats()
	if hits != 1 || misses != 1 {
		t.Fatalf("expected 1 hit / 1 miss, got hits=%d misses=%d", hits, misses)
	}

	// Advancing past the minute boundary invalidates the relative time.
	m3 := cache.getOrCompute(item, now.Add(2*time.Minute), i18n.English)
	if m3.subtitle == m1.subtitle {
		t.Fatalf("expected subtitle refresh across minute boundary, still %q", m3.subtitle)
	}

	// Pinning/keyHint change invalidates cache entry.
	pinned := item
	pinned.KeyHint = engine.KeyHintPinned
	_ = cache.getOrCompute(pinned, now, i18n.English)
	_, missesAfterPin := cache.stats()
	if missesAfterPin != 3 {
		t.Fatalf("expected pin change to miss cache, misses=%d", missesAfterPin)
	}

	// Language switch invalidates cache entry and produces localized subtitle.
	i18n.SetLanguage(i18n.Chinese)
	mZh := cache.getOrCompute(item, now, i18n.Chinese)
	if !strings.Contains(mZh.subtitle, "分钟前") || !strings.Contains(mZh.subtitle, "3 行") {
		t.Fatalf("expected Chinese subtitle after language switch, got %q", mZh.subtitle)
	}

	// Bounded size must never exceed maxSize.
	for i := 0; i < 32; i++ {
		it := item
		it.ID = fmt.Sprintf("clipboard:bulk-%d", i)
		_ = cache.getOrCompute(it, now, i18n.Chinese)
		if got := cache.len(); got > 8 {
			t.Fatalf("rowMetaCache exceeded bound: len=%d > 8", got)
		}
	}
}

func TestPreviewTextCacheHitsInvalidationAndBounds(t *testing.T) {
	defer i18n.SetLanguage(i18n.Current())
	i18n.SetLanguage(i18n.English)

	cache := newPreviewTextCache(4)
	lines := make([]string, 120)
	for i := range lines {
		lines[i] = fmt.Sprintf("log line %03d: served ok", i+1)
	}
	text := strings.Join(lines, "\n") + "\n"

	e1 := cache.getOrCompute("clipboard:log", text, false, false, previewInitialLineLimit, i18n.English)
	if len(e1.lines) != previewInitialLineLimit || e1.hidden != 60 || e1.logicalTotal != 120 {
		t.Fatalf("unexpected collapsed preview cache entry: len=%d hidden=%d logical=%d", len(e1.lines), e1.hidden, e1.logicalTotal)
	}
	e2 := cache.getOrCompute("clipboard:log", text, false, false, previewInitialLineLimit, i18n.English)
	if len(e2.lines) != len(e1.lines) || e2.statsLabel != e1.statsLabel {
		t.Fatalf("expected identical cached preview entry")
	}
	hits, misses := cache.stats()
	if hits != 1 || misses != 1 {
		t.Fatalf("expected 1 hit / 1 miss, got hits=%d misses=%d", hits, misses)
	}

	// Expanding uses a distinct cache key and returns all 120 lines (<=200 cap).
	expanded := cache.getOrCompute("clipboard:log", text, false, true, previewExpandedLineLimit, i18n.English)
	if len(expanded.lines) != 120 || expanded.hidden != 0 {
		t.Fatalf("unexpected expanded preview cache entry: len=%d hidden=%d", len(expanded.lines), expanded.hidden)
	}

	// Content change invalidates cache.
	modified := cache.getOrCompute("clipboard:log", text+"extra line\n", false, false, previewInitialLineLimit, i18n.English)
	if modified.hidden != 61 {
		t.Fatalf("expected updated hidden=61 after content edit, got %d", modified.hidden)
	}

	// Bounded size must never exceed maxSize.
	for i := 0; i < 16; i++ {
		_ = cache.getOrCompute(fmt.Sprintf("clipboard:item-%d", i), fmt.Sprintf("body %d", i), false, false, previewInitialLineLimit, i18n.English)
		if got := cache.len(); got > 4 {
			t.Fatalf("previewTextCache exceeded bound: len=%d > 4", got)
		}
	}
}

func TestClipboardPreview60LineCapAndExpandToggle(t *testing.T) {
	defer i18n.SetLanguage(i18n.Current())

	makeLines := func(prefix string, count int) string {
		var b strings.Builder
		for i := 1; i <= count; i++ {
			fmt.Fprintf(&b, "%s-row-%03d\n", prefix, i)
		}
		return b.String()
	}

	for _, tc := range []struct {
		name          string
		lang          i18n.Language
		wantExpand120 string
		wantExpand300 string
		wantCollapse  string
		wantMoreAfter string
	}{
		{
			name:          "en",
			lang:          i18n.English,
			wantExpand120: "Expand all 120 lines",
			wantExpand300: "Expand all 300 lines (first 200)",
			wantCollapse:  "Show first 60 lines",
			wantMoreAfter: "100 more lines…",
		},
		{
			name:          "zh",
			lang:          i18n.Chinese,
			wantExpand120: "展开其余 60 行",
			wantExpand300: "展开其余 240 行（前 200 行）",
			wantCollapse:  "收起至前 60 行",
			wantMoreAfter: "还有 100 行…",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i18n.SetLanguage(tc.lang)
			f := newInteractionFixture(t, nil)
			entries := []engine.OmniboxClipboardEntry{
				{ID: "short120", Content: makeLines("alpha", 120), SourceAppName: "Notes"},
				{ID: "huge300", Content: makeLines("beta", 300), SourceAppName: "Terminal"},
			}
			f.ctrl.Dispatch(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: entries})
			f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
			tt := f.omniboxTester(t)

			// First entry (120 lines): initially capped at 60 lines with visible localized expand toggle.
			if !tt.HasText("alpha-row-060") {
				t.Fatalf("expected line 60 rendered initially; texts=%v", tt.Texts())
			}
			if tt.HasText("alpha-row-061") {
				t.Fatalf("line 61 must NOT render before expanding")
			}
			if !tt.HasText(tc.wantExpand120) {
				t.Fatalf("missing expand toggle %q; texts=%v", tc.wantExpand120, tt.Texts())
			}

			// Click expand toggle directly in view -> shows all 120 lines and switches to collapse.
			if err := tt.Click(tc.wantExpand120); err != nil {
				t.Fatalf("failed to click expand toggle %q: %v", tc.wantExpand120, err)
			}
			tt.Frame()
			if !tt.HasText("alpha-row-120") {
				t.Fatalf("expected line 120 rendered after expanding")
			}
			if !tt.HasText(tc.wantCollapse) {
				t.Fatalf("expected collapse button %q after expanding", tc.wantCollapse)
			}

			// Clicking collapse restores the 60-line first-screen cap.
			if err := tt.Click(tc.wantCollapse); err != nil {
				t.Fatalf("failed to click collapse toggle %q: %v", tc.wantCollapse, err)
			}
			tt.Frame()
			if tt.HasText("alpha-row-061") {
				t.Fatalf("line 61 must not render after collapsing back to 60 lines")
			}

			// Move to second entry (300 lines): must start collapsed at 60 lines!
			tt.Key(0, ui.KeyDown)
			tt.Frame()
			if !tt.HasText("beta-row-060") {
				t.Fatalf("expected second entry line 60 rendered")
			}
			if tt.HasText("beta-row-061") {
				t.Fatalf("second entry must start collapsed at 60 lines")
			}
			if !tt.HasText(tc.wantExpand300) {
				t.Fatalf("missing 300-line expand toggle %q; texts=%v", tc.wantExpand300, tt.Texts())
			}

			// Expand 300-line entry -> caps safely at 200 lines and displays remaining count.
			if err := tt.Click(tc.wantExpand300); err != nil {
				t.Fatalf("failed to click 300-line expand toggle %q: %v", tc.wantExpand300, err)
			}
			tt.Frame()
			if !tt.HasText("beta-row-200") {
				t.Fatalf("expected line 200 rendered after expanding 300-line entry")
			}
			if tt.HasText("beta-row-201") {
				t.Fatalf("expanded preview must remain capped at 200 lines")
			}
			if !tt.HasText(tc.wantMoreAfter) {
				t.Fatalf("missing remaining lines indicator %q; texts=%v", tc.wantMoreAfter, tt.Texts())
			}
		})
	}
}

func TestClipboardCodePreview60LineCapAndExpand(t *testing.T) {
	defer i18n.SetLanguage(i18n.Current())
	i18n.SetLanguage(i18n.English)

	var b strings.Builder
	b.WriteString("package demo\n")
	for i := 2; i <= 100; i++ {
		fmt.Fprintf(&b, "func fn%03d() int { return %d }\n", i, i)
	}

	f := newInteractionFixture(t, nil)
	f.ctrl.Dispatch(engine.Event{
		Type: engine.EventSetClipboardEntries,
		ClipboardEntries: []engine.OmniboxClipboardEntry{
			{ID: "go100", Content: b.String(), SourceAppName: "VSCode"},
		},
	})
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	tt := f.omniboxTester(t)

	if !tt.HasText("Expand all 100 lines") {
		t.Fatalf("expected 60-line cap toggle for 100-line code snippet; texts=%v", tt.Texts())
	}
	if tt.HasText("func fn061()") {
		t.Fatalf("code line 61 must not render before expanding")
	}
	if err := tt.Click("Expand all 100 lines"); err != nil {
		t.Fatalf("click expand on code preview failed: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Show first 60 lines") {
		t.Fatalf("expected collapse button after expanding code preview; texts=%v", tt.Texts())
	}
	if !tt.HasText("func fn100()") {
		t.Fatalf("expected code line 100 after expanding; texts=%v", tt.Texts())
	}
}

func TestWorkflowPreviewStepsBadgeRendered(t *testing.T) {
	f := newInteractionFixture(t, nil)
	cfg := f.ctrl.Config()
	cfg.Workflows.Workflows = []config.Workflow{{
		ID: "deploy-check", Name: "Deploy Check", Description: "Verify rollout", Enabled: true,
		Steps: []config.WorkflowStep{
			{ID: "s1", Name: "Lint", Action: "lint", Enabled: true},
			{ID: "s2", Name: "Test", Action: "test", Enabled: true},
		},
	}}
	f.ctrl.SetConfig(cfg)
	f.ctrl.OpenOmnibox(config.TabActions, "deploy-check")
	tt := f.omniboxTester(t)

	if !tt.HasText("Safe workflow") {
		t.Fatalf("missing workflow safety badge: %v", tt.Texts())
	}
	if !tt.HasText("2 steps") {
		t.Fatalf("workflow step count badge was not rendered as label (swapped args regression?): %v", tt.Texts())
	}
}
