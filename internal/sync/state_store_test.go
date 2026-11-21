package sync

import (
	"context"
	"testing"

	coresync "github.com/yeisme/clavis/pkg/core/sync"
)

func TestSQLiteStateStorePersistsWatermark(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLiteStateStore(ctx, t.TempDir()+"/state.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, 17, true); err != nil {
		t.Fatal(err)
	}
	if got, ready, err := store.Load(ctx); err != nil || got != 17 || !ready {
		t.Fatalf("load = %d, %v, %v", got, ready, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	_ = coresync.ErrStateStoreNil
}
