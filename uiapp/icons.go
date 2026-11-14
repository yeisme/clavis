package uiapp

import (
	"fmt"
	"runtime"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/sfsymbols"
	"github.com/egoist/mygo/ui"
)

// Optical icon sizes the UI draws at. Vector icons are authored once in a
// 24-unit grid; the optical variants compensate stroke weight per size so
// 16 DIP icons stay crisp beside 13px text.
const (
	IconSize16 float32 = 16
	IconSize20 float32 = 20
	IconSize24 float32 = 24
)

// CoreSemanticIconNames lists all semantic icon names required across Leader, Omnibox, and Settings.
func CoreSemanticIconNames() []string {
	return []string{
		"sparkles",
		"wand.and.stars",
		"globe",
		"doc.on.clipboard",
		"keyboard",
		"terminal",
		"macwindow",
		"rectangle.lefthalf.filled",
		"rectangle.righthalf.filled",
		"rectangle.tophalf.filled",
		"rectangle.bottomhalf.filled",
		"arrow.up.left.and.arrow.down.right",
		"rectangle.center.inset.filled",
		"display.next",
		"display.previous",
		"arrow.uturn",
		"folder",
		"text.badge.plus",
		"text.badge.checkmark",
		"gearshape",
		"bolt",
		"character.book.closed",
		"app.badge",
		"calculator",
		"face.smiling",
		"pin.fill",
		"search",
		"command",
		// navigation & status
		"chevron.right",
		"chevron.left",
		"chevron.down",
		"xmark",
		"checkmark",
		"ellipsis",
		// clipboard & labels
		"clipboard",
		"tag",
		// IME rule variants
		"keyboard.badge.ellipsis",
		"keyboard.chevron.compact.down",
	}
}

