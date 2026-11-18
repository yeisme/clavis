package uiapp

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestNormalizeImagePNG(t *testing.T) {
	pngData := []byte("not-a-real-png")
	if _, err := normalizeImagePNG(pngData); err == nil {
		t.Fatal("expected invalid image to be rejected")
	}
	valid := testPNG16(t)
	got, err := normalizeImagePNG(valid)
	if err != nil {
		t.Fatalf("valid PNG normalization: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("valid PNG normalization returned no bytes")
	}
}

func TestWriteClipboardMediaMissingPayload(t *testing.T) {
	// writeClipboardMedia first checks the host lifecycle. Once MyGo is ready,
	// malformed entries must still report missing media rather than success.
	if err := writeClipboardMedia(engine.ClipboardMedia{Kind: "image"}); err == nil {
		t.Fatal("missing image payload was reported as success")
	} else if !errors.Is(err, errMediaMissing) && err.Error() != "app not ready" {
		t.Fatalf("missing image error = %v, want missing-media or lifecycle error", err)
	}
	if err := writeClipboardMedia(engine.ClipboardMedia{Kind: "file", FilePaths: []string{"/missing/file"}}); err == nil {
		t.Fatal("missing file payload was reported as success")
	} else if !errors.Is(err, errMediaMissing) && err.Error() != "app not ready" {
		t.Fatalf("missing file error = %v, want missing-media or lifecycle error", err)
	}
}

func TestWriteClipboardMediaRequiresReadyApp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, []byte("not-a-png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeClipboardMedia(engine.ClipboardMedia{Kind: "image", Path: path}); err == nil {
		t.Fatal("expected app-not-ready or media error before MyGo app startup")
	}
}

func TestPasteClipboardMediaAsyncDoesNotPasteAfterWriteFailure(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.ctrl.pasteClipboardMediaAsync(engine.ClipboardMedia{Kind: "image"})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if f.bridge.getPasteClipboardCalls() != 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := f.bridge.getPasteClipboardCalls(); got != 0 {
		t.Fatalf("media write failure still invoked bridge paste %d time(s)", got)
	}
}

func TestExecuteEffectMediaPasteFailureDoesNotRecordUse(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.ctrl.executeEffect(engine.Effect{
		Type:        engine.EffectPasteClipboardItem,
		ClipboardID: "missing-image",
		ClipboardMedia: &engine.ClipboardMedia{
			Kind: "image",
		},
	}, f.ctrl.State())

	if got := f.usedItemIDs(); len(got) != 0 {
		t.Fatalf("failed media paste recorded use %v", got)
	}
	if got := f.bridge.getPasteClipboardCalls(); got != 0 {
		t.Fatalf("failed media paste invoked bridge paste %d time(s)", got)
	}
}

func TestPasteClipboardMediaAsyncRecordsOnlyAfterWrite(t *testing.T) {
	f := newInteractionFixture(t, nil)
	var events []string
	var eventsMu sync.Mutex
	record := func() {
		eventsMu.Lock()
		events = append(events, "record")
		eventsMu.Unlock()
	}
	write := func(engine.ClipboardMedia) error {
		eventsMu.Lock()
		events = append(events, "write")
		eventsMu.Unlock()
		return nil
	}

	f.ctrl.pasteClipboardMediaAsyncWithWriter(engine.ClipboardMedia{Kind: "image"}, write, record)
	waitUntil(t, time.Second, func() bool {
		return f.bridge.getPasteClipboardCalls() == 1
	}, "successful media paste")

	eventsMu.Lock()
	got := append([]string(nil), events...)
	eventsMu.Unlock()
	if want := []string{"write", "record"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("successful media paste events = %v, want %v", got, want)
	}
}

func TestExecuteEffectMediaCopyFailureDoesNotRecordUse(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.ctrl.executeEffect(engine.Effect{
		Type:        engine.EffectCopyText,
		ClipboardID: "missing-image",
		ClipboardMedia: &engine.ClipboardMedia{
			Kind: "image",
		},
	}, f.ctrl.State())
	if got := f.usedItemIDs(); len(got) != 0 {
		t.Fatalf("failed media copy recorded use %v", got)
	}
	if got := f.copiedTexts(); len(got) != 0 {
		t.Fatalf("failed media copy invoked text writer %v", got)
	}
}

func TestPasteFormatFor(t *testing.T) {
	cases := []struct {
		m    engine.ClipboardMedia
		want pasteFormat
	}{
		{engine.ClipboardMedia{Kind: "image", Path: "/x/a.png"}, pastePNG},
		{engine.ClipboardMedia{Kind: "image"}, pasteNone},
		{engine.ClipboardMedia{Kind: "file", FilePaths: []string{`C:\a.txt`}}, pasteFiles},
		{engine.ClipboardMedia{Kind: "video", FilePaths: []string{`C:\v.mp4`}}, pasteFiles},
		{engine.ClipboardMedia{Kind: "video"}, pasteNone},
		{engine.ClipboardMedia{}, pasteNone},
	}
	for i, c := range cases {
		if got := pasteFormatFor(c.m); got != c.want {
			t.Fatalf("case %d: got %v want %v", i, got, c.want)
		}
	}
}

func TestEngineClipboardMediaMapping(t *testing.T) {
	if got := engineClipboardMedia(clipboard.Media{}); got.IsMedia() {
		t.Fatalf("text mapped to media: %+v", got)
	}
	img := engineClipboardMedia(clipboard.Media{Kind: clipboard.KindImage, MediaPath: "/m/a.png", ThumbPath: "/m/t.png", Width: 3, Height: 4})
	if img.Kind != "image" || img.Path != "/m/a.png" || img.ThumbPath != "/m/t.png" || img.FilePaths != nil {
		t.Fatalf("image: %+v", img)
	}
	files := engineClipboardMedia(clipboard.Media{Kind: clipboard.KindFile, MediaPath: clipboard.JoinFilePaths([]string{`C:\a`, `C:\b`})})
	if len(files.FilePaths) != 2 || files.FilePaths[1] != `C:\b` {
		t.Fatalf("files: %+v", files)
	}
}

func TestClipboardMediaMetaRows(t *testing.T) {
	rows := clipboardMediaMetaRows(engine.ClipboardMedia{Kind: "image", Width: 1920, Height: 1080, ByteSize: 2_400_000})
	if len(rows) != 1 || rows[0] != "1920×1080 · 2.3 MB" {
		t.Fatalf("rows = %q", rows)
	}
	rows = clipboardMediaMetaRows(engine.ClipboardMedia{Kind: "video", ByteSize: 500, DurationMs: 42_500})
	if len(rows) != 2 || rows[0] != "500 B" {
		t.Fatalf("video rows = %q", rows)
	}
	if got := humanBytes(1536); got != "1.5 KB" {
		t.Fatalf("humanBytes = %q", got)
	}
}
