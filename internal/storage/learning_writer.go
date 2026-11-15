package storage

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
)

// LearningWriter is the hot-path front of LearningStore. Each Queue* call
// updates the RAM mirror at once, so the very next candidate ranking or
// frecency sort sees the commit, and leaves persistence to a background
// flusher that coalesces repeated words, bigrams and items into one
// transaction per flush.
//
// Why it exists: a native SQLite round trip costs about 12.6ms on Windows
// (FFI plus an fsync per auto-commit). LearningStore.RecordIMECommit and
// RecordItemUse persist synchronously, so calling them from the keyboard
// hook thread (an IME commit), the commit path or the UI thread would stall
// typing and risk the system silently removing the low-level hook
// (LowLevelHooksTimeout). Every Queue* method here returns without I/O and
// holds a mutex only for a map update; the flusher swaps the pending batch
// out under that mutex in O(1).
//
// Trade-off: the mirror is updated before the data is durable. A flush that
// keeps failing drops its batch after flushRetries attempts (counted in
// Stats), so a learned boost may be lost across a restart. That is the
// right side to err on for usage heuristics; typing latency is not.
type LearningWriter struct {
	store *LearningStore
	// persist writes one batch; it is store.persistBatch, swapped in tests
	// to simulate a slow disk.
	persist func(ctx context.Context, b *learningBatch) error

	interval time.Duration
	maxOps   int

	mu      sync.Mutex
	pending *learningBatch

	kick chan struct{}
	stop chan struct{}
	done chan struct{}
	once sync.Once

	flushes atomic.Uint64
	dropped atomic.Uint64
	queued  atomic.Uint64
	lastErr atomic.Value // string
}

// LearningWriterOptions tune the flusher.
type LearningWriterOptions struct {
	// FlushInterval bounds how long a learned commit stays only in RAM
	// (default 750ms).
	FlushInterval time.Duration
	// MaxPendingOps flushes early once this many updates are pending
	// (default 256), bounding memory during a typing burst.
	MaxPendingOps int
}

// LearningWriterStats reports the flusher's health for diagnostics.
type LearningWriterStats struct {
	Queued, Flushes, Dropped uint64
	Pending                  int
	LastError                string
}

const (
	defaultLearningFlushInterval = 750 * time.Millisecond
	defaultLearningMaxOps        = 256
	// maxPendingPrompts caps queued prompt-history rows (each is a full
	// draft/output pair, unlike the coalesced counters).
	maxPendingPrompts = 64
	// flushRetries is how many times a failing batch is retried before it
	// is dropped.
	flushRetries = 3
)

type lexDelta struct {
	count    int64
	lastUsed int64
	pinned   bool
}

type useDelta struct {
	count    int64
	lastUsed int64
}

type promptVersion struct {
	promptID, trigger, rawDraft, finalOutput, preset string
	createdAt                                        int64
}

// learningBatch accumulates coalesced updates between flushes.
type learningBatch struct {
	lex     map[lexKey]lexDelta
	bigrams map[[2]string]lexDelta
	uses    map[string]useDelta
	prompts []promptVersion
	ops     int
	retries int
}

func newLearningBatch() *learningBatch {
	return &learningBatch{
		lex:     make(map[lexKey]lexDelta),
		bigrams: make(map[[2]string]lexDelta),
		uses:    make(map[string]useDelta),
	}
}

func (b *learningBatch) empty() bool {
	return len(b.lex) == 0 && len(b.bigrams) == 0 && len(b.uses) == 0 && len(b.prompts) == 0
}

// merge folds older (a batch whose flush failed) back under b.
func (b *learningBatch) merge(older *learningBatch) {
	for k, d := range older.lex {
		cur := b.lex[k]
		cur.count += d.count
		cur.lastUsed = max(cur.lastUsed, d.lastUsed)
		cur.pinned = cur.pinned || d.pinned
		b.lex[k] = cur
	}
	for k, d := range older.bigrams {
		cur := b.bigrams[k]
		cur.count += d.count
		cur.lastUsed = max(cur.lastUsed, d.lastUsed)
		b.bigrams[k] = cur
	}
	for k, d := range older.uses {
		cur := b.uses[k]
		cur.count += d.count
		cur.lastUsed = max(cur.lastUsed, d.lastUsed)
		b.uses[k] = cur
	}
	b.prompts = append(older.prompts, b.prompts...)
	if extra := len(b.prompts) - maxPendingPrompts; extra > 0 {
		b.prompts = b.prompts[extra:]
	}
	b.ops += older.ops
	b.retries = max(b.retries, older.retries)
}

// NewLearningWriter starts a writer in front of store. Close it to flush
// what is pending and stop the flusher.
func NewLearningWriter(store *LearningStore, opts LearningWriterOptions) *LearningWriter {
	return newLearningWriter(store, opts, store.persistBatch)
}

