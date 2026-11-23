package storage

import (
	"context"
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
