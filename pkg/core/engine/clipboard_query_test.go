package engine_test

import (
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestParseAndFilterClipboardQuery(t *testing.T) {
	now := time.Date(2026, 11, 27, 15, 0, 0, 0, time.Local)
	entries := []engine.OmniboxClipboardEntry{
		{
			ID:             "c1",
			Content:        "https://clavis.dev/docs/clipboard",
			SourceBundleID: "com.apple.Safari",
			SourceAppName:  "Safari",
			IsPinned:       true,
			CopiedAt:       now.Add(-30 * time.Minute),
		},
		{
			ID:             "c2",
			Content:        "func main() {\n\tfmt.Println(\"hello world\")\n}",
			SourceBundleID: "com.microsoft.VSCode",
			SourceAppName:  "VS Code",
			IsPinned:       false,
			CopiedAt:       now.Add(-2 * time.Hour),
		},
		{
			ID:             "c3",
			Content:        "screenshot.png",
			SourceBundleID: "com.apple.screencaptureui",
			SourceAppName:  "Screenshot",
			CopiedAt:       now.Add(-48 * time.Hour),
			Media:          engine.ClipboardMedia{Kind: "image", Width: 1920, Height: 1080},
		},
		{
			ID:             "c4",
			Content:        "#0A84FF",
			SourceBundleID: "com.figma.Desktop",
			SourceAppName:  "Figma",
			CopiedAt:       now.Add(-10 * time.Minute),
		},
	}

	cases := []struct {
		query   string
		wantIDs []string
	}{
		{`type:link`, []string{"c1"}},
		{`type:image`, []string{"c3"}},
		{`type:color`, []string{"c4"}},
		{`type:code`, []string{"c2"}},
		{`is:pinned`, []string{"c1"}},
		{`pin:false app:figma`, []string{"c4"}},
		{`today`, []string{"c1", "c2", "c4"}},
		{`since:1h`, []string{"c1", "c4"}},
		{`"hello world"`, []string{"c2"}},
		{`/clavis\.dev/`, []string{"c1"}},
		{`since:7d app:safari "clipboard"`, []string{"c1"}},
	}
	for _, tc := range cases {
		q := engine.ParseClipboardQuery(tc.query, now)
		if q.RegexErr != "" {
			t.Fatalf("query %q unexpected regex err: %s", tc.query, q.RegexErr)
		}
		got := engine.FilterClipboardEntries(entries, q, now)
		if len(got) != len(tc.wantIDs) {
			t.Fatalf("query %q returned %d items, want %d (%v)", tc.query, len(got), len(tc.wantIDs), tc.wantIDs)
		}
		for i, w := range tc.wantIDs {
			if got[i].ID != w {
				t.Fatalf("query %q item[%d]=%q, want %q", tc.query, i, got[i].ID, w)
			}
		}
	}

	// Malformed regex must not panic and sets RegexErr cleanly.
	bad := engine.ParseClipboardQuery(`/[unclosed/`, now)
	if bad.RegexErr == "" {
		t.Fatal("expected RegexErr for malformed /[unclosed/")
	}
	if res := engine.FilterClipboardEntries(entries, bad, now); len(res) != 0 {
		t.Fatalf("expected 0 results for malformed regex, got %d", len(res))
	}
}

func TestSortClipboardOmniboxItemsPinPlacement(t *testing.T) {
	base := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	items := []engine.OmniboxItem{
		{ID: "p1", KeyHint: engine.KeyHintPinned, ClipboardCopiedAt: base.Add(-10 * time.Minute)},
		{ID: "u1", ClipboardCopiedAt: base.Add(-1 * time.Minute)},
		{ID: "u2", ClipboardCopiedAt: base.Add(-20 * time.Minute)},
	}

	check := func(placement string, want ...string) {
		t.Helper()
		sorted := engine.SortClipboardOmniboxItems(items, placement)
		for i, w := range want {
			if sorted[i].ID != w {
				t.Fatalf("placement=%q [%d]=%s, want %s", placement, i, sorted[i].ID, w)
			}
		}
	}
	check("top", "p1", "u1", "u2")
	check("bottom", "u1", "u2", "p1")
	check("inline", "u1", "p1", "u2")
}

func TestClipboardActionMatrixAndEnterOverride(t *testing.T) {
	urlEntry := engine.OmniboxClipboardEntry{
		ID:             "u1",
		Content:        "https://github.com/yeisme/clavis",
		SourceBundleID: "com.apple.Safari",
		SourceAppName:  "Safari",
		CopiedAt:       time.Now(),
	}

	// Default EnterAction="paste"
	sm := engine.NewStateMachine(config.DefaultConfig())
	_ = sm.Handle(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: []engine.OmniboxClipboardEntry{urlEntry}})
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})

	if eff := sm.Handle(engine.Event{Type: engine.EventConfirmSelection}); len(eff) != 1 || eff[0].Type != engine.EffectPasteClipboardItem || eff[0].KeepOpen {
		t.Fatalf("primary paste eff = %+v", eff)
	}
	if eff := sm.Handle(engine.Event{Type: engine.EventConfirmSecondarySelection}); len(eff) != 1 || eff[0].Type != engine.EffectPasteClipboardItem || !eff[0].KeepOpen {
		t.Fatalf("secondary paste_keep_open eff = %+v", eff)
	}
	if eff := sm.Handle(engine.Event{Type: engine.EventExecuteItemAction, ActionID: "paste_plain"}); len(eff) != 1 || eff[0].Type != engine.EffectPasteClipboardItem || !eff[0].PlainText {
		t.Fatalf("paste_plain eff = %+v", eff)
	}
	if eff := sm.Handle(engine.Event{Type: engine.EventExecuteItemAction, ActionID: "block_app"}); len(eff) != 1 || eff[0].Type != engine.EffectBlockClipboardApp || eff[0].BundleID != "com.apple.Safari" {
		t.Fatalf("block_app eff = %+v", eff)
	}
	if eff := sm.Handle(engine.Event{Type: engine.EventExecuteItemAction, ActionID: "save_snippet"}); len(eff) != 2 || eff[0].Type != engine.EffectSaveClipboardAsSnippet || eff[0].Text != urlEntry.Content {
		t.Fatalf("save_snippet eff = %+v", eff)
	}
	// Re-open because save_snippet closes the capsule state.
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	if eff := sm.Handle(engine.Event{Type: engine.EventExecuteItemAction, ActionID: "open_url"}); len(eff) != 2 || eff[0].Type != engine.EffectOpenExternal || eff[0].Text != urlEntry.Content {
		t.Fatalf("open_url eff = %+v", eff)
	}

	// Override EnterAction="copy"
	copyCfg := config.DefaultConfig()
	copyCfg.Main.Clipboard.EnterAction = "copy"
	smCopy := engine.NewStateMachine(copyCfg)
	_ = smCopy.Handle(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: []engine.OmniboxClipboardEntry{urlEntry}})
	_ = smCopy.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	if eff := smCopy.Handle(engine.Event{Type: engine.EventConfirmSelection}); len(eff) != 2 || eff[0].Type != engine.EffectCopyText || eff[0].Text != urlEntry.Content {
		t.Fatalf("enter_action=copy primary eff = %+v", eff)
	}
}
