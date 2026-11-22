package storage

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

func TestAIUsageDailyIncludesZeroDaysAndAggregates(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:", sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewLearningStore(db)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().Truncate(24 * time.Hour)
	writer := NewLearningWriter(store, LearningWriterOptions{FlushInterval: time.Hour})
	writer.QueueAIUsage(AIUsageRecord{At: day.Add(-72*time.Hour + time.Hour), Profile: "p", Provider: "x", Model: "m", Action: "polish", InputTokens: 10, OutputTokens: 3, Reported: true})
	writer.QueueAIUsage(AIUsageRecord{At: day.Add(-24*time.Hour + time.Hour), Profile: "p", Provider: "x", Model: "m", Action: "polish", InputTokens: 4, OutputTokens: 2, Reported: true})
	if err := writer.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(ctx, `SELECT COUNT(*) FROM ai_usage`)
	if err != nil || len(rows.Values) == 0 || int64val(rows.Values[0], 0) != 2 {
		t.Fatalf("ai_usage rows=%+v err=%v", rows.Values, err)
	}
	days, err := store.AIUsageDaily(ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 4 || days[0].Requests != 1 || days[0].InputTokens != 10 || days[1].Requests != 0 || days[2].Requests != 1 || days[2].OutputTokens != 2 || days[3].Requests != 0 {
		t.Fatalf("days=%+v", days)
	}
}

func TestLearningStoreRoundTripAndFrecency(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:", sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := NewLearningStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordIMECommit(ctx, "ni", "你", "好", true); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordIMECommit(ctx, "ni", "你", "好", false); err != nil {
		t.Fatal(err)
	}
	if got := s.LookupBoost("ni", "你", "好"); got != 106 {
		t.Fatalf("boost=%d", got)
	}
	words := s.CustomWordsForPrefix("n")
	if len(words) != 1 || !words[0].Pinned {
		t.Fatalf("words=%+v", words)
	}
	stats, err := s.Stats(ctx)
	if err != nil || stats.Words != 1 || stats.TotalFrequency != 2 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	base := time.Now().Add(-72 * time.Hour)
	if err := s.RecordItemUse(ctx, "clip", base); err != nil {
		t.Fatal(err)
	}
	got := s.FrecencySnapshot(base.Add(72 * time.Hour))
	if got["clip"] != 1 {
		t.Fatalf("frecency=%v", got)
	}
	if math.Abs(float64(got["clip"])-1) > 0 {
		t.Fatal(got)
	}
	if err := s.RecordPromptVersion(ctx, "p", "hotkey", "draft", "output", "default"); err != nil {
		t.Fatal(err)
	}
	history, err := s.RecentPromptHistory(ctx, "p", 1)
	if err != nil || len(history) != 1 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if err := s.DeleteLearnedWord(ctx, "ni", "你"); err != nil {
		t.Fatal(err)
	}
}

func TestLearningStoreBigramWordsForPrefixUsesRAMMirror(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:", sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewLearningStore(db)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := store.RecordIMECommit(ctx, "women", "我们", "我", false); err != nil {
			t.Fatal(err)
		}
	}
	adapter := NewUserFrequencyAdapter(store)
	got := adapter.BigramWordsForPrefix("我", "men")
	if len(got) != 1 || got[0].Word != "我们" || got[0].Frequency != 2 {
		t.Fatalf("bigram candidates = %+v", got)
	}
	if got := adapter.BigramWordsForPrefix("你", "men"); len(got) != 0 {
		t.Fatalf("wrong previous word returned candidates: %+v", got)
	}
	eng := inputengine.New(inputengine.Options{CandidateCount: 7, UserFrequency: adapter})
	typeEngineLetters(eng, "wo")
	previous := eng.View()
	previousIndex := -1
	for i, candidate := range previous.Candidates {
		if candidate.Text == "我" {
			previousIndex = i
			break
		}
	}
	if previousIndex < 0 {
		t.Fatalf("previous-word candidate missing: %+v", previous.Candidates)
	}
	if r := eng.HandleKey(inputengine.Key{Kind: inputengine.KeyDigit, Rune: rune('1' + previousIndex)}); r.Commit != "我" {
		t.Fatalf("commit previous word: %+v", r)
	}
	typeEngineLetters(eng, "men")
	candidates := eng.View().Candidates
	if len(candidates) == 0 || candidates[0].Text != "我们" {
		t.Fatalf("bigram candidate generation = %+v", candidates)
	}
}

