package storage

import (
	"context"
	"testing"

	"github.com/egoist/mygo/plugins/sqlite"
)

func TestLearningStoreCommitRollsBackWhenBigramFails(t *testing.T) {
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
	if _, err := db.Execute(ctx, "DROP TABLE ime_bigrams"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordIMECommit(ctx, "shi", "是", "我", false); err == nil {
		t.Fatal("commit succeeded without the bigram table")
	}
	rows, err := db.Query(ctx, "SELECT frequency FROM ime_user_lexicon WHERE pinyin_key=? AND word=?", "shi", "是")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Values) != 0 || store.LookupBoost("shi", "是", "我") != 0 {
		t.Fatalf("failed commit left a unigram: %v", rows.Values)
	}
}