// semanticIconBodies holds the vector geometry of each icon as the inner
// markup of a 24×24 SVG (paths only; the outer element and its stroke
// attributes are generated per optical size in svgFor). All shapes inherit
// the text color via stroke="currentColor" on the root.
var semanticIconBodies = map[string]string{
	"sparkles":                           `<path d="M12 3l1.9 5.8a2 2 0 0 0 1.3 1.3L21 12l-5.8 1.9a2 2 0 0 0-1.3 1.3L12 21l-1.9-5.8a2 2 0 0 0-1.3-1.3L3 12l5.8-1.9a2 2 0 0 0 1.3-1.3L12 3z"/><path d="M5 3v4"/><path d="M3 5h4"/><path d="M19 17v4"/><path d="M17 19h4"/>`,
	"wand.and.stars":                     `<path d="M15 4V2"/><path d="M15 16v-2"/><path d="M8 9h2"/><path d="M20 9h2"/><path d="M17.8 11.8L19 13"/><path d="M15 9h.01"/><path d="M17.8 6.2L19 5"/><path d="m3 21 9-9"/><path d="M12.2 6.2 11 5"/>`,
	"globe":                              `<circle cx="12" cy="12" r="10"/><path d="M2 12h20"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>`,
	"doc.on.clipboard":                   `<rect width="8" height="4" x="8" y="2" rx="1" ry="1"/><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"/><path d="M9 12h6"/><path d="M9 16h6"/>`,
	"keyboard":                           `<rect width="20" height="16" x="2" y="4" rx="2"/><path d="M6 8h.01"/><path d="M10 8h.01"/><path d="M14 8h.01"/><path d="M18 8h.01"/><path d="M8 12h.01"/><path d="M12 12h.01"/><path d="M16 12h.01"/><path d="M7 16h10"/>`,
	"keyboard.badge.ellipsis":            `<rect width="20" height="16" x="2" y="4" rx="2"/><path d="M7 16h7"/><path d="M6 8h.01"/><path d="M10 8h.01"/><path d="M14 8h.01"/><path d="M18 8h.01"/><path d="M8 12h.01"/><path d="M12 12h.01"/><circle cx="18.5" cy="15.5" r="3.2" fill="currentColor" stroke="none"/><path d="M17 15.5h.01"/><path d="M18.5 15.5h.01"/><path d="M20 15.5h.01"/>`,
	"keyboard.chevron.compact.down":      `<rect width="20" height="16" x="2" y="4" rx="2"/><path d="M6 8h.01"/><path d="M10 8h.01"/><path d="M14 8h.01"/><path d="M18 8h.01"/><path d="M8 12h.01"/><path d="M12 12h.01"/><path d="M7 16h4"/><path d="m14 15 2.5 2.5L19 15"/>`,
	"terminal":                           `<polyline points="4 17 10 11 4 5"/><line x1="12" x2="20" y1="19" y2="19"/>`,
	"macwindow":                          `<rect width="20" height="16" x="2" y="4" rx="2"/><path d="M2 8h20"/><path d="M6 6h.01"/><path d="M9 6h.01"/>`,
	"rectangle.lefthalf.filled":          `<rect width="20" height="16" x="2" y="4" rx="2"/><path d="M4 6h8v12H4z" fill="currentColor" stroke="none"/>`,
	"rectangle.righthalf.filled":         `<rect width="20" height="16" x="2" y="4" rx="2"/><path d="M12 6h8v12h-8z" fill="currentColor" stroke="none"/>`,
	"rectangle.tophalf.filled":           `<rect width="20" height="16" x="2" y="4" rx="2"/><path d="M4 6h16v6H4z" fill="currentColor" stroke="none"/>`,
	"rectangle.bottomhalf.filled":        `<rect width="20" height="16" x="2" y="4" rx="2"/><path d="M4 12h16v6H4z" fill="currentColor" stroke="none"/>`,
	"arrow.up.left.and.arrow.down.right": `<polyline points="15 3 21 3 21 9"/><polyline points="9 21 3 21 3 15"/><line x1="21" x2="14" y1="3" y2="10"/><line x1="3" x2="10" y1="21" y2="14"/>`,
	"rectangle.center.inset.filled":      `<rect width="20" height="16" x="2" y="4" rx="2"/><rect width="10" height="8" x="7" y="8" rx="1" fill="currentColor" stroke="none"/>`,
	"display.next":                       `<rect width="18" height="12" x="3" y="4" rx="2"/><path d="M8 20h8"/><path d="M12 16v4"/><path d="M17 8l3 2-3 2"/>`,
	"display.previous":                   `<rect width="18" height="12" x="3" y="4" rx="2"/><path d="M8 20h8"/><path d="M12 16v4"/><path d="M10 8l-3 2 3 2"/>`,
	"arrow.uturn":                        `<path d="M9 14L4 9l5-5"/><path d="M4 9h10a6 6 0 0 1 6 6v6"/>`,
	"folder":                             `<path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>`,
	"text.badge.plus":                    `<path d="M17 6H3"/><path d="M21 12H3"/><path d="M11 18H3"/><path d="M18 15v6"/><path d="M15 18h6"/>`,
	"text.badge.checkmark":               `<path d="M17 6H3"/><path d="M21 12H3"/><path d="M11 18H3"/><path d="m15 18 2 2 4-4"/>`,
	"gearshape":                          `<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>`,
	"bolt":                               `<polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>`,
	"character.book.closed":              `<path d="M4 19.5v-15A2.5 2.5 0 0 1 6.5 2H20v20H6.5a2.5 2.5 0 0 1 0-5H20"/><path d="M9 10h6"/><path d="M12 7v6"/>`,
	"app.badge":                          `<rect width="16" height="16" x="3" y="5" rx="4"/><circle cx="19" cy="5" r="3" fill="currentColor"/>`,
	"calculator":                         `<rect width="16" height="20" x="4" y="2" rx="2"/><line x1="8" x2="16" y1="6" y2="6"/><line x1="16" x2="16" y1="14" y2="18"/><path d="M16 10h.01"/><path d="M12 10h.01"/><path d="M8 10h.01"/><path d="M12 14h.01"/><path d="M8 14h.01"/><path d="M12 18h.01"/><path d="M8 18h.01"/>`,
	"face.smiling":                       `<circle cx="12" cy="12" r="10"/><path d="M8 14s1.5 2 4 2 4-2 4-2"/><line x1="9" x2="9.01" y1="9" y2="9"/><line x1="15" x2="15.01" y1="9" y2="9"/>`,
	"pin.fill":                           `<line x1="12" x2="12" y1="17" y2="22"/><path d="M5 17h14v-1.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V6h1a2 2 0 0 0 0-4H8a2 2 0 0 0 0 4h1v4.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24Z"/>`,
	"search":                             `<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>`,
	"command":                            `<path d="M18 3a3 3 0 0 0-3 3v12a3 3 0 0 0 3 3 3 3 0 0 0 3-3 3 3 0 0 0-3-3H6a3 3 0 0 0-3 3 3 3 0 0 0 3 3 3 3 0 0 0 3-3V6a3 3 0 0 0-3-3 3 3 0 0 0-3 3 3 3 0 0 0 3 3h12a3 3 0 0 0 3-3 3 3 0 0 0-3-3z"/>`,
	"chevron.right":                      `<path d="m9 18 6-6-6-6"/>`,
	"chevron.left":                       `<path d="m15 18-6-6 6-6"/>`,
	"chevron.down":                       `<path d="m6 9 6 6 6-6"/>`,
	"xmark":                              `<path d="M18 6 6 18"/><path d="m6 6 12 12"/>`,
	"checkmark":                          `<path d="M20 6 9 17l-5-5"/>`,
	"ellipsis":                           `<circle cx="5" cy="12" r="1.6" fill="currentColor" stroke="none"/><circle cx="12" cy="12" r="1.6" fill="currentColor" stroke="none"/><circle cx="19" cy="12" r="1.6" fill="currentColor" stroke="none"/>`,
	"clipboard":                          `<rect width="8" height="4" x="8" y="2" rx="1" ry="1"/><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"/><path d="M9 12h6"/><path d="M9 16h4"/>`,
	"tag":                                `<path d="M12.586 2.586A2 2 0 0 0 11.172 2H4a2 2 0 0 0-2 2v7.172a2 2 0 0 0 .586 1.414l8.704 8.704a2.426 2.426 0 0 0 3.42 0l6.58-6.58a2.426 2.426 0 0 0 0-3.42z"/><circle cx="7.5" cy="7.5" r=".99"/>`,

	// Utility & supplementary semantic icons
	"function":             `<rect width="16" height="20" x="4" y="2" rx="2"/><line x1="8" x2="16" y1="6" y2="6"/><line x1="16" x2="16" y1="14" y2="18"/><path d="M16 10h.01"/><path d="M12 10h.01"/><path d="M8 10h.01"/><path d="M12 14h.01"/><path d="M8 14h.01"/><path d="M12 18h.01"/><path d="M8 18h.01"/>`,
	"number":               `<line x1="4" x2="20" y1="9" y2="9"/><line x1="4" x2="20" y1="15" y2="15"/><line x1="10" x2="8" y1="3" y2="21"/><line x1="16" x2="14" y1="3" y2="21"/>`,
	"clock":                `<circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>`,
	"calendar.badge.clock": `<path d="M21 7.5V6a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h3.5"/><path d="M16 2v4"/><path d="M8 2v4"/><path d="M3 10h5"/><circle cx="16" cy="16" r="6"/><path d="M16 14v2l1 1"/>`,
	"lock.rotation":        `<rect width="18" height="11" x="3" y="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>`,
	"lock.open":            `<rect width="18" height="11" x="3" y="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 9.9-1"/>`,
	"arrow.clockwise":      `<path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/>`,
	"circle":               `<circle cx="12" cy="12" r="9"/>`,

	// Clipboard content kinds (names match SF Symbols so macOS draws the
	// native glyph and other platforms these vectors).
	"link":        `<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>`,
	"envelope":    `<rect width="20" height="16" x="2" y="4" rx="2"/><path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7"/>`,
	"curlybraces": `<path d="M8 3H7a2 2 0 0 0-2 2v5a2 2 0 0 1-2 2 2 2 0 0 1 2 2v5c0 1.1.9 2 2 2h1"/><path d="M16 21h1a2 2 0 0 0 2-2v-5c0-1.1.9-2 2-2a2 2 0 0 1-2-2V5a2 2 0 0 0-2-2h-1"/>`,
	"doc.text":    `<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M10 9H8"/><path d="M16 13H8"/><path d="M16 17H8"/>`,

	// Floating pill states.
	"lock.fill":                `<rect width="18" height="11" x="3" y="11" rx="2" ry="2" fill="currentColor"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>`,
	"square.3.layers.3d":       `<path d="m12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83Z"/><path d="m22 17.65-9.17 4.16a2 2 0 0 1-1.66 0L2 17.65"/><path d="m22 12.65-9.17 4.16a2 2 0 0 1-1.66 0L2 12.65"/>`,
	"info.circle":              `<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>`,
	"exclamationmark.triangle": `<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"/><path d="M12 9v4"/><path d="M12 17h.01"/>`,
}

