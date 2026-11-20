package watcher

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultDictionaryPollInterval = 150 * time.Millisecond
	defaultDictionaryDebounce     = 120 * time.Millisecond
)

// DictionaryWatcher polls a user's .clvdict directory and invokes OnChange after
// a debounced add, modification, or deletion. The callback runs outside watcher
// locks and should resolve/index dictionaries before reconfiguring the bridge.
type DictionaryWatcher struct {
	dir          string
	pollInterval time.Duration
	debounce     time.Duration
	onChange     func()
	mu           sync.Mutex
	running      bool
	cancel       context.CancelFunc
	done         chan struct{}
}

type DictionaryWatcherOption func(*DictionaryWatcher)

func WithDictionaryPollInterval(d time.Duration) DictionaryWatcherOption {
	return func(w *DictionaryWatcher) {
		if d > 0 {
			w.pollInterval = d
		}
	}
}
func WithDictionaryDebounce(d time.Duration) DictionaryWatcherOption {
	return func(w *DictionaryWatcher) {
		if d >= 0 {
			w.debounce = d
		}
	}
}

// UserDictionaryDir returns the conventional per-user dictionary directory.
func UserDictionaryDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".config", "clavis", "dicts")
	}
	return filepath.Join(home, ".config", "clavis", "dicts")
}

func NewDictionaryWatcher(dir string, onChange func(), opts ...DictionaryWatcherOption) *DictionaryWatcher {
	w := &DictionaryWatcher{dir: dir, onChange: onChange, pollInterval: defaultDictionaryPollInterval, debounce: defaultDictionaryDebounce}
	for _, opt := range opts {
		if opt != nil {
			opt(w)
		}
	}
	return w
}

func (w *DictionaryWatcher) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return os.ErrExist
	}
	watchCtx, cancel := context.WithCancel(ctx)
	w.running, w.cancel, w.done = true, cancel, make(chan struct{})
	done := w.done
	w.mu.Unlock()
	go func() { defer close(done); defer w.markStopped(); w.run(watchCtx) }()
	return nil
}

func (w *DictionaryWatcher) Stop() {
	w.mu.Lock()
	cancel, done := w.cancel, w.done
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (w *DictionaryWatcher) Done() <-chan struct{} { w.mu.Lock(); defer w.mu.Unlock(); return w.done }

func (w *DictionaryWatcher) markStopped() {
	w.mu.Lock()
	w.running, w.cancel = false, nil
	w.mu.Unlock()
}

func (w *DictionaryWatcher) run(ctx context.Context) {
	prev := snapshotDictionaryDir(w.dir)
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	var timer *time.Timer
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-ticker.C:
			curr := snapshotDictionaryDir(w.dir)
			if dictionarySnapshotsEqual(prev, curr) {
				continue
			}
			prev = curr
			if w.debounce <= 0 {
				w.fire()
				continue
			}
			if timer == nil {
				timer = time.NewTimer(w.debounce)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(w.debounce)
			}
			debounce = timer.C
		case <-debounce:
			debounce = nil
			w.fire()
		}
	}
}

func (w *DictionaryWatcher) fire() {
	if w.onChange != nil {
		w.onChange()
	}
}

type dictionaryDigest struct {
	size    int64
	modTime int64
	hash    [32]byte
}

func snapshotDictionaryDir(dir string) map[string]dictionaryDigest {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".clvdict") {
			paths = append(paths, entry.Name())
		}
	}
	sort.Strings(paths)
	out := make(map[string]dictionaryDigest, len(paths))
	for _, name := range paths {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		out[name] = dictionaryDigest{size: info.Size(), modTime: info.ModTime().UnixNano(), hash: sha256.Sum256(data)}
	}
	return out
}
func dictionarySnapshotsEqual(a, b map[string]dictionaryDigest) bool {
	if len(a) != len(b) {
		return false
	}
	for key, va := range a {
		if vb, ok := b[key]; !ok || vb != va {
			return false
		}
	}
	return true
}
