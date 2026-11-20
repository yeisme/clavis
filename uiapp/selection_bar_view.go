package uiapp

import (
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// SelectionBarActionLabel returns the localized label for a selection-bar action id.
func SelectionBarActionLabel(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "optimize_prompt":
		return i18n.T("selection.action.optimize")
	case "explain":
		return i18n.T("selection.action.explain")
	case "translate":
		return i18n.T("ai.mode.translate")
	case "polish":
		return i18n.T("ai.mode.polish")
	case "copy":
		return i18n.T("common.copy")
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

// Selection bar item metrics, shared by the view and the width estimate so
// the window is sized for what the view draws: item padding, icon, digit
// and the gaps between them, plus the gap between items.
const (
	selectionItemPadX float32 = space8
	selectionItemGap  float32 = space6
	selectionBarGap   float32 = space2
)

// selectionLabelWidth estimates a label's width at typeLabel/600: CJK and
// other wide runes take about a full em, Latin about 0.6 em.
func selectionLabelWidth(label string) float32 {
	var w float32
	for _, r := range label {
		if r >= 0x1100 && (unicode.Is(unicode.Han, r) || unicode.In(r, unicode.Hangul, unicode.Hiragana, unicode.Katakana) || r >= 0xFF00 && r <= 0xFFEF) {
			w += typeLabel
		} else {
			w += typeLabel * 0.62
		}
	}
	return w
}

// PreferredSelectionBarWidth measures the bar for the given actions from the
// same metrics the view lays out with, so no label is cut ("Transl…").
func PreferredSelectionBarWidth(actions []string) int {
	if len(actions) == 0 {
		return 120
	}
	width := float32(selectionBarPaddingX * 2)
	for i, action := range actions {
		// icon + digit (one figure at typeMicro) + label, two inner gaps, padding
		width += 2*selectionItemPadX + typeMeta + typeMicro + 2*selectionItemGap + selectionLabelWidth(SelectionBarActionLabel(action))
		if i > 0 {
			width += selectionBarGap
		}
	}
	w := int(width + 0.999)
	if w < 120 {
		w = 120
	}
	if w > 560 {
		w = 560
	}
	return w
}

// SelectionBarSize returns the window size for a payload.
func SelectionBarSize(p SelectionBarPayload) (width, height int) {
	return PreferredSelectionBarWidth(p.Actions), selectionBarHeight
}

// SelectionBarView renders the non-activating floating action bar shown next to
// a text selection. It never takes focus: the host window uses ShowInactive.
// It shares the dialog shell (glass edge, near-opaque body, hairline); its
// actions are neutral — gray icon and digit, primary text — and take their
// own width, so the accent stays for hover/focus feedback only.
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
		Gap(selectionBarGap).
		Center().
		Clip()
	kitFloatingShell(c, theme, bar, appearance)

	bar.Children(func() {
		for i, action := range p.Actions {
			item := ui.Row(c).Key("selection-action:"+action).Padding(space4, selectionItemPadX).Radius(radiusPill).
				Gap(selectionItemGap).AlignItems(ui.Center).Shrink(0)
			if item.Hovered() {
				item.Background(tileFill(theme))
			}
			item.Children(func() {
				renderSemanticIcon(c, selectionBarIcon(action), typeMeta, secondaryInk(theme))
				if hint := SelectionBarActionShortcut(i); hint != "" {
					ui.Text(c, hint).FontSize(typeMicro).TextColor(secondaryInk(theme)).SingleLine()
				}
				ui.Text(c, SelectionBarActionLabel(action)).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
			})
		}
		if len(p.Actions) == 0 {
			ui.Text(c, "No actions").FontSize(typeLabel).TextColor(secondaryInk(theme)).SingleLine()
		}
	})
}

// selectionBarIcon maps an action id to a semantic icon name. Every name
// exists both as an SF Symbol and in the embedded set, so Windows and
// headless renders show the same glyph instead of the generic fallback.
func selectionBarIcon(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "optimize_prompt":
		return "sparkles"
	case "explain":
		return "info.circle"
	case "translate":
		return "globe"
	case "polish":
		return "wand.and.stars"
	case "copy":
		return "doc.on.clipboard"
	default:
		return "circle"
	}
}

// AnchorRect returns the host.CursorAnchor the bar should be placed against,
// falling back to the pointer position when the anchor is invalid.
func SelectionBarAnchor(p SelectionBarPayload) host.CursorAnchor {
	return p.Anchor
}
