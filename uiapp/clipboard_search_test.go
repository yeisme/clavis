package uiapp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/internal/storage"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

type countingClipboardStore struct {
	clipboard.Store
	gen         clipboard.GenerationTracker
	recentCalls atomic.Int64
	lastLimit   atomic.Int64
	searchCalls atomic.Int64
}

func newCountingStore(inner clipboard.Store) *countingClipboardStore {
	gt, _ := inner.(clipboard.GenerationTracker)
	return &countingClipboardStore{Store: inner, gen: gt}
}

func (s *countingClipboardStore) Generation() uint64 {
	return s.gen.Generation()
}

func (s *countingClipboardStore) Recent(ctx context.Context, limit int) ([]clipboard.ClipboardItem, error) {
	s.recentCalls.Add(1)
	s.lastLimit.Store(int64(limit))
	return s.Store.Recent(ctx, limit)
}

func (s *countingClipboardStore) Search(ctx context.Context, query string, limit int) ([]clipboard.ClipboardItem, error) {
	s.searchCalls.Add(1)
	return s.Store.Search(ctx, query, limit)
}

func newControllerWithStore(t testing.TB, store clipboard.Store, searcher ClipboardSearcher, maxHistory int) *AppController {
	t.Helper()
	dir := t.TempDir()
	cfgStore := config.NewConfigStore(dir)
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}
	if maxHistory > 0 {
		mainCfg := cfgStore.Current().Main
		mainCfg.Clipboard.MaxHistoryItems = maxHistory
		if err := cfgStore.SaveMain(mainCfg); err != nil {
			t.Fatalf("SaveMain: %v", err)
		}
	}
	secStore := secrets.NewMemoryAPIKeyStore()
	ctrl := NewAppController(Options{
		ConfigStore:       cfgStore,
		ClipboardStore:    store,
		ClipboardSearcher: searcher,
		SecretStore:       secStore,
		Bridge:            newMockBridge(),
	})
	t.Cleanup(ctrl.Close)
	return ctrl
}