// newLearningWriter takes the persist function so tests can stand in a slow
// or failing disk before the flusher starts.
func newLearningWriter(store *LearningStore, opts LearningWriterOptions, persist func(context.Context, *learningBatch) error) *LearningWriter {
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = defaultLearningFlushInterval
	}
	if opts.MaxPendingOps <= 0 {
		opts.MaxPendingOps = defaultLearningMaxOps
	}
	w := &LearningWriter{
		store:    store,
		interval: opts.FlushInterval,
		maxOps:   opts.MaxPendingOps,
		pending:  newLearningBatch(),
		kick:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	w.persist = persist
	go w.loop()
	return w
}

// QueueIMECommit records that word was committed for pinyinKey (after
// prevWord, "" at a sentence start). It never blocks on storage: safe on
// the keyboard hook thread.
func (w *LearningWriter) QueueIMECommit(pinyinKey, word, prevWord string, pinned bool) {
	// Same normalization as LearningStore.RecordIMECommit, so the async and
	// the synchronous path persist and mirror identical keys.
	pinyinKey = truncateUTF8(strings.TrimSpace(pinyinKey), MaxMirrorKeyBytes)
	word = truncateUTF8(word, MaxMirrorKeyBytes)
	prevWord = truncateUTF8(prevWord, MaxMirrorKeyBytes)
	if pinyinKey == "" || word == "" {
		return
	}
	now := time.Now().UTC().UnixNano()
	w.store.mirrorIMECommit(pinyinKey, word, prevWord, pinned, now)

	w.mu.Lock()
	k := lexKey{pinyinKey, word, ""}
	d := w.pending.lex[k]
	d.count++
	d.lastUsed = now
	d.pinned = d.pinned || pinned
	w.pending.lex[k] = d
	if prevWord != "" {
		bk := [2]string{prevWord, word}
		bd := w.pending.bigrams[bk]
		bd.count++
		bd.lastUsed = now
		w.pending.bigrams[bk] = bd
	}
	w.pending.ops++
	full := w.pending.ops >= w.maxOps
	w.mu.Unlock()
	w.queued.Add(1)
	if full {
		w.wake()
	}
}

// QueueItemUse records an Omnibox item execution for frecency ranking. It
// never blocks on storage: safe on the UI thread.
func (w *LearningWriter) QueueItemUse(itemID string, now time.Time) {
	itemID = truncateUTF8(itemID, MaxMirrorItemIDBytes)
	if itemID == "" {
		return
	}
	ns := now.UnixNano()
	w.store.mirrorItemUse(itemID, ns)

	w.mu.Lock()
	d := w.pending.uses[itemID]
	d.count++
	d.lastUsed = max(d.lastUsed, ns)
	w.pending.uses[itemID] = d
	w.pending.ops++
	full := w.pending.ops >= w.maxOps
	w.mu.Unlock()
	w.queued.Add(1)
	if full {
		w.wake()
	}
}

// QueuePromptVersion appends a prompt-history row. Prompt history is
// persistence-only (no RAM mirror); at most maxPendingPrompts rows wait,
// the oldest dropping first.
func (w *LearningWriter) QueuePromptVersion(promptID, trigger, rawDraft, finalOutput, preset string) {
	if promptID == "" && finalOutput == "" {
		return
	}
	pv := promptVersion{promptID, trigger, rawDraft, finalOutput, preset, time.Now().UTC().UnixNano()}
	w.mu.Lock()
	w.pending.prompts = append(w.pending.prompts, pv)
	if extra := len(w.pending.prompts) - maxPendingPrompts; extra > 0 {
		w.pending.prompts = w.pending.prompts[extra:]
		w.dropped.Add(uint64(extra))
	}
	w.pending.ops++
	full := w.pending.ops >= w.maxOps
	w.mu.Unlock()
	w.queued.Add(1)
	if full {
		w.wake()
	}
}

func (w *LearningWriter) wake() {
	select {
	case w.kick <- struct{}{}:
	default: // a flush is already requested
	}
}

func (w *LearningWriter) loop() {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
		case <-w.kick:
		}
		_ = w.flushOnce(context.Background())
	}
}

// flushOnce persists what is pending. A failed batch is merged back in
// front of newer updates and retried on the next flush, up to flushRetries.
func (w *LearningWriter) flushOnce(ctx context.Context) error {
	w.mu.Lock()
	batch := w.pending
	if batch.empty() {
		w.mu.Unlock()
		return nil
	}
	w.pending = newLearningBatch()
	w.mu.Unlock()

	err := w.persist(ctx, batch)
	if err == nil {
		w.flushes.Add(1)
		return nil
	}
	w.lastErr.Store(err.Error())
	batch.retries++
	if batch.retries >= flushRetries || errors.Is(err, sqlite.ErrClosed) {
		w.dropped.Add(uint64(batch.ops))
		return err
	}
	w.mu.Lock()
	w.pending.merge(batch)
	w.mu.Unlock()
	return err
}

// Flush synchronously persists what is pending. For shutdown and tests;
// never call it from a hot path.
func (w *LearningWriter) Flush(ctx context.Context) error {
	return w.flushOnce(ctx)
}

