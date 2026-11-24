package engine_test

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestQuickJumpHashAndFullwidthRouting(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.QuickJump.Bookmarks = []config.QuickJumpBookmark{
		{ID: "clavis", Name: "Clavis Workspace", Kind: config.QuickJumpDir, Target: "/tmp/clavis", Tags: []string{"go"}},
		{ID: "gh", Name: "GitHub Search", Kind: config.QuickJumpURL, Target: "https://github.com/search?q={query}", Tags: []string{"github", "code"}},
	}

	sm := engine.NewStateMachine(cfg)
	_ = sm.Handle(engine.Event{
		Type: engine.EventSetQuickJumpTargets,
		QuickJumpTargets: []engine.QuickJumpTarget{
			{ID: "ws:w4", Title: "Workspace w4 · clavis", Subtitle: "herdr workspace", Kind: "workspace", Target: "w4"},
		},
	})
	_ = sm.Handle(engine.Event{
		Type:           engine.EventSetListeningPorts,
		ListeningPorts: []engine.PortEntry{{Port: 8080, PID: 99, Process: "vite", Command: "npm run dev"}},
	})

	// Full-width Chinese IME hash `＃` normalizes identically to `#`.
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "＃w4"})
	items := sm.State().OmniboxItems
	if len(items) == 0 || items[0].Kind != engine.ItemQuickJump || items[0].QuickJump.Target != "w4" {
		t.Fatalf("expected Workspace w4 quick jump via ＃w4, got %+v", items)
	}
	effs := sm.Handle(engine.Event{Type: engine.EventConfirmSelection})
	if len(effs) == 0 || effs[0].Type != engine.EffectFocusHerdrTarget || effs[0].Text != "w4" {
		t.Fatalf("expected EffectFocusHerdrTarget w4, got %+v", effs)
	}

	// URL template parameter expansion: `#gh herdr cli` -> URL-escaped `{query}`
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "#gh herdr cli"})
	urlItems := sm.State().OmniboxItems
	if len(urlItems) == 0 || urlItems[0].QuickJump == nil || urlItems[0].QuickJump.Target != "https://github.com/search?q=herdr+cli" {
		t.Fatalf("expected expanded GitHub URL target, got %+v", urlItems)
	}
	urlEffs := sm.Handle(engine.Event{Type: engine.EventConfirmSelection})
	if len(urlEffs) == 0 || urlEffs[0].Type != engine.EffectOpenExternal || urlEffs[0].Text != "https://github.com/search?q=herdr+cli" {
		t.Fatalf("expected EffectOpenExternal, got %+v", urlEffs)
	}

	// Listening port under `#`
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "#8080"})
	portItems := sm.State().OmniboxItems
	if len(portItems) == 0 || portItems[0].QuickJump == nil || portItems[0].QuickJump.Target != "http://127.0.0.1:8080" {
		t.Fatalf("expected port jump item, got %+v", portItems)
	}
}