func TestClipboardSync_GenerationNoOpAndBoundedMirror(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := storage.NewSQLiteClipboardStore(ctx, ":memory:", clipboard.NewPrivacyFilter())
	if err != nil {
		t.Fatalf("NewSQLiteClipboardStore: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })

	now := time.Now().UTC()
	for i := range 200 {
		_, _, err := sqlStore.Record(ctx, clipboard.CaptureCandidate{
			Content:       fmt.Sprintf("item-%03d %s", i, strings.Repeat("x", 1024)),
			SourceAppName: "Notes",
			Timestamp:     now.Add(time.Duration(i) * time.Millisecond),
		})
		if err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}

	counted := newCountingStore(sqlStore)
	ctrl := newControllerWithStore(t, counted, nil, 200)

	// First sync queries Recent with bounded mirror window (min(200, 50) = 50).
	ctrl.syncClipboardEntries(ctx)
	if got := counted.recentCalls.Load(); got != 1 {
		t.Fatalf("first sync Recent calls = %d, want 1", got)
	}
	if got := counted.lastLimit.Load(); got != defaultClipboardMirrorLimit {
		t.Fatalf("bounded mirror limit = %d, want %d", got, defaultClipboardMirrorLimit)
	}
	if got := len(ctrl.State().ClipboardEntries); got != defaultClipboardMirrorLimit {
		t.Fatalf("ClipboardEntries len = %d, want %d", got, defaultClipboardMirrorLimit)
	}

	// Repeated syncs / Capsule opens when generation is unchanged must be no-ops.
	ctrl.syncClipboardEntries(ctx)
	ctrl.OpenLeader()
	ctrl.OpenOmnibox(config.TabClipboard, "")
	if got := counted.recentCalls.Load(); got != 1 {
		t.Fatalf("unchanged generation Recent calls = %d, want 1 (no-op)", got)
	}

	// Mutating the store advances Generation(), triggering exactly one re-sync.
	_, _, err = sqlStore.Record(ctx, clipboard.CaptureCandidate{
		Content:       "brand-new-entry",
		SourceAppName: "Terminal",
		Timestamp:     now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("Record new: %v", err)
	}
	ctrl.OpenOmnibox(config.TabClipboard, "")
	if got := counted.recentCalls.Load(); got != 2 {
		t.Fatalf("post-mutation Recent calls = %d, want 2", got)
	}
	if ctrl.State().OmniboxItems[0].ClipboardContent != "brand-new-entry" {
		t.Fatalf("expected newest entry first, got %q", ctrl.State().OmniboxItems[0].ClipboardContent)
	}

	// Subsequent unchanged open is again a no-op.
	ctrl.OpenOmnibox(config.TabClipboard, "")
	if got := counted.recentCalls.Load(); got != 2 {
		t.Fatalf("post-resync unchanged Recent calls = %d, want 2", got)
	}

	// Small MaxHistoryItems (< 50) and small histories preserve exact counts.
	smallStore := clipboard.NewMemoryStore(clipboard.NewPrivacyFilter())
	for i := range 5 {
		_, _, _ = smallStore.Record(ctx, clipboard.CaptureCandidate{
			Content:   fmt.Sprintf("small-%d", i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
		})
	}
	smallCounted := newCountingStore(smallStore)
	smallCtrl := newControllerWithStore(t, smallCounted, nil, 10)
	smallCtrl.syncClipboardEntries(ctx)
	if got := smallCounted.lastLimit.Load(); got != 10 {
		t.Fatalf("small MaxHistoryItems limit = %d, want 10", got)
	}
	if got := len(smallCtrl.State().ClipboardEntries); got != 5 {
		t.Fatalf("small history ClipboardEntries = %d, want 5", got)
	}
}

func TestClipboardSearch_SQLiteFTS5AndCJKFallback(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := storage.NewSQLiteClipboardStore(ctx, ":memory:", clipboard.NewPrivacyFilter())
	if err != nil {
		t.Fatalf("NewSQLiteClipboardStore: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })

	base := time.Now().UTC()
	// Record old items outside the 50-item recent mirror window so only FTS5/LIKE can find them.
	oldFTSItem, _, err := sqlStore.Record(ctx, clipboard.CaptureCandidate{
		Content:       "kubectl get pods -n production-cluster",
		SourceAppName: "iTerm2",
		Timestamp:     base,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = sqlStore.Record(ctx, clipboard.CaptureCandidate{
		Content:       "这是一个用于验证中文剪贴板全文检索的条目",
		SourceAppName: "WeChat",
		Timestamp:     base.Add(1 * time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = sqlStore.Record(ctx, clipboard.CaptureCandidate{
		Content:       "deploy_check 100% foo_bar.baz",
		SourceAppName: "VS Code",
		Timestamp:     base.Add(2 * time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 80 {
		_, _, err := sqlStore.Record(ctx, clipboard.CaptureCandidate{
			Content:       fmt.Sprintf("recent-filler-%03d", i),
			SourceAppName: "Notes",
			Timestamp:     base.Add(time.Duration(10+i) * time.Millisecond),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	ctrl := newControllerWithStore(t, sqlStore, nil, 200)
	ctrl.clipSearchDebounce = 10 * time.Millisecond

	ctrl.OpenOmnibox(config.TabClipboard, "")
	if got := len(ctrl.State().OmniboxItems); got != defaultClipboardMirrorLimit {
		t.Fatalf("initial mirror items = %d, want %d", got, defaultClipboardMirrorLimit)
	}

	// 1. FTS5 English prefix search finds item outside the 50-item mirror.
	ctrl.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: "kube prod"})
	waitUntil(t, 2*time.Second, func() bool {
		st := ctrl.State()
		return len(st.OmniboxItems) == 1 && st.OmniboxItems[0].ClipboardID == oldFTSItem.ID
	}, "FTS5 query finds old item outside 50-item mirror")

	// 2. Pinning the searched item outside the mirror persists and updates state.
	ctrl.Dispatch(engine.Event{Type: engine.EventTogglePinSelectedClipboardItem})
	waitUntil(t, 2*time.Second, func() bool {
		recent, _ := sqlStore.Recent(ctx, 5)
		return len(recent) > 0 && recent[0].ID == oldFTSItem.ID && recent[0].IsPinned
	}, "pinned searched item persisted")

	// 3. CJK query hits SQLite searchLike fallback through AppController.
	ctrl.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: "中文剪贴板"})
	waitUntil(t, 2*time.Second, func() bool {
		st := ctrl.State()
		return len(st.OmniboxItems) == 1 && strings.Contains(st.OmniboxItems[0].ClipboardContent, "中文剪贴板全文检索")
	}, "CJK query via SQLite LIKE fallback")

	// 4. Punctuation / special char query hits SQLite LIKE fallback.
	ctrl.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: "100%"})
	waitUntil(t, 2*time.Second, func() bool {
		st := ctrl.State()
		return len(st.OmniboxItems) == 1 && strings.Contains(st.OmniboxItems[0].ClipboardContent, "foo_bar.baz")
	}, "punctuation query via SQLite LIKE fallback")

	// 5. Clearing the query restores the bounded recent mirror immediately.
	ctrl.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: ""})
	st := ctrl.State()
	if len(st.OmniboxItems) != defaultClipboardMirrorLimit {
		t.Fatalf("cleared query items = %d, want %d", len(st.OmniboxItems), defaultClipboardMirrorLimit)
	}
	if st.OmniboxItems[0].ClipboardID != oldFTSItem.ID || !st.ClipboardEntries[0].IsPinned {
		t.Fatalf("expected newly pinned item at top of restored mirror, got %+v", st.OmniboxItems[0])
	}
}

type spyClipboardSearcher struct {
	mu      sync.Mutex
	queries []string
	fn      func(ctx context.Context, query string, limit int) ([]clipboard.ClipboardItem, error)
}

func (s *spyClipboardSearcher) Search(ctx context.Context, query string, limit int) ([]clipboard.ClipboardItem, error) {
	s.mu.Lock()
	s.queries = append(s.queries, query)
	fn := s.fn
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, query, limit)
	}
	return nil, nil
}

func (s *spyClipboardSearcher) recordedQueries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queries...)
}

func TestClipboardSearch_DebounceCoalescesRapidTyping(t *testing.T) {
	store := clipboard.NewMemoryStore(clipboard.NewPrivacyFilter())
	spy := &spyClipboardSearcher{
		fn: func(_ context.Context, query string, _ int) ([]clipboard.ClipboardItem, error) {
			return []clipboard.ClipboardItem{{ID: "hit-" + query, Content: "matched " + query}}, nil
		},
	}
	ctrl := newControllerWithStore(t, store, spy, 200)
	ctrl.clipSearchDebounce = 40 * time.Millisecond

	ctrl.OpenOmnibox(config.TabClipboard, "")
	for _, q := range []string{"k", "ku", "kub", "kube"} {
		ctrl.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: q})
		time.Sleep(3 * time.Millisecond)
	}

	waitUntil(t, 2*time.Second, func() bool {
		st := ctrl.State()
		return len(st.OmniboxItems) == 1 && st.OmniboxItems[0].ClipboardID == "hit-kube"
	}, "coalesced search result applied")

	queries := spy.recordedQueries()
	if len(queries) != 1 || queries[0] != "kube" {
		t.Fatalf("expected 1 coalesced query [kube], got %v", queries)
	}
}

func TestClipboardSearch_StaleQueryAndGenerationDropped(t *testing.T) {
	t.Run("stale query dropped", func(t *testing.T) {
		store := clipboard.NewMemoryStore(clipboard.NewPrivacyFilter())
		alphaStarted := make(chan struct{})
		releaseAlpha := make(chan struct{})
		var alphaOnce sync.Once

		spy := &spyClipboardSearcher{
			fn: func(_ context.Context, query string, _ int) ([]clipboard.ClipboardItem, error) {
				if query == "alpha" {
					alphaOnce.Do(func() { close(alphaStarted) })
					<-releaseAlpha
					return []clipboard.ClipboardItem{{ID: "stale-alpha", Content: "stale alpha"}}, nil
				}
				return []clipboard.ClipboardItem{{ID: "fresh-beta", Content: "fresh beta"}}, nil
			},
		}
		ctrl := newControllerWithStore(t, store, spy, 200)
		ctrl.clipSearchDebounce = 5 * time.Millisecond
		t.Cleanup(func() {
			select {
			case <-releaseAlpha:
			default:
				close(releaseAlpha)
			}
		})

		// Start "alpha" immediately via OpenOmnibox with non-empty query.
		ctrl.OpenOmnibox(config.TabClipboard, "alpha")
		<-alphaStarted

		// User types "beta" while "alpha" is in-flight.
		ctrl.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: "beta"})

		// Unblock stale "alpha" first; it must be dropped because seq/query moved to "beta".
		close(releaseAlpha)

		waitUntil(t, 2*time.Second, func() bool {
			st := ctrl.State()
			return len(st.OmniboxItems) == 1 && st.OmniboxItems[0].ClipboardID == "fresh-beta"
		}, "fresh beta applied")

		// Ensure stale alpha never overwrites beta.
		time.Sleep(30 * time.Millisecond)
		st := ctrl.State()
		if len(st.OmniboxItems) != 1 || st.OmniboxItems[0].ClipboardID != "fresh-beta" {
			t.Fatalf("stale alpha overwrote beta: %+v", st.OmniboxItems)
		}
	})

	t.Run("stale generation dropped", func(t *testing.T) {
		ctx := context.Background()
		store := clipboard.NewMemoryStore(clipboard.NewPrivacyFilter())
		_, _, _ = store.Record(ctx, clipboard.CaptureCandidate{Content: "initial item"})

		gammaStarted := make(chan struct{})
		releaseGamma := make(chan struct{})
		var gammaOnce sync.Once

		spy := &spyClipboardSearcher{
			fn: func(_ context.Context, query string, _ int) ([]clipboard.ClipboardItem, error) {
				gammaOnce.Do(func() { close(gammaStarted) })
				<-releaseGamma
				return []clipboard.ClipboardItem{{ID: "stale-gen", Content: "stale generation result"}}, nil
			},
		}
		ctrl := newControllerWithStore(t, store, spy, 200)
		t.Cleanup(func() {
			select {
			case <-releaseGamma:
			default:
				close(releaseGamma)
			}
		})

		ctrl.OpenOmnibox(config.TabClipboard, "gamma")
		<-gammaStarted

		// Mutate the store while "gamma" is in-flight, advancing Generation().
		_, _, _ = store.Record(ctx, clipboard.CaptureCandidate{Content: "mutated while searching"})
		close(releaseGamma)

		// Give worker time to finish and evaluate the generation guard.
		waitUntil(t, 2*time.Second, func() bool {
			return len(spy.recordedQueries()) == 1
		}, "gamma search finished")
		time.Sleep(30 * time.Millisecond)

		if st := ctrl.State(); len(st.OmniboxItems) != 0 {
			t.Fatalf("expected stale-generation result dropped, got %+v", st.OmniboxItems)
		}
	})
}

func TestClipboardSearch_MemoryStoreFallback(t *testing.T) {
	ctx := context.Background()
	memStore := clipboard.NewMemoryStore(clipboard.NewPrivacyFilter())
	base := time.Now().UTC()

	// Insert 80 items so oldest target falls outside the 50-item recent mirror.
	_, _, _ = memStore.Record(ctx, clipboard.CaptureCandidate{
		Content:       "ancient-memory-needle",
		SourceAppName: "Notes",
		Timestamp:     base,
	})
	for i := range 79 {
		_, _, _ = memStore.Record(ctx, clipboard.CaptureCandidate{
			Content:       fmt.Sprintf("recent-mem-%03d", i),
			SourceAppName: "Notes",
			Timestamp:     base.Add(time.Duration(i+1) * time.Millisecond),
		})
	}

	counted := newCountingStore(memStore)
	ctrl := newControllerWithStore(t, counted, nil, 200)
	ctrl.clipSearchDebounce = 10 * time.Millisecond

	ctrl.OpenOmnibox(config.TabClipboard, "")
	if got := len(ctrl.State().ClipboardEntries); got != defaultClipboardMirrorLimit {
		t.Fatalf("MemoryStore mirror len = %d, want %d", got, defaultClipboardMirrorLimit)
	}

	ctrl.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: "ancient-memory"})
	waitUntil(t, 2*time.Second, func() bool {
		st := ctrl.State()
		return len(st.OmniboxItems) == 1 && st.OmniboxItems[0].ClipboardContent == "ancient-memory-needle"
	}, "MemoryStore.Search fallback found item outside mirror")
	if counted.searchCalls.Load() != 1 {
		t.Fatalf("expected MemoryStore.Search called once, got %d", counted.searchCalls.Load())
	}
}

