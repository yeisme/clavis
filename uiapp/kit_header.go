package uiapp

import (
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
)

func kitHeaderBar(c *ui.Context) ui.Element {
	return ui.Row(c).FillWidth().MinHeight(headerHeight).Padding(space8, space14).
		Gap(space8).AlignItems(ui.Center)
}

func kitSegmentedTabs(c *ui.Context, t *ui.Theme, labels []string, active int, onPick func(int)) {
	cfg := config.DefaultAppearanceConfig()
	RenderGlassPillGroup(c, cfg, 0, func(addPane func(ui.Element)) {
		for i, label := range labels {
			selected := i == active
			color := t.TextMuted
			if selected {
				color = t.AccentText
			}
			segment := ui.Row(c).Padding(space4, space8).Radius(radiusSegment).
				AlignItems(ui.Center).OnClick(func() {
				if onPick != nil {
					onPick(i)
				}
			})
			if selected {
				segment.Background(t.Accent)
			}
			segment.Children(func() { ui.Text(c, label).FontSize(typeLabel).TextColor(color).SingleLine() })
			addPane(segment)
		}
	})
}

func kitChipGroup(c *ui.Context, t *ui.Theme, items []string) {
	group := ui.Row(c).Radius(radiusPill).Border(space1, hairline(t)).Background(tileFill(t)).AlignItems(ui.Center)
	group.Children(func() {
		for i, item := range items {
			if i > 0 {
				ui.Box(c).Width(space1).Height(space12).Background(hairline(t))
			}
			ui.Text(c, item).Padding(space2, space6).FontSize(typeMeta).TextColor(t.TextMuted).SingleLine()
		}
	})
}

func kitIconButton(c *ui.Context, t *ui.Theme, icon string, active bool, label string, onClick func()) {
	// A footer toggle is a compact control, not a call to action: it takes the
	// control height rather than the footer's, and an active toggle is a tinted
	// chip rather than a solid accent pill. A filled pill at footer height read
	// as the panel's primary button and swallowed its neighbour.
	button := ui.Row(c).Height(controlHeight).Padding(space4, space8).Gap(space4).
		Radius(radiusPill).AlignItems(ui.Center).OnClick(func() {
		if onClick != nil {
			onClick()
		}
	})
	color := t.TextMuted
	if active {
		button.Background(t.Accent.Alpha(alphaSubtle)).Border(space1, t.Accent.Alpha(alphaHalf))
		color = accentInk(t, surfaceBase(t))
	} else {
		button.Background(tileFill(t)).Border(space1, hairline(t))
	}
	button.Children(func() {
		renderSemanticIcon(c, icon, IconSize16, color)
		if label != "" {
			ui.Text(c, label).FontSize(typeMeta).TextColor(color).SingleLine()
		}
	})
}

func kitStatusPillElement(c *ui.Context, t *ui.Theme, icon, label string, tone chipTone) ui.Element {
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
	fg := t.TextMuted
	if tone != chipNeutral {
		fg = readableOn(base, t.Text, surfaceBase(t), minTextContrast)
	}
	pill := ui.Row(c).Padding(space2, space6).Radius(radiusPill).Gap(space4).AlignItems(ui.Center)
	if tone == chipNeutral {
		pill.Background(tileFill(t)).Border(space1, hairline(t))
	} else {
		pill.Background(base.Alpha(alphaSubtle)).Border(space1, base.Alpha(alphaHalf))
	}
	pill.Children(func() {
		if icon != "" {
			renderSemanticIcon(c, icon, typeLabel, fg)
		}
		ui.Text(c, label).FontSize(typeMeta).FontWeight(600).TextColor(fg).SingleLine()
	})
	return pill
}

func kitStatusPill(c *ui.Context, t *ui.Theme, label string, tone chipTone) {
	kitStatusPillElement(c, t, "", label, tone)
}
