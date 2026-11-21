package uiapp

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
)

// widePNG is a solid 16:9 image in a color nothing else in the UI uses, so
// its drawn width can be read off a render.
func widePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 64, 36))
	for x := range 64 {
		for y := range 36 {
			img.Set(x, y, color.NRGBA{R: 20, G: 200, B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// splitHandleX finds the divider by its resize cursor along row y.
func splitHandleX(tt *ui.Tester, y float32, from, to int) (float32, bool) {
	for x := from; x < to; x++ {
		tt.Move(float32(x), y)
		tt.Frame()
		if tt.Cursor() == ui.CursorResizeEW {
			return float32(x), true
		}
	}
	return 0, false
}

// drawnImageWidth is the width of the image's bounding box right of x0 —
// the preview pane; the list's row thumbnail shows the same image.
func drawnImageWidth(tt *ui.Tester, x0 float32) int {
	img := tt.Image()
	b := img.Bounds()
	lo, hi := b.Max.X, b.Min.X-1
	green := ui.RGBA(20, 200, 40, 1)
	for y := b.Min.Y; y < b.Max.Y; y += 2 {
		for x := max(b.Min.X, int(x0)+1); x < b.Max.X; x++ {
			if colorClose(rgbaAt(img, x, y), green) {
				lo, hi = min(lo, x), max(hi, x)
			}
		}
	}
	if hi < lo {
		return 0
	}
	return hi - lo + 1
}

// TestPreviewSplitDragsClampsResetsAndPersists renders the clipboard tab with
// a 16:9 image chosen and works the divider like a user:
//   - the default split gives the list 58%, the preview 42%
//   - dragging moves it; far left stops at 35%, far right at 75%
//   - a double click puts it back at 58%
//   - once the divider rests, ui-state.json holds the value and a new
//     controller on the same config directory starts from it
//   - a wider preview pane draws the image wider (the canvas follows the
//     pane rather than a fixed 300px box)
func TestPreviewSplitDragsClampsResetsAndPersists(t *testing.T) {
	f := newInteractionFixture(t, nil)
	mediaPath := filepath.Join(t.TempDir(), "wide.png")
	if err := os.WriteFile(mediaPath, widePNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	f.record(t, "first entry", "second entry")
	m := clipboard.Media{Kind: clipboard.KindImage, BlobSHA: "wide-sha", MediaPath: mediaPath, ThumbPath: mediaPath, Width: 3304, Height: 1782, ByteSize: 1300000}
	if _, _, err := f.clips.Record(context.Background(), clipboard.CaptureCandidate{Content: "wide", SourceAppName: "Preview", Timestamp: time.Now().Add(time.Minute), Media: m}); err != nil {
		t.Fatal(err)
	}
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	waitUntil(t, 2*time.Second, func() bool { return len(f.ctrl.State().OmniboxItems) >= 3 }, "clipboard items")
	for idx, it := range f.ctrl.State().OmniboxItems {
		if it.ClipboardMedia != nil && it.ClipboardMedia.IsMedia() {
			f.ctrl.SelectIndex(idx)
			break
		}
	}
	w, h := PreferredCapsuleSizeForState(f.ctrl.State(), f.ctrl.Config().Main.Appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	y := float32(h) / 2
	x0, total := space14, float32(w)-2*space14
	share := func(x float32) float32 { return (x - x0) / total * 100 }
	hx, ok := splitHandleX(tt, y, int(x0)+int(total*0.3), int(x0)+int(total*0.8))
	if !ok {
		t.Fatal("no divider handle (resize cursor) between the list and the preview")
	}
	if got := share(hx); got < defaultPreviewListPercent-3 || got > defaultPreviewListPercent+3 {
		t.Fatalf("default divider at %.1f%% of the body, want ~%v%%", got, defaultPreviewListPercent)
	}
	defaultW := drawnImageWidth(tt, hx)
	if defaultW == 0 {
		t.Fatal("the chosen image is not drawn in the preview")
	}
	drag := func(from, to float32) {
		tt.Press(from, y)
		tt.Move((from+to)/2, y)
		tt.Move(to, y)
		tt.Release(to, y)
		tt.Frame()
		tt.Advance(time.Second)
		tt.Frame()
	}

	drag(hx, x0)
	if got := f.ctrl.previewListPercent(); got != minPreviewListPercent {
		t.Fatalf("dragged far left: list %.1f%%, want clamped to %v%%", got, minPreviewListPercent)
	}
	leftX, _ := splitHandleX(tt, y, int(x0), int(x0)+int(total*0.5))
	wideW := drawnImageWidth(tt, leftX)

	hx, _ = splitHandleX(tt, y, int(x0)+int(total*0.2), int(x0)+int(total*0.6))
	drag(hx, float32(w))
	if got := f.ctrl.previewListPercent(); got != maxPreviewListPercent {
		t.Fatalf("dragged far right: list %.1f%%, want clamped to %v%%", got, maxPreviewListPercent)
	}
	rightX, _ := splitHandleX(tt, y, int(x0)+int(total*0.5), w)
	narrowW := drawnImageWidth(tt, rightX)
	if narrowW == 0 || wideW <= narrowW+40 {
		t.Fatalf("image drawn %dpx wide with the preview at 65%% and %dpx at 25%%: it must grow with the pane", wideW, narrowW)
	}

	hx, ok = splitHandleX(tt, y, int(x0)+int(total*0.6), int(x0)+int(total*0.9))
	if !ok {
		t.Fatal("divider handle lost after dragging right")
	}
	tt.ClickAt(hx, y)
	tt.ClickAt(hx, y)
	tt.Frame()
	if got := f.ctrl.previewListPercent(); got != defaultPreviewListPercent {
		t.Fatalf("double click: list %.1f%%, want the default %v%%", got, defaultPreviewListPercent)
	}

	hx, _ = splitHandleX(tt, y, int(x0)+int(total*0.4), int(x0)+int(total*0.75))
	drag(hx, x0+total*0.45)
	want := f.ctrl.previewListPercent()
	waitUntil(t, 3*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(f.ctrl.ConfigStore.Directory(), uiStateFileName))
		return err == nil
	}, "ui-state.json written")
	data, err := os.ReadFile(filepath.Join(f.ctrl.ConfigStore.Directory(), uiStateFileName))
	if err != nil {
		t.Fatal(err)
	}
	var saved persistedUIState
	if err := json.Unmarshal(data, &saved); err != nil || saved.PreviewListPercent != want {
		t.Fatalf("ui-state.json = %s (err %v), want previewListPercent %v", data, err, want)
	}
	again := NewAppController(Options{
		ConfigStore:    config.NewConfigStore(f.ctrl.ConfigStore.Directory()),
		ClipboardStore: clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		SecretStore:    secrets.NewMemoryAPIKeyStore(),
		Bridge:         newMockBridge(),
	})
	defer again.Close()
	if got := again.previewListPercent(); got != want {
		t.Fatalf("a new controller starts at %.1f%%, want the saved %v%%", got, want)
	}
	t.Logf("default %.0f%%: image %dpx wide; clamps 35/75: image %dpx (preview 65%%) vs %dpx (25%%); saved %.1f%%", defaultPreviewListPercent, defaultW, wideW, narrowW, want)
}

func TestClampPreviewListPercent(t *testing.T) {
	for in, want := range map[float32]float32{0: 58, -5: 58, 10: 35, 35: 35, 50: 50, 75: 75, 99: 75} {
		if got := clampPreviewListPercent(in); got != want {
			t.Errorf("clampPreviewListPercent(%v) = %v, want %v", in, got, want)
		}
	}
}