func BenchmarkLearningStoreLookupBoost(b *testing.B) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:", sqlite.OpenOptions{})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	s, err := NewLearningStore(db)
	if err != nil {
		b.Fatal(err)
	}
	if err := s.RecordIMECommit(ctx, "ni", "你", "好", false); err != nil {
		b.Fatal(err)
	}
	adapter := NewUserFrequencyAdapter(s)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = adapter.LookupBoost("ni", "你", "好")
	}
}

func typeEngineLetters(e *inputengine.Engine, s string) {
	for _, r := range s {
		e.HandleKey(inputengine.Key{Kind: inputengine.KeyLetter, Rune: r})
	}
}

func TestUserFrequencyAdapterAndEndToEndBoost(t *testing.T) {
	if NewUserFrequencyAdapter(nil) != nil {
		t.Fatal("NewUserFrequencyAdapter(nil) must return nil interface")
	}
	var typedNil *UserFrequencyAdapter
	if typedNil.LookupBoost("ni", "你", "") != 0 || len(typedNil.CustomWordsForPrefix("n")) != 0 {
		t.Fatal("nil UserFrequencyAdapter must be inert")
	}

	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:", sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ls, err := NewLearningStore(db)
	if err != nil {
		t.Fatal(err)
	}

	// Baseline engine without user learning: "shi" -> "是" leads "时" and "事".
	inertEng := inputengine.New(inputengine.Options{CandidateCount: 7, UserFrequency: nil})
	typeEngineLetters(inertEng, "shi")
	baseView := inertEng.View()
	if len(baseView.Candidates) < 2 || baseView.Candidates[0].Text != "是" {
		t.Fatalf("unexpected baseline candidates for shi: %+v", baseView.Candidates)
	}
	inertEng.Reset()

	// Wired engine with UserFrequencyAdapter.
	adapter := NewUserFrequencyAdapter(ls)
	eng := inputengine.New(inputengine.Options{CandidateCount: 7, UserFrequency: adapter})

	// Commit "时" 3 times -> boost lifts "时" above "是" at #0.
	for i := 0; i < 3; i++ {
		if err := ls.RecordIMECommit(ctx, "shi", "时", "", false); err != nil {
			t.Fatal(err)
		}
	}
	typeEngineLetters(eng, "shi")
	if v := eng.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "时" {
		t.Fatalf("expected unigram boost to lift 时 to #0, got %+v", v.Candidates)
	}
	eng.Reset()

	// Bigram context: commit "这" -> "事" twice, then commit "这" in engine and type "shi".
	for i := 0; i < 2; i++ {
		if err := ls.RecordIMECommit(ctx, "shi", "事", "这", false); err != nil {
			t.Fatal(err)
		}
	}
	typeEngineLetters(eng, "zhe")
	if r := eng.HandleKey(inputengine.Key{Kind: inputengine.KeySpace}); r.Commit != "这" {
		t.Fatalf("expected commit 这, got %+v", r)
	}
	typeEngineLetters(eng, "shi")
	if v := eng.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "事" {
		t.Fatalf("expected bigram boost (这->事) to lift 事 to #0, got %+v", v.Candidates)
	}
	eng.Reset()

	// Custom user word not in starter dict surfaces via CustomWordsForPrefix.
	if err := ls.RecordIMECommit(ctx, "nihao", "泥嚎", "", true); err != nil {
		t.Fatal(err)
	}
	typeEngineLetters(eng, "nihao")
	if v := eng.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "泥嚎" {
		t.Fatalf("expected learned custom word 泥嚎 at #0, got %+v", v.Candidates)
	}

	// Inert engine (user_learning=false -> nil UserFrequency) remains unaffected.
	typeEngineLetters(inertEng, "shi")
	if v := inertEng.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "是" {
		t.Fatalf("inert engine must remain unaffected, got %+v", v.Candidates)
	}
}

