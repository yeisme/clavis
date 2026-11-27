package uiapp

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestAppIconICOAndMultiSizeSet pins the multi-size icon set and the
// Windows .ico pack: every size decodes, the 512 entry stays byte-identical
// to GenerateAppIconPNG(512) (and thus resources/icon.png), and the ICO
// container resolves to the right PNGs. The .ico is written to resources/
// when it drifts, mirroring how icon.png is kept in sync.
func TestAppIconICOAndMultiSizeSet(t *testing.T) {
	set := GenerateAppIconPNGSet()
	for _, size := range AppIconSizes() {
		data, ok := set[size]
		if !ok || len(data) == 0 {
			t.Fatalf("icon set missing %d", size)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("png decode %d: %v", size, err)
		}
		if img.Bounds().Dx() != size || img.Bounds().Dy() != size {
			t.Fatalf("icon %d bounds = %v", size, img.Bounds())
		}
	}
	if !bytes.Equal(set[512], GenerateAppIconPNG(512)) {
		t.Fatal("set[512] differs from GenerateAppIconPNG(512); icon.png consistency broken")
	}

	ico := GenerateAppIconICO()
	if len(ico) == 0 {
		t.Fatal("GenerateAppIconICO returned empty bytes")
	}
	if ico[0] != 0 || ico[1] != 0 || ico[2] != 1 || ico[3] != 0 {
		t.Fatalf("ico header = %v, want reserved=0 type=1", ico[:4])
	}
	count := int(binary.LittleEndian.Uint16(ico[4:6]))
	if count != 7 {
		t.Fatalf("ico entry count = %d, want 7", count)
	}
	var wantSizes = map[int]bool{16: false, 24: false, 32: false, 48: false, 64: false, 128: false, 256: false}
	for i := 0; i < count; i++ {
		e := ico[6+16*i : 6+16*(i+1)]
		w := int(e[0])
		if w == 0 {
			w = 256
		}
		dataLen := int(binary.LittleEndian.Uint32(e[8:12]))
		off := int(binary.LittleEndian.Uint32(e[12:16]))
		if off+dataLen > len(ico) {
			t.Fatalf("ico entry %d (size %d) out of range", i, w)
		}
		img, err := png.Decode(bytes.NewReader(ico[off : off+dataLen]))
		if err != nil {
			t.Fatalf("ico entry %d is not a PNG: %v", i, err)
		}
		if img.Bounds().Dx() != w || img.Bounds().Dy() != w {
			t.Fatalf("ico entry %d bounds = %v, want %d", i, img.Bounds(), w)
		}
		wantSizes[w] = true
	}
	for size, seen := range wantSizes {
		if !seen {
			t.Fatalf("ico missing size %d", size)
		}
	}

	// Keep resources/icon.ico in sync with the generator.
	icoPath := filepath.Join("..", "resources", "icon.ico")
	if existing, err := os.ReadFile(icoPath); err != nil || !bytes.Equal(existing, ico) {
		if err := os.WriteFile(icoPath, ico, 0o644); err != nil {
			t.Fatalf("write %s: %v", icoPath, err)
		}
	}
}

// TestSemanticIconCoverageAndOpticalVariants verifies the icon set covers
// every name the engine, config defaults and views reference — with no
// silent bolt fallbacks — and that small targets get the heavier optical
// stroke variant.
func TestSemanticIconCoverageAndOpticalVariants(t *testing.T) {
	for _, name := range CoreSemanticIconNames() {
		if svg := SemanticIconSVG(name); svg == nil {
			t.Fatalf("SemanticIconSVG(%q) returned nil", name)
		}
	}
	// Names that previously fell back to bolt must now resolve to their
	// own geometry (distinct parsed SVG instances).
	for _, name := range []string{
		"chevron.right", "tag", "clipboard",
		"keyboard.badge.ellipsis", "keyboard.chevron.compact.down",
		"xmark", "checkmark", "chevron.down", "chevron.left", "ellipsis",
		"person.crop.circle", "info.circle", "questionmark",
	} {
		if SemanticIconSVG(name) == SemanticIconSVG("bolt") {
			t.Fatalf("%q still falls back to bolt", name)
		}
	}
	// Optical sizing: small targets draw the heavier-stroke variant.
	small := semanticIconSVGForSize("bolt", IconSize16)
	regular := semanticIconSVGForSize("bolt", IconSize24)
	if small == regular {
		t.Fatal("16 DIP variant should differ from the 24 DIP variant")
	}
	if semanticIconSVGForSize("bolt", IconSize20) != regular {
		t.Fatal("20 DIP should use the regular variant")
	}
}

func TestSemanticIconFallbackRendersNeutralInBothThemes(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			tt := ui.NewTesterWith(func(c *ui.Context) {
				ui.Box(c).Size(48, 48).Center().Children(func() {
					renderSemanticIcon(c, "missing-semantic-icon", IconSize24, c.Theme().Text)
				})
			}, 80, 80, ui.TesterOptions{Dark: dark})
			tt.Frame()
			if countOpaquePixels(tt.Image()) == 0 {
				t.Fatal("neutral fallback produced no visible pixels")
			}
			t.Logf("%s fallback rendered %d opaque pixels", name, countOpaquePixels(tt.Image()))
		})
	}
}

func countOpaquePixels(img *image.RGBA) int {
	n := 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if img.RGBAAt(x, y).A > 0 {
				n++
			}
		}
	}
	return n
}

func TestAppIconFollowsMacOSGrayscaleAndSystemBluePalette(t *testing.T) {
	raw := GenerateAppIconPNG(512)
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode 512x512 icon: %v", err)
	}
	blueDots := 0
	for y := range 512 {
		for x := range 512 {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A < 32 {
				continue
			}
			// Reject warm amber/gold spark pixels.
			if c.R > 210 && c.G > 140 && c.B < 100 {
				t.Fatalf("unexpected warm amber/gold pixel at (%d,%d): %+v", x, y, c)
			}
			// Reject purple/indigo gradient pixels.
			if int(c.B) > int(c.R)+40 && c.R > 40 && c.G < c.R {
				t.Fatalf("unexpected purple/indigo gradient pixel at (%d,%d): %+v", x, y, c)
			}
			if c.B > 220 && c.G > 110 && c.G < 160 && c.R < 35 {
				blueDots++
			}
		}
	}
	if blueDots == 0 {
		t.Fatal("expected #0A84FF system-blue indicator dot pixels in redesigned app icon")
	}
}
