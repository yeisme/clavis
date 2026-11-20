package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func waitForCount(t *testing.T, count *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if count.Load() >= want {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("callback count=%d, want >=%d", count.Load(), want)
}

func TestDictionaryWatcherDetectsAddModifyDelete(t *testing.T) {
	dir := t.TempDir()
	var changes atomic.Int32
	w := NewDictionaryWatcher(dir, func() { changes.Add(1) }, WithDictionaryPollInterval(10*time.Millisecond), WithDictionaryDebounce(0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	path := filepath.Join(dir, "user.clvdict")
	if err := os.WriteFile(path, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, &changes, 1)
	if err := os.WriteFile(path, []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, &changes, 2)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, &changes, 3)
}

func TestDictionaryWatcherIgnoresOtherFiles(t *testing.T) {
	dir := t.TempDir()
	var changes atomic.Int32
	w := NewDictionaryWatcher(dir, func() { changes.Add(1) }, WithDictionaryPollInterval(10*time.Millisecond), WithDictionaryDebounce(0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if got := changes.Load(); got != 0 {
		t.Fatalf("non-dictionary callback count=%d", got)
	}
}
