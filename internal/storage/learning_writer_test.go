package storage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
)

// mirrorOnlyStore is a LearningStore without a database, for tests that
// stand in their own persist function.
func mirrorOnlyStore() *LearningStore {
	return &LearningStore{lex: make(map[lexKey]lexValue), bigrams: make(map[[2]string]lexValue), uses: make(map[string]useValue)}
}

// recordingSink is a persist function that takes `delay` per batch, as a
// native SQLite round trip does on Windows (~12.6ms, far worse with an
// fsync), and records what it was given.
type recordingSink struct {
	delay time.Duration
	fail  int // fail this many calls first

	mu      sync.Mutex
	batches []*learningBatch
	calls   int
}

func (r *recordingSink) persist(ctx context.Context, b *learningBatch) error {
	time.Sleep(r.delay)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.calls <= r.fail {
		return errors.New("disk I/O error")
	}
	r.batches = append(r.batches, b)
	return nil
}

func (r *recordingSink) totals() (lex, bigram, uses int64, batches int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range r.batches {
		for _, d := range b.lex {
			lex += d.count
		}
		for _, d := range b.bigrams {
			bigram += d.count
		}
		for _, d := range b.uses {
			uses += d.count
		}
	}
	return lex, bigram, uses, len(r.batches)
}

// TestLearningWriterNeverBlocksOnSlowDisk is the hot-path contract: with a
// disk that takes 120ms per batch, queueing 2000 commits (a long typing
// burst on the keyboard hook thread) must not wait for it, the RAM mirror
// must reflect every commit at once, and the commits must coalesce into a
// few batches rather than 2000 writes.
func TestLearningWriterNeverBlocksOnSlowDisk(t *testing.T) {
	store := mirrorOnlyStore()
	sink := &recordingSink{delay: 120 * time.Millisecond}
	w := newLearningWriter(store, LearningWriterOptions{FlushInterval: 5 * time.Millisecond, MaxPendingOps: 8}, sink.persist)

	const commits = 2000
	start := time.Now()
	for i := 0; i < commits; i++ {
		w.QueueIMECommit("nihao", "你好", "", false)
		w.QueueIMECommit("shijie", "世界", "你好", false)
	}
	elapsed := time.Since(start)
	// A writer that persisted inline would need ≥ 2000×120ms; even one
	// blocking flush would cost 120ms. Generous bound for loaded CI.
	if elapsed > 100*time.Millisecond {
		t.Fatalf("queueing %d commits took %v: the hot path is waiting on storage", 2*commits, elapsed)
	}
	if got := store.LookupBoost("nihao", "你好", ""); got != commits {
		t.Fatalf("mirror boost = %d right after queueing, want %d", got, commits)
	}
	if got := store.LookupBoost("shijie", "世界", "你好"); got != commits+2*commits {
		t.Fatalf("mirror boost with bigram = %d, want %d", got, 3*commits)
	}

	if err := w.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	lex, bigram, _, batches := sink.totals()
	if lex != 2*commits || bigram != commits {
		t.Fatalf("persisted lex=%d bigram=%d, want %d and %d", lex, bigram, 2*commits, commits)
	}
	if batches > 20 {
		t.Fatalf("%d batches for one burst: updates are not coalescing", batches)
	}
	if st := w.Stats(); st.Dropped != 0 || st.Pending != 0 {
		t.Fatalf("stats after Close = %+v", st)
	}
}

func TestLearningWriterQueuesAIUsageWithoutInlineStorage(t *testing.T) {
	store := mirrorOnlyStore()
	sink := &recordingSink{delay: 120 * time.Millisecond}
	w := newLearningWriter(store, LearningWriterOptions{FlushInterval: time.Hour}, sink.persist)
	start := time.Now()
	w.QueueAIUsage(AIUsageRecord{At: time.Now(), Profile: "p", Provider: "provider", Model: "model", Action: "polish", InputTokens: 3, OutputTokens: 2, Reported: true})
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("QueueAIUsage blocked for %v", elapsed)
	}
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.batches) != 1 || len(sink.batches[0].aiUsage) != 1 {
		t.Fatalf("batches=%+v", sink.batches)
	}
}

func TestLearningWriterRetriesThenSucceeds(t *testing.T) {
	store := mirrorOnlyStore()
	sink := &recordingSink{fail: 1}
	w := newLearningWriter(store, LearningWriterOptions{FlushInterval: time.Hour}, sink.persist)
	defer w.Close(context.Background())

	w.QueueItemUse("leader:w.h#0", time.Now())
	w.QueueItemUse("leader:w.h#0", time.Now())
	if err := w.Flush(context.Background()); err == nil {
		t.Fatal("first flush should report the injected failure")
	}
	w.QueueItemUse("leader:w.h#0", time.Now())
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("retry flush: %v", err)
	}
	if _, _, uses, _ := sink.totals(); uses != 3 {
		t.Fatalf("persisted uses = %d, want the failed 2 merged with the new 1", uses)
	}
	if st := w.Stats(); st.LastError == "" || st.Dropped != 0 {
		t.Fatalf("stats = %+v, want a recorded error and nothing dropped", st)
	}
}

