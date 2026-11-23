package storage

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/ime"
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

// LearningStore owns the learning, frecency, and prompt-history tables on a shared SQLite DB.
type LearningStore struct {
	db           *sqlite.DB
	mu           sync.RWMutex
	lex          map[lexKey]lexValue
	lexByInitial map[byte]map[lexKey]struct{}
	bigrams      map[[2]string]lexValue
	readings     map[string]map[string]struct{}
	uses         map[string]useValue
}

// NewLearningStore creates learning tables and loads their bounded hot mirror.
func NewLearningStore(db *sqlite.DB) (*LearningStore, error) {
	if db == nil {
		return nil, fmt.Errorf("nil sqlite database")
	}
	s := &LearningStore{db: db, lex: make(map[lexKey]lexValue), bigrams: make(map[[2]string]lexValue), readings: make(map[string]map[string]struct{}), uses: make(map[string]useValue)}
	if err := s.initSchema(context.Background()); err != nil {
		return nil, err
	}
	if err := s.load(context.Background()); err != nil {
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

func (s *LearningStore) initSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS ime_user_lexicon (pinyin_key TEXT NOT NULL, word TEXT NOT NULL, frequency INTEGER NOT NULL DEFAULT 0, last_used_ns INTEGER NOT NULL DEFAULT 0, pinned INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (pinyin_key, word))`,
		`CREATE TABLE IF NOT EXISTS ime_bigrams (prev_word TEXT NOT NULL, word TEXT NOT NULL, transition_count INTEGER NOT NULL DEFAULT 0, last_used_ns INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (prev_word, word))`,
		`CREATE INDEX IF NOT EXISTS idx_ime_lexicon_prefix ON ime_user_lexicon(pinyin_key)`,
		`CREATE TABLE IF NOT EXISTS usage_frecency (item_id TEXT PRIMARY KEY, use_count INTEGER NOT NULL DEFAULT 0, last_used_ns INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS prompt_history (id INTEGER PRIMARY KEY AUTOINCREMENT, prompt_id TEXT NOT NULL, trigger TEXT NOT NULL, raw_draft TEXT NOT NULL, final_output TEXT NOT NULL, preset TEXT NOT NULL, created_at_ns INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_prompt_history_recent ON prompt_history(prompt_id, created_at_ns DESC, id DESC)`,
		`CREATE TABLE IF NOT EXISTS lexicon_hits_daily (day TEXT NOT NULL, hits INTEGER NOT NULL DEFAULT 0, misses INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(day))`,
		`CREATE TABLE IF NOT EXISTS ai_usage (id INTEGER PRIMARY KEY AUTOINCREMENT, at_ns INTEGER NOT NULL, profile TEXT NOT NULL, provider TEXT NOT NULL, model TEXT NOT NULL, action TEXT NOT NULL, input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL, cached_tokens INTEGER NOT NULL, reported INTEGER NOT NULL, latency_ms INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS ai_usage_at ON ai_usage(at_ns)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Execute(ctx, stmt); err != nil {
			return fmt.Errorf("init learning schema: %w", err)
		}
	}
	return nil
}

func (s *LearningStore) load(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `SELECT pinyin_key, word, frequency, last_used_ns, pinned FROM ime_user_lexicon ORDER BY pinned DESC, frequency DESC, last_used_ns DESC LIMIT ?`, MaxLexMirrorEntries)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range rows.Values {
		if len(r) >= 5 {
			py := truncateUTF8(str(r, 0), MaxMirrorKeyBytes)
			w := truncateUTF8(str(r, 1), MaxMirrorKeyBytes)
			if py != "" && w != "" {
				s.lex[lexKey{py, w, ""}] = lexValue{int64val(r, 2), int64val(r, 4) != 0, int64val(r, 3)}
				s.indexLexKeyLocked(lexKey{py, w, ""})
			}
		}
	}
	rows, err = s.db.Query(ctx, `SELECT prev_word, word, transition_count, last_used_ns FROM ime_bigrams ORDER BY transition_count DESC, last_used_ns DESC LIMIT ?`, MaxBigramMirrorEntries)
	if err != nil {
		return err
	}
	for _, r := range rows.Values {
		if len(r) >= 4 {
			pw := truncateUTF8(str(r, 0), MaxMirrorKeyBytes)
			w := truncateUTF8(str(r, 1), MaxMirrorKeyBytes)
			if pw != "" && w != "" {
				s.bigrams[[2]string{pw, w}] = lexValue{frequency: int64val(r, 2), lastUsed: int64val(r, 3)}
			}
		}
	}
	rows, err = s.db.Query(ctx, `SELECT item_id, use_count, last_used_ns FROM usage_frecency ORDER BY last_used_ns DESC, use_count DESC LIMIT ?`, MaxUsesMirrorEntries)
	if err != nil {
		return err
	}
	for _, r := range rows.Values {
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
	s.mu.RLock()
	byWord := make(map[string]ime.UserCandidate, 8)
	for key, value := range s.bigrams {
		if key[0] != prevWord || value.frequency < minBigramCandidateCount {
			continue
		}
		for reading := range s.readings[key[1]] {
			// The learned lexicon stores the complete compact reading (for example
			// women); the current composition is the continuation suffix (men).
			if !strings.HasSuffix(reading, syllablePrefix) {
				continue
			}
			frequency := value.frequency
			if frequency > math.MaxInt32 {
				frequency = math.MaxInt32
			}
			candidate := ime.UserCandidate{PinyinKey: reading, Word: key[1], Frequency: int32(frequency)}
			if prior, ok := byWord[candidate.Word]; !ok || candidate.Frequency > prior.Frequency {
				byWord[candidate.Word] = candidate
			}
		}
	}
	s.mu.RUnlock()
	out := make([]ime.UserCandidate, 0, len(byWord))
	for _, candidate := range byWord {
		out = append(out, candidate)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Frequency > out[j].Frequency })
	return out
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
