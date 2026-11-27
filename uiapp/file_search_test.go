package uiapp

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/terminal"
)

func TestFileSearchDebouncedAsyncAndStaleDrop(t *testing.T) {
	gate := make(chan struct{})
	var mu sync.Mutex
	var calls []string

	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.fileSearchMu.Lock()
	ctrl.FileSearcher = func(ctx context.Context, query string) ([]engine.FileMatch, error) {
		mu.Lock()
		calls = append(calls, query)
		mu.Unlock()
		if strings.Contains(query, "old") {
			select {
			case <-gate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return []engine.FileMatch{{Path: "/tmp/old.txt", Name: "old.txt", Dir: "/tmp"}}, nil
		}
		return []engine.FileMatch{{Path: "/tmp/new.txt", Name: "new.txt", Dir: "/tmp"}}, nil
	}
	ctrl.fileSearchDebounce = 10 * time.Millisecond
	ctrl.fileSearchMu.Unlock()

	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	ctrl.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: "f old"})
	// Let the first debounced search start, then immediately supersede with "f new".
	waitUntil(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) >= 1
	}, "first file search starts")

	ctrl.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: "f new"})
	waitUntil(t, time.Second, func() bool {
		st := ctrl.State()
		return len(st.OmniboxItems) == 1 && st.OmniboxItems[0].File != nil && st.OmniboxItems[0].File.Path == "/tmp/new.txt"
	}, "newer file search result wins")

	close(gate)
	time.Sleep(40 * time.Millisecond)
	st := ctrl.State()
	if len(st.OmniboxItems) != 1 || st.OmniboxItems[0].File == nil || st.OmniboxItems[0].File.Path != "/tmp/new.txt" {
		t.Fatalf("stale search overwrote items: %+v", st.OmniboxItems)
	}
}

func TestFileSearchCopyPathShortcutAndOpenTerminalEffect(t *testing.T) {
	var copied string
	runner := &fakeTerminalRunner{}
	restoreTerm := setAgentTerminalResolverForTest(func(cfg config.TerminalConfig) *terminal.Resolver {
		return terminal.NewResolverWith(cfg, fakeTerminalDetector{"terminal": true}, runner)
	})
	defer restoreTerm()

	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.ClipboardWriter = func(text string) { copied = text }
	ctrl.fileSearchMu.Lock()
	ctrl.FileSearcher = func(ctx context.Context, query string) ([]engine.FileMatch, error) {
		return []engine.FileMatch{{Path: "/tmp/project/README.md", Name: "README.md", Dir: "/tmp/project"}}, nil
	}
	ctrl.fileSearchMu.Unlock()

	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	ctrl.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: "f README"})
	ctrl.Dispatch(engine.Event{
		Type:        engine.EventSetFileSearchResults,
		Query:       "f README",
		FileMatches: []engine.FileMatch{{Path: "/tmp/project/README.md", Name: "README.md", Dir: "/tmp/project"}},
	})

	if !ctrl.handleOmniboxKey(ui.KeyP, primaryMod()|ui.Shift, ctrl.State()) {
		t.Fatal("Cmd+Shift+P did not handle ItemFile copy_path")
	}
	if copied != "/tmp/project/README.md" {
		t.Fatalf("copied path = %q, want /tmp/project/README.md", copied)
	}

	// Open parent directory in Terminal.
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	ctrl.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: "f README"})
	ctrl.Dispatch(engine.Event{
		Type:        engine.EventSetFileSearchResults,
		Query:       "f README",
		FileMatches: []engine.FileMatch{{Path: "/tmp/project/README.md", Name: "README.md", Dir: "/tmp/project"}},
	})
	ctrl.Dispatch(engine.Event{Type: engine.EventExecuteItemAction, ActionID: "open_in_terminal"})
	if runner.calls == 0 {
		t.Fatal("open_in_terminal did not invoke resolved terminal")
	}
}
