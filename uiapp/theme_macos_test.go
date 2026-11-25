package uiapp

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
)

// TestMacThemeHardMetrics pins the owner's visual anchor on the palette every
// window paints with: macOS grayscale layers, one system blue in both
// appearances whatever the desktop accent is, opaque reading surfaces, and
// text that clears its contrast floors on each layer.
func TestMacThemeHardMetrics(t *testing.T) {
	purple := ui.Hex("#AF52DE")
	for _, dark := range []bool{false, true} {
		th := macTheme(dark, ui.Preferences{Accent: purple})
		if th.Accent != ui.Hex("#0A84FF") {
			t.Fatalf("dark=%v: accent %v follows the desktop; want the single system blue #0A84FF", dark, th.Accent)
		}
		wantContent, wantText := ui.Hex("#FFFFFF"), ui.Hex("#1D1D1F")
		if dark {
			wantContent, wantText = ui.Hex("#1C1C1E"), ui.Hex("#F5F5F7")
		}
		if th.Background != wantContent || th.Text != wantText {
			t.Fatalf("dark=%v: content %v / text %v, want %v / %v", dark, th.Background, th.Text, wantContent, wantText)
		}
		if got := contentSurface(th); got.A != 255 {
			t.Fatalf("dark=%v: content surface alpha %d, want fully opaque", dark, got.A)
		}
		if got := shellGlassTint(th); got.A == 0 || got.A == 255 {
			t.Fatalf("dark=%v: shell glass tint alpha %d, want translucent glass", dark, got.A)
		}
		if th.Surface == th.Background {
			t.Fatalf("dark=%v: elevated layer equals the content layer — no grayscale layering", dark)
		}
		for _, bg := range []ui.Color{th.Background, th.Surface} {
			if r := contrastRatio(th.Text, bg); r < 12 {
				t.Fatalf("dark=%v: primary text %.2f:1 on %v, want ≥ 12:1", dark, r, bg)
			}
			if r := contrastRatio(th.TextMuted, bg); r < minTextContrast {
				t.Fatalf("dark=%v: secondary text %.2f:1 on %v, want ≥ 4.5:1", dark, r, bg)
			}
		}
		for name, c := range map[string]ui.Color{"danger": th.Danger, "warning": th.Warning, "success": th.Success} {
			if r := contrastRatio(statusInk(th, c), th.Background); r < minTextContrast {
				t.Fatalf("dark=%v: %s text %.2f:1, want ≥ 4.5:1", dark, name, r)
			}
		}
	}
}

// TestCapsuleBodyIsOpaque renders the Omnibox for real over a magenta
// backdrop (standing in for a loud wallpaper) and reads the pixels: the glass
// title bar lets the backdrop tint through, while no pixel of the reading area
// under it carries any magenta — the body is opaque, never glass.
func TestCapsuleBodyIsOpaque(t *testing.T) {
	magenta := ui.RGBA(255, 0, 255, 1)
	for _, dark := range []bool{false, true} {
		f := newInteractionFixture(t, nil)
		_ = f.ctrl.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.VisualPreset = "liquid_glass" })
		f.ctrl.OpenOmnibox(config.TabActions, "")
		w, h := PreferredCapsuleSizeForState(f.ctrl.State(), f.ctrl.Config().Main.Appearance)
		tt := ui.NewTester(func(c *ui.Context) {
			f.ctrl.CapsuleView(c)
			c.Root().Background(magenta)
		}, w, h)
		tt.SetDark(dark)
		tt.Frame()
		img := tt.Image()
		b := img.Bounds()
		scale := float32(b.Dx()) / float32(w)
		// Stay clear of the rounded corners and the 1px shell edge.
		inset := int(float32(radiusCard+space4) * scale)
		bodyTop := int(float32(headerHeight+space4) * scale)
		tinted := 0
		for y := bodyTop; y < b.Max.Y-inset; y++ {
			for x := inset; x < b.Max.X-inset; x++ {
				if magentaTinted(img.At(x, y)) {
					tinted++
				}
			}
		}
		if tinted > 0 {
			t.Fatalf("dark=%v: %d body pixels show the backdrop through; the reading area must be opaque", dark, tinted)
		}
		headerY := int(float32(headerHeight/2) * scale)
		glassy := 0
		for x := inset; x < b.Max.X-inset; x++ {
			if magentaTinted(img.At(x, headerY)) {
				glassy++
			}
		}
		if glassy == 0 {
			t.Fatalf("dark=%v: no backdrop shows through the title bar; the floating shell should stay glass", dark)
		}
	}
}

// magentaTinted reports a pixel whose green sits clearly below both red and
// blue: the signature of the magenta backdrop bleeding through. Clavis's own
// colors (grays, the system blue and its tints) never have it.
func magentaTinted(c color.Color) bool {
	p := color.NRGBAModel.Convert(c).(color.NRGBA)
	return int(p.R)-int(p.G) > 8 && int(p.B)-int(p.G) > 8
}
