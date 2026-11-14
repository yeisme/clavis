package storage

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
)

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
	if err := s.RecordIMECommit(ctx, "ni", "你", "", false); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.LookupBoost("ni", "你", "")
	}
}