func TestLearningWriterDropsAfterRetries(t *testing.T) {
	store := mirrorOnlyStore()
	sink := &recordingSink{fail: 1 << 30}
	w := newLearningWriter(store, LearningWriterOptions{FlushInterval: time.Hour}, sink.persist)

	w.QueueIMECommit("ceshi", "测试", "", false)
	for i := 0; i < flushRetries; i++ {
		_ = w.Flush(context.Background())
	}
	if st := w.Stats(); st.Dropped != 1 || st.Pending != 0 {
		t.Fatalf("stats = %+v, want the batch dropped after %d attempts", st, flushRetries)
	}
	_ = w.Close(context.Background())
}

func TestLearningWriterCloseHonorsDeadlineDuringBackgroundFlush(t *testing.T) {
	store := mirrorOnlyStore()
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	backgroundCanceled := make(chan struct{})
	var persisted int64
	w := newLearningWriter(store, LearningWriterOptions{MaxPendingOps: 1}, func(ctx context.Context, batch *learningBatch) error {
		select {
		case <-started:
			persisted += batch.uses["item"].count
			return ctx.Err()
		default:
			close(started)
		}
		select {
		case <-ctx.Done():
			close(backgroundCanceled)
			return ctx.Err()
		case <-release:
			return nil
		}
	})
	w.QueueItemUse("item", time.Now())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background flush did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- w.Close(ctx) }()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close blocked past its deadline")
	}
	select {
	case <-backgroundCanceled:
	case <-time.After(time.Second):
		t.Fatal("background flush was not canceled")
	}
	if err := w.Close(context.Background()); err != nil {
		t.Fatalf("retry Close: %v", err)
	}
	if persisted != 1 {
		t.Fatalf("retry persisted %d events, want 1", persisted)
	}
}

// TestLearningWriterPersistsDeltas checks the SQL side on a real database:
// coalesced counters land as deltas in one transaction and reload intact.
// It costs a handful of native round trips, fine on Windows too.
func TestLearningWriterPersistsDeltas(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:", sqlite.OpenOptions{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	store, err := NewLearningStore(db)
	if err != nil {
		t.Fatalf("NewLearningStore: %v", err)
	}
	// An earlier synchronous write must be added to, not overwritten.
	if err := store.RecordIMECommit(ctx, "nihao", "你好", "", false); err != nil {
		t.Fatalf("RecordIMECommit: %v", err)
	}

	w := NewLearningWriter(store, LearningWriterOptions{FlushInterval: time.Hour})
	for i := 0; i < 5; i++ {
		w.QueueIMECommit("nihao", "你好", "", false)
	}
	w.QueueIMECommit("shijie", "世界", "你好", true)
	w.QueueItemUse("leader:w.h#0", time.Now())
	w.QueueItemUse("leader:w.h#0", time.Now())
	w.QueuePromptVersion("cr", "/cr", "draft", "final", "default")
	if err := w.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	scalar := func(q string, args ...any) int64 {
		t.Helper()
		rows, err := db.Query(ctx, q, args...)
		if err != nil || len(rows.Values) != 1 || len(rows.Values[0]) != 1 {
			t.Fatalf("query %q: rows=%v err=%v", q, rows.Values, err)
		}
		return int64val(rows.Values[0], 0)
	}
	if got := scalar(`SELECT frequency FROM ime_user_lexicon WHERE pinyin_key=? AND word=?`, "nihao", "你好"); got != 6 {
		t.Fatalf("frequency = %d, want 1 synchronous + 5 queued", got)
	}
	if got := scalar(`SELECT pinned FROM ime_user_lexicon WHERE pinyin_key=? AND word=?`, "shijie", "世界"); got != 1 {
		t.Fatalf("pinned = %d, want 1", got)
	}
	if got := scalar(`SELECT transition_count FROM ime_bigrams WHERE prev_word=? AND word=?`, "你好", "世界"); got != 1 {
		t.Fatalf("bigram count = %d", got)
	}
	if got := scalar(`SELECT use_count FROM usage_frecency WHERE item_id=?`, "leader:w.h#0"); got != 2 {
		t.Fatalf("use_count = %d", got)
	}
	if got := scalar(`SELECT COUNT(*) FROM prompt_history WHERE prompt_id=?`, "cr"); got != 1 {
		t.Fatalf("prompt rows = %d", got)
	}

	reloaded, err := NewLearningStore(db)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, want := reloaded.LookupBoost("nihao", "你好", ""), store.LookupBoost("nihao", "你好", ""); got != want || got != 6 {
		t.Fatalf("reloaded boost = %d, live mirror = %d, want both 6", got, want)
	}
}
