package watcher

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/yeisme/clavis/pkg/core/config"
)

const (
	defaultDebounce     = 120 * time.Millisecond
	defaultPollInterval = 150 * time.Millisecond
)

// ReloadResult summarizes a configuration reload attempt triggered by ConfigWatcher.
type ReloadResult struct {
	OK       bool
	Config   config.ClavisConfig
	Errors   []config.LoadError
	Warnings []config.ValidationIssue
}

// ConfigWatcher watches a Clavis configuration directory for `.toml` changes,
// debounces bursts of edits, and triggers ConfigStore.Reload().
type ConfigWatcher struct {
	store        *config.ConfigStore
	debounce     time.Duration
	pollInterval time.Duration
	onReload     func(ReloadResult)

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// Option configures a ConfigWatcher.
type Option func(*ConfigWatcher)

// WithDebounce sets the quiet period after file changes before Reload is invoked.
func WithDebounce(d time.Duration) Option {
	return func(w *ConfigWatcher) {
		if d >= 0 {
			w.debounce = d
		}
	}
}

// WithPollInterval sets the filesystem polling interval for the pure-Go fallback watcher.
func WithPollInterval(d time.Duration) Option {
	return func(w *ConfigWatcher) {
		if d > 0 {
			w.pollInterval = d
		}
	}
}

// WithOnReload registers a callback invoked after every reload attempt.
func WithOnReload(fn func(ReloadResult)) Option {
	return func(w *ConfigWatcher) {
		w.onReload = fn
	}
}

// NewConfigWatcher constructs a ConfigWatcher for store.
func NewConfigWatcher(store *config.ConfigStore, opts ...Option) *ConfigWatcher {
	w := &ConfigWatcher{
		store:        store,
		debounce:     defaultDebounce,
		pollInterval: defaultPollInterval,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(w)
		}
	}
	return w
}

// StartWatching starts watching store.Directory() asynchronously.
// When the MyGo application event loop is active, it uses mygo.WatchFiles;
// otherwise it falls back to pure-Go polling so it works in headless tests and CLIs.
func (w *ConfigWatcher) StartWatching(ctx context.Context) error {
	if w.store == nil {
		return errors.New("watcher: nil ConfigStore")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return errors.New("watcher: already running")
	}
	watchCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	w.running = true
	w.cancel = cancel
	w.done = done
	w.mu.Unlock()

	if mygo.App.IsReady() {
		fw, err := mygo.WatchFiles(watchCtx, w.store.Directory(), mygo.FileWatchOptions{
			Recursive: false,
			Debounce:  w.debounce,
			Ignore: func(relPath string, isDir bool) bool {
				if isDir {
					return relPath != "."
				}
				return !isTOMLPath(relPath)
			},
		})
		if err == nil {
			go func() {
				defer func() {
					_ = fw.Close()
					w.markStopped()
					close(done)
				}()
				w.runMyGoLoop(watchCtx, fw)
			}()
			return nil
		}
	}

	initialSnap := snapshotTOMLDir(w.store.Directory())
	go func() {
		defer func() {
			w.markStopped()
			close(done)
		}()
		_ = w.watchDirWithPollSnapshot(watchCtx, initialSnap)
	}()
	return nil
}

// Stop cancels the active watch loop and waits for it to exit.
func (w *ConfigWatcher) Stop() {
	w.mu.Lock()
	cancel := w.cancel
	done := w.done
	if !w.running {
		w.mu.Unlock()
		if done != nil {
			<-done
		}
		return
	}
	w.cancel = nil
	w.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// Done returns a channel that closes when the watcher started by StartWatching exits.
// Returns nil if StartWatching has not been called.
func (w *ConfigWatcher) Done() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.done
}

func (w *ConfigWatcher) markStopped() {
	w.mu.Lock()
	w.running = false
	w.cancel = nil
	w.mu.Unlock()
}

func (w *ConfigWatcher) runMyGoLoop(ctx context.Context, fw *mygo.FileWatcher) {
	for {
		events, err := fw.Next(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return
			}
			// Fallback to polling if native watch stops unexpectedly.
			_ = w.WatchDirWithPoll(ctx)
			return
		}
		hasTOML := false
		for _, ev := range events {
			if isTOMLPath(ev.Path) {
				hasTOML = true
				break
			}
		}
		if hasTOML {
			w.triggerReloadIfChanged()
		}
	}
}

// WatchDirWithPoll polls store.Directory() for `.toml` changes, debounces rapid writes,
// and reloads ConfigStore until ctx is canceled.
func (w *ConfigWatcher) WatchDirWithPoll(ctx context.Context) error {
	if w.store == nil {
		return errors.New("watcher: nil ConfigStore")
	}
	return w.watchDirWithPollSnapshot(ctx, snapshotTOMLDir(w.store.Directory()))
}

func (w *ConfigWatcher) watchDirWithPollSnapshot(ctx context.Context, prev map[string]fileDigest) error {
	if w.store == nil {
		return errors.New("watcher: nil ConfigStore")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	dir := w.store.Directory()

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	var (
		debounceTimer *time.Timer
		debounceCh    <-chan time.Time
	)
	defer func() {
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
	}()

	scheduleReload := func() {
		if w.debounce <= 0 {
			w.triggerReloadIfChanged()
			return
		}
		if debounceTimer == nil {
			debounceTimer = time.NewTimer(w.debounce)
		} else {
			if !debounceTimer.Stop() {
				select {
				case <-debounceTimer.C:
				default:
				}
			}
			debounceTimer.Reset(w.debounce)
		}
		debounceCh = debounceTimer.C
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			curr := snapshotTOMLDir(dir)
			if !snapshotsEqual(prev, curr) {
				prev = curr
				scheduleReload()
			}
		case <-debounceCh:
			debounceCh = nil
			w.triggerReloadIfChanged()
		}
	}
}

func (w *ConfigWatcher) triggerReloadIfChanged() {
	if w.store != nil && len(w.store.LastErrors()) == 0 {
		saved := w.store.SavedDigests()
		if len(saved) > 0 {
			curr := snapshotTOMLDir(w.store.Directory())
			if len(curr) == len(saved) {
				identical := true
				for name, snap := range curr {
					if savedHash, ok := saved[name]; !ok || savedHash != snap.hash {
						identical = false
						break
					}
				}
				if identical {
					return
				}
			}
		}
	}
	w.triggerReload()
}

func (w *ConfigWatcher) triggerReload() {
	ok := w.store.Reload()
	if w.onReload != nil {
		w.onReload(ReloadResult{
			OK:       ok,
			Config:   w.store.Current(),
			Errors:   w.store.LastErrors(),
			Warnings: w.store.LastWarnings(),
		})
	}
}

type fileDigest struct {
	size      int64
	modTimeNs int64
	hash      [32]byte
}

func snapshotTOMLDir(dir string) map[string]fileDigest {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make(map[string]fileDigest)
	for _, entry := range entries {
		if entry.IsDir() || !isTOMLPath(entry.Name()) {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		out[entry.Name()] = fileDigest{
			size:      info.Size(),
			modTimeNs: info.ModTime().UnixNano(),
			hash:      sha256.Sum256(data),
		}
	}
	return out
}

func snapshotsEqual(a, b map[string]fileDigest) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || va != vb {
			return false
		}
	}
	return true
}

func isTOMLPath(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".toml")
}
