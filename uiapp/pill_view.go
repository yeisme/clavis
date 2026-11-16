package uiapp

import (
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// pillKind is what the floating pill is announcing.
type pillKind int

const (
	// pillIMEState is an input-mode badge: 中 / あ / 한 (native) or EN.
	pillIMEState pillKind = iota
	// pillLayer is a keymap hold layer notice ("LAYER • NAV").
	pillLayer
	// pillMessage is any other transient notice (rule bound, copied, error).
	pillMessage
)

// pillSpec is the classified content of one pill label.
type pillSpec struct {
	kind pillKind
	// native is the native-mode glyph (中, あ, 한) of an IME state pill.
	native string
	// ascii reports an IME state pill showing the English (ASCII) mode.
	ascii bool
	// text is the layer name or the message.
	text string
}

const pillLayerPrefix = "LAYER • "

// classifyPill reads a pill label as the controller formats it
// (FormatIMEPillLabel, NotifyKeymapLayer, ShowToastOrHUD) so the pill can
// render each kind in its own visual language without widening the
// OnShowIMEPill callback.
func classifyPill(label string) pillSpec {
	trimmed := strings.TrimSpace(label)
	switch trimmed {
	case "", "EN":
		return pillSpec{kind: pillIMEState, native: "中", ascii: true}
	case "中", "あ", "한":
		return pillSpec{kind: pillIMEState, native: trimmed}
	}
	if strings.HasPrefix(trimmed, pillLayerPrefix) {
		return pillSpec{kind: pillLayer, text: strings.TrimSpace(strings.TrimPrefix(trimmed, pillLayerPrefix))}
	}
	return pillSpec{kind: pillMessage, text: trimmed}
}

// PillDisplayDuration is how long the shell keeps a pill on screen: a mode
// badge is glanceable, a sentence needs reading time, and a failure stays
// long enough to be noticed.
func PillDisplayDuration(label string) time.Duration {
	spec := classifyPill(label)
	switch spec.kind {
	case pillIMEState:
		return 900 * time.Millisecond
	case pillLayer:
		return 1100 * time.Millisecond
	}
	if hudIconForMessage(spec.text) == "xmark" {
		return 2600 * time.Millisecond
	}
	return 1600 * time.Millisecond
}

// renderIMEPill draws the floating pill for label.
func renderIMEPill(c *ui.Context, label string) {
	theme := c.Theme()
	prefs := c.Preferences()
	spec := classifyPill(label)

	if !prefs.ReduceTransparency {
		c.Root().Radius(radiusPill)
	}
	pill := applyTextLocale(ui.Row(c).
		Fill().
		Radius(radiusPill).
		Center().
		Gap(space6).
		Clip())
	if spec.kind == pillIMEState {
		pill.Padding(space4, space4)
	} else {
		pill.Padding(space4, space12)
	}
	if !prefs.ReduceTransparency {
		pill.Material(glass.Glass{Native: c.Vibrancy()})
		pill.Border(1, theme.Border.Alpha(alphaHalf))
	} else {
		pill.Background(theme.Background).Border(1, theme.Border)
	}

	pill.Children(func() {
		switch spec.kind {
		case pillIMEState:
			renderIMEModeSegments(c, theme, spec)
		case pillLayer:
			renderSemanticIcon(c, "square.3.layers.3d", 13, accentGlyph(theme, surfaceBase(theme)))
			ui.Text(c, i18n.Tf("pill.layer", spec.text)).FontSize(typeLabel).FontWeight(700).TextColor(theme.Text).SingleLine()
		default:
			icon, color := pillMessageIcon(theme, spec.text)
			renderSemanticIcon(c, icon, 13, color)
			ui.Text(c, spec.text).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
		}
	})
}

// renderIMEModeSegments shows both modes as a two-segment switch with the
// live one filled: the native glyph in the accent, English in an inverted
// neutral. Position and color both change on a switch, so 中→EN reads at a
// glance even in peripheral vision; the fill cross-fades (a color change,
// kept under reduced motion).
func renderIMEModeSegments(c *ui.Context, theme *ui.Theme, spec pillSpec) {
	anim := ui.Animation{Duration: 140 * time.Millisecond, Ease: ui.EaseOut}
	neutralFill := theme.Text.Alpha(alphaHalf)
	neutralText := theme.Background
	ui.Row(c).Gap(space2).AlignItems(ui.Center).Children(func() {
		segment := func(key, glyph string, active bool, fill, ink ui.Color) {
			box := ui.Box(c).Size(28, space20).Radius(radiusSegment).Center()
			bg, fg := ui.Transparent, theme.TextMuted
			if active {
				bg, fg = fill, ink
			}
			box.Background(box.AnimateColor(key+"-bg", bg, anim))
			box.Children(func() {
				ui.Text(c, glyph).FontSize(typeBody).FontWeight(700).TextColor(fg).SingleLine()
			})
		}
		segment("native", spec.native, !spec.ascii, theme.Accent, theme.AccentText)
		segment("ascii", "EN", spec.ascii, neutralFill, neutralText)
	})
}

// pillMessageIcon picks a message pill's icon and its color from the
// message, matching the HUD's vocabulary.
func pillMessageIcon(theme *ui.Theme, msg string) (string, ui.Color) {
	bg := surfaceBase(theme)
	switch hudIconForMessage(msg) {
	case "checkmark":
		return "checkmark", readableOn(theme.Success, theme.Text, bg, minGlyphContrast)
	case "xmark":
		return "exclamationmark.triangle", readableOn(theme.Danger, theme.Text, bg, minGlyphContrast)
	case "keyboard":
		return "keyboard", accentGlyph(theme, bg)
	default:
		return "info.circle", accentGlyph(theme, bg)
	}
}
