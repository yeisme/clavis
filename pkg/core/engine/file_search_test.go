package engine

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

func TestParseFileSearchQueryNoCollision(t *testing.T) {
	explicitCases := []struct {
		raw        string
		wantNeedle string
	}{
		{"f invoice", "invoice"},
		{"file design spec", "design spec"},
		{"~", "~"},
		{"~/Documents/rep", "~/Documents/rep"},
		{"/Users/alice/notes", "/Users/alice/notes"},
	}
	for _, tc := range explicitCases {
		needle, explicit, ok := ParseFileSearchQuery(tc.raw)
		if !ok || !explicit || needle != tc.wantNeedle {
			t.Fatalf("ParseFileSearchQuery(%q) = (%q, %v, %v), want (%q, true, true)",
				tc.raw, needle, explicit, ok, tc.wantNeedle)
		}
	}

	nonFileRoutes := []string{
		"",
		"a",
		"/code-review",
		"//draft prompt",
		"/",
		">window",
		";sig",
		"@type:image",
		"?explain",
		":rocket",
		"=1+1",
		"#clavis",
	}
	for _, raw := range nonFileRoutes {
		if needle, explicit, ok := ParseFileSearchQuery(raw); ok {
			t.Fatalf("ParseFileSearchQuery(%q) matched unexpectedly: (%q, %v, %v)", raw, needle, explicit, ok)
		}
	}

	needle, explicit, ok := ParseFileSearchQuery("readme")
	if !ok || explicit || needle != "readme" {
		t.Fatalf("ParseFileSearchQuery(\"readme\") = (%q, %v, %v), want (\"readme\", false, true)", needle, explicit, ok)
	}
}

func TestFileSearchExplicitImplicitAndActions(t *testing.T) {
	cfg := config.DefaultConfig()
	sm := NewStateMachine(cfg)

	sm.Handle(Event{Type: EventOpenOmnibox, Tab: config.TabActions})
	sm.Handle(Event{Type: EventUpdateQuery, Query: "f report"})
	if items := sm.State().OmniboxItems; len(items) != 0 {
		t.Fatalf("expected 0 items while explicit file search pending, got %+v", items)
	}

	matches := []FileMatch{
		{Path: "/Users/alice/docs/report.pdf", Name: "report.pdf", Dir: "/Users/alice/docs", SizeBytes: 4096},
		{Path: "/Users/alice/docs", Name: "docs", Dir: "/Users/alice", IsDir: true},
	}
	sm.Handle(Event{Type: EventSetFileSearchResults, Query: "f report", FileMatches: matches})
	items := sm.State().OmniboxItems
	if len(items) != 2 || items[0].Kind != ItemFile || items[0].File.Path != "/Users/alice/docs/report.pdf" {
		t.Fatalf("unexpected explicit file search items: %+v", items)
	}

	// Verify 4 item actions.
	specs := AvailableItemActionsWithConfig(items[0], cfg)
	if len(specs) != 4 || specs[0].ID != "open" || specs[1].ID != "reveal" || specs[2].ID != "copy_path" || specs[3].ID != "open_in_terminal" {
		t.Fatalf("unexpected ItemFile action specs: %+v", specs)
	}

	// open (primary)
	eff := sm.Handle(Event{Type: EventConfirmSelection})
	if len(eff) != 2 || eff[0].Type != EffectOpenPath || eff[0].Text != "/Users/alice/docs/report.pdf" {
		t.Fatalf("open action effects = %+v", eff)
	}

	// reveal (secondary)
	sm.Handle(Event{Type: EventOpenOmnibox, Tab: config.TabActions})
	sm.Handle(Event{Type: EventUpdateQuery, Query: "f report"})
	sm.Handle(Event{Type: EventSetFileSearchResults, Query: "f report", FileMatches: matches})
	eff = sm.Handle(Event{Type: EventConfirmSecondarySelection})
	if len(eff) != 2 || eff[0].Type != EffectRevealPath || eff[0].Text != "/Users/alice/docs/report.pdf" {
		t.Fatalf("reveal action effects = %+v", eff)
	}

	// copy_path & open_in_terminal (directory vs file parent dir)
	sm.Handle(Event{Type: EventOpenOmnibox, Tab: config.TabActions})
	sm.Handle(Event{Type: EventUpdateQuery, Query: "f report"})
	sm.Handle(Event{Type: EventSetFileSearchResults, Query: "f report", FileMatches: matches})
	eff = sm.Handle(Event{Type: EventExecuteItemAction, ActionID: "copy_path"})
	if len(eff) != 2 || eff[0].Type != EffectCopyText || eff[0].Text != "/Users/alice/docs/report.pdf" {
		t.Fatalf("copy_path effects = %+v", eff)
	}
	sm.Handle(Event{Type: EventOpenOmnibox, Tab: config.TabActions})
	sm.Handle(Event{Type: EventUpdateQuery, Query: "f report"})
	sm.Handle(Event{Type: EventSetFileSearchResults, Query: "f report", FileMatches: matches})
	eff = sm.Handle(Event{Type: EventExecuteItemAction, ActionID: "open_in_terminal"})
	if len(eff) != 2 || eff[0].Type != EffectOpenInTerminal || eff[0].Text != "/Users/alice/docs" {
		t.Fatalf("open_in_terminal on file effects = %+v", eff)
	}
	sm.Handle(Event{Type: EventOpenOmnibox, Tab: config.TabActions})
	sm.Handle(Event{Type: EventUpdateQuery, Query: "f report"})
	sm.Handle(Event{Type: EventSetFileSearchResults, Query: "f report", FileMatches: matches})
	sm.Handle(Event{Type: EventSelectIndex, Index: 1})
	eff = sm.Handle(Event{Type: EventExecuteItemAction, ActionID: "open_in_terminal"})
	if len(eff) != 2 || eff[0].Type != EffectOpenInTerminal || eff[0].Text != "/Users/alice/docs" {
		t.Fatalf("open_in_terminal on dir effects = %+v", eff)
	}

	// Implicit fallback when Actions has < 5 scored items.
	sm.Handle(Event{Type: EventOpenOmnibox, Tab: config.TabActions})
	sm.Handle(Event{Type: EventUpdateQuery, Query: "zzuniquefile"})
	sm.Handle(Event{Type: EventSetFileSearchResults, Query: "zzuniquefile", FileMatches: []FileMatch{
		{Path: "/tmp/zzuniquefile.txt", Name: "zzuniquefile.txt", Dir: "/tmp"},
	}})
	foundFile := false
	for _, it := range sm.State().OmniboxItems {
		if it.Kind == ItemFile && it.File != nil && it.File.Path == "/tmp/zzuniquefile.txt" {
			foundFile = true
			break
		}
	}
	if !foundFile {
		t.Fatalf("implicit fallback file match missing from %+v", sm.State().OmniboxItems)
	}
}
