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

func TestDictionaryWatcherMissingDirCreatedLaterAndRemoved(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created-yet", "dicts")
	var changes atomic.Int32
	w := NewDictionaryWatcher(dir, func() { changes.Add(1) },
		WithDictionaryPollInterval(10*time.Millisecond),
		WithDictionaryDebounce(0),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start on missing dir must succeed, got: %v", err)
	}
	defer w.Stop()

	// Creating an empty directory should not spuriously trigger a reload.
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := changes.Load(); got != 0 {
		t.Fatalf("empty dir creation triggered spurious reload: count=%d", got)
	}

	// Dropping a .clvdict into the newly created directory triggers hot-reload.
	dictPath := filepath.Join(dir, "personal.clvdict")
	if err := os.WriteFile(dictPath, []byte("initial-dict"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, &changes, 1)

	// Removing the whole directory triggers hot-reload back to embedded fallback.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, &changes, 2)
}

func TestDictionaryWatcherStatBeforeHashSkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stable.clvdict")
	if err := os.WriteFile(path, []byte("unchanged-content"), 0600); err != nil {
		t.Fatal(err)
	}
	var changes atomic.Int32
	var hashes atomic.Int32
	w := NewDictionaryWatcher(dir, func() { changes.Add(1) },
		WithDictionaryPollInterval(10*time.Millisecond),
		WithDictionaryDebounce(0),
	)
	w.hashFile = func(p string) ([32]byte, error) {
		hashes.Add(1)
		return sha256File(p)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	// Baseline hashes the file once; idle polls must skip hashing while (size, modTime) match.
	if got := hashes.Load(); got != 1 {
		t.Fatalf("baseline hash count = %d, want 1", got)
	}
	time.Sleep(100 * time.Millisecond)
	if got := hashes.Load(); got != 1 {
		t.Fatalf("unchanged file re-hashed across idle polls: count=%d, want 1", got)
	}
	if got := changes.Load(); got != 0 {
		t.Fatalf("unexpected callback on unchanged file: count=%d", got)
	}

	// Touching mtime with identical bytes hashes once, does NOT fire callback,
	// and skips hashing on subsequent polls.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, &hashes, 2)
	time.Sleep(80 * time.Millisecond)
	if got := hashes.Load(); got != 2 {
		t.Fatalf("mtime-only touch kept re-hashing: count=%d, want 2", got)
	}
	if got := changes.Load(); got != 0 {
		t.Fatalf("mtime-only touch with identical content fired reload: count=%d", got)
	}

	// Modifying content changes size/mtime -> hashes once more and triggers callback.
	if err := os.WriteFile(path, []byte("updated-content-payload"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, &changes, 1)
	if got := hashes.Load(); got != 3 {
		t.Fatalf("hash count after content update = %d, want 3", got)
	}
	time.Sleep(60 * time.Millisecond)
	if got := hashes.Load(); got != 3 {
		t.Fatalf("file re-hashed after settling: count=%d, want 3", got)
	}
}

func TestDictionaryWatcherExtraCustomDictPathsAndExplicitConfigDir(t *testing.T) {
	explicitCfg := filepath.Join(t.TempDir(), "custom-config")
	if got, want := UserDictionaryDirIn(explicitCfg), filepath.Join(explicitCfg, "dicts"); got != want {
		t.Fatalf("UserDictionaryDirIn(%q) = %q, want %q", explicitCfg, got, want)
	}

	customDir := t.TempDir()
	customYAML := filepath.Join(customDir, "my.dict.yaml")
	secondCustom := filepath.Join(customDir, "second.dict.yaml")
	var changes atomic.Int32
	// Pass empty primary dir to confirm only configured extra paths are watched.
	w := NewDictionaryWatcher("", func() { changes.Add(1) },
		WithDictionaryPollInterval(10*time.Millisecond),
		WithDictionaryDebounce(0),
		WithDictionaryExtraPaths(customYAML),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start with initially absent extra path failed: %v", err)
	}
	defer w.Stop()

	// Unrelated sibling file must not trigger a reload.
	if err := os.WriteFile(filepath.Join(customDir, "unrelated.yaml"), []byte("nope"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if got := changes.Load(); got != 0 {
		t.Fatalf("unrelated file triggered reload: count=%d", got)
	}

	// Create, modify, and delete the watched custom .dict.yaml file.
	if err := os.WriteFile(customYAML, []byte("---\nname: one\n...\n词甲\tci jia\t10\n"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, &changes, 1)

	if err := os.WriteFile(customYAML, []byte("---\nname: one\n...\n词乙\tci yi\t20\n"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, &changes, 2)

	// Dynamically update extra paths via SetExtraPaths and write to the newly added file.
	w.SetExtraPaths([]string{customYAML, secondCustom})
	if err := os.WriteFile(secondCustom, []byte("---\nname: two\n...\n词丙\tci bing\t30\n"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, &changes, 3)

	if err := os.Remove(customYAML); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, &changes, 4)
}
