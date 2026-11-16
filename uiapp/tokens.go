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

// Extended spacing scale. The original 2–16 ramp covered the transient
// surfaces; these steps cover the wider radii, empty-state padding and card
// insets the persistent surfaces need, so no view has to invent a number.
const (
	space0  float32 = 0
	space1  float32 = 1
	space18 float32 = 18
	space20 float32 = 20
	space24 float32 = 24
	space32 float32 = 32
)

// Opacity steps for translucent fills and tints. These are deliberately NOT
// expressed as a ratio of the spacing ramp: a tint must not change when the
// spacing scale moves, and "space2/space8" hides the fact that it is a ratio.
const (
	alphaFaint  float32 = 0.06 // resting tile washes
	alphaSubtle float32 = 0.14 // selected-row and status tints
	alphaSoft   float32 = 0.24 // chosen-row fill in dark mode
	alphaHalf   float32 = 0.50 // keycap and chip outlines
	alphaStrong float32 = 0.70 // focus rings and emphasis borders
)

// Radii beyond the original set: markers, inline controls, panels and the
// two-segment pill. Everything else keeps the 5/6/7/9/10/999 ramp above.
const (
	radius4       float32 = 4  // underline, marker-pen edge
	radius8       float32 = 8  // inline controls: fields, steppers, swatches
	radius12      float32 = 12 // panels and stage cards
	radiusSegment float32 = 11 // pill segments (28x22 caps)
)

// Type scale (points). typeSearch is the Spotlight-sized query text.
const (
	typeMicro  float32 = 10
	typeMeta   float32 = 11
	typeLabel  float32 = 12
	typeBody   float32 = 13
	typeTitle  float32 = 14
	typeSearch float32 = 16
	typeGlyph  float32 = 17 // geometric glyphs: emoji, calculator output
	typeHero   float32 = 20 // onboarding and section hero titles
)

// Line-height multipliers. Headings and single-line labels stay tight; prose
// (help text, permission purposes, preview bodies) gets room to be read.
const (
	lineTight float32 = 1.25
	lineProse float32 = 1.55
)

// Elevation is one step of the shadow scale. Levels are semantic, not pixel
// values: elev1 rests, elev2 is a card on the desktop, elev3 is a card under
// the pointer, elev4 is a floating stage surface.
type elevation struct {
	X, Y, Blur, Spread float32
}

// Elevation levels, from flat to floating.
const (
	elevNone = iota
	elev1
	elev2
	elev3
	elev4
)

// elevationSteps is the geometry of each level: the shadow grows down and out
// monotonically so a rising card reads as rising without a colour change.
var elevationSteps = [5]elevation{
	elevNone: {},
	elev1:    {X: 0, Y: 1, Blur: 2, Spread: 0},
	elev2:    {X: 0, Y: 3, Blur: 10, Spread: 0},
	elev3:    {X: 0, Y: 8, Blur: 24, Spread: 0},
	elev4:    {X: 0, Y: 16, Blur: 40, Spread: 0},
}

// elevationAlpha is the shadow opacity per level. Dark surfaces need more
// alpha than light ones to read as the same depth.
var (
	elevationAlphaLight = [5]float32{elevNone: 0, elev1: 0.10, elev2: 0.14, elev3: 0.18, elev4: 0.22}
	elevationAlphaDark  = [5]float32{elevNone: 0, elev1: 0.28, elev2: 0.34, elev3: 0.40, elev4: 0.46}
)

// elevationStep returns the shadow geometry of a level, clamped into range so
// a caller cannot ask for elev9 and silently get no shadow.
func elevationStep(level int) elevation {
	if level < elevNone {
		level = elevNone
	}
	if level > elev4 {
		level = elev4
	}
	return elevationSteps[level]
}

// elevationShadow resolves a level to concrete Shadow() arguments for the
// theme. A flat level yields a transparent shadow rather than a zero blur, so
// callers can apply it unconditionally.
func elevationShadow(t *ui.Theme, level int) (x, y, blur, spread float32, c ui.Color) {
	if level <= elevNone || t == nil {
		return 0, 0, 0, 0, ui.Transparent
	}
	if level > elev4 {
		level = elev4
	}
	e := elevationStep(level)
	alpha := elevationAlphaLight[level]
	if t.Dark {
		alpha = elevationAlphaDark[level]
	}
	return e.X, e.Y, e.Blur, e.Spread, ui.RGBA(0, 0, 0, alpha)
}

// surfaceHighlight is the 1px inset top highlight that separates a raised
// card from the surface behind it. It is deliberately opaque-ish in light mode
// and a whisper in dark mode, matching how macOS draws card chrome.
func surfaceHighlight(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.08)
	}
	return ui.RGBA(255, 255, 255, 0.75)
}

// Density metrics shared by the Capsule and the Settings Center. These replace
// the literals the views used to inline (header 40, search 52, row 44, footer
// 44) and are the numbers layout.go's Preferred* formulas build on.
const (
	headerHeight     float32 = 40
	searchRowHeight  float32 = 52
	rowHeightOmnibox float32 = 44
	footerHeight     float32 = 44
	controlHeight    float32 = 28
	cardPadX         float32 = 12
	cardPadY         float32 = 10
	cardGap          float32 = 10
	railWidth        float32 = 216
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
