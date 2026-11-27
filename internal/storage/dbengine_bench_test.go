package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/clipboard"
)

func seed10kClipboardDB(b *testing.B) *SQLiteClipboardStore {
	b.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(b.TempDir(), "bench10k.db")
	store, err := NewSQLiteClipboardStore(ctx, dbPath, clipboard.NewPrivacyFilter())
	if err != nil {
		b.Fatalf("NewSQLiteClipboardStore: %v", err)
	}
	b.Cleanup(func() { _ = store.Close() })

	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).UnixNano()
	const batchSize = 500
	for start := 0; start < 10000; start += batchSize {
		stmts := make([]sqlite.Statement, 0, batchSize)
		for i := start; i < start+batchSize; i++ {
			id := fmt.Sprintf("clip-b-%05d", i)
			content := fmt.Sprintf("GET https://api.example.com/v1/items/%05d status=200 regular log line", i)
			app := "Chrome"
			if i%50 == 0 {
				content = fmt.Sprintf("剪贴板性能优化索引记录 id=%05d /api/v2/trigram/search", i)
				app = "Terminal"
			}
			ts := base + int64(i)*int64(time.Millisecond)
			stmts = append(stmts, sqlite.Statement{
				SQL: `INSERT INTO clipboard_items (id, content, content_hash, source_bundle_id, source_app_name, created_at_ns, updated_at_ns, is_pinned, kind, byte_size, copy_count)
				      VALUES (?, ?, ?, '', ?, ?, ?, 0, 'text', ?, 1)`,
				Args: []any{id, content, "hash-" + id, app, ts, ts, int64(len(content))},
			})
		}
		if _, err := store.Pool().TxWrite(ctx, "bench.seed", stmts); err != nil {
			b.Fatalf("seed batch: %v", err)
		}
	}
	return store
}

func BenchmarkDBPoolConcurrentReadWrite(b *testing.B) {
	ctx := context.Background()
	dbPath := filepath.Join(b.TempDir(), "benchpool.db")
	pool, err := OpenDBPool(ctx, DBPoolOptions{Path: dbPath, MaxReaders: 3})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = pool.Close() })
	if _, err := pool.ExecWrite(ctx, "init", `CREATE TABLE kv(k INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		b.Fatal(err)
	}
	if _, err := pool.ExecWrite(ctx, "seed", `INSERT INTO kv(k, v) VALUES(1, 'hello')`); err != nil {
		b.Fatal(err)
	}
	_ = pool.TuneAfterMigrate(ctx)
	_ = pool.InitReaders(ctx)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := pool.QueryRead(ctx, "kv.get", `SELECT v FROM kv WHERE k=1`); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkSQLiteClipboardSearch10kCJK(b *testing.B) {
	store := seed10kClipboardDB(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, _, err := store.ListSummaryPage(ctx, clipboard.ClipboardQuerySpec{
			Text:  "性能优化",
			Limit: 50,
		})
		if err != nil {
			b.Fatal(err)
		}
		if len(res) == 0 {
			b.Fatal("expected trigram matches")
		}
	}
}

func BenchmarkSQLiteClipboardKeysetPage10k(b *testing.B) {
	store := seed10kClipboardDB(b)
	ctx := context.Background()
	_, cursor, err := store.ListSummaryPage(ctx, clipboard.ClipboardQuerySpec{Limit: 80})
	if err != nil || cursor == nil {
		b.Fatalf("initial page cursor=%v err=%v", cursor, err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		items, _, err := store.ListSummaryPage(ctx, clipboard.ClipboardQuerySpec{
			Limit:  80,
			Cursor: cursor,
		})
		if err != nil {
			b.Fatal(err)
		}
		if len(items) != 80 {
			b.Fatalf("page len=%d, want 80", len(items))
		}
	}
}
