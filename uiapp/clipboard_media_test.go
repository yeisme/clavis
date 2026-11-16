package uiapp

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestNormalizeImagePNG(t *testing.T) {
	pngData := []byte("not-a-real-png")
	if _, err := normalizeImagePNG(pngData); err == nil {
		t.Fatal("expected invalid image to be rejected")
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
