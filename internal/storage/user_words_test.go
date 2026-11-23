package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

func TestInjectUserWordsReportsPersistenceFailure(t *testing.T) {
	db, err := sqlite.Open(context.Background(), ":memory:", sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewLearningStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rejected := InjectUserWords(ctx, store, []string{"叶树根"}); len(rejected) != 1 || rejected[0] != "叶树根" {
		t.Fatalf("rejected=%v, want the word whose persistence failed", rejected)
	}
}

func TestInjectUserWordsRollsBackAllReadingsOnFailure(t *testing.T) {
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
	if _, err := db.Execute(ctx, `CREATE TRIGGER reject_second_reading BEFORE INSERT ON ime_user_lexicon WHEN EXISTS (SELECT 1 FROM ime_user_lexicon) BEGIN SELECT RAISE(ABORT, 'second reading'); END`); err != nil {
		t.Fatal(err)
	}
	if rejected := InjectUserWords(ctx, store, []string{"重庆"}); len(rejected) != 1 || rejected[0] != "重庆" {
		t.Fatalf("rejected=%v", rejected)
	}
	rows, err := db.Query(ctx, `SELECT pinyin_key FROM ime_user_lexicon`)
	if err != nil || len(rows.Values) != 0 || len(store.PinnedIMEWords(ctx)) != 0 {
		t.Fatalf("failed injection left persisted or mirrored readings: rows=%v, pinned=%v, err=%v", rows.Values, store.PinnedIMEWords(ctx), err)
	}
}

func TestInjectUserWordsRejectsExcessiveReadingExpansion(t *testing.T) {
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
	word := strings.Repeat("重", 13)
	if rejected := InjectUserWords(ctx, store, []string{word}); len(rejected) != 1 || rejected[0] != word {
		t.Fatalf("rejected=%v", rejected)
	}
	if got := store.PinnedIMEWords(ctx); len(got) != 0 {
		t.Fatalf("expanded word left %d pinned readings", len(got))
	}
}

func TestInjectUserWordsPinsAndRejectsSingleCharacters(t *testing.T) {
	db, err := sqlite.Open(context.Background(), ":memory:", sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewLearningStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if rejected := InjectUserWords(context.Background(), store, []string{"叶树根", "是"}); len(rejected) != 1 || rejected[0] != "是" {
		t.Fatalf("rejected=%v", rejected)
	}
	adapter := NewUserFrequencyAdapter(store)
	for _, input := range []string{"yeshugen", "yeshu"} {
		eng := inputengine.New(inputengine.Options{CandidateCount: 7, UserFrequency: adapter})
		typeLettersForUserWordTest(eng, input)
		view := eng.View()
		if len(view.Candidates) == 0 || view.Candidates[0].Text != "叶树根" {
			t.Fatalf("%s candidates=%+v", input, view.Candidates)
		}
	}
}

func TestInjectUserWordsPinsAllPolyphonicReadings(t *testing.T) {
	db, err := sqlite.Open(context.Background(), ":memory:", sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewLearningStore(db)
	if err != nil {
		t.Fatal(err)
	}
	InjectUserWords(context.Background(), store, []string{"重庆"})
	adapter := NewUserFrequencyAdapter(store)
	for _, input := range []string{"chongqing", "zhongqing"} {
		eng := inputengine.New(inputengine.Options{CandidateCount: 7, UserFrequency: adapter})
		typeLettersForUserWordTest(eng, input)
		view := eng.View()
		if len(view.Candidates) == 0 || view.Candidates[0].Text != "重庆" {
			t.Fatalf("%s candidates=%+v", input, view.Candidates)
		}
	}
}

func typeLettersForUserWordTest(eng *inputengine.Engine, input string) {
	for _, r := range input {
		eng.HandleKey(inputengine.Key{Kind: inputengine.KeyLetter, Rune: r})
	}
}
