package uiapp

import (
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
)

// One skeleton for Clavis's dialogs (prompt composer, first-run guide) and
// floating bars (selection actions): the same shell, header, body insets and
// footer, so radius, padding, title bar and button placement no longer differ
// from one surface to the next.
//
//	┌ shell ─────────────────────────────────────────────┐ glass only at the
//	│ ⌁ Title  secondary                     [meta][meta] │ edge, body washed
//	├─────────────────────────────────────────────────────┤ near-opaque
//	│ body  (dialogPadY × dialogPadX, cardGap)            │
//	├─────────────────────────────────────────────────────┤
//	│ hint …                     [Other] [Cancel] [Primary]│ primary trails
//	└─────────────────────────────────────────────────────┘ (macOS order)
const (
	dialogPadX float32 = space14
	dialogPadY float32 = space12
	// dialogBorderHighContrast is the shell edge under Increase Contrast.
	dialogBorderHighContrast float32 = 1.5
)

// kitFloatingShell styles el as a floating surface: Liquid Glass at the edge
// with the body washed toward the theme background (glassBodyTint) so the
// desktop behind cannot read through text, and a hairline edge. Reduce
// Transparency gives an opaque background; Increase Contrast a full border.
func kitFloatingShell(c *ui.Context, t *ui.Theme, el ui.Element, appearance config.AppearanceConfig) ui.Element {
	prefs := c.Preferences()
	width, edge := space1, withAlpha(t.Border, alphaHalf)
	if prefs.HighContrast {
		width, edge = dialogBorderHighContrast, t.Border
	}
	if prefs.ReduceTransparency {
		return el.Background(t.Background).Border(width, t.Border)
	}
	return el.Material(glass.Glass{Native: c.Vibrancy(), Clarity: float32(appearance.GlassClarity), Tint: glassBodyTint(t)}).
		Border(width, edge)
}

// kitDialogHeader is the title bar: a neutral icon, the title, an optional
// secondary line, right-aligned meta (kitMetaChip), then a hairline.
func kitDialogHeader(c *ui.Context, t *ui.Theme, icon, title, subtitle string, trailing func()) {
	ui.Row(c).FillWidth().Height(headerHeight).Padding(space0, dialogPadX).Gap(space8).AlignItems(ui.Center).Children(func() {
		if icon != "" {
			renderSemanticIcon(c, icon, typeBody, secondaryInk(t))
		}
		ui.Text(c, title).FontSize(typeTitle).FontWeight(600).TextColor(t.Text).SingleLine().Shrink(1)
		if subtitle != "" {
			ui.Text(c, subtitle).FontSize(typeMeta).TextColor(secondaryInk(t)).SingleLine().Shrink(1)
		}
		ui.Spacer(c)
		if trailing != nil {
			trailing()
		}
	})
	kitDivider(c, t)
}

// kitDialogBody is the content column between header and footer; callers
// add children (and may bind focus to it).
func kitDialogBody(c *ui.Context) ui.Element {
	return ui.Column(c).FillWidth().Grow(1).Padding(dialogPadY, dialogPadX).Gap(cardGap)
}

// dialogAction is one footer button. Footers list secondary actions first
// and the primary last, so it sits at the trailing edge (macOS order).
type dialogAction struct {
	Label    string
	Key      string
	Primary  bool
	Disabled bool
	OnClick  func()
}

// kitDialogFooter is the bottom bar: a hairline, a one-line hint that takes
// the leading space, and the buttons at the trailing edge.
func kitDialogFooter(c *ui.Context, t *ui.Theme, hint string, actions ...dialogAction) {
	kitDivider(c, t)
	ui.Row(c).FillWidth().Height(footerHeight).Padding(space0, dialogPadX).Gap(space8).AlignItems(ui.Center).Children(func() {
		if hint != "" {
			ui.Text(c, hint).FontSize(typeMeta).TextColor(secondaryInk(t)).SingleLine().Grow(1).Shrink(1)
		} else {
			ui.Spacer(c)
		}
		for _, act := range actions {
			var btn ui.Element
			if act.Primary {
				btn = ui.PrimaryButton(c, act.Label)
			} else {
				btn = ui.Button(c, act.Label)
			}
			if act.Key != "" {
				btn.Key(act.Key)
			}
			if btn.Disabled(act.Disabled).Clicked() && act.OnClick != nil {
				act.OnClick()
			}
		}
	})
}

// kitMetaChip is a neutral label in a header (trigger, source app): gray
// tile, hairline, secondary text — never an accent fill.
func kitMetaChip(c *ui.Context, t *ui.Theme, text string) {
	ui.Box(c).Padding(space2, space6).Radius(radiusKeycap).Background(tileFill(t)).Border(space1, hairline(t)).Shrink(0).Children(func() {
		ui.Text(c, text).FontSize(typeMeta).TextColor(secondaryInk(t)).SingleLine()
	})
}
