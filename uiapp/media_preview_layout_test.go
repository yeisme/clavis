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