// Optical stroke weights: strokes get slightly heavier as icons shrink so
// hairlines survive small rendering targets (16 DIP) next to 11-13px text.
func opticalStrokeWidth(size float32) string {
	if size <= 16 {
		return "2.25"
	}
	if size <= 20 {
		return "2.1"
	}
	return "2"
}

// svgFor wraps an icon body in a 24×24 viewBox sized by optical variant.
func svgFor(body, strokeW string) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="%s" stroke-linecap="round" stroke-linejoin="round">%s</svg>`, strokeW, body)
}

var (
	parsedSVGsOnce  sync.Once
	parsedSVGs      map[string]*ui.SVG // canonical 24pt variant (stroke 2)
	parsedSVGsSmall map[string]*ui.SVG // small-target variant (heavier stroke)
	defaultSVG      *ui.SVG
)

func initParsedSVGs() {
	parsedSVGsOnce.Do(func() {
		parsedSVGs = make(map[string]*ui.SVG, len(semanticIconBodies))
		parsedSVGsSmall = make(map[string]*ui.SVG, len(semanticIconBodies))
		for k, body := range semanticIconBodies {
			parsedSVGs[k] = ui.MustParseSVG([]byte(svgFor(body, opticalStrokeWidth(IconSize24))))
			parsedSVGsSmall[k] = ui.MustParseSVG([]byte(svgFor(body, opticalStrokeWidth(IconSize16))))
		}
		defaultSVG = parsedSVGs["bolt"]
	})
}

// SemanticIconSVG returns the parsed *ui.SVG for a semantic icon name,
// falling back to the default bolt icon when name is unknown or empty.
func SemanticIconSVG(name string) *ui.SVG {
	initParsedSVGs()
	key := strings.ToLower(strings.TrimSpace(name))
	if svg, ok := parsedSVGs[key]; ok && svg != nil {
		return svg
	}
	return defaultSVG
}

// semanticIconSVGForSize returns the optically-tuned variant for a draw
// size: small targets get the heavier stroke so hairlines stay visible.
func semanticIconSVGForSize(name string, size float32) *ui.SVG {
	initParsedSVGs()
	key := strings.ToLower(strings.TrimSpace(name))
	if size <= 16 {
		if svg, ok := parsedSVGsSmall[key]; ok && svg != nil {
			return svg
		}
	}
	return SemanticIconSVG(key)
}

// renderSemanticIcon renders a semantic icon using SF Symbols on macOS when the
// desktop app runtime is active, or falls back to the embedded vector SVG icon
// in headless tests and on Windows/Linux. Icons take the current text color of
// their context (currentColor) and optically compensate stroke per size.
func renderSemanticIcon(c *ui.Context, name string, size float32, color ui.Color) ui.Element {
	if size <= 0 {
		size = 16
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		trimmed = "bolt"
	}
	if runtime.GOOS == "darwin" && mygo.App.IsReady() && sfsymbols.Available() {
		return sfsymbols.Symbol(c, trimmed).
			Size(size).
			Color(color).
			Fallback(semanticIconSVGForSize(trimmed, size)).
			Element
	}
	svg := semanticIconSVGForSize(trimmed, size)
	return ui.Icon(c, svg).Size(size, size).TextColor(color)
}