func BenchmarkClipboardSync_And_SQLiteFTS5Search(b *testing.B) {
	ctx := context.Background()
	sqlStore, err := storage.NewSQLiteClipboardStore(ctx, ":memory:", clipboard.NewPrivacyFilter())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = sqlStore.Close() })

	pad := strings.Repeat("clipboard benchmark payload text ", 128) // ~4KB
	now := time.Now().UTC()
	for i := range 200 {
		content := fmt.Sprintf("item-%03d %s", i, pad)
		if i == 10 {
			content = fmt.Sprintf("item-%03d unique-fts-needle %s", i, pad)
		}
		_, _, err := sqlStore.Record(ctx, clipboard.CaptureCandidate{
			Content:       content,
			SourceAppName: "Notes",
			Timestamp:     now.Add(time.Duration(i) * time.Millisecond),
		})
		if err != nil {
			b.Fatal(err)
		}
	}

	b.Run("Sync_GenerationNoOp", func(b *testing.B) {
		ctrl := newControllerWithStore(b, sqlStore, nil, 200)
		ctrl.syncClipboardEntries(ctx)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			ctrl.syncClipboardEntries(ctx)
		}
	})

	b.Run("SQLiteFTS5_Search_200x4KB", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			items, err := sqlStore.Search(ctx, "unique-fts-needle", 50)
			if err != nil || len(items) != 1 {
				b.Fatalf("Search err=%v len=%d", err, len(items))
			}
		}
	})
}
