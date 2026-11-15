package uiapp

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

// TestTrayMenuGlyphsFillTheirIcon: each tray menu glyph is drawn centered
// on its 16×16 icon, about two thirds of it wide, with solid strokes. The
// generator once mapped pixels onto [-8, 24] and sampled two points per
// pixel out of four, so every glyph sat in the top-left 6×6 pixels at half
// opacity.
func TestTrayMenuGlyphsFillTheirIcon(t *testing.T) {
	kinds := []TrayMenuIconKind{
		TrayMenuIconCapsule, TrayMenuIconClipboard, TrayMenuIconReload, TrayMenuIconFolder,
		TrayMenuIconSettings, TrayMenuIconQuit, TrayMenuIconGuide,
	}
	for _, kind := range kinds {
		img, err := png.Decode(bytes.NewReader(GenerateTrayMenuIconPNG(kind, 16)))
		if err != nil {
			t.Fatalf("%s: decode: %v", kind, err)
		}
		box, peak := alphaBounds(img, 40)
		if box.Empty() {
			t.Fatalf("%s: nothing drawn", kind)
		}
		cx := float64(box.Min.X+box.Max.X) / 2
		cy := float64(box.Min.Y+box.Max.Y) / 2
		if cx < 6.5 || cx > 9.5 || cy < 6.5 || cy > 9.5 {
			t.Errorf("%s: drawn around (%.1f, %.1f) in %v, want the icon's center (8, 8)", kind, cx, cy, box)
		}
		if box.Dx() < 9 || box.Dy() < 9 {
			t.Errorf("%s: drawn %d×%d px, want at least 9×9 of 16×16", kind, box.Dx(), box.Dy())
		}
		if peak < 230 {
			t.Errorf("%s: strongest pixel alpha %d, want solid strokes", kind, peak)
		}
	}
}

// alphaBounds is the bounding box of the pixels more opaque than min, and
// the highest alpha.
func alphaBounds(img image.Image, min uint32) (image.Rectangle, uint32) {
	var box image.Rectangle
	var peak uint32
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			a >>= 8
			peak = max(peak, a)
			if a > min {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return box, peak
}
