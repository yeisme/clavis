package storage

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
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

type lexKey struct{ pinyin, word, prev string }
type lexValue struct {
	frequency int64
	pinned    bool
	lastUsed  int64
}

// LearningStore owns the learning, frecency, and prompt-history tables on a shared SQLite DB.
type LearningStore struct {
	db      *sqlite.DB
	mu      sync.RWMutex
	lex     map[lexKey]lexValue
	bigrams map[[2]string]lexValue
	uses    map[string][]int64
}

// NewLearningStore creates learning tables and loads their bounded hot mirror.
func NewLearningStore(db *sqlite.DB) (*LearningStore, error) {
	if db == nil {
		return nil, fmt.Errorf("nil sqlite database")
	}
	s := &LearningStore{db: db, lex: make(map[lexKey]lexValue), bigrams: make(map[[2]string]lexValue), uses: make(map[string][]int64)}
	if err := s.initSchema(context.Background()); err != nil {
		return nil, err
	}
	if err := s.load(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *LearningStore) initSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS ime_user_lexicon (pinyin_key TEXT NOT NULL, word TEXT NOT NULL, frequency INTEGER NOT NULL DEFAULT 0, last_used_ns INTEGER NOT NULL DEFAULT 0, pinned INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (pinyin_key, word))`,
		`CREATE TABLE IF NOT EXISTS ime_bigrams (prev_word TEXT NOT NULL, word TEXT NOT NULL, transition_count INTEGER NOT NULL DEFAULT 0, last_used_ns INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (prev_word, word))`,
		`CREATE INDEX IF NOT EXISTS idx_ime_lexicon_prefix ON ime_user_lexicon(pinyin_key)`,
		`CREATE TABLE IF NOT EXISTS usage_frecency (item_id TEXT PRIMARY KEY, use_count INTEGER NOT NULL DEFAULT 0, last_used_ns INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS prompt_history (id INTEGER PRIMARY KEY AUTOINCREMENT, prompt_id TEXT NOT NULL, trigger TEXT NOT NULL, raw_draft TEXT NOT NULL, final_output TEXT NOT NULL, preset TEXT NOT NULL, created_at_ns INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_prompt_history_recent ON prompt_history(prompt_id, created_at_ns DESC, id DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Execute(ctx, stmt); err != nil {
			return fmt.Errorf("init learning schema: %w", err)
		}
	}
	return nil
}

func (s *LearningStore) load(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `SELECT pinyin_key, word, frequency, last_used_ns, pinned FROM ime_user_lexicon`)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range rows.Values {
		if len(r) >= 5 {
			s.lex[lexKey{str(r, 0), str(r, 1), ""}] = lexValue{int64val(r, 2), int64val(r, 4) != 0, int64val(r, 3)}
		}
	}
	rows, err = s.db.Query(ctx, `SELECT prev_word, word, transition_count, last_used_ns FROM ime_bigrams`)
	if err != nil {
		return err
	}
	for _, r := range rows.Values {
		if len(r) >= 4 {
			s.bigrams[[2]string{str(r, 0), str(r, 1)}] = lexValue{frequency: int64val(r, 2), lastUsed: int64val(r, 3)}
		}
	}
	rows, err = s.db.Query(ctx, `SELECT item_id, use_count, last_used_ns FROM usage_frecency`)
	if err != nil {
		return err
	}
	for _, r := range rows.Values {
		if len(r) >= 3 {
			n := int(int64val(r, 1))
			if n > 0 {
				s.uses[str(r, 0)] = []int64{int64val(r, 2), int64(n)}
			}
		}
	}
	return nil
}

func (s *LearningStore) RecordIMECommit(ctx context.Context, pinyinKey, word, prevWord string, pinned bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now().UTC().UnixNano()
	if _, err := s.db.Execute(ctx, `INSERT INTO ime_user_lexicon(pinyin_key,word,frequency,last_used_ns,pinned) VALUES(?,?,?,?,?) ON CONFLICT(pinyin_key,word) DO UPDATE SET frequency=frequency+1,last_used_ns=excluded.last_used_ns,pinned=MAX(pinned,excluded.pinned)`, pinyinKey, word, 1, now, boolInt(pinned)); err != nil {
		return err
	}
	if prevWord != "" {
		if _, err := s.db.Execute(ctx, `INSERT INTO ime_bigrams(prev_word,word,transition_count,last_used_ns) VALUES(?,?,1,?) ON CONFLICT(prev_word,word) DO UPDATE SET transition_count=transition_count+1,last_used_ns=excluded.last_used_ns`, prevWord, word, now); err != nil {
			return err
		}
	}
	// Update the write-through mirror only after persistence succeeds, so a
	// cancelled or failed write cannot make the hot path observe phantom data.
	s.mu.Lock()
	v := s.lex[lexKey{pinyinKey, word, ""}]
	v.frequency++
	v.lastUsed = now
	v.pinned = v.pinned || pinned
	s.lex[lexKey{pinyinKey, word, ""}] = v
	if prevWord != "" {
		b := s.bigrams[[2]string{prevWord, word}]
		b.frequency++
		b.lastUsed = now
		s.bigrams[[2]string{prevWord, word}] = b
	}
	s.mu.Unlock()
	return nil
}

// LookupBoost is the allocation-free RAM mirror lookup used by the typing hot path.
func (s *LearningStore) LookupBoost(pinyinKey, word, prevWord string) int32 {
	s.mu.RLock()
	v := s.lex[lexKey{pinyinKey, word, ""}]
	big := s.bigrams[[2]string{prevWord, word}]
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
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return s.LookupBoost(pinyinKey, word, prevWord), nil
}
func (s *LearningStore) CustomWordsForPrefix(prefix string) []LearnedWord {
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
func (s *LearningStore) DeleteLearnedWord(ctx context.Context, pinyinKey, word string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.db.Execute(ctx, `DELETE FROM ime_user_lexicon WHERE pinyin_key=? AND word=?`, pinyinKey, word); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.lex, lexKey{pinyinKey, word, ""})
	s.mu.Unlock()
	return nil
}
func (s *LearningStore) Stats(ctx context.Context) (LexiconStats, error) {
	if err := ctx.Err(); err != nil {
		return LexiconStats{}, err
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

func (s *LearningStore) RecordItemUse(ctx context.Context, itemID string, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ns := now.UnixNano()
	if _, err := s.db.Execute(ctx, `INSERT INTO usage_frecency(item_id,use_count,last_used_ns) VALUES(?,?,?) ON CONFLICT(item_id) DO UPDATE SET use_count=use_count+1,last_used_ns=excluded.last_used_ns`, itemID, 1, ns); err != nil {
		return err
	}
	s.mu.Lock()
	v := s.uses[itemID]
	if len(v) == 0 {
		v = []int64{ns, 1}
	} else {
		v[0] = ns
		v[1]++
	}
	s.uses[itemID] = v
	s.mu.Unlock()
	return nil
}

// FrecencySnapshot returns half-life-decayed use scores from the hot mirror.
func (s *LearningStore) FrecencySnapshot(now time.Time) map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int, len(s.uses))
	for id, v := range s.uses {
		if len(v) < 2 {
			continue
		}
		age := now.UnixNano() - v[0]
		score := float64(v[1]) * math.Pow(0.5, float64(age)/float64(72*time.Hour))
		if score > 0 {
			out[id] = int(math.Round(score))
		}
	}
	return out
}
func (s *LearningStore) RecordPromptVersion(ctx context.Context, promptID, trigger, rawDraft, finalOutput, preset string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.db.Execute(ctx, `INSERT INTO prompt_history(prompt_id,trigger,raw_draft,final_output,preset,created_at_ns) VALUES(?,?,?,?,?,?)`, promptID, trigger, rawDraft, finalOutput, preset, time.Now().UTC().UnixNano())
	return err
}
func (s *LearningStore) RecentPromptHistory(ctx context.Context, promptID string, limit int) ([]PromptHistoryEntry, error) {
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
