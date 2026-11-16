package uiapp

import (
	"github.com/egoist/mygo/ui"
	"strings"
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
	return kitKeycap(c, t, strings.TrimSpace(label), tone)
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
	return kitStatusPillElement(c, t, icon, label, tone)
}

// iconTile renders a semantic icon centered in a rounded tile, the leading
// glyph of an Omnibox row: neutral at rest, accent-tinted when chosen.
func iconTile(c *ui.Context, t *ui.Theme, icon string, selected bool) {
	kitIconTile(c, t, icon, selected)
}
