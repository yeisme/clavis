package uiapp

import (
	"archive/zip"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func createRealMediaFixtures(t *testing.T) []engine.OmniboxItem {
	t.Helper()
	dir := t.TempDir()

	// 1. Real PNG image
	imgPath := filepath.Join(dir, "architecture-diagram.png")
	if err := os.WriteFile(imgPath, regressionPreviewPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}

	// 2. Real video file container bytes (placeholder file on disk with real metadata)
	videoPath := filepath.Join(dir, "demo-recording.mp4")
	if err := os.WriteFile(videoPath, make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}

	// 3. Real audio file on disk
	audioPath := filepath.Join(dir, "voice-note.m4a")
	if err := os.WriteFile(audioPath, make([]byte, 2048), 0o600); err != nil {
		t.Fatal(err)
	}

	// 4. Real Markdown document on disk
	docPath := filepath.Join(dir, "release-notes.md")
	docBody := "# Clavis Release Notes\n\n- Added 6-category media filter\n- Async file probe off UI thread\n- Honest unavailable state\n"
	if err := os.WriteFile(docPath, []byte(docBody), 0o600); err != nil {
		t.Fatal(err)
	}

	// 5. Real ZIP archive with real members
	zipPath := filepath.Join(dir, "bundle-export.zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	for _, member := range []struct{ name, body string }{
		{"README.md", "# Bundle\n"},
		{"configs/main.toml", "[trigger]\nleader_key = \"right_option\"\n"},
		{"assets/logo.svg", "<svg></svg>"},
	} {
		w, err := zw.Create(member.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(member.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zf.Close(); err != nil {
		t.Fatal(err)
	}

	// 6. Real generic binary file on disk
	filePath := filepath.Join(dir, "firmware-image.bin")
	if err := os.WriteFile(filePath, []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00}, 0o600); err != nil {
		t.Fatal(err)
	}

	// 7. Missing / unavailable file path
	missingPath := filepath.Join(dir, "deleted-clip.mov")

	return []engine.OmniboxItem{
		{
			ID:                 "media-img",
			Title:              "architecture-diagram.png",
			ClipboardID:        "clip-img",
			ClipboardSourceApp: "Screenshot",
			Kind:               engine.ItemClipboardEntry,
			ClipboardMedia:     &engine.ClipboardMedia{Kind: "image", Path: imgPath, ThumbPath: imgPath, Mime: "image/png", Width: 12, Height: 12},
		},
		{
			ID:                 "media-vid",
			Title:              "demo-recording.mp4",
			ClipboardID:        "clip-vid",
			ClipboardSourceApp: "QuickTime",
			Kind:               engine.ItemClipboardEntry,
			ClipboardMedia:     &engine.ClipboardMedia{Kind: "video", Path: videoPath, FilePaths: []string{videoPath}, Mime: "video/mp4", Width: 1920, Height: 1080, DurationMs: 42000},
		},
		{
			ID:                 "media-aud",
			Title:              "voice-note.m4a",
			ClipboardID:        "clip-aud",
			ClipboardSourceApp: "Voice Memos",
			Kind:               engine.ItemClipboardEntry,
			ClipboardMedia:     &engine.ClipboardMedia{Kind: "audio", Path: audioPath, FilePaths: []string{audioPath}, Mime: "audio/mp4", DurationMs: 18500},
		},
		{
			ID:                 "media-doc",
			Title:              "release-notes.md",
			ClipboardID:        "clip-doc",
			ClipboardSourceApp: "Finder",
			Kind:               engine.ItemClipboardEntry,
			ClipboardMedia:     &engine.ClipboardMedia{Kind: "file", Path: docPath, FilePaths: []string{docPath}, Mime: "text/markdown"},
		},
		{
			ID:                 "media-zip",
			Title:              "bundle-export.zip",
			ClipboardID:        "clip-zip",
			ClipboardSourceApp: "Finder",
			Kind:               engine.ItemClipboardEntry,
			ClipboardMedia:     &engine.ClipboardMedia{Kind: "file", Path: zipPath, FilePaths: []string{zipPath}, Mime: "application/zip"},
		},
		{
			ID:                 "media-file",
			Title:              "firmware-image.bin",
			ClipboardID:        "clip-file",
			ClipboardSourceApp: "Terminal",
			Kind:               engine.ItemClipboardEntry,
			ClipboardMedia:     &engine.ClipboardMedia{Kind: "file", Path: filePath, FilePaths: []string{filePath}},
		},
		{
			ID:                 "media-missing",
			Title:              "deleted-clip.mov",
			ClipboardID:        "clip-missing",
			ClipboardSourceApp: "Finder",
			Kind:               engine.ItemClipboardEntry,
			ClipboardMedia:     &engine.ClipboardMedia{Kind: "video", Path: missingPath, FilePaths: []string{missingPath}, Mime: "video/quicktime"},
		},
	}
}

func TestClassifyClipboardMediaAllSixCategories(t *testing.T) {
	cases := []struct {
		m    engine.ClipboardMedia
		want MediaCategory
	}{
		{engine.ClipboardMedia{Kind: "image", Path: "/tmp/a.png"}, MediaCategoryImage},
		{engine.ClipboardMedia{Kind: "video", Path: "/tmp/b.mp4"}, MediaCategoryVideo},
		{engine.ClipboardMedia{Kind: "file", Path: "/tmp/c.flac"}, MediaCategoryAudio},
		{engine.ClipboardMedia{Kind: "file", Path: "/tmp/d.pdf"}, MediaCategoryDocument},
		{engine.ClipboardMedia{Kind: "file", Path: "/tmp/e.tar.gz"}, MediaCategoryArchive},
		{engine.ClipboardMedia{Kind: "file", Path: "/tmp/f.bin"}, MediaCategoryFile},
	}
	for _, tc := range cases {
		if got := ClassifyClipboardMedia(tc.m); got != tc.want {
			t.Errorf("ClassifyClipboardMedia(%+v) = %q, want %q", tc.m, got, tc.want)
		}
	}
}

func TestMediaAsyncProbeRealFilesAndUnavailable(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	items := createRealMediaFixtures(t)

	for _, it := range items {
		m := *it.ClipboardMedia
		_ = ctrl.MediaProbe(m)
		waitUntil(t, 2*time.Second, func() bool {
			return ctrl.MediaProbe(m).Status != MediaProbeLoading
		}, "probe "+it.ID)
	}

	// Document excerpt is real markdown content.
	docSnap := ctrl.MediaProbe(*items[3].ClipboardMedia)
	if docSnap.Status != MediaProbeReady || !docSnap.Available || len(docSnap.TextExcerptLines) == 0 || !strings.Contains(docSnap.TextExcerptLines[0], "Clavis Release Notes") {
		t.Fatalf("unexpected doc probe: %+v", docSnap)
	}

	// Archive lists real ZIP entries.
	zipSnap := ctrl.MediaProbe(*items[4].ClipboardMedia)
	if zipSnap.Status != MediaProbeReady || zipSnap.ArchiveEntryCount != 3 || len(zipSnap.ArchiveEntries) != 3 {
		t.Fatalf("unexpected zip probe: %+v", zipSnap)
	}

	// Missing file explicitly reports Unavailable.
	missingSnap := ctrl.MediaProbe(*items[6].ClipboardMedia)
	if missingSnap.Status != MediaProbeUnavailable || missingSnap.Available {
		t.Fatalf("missing file must report Unavailable, got %+v", missingSnap)
	}
}

func TestMediaSearchKeyboardFilterIMEAndSnapshots(t *testing.T) {
	prev := i18n.Current()
	defer i18n.SetLanguage(prev)
	i18n.SetLanguage(i18n.Chinese)

	ctrl, _, _, _ := newTestController(t, nil)
	items := createRealMediaFixtures(t)
	ctrl.SetMediaSearchItems(items)

	// Wait for all background probes to settle before asserting previews.
	for _, it := range items {
		m := *it.ClipboardMedia
		_ = ctrl.MediaProbe(m)
		waitUntil(t, 2*time.Second, func() bool {
			return ctrl.MediaProbe(m).Status != MediaProbeLoading
		}, "probe "+it.ID)
	}

	tt := ui.NewTester(ctrl.MediaSearchView, 980, 620)
	tt.Frame()
	tt.Frame()

	// Keyboard Tab cycles categories: All -> Image -> Video -> Audio -> Document -> Archive -> File.
	wantOrder := []MediaCategory{
		MediaCategoryImage, MediaCategoryVideo, MediaCategoryAudio,
		MediaCategoryDocument, MediaCategoryArchive, MediaCategoryFile, MediaCategoryAll,
	}
	for _, want := range wantOrder {
		visible := FilterMediaItems(items, ctrl.MediaSearchCategory(), ctrl.MediaSearchQuery())
		if !ctrl.HandleMediaSearchKey(ui.KeyTab, 0, visible) {
			t.Fatalf("Tab did not advance media category to %q", want)
		}
		if got := ctrl.MediaSearchCategory(); got != want {
			t.Fatalf("after Tab category = %q, want %q", got, want)
		}
	}

	// IME composition yields Tab / Escape / Arrow keys.
	st := ctrl.mediaSearchState()
	st.mu.Lock()
	st.isComposing = true
	st.mu.Unlock()
	if ctrl.HandleMediaSearchKey(ui.KeyTab, 0, items) {
		t.Fatal("media search must yield Tab while IME composition is active")
	}
	st.mu.Lock()
	st.isComposing = false
	st.mu.Unlock()

	// Optional snapshot export for all 6 media categories + unavailable state.
	snapDir := strings.TrimSpace(os.Getenv("CLAVIS_MEDIA_SNAPSHOT_DIR"))
	if snapDir != "" {
		if err := os.MkdirAll(snapDir, 0o755); err != nil {
			t.Fatal(err)
		}
		shots := []struct {
			name string
			cat  MediaCategory
			idx  int
			dark bool
			lang i18n.Language
		}{
			{"01-media-image-zh-light.png", MediaCategoryImage, 0, false, i18n.Chinese},
			{"02-media-video-zh-dark.png", MediaCategoryVideo, 0, true, i18n.Chinese},
			{"03-media-audio-zh-light.png", MediaCategoryAudio, 0, false, i18n.Chinese},
			{"04-media-document-zh-dark.png", MediaCategoryDocument, 0, true, i18n.Chinese},
			{"05-media-archive-en-light.png", MediaCategoryArchive, 0, false, i18n.English},
			{"06-media-file-en-dark.png", MediaCategoryFile, 0, true, i18n.English},
			{"07-media-unavailable-zh-dark.png", MediaCategoryVideo, 1, true, i18n.Chinese},
		}
		for _, s := range shots {
			i18n.SetLanguage(s.lang)
			ctrl.SetMediaSearchCategory(s.cat)
			vis := FilterMediaItems(items, s.cat, "")
			ctrl.SetMediaSearchSelectedIndex(s.idx, len(vis))
			shotTester := ui.NewTester(ctrl.MediaSearchView, 980, 620)
			shotTester.SetDark(s.dark)
			shotTester.Frame()
			shotTester.Frame()
			img := shotTester.Image()
			if img == nil {
				t.Fatalf("nil image for %s", s.name)
			}
			outPath := filepath.Join(snapDir, s.name)
			f, err := os.Create(outPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				_ = f.Close()
				t.Fatal(err)
			}
			_ = f.Close()
			t.Logf("snapshot: %s", outPath)
		}
	}
}
