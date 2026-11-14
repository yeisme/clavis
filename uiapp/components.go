package uiapp

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

// keyTone selects how a keycap stands out.
type keyTone int

const (
	// keyToneNeutral is the resting keycap: raised face, muted glyph.
	keyToneNeutral keyTone = iota
	// keyToneAccent marks a live shortcut (e.g. ⌥1..⌥9 while Option is held).
	keyToneAccent
	// keyToneQuiet is a flat, borderless cap for dense footers.
	keyToneQuiet
)

// keycap renders one key ("⌘", "K", "↵", "esc", "w") as a small raised cap:
// a light face, a hairline outline and a 1px bottom lip that reads as key
// travel. Single glyphs sit in a square-ish cap; words get side padding.
func keycap(c *ui.Context, t *ui.Theme, label string, tone keyTone) ui.Element {
	label = strings.TrimSpace(label)
	face := ui.Box(c).
		MinWidth(18).
		Height(18).
		Padding(0, space4).
		Radius(radiusKeycap).
		Center()
	fg := t.TextMuted
	switch tone {
	case keyToneAccent:
		bg := surfaceBase(t)
		fg = accentInk(t, bg)
		face.Background(t.Accent.Alpha(0.16)).Border(1, t.Accent.Alpha(0.5))
	case keyToneQuiet:
		face.Background(tileFill(t))
	default:
		face.Background(keycapFill(t)).
			Border(1, keycapEdge(t)).
			Shadow(0, 1, 0, 0, keycapLip(t))
	}
	size := typeMicro
	if utf8.RuneCountInString(label) == 1 {
		size = typeMeta
	}
	face.Children(func() {
		ui.Text(c, label).FontSize(size).FontWeight(600).TextColor(fg).SingleLine()
	})
	return face
}

// keyHint renders a shortcut as keycaps followed by a muted caption:
// [⌘][K] Actions. An empty caption renders the caps alone.
func keyHint(c *ui.Context, t *ui.Theme, shortcut string, caption string, tone keyTone) {
	ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Gap(space2).AlignItems(ui.Center).Children(func() {
			for _, k := range splitShortcut(shortcut) {
				keycap(c, t, k, tone)
			}
		})
		if caption != "" {
			ui.Text(c, caption).FontSize(typeMeta).TextColor(t.TextMuted).SingleLine()
		}
	})
}

// splitShortcut splits a shortcut label into its keys: "⌘↵" → [⌘ ↵],
// "Ctrl+Shift+M" → [Ctrl Shift M], "w h" → [w h], "⌥1" → [⌥ 1], "Esc" → [Esc].
// Modifier glyphs (⌘ ⌥ ⇧ ⌃) always stand alone; a run of other characters
// stays one key so "Esc", "Tab" or "F12" read as words.
func splitShortcut(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var keys []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '+' }) {
		var word strings.Builder
		flush := func() {
			if word.Len() > 0 {
				keys = append(keys, word.String())
				word.Reset()
			}
		}
		for _, r := range part {
			switch r {
			case '⌘', '⌥', '⇧', '⌃', '↵', '⏎', '⌫', '⌦', '↑', '↓', '←', '→', '⇥':
				flush()
				keys = append(keys, string(r))
			default:
				word.WriteRune(r)
			}
		}
		flush()
	}
	return keys
}

// chipTone colors a chip.
type chipTone int

const (
	chipNeutral chipTone = iota
	chipAccent
	chipSuccess
	chipWarning
	chipDanger
)

// chip renders a compact rounded label with an optional leading icon.
func chip(c *ui.Context, t *ui.Theme, icon, label string, tone chipTone) ui.Element {
	base := t.TextMuted
	switch tone {
	case chipAccent:
		base = t.Accent
	case chipSuccess:
		base = t.Success
	case chipWarning:
		base = t.Warning
	case chipDanger:
		base = t.Danger
	}
	bg := surfaceBase(t)
	fg := readableOn(base, t.Text, bg, minTextContrast)
	box := ui.Row(c).Padding(space2, space6+1).Radius(radiusChip).Gap(space4).AlignItems(ui.Center)
	if tone == chipNeutral {
		box.Background(tileFill(t)).Border(1, hairline(t))
		fg = t.TextMuted
	} else {
		box.Background(base.Alpha(0.14)).Border(1, base.Alpha(0.35))
	}
	box.Children(func() {
		if icon != "" {
			renderSemanticIcon(c, icon, 11, fg)
		}
		ui.Text(c, label).FontSize(typeMeta).FontWeight(600).TextColor(fg).SingleLine()
	})
	return box
}

// iconTile renders a semantic icon centered in a rounded tile, the leading
// glyph of an Omnibox row: neutral at rest, accent-tinted when chosen.
func iconTile(c *ui.Context, t *ui.Theme, icon string, selected bool) {
	tile := ui.Box(c).Size(rowIconTile, rowIconTile).Radius(radiusTile).Center()
	color := t.TextMuted
	if selected {
		tile.Background(selectedTileFill(t))
		color = selectedTileGlyph(t)
	} else {
		tile.Background(tileFill(t))
	}
	tile.Children(func() {
		renderSemanticIcon(c, icon, rowIconSize, color)
	})
}
