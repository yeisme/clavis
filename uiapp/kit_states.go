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
	ui.Row(c).FillWidth().Padding(space8, space10).Radius(radius8).Gap(space8).AlignItems(ui.Center).Background(background).Border(space1, t.Danger.Alpha(alphaHalf)).Children(func() {
		renderSemanticIcon(c, "xmark", IconSize16, foreground)
		ui.Text(c, text).FontSize(typeBody).TextColor(foreground).SingleLine()
	})
}

type kitStateKind uint8

const (
	kitStateEmpty kitStateKind = iota
	kitStateLoading
	kitStateError
)

// kitStateView is the shared empty/loading/error treatment for content pages.
// It keeps the body neutral, reserves blue for active loading, and makes retry
// an explicit action on failures.
func kitStateView(c *ui.Context, t *ui.Theme, kind kitStateKind, icon, title, body string, retry func()) {
	ui.Column(c).FillWidth().Grow(1).Padding(space24).Gap(space8).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		color := kitStateEmptyColor(t)
		if kind == kitStateLoading {
			color = kitStateLoadingColor(t)
		}
		if kind == kitStateError {
			color = kitStateErrorColor(t)
		}
		renderSemanticIcon(c, icon, 32, color)
		ui.Text(c, title).FontSize(typeTitle).FontWeight(600).TextColor(t.Text).SingleLine()
		if body != "" {
			ui.Text(c, body).FontSize(typeBody).TextColor(t.TextMuted).TextAlign(ui.Center)
		}
		if kind == kitStateLoading {
			kitStatusDot(c, color, true)
		}
		if kind == kitStateError && retry != nil {
			button := ui.ButtonBase(c).Key("kit-state-retry").Padding(space4, space10).Radius(radiusChip).Background(tileFill(t)).Border(space1, hairline(t)).FocusRing(false)
			button.Children(func() {
				ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
					renderSemanticIcon(c, "arrow.clockwise", typeMeta, secondaryInk(t))
					ui.Text(c, "重试").FontSize(typeMeta).FontWeight(600).TextColor(secondaryInk(t)).SingleLine()
				})
			})
			if button.Clicked() {
				retry()
			}
		}
	})
}

func kitStateEmptyColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(122, 122, 128, 0.75)
	}
	return ui.RGBA(142, 142, 147, 0.75)
}

func kitStateLoadingColor(_ *ui.Theme) ui.Color {
	return ui.RGBA(10, 132, 255, 1)
}

func kitStateErrorColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(255, 69, 58, 1)
	}
	return ui.RGBA(255, 59, 48, 1)
}

func kitStatusDot(c *ui.Context, color ui.Color, animated bool) {
	dot := ui.Box(c).Size(space8, space8).Radius(radiusPill).Background(color)
	if animated && !c.Preferences().ReduceMotion {
		dot.Opacity(0.55 + 0.45*dot.Loop("kit-status-dot", 900*time.Millisecond, ui.EaseInOut))
	}
}
