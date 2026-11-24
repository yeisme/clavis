package uiapp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// TestMediaPreviewStaysAboveFooter renders the clipboard tab with an image
// chosen, at the window size the app uses, and checks the preview card's
// last lines — the image metadata and the action hints — end above the
// footer instead of running under its buttons (the 300px canvas used to push
// the card ~100px past the list).
func TestMediaPreviewStaysAboveFooter(t *testing.T) {
	f, w, h := mediaPreviewFixture(t)
	for _, dark := range []bool{false, true} {
		tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
		tt.SetDark(dark)
		tt.Frame()
		tt.Advance(2 * time.Second)
		tt.Frame()

		footer, ok := tt.Find(modPrefix() + i18n.T("omnibox.footer.preview"))
		if !ok {
			t.Fatalf("footer preview toggle missing; texts %q", tt.Texts())
		}
		meta := clipboardMediaMetaRows(engine.ClipboardMedia{Width: 1920, Height: 1080, ByteSize: 2400000})[0]
		got, ok := tt.Find(meta)
		if !ok {
			t.Fatalf("media metadata %q not drawn; texts %q", meta, tt.Texts())
		}
		// The footer row starts at least a divider and a gap above its
		// labels; the preview must end before that.
		if got.Y+got.H > footer.Y-space8 {
			t.Fatalf("dark=%v: metadata %q ends at y=%.1f, under the footer starting near y=%.1f", dark, meta, got.Y+got.H, footer.Y)
		}
		hint, ok := tt.Find(i18n.T("engine.action.paste"))
		if ok && hint.Y+hint.H > footer.Y-space8 {
			t.Fatalf("dark=%v: action hint ends at y=%.1f, under the footer near y=%.1f", dark, hint.Y+hint.H, footer.Y)
		}
		t.Logf("dark=%v window %dx%d: metadata ends y=%.1f, footer labels at y=%.1f", dark, w, h, got.Y+got.H, footer.Y)
	}
}

