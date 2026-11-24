package uiapp

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func regressionPreviewPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 12, 12))
	for y := 0; y < 12; y++ {
		for x := 0; x < 12; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 12, G: 180, B: 240, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// renderMediaFallbackAfterFailure primes the real original-image cache with a
// failed decode. Removing the source models a missing file after the
// asynchronous decode has completed without changing production state.
func renderMediaFallbackAfterFailure(t *testing.T, thumbPath string, removeOriginal bool) *ui.Tester {
	t.Helper()
	dir := t.TempDir()
	original := filepath.Join(dir, "original.png")
	if err := os.WriteFile(original, []byte("not a valid image"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctrl, _, _, _ := newTestController(t, nil)
	if got := ctrl.OriginalBitmap(original); got != nil {
		t.Fatal("invalid original unexpectedly decoded")
	}
	if removeOriginal {
		if err := os.Remove(original); err != nil {
			t.Fatal(err)
		}
	}
	m := engine.ClipboardMedia{Kind: "image", Path: original, ThumbPath: thumbPath, Width: 12, Height: 12}
	tt := ui.NewTester(func(c *ui.Context) {
		ctrl.renderClipboardMediaCanvas(c, ui.LightTheme(), m)
	}, 320, 180)
	if thumbPath != "" {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if bmp, ok := ctrl.cachedThumbBitmap(thumbPath); ok && bmp != nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
	tt.Frame()
	return tt
}

func previewImagePixels(tt *ui.Tester) int {
	img := tt.Image()
	count := 0
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a > 0 && r < 80<<8 && g > 120<<8 && b > 180<<8 {
				count++
			}
		}
	}
	return count
}

func TestMediaZoomClampAndModes(t *testing.T) {
	cases := []struct {
		in, want float32
	}{
		{-1, mediaZoomMin},
		{0.25, mediaZoomMin},
		{1, 1},
		{8, mediaZoomMax},
		{12, mediaZoomMax},
	}
	for _, tc := range cases {
		if got := clampMediaZoom(tc.in); got != tc.want {
			t.Fatalf("clampMediaZoom(%v)=%v, want %v", tc.in, got, tc.want)
		}
	}
	if got := mediaZoomPercent(MediaZoomFit, 4); got != 0 {
		t.Fatalf("fit percent=%d, want 0", got)
	}
	if got := mediaZoomPercent(MediaZoomOriginal, 4); got != 100 {
		t.Fatalf("original percent=%d, want 100", got)
	}
	if got := mediaZoomPercent(MediaZoomCustom, 99); got != 800 {
		t.Fatalf("custom percent=%d, want 800", got)
	}
}

func TestMediaPreviewSelectionResetsZoom(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	first := engine.OmniboxItem{ID: "one", Kind: engine.ItemClipboardEntry, ClipboardMedia: &engine.ClipboardMedia{Kind: "image", Path: "/one.png"}}
	ctrl.resetMediaPreview(first)
	ctrl.setMediaZoom(MediaZoomCustom, 3)
	ctrl.uiState.mediaPreview.panX, ctrl.uiState.mediaPreview.panY = 20, -5
	ctrl.resetMediaPreview(first)
	if ctrl.uiState.mediaPreview.mode != MediaZoomCustom || ctrl.uiState.mediaPreview.zoom != 3 {
		t.Fatal("same media selection unexpectedly reset zoom")
	}
	second := engine.OmniboxItem{ID: "two", Kind: engine.ItemClipboardEntry, ClipboardMedia: &engine.ClipboardMedia{Kind: "image", Path: "/two.png"}}
	ctrl.resetMediaPreview(second)
	p := ctrl.uiState.mediaPreview
	if p.mode != MediaZoomFit || p.zoom != 1 || p.panX != 0 || p.panY != 0 {
		t.Fatalf("selection change state=%+v, want fit with zero pan", p)
	}
}

func TestMediaPreviewKeyboardDoesNotHandleOmniboxKeys(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.uiState.mediaPreview.focused = true
	for _, key := range []ui.Key{ui.KeyUp, ui.KeyDown, ui.KeyEnter, ui.KeyEscape} {
		if ctrl.handleMediaPreviewInput(ui.InputEvent{Kind: ui.InputKeyDown, Key: key}) {
			t.Fatalf("media canvas claimed Omnibox key %v", key)
		}
	}
	if !ctrl.handleMediaPreviewInput(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.Key1}) {
		t.Fatal("media canvas did not handle original-size shortcut")
	}
	if ctrl.uiState.mediaPreview.mode != MediaZoomOriginal {
		t.Fatal("1 did not select original-size mode")
	}
}

func TestMediaPreviewOriginalFailureFallsBack(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	p := mediaPreviewState{}
	if got := ctrl.requestOriginalBitmap("/definitely/missing-media-preview.png", &p); got != nil {
		t.Fatal("missing original unexpectedly returned a bitmap")
	}
	if !p.loading {
		t.Fatal("missing original did not enter the loading state")
	}
}

func TestMediaPreviewMissingOriginalUsesThumbnail(t *testing.T) {
	thumb := filepath.Join(t.TempDir(), "thumb.png")
	if err := os.WriteFile(thumb, regressionPreviewPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	tt := renderMediaFallbackAfterFailure(t, thumb, true)
	if tt.HasText(i18n.T("preview.media.unavailable")) {
		t.Fatalf("missing original discarded a valid thumbnail; texts %q", tt.Texts())
	}
	if got := previewImagePixels(tt); got == 0 {
		t.Fatalf("missing original rendered no visible thumbnail; texts %q", tt.Texts())
	}
}

func TestMediaPreviewMissingOriginalAndThumbnailShowsUnavailable(t *testing.T) {
	tt := renderMediaFallbackAfterFailure(t, "", true)
	if !tt.HasText(i18n.T("preview.media.unavailable")) {
		t.Fatalf("both media files missing had no unavailable message; texts %q", tt.Texts())
	}
}

func TestMediaPreviewCorruptOriginalFallsBackWithoutPanic(t *testing.T) {
	thumb := filepath.Join(t.TempDir(), "thumb.png")
	if err := os.WriteFile(thumb, regressionPreviewPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	tt := renderMediaFallbackAfterFailure(t, thumb, false)
	if tt.HasText(i18n.T("preview.media.unavailable")) || previewImagePixels(tt) > 0 {
		return
	}
	t.Fatalf("corrupt original produced blank media canvas; texts %q", tt.Texts())
}

func TestMediaPreviewPointerZoomAndPan(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	if !ctrl.handleMediaPreviewInput(ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, Clicks: 2}) {
		t.Fatal("double click was not handled")
	}
	if ctrl.uiState.mediaPreview.mode != MediaZoomOriginal {
		t.Fatal("double click did not switch to original")
	}
	ctrl.uiState.mediaPreview.zoom = 2
	ctrl.uiState.mediaPreview.mode = MediaZoomCustom
	ctrl.handleMediaPreviewInput(ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, X: 10, Y: 10})
	ctrl.handleMediaPreviewInput(ui.InputEvent{Kind: ui.InputPointerMove, Button: 0, X: 30, Y: 18})
	ctrl.handleMediaPreviewInput(ui.InputEvent{Kind: ui.InputPointerUp, Button: 0})
	if ctrl.uiState.mediaPreview.panX != 20 || ctrl.uiState.mediaPreview.panY != 8 {
		t.Fatalf("pan=(%v,%v), want (20,8)", ctrl.uiState.mediaPreview.panX, ctrl.uiState.mediaPreview.panY)
	}
}
