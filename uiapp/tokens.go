package uiapp

import (
	"math"
	"sync"

	"github.com/egoist/mygo/ui"
)

// Design tokens shared by the transient surfaces (Capsule, Leader, floating
// pill, HUD). Views take spacing, radii, type sizes and semantic colors from
// here instead of inlining magic numbers, so the rhythm stays consistent and
// legibility is checked in one place (tokens_test.go).

// Spacing scale (DIPs).
const (
	space2  float32 = 2
	space4  float32 = 4
	space6  float32 = 6
	space8  float32 = 8
	space10 float32 = 10
	space12 float32 = 12
	space14 float32 = 14
	space16 float32 = 16
)

// Corner radii: keycaps are the tightest, rows and cards progressively
// softer, pills fully round.
const (
	radiusKeycap float32 = 5
	radiusChip   float32 = 6
	radiusTile   float32 = 7
	radiusRow    float32 = 9
	radiusCard   float32 = 10
	radiusPill   float32 = 999
)

// Type scale (points). typeSearch is the Spotlight-sized query text.
const (
	typeMicro  float32 = 10
	typeMeta   float32 = 11
	typeLabel  float32 = 12
	typeBody   float32 = 13
	typeTitle  float32 = 14
	typeSearch float32 = 16
)

// Row metrics for the Omnibox result list.
const (
	rowIconTile float32 = 26
	rowIconSize float32 = 15
)

// minTextContrast and minGlyphContrast are the WCAG 2 thresholds the tokens
// are held to: body text 4.5:1, icons and large/bold glyphs 3:1.
const (
	minTextContrast  = 4.5
	minGlyphContrast = 3.0
)

// selectionFill is the soft tint behind the chosen row. It stays light
// enough that icons, thumbnails and match highlights keep their own colors
// on top of it (a solid accent fill would swallow accent-colored content).
func selectionFill(t *ui.Theme) ui.Color {
	if t.Dark {
		return t.Accent.Alpha(0.24)
	}
	return t.Accent.Alpha(0.14)
}

// selectionEdge is the hairline that crisps the chosen row against glass.
func selectionEdge(t *ui.Theme) ui.Color {
	if t.Dark {
		return t.Accent.Alpha(0.55)
	}
	return t.Accent.Alpha(0.40)
}

// hairline is the low-contrast separator/border used on glass surfaces.
func hairline(t *ui.Theme) ui.Color { return t.Border.Alpha(0.6) }

// matchMark is the marker-pen background behind fuzzy/pinyin matched
// characters; the glyphs themselves keep the readable text color. The tint
// backs off for bright accents (a yellow mark under white text in dark mode)
// until the text still reads at 4.5:1 on a chosen row, the worst case.
func matchMark(t *ui.Theme) ui.Color { return derivedPalette(t).matchMark }

func computeMatchMark(t *ui.Theme) ui.Color {
	alpha := float32(0.20)
	if t.Dark {
		alpha = 0.38
	}
	worst := selectionFill(t).Over(surfaceBase(t))
	for ; alpha > 0.08; alpha -= 0.04 {
		mark := t.Accent.Alpha(alpha)
		if contrastRatio(t.Text, mark.Over(worst)) >= minTextContrast {
			return mark
		}
	}
	return t.Accent.Alpha(0.08)
}

// selectedTileFill is the accent tint of the chosen row's icon tile.
func selectedTileFill(t *ui.Theme) ui.Color { return t.Accent.Alpha(0.18) }

// selectedTileGlyph is the icon color on the chosen row's tile: the accent,
// pushed toward the text color until it reads at 3:1 on that tile (the tile
// sits on the row tint, so it is darker/brighter than the bare surface).
func selectedTileGlyph(t *ui.Theme) ui.Color { return derivedPalette(t).selectedTileGlyph }

func computeSelectedTileGlyph(t *ui.Theme) ui.Color {
	tileBg := selectedTileFill(t).Over(selectionFill(t).Over(surfaceBase(t)))
	return readableOn(t.Accent, t.Text, tileBg, minGlyphContrast)
}

