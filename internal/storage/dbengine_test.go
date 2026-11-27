package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDBPoolConcurrentReadersAndWritersZeroBusy(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "pool-race.db")
	var observed atomic.Int64
	pool, err := OpenDBPool(ctx, DBPoolOptions{
		Path:               dbPath,
		MaxReaders:         3,
		SlowQueryThreshold: time.Nanosecond,
		Observer: func(ev QueryEvent) {
			observed.Add(1)
		},
	})
	if err != nil {
		t.Fatalf("OpenDBPool: %v", err)
	}
	defer pool.Close()

	if _, err := pool.ExecWrite(ctx, "init.table", `CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := pool.TuneAfterMigrate(ctx); err != nil {
		t.Fatalf("TuneAfterMigrate: %v", err)
	}
	if err := pool.InitReaders(ctx); err != nil {
		t.Fatalf("InitReaders: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 16)

	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				key := fmt.Sprintf("w%d-%d", id, i)
				if _, err := pool.ExecWrite(ctx, "kv.put", `INSERT OR REPLACE INTO kv(k, v) VALUES(?, ?)`, key, "secret-arg-must-not-leak"); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				if _, err := pool.QueryRead(ctx, "kv.count", `SELECT COUNT(*) FROM kv`); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent DBPool error: %v", err)
	}

	if err := pool.QuickCheck(ctx); err != nil {
		t.Fatalf("QuickCheck: %v", err)
	}
	if err := pool.Optimize(ctx); err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if _, _, _, err := pool.Checkpoint(ctx, CheckpointPassive); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	snap, err := pool.InspectStats(ctx)
	if err != nil {
		t.Fatalf("InspectStats: %v", err)
	}
	if snap.ReaderCount != 3 || snap.TotalWrites < 80 || snap.TotalReads < 320 || observed.Load() == 0 {
		t.Fatalf("unexpected stats snapshot: %+v observed=%d", snap, observed.Load())
	}
	if len(snap.RecentSlow) == 0 || len(snap.RecentSlow) > slowQueryRingSize {
		t.Fatalf("unexpected slow ring len=%d", len(snap.RecentSlow))
	}
	for _, rec := range snap.RecentSlow {
		if strings.Contains(rec.Op, "secret-arg-must-not-leak") {
			t.Fatalf("slow query record leaked bound argument: %+v", rec)
		}
	}
}

func TestDBPoolCloseIsIdempotentAndRejectsLaterOps(t *testing.T) {
	ctx := context.Background()
	pool, err := OpenDBPool(ctx, DBPoolOptions{Path: ":memory:"})
	if err != nil {
		t.Fatalf("OpenDBPool: %v", err)
	}
	if err := pool.TuneAfterMigrate(ctx); err != nil {
		t.Fatalf("TuneAfterMigrate: %v", err)
	}
	if err := pool.InitReaders(ctx); err != nil {
		t.Fatalf("InitReaders: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := pool.QueryRead(ctx, "after_close", `SELECT 1`); err == nil {
		t.Fatal("expected error querying closed DBPool")
	}
}
