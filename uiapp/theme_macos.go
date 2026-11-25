package uiapp

import "github.com/egoist/mygo/ui"

// Clavis paints with its own macOS palette instead of MyGo's stock themes.
// The stock LightTheme/DarkTheme are Tailwind's zinc grays (#18181b,
// #71717a…) with Tailwind blue (#2563eb / #3b82f6): a web look that never
// matched the anchor the owner set for every surface — modern macOS grayscale
// layering, 1px hairlines and exactly one accent, system blue #0A84FF.
//
// The layers, light → dark:
//
//	content    #FFFFFF  #1C1C1E   reading surfaces: lists, previews, editors (always opaque)
//	elevated   #F5F5F7  #262628   cards, wells, control faces
//	hover      #EDEDF0  #2F2F32   pointer-over fills
//	pressed    #E3E3E8  #38383B   pressed fills
//	separator  #D1D1D6  #48484A   control edges; hairline() is 60% of it
//	text       #1D1D1F  #F5F5F7
//	secondary  #6E6E73  #A1A1A6
//
// Semantic colors are Apple's system red/orange/green; text that wears them
// goes through statusInk so it still reads at 4.5:1.

// macAccent is the one accent Clavis uses, in both appearances.
var macAccent = ui.Hex("#0A84FF")

// macFontSize is the platform's body size (13 on macOS), read once.
var macFontSize = ui.LightTheme().FontSize

var (
	macLight = ui.Theme{
		Background:      ui.Hex("#FFFFFF"),
		Surface:         ui.Hex("#F5F5F7"),
		SurfaceHover:    ui.Hex("#EDEDF0"),
		SurfacePressed:  ui.Hex("#E3E3E8"),
		Border:          ui.Hex("#D1D1D6"),
		Text:            ui.Hex("#1D1D1F"),
		TextMuted:       ui.Hex("#6E6E73"),
		Accent:          macAccent,
		AccentHover:     ui.Hex("#0077ED"),
		AccentPressed:   ui.Hex("#006EDB"),
		AccentText:      ui.Hex("#FFFFFF"),
		Danger:          ui.Hex("#FF3B30"),
		Warning:         ui.Hex("#FF9500"),
		Success:         ui.Hex("#34C759"),
		Selection:       ui.RGBA(10, 132, 255, 0.24),
		Focus:           ui.RGBA(10, 132, 255, 0.55),
		FocusRingWidth:  2,
		FocusRingOffset: 1,
		Scrollbar:       ui.RGBA(0, 0, 0, 0.28),
		Radius:          radiusChip,
		Spacing:         space4,
	}
	macDark = ui.Theme{
		Dark:            true,
		Background:      ui.Hex("#1C1C1E"),
		Surface:         ui.Hex("#262628"),
		SurfaceHover:    ui.Hex("#2F2F32"),
		SurfacePressed:  ui.Hex("#38383B"),
		Border:          ui.Hex("#48484A"),
		Text:            ui.Hex("#F5F5F7"),
		TextMuted:       ui.Hex("#A1A1A6"),
		Accent:          macAccent,
		AccentHover:     ui.Hex("#409CFF"),
		AccentPressed:   ui.Hex("#0071E3"),
		AccentText:      ui.Hex("#FFFFFF"),
		Danger:          ui.Hex("#FF453A"),
		Warning:         ui.Hex("#FF9F0A"),
		Success:         ui.Hex("#30D158"),
		Selection:       ui.RGBA(10, 132, 255, 0.40),
		Focus:           ui.RGBA(10, 132, 255, 0.60),
		FocusRingWidth:  2,
		FocusRingOffset: 1,
		Scrollbar:       ui.RGBA(255, 255, 255, 0.30),
		Radius:          radiusChip,
		Spacing:         space4,
	}
)

// macTheme returns the Clavis palette for one appearance, following the
// desktop's accessibility preferences (high contrast, text size) but not its
// accent color: the owner's anchor is a single system blue, so a purple or
// graphite system accent must not repaint the selection, focus ring and
// primary actions.
func macTheme(dark bool, prefs ui.Preferences) *ui.Theme {
	base := macLight
	if dark {
		base = macDark
	}
	base.FontSize = macFontSize
	prefs.Accent = ui.Color{}
	return base.WithPreferences(prefs)
}

// statusInk is a semantic color (success, warning, danger) made safe for
// text on the content surface: Apple's system colors are tuned for glyphs and
// dots, and bright green or orange text on white falls well under 4.5:1.
func statusInk(t *ui.Theme, c ui.Color) ui.Color {
	return readableOn(c, t.Text, surfaceBase(t), minTextContrast)
}