// keycapFill is the face of a keycap: slightly raised from the surface.
func keycapFill(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.07)
	}
	return ui.RGBA(255, 255, 255, 0.85)
}

// keycapEdge is the keycap outline; keycapLip the 1px bottom edge that reads
// as key travel.
func keycapEdge(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.16)
	}
	return t.Border.Alpha(0.95)
}

func keycapLip(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(0, 0, 0, 0.45)
	}
	return t.Border
}

// tileFill is the neutral tile behind a row's leading icon.
func tileFill(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.06)
	}
	return ui.RGBA(0, 0, 0, 0.045)
}

// surfaceBase approximates what sits behind the content of a glass surface
// for contrast purposes: glass over an arbitrary wallpaper settles close to
// the theme background once vibrancy tints it.
func surfaceBase(t *ui.Theme) ui.Color { return t.Background }

// accentInk is the accent made safe for text and glyphs on bg: it keeps the
// accent hue but blends toward the theme text color until it reaches 4.5:1,
// so a yellow or graphite system accent never yields unreadable highlights.
func accentInk(t *ui.Theme, bg ui.Color) ui.Color {
	if bg == surfaceBase(t) {
		return derivedPalette(t).accentInk
	}
	return readableOn(t.Accent, t.Text, bg, minTextContrast)
}

// accentGlyph is accentInk's icon counterpart (3:1 is enough for glyphs).
func accentGlyph(t *ui.Theme, bg ui.Color) ui.Color {
	if bg == surfaceBase(t) {
		return derivedPalette(t).accentGlyph
	}
	return readableOn(t.Accent, t.Text, bg, minGlyphContrast)
}

// palette holds the contrast-derived colors of a theme. Deriving them runs
// WCAG luminance math per candidate, so the views fetch them once per theme
// (cached below) instead of per row and per frame.
type palette struct {
	matchMark         ui.Color
	selectedTileGlyph ui.Color
	accentInk         ui.Color
	accentGlyph       ui.Color
}

// paletteKey is what the derived colors depend on.
type paletteKey struct {
	accent, text, background ui.Color
	dark                     bool
}

var paletteCache struct {
	sync.Mutex
	valid bool
	key   paletteKey
	p     palette
}

// derivedPalette returns t's derived colors, recomputing them only when the
// theme's accent, text, background or appearance changed (one entry: the
// UI uses one theme at a time, and a system accent change just refills it).
func derivedPalette(t *ui.Theme) palette {
	key := paletteKey{accent: t.Accent, text: t.Text, background: t.Background, dark: t.Dark}
	paletteCache.Lock()
	defer paletteCache.Unlock()
	if paletteCache.valid && paletteCache.key == key {
		return paletteCache.p
	}
	bg := surfaceBase(t)
	p := palette{
		matchMark:         computeMatchMark(t),
		selectedTileGlyph: computeSelectedTileGlyph(t),
		accentInk:         readableOn(t.Accent, t.Text, bg, minTextContrast),
		accentGlyph:       readableOn(t.Accent, t.Text, bg, minGlyphContrast),
	}
	paletteCache.valid, paletteCache.key, paletteCache.p = true, key, p
	return p
}

// readableOn blends c toward toward (in 10% steps) until it reaches min
// contrast against bg, returning the first color that does.
func readableOn(c, toward, bg ui.Color, min float64) ui.Color {
	for step := 0; step <= 10; step++ {
		cand := c.Mix(toward, float32(step)/10)
		if contrastRatio(cand, bg) >= min {
			return cand
		}
	}
	return toward
}

// contrastRatio is the WCAG 2 contrast ratio of fg drawn over bg; a
// translucent fg is composited over bg first, a translucent bg is assumed
// to sit on its own opaque color.
func contrastRatio(fg, bg ui.Color) float64 {
	bg.A = 255
	if fg.A < 255 {
		fg = fg.Over(bg)
	}
	l1, l2 := relativeLuminance(fg), relativeLuminance(bg)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// relativeLuminance is the WCAG 2 relative luminance of an opaque sRGB color.
func relativeLuminance(c ui.Color) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}