// TestMediaPreviewToolbarSaysFitOnce renders the image preview: in Fit mode
// "Fit" appears once (the lit pill; the readout between − and + used to
// repeat it), and zooming in shows a percentage at the trailing edge without
// moving the "+" button under the pointer.
func TestMediaPreviewToolbarSaysFitOnce(t *testing.T) {
	f, w, h := mediaPreviewFixture(t)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()
	tt.Advance(2 * time.Second)
	tt.Frame()

	fit := i18n.T("preview.media.fit")
	count := 0
	for _, s := range tt.Texts() {
		if s == fit {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("Fit mode shows %q %d times, want once; texts %q", fit, count, tt.Texts())
	}

	plus, ok := tt.Find("+")
	if !ok {
		t.Fatal("zoom + button missing")
	}
	if err := tt.Click("+"); err != nil {
		t.Fatalf("click +: %v", err)
	}
	tt.Advance(time.Second)
	tt.Frame()
	after, _ := tt.Find("+")
	if after.X != plus.X {
		t.Fatalf("+ moved from x=%.1f to x=%.1f when the readout appeared", plus.X, after.X)
	}
	readout := mediaZoomLabel(f.ctrl.uiState.mediaPreview.mode, f.ctrl.uiState.mediaPreview.zoom)
	r, ok := tt.Find(readout)
	if !ok || readout == fit {
		t.Fatalf("zoomed in, readout %q not shown as a percentage; texts %q", readout, tt.Texts())
	}
	if r.X > after.X {
		t.Fatalf("readout %q at x=%.1f sits after + (x=%.1f); it belongs between − and +", readout, r.X, after.X)
	}
	t.Logf("Fit shown once; + stays at x=%.1f; readout %q at x=%.1f", plus.X, readout, r.X)
}

func TestMediaPreviewSpecFrameKeepsWideImageReadable(t *testing.T) {
	f, _, _ := mediaPreviewFixture(t)
	const width, height = 720, 536
	tt := ui.NewTester(f.ctrl.CapsuleView, width, height)
	tt.Frame()
	tt.Advance(2 * time.Second)
	tt.Frame()
	handle, ok := splitHandleX(tt, float32(height)/2, 200, width-100)
	if !ok {
		t.Fatal("spec frame has no draggable preview divider")
	}
	got := float32(width) - handle - space8
	if got < 420 {
		t.Fatalf("720×536 preview net width = %.0fpt, want at least 420pt", got)
	}
	t.Logf("720×536 preview divider %.1fpt, preview net width %.0fpt", handle, got)
}

// mediaPreviewFixture opens the clipboard tab with an image entry chosen and
// returns the window size the app would use.
func mediaPreviewFixture(t *testing.T) (*interactionFixture, int, int) {
	t.Helper()
	f := newInteractionFixture(t, nil)
	mediaPath := filepath.Join(t.TempDir(), "preview.png")
	if err := os.WriteFile(mediaPath, testPNG16(t), 0o600); err != nil {
		t.Fatal(err)
	}
	f.record(t, "first entry", "second entry", "third entry")
	m := clipboard.Media{Kind: clipboard.KindImage, BlobSHA: "deck-sha", MediaPath: mediaPath, ThumbPath: mediaPath, Width: 1920, Height: 1080, ByteSize: 2400000}
	if _, _, err := f.clips.Record(context.Background(), clipboard.CaptureCandidate{Content: "deck", SourceAppName: "Photos", Timestamp: time.Now().Add(time.Minute), Media: m}); err != nil {
		t.Fatal(err)
	}
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	waitUntil(t, 2*time.Second, func() bool { return len(f.ctrl.State().OmniboxItems) >= 4 }, "clipboard items")
	for idx, it := range f.ctrl.State().OmniboxItems {
		if it.ClipboardMedia != nil && it.ClipboardMedia.IsMedia() {
			f.ctrl.SelectIndex(idx)
			break
		}
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if bmp, ok := f.ctrl.cachedThumbBitmap(mediaPath); ok && bmp != nil {
			break
		}
		f.ctrl.ThumbBitmap(mediaPath)
		time.Sleep(time.Millisecond)
	}
	w, h := PreferredCapsuleSizeForState(f.ctrl.State(), f.ctrl.Config().Main.Appearance)
	return f, w, h
}

// TestPreviewCardTitleIconIsNeutral reads the preview card's title row off a
// real render, light and dark: the icon before "Preview" is a label, so no
// pixel left of the title may be painted in the accent.
func TestPreviewCardTitleIconIsNeutral(t *testing.T) {
	f, w, h := mediaPreviewFixture(t)
	for _, dark := range []bool{false, true} {
		tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
		tt.SetDark(dark)
		tt.Frame()
		title, ok := tt.Find(i18n.T("preview.title"))
		if !ok {
			t.Fatalf("preview title missing; texts %q", tt.Texts())
		}
		accent := ui.LightTheme().Accent
		if dark {
			accent = ui.DarkTheme().Accent
		}
		if n := accentPixels(tt, int(title.X)-int(space24), int(title.Y), int(title.X), int(title.Y+title.H)+1, accent); n != 0 {
			t.Fatalf("dark=%v: preview title icon paints %d accent pixels; it must be neutral", dark, n)
		}
	}
}

// TestClipboardThumbnailsFillTheLeadingSlot renders the clipboard list with an
// image entry: its thumbnail is drawn rowLeadingSlot wide (it was the 26px
// icon tile), the titles of image and text rows start at the same x (icon
// tiles centre in the same slot), and the row pitch stays within the row
// budget the window is sized by.
func TestClipboardThumbnailsFillTheLeadingSlot(t *testing.T) {
	f, w, h := mediaPreviewFixture(t)
	// A text row whose title starts with the same glyph as the image row's,
	// so the first ink pixel compares layout, not side bearings.
	f.record(t, "Image notes")
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	waitUntil(t, 2*time.Second, func() bool {
		for _, it := range f.ctrl.State().OmniboxItems {
			if it.Title == "Image notes" {
				return true
			}
		}
		return false
	}, "text row")
	for idx, it := range f.ctrl.State().OmniboxItems {
		if it.ClipboardMedia != nil && it.ClipboardMedia.IsMedia() {
			f.ctrl.SelectIndex(idx)
			break
		}
	}
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	// Reduce Motion flattens the stage: the spatial preset skews resting
	// rows a few px, which would move their titles apart regardless of the
	// leading slot.
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	img := tt.Image()
	media, ok := tt.Find("Image 1920×1080")
	text, ok2 := tt.Find("Image notes")
	third, ok3 := tt.Find("third entry")
	second, ok4 := tt.Find("second entry")
	if !ok || !ok2 || !ok3 || !ok4 {
		t.Fatalf("rows missing; texts %q", tt.Texts())
	}
	// The thumbnail (testPNG16: blue 200 under an R/G gradient) along the
	// media row's middle.
	y := int(media.Y + media.H/2)
	lo, hi := -1, -1
	for x := int(media.X); x < int(media.X)+80; x++ {
		c := img.RGBAAt(x, y)
		if c.B >= 185 && c.B <= 215 && c.R < 200 {
			if lo < 0 {
				lo = x
			}
			hi = x
		}
	}
	if lo < 0 || hi-lo+1 < int(rowLeadingSlot)-2 {
		t.Fatalf("thumbnail drawn %dpx wide (x %d..%d), want ~%vpx", hi-lo+1, lo, hi, rowLeadingSlot)
	}
	// Title start: first text-colored pixel right of the slot, upper half.
	titleX := func(row ui.Rect) int {
		ink := ui.LightTheme().Text
		for x := int(row.X) + int(rowLeadingSlot); x < int(row.X+row.W); x++ {
			for yy := int(row.Y) + 4; yy < int(row.Y+row.H/2); yy++ {
				if colorClose(rgbaAt(img, x, yy), ink) {
					return x
				}
			}
		}
		return -1
	}
	if a, b := titleX(media), titleX(text); a < 0 || a != b {
		t.Fatalf("titles start at x=%d (image row) and x=%d (text row); they must align", a, b)
	}
	// "third entry" was captured just after "second entry": adjacent rows.
	pitch := second.Y - third.Y
	if pitch <= 0 || pitch > rowHeightOmnibox+rowGap {
		t.Fatalf("row pitch %.1f outside (0, %v], the row budget the window is sized by", pitch, rowHeightOmnibox+rowGap)
	}
	t.Logf("thumbnail %dpx; titles at x=%d; row pitch %.1f", hi-lo+1, titleX(text), pitch)
}
