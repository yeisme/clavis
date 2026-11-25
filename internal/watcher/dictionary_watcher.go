package watcher

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
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

// DictionaryWatcher polls a user's .clvdict directory and optional custom
// dictionary paths and invokes OnChange after a debounced add, modification, or
// deletion. The callback runs outside watcher locks and should resolve/index
// dictionaries before reconfiguring the bridge.
type DictionaryWatcher struct {
	dir          string
	extraPaths   []string
	pollInterval time.Duration
	debounce     time.Duration
	onChange     func()
	hashFile     func(string) ([32]byte, error)
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

// WithDictionaryExtraPaths registers additional custom dictionary file or
// directory paths (for example configured CustomDictPaths such as .dict.yaml or
// .clvdict files) to watch alongside the primary dictionary directory.
func WithDictionaryExtraPaths(paths ...string) DictionaryWatcherOption {
	return func(w *DictionaryWatcher) {
		w.extraPaths = cleanWatchPaths(append(w.extraPaths, paths...))
	}
}

// UserDictionaryDir returns the conventional per-user dictionary directory.
func UserDictionaryDir() string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".config", "clavis", "dicts")
	}
	if cfgDir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(cfgDir) != "" {
		return filepath.Join(cfgDir, "clavis", "dicts")
	}
	return filepath.Join(".config", "clavis", "dicts")
}

// UserDictionaryDirIn returns the dictionary directory beneath an explicit
// configDir without hardcoding ~/.config/clavis. When configDir is blank it
// falls back to UserDictionaryDir.
func UserDictionaryDirIn(configDir string) string {
	if dir := strings.TrimSpace(configDir); dir != "" {
		return filepath.Join(dir, "dicts")
	}
	return UserDictionaryDir()
}

func NewDictionaryWatcher(dir string, onChange func(), opts ...DictionaryWatcherOption) *DictionaryWatcher {
	w := &DictionaryWatcher{
		dir:          strings.TrimSpace(dir),
		onChange:     onChange,
		pollInterval: defaultDictionaryPollInterval,
		debounce:     defaultDictionaryDebounce,
		hashFile:     sha256File,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(w)
		}
	}
	return w
}

// SetExtraPaths replaces the extra watched custom dictionary paths safely while
// the watcher is idle or running.
func (w *DictionaryWatcher) SetExtraPaths(paths []string) {
	cleaned := cleanWatchPaths(paths)
	w.mu.Lock()
	w.extraPaths = cleaned
	w.mu.Unlock()
}

func (w *DictionaryWatcher) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Establish the baseline before returning from Start. If this happens in the
	// goroutine, a caller can write a dictionary immediately after Start and have
	// that write mistaken for the initial state, losing the first callback.
	baseline, err := w.snapshot(nil)
	if err != nil {
		return err
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
	go func() { defer close(done); defer w.markStopped(); w.run(watchCtx, baseline) }()
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

func (w *DictionaryWatcher) run(ctx context.Context, prev map[string]dictionaryDigest) {
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
			curr, err := w.snapshot(prev)
			if err != nil {
				continue
			}
			if dictionarySnapshotsEqual(prev, curr) {
				// Advance cached metadata (e.g. mtime-only touch with identical bytes)
				// so subsequent ticks skip re-hashing unchanged files.
				prev = curr
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

func (w *DictionaryWatcher) snapshot(prev map[string]dictionaryDigest) (map[string]dictionaryDigest, error) {
	w.mu.Lock()
	dir := w.dir
	extra := append([]string(nil), w.extraPaths...)
	hashFn := w.hashFile
	w.mu.Unlock()
	if hashFn == nil {
		hashFn = sha256File
	}

	type targetFile struct {
		path string
		info os.FileInfo
	}
	seen := make(map[string]struct{})
	targets := make([]targetFile, 0, len(extra)+4)
	addTarget := func(p string, info os.FileInfo) {
		clean := filepath.Clean(p)
		if _, exists := seen[clean]; exists {
			return
		}
		seen[clean] = struct{}{}
		targets = append(targets, targetFile{path: clean, info: info})
	}
	scanClvDictDir := func(dirPath string, strict bool) error {
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// A missing directory is treated as an empty file set so creating it
				// later automatically triggers hot-reload instead of failing Start.
				return nil
			}
			if strict {
				return err
			}
			return nil
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".clvdict") {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			addTarget(filepath.Join(dirPath, name), nil)
		}
		return nil
	}

	if dir != "" {
		if err := scanClvDictDir(dir, true); err != nil {
			return nil, err
		}
	}
	for _, p := range extra {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.IsDir() {
			_ = scanClvDictDir(p, false)
			continue
		}
		addTarget(p, info)
	}

	out := make(map[string]dictionaryDigest, len(targets))
	for _, t := range targets {
		info := t.info
		if info == nil {
			var err error
			info, err = os.Stat(t.path)
			if err != nil || info.IsDir() {
				continue
			}
		}
		size := info.Size()
		modTime := info.ModTime().UnixNano()
		if old, ok := prev[t.path]; ok && old.size == size && old.modTime == modTime {
			out[t.path] = old
			continue
		}
		// Verify file stat did not change mid-hash so we never pair a stale
		// (size, modTime) with newer content and force a duplicate re-hash.
		var sum [32]byte
		var err error
		for attempt := 0; attempt < 2; attempt++ {
			sum, err = hashFn(t.path)
			if err != nil {
				break
			}
			afterInfo, statErr := os.Stat(t.path)
			if statErr != nil || afterInfo.IsDir() {
				if statErr != nil {
					err = statErr
				} else {
					err = os.ErrInvalid
				}
				break
			}
			if afterInfo.Size() == size && afterInfo.ModTime().UnixNano() == modTime {
				break
			}
			size = afterInfo.Size()
			modTime = afterInfo.ModTime().UnixNano()
		}
		if err != nil {
			continue
		}
		out[t.path] = dictionaryDigest{size: size, modTime: modTime, hash: sum}
	}
	return out, nil
}

func sha256File(path string) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [32]byte{}, err
	}
	var sum [32]byte
	h.Sum(sum[:0])
	return sum, nil
}

func cleanWatchPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		clean := filepath.Clean(p)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func dictionarySnapshotsEqual(a, b map[string]dictionaryDigest) bool {
	if len(a) != len(b) {
		return false
	}
	for key, va := range a {
		if vb, ok := b[key]; !ok || vb.size != va.size || vb.hash != va.hash {
			return false
		}
	}
	return true
}
