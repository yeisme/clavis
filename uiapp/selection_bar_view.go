package uiapp

import (
	"strings"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

// SelectionBarActionLabel returns the localized label for a selection-bar action id.
func SelectionBarActionLabel(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "optimize_prompt":
		return "Optimize"
	case "explain":
		return "Explain"
	case "translate":
		return "Translate"
	case "polish":
		return "Polish"
	case "copy":
		return "Copy"
	default:
		return action
	}
}

// SelectionBarActionShortcut returns the digit hint shown on an action pill.
func SelectionBarActionShortcut(index int) string {
	if index < 0 || index > 8 {
		return ""
	}
	return string(rune('1' + index))
}

// selectionBarHeight is the fixed DIP height of the floating selection bar.
const selectionBarHeight = 34

// selectionBarPaddingX is the horizontal padding inside the bar.
const selectionBarPaddingX = 6

// PreferredSelectionBarWidth measures the bar for the given actions: three
// digits-of-padding per pill plus the label text estimated from its rune count.
func PreferredSelectionBarWidth(actions []string) int {
	if len(actions) == 0 {
		return 120
	}
	width := selectionBarPaddingX * 2
	for i, action := range actions {
		label := SelectionBarActionLabel(action)
		// digit badge + gap + label glyphs (~9 DIP per rune at 12pt) + pill padding
		width += 22 + 6 + len([]rune(label))*9 + 16
		if i > 0 {
			width += 4
		}
	}
	if width < 120 {
		width = 120
	}
	if width > 560 {
		width = 560
	}
	return width
}

// SelectionBarSize returns the window size for a payload.
func SelectionBarSize(p SelectionBarPayload) (width, height int) {
	return PreferredSelectionBarWidth(p.Actions), selectionBarHeight
}

// SelectionBarView renders the non-activating floating action bar shown next to
// a text selection. It never takes focus: the host window uses ShowInactive.
func SelectionBarView(c *ui.Context, p SelectionBarPayload, appearance config.AppearanceConfig) {
	theme := resolveAppearanceTheme(c, appearance.Theme)
	c.SetTheme(theme)
	prefs := c.Preferences()

	if !prefs.ReduceTransparency {
		c.Root().Radius(radiusPill)
	}

	bar := ui.Row(c).
		Fill().
		Padding(space4, selectionBarPaddingX).
		Radius(radiusPill).
		Gap(4).
		Center().
		Clip()
	if !prefs.ReduceTransparency {
		bar.Material(glass.Glass{Native: c.Vibrancy(), Clarity: float32(appearance.GlassClarity)})
		bar.Border(1, withAlpha(theme.Border, alphaHalf))
	} else {
		bar.Background(theme.Background).Border(1, theme.Border)
	}

	bar.Children(func() {
		for i, action := range p.Actions {
			// Selection actions share the same row interaction language as the
			// Capsule list; the bar remains non-activating but hover/focus styling
			// is still owned by the kit rather than a bespoke pill.
			kitRow(c, theme, interactionState{}, motionProfiles[animationOff], func() {
				renderSemanticIcon(c, selectionBarIcon(action), typeMeta, theme.Accent)
				hint := SelectionBarActionShortcut(i)
				if hint != "" {
					ui.Text(c, hint).FontSize(typeMicro).TextColor(theme.Accent.Alpha(alphaStrong)).SingleLine()
				}
				ui.Text(c, SelectionBarActionLabel(action)).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
			})
		}
		if len(p.Actions) == 0 {
			ui.Text(c, "No actions").FontSize(typeLabel).TextColor(theme.TextMuted).SingleLine()
		}
	})
}

// selectionBarIcon maps an action id to a semantic icon name.
func selectionBarIcon(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "optimize_prompt":
		return "sparkles"
	case "explain":
		return "text.magnifyingglass"
	case "translate":
		return "character.book.closed"
	case "polish":
		return "wand.and.stars"
	case "copy":
		return "doc.on.doc"
	default:
		return "circle"
	}
}

// AnchorRect returns the host.CursorAnchor the bar should be placed against,
// falling back to the pointer position when the anchor is invalid.
func SelectionBarAnchor(p SelectionBarPayload) host.CursorAnchor {
	return p.Anchor
}
