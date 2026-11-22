package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yeisme/clavis/internal/watcher"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/ime"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

func writeReloadDict(t *testing.T, path, word, pinyin string) {
	t.Helper()
	lex, err := ime.CompileRimeDict(strings.NewReader("---\nname: reload\n...\n" + word + "\t" + pinyin + "\t999999\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, lex.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func reloadEngineText(eng *inputengine.Engine, raw string) []string {
	eng.Reset()
	for _, r := range raw {
		eng.HandleKey(hostToInputKey(r))
	}
	view := eng.View()
	out := make([]string, len(view.Candidates))
	for i, candidate := range view.Candidates {
		out[i] = candidate.Text
	}
	return out
}

func hostToInputKey(r rune) inputengine.Key {
	return inputengine.Key{Kind: inputengine.KeyLetter, Rune: r}
}

func waitReload(t *testing.T, want func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if want() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("dictionary watcher did not complete reload before timeout")
}

func TestDictionaryWatcherReloadReplacesPointerAndFallsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reload.clvdict")
	cacheDir := filepath.Join(dir, "cache")
	cfg := config.DefaultInputEngineConfig()
	cfg.Enabled, cfg.Mode = true, "inline_hook"
	cfg.CustomDictPaths = []string{path}

	st := &inputEngineState{dirty: make(chan struct{}, 1), cacheDir: cacheDir}
	changed, initial := st.configureWithForce(cfg, true)
	if !changed || initial == nil {
		t.Fatal("initial fallback engine was not created")
	}
	st.setEngine(initial)
	oldPointer := st.eng.Load()

	var diagnosticsMu sync.Mutex
	var diagnostics []inputengine.DictionaryDiagnostic
	var reloads atomic.Int32
	reload := func() {
		_, _ = inputengine.LoadDictionaryWithDiagnostics(cfg.CustomDictPaths, cacheDir, func(d inputengine.DictionaryDiagnostic) {
			diagnosticsMu.Lock()
			diagnostics = append(diagnostics, d)
			diagnosticsMu.Unlock()
		})
		_, next := st.configureWithForce(cfg, true)
		if next != nil {
			st.setEngine(next)
			reloads.Add(1)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := watcher.NewDictionaryWatcher(dir, reload,
		watcher.WithDictionaryPollInterval(10*time.Millisecond),
		watcher.WithDictionaryDebounce(0),
	)
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	writeReloadDict(t, path, "热加载甲", "ni hao")
	waitReload(t, func() bool {
		return strings.Contains(strings.Join(reloadEngineText(st.eng.Load(), "nihao"), "\x00"), "热加载甲")
	})
	if got := st.eng.Load(); got == oldPointer {
		t.Fatal("reload kept the old engine pointer")
	}
	oldPointer = st.eng.Load()

	writeReloadDict(t, path, "热加载乙", "ni hao")
	waitReload(t, func() bool {
		texts := strings.Join(reloadEngineText(st.eng.Load(), "nihao"), "\x00")
		return strings.Contains(texts, "热加载乙") && !strings.Contains(texts, "热加载甲")
	})
	if st.eng.Load() == oldPointer {
		t.Fatal("dictionary modification did not replace the engine pointer")
	}

	if err := os.WriteFile(path, []byte("not a clvdict"), 0600); err != nil {
		t.Fatal(err)
	}
	waitReload(t, func() bool {
		diagnosticsMu.Lock()
		defer diagnosticsMu.Unlock()
		return len(diagnostics) > 0 && len(reloadEngineText(st.eng.Load(), "nihao")) > 0
	})
	diagnosticsMu.Lock()
	if len(diagnostics) == 0 {
		diagnosticsMu.Unlock()
		t.Fatal("corrupt dictionary produced no diagnostic")
	}
	diagnosticsMu.Unlock()

	// Exercise the hook-side read path concurrently with reloads. The test has
	// no filesystem access in the key loop; only the watcher callback rebuilds.
	var stop atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				eng := st.eng.Load()
				if eng != nil {
					eng.HandleKey(inputengine.Key{Kind: inputengine.KeyLetter, Rune: 'n'})
					eng.Reset()
				}
			}
		}()
	}
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(path, []byte("still corrupt "+string(rune('a'+i))), 0600); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	stop.Store(true)
	wg.Wait()
	if reloads.Load() == 0 {
		t.Fatal("watcher did not invoke reload callback")
	}
}
