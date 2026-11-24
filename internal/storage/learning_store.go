package storage

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/ime"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

const (
	// MaxLexMirrorEntries bounds the in-memory IME unigram mirror.
	MaxLexMirrorEntries = 4096
	// MaxBigramMirrorEntries bounds the in-memory IME bigram mirror.
	MaxBigramMirrorEntries = 4096
	// MaxUsesMirrorEntries bounds the in-memory usage frecency mirror.
	MaxUsesMirrorEntries = 2048

	// MaxMirrorKeyBytes caps per-entry pinyinKey, word, and prevWord byte length in RAM mirrors.
	MaxMirrorKeyBytes = 128
	// MaxMirrorItemIDBytes caps per-entry usage_frecency item_id byte length in RAM mirrors.
	MaxMirrorItemIDBytes = 256

	// DefaultMaxFrecencyRows caps retained rows in the usage_frecency SQLite table on prune.
	DefaultMaxFrecencyRows = 2048
	// DefaultFrecencyRetentionWindow drops usage_frecency rows older than 90 days on prune.
	DefaultFrecencyRetentionWindow = 90 * 24 * time.Hour

	maxCustomPrefixCandidates = 32
	// Do not let a single accidental transition change candidate generation.
	minBigramCandidateCount = 2
)

// LearnedWord is a locally learned IME candidate.
type LearnedWord struct {
	PinyinKey string
	Word      string
	Frequency int64
	Pinned    bool
	LastUsed  time.Time
}

type LexiconStats struct {
	Words          int
	TotalFrequency int64
	PinnedWords    int
}
type PromptHistoryEntry struct {
	ID                                               int64
	PromptID, Trigger, RawDraft, FinalOutput, Preset string
	CreatedAt                                        time.Time
}

// AIUsageRecord is an immutable provider accounting event queued away from the
// typing/request hot path. Cost is intentionally not stored.
type AIUsageRecord struct {
	At                                      time.Time
	Profile, Provider, Model, Action        string
	InputTokens, OutputTokens, CachedTokens int
	Reported                                bool
	Latency                                 time.Duration
}

type AIUsageDay struct {
	Day                                 string
	Requests, InputTokens, OutputTokens int
}

type LexiconHitDay struct {
	Day          string
	Hits, Misses int
}

// LexiconTopWord is a displayable glossary word ranked by cumulative use.
type LexiconTopWord struct {
	Word     string
	UseCount int
}

type lexKey struct{ pinyin, word, prev string }
type lexValue struct {
	frequency int64
	pinned    bool
	lastUsed  int64
}
type useValue struct {
	lastUsed int64
	count    int64
}

// LearningStoreOptions configures optional cold-start snapshot and migration behavior.
type LearningStoreOptions struct {
	// BigramSnapshotPath optionally points to bigram-snapshot-v1.bin for fast
	// cold-start bigram loading and flush/close persistence.
	BigramSnapshotPath string
	// DBPath optionally hints the backing SQLite file path for migration locking/backups.
	DBPath string
}

// LearningStore owns the learning, frecency, and prompt-history tables on a shared SQLite DB.
type LearningStore struct {
	db                       *sqlite.DB
	mu                       sync.RWMutex
	lex                      map[lexKey]lexValue
	lexByInitial             map[byte]map[lexKey]struct{}
	bigrams                  map[[2]string]lexValue
	readings                 map[string]map[string]struct{}
	uses                     map[string]useValue
	bigramSnapshotPath       string
	bigramGen                atomic.Uint64
	flushWatermark           atomic.Uint64
	loadedBigramFromSnapshot atomic.Bool
	lastSnapshotErr          atomic.Value // string
}

// NewLearningStore runs the unified app schema migration and loads the bounded hot mirror.
func NewLearningStore(db *sqlite.DB) (*LearningStore, error) {
	return NewLearningStoreWithOptions(context.Background(), db, LearningStoreOptions{})
}

