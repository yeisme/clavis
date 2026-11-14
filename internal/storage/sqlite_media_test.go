package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/clipboard"
)

// A database created before media columns existed must upgrade in place and keep its rows.
func TestSQLiteClipboardStore_MigratesPreMediaSchema(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "old.db")
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE clipboard_items (id TEXT PRIMARY KEY, content TEXT NOT NULL, content_hash TEXT NOT NULL UNIQUE,
			source_bundle_id TEXT NOT NULL DEFAULT '', source_app_name TEXT NOT NULL DEFAULT '',
			created_at_ns INTEGER NOT NULL, updated_at_ns INTEGER NOT NULL, is_pinned INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO clipboard_items VALUES ('old-1', 'legacy text', 'h1', '', 'Notes', 1, 1, 1)`,
	} {
		if _, err := db.Execute(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	store, err := NewSQLiteClipboardStore(ctx, dbPath, clipboard.NewPrivacyFilter())
	if err != nil {
		t.Fatalf("open+migrate: %v", err)
	}
	defer store.Close()
	items, err := store.Recent(ctx, 10)
	if err != nil || len(items) != 1 || items[0].Content != "legacy text" || items[0].IsMedia() || !items[0].IsPinned {
		t.Fatalf("legacy row after migrate: %+v %v", items, err)
	}
	// Re-opening is idempotent.
	_ = store.Close()
	store, err = NewSQLiteClipboardStore(ctx, dbPath, clipboard.NewPrivacyFilter())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store.Close()
}

func TestSQLiteClipboardStore_MediaRoundTripAndDedupe(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "m.db"), clipboard.NewPrivacyFilter())
	img := clipboard.CaptureCandidate{
		SourceAppName: "mspaint",
		Media: clipboard.Media{Kind: clipboard.KindImage, MediaPath: "/m/abc.png", MediaMime: "image/png",
			Width: 640, Height: 480, BlobSHA: "abc", ThumbPath: "/m/thumbs/abc.png", ByteSize: 1234},
	}
	got, ok, err := store.Record(ctx, img)
	if err != nil || !ok || got.Kind != clipboard.KindImage {
		t.Fatalf("record image: %+v %v %v", got, ok, err)
	}
	if _, _, err := store.Record(ctx, img); err != nil {
		t.Fatal(err)
	}
	// Same text content "" but a different blob is a different entry.
	img2 := img
	img2.BlobSHA, img2.MediaPath = "def", "/m/def.png"
	if _, _, err := store.Record(ctx, img2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Record(ctx, clipboard.CaptureCandidate{Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	items, _ := store.Recent(ctx, 10)
	if len(items) != 3 {
		t.Fatalf("want 3 items (2 images deduped by blob + text), got %d: %+v", len(items), items)
	}
	var found bool
	for _, it := range items {
		if it.BlobSHA == "abc" {
			found = true
			if it.MediaPath != "/m/abc.png" || it.Width != 640 || it.Height != 480 || it.ThumbPath == "" || it.ByteSize != 1234 {
				t.Fatalf("media fields lost: %+v", it)
			}
		}
		if it.Content == "hello" && it.IsMedia() {
			t.Fatalf("text row must not be media: %+v", it)
		}
	}
	if !found {
		t.Fatal("image row missing")
	}
	// Binary payloads never reach FTS: searching the blob path finds nothing.
	if res, _ := store.Search(ctx, "abc.png", 10); len(res) != 0 {
		t.Fatalf("media path leaked into search: %+v", res)
	}
}