func TestLearningStoreBoundedMirrorsAndStringCaps(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "learning_bounds.db")
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ls, err := NewLearningStore(db)
	if err != nil {
		t.Fatal(err)
	}

	// Pin a hot word and record high-frequency item use; these must survive >10k inserts.
	if err := ls.RecordIMECommit(ctx, "hotpy", "热词", "前词", true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := ls.RecordIMECommit(ctx, "hotpy", "热词", "前词", false); err != nil {
			t.Fatal(err)
		}
		if err := ls.RecordItemUse(ctx, "hot-item", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}

	// Oversized strings must be truncated to MaxMirrorKeyBytes / MaxMirrorItemIDBytes.
	longPy := strings.Repeat("a", MaxMirrorKeyBytes+100)
	longWord := strings.Repeat("字", MaxMirrorKeyBytes) // > MaxMirrorKeyBytes bytes
	if err := ls.RecordIMECommit(ctx, longPy, longWord, longWord, false); err != nil {
		t.Fatal(err)
	}
	longItem := strings.Repeat("x", MaxMirrorItemIDBytes+100)
	if err := ls.RecordItemUse(ctx, longItem, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	ls.mu.RLock()
	for k := range ls.lex {
		if len(k.pinyin) > MaxMirrorKeyBytes || len(k.word) > MaxMirrorKeyBytes {
			ls.mu.RUnlock()
			t.Fatalf("lex key exceeded MaxMirrorKeyBytes: py=%d word=%d", len(k.pinyin), len(k.word))
		}
	}
	for k := range ls.bigrams {
		if len(k[0]) > MaxMirrorKeyBytes || len(k[1]) > MaxMirrorKeyBytes {
			ls.mu.RUnlock()
			t.Fatalf("bigram key exceeded MaxMirrorKeyBytes: %d/%d", len(k[0]), len(k[1]))
		}
	}
	for k := range ls.uses {
		if len(k) > MaxMirrorItemIDBytes {
			ls.mu.RUnlock()
			t.Fatalf("uses key exceeded MaxMirrorItemIDBytes: %d", len(k))
		}
	}
	ls.mu.RUnlock()

	// Insert >10k distinct entries into lex, bigrams, and uses.
	now := time.Now().UTC()
	for i := 0; i < 10500; i++ {
		py := fmt.Sprintf("k%05d", i)
		w := fmt.Sprintf("w%05d", i)
		pw := fmt.Sprintf("p%05d", i)
		if err := ls.RecordIMECommit(ctx, py, w, pw, false); err != nil {
			t.Fatal(err)
		}
		if err := ls.RecordItemUse(ctx, fmt.Sprintf("item-%05d", i), now.Add(time.Duration(i)*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}

	lexN, bigN, usesN := ls.MirrorCounts()
	if lexN == 0 || lexN > MaxLexMirrorEntries {
		t.Fatalf("lex mirror count = %d, want (0, %d]", lexN, MaxLexMirrorEntries)
	}
	if bigN == 0 || bigN > MaxBigramMirrorEntries {
		t.Fatalf("bigram mirror count = %d, want (0, %d]", bigN, MaxBigramMirrorEntries)
	}
	if usesN == 0 || usesN > MaxUsesMirrorEntries {
		t.Fatalf("uses mirror count = %d, want (0, %d]", usesN, MaxUsesMirrorEntries)
	}
	if boost := ls.LookupBoost("hotpy", "热词", "前词"); boost <= 100 {
		t.Fatalf("pinned/hot entry was unexpectedly evicted, boost=%d", boost)
	}
	if snap := ls.FrecencySnapshot(now); snap["hot-item"] <= 0 {
		t.Fatalf("hot-item was unexpectedly evicted from uses mirror: %+v", snap["hot-item"])
	}

	_ = db.Close()

	// Reopening LearningStore on the populated DB loads bounded mirrors directly.
	db2, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	ls2, err := NewLearningStore(db2)
	if err != nil {
		t.Fatal(err)
	}
	lex2, big2, uses2 := ls2.MirrorCounts()
	if lex2 > MaxLexMirrorEntries || big2 > MaxBigramMirrorEntries || uses2 > MaxUsesMirrorEntries {
		t.Fatalf("reloaded mirrors exceed caps: lex=%d big=%d uses=%d", lex2, big2, uses2)
	}
}

func TestLearningStorePruneFrecency(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:", sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ls, err := NewLearningStore(db)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if err := ls.RecordItemUse(ctx, "stale-item", now.Add(-100*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := ls.RecordItemUse(ctx, fmt.Sprintf("fresh-%02d", i), now.Add(-time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	if err := ls.PruneFrecency(ctx, 5, DefaultFrecencyRetentionWindow); err != nil {
		t.Fatal(err)
	}
	_, _, usesN := ls.MirrorCounts()
	if usesN != 5 {
		t.Fatalf("uses mirror after PruneFrecency = %d, want 5", usesN)
	}
	rows, err := db.Query(ctx, `SELECT item_id FROM usage_frecency`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Values) != 5 {
		t.Fatalf("usage_frecency SQLite rows = %d, want 5", len(rows.Values))
	}
	snap := ls.FrecencySnapshot(now)
	if _, hasStale := snap["stale-item"]; hasStale {
		t.Fatalf("stale-item should have been pruned, snap=%+v", snap)
	}
}
