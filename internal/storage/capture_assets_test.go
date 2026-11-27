package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/clipboard"
)

func TestCaptureAssetStoreCRUDAndStats(t *testing.T) {
	ctx := context.Background()
	clipStore, err := NewSQLiteClipboardStore(ctx, filepath.Join(t.TempDir(), "app.db"), clipboard.NewPrivacyFilter())
	if err != nil {
		t.Fatalf("NewSQLiteClipboardStore: %v", err)
	}
	defer clipStore.Close()

	assets := NewCaptureAssetStore(clipStore.Pool())
	t0 := time.Now().UTC().Add(-time.Minute)
	if _, err := assets.Record(ctx, CaptureAsset{
		ID:        "shot-1",
		Kind:      "screenshot",
		FilePath:  "/tmp/shot-1.png",
		Mime:      "image/png",
		Width:     1200,
		Height:    800,
		ByteSize:  4096,
		CreatedAt: t0,
	}); err != nil {
		t.Fatalf("Record shot-1: %v", err)
	}
	if _, err := assets.Record(ctx, CaptureAsset{
		ID:         "rec-1",
		Kind:       "recording",
		FilePath:   "/tmp/rec-1.mov",
		Mime:       "video/quicktime",
		DurationMs: 5200,
		ByteSize:   102400,
		Pinned:     true,
		CreatedAt:  t0.Add(time.Second),
	}); err != nil {
		t.Fatalf("Record rec-1: %v", err)
	}

	got, ok, err := assets.Get(ctx, "rec-1")
	if err != nil || !ok || !got.Pinned || got.DurationMs != 5200 {
		t.Fatalf("Get rec-1 = (%+v, %v, %v)", got, ok, err)
	}

	list, err := assets.List(ctx, "recording", 10)
	if err != nil || len(list) != 1 || list[0].ID != "rec-1" {
		t.Fatalf("List recording = (%+v, %v)", list, err)
	}

	stats, err := assets.Stats(ctx)
	if err != nil || stats.TotalCount != 2 || stats.ScreenshotCnt != 1 || stats.RecordingCnt != 1 || stats.PinnedCount != 1 || stats.TotalByteSize != 106496 {
		t.Fatalf("Stats = (%+v, %v)", stats, err)
	}

	if err := assets.SetPinned(ctx, "shot-1", true); err != nil {
		t.Fatalf("SetPinned: %v", err)
	}
	if err := assets.Delete(ctx, "rec-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	stats, err = assets.Stats(ctx)
	if err != nil || stats.TotalCount != 1 || stats.PinnedCount != 1 {
		t.Fatalf("Stats after delete = (%+v, %v)", stats, err)
	}
}
