package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
)

func TestConfigWatcher_PollReloadAndRollback(t *testing.T) {
	dir := t.TempDir()
	store := config.NewConfigStore(dir)
	initialCfg, err := store.LoadOrInitialize()
	if err != nil {
		t.Fatalf("LoadOrInitialize failed: %v", err)
	}
	if initialCfg.Main.Appearance.CapsuleWidth != 680 {
		t.Fatalf("expected default CapsuleWidth 680, got %v", initialCfg.Main.Appearance.CapsuleWidth)
	}

	reloads := make(chan ReloadResult, 8)
	w := NewConfigWatcher(
		store,
		WithDebounce(30*time.Millisecond),
		WithPollInterval(20*time.Millisecond),
		WithOnReload(func(res ReloadResult) {
			reloads <- res
		}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.StartWatching(ctx); err != nil {
		t.Fatalf("StartWatching failed: %v", err)
	}
	defer w.Stop()

	// Edit config.toml with valid new values
	updated := initialCfg.Main
	updated.Appearance.CapsuleWidth = 740
	updated.AI.Model = "gpt-4.1-mini"
	configPath := filepath.Join(dir, string(config.FileMain))
	writeFileAtomic(t, configPath, []byte(updated.Encode()))

	// Non-TOML file write should be ignored
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0o600); err != nil {
		t.Fatalf("WriteFile notes.txt failed: %v", err)
	}

	select {
	case res := <-reloads:
		if !res.OK {
			t.Fatalf("expected successful reload, got errors: %+v", res.Errors)
		}
		if res.Config.Main.Appearance.CapsuleWidth != 740 || res.Config.Main.AI.Model != "gpt-4.1-mini" {
			t.Fatalf("unexpected reloaded config: %+v", res.Config.Main)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for valid config reload")
	}

	// Write invalid TOML -> reload fails, preserves last-known-good config
	writeFileAtomic(t, configPath, []byte("[trigger\nbad = "))

	select {
	case res := <-reloads:
		if res.OK {
			t.Fatal("expected reload failure on invalid TOML")
		}
		if len(res.Errors) == 0 {
			t.Fatal("expected non-empty LoadErrors on invalid TOML")
		}
		if store.Current().Main.Appearance.CapsuleWidth != 740 {
			t.Fatalf("expected rollback to preserve CapsuleWidth 740, got %v", store.Current().Main.Appearance.CapsuleWidth)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for invalid config reload callback")
	}

	w.Stop()
	select {
	case <-w.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not close Done channel on Stop")
	}
}

func TestConfigWatcher_WatchDirWithPollCancel(t *testing.T) {
	dir := t.TempDir()
	store := config.NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}

	w := NewConfigWatcher(store, WithPollInterval(15*time.Millisecond), WithDebounce(0))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	err := w.WatchDirWithPoll(ctx)
	if err == nil {
		t.Fatal("expected context error on cancellation")
	}
}

func TestConfigWatcher_StopRestartAndConcurrentStop(t *testing.T) {
	dir := t.TempDir()
	store := config.NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}

	w := NewConfigWatcher(store, WithPollInterval(10*time.Millisecond), WithDebounce(10*time.Millisecond))
	for i := 0; i < 3; i++ {
		if err := w.StartWatching(context.Background()); err != nil {
			t.Fatalf("StartWatching iteration %d failed: %v", i, err)
		}
		var wg sync.WaitGroup
		for j := 0; j < 4; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w.Stop()
			}()
		}
		wg.Wait()
	}
}

func TestConfigWatcher_SkipsSelfSavesAndReloadsExternalEdits(t *testing.T) {
	dir := t.TempDir()
	store := config.NewConfigStore(dir)
	initCfg, err := store.LoadOrInitialize()
	if err != nil {
		t.Fatalf("LoadOrInitialize failed: %v", err)
	}

	reloads := make(chan ReloadResult, 8)
	w := NewConfigWatcher(
		store,
		WithPollInterval(15*time.Millisecond),
		WithDebounce(20*time.Millisecond),
		WithOnReload(func(res ReloadResult) {
			reloads <- res
		}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.StartWatching(ctx); err != nil {
		t.Fatalf("StartWatching failed: %v", err)
	}
	defer w.Stop()

	// Atomically save all 5 TOML files via ConfigStore -> must NOT trigger redundant watcher reloads
	mainCfg := initCfg.Main
	mainCfg.Appearance.CapsuleWidth = 710
	mainCfg.Appearance.ShowPreviewPane = false
	if err := store.SaveMain(mainCfg); err != nil {
		t.Fatalf("SaveMain: %v", err)
	}
	if err := store.SaveLeader(initCfg.Leader); err != nil {
		t.Fatalf("SaveLeader: %v", err)
	}
	if err := store.SaveSnippets(initCfg.Snippets); err != nil {
		t.Fatalf("SaveSnippets: %v", err)
	}
	if err := store.SaveGlossary(initCfg.Glossary); err != nil {
		t.Fatalf("SaveGlossary: %v", err)
	}
	if err := store.SaveIMERules(initCfg.IMERules); err != nil {
		t.Fatalf("SaveIMERules: %v", err)
	}

	select {
	case unexpected := <-reloads:
		t.Fatalf("expected self-saves via ConfigStore to be suppressed by content hash, got reload: %+v", unexpected)
	case <-time.After(140 * time.Millisecond):
	}

	// External write with genuinely different content still triggers reload cleanly
	extMain := mainCfg
	extMain.Appearance.CapsuleWidth = 760
	writeFileAtomic(t, filepath.Join(dir, string(config.FileMain)), []byte(extMain.Encode()))

	select {
	case res := <-reloads:
		if !res.OK || res.Config.Main.Appearance.CapsuleWidth != 760 {
			t.Fatalf("expected external edit reload width=760, got %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for external edit reload")
	}
}