// NewLearningStoreWithOptions migrates db (if needed) and loads bounded hot mirrors,
// using opts.BigramSnapshotPath as a validated cold-start cache when available.
func NewLearningStoreWithOptions(ctx context.Context, db *sqlite.DB, opts LearningStoreOptions) (*LearningStore, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if db == nil {
		return nil, fmt.Errorf("nil sqlite database")
	}
	s := &LearningStore{
		db:                 db,
		lex:                make(map[lexKey]lexValue),
		bigrams:            make(map[[2]string]lexValue),
		readings:           make(map[string]map[string]struct{}),
		uses:               make(map[string]useValue),
		bigramSnapshotPath: strings.TrimSpace(opts.BigramSnapshotPath),
	}
	if err := s.initSchema(ctx, opts.DBPath); err != nil {
		return nil, err
	}
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// LexiconHitsDaily returns per-day hit/miss counts for the last n local days,
// oldest first, including zero rows for days without activity.
func (s *LearningStore) LexiconHitsDaily(ctx context.Context, n int) ([]LexiconHitDay, error) {
	if n <= 0 {
		return []LexiconHitDay{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	loc := time.Local
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	start := today.AddDate(0, 0, -(n - 1))
	end := today.AddDate(0, 0, 1)
	rows, err := s.db.Query(ctx, `SELECT day,hits,misses FROM lexicon_hits_daily WHERE day>=? AND day<? ORDER BY day`, start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	byDay := make(map[string]LexiconHitDay, len(rows.Values))
	for _, row := range rows.Values {
		if len(row) >= 3 {
			byDay[str(row, 0)] = LexiconHitDay{Day: str(row, 0), Hits: int(int64val(row, 1)), Misses: int(int64val(row, 2))}
		}
	}
	out := make([]LexiconHitDay, n)
	for i := range out {
		day := start.AddDate(0, 0, i).Format("2006-01-02")
		out[i] = byDay[day]
		out[i].Day = day
	}
	return out, nil
}

// AIUsageDaily returns one row per local UTC calendar day, oldest first. Missing
// days are included with zeroes so chart axes remain continuous.
func (s *LearningStore) AIUsageDaily(ctx context.Context, n int) ([]AIUsageDay, error) {
	if n <= 0 {
		return []AIUsageDay{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	end := time.Now().UTC().Truncate(24 * time.Hour)
	start := end.AddDate(0, 0, -(n - 1))
	rows, err := s.db.Query(ctx, `SELECT date(at_ns/1000000000,'unixepoch') AS day, COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0) FROM ai_usage WHERE at_ns>=? AND at_ns<? GROUP BY day ORDER BY day`, start.UnixNano(), end.AddDate(0, 0, 1).UnixNano())
	if err != nil {
		return nil, err
	}
	byDay := make(map[string]AIUsageDay, len(rows.Values))
	for _, row := range rows.Values {
		if len(row) >= 4 {
			byDay[str(row, 0)] = AIUsageDay{Day: str(row, 0), Requests: int(int64val(row, 1)), InputTokens: int(int64val(row, 2)), OutputTokens: int(int64val(row, 3))}
		}
	}
	out := make([]AIUsageDay, n)
	for i := range out {
		day := start.AddDate(0, 0, i).Format("2006-01-02")
		out[i] = byDay[day]
		out[i].Day = day
	}
	return out, nil
}

func (s *LearningStore) initSchema(ctx context.Context, dbPath string) error {
	if _, err := Migrate(ctx, s.db, DBKindApp, MigrationOptions{
		DBPath:      dbPath,
		AllowCreate: true,
	}); err != nil {
		return fmt.Errorf("init learning schema: %w", err)
	}
	return nil
}

func (s *LearningStore) load(ctx context.Context) error {
	lexRows, err := s.db.Query(ctx, `SELECT pinyin_key, word, frequency, last_used_ns, pinned FROM ime_user_lexicon ORDER BY pinned DESC, frequency DESC, last_used_ns DESC LIMIT ?`, MaxLexMirrorEntries)
	if err != nil {
		return err
	}

	var (
		snapBigrams   []BigramSnapshotEntry
		usedSnapshot  bool
		bigramSQLRows sqlite.Rows
	)
	snapPath := s.BigramSnapshotPath()
	if snapPath != "" {
		if snap, ok := s.tryLoadValidatedBigramSnapshot(ctx, snapPath); ok {
			snapBigrams = snap.Entries
			usedSnapshot = true
		}
	}
	if !usedSnapshot {
		bigramSQLRows, err = s.db.Query(ctx, `SELECT prev_word, word, transition_count, last_used_ns FROM ime_bigrams ORDER BY transition_count DESC, last_used_ns DESC LIMIT ?`, MaxBigramMirrorEntries)
		if err != nil {
			return err
		}
	}

	useRows, err := s.db.Query(ctx, `SELECT item_id, use_count, last_used_ns FROM usage_frecency ORDER BY last_used_ns DESC, use_count DESC LIMIT ?`, MaxUsesMirrorEntries)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range lexRows.Values {
		if len(r) >= 5 {
			py := truncateUTF8(str(r, 0), MaxMirrorKeyBytes)
			w := truncateUTF8(str(r, 1), MaxMirrorKeyBytes)
			if py != "" && w != "" {
				s.lex[lexKey{py, w, ""}] = lexValue{int64val(r, 2), int64val(r, 4) != 0, int64val(r, 3)}
				s.indexLexKeyLocked(lexKey{py, w, ""})
			}
		}
	}
	if usedSnapshot {
		for _, e := range snapBigrams {
			pw := truncateUTF8(e.PrevWord, MaxMirrorKeyBytes)
			w := truncateUTF8(e.Word, MaxMirrorKeyBytes)
			if pw == "" || w == "" {
				continue
			}
			s.bigrams[[2]string{pw, w}] = lexValue{
				frequency: clampUint64ToInt64(e.Frequency),
				lastUsed:  e.LastUsedNS,
			}
			if e.Reading != "" && len(s.lex) == 0 {
				if s.readings == nil {
					s.readings = make(map[string]map[string]struct{})
				}
				if s.readings[w] == nil {
					s.readings[w] = make(map[string]struct{})
				}
				s.readings[w][truncateUTF8(e.Reading, MaxMirrorKeyBytes)] = struct{}{}
			}
		}
		s.loadedBigramFromSnapshot.Store(true)
	} else {
		for _, r := range bigramSQLRows.Values {
			if len(r) >= 4 {
				pw := truncateUTF8(str(r, 0), MaxMirrorKeyBytes)
				w := truncateUTF8(str(r, 1), MaxMirrorKeyBytes)
				if pw != "" && w != "" {
					s.bigrams[[2]string{pw, w}] = lexValue{frequency: int64val(r, 2), lastUsed: int64val(r, 3)}
				}
			}
		}
		s.loadedBigramFromSnapshot.Store(false)
	}
	for _, r := range useRows.Values {
		if len(r) >= 3 {
			n := int64val(r, 1)
			id := truncateUTF8(str(r, 0), MaxMirrorItemIDBytes)
			if n > 0 && id != "" {
				s.uses[id] = useValue{lastUsed: int64val(r, 2), count: n}
			}
		}
	}
	return nil
}

func (s *LearningStore) tryLoadValidatedBigramSnapshot(ctx context.Context, path string) (BigramSnapshot, bool) {
	snap, err := ReadBigramSnapshot(path)
	if err != nil {
		s.lastSnapshotErr.Store(err.Error())
		return BigramSnapshot{}, false
	}
	if snap.SourceSchema != AppSchemaVersion {
		s.lastSnapshotErr.Store(fmt.Sprintf("%v: snapshot schema %d != %d", ErrBigramSnapshotStale, snap.SourceSchema, AppSchemaVersion))
		return BigramSnapshot{}, false
	}
	dbGen, err := dbBigramGeneration(ctx, s.db, AppSchemaVersion)
	if err != nil {
		s.lastSnapshotErr.Store(err.Error())
		return BigramSnapshot{}, false
	}
	if snap.Generation != dbGen {
		s.lastSnapshotErr.Store(fmt.Sprintf("%v: snapshot generation %d != db %d", ErrBigramSnapshotStale, snap.Generation, dbGen))
		return BigramSnapshot{}, false
	}
	s.lastSnapshotErr.Store("")
	return snap, true
}

// SetBigramSnapshotPath configures or clears the binary cold-start bigram snapshot path.
func (s *LearningStore) SetBigramSnapshotPath(path string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.bigramSnapshotPath = strings.TrimSpace(path)
	s.mu.Unlock()
}

// BigramSnapshotPath returns the configured binary cold-start bigram snapshot path.
func (s *LearningStore) BigramSnapshotPath() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	p := s.bigramSnapshotPath
	s.mu.RUnlock()
	return p
}

// LoadedBigramFromSnapshot reports whether the initial bigram mirror was populated
// from a validated matching binary snapshot rather than scanning SQLite ime_bigrams.
func (s *LearningStore) LoadedBigramFromSnapshot() bool {
	if s == nil {
		return false
	}
	return s.loadedBigramFromSnapshot.Load()
}

// LastBigramSnapshotError returns the most recent snapshot fallback diagnostic, if any.
func (s *LearningStore) LastBigramSnapshotError() string {
	if s == nil {
		return ""
	}
	if msg, ok := s.lastSnapshotErr.Load().(string); ok {
		return msg
	}
	return ""
}

// BigramGeneration returns a monotonically increasing generation counter for in-memory
// bigram/lexicon mutations so stale background snapshot loads cannot clobber online learning.
func (s *LearningStore) BigramGeneration() uint64 {
	if s == nil {
		return 0
	}
	return s.bigramGen.Load()
}

// FlushWatermark returns the store's durable write watermark for snapshot tagging.
func (s *LearningStore) FlushWatermark() uint64 {
	if s == nil {
		return 0
	}
	return s.flushWatermark.Load()
}

// BuildBigramSnapshot constructs a canonical, bounded BigramSnapshot from SQLite
// (the sole source of truth) enriched with primary reading hints from the lexicon mirror.
func (s *LearningStore) BuildBigramSnapshot(ctx context.Context, flushWatermark uint64) (BigramSnapshot, error) {
	if s == nil {
		return BigramSnapshot{}, fmt.Errorf("nil learning store")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return BigramSnapshot{}, err
	}
	if flushWatermark == 0 {
		flushWatermark = s.flushWatermark.Load()
	}

	// Capture deterministic best reading per word and optional usage count under RLock.
	bestReading := make(map[string]string)
	bestReadingFreq := make(map[string]int64)
	useCounts := make(map[string]uint64)
	var entries []BigramSnapshotEntry
	var gen uint64

	s.mu.RLock()
	for k, v := range s.lex {
		if k.prev != "" || k.word == "" || k.pinyin == "" {
			continue
		}
		cur, exists := bestReading[k.word]
		if !exists || v.frequency > bestReadingFreq[k.word] || (v.frequency == bestReadingFreq[k.word] && k.pinyin < cur) {
			bestReading[k.word] = k.pinyin
			bestReadingFreq[k.word] = v.frequency
		}
	}
	for id, u := range s.uses {
		if u.count > 0 {
			useCounts[id] = uint64(u.count)
		}
	}
	if s.db == nil {
		gen = s.bigramGen.Load()
		for k, v := range s.bigrams {
			if k[0] == "" || k[1] == "" || v.frequency <= 0 {
				continue
			}
			entries = append(entries, BigramSnapshotEntry{
				PrevWord:   k[0],
				Word:       k[1],
				Reading:    bestReading[k[1]],
				Frequency:  uint64(v.frequency),
				LastUsedNS: v.lastUsed,
				UsageCount: useCounts[k[1]],
			})
		}
	}
	s.mu.RUnlock()

	if s.db != nil {
		var err error
		gen, err = dbBigramGeneration(ctx, s.db, AppSchemaVersion)
		if err != nil {
			return BigramSnapshot{}, err
		}
		rows, err := s.db.Query(ctx, `SELECT prev_word, word, transition_count, last_used_ns FROM ime_bigrams WHERE transition_count > 0 ORDER BY transition_count DESC, last_used_ns DESC, prev_word ASC, word ASC LIMIT ?`, MaxBigramSnapshotEntries)
		if err != nil {
			return BigramSnapshot{}, err
		}
		entries = make([]BigramSnapshotEntry, 0, len(rows.Values))
		for _, r := range rows.Values {
			if len(r) < 4 {
				continue
			}
			pw := truncateUTF8(str(r, 0), MaxMirrorKeyBytes)
			w := truncateUTF8(str(r, 1), MaxMirrorKeyBytes)
			freq := int64val(r, 2)
			if pw == "" || w == "" || freq <= 0 {
				continue
			}
			entries = append(entries, BigramSnapshotEntry{
				PrevWord:   pw,
				Word:       w,
				Reading:    bestReading[w],
				Frequency:  uint64(freq),
				LastUsedNS: int64val(r, 3),
				UsageCount: useCounts[w],
			})
		}
	}

	snap := normalizeBigramSnapshot(BigramSnapshot{
		FormatVersion:  BigramSnapshotFormatVersion,
		CreatedAt:      time.Now().UTC(),
		SourceSchema:   AppSchemaVersion,
		Generation:     gen,
		FlushWatermark: flushWatermark,
		Entries:        entries,
	})
	snap.SourceDigest = computeBigramSourceDigest(snap.SourceSchema, snap.Generation, snap.FlushWatermark, snap.Entries)
	return snap, nil
}

// SaveBigramSnapshot builds and atomically persists bigram-snapshot-v1.bin when a path is configured.
func (s *LearningStore) SaveBigramSnapshot(ctx context.Context, flushWatermark uint64) error {
	if s == nil {
		return nil
	}
	path := s.BigramSnapshotPath()
	if path == "" {
		return nil
	}
	snap, err := s.BuildBigramSnapshot(ctx, flushWatermark)
	if err != nil {
		s.lastSnapshotErr.Store(err.Error())
		return err
	}
	if err := WriteBigramSnapshot(path, snap); err != nil {
		s.lastSnapshotErr.Store(err.Error())
		return err
	}
	s.lastSnapshotErr.Store("")
	return nil
}

// FlushBigramSnapshot persists the current bigram snapshot using the store's latest flush watermark.
func (s *LearningStore) FlushBigramSnapshot(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.SaveBigramSnapshot(ctx, s.flushWatermark.Load())
}

// ApplyBigramSnapshotIfCurrent publishes snap into the in-memory bigram mirror only if
// no online learning mutation has occurred since expectedGen was observed.
func (s *LearningStore) ApplyBigramSnapshotIfCurrent(snap BigramSnapshot, expectedGen uint64) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bigramGen.Load() != expectedGen {
		return false
	}
	next := make(map[[2]string]lexValue, min(len(snap.Entries), MaxBigramMirrorEntries))
	for _, e := range snap.Entries {
		if len(next) >= MaxBigramMirrorEntries {
			break
		}
		pw := truncateUTF8(e.PrevWord, MaxMirrorKeyBytes)
		w := truncateUTF8(e.Word, MaxMirrorKeyBytes)
		if pw == "" || w == "" || e.Frequency == 0 {
			continue
		}
		next[[2]string{pw, w}] = lexValue{
			frequency: clampUint64ToInt64(e.Frequency),
			lastUsed:  e.LastUsedNS,
		}
		if e.Reading != "" && len(s.lex) == 0 {
			if s.readings == nil {
				s.readings = make(map[string]map[string]struct{})
			}
			if s.readings[w] == nil {
				s.readings[w] = make(map[string]struct{})
			}
			s.readings[w][truncateUTF8(e.Reading, MaxMirrorKeyBytes)] = struct{}{}
		}
	}
	s.bigrams = next
	s.bigramGen.Add(1)
	return true
}

// Close persists the cold-start bigram snapshot if a snapshot path is configured.
// Because LearningStore borrows a shared *sqlite.DB, Close does not close the DB handle.
func (s *LearningStore) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.BigramSnapshotPath() == "" {
		return nil
	}
	return s.FlushBigramSnapshot(ctx)
}

func clampUint64ToInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

func (s *LearningStore) RecordIMECommit(ctx context.Context, pinyinKey, word, prevWord string, pinned bool) error {
	if s == nil || s.db == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	pinyinKey = truncateUTF8(strings.TrimSpace(pinyinKey), MaxMirrorKeyBytes)
	word = truncateUTF8(word, MaxMirrorKeyBytes)
	prevWord = truncateUTF8(prevWord, MaxMirrorKeyBytes)
	if pinyinKey == "" || word == "" {
		return nil
	}
	now := time.Now().UTC().UnixNano()
	wordUpdate := imeWordCommitStatement(pinyinKey, word, pinned, now)
	if prevWord != "" {
		bigramUpdate := sqlite.Statement{
			SQL:  `INSERT INTO ime_bigrams(prev_word,word,transition_count,last_used_ns) VALUES(?,?,1,?) ON CONFLICT(prev_word,word) DO UPDATE SET transition_count=transition_count+1,last_used_ns=MAX(last_used_ns,excluded.last_used_ns)`,
			Args: []any{prevWord, word, now},
		}
		if _, err := s.db.Transaction(ctx, []sqlite.Statement{wordUpdate, bigramUpdate}); err != nil {
			return err
		}
	} else if _, err := s.db.Execute(ctx, wordUpdate.SQL, wordUpdate.Args...); err != nil {
		return err
	}
	// Update the write-through mirror only after persistence succeeds, so a
	// cancelled or failed write cannot make the hot path observe phantom data.
	s.mirrorIMECommit(pinyinKey, word, prevWord, pinned, now)
	s.bigramGen.Add(1)
	s.flushWatermark.Add(1)
	return nil
}

func imeWordCommitStatement(pinyinKey, word string, pinned bool, now int64) sqlite.Statement {
	return sqlite.Statement{
		SQL:  `INSERT INTO ime_user_lexicon(pinyin_key,word,frequency,last_used_ns,pinned) VALUES(?,?,?,?,?) ON CONFLICT(pinyin_key,word) DO UPDATE SET frequency=frequency+1,last_used_ns=MAX(last_used_ns,excluded.last_used_ns),pinned=MAX(pinned,excluded.pinned)`,
		Args: []any{pinyinKey, word, 1, now, boolInt(pinned)},
	}
}

// LookupBoost is the allocation-free RAM mirror lookup used by the typing hot path.
func (s *LearningStore) LookupBoost(pinyinKey, word, prevWord string) int32 {
	if s == nil || pinyinKey == "" || word == "" {
		return 0
	}
	s.mu.RLock()
	v := s.lex[lexKey{pinyinKey, word, ""}]
	var big lexValue
	if prevWord != "" {
		big = s.bigrams[[2]string{prevWord, word}]
	}
	s.mu.RUnlock()
	boost := v.frequency
	if prevWord != "" {
		boost += big.frequency * 2
	}
	if v.pinned {
		boost += 100
	}
	if boost > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(boost)
}
func (s *LearningStore) LookupIMEBoost(ctx context.Context, pinyinKey, word, prevWord string) (int32, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
	}
	if s == nil {
		return 0, nil
	}
	return s.LookupBoost(pinyinKey, word, prevWord), nil
}
func (s *LearningStore) CustomWordsForPrefix(prefix string) []LearnedWord {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []LearnedWord{}
	for k, v := range s.lex {
		if k.prev == "" && strings.HasPrefix(k.pinyin, prefix) {
			out = append(out, LearnedWord{k.pinyin, k.word, v.frequency, v.pinned, time.Unix(0, v.lastUsed).UTC()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Frequency > out[j].Frequency })
	return out
}

// BigramWordsForPrefix returns learned continuations from the bounded RAM
// mirror. The returned candidates carry their compact reading so the engine can
// verify the requested prefix and account for consumed syllables without any
// database access on the typing path.
func (s *LearningStore) BigramWordsForPrefix(prevWord, syllablePrefix string) []ime.UserCandidate {
	if s == nil || prevWord == "" || syllablePrefix == "" {
		return nil
	}
	syllablePrefix = strings.ToLower(strings.TrimSpace(syllablePrefix))
	if syllablePrefix == "" {
		return nil
	}
	var prefixSegs []string
	s.mu.RLock()
	byWord := make(map[string]ime.UserCandidate, 8)
	for key, value := range s.bigrams {
		if key[0] != prevWord || value.frequency < minBigramCandidateCount {
			continue
		}
		for reading := range s.readings[key[1]] {
			if !matchesBigramReadingPrefix(reading, syllablePrefix, &prefixSegs) {
				continue
			}
			frequency := value.frequency
			if frequency > math.MaxInt32 {
				frequency = math.MaxInt32
			}
			candidate := ime.UserCandidate{PinyinKey: reading, Word: key[1], Frequency: int32(frequency)}
			if prior, ok := byWord[candidate.Word]; !ok || betterBigramCandidate(candidate, prior, syllablePrefix) {
				byWord[candidate.Word] = candidate
			}
		}
	}
	s.mu.RUnlock()
	out := make([]ime.UserCandidate, 0, len(byWord))
	for _, candidate := range byWord {
		out = append(out, candidate)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Frequency != out[j].Frequency {
			return out[i].Frequency > out[j].Frequency
		}
		return out[i].Word < out[j].Word
	})
	return out
}

func matchesBigramReadingPrefix(reading, syllablePrefix string, prefixSegs *[]string) bool {
	if reading == "" || syllablePrefix == "" {
		return false
	}
	if strings.HasPrefix(reading, syllablePrefix) {
		return true
	}
	if reading[0] != syllablePrefix[0] || len(syllablePrefix) < 2 {
		return false
	}
	if *prefixSegs == nil {
		*prefixSegs = inputengine.Segment(syllablePrefix)
	}
	psegs := *prefixSegs
	if len(psegs) == 0 {
		return false
	}
	rsegs := inputengine.Segment(strings.ReplaceAll(reading, " ", "'"))
	if len(rsegs) < len(psegs) {
		return false
	}
	for i, seg := range psegs {
		if !strings.HasPrefix(rsegs[i], seg) {
			return false
		}
	}
	return true
}

func betterBigramCandidate(a, b ime.UserCandidate, syllablePrefix string) bool {
	if a.Frequency != b.Frequency {
		return a.Frequency > b.Frequency
	}
	aExact := a.PinyinKey == syllablePrefix
	bExact := b.PinyinKey == syllablePrefix
	if aExact != bExact {
		return aExact
	}
	return a.PinyinKey < b.PinyinKey
}

func (s *LearningStore) indexLexKeyLocked(k lexKey) {
	if s.readings == nil {
		s.readings = make(map[string]map[string]struct{})
	}
	if s.readings[k.word] == nil {
		s.readings[k.word] = make(map[string]struct{})
	}
	s.readings[k.word][k.pinyin] = struct{}{}
	if s.lexByInitial == nil {
		s.lexByInitial = make(map[byte]map[lexKey]struct{})
	}
	initial := k.pinyin[0]
	if s.lexByInitial[initial] == nil {
		s.lexByInitial[initial] = make(map[lexKey]struct{})
	}
	s.lexByInitial[initial][k] = struct{}{}
}

func (s *LearningStore) deleteLexKeyLocked(k lexKey) {
	delete(s.lex, k)
	readings := s.readings[k.word]
	delete(readings, k.pinyin)
	if len(readings) == 0 {
		delete(s.readings, k.word)
	}
	if k.pinyin != "" {
		delete(s.lexByInitial[k.pinyin[0]], k)
	}
}

func (s *LearningStore) DeleteLearnedWord(ctx context.Context, pinyinKey, word string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.db.Execute(ctx, `DELETE FROM ime_user_lexicon WHERE pinyin_key=? AND word=?`, pinyinKey, word); err != nil {
		return err
	}
	s.mu.Lock()
	s.deleteLexKeyLocked(lexKey{pinyinKey, word, ""})
	s.mu.Unlock()
	s.bigramGen.Add(1)
	return nil
}

// UnpinIMEWord clears the pinned flag for every reading of word while retaining
// its learned frequency and recency.
func (s *LearningStore) UnpinIMEWord(ctx context.Context, word string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.db.Execute(ctx, `UPDATE ime_user_lexicon SET pinned=0 WHERE word=?`, word); err != nil {
		return err
	}
	s.mu.Lock()
	for k, v := range s.lex {
		if k.word == word {
			v.pinned = false
			s.lex[k] = v
		}
	}
	s.mu.Unlock()
	return nil
}

// PinnedIMEWords lists the words currently pinned to the top of the candidate list. The settings
// page uses it to unpin words the user removed from the config: without that, a removed word
// keeps outranking everything for its pinyin until the database is wiped.
func (s *LearningStore) PinnedIMEWords(ctx context.Context) []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for k, v := range s.lex {
		if v.pinned {
			out = append(out, k.word)
		}
	}
	return out
}

func (s *LearningStore) Stats(ctx context.Context) (LexiconStats, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return LexiconStats{}, err
		}
	}
	if s == nil {
		return LexiconStats{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out LexiconStats
	for k, v := range s.lex {
		if k.prev == "" {
			out.Words++
			out.TotalFrequency += v.frequency
			if v.pinned {
				out.PinnedWords++
			}
		}
	}
	return out, nil
}

func (s *LearningStore) customUserCandidatesForPrefix(prefix string) []ime.UserCandidate {
	if s == nil || prefix == "" {
		return nil
	}
	s.mu.RLock()
	var byWord map[string]int32
	for k := range s.lexByInitial[prefix[0]] {
		if strings.HasPrefix(k.pinyin, prefix) {
			v := s.lex[k]
			if byWord == nil {
				byWord = make(map[string]int32, 8)
			}
			freq := v.frequency
			if v.pinned {
				freq += 100
			}
			if freq > math.MaxInt32 {
				freq = math.MaxInt32
			}
			f32 := int32(freq)
			if cur, ok := byWord[k.word]; !ok || f32 > cur {
				byWord[k.word] = f32
			}
		}
	}
	s.mu.RUnlock()
	if len(byWord) == 0 {
		return nil
	}
	out := make([]ime.UserCandidate, 0, len(byWord))
	for word, freq := range byWord {
		out = append(out, ime.UserCandidate{Word: word, Frequency: freq})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Frequency == out[j].Frequency {
			return out[i].Word < out[j].Word
		}
		return out[i].Frequency > out[j].Frequency
	})
	if len(out) > maxCustomPrefixCandidates {
		out = out[:maxCustomPrefixCandidates]
	}
	return out
}

// LexiconTopWords returns the most-used stored glossary words. Unknown item IDs
// are skipped rather than displayed as internal identifiers; ties are stable.
func (s *LearningStore) LexiconTopWords(ctx context.Context, n int) ([]LexiconTopWord, error) {
	if n <= 0 {
		return []LexiconTopWord{}, nil
	}
	if s == nil || s.db == nil {
		return []LexiconTopWord{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT item_id, use_count FROM usage_frecency WHERE use_count > 0 ORDER BY use_count DESC, last_used_ns DESC, item_id ASC LIMIT ?`, MaxUsesMirrorEntries)
	if err != nil {
		return nil, err
	}
	out := make([]LexiconTopWord, 0, min(n, len(rows.Values)))
	for _, row := range rows.Values {
		if len(row) < 2 {
			continue
		}
		word, ok := glossaryWordFromItemID(str(row, 0))
		if !ok {
			continue
		}
		count := int(int64val(row, 1))
		if count <= 0 {
			continue
		}
		out = append(out, LexiconTopWord{Word: word, UseCount: count})
		if len(out) == n {
			break
		}
	}
	return out, nil
}

func glossaryWordFromItemID(itemID string) (string, bool) {
	parts := strings.SplitN(itemID, ":", 3)
	if len(parts) != 3 || parts[0] != "glossary" || strings.TrimSpace(parts[2]) == "" {
		return "", false
	}
	return parts[2], true
}

func (s *LearningStore) RecordItemUse(ctx context.Context, itemID string, now time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	itemID = truncateUTF8(itemID, MaxMirrorItemIDBytes)
	if itemID == "" {
		return nil
	}
	ns := now.UnixNano()
	if _, err := s.db.Execute(ctx, `INSERT INTO usage_frecency(item_id,use_count,last_used_ns) VALUES(?,?,?) ON CONFLICT(item_id) DO UPDATE SET use_count=use_count+1,last_used_ns=excluded.last_used_ns`, itemID, 1, ns); err != nil {
		return err
	}
	s.mu.Lock()
	v, exists := s.uses[itemID]
	if !exists && len(s.uses) >= MaxUsesMirrorEntries {
		evictUseEntries(s.uses, MaxUsesMirrorEntries)
	}
	v.lastUsed = ns
	v.count++
	s.uses[itemID] = v
	s.mu.Unlock()
	return nil
}

// FrecencySnapshot returns half-life-decayed use scores from the hot mirror.
func (s *LearningStore) FrecencySnapshot(now time.Time) map[string]int {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int, len(s.uses))
	for id, v := range s.uses {
		if v.count <= 0 {
			continue
		}
		age := now.UnixNano() - v.lastUsed
		score := float64(v.count) * math.Pow(0.5, float64(age)/float64(72*time.Hour))
		if score > 0 {
			out[id] = int(math.Round(score))
		}
	}
	return out
}

// PruneFrecency drops usage_frecency rows older than maxAge (when > 0) and retains at most
// maxEntries highest-recency/frequency rows, keeping the RAM uses mirror in sync.
func (s *LearningStore) PruneFrecency(ctx context.Context, maxEntries int, maxAge time.Duration) error {
	if s == nil || s.db == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if maxEntries < 0 {
		return fmt.Errorf("maxEntries must be non-negative")
	}
	var cutoffNs int64
	if maxAge > 0 {
		cutoffNs = time.Now().UTC().Add(-maxAge).UnixNano()
		if _, err := s.db.Execute(ctx, `DELETE FROM usage_frecency WHERE last_used_ns < ?`, cutoffNs); err != nil {
			return err
		}
	}
	if _, err := s.db.Execute(ctx, `DELETE FROM usage_frecency WHERE item_id NOT IN (SELECT item_id FROM usage_frecency ORDER BY last_used_ns DESC, use_count DESC, item_id ASC LIMIT ?)`, maxEntries); err != nil {
		return err
	}

	s.mu.Lock()
	if maxAge > 0 {
		for k, v := range s.uses {
			if v.lastUsed < cutoffNs {
				delete(s.uses, k)
			}
		}
	}
	if len(s.uses) > maxEntries {
		trimUsesTo(s.uses, maxEntries)
	}
	s.mu.Unlock()
	return nil
}

// MirrorCounts reports the current entry counts in the bounded RAM mirrors (for diagnostics/tests).
func (s *LearningStore) MirrorCounts() (lexCount, bigramCount, usesCount int) {
	if s == nil {
		return 0, 0, 0
	}
	s.mu.RLock()
	lexCount = len(s.lex)
	bigramCount = len(s.bigrams)
	usesCount = len(s.uses)
	s.mu.RUnlock()
	return lexCount, bigramCount, usesCount
}
func (s *LearningStore) RecordPromptVersion(ctx context.Context, promptID, trigger, rawDraft, finalOutput, preset string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.db.Execute(ctx, `INSERT INTO prompt_history(prompt_id,trigger,raw_draft,final_output,preset,created_at_ns) VALUES(?,?,?,?,?,?)`, promptID, trigger, rawDraft, finalOutput, preset, time.Now().UTC().UnixNano())
	return err
}
func (s *LearningStore) RecentPromptHistory(ctx context.Context, promptID string, limit int) ([]PromptHistoryEntry, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q := `SELECT id,prompt_id,trigger,raw_draft,final_output,preset,created_at_ns FROM prompt_history WHERE prompt_id=? ORDER BY created_at_ns DESC,id DESC`
	args := []any{promptID}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	out := make([]PromptHistoryEntry, 0, len(rows.Values))
	for _, r := range rows.Values {
		if len(r) >= 7 {
			out = append(out, PromptHistoryEntry{int64val(r, 0), str(r, 1), str(r, 2), str(r, 3), str(r, 4), str(r, 5), time.Unix(0, int64val(r, 6)).UTC()})
		}
	}
	return out, nil
}
func (s *LearningStore) PruneHistory(ctx context.Context, maxEntries int) error {
	if s == nil || s.db == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if maxEntries < 0 {
		return fmt.Errorf("maxEntries must be non-negative")
	}
	_, err := s.db.Execute(ctx, `DELETE FROM prompt_history WHERE id NOT IN (SELECT id FROM prompt_history ORDER BY created_at_ns DESC,id DESC LIMIT ?)`, maxEntries)
	return err
}

func str(r []any, i int) string {
	if i < len(r) {
		v, _ := r[i].(string)
		return v
	}
	return ""
}
func int64val(r []any, i int) int64 {
	if i >= len(r) {
		return 0
	}
	switch v := r[i].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func truncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func evictTarget(capLimit int) int {
	batch := capLimit / 10
	if batch < 1 {
		batch = 1
	}
	target := capLimit - batch
	if target < 0 {
		return 0
	}
	return target
}

func (s *LearningStore) evictLexEntriesLocked(capLimit int) {
	m := s.lex
	target := evictTarget(capLimit)
	if len(m) <= target {
		return
	}
	type item struct {
		k lexKey
		v lexValue
	}
	items := make([]item, 0, len(m))
	for k, v := range m {
		items = append(items, item{k, v})
	}
	// Ascending priority: non-pinned first, lower frequency first, older lastUsed first.
	sort.Slice(items, func(i, j int) bool {
		if items[i].v.pinned != items[j].v.pinned {
			return !items[i].v.pinned
		}
		if items[i].v.frequency != items[j].v.frequency {
			return items[i].v.frequency < items[j].v.frequency
		}
		return items[i].v.lastUsed < items[j].v.lastUsed
	})
	drop := len(items) - target
	for i := 0; i < drop; i++ {
		s.deleteLexKeyLocked(items[i].k)
	}
}

func evictBigramEntries(m map[[2]string]lexValue, capLimit int) {
	target := evictTarget(capLimit)
	if len(m) <= target {
		return
	}
	type item struct {
		k [2]string
		v lexValue
	}
	items := make([]item, 0, len(m))
	for k, v := range m {
		items = append(items, item{k, v})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v.frequency != items[j].v.frequency {
			return items[i].v.frequency < items[j].v.frequency
		}
		return items[i].v.lastUsed < items[j].v.lastUsed
	})
	drop := len(items) - target
	for i := 0; i < drop; i++ {
		delete(m, items[i].k)
	}
}

func evictUseEntries(m map[string]useValue, capLimit int) {
	trimUsesTo(m, evictTarget(capLimit))
}

func trimUsesTo(m map[string]useValue, target int) {
	if target < 0 {
		target = 0
	}
	if len(m) <= target {
		return
	}
	if target == 0 {
		clear(m)
		return
	}
	type item struct {
		k string
		v useValue
	}
	items := make([]item, 0, len(m))
	for k, v := range m {
		items = append(items, item{k, v})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v.count != items[j].v.count {
			return items[i].v.count < items[j].v.count
		}
		return items[i].v.lastUsed < items[j].v.lastUsed
	})
	drop := len(items) - target
	for i := 0; i < drop; i++ {
		delete(m, items[i].k)
	}
}
