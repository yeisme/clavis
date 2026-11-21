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
	if r.X < after.X {
		t.Fatalf("readout %q at x=%.1f sits before + (x=%.1f); it belongs at the trailing edge", readout, r.X, after.X)
	}
	t.Logf("Fit shown once; + stays at x=%.1f; readout %q at x=%.1f", plus.X, readout, r.X)
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
