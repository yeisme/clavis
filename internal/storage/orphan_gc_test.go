package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/clipboard"
)

func TestReconcileOrphanMediaRespectsGraceAndCaptureAssets(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "app.db")
	mediaDir := filepath.Join(tmp, "clipboard-media")

	store, err := NewSQLiteClipboardStoreWithOptions(ctx, dbPath, clipboard.NewPrivacyFilter(), SQLiteClipboardStoreOptions{
		MediaDir: mediaDir,
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Record one active overflow text item and one active capture asset.
	large := strings.Repeat("active overflow text\n", 4000)
	if _, _, err := store.Record(ctx, clipboard.CaptureCandidate{Content: large, Timestamp: time.Now()}); err != nil {
		t.Fatalf("record overflow: %v", err)
	}
	activeThumb := filepath.Join(mediaDir, "thumbs", strings.Repeat("a", 64)+".png")
	if err := os.MkdirAll(filepath.Dir(activeThumb), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activeThumb, []byte("thumb"), 0o600); err != nil {
		t.Fatal(err)
	}
	assets := NewCaptureAssetStore(store.Pool())
	if _, err := assets.Record(ctx, CaptureAsset{
		ID:        "cap-1",
		Kind:      "recording",
		FilePath:  filepath.Join(tmp, "movie.mov"),
		ThumbPath: activeThumb,
	}); err != nil {
		t.Fatalf("record capture asset: %v", err)
	}

	// Create two orphans: one stale (old mtime), one fresh (within grace period).
	staleOrphan := filepath.Join(mediaDir, "text", strings.Repeat("b", 64)+".txt.gz")
	freshOrphan := filepath.Join(mediaDir, strings.Repeat("c", 64)+".png")
	unrelatedFile := filepath.Join(mediaDir, "notes.txt")
	if err := os.WriteFile(staleOrphan, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(freshOrphan, []byte("fresh"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelatedFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(staleOrphan, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	res, err := ReconcileOrphanMedia(ctx, store.Pool(), mediaDir, 10*time.Minute)
	if err != nil {
		t.Fatalf("ReconcileOrphanMedia: %v", err)
	}
	if res.RemovedFiles != 1 || res.ReclaimedBytes != int64(len("stale")) {
		t.Fatalf("unexpected GC result: %+v", res)
	}
	if _, err := os.Stat(staleOrphan); !os.IsNotExist(err) {
		t.Fatalf("expected stale orphan removed, stat err=%v", err)
	}
	if _, err := os.Stat(freshOrphan); err != nil {
		t.Fatalf("fresh orphan inside grace period removed: %v", err)
	}
	if _, err := os.Stat(activeThumb); err != nil {
		t.Fatalf("active capture asset thumb removed: %v", err)
	}
	if _, err := os.Stat(unrelatedFile); err != nil {
		t.Fatalf("unrelated user file removed: %v", err)
	}
}