// Close stops the flusher and persists what is pending.
func (w *LearningWriter) Close(ctx context.Context) error {
	w.once.Do(func() { close(w.stop) })
	<-w.done
	var err error
	for attempt := 0; attempt < flushRetries; attempt++ {
		if err = w.flushOnce(ctx); err == nil {
			w.mu.Lock()
			empty := w.pending.empty()
			w.mu.Unlock()
			if empty {
				return nil
			}
		}
	}
	return err
}

// Stats reports queue and flush counters for the diagnostics page.
func (w *LearningWriter) Stats() LearningWriterStats {
	w.mu.Lock()
	pending := w.pending.ops
	w.mu.Unlock()
	st := LearningWriterStats{
		Queued:  w.queued.Load(),
		Flushes: w.flushes.Load(),
		Dropped: w.dropped.Load(),
		Pending: pending,
	}
	if s, ok := w.lastErr.Load().(string); ok {
		st.LastError = s
	}
	return st
}

// mirrorIMECommit applies one commit to the RAM mirror (what LookupBoost and
// CustomWordsForPrefix read), honoring the mirror caps exactly as
// RecordIMECommit does.
func (s *LearningStore) mirrorIMECommit(pinyinKey, word, prevWord string, pinned bool, now int64) {
	s.mu.Lock()
	k := lexKey{pinyinKey, word, ""}
	v, exists := s.lex[k]
	if !exists && len(s.lex) >= MaxLexMirrorEntries {
		evictLexEntries(s.lex, MaxLexMirrorEntries)
	}
	v.frequency++
	v.lastUsed = now
	v.pinned = v.pinned || pinned
	s.lex[k] = v
	if prevWord != "" {
		bk := [2]string{prevWord, word}
		b, bexists := s.bigrams[bk]
		if !bexists && len(s.bigrams) >= MaxBigramMirrorEntries {
			evictBigramEntries(s.bigrams, MaxBigramMirrorEntries)
		}
		b.frequency++
		b.lastUsed = now
		s.bigrams[bk] = b
	}
	s.mu.Unlock()
}

// mirrorItemUse applies one item use to the bounded frecency mirror.
func (s *LearningStore) mirrorItemUse(itemID string, ns int64) {
	s.mu.Lock()
	v, exists := s.uses[itemID]
	if !exists && len(s.uses) >= MaxUsesMirrorEntries {
		evictUseEntries(s.uses, MaxUsesMirrorEntries)
	}
	v.lastUsed = max(v.lastUsed, ns)
	v.count++
	s.uses[itemID] = v
	s.mu.Unlock()
}

// persistBatch writes a coalesced batch in one transaction: counters are
// added as deltas, so N commits of one word cost one UPSERT.
func (s *LearningStore) persistBatch(ctx context.Context, b *learningBatch) error {
	stmts := make([]sqlite.Statement, 0, len(b.lex)+len(b.bigrams)+len(b.uses)+len(b.prompts))
	for k, d := range b.lex {
		stmts = append(stmts, sqlite.Statement{
			SQL:  `INSERT INTO ime_user_lexicon(pinyin_key,word,frequency,last_used_ns,pinned) VALUES(?,?,?,?,?) ON CONFLICT(pinyin_key,word) DO UPDATE SET frequency=frequency+excluded.frequency,last_used_ns=MAX(last_used_ns,excluded.last_used_ns),pinned=MAX(pinned,excluded.pinned)`,
			Args: []any{k.pinyin, k.word, d.count, d.lastUsed, boolInt(d.pinned)},
		})
	}
	for k, d := range b.bigrams {
		stmts = append(stmts, sqlite.Statement{
			SQL:  `INSERT INTO ime_bigrams(prev_word,word,transition_count,last_used_ns) VALUES(?,?,?,?) ON CONFLICT(prev_word,word) DO UPDATE SET transition_count=transition_count+excluded.transition_count,last_used_ns=MAX(last_used_ns,excluded.last_used_ns)`,
			Args: []any{k[0], k[1], d.count, d.lastUsed},
		})
	}
	for id, d := range b.uses {
		stmts = append(stmts, sqlite.Statement{
			SQL:  `INSERT INTO usage_frecency(item_id,use_count,last_used_ns) VALUES(?,?,?) ON CONFLICT(item_id) DO UPDATE SET use_count=use_count+excluded.use_count,last_used_ns=MAX(last_used_ns,excluded.last_used_ns)`,
			Args: []any{id, d.count, d.lastUsed},
		})
	}
	for _, p := range b.prompts {
		stmts = append(stmts, sqlite.Statement{
			SQL:  `INSERT INTO prompt_history(prompt_id,trigger,raw_draft,final_output,preset,created_at_ns) VALUES(?,?,?,?,?,?)`,
			Args: []any{p.promptID, p.trigger, p.rawDraft, p.finalOutput, p.preset, p.createdAt},
		})
	}
	if len(stmts) == 0 {
		return nil
	}
	_, err := s.db.Transaction(ctx, stmts)
	return err
}
