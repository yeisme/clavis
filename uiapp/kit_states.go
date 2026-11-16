package uiapp

import (
	"time"

	"github.com/egoist/mygo/ui"
)

func kitEmptyState(c *ui.Context, t *ui.Theme, icon, title, body string) {
	ui.Column(c).FillWidth().Padding(space24).Gap(space8).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		renderSemanticIcon(c, icon, IconSize24, t.TextMuted)
		ui.Text(c, title).FontSize(typeTitle).FontWeight(600).TextColor(t.Text).SingleLine()
		if body != "" {
			ui.Text(c, body).FontSize(typeBody).TextColor(t.TextMuted).TextAlign(ui.Center)
		}
	})
}

func kitErrorBanner(c *ui.Context, t *ui.Theme, text string) {
	background := t.Danger.Alpha(alphaSubtle)
	foreground := readableOn(t.Danger, t.Text, background.Over(t.Surface), minTextContrast)
	ui.Row(c).FillWidth().Padding(space8, space10).Radius(radius8).Gap(space8).
		AlignItems(ui.Center).Background(background).Border(space1, t.Danger.Alpha(alphaHalf)).Children(func() {
		renderSemanticIcon(c, "xmark", IconSize16, foreground)
		ui.Text(c, text).FontSize(typeBody).TextColor(foreground).SingleLine()
	})
}

func kitStatusDot(c *ui.Context, color ui.Color, animated bool) {
	dot := ui.Box(c).Size(space8, space8).Radius(radiusPill).Background(color)
	if animated && !c.Preferences().ReduceMotion {
		dot.Opacity(0.55 + 0.45*dot.Loop("kit-status-dot", 900*time.Millisecond, ui.EaseInOut))
	}
}
