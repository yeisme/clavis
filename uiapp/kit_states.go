package uiapp

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/i18n"
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
		ui.Text(c, text).FontSize(typeBody).TextColor(foreground).Wrap().Grow(1)
	})
}

func kitWarningBanner(c *ui.Context, t *ui.Theme, text string) {
	warning := kitStateWarningColor(t)
	background := warning.Alpha(alphaSubtle)
	foreground := readableOn(warning, t.Text, background.Over(t.Surface), minTextContrast)
	ui.Row(c).FillWidth().Padding(space8, space10).Radius(radius8).Gap(space8).AlignItems(ui.Center).Background(background).Border(space1, warning.Alpha(alphaHalf)).Children(func() {
		renderSemanticIcon(c, "exclamationmark.triangle", IconSize16, foreground)
		ui.Text(c, text).FontSize(typeBody).TextColor(foreground).Wrap().Grow(1)
	})
}

// kitSearchEmptyState renders the unified empty-results view when an active
// search query or kind filter matches zero items, with an explicit Clear button.
func kitSearchEmptyState(c *ui.Context, t *ui.Theme, title, query string, hasFilter bool, onClear func()) {
	zh := i18n.IsChinese()
	clearLabel := "Clear filter"
	if zh {
		clearLabel = "清除筛选"
	}
	if strings.TrimSpace(title) == "" {
		if zh {
			title = "未找到匹配结果"
		} else {
			title = "No matching results"
		}
	}
	body := ""
	trimmed := strings.TrimSpace(query)
	if trimmed != "" {
		if zh {
			body = "没有与「" + trimmed + "」匹配的内容"
		} else {
			body = "No items matched \"" + trimmed + "\""
		}
	} else if hasFilter {
		if zh {
			body = "当前分类筛选下暂无记录"
		} else {
			body = "No entries match the active category filter"
		}
	}
	ui.Column(c).FillWidth().Grow(1).Padding(space24).Gap(space8).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		renderSemanticIcon(c, "search", space32, kitStateEmptyColor(t))
		ui.Text(c, title).FontSize(typeTitle).FontWeight(600).TextColor(t.Text).SingleLine()
		if body != "" {
			ui.Text(c, body).FontSize(typeBody).TextColor(t.TextMuted).TextAlign(ui.Center)
		}
		if onClear != nil {
			button := ui.ButtonBase(c).Key("kit-clear-filter").Label(clearLabel).Padding(space4, space10).Radius(radiusChip).Background(tileFill(t)).Border(space1, hairline(t)).FocusRing(false)
			button.Children(func() {
				ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
					renderSemanticIcon(c, "xmark", typeMeta, secondaryInk(t))
					ui.Text(c, clearLabel).FontSize(typeMeta).FontWeight(600).TextColor(secondaryInk(t)).SingleLine()
				})
			})
			if button.Clicked() {
				onClear()
			}
		}
	})
}

// kitInlineLoading renders a compact single-line loading indicator for stats
// bands and secondary strips using the spec's active blue status dot.
func kitInlineLoading(c *ui.Context, t *ui.Theme, key, label string) {
	ui.Row(c).Gap(space6).AlignItems(ui.Center).Children(func() {
		kitStatusDot(c, kitStateLoadingColor(t), true)
		ui.Text(c, label).Key(key).FontSize(typeMeta).TextColor(t.TextMuted).SingleLine()
	})
}

// kitInlineError renders a compact single-line error indicator with semantic
// error icon and an optional explicit Retry button for stats bands.
func kitInlineError(c *ui.Context, t *ui.Theme, key, label string, onRetry func()) {
	ui.Row(c).Gap(space6).AlignItems(ui.Center).Children(func() {
		renderSemanticIcon(c, "exclamationmark.circle", typeLabel, kitStateErrorColor(t))
		ui.Text(c, label).Key(key).FontSize(typeMeta).TextColor(t.TextMuted).SingleLine()
		if onRetry != nil {
			retryLabel := "Retry"
			if i18n.IsChinese() {
				retryLabel = "重试"
			}
			btn := ui.ButtonBase(c).Key(key + "-retry").Label(retryLabel).Padding(space2, space6).Radius(radiusChip).Background(tileFill(t)).Border(space1, hairline(t)).FocusRing(false)
			btn.Children(func() {
				ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
					renderSemanticIcon(c, "arrow.clockwise", typeMicro, secondaryInk(t))
					ui.Text(c, retryLabel).FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(t)).SingleLine()
				})
			})
			if btn.Clicked() {
				onRetry()
			}
		}
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

func kitStateWarningColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(255, 159, 10, 1)
	}
	return ui.RGBA(255, 149, 0, 1)
}

func kitStatusDot(c *ui.Context, color ui.Color, animated bool) {
	dot := ui.Box(c).Size(space8, space8).Radius(radiusPill).Background(color)
	if animated && !c.Preferences().ReduceMotion {
		dot.Opacity(0.55 + 0.45*dot.Loop("kit-status-dot", 900*time.Millisecond, ui.EaseInOut))
	}
}
