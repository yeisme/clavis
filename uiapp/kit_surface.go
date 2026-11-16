package uiapp

import (
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
)

type cardVariant int

const (
	cardFlat cardVariant = iota
	cardElevated
	cardInset
	cardGlass
)

func kitCard(c *ui.Context, t *ui.Theme, v cardVariant, body func()) ui.Element {
	card := ui.Column(c).FillWidth().Padding(cardPadY, cardPadX).Radius(radiusCard).Gap(cardGap)
	card.Background(t.Surface).Border(space1, hairline(t))
	switch v {
	case cardElevated:
		x, y, blur, spread, shadow := elevationShadow(t, elev2)
		card.Shadow(x, y, blur, spread, shadow).InsetShadow(0, -space1, space1, 0, surfaceHighlight(t))
	case cardInset:
		card.Background(t.Surface.Mix(t.Text, alphaFaint)).Border(space1, hairline(t))
	case cardGlass:
		GlassSurface(c, card, config.DefaultAppearanceConfig(), GlassOpts{Interactive: true, Tint: t.Surface, Radius: radiusCard})
	}
	card.Children(body)
	return card
}

func kitPanel(c *ui.Context, t *ui.Theme, v cardVariant, body func()) ui.Element {
	panel := ui.Column(c).FillWidth().Padding(space12, space14).Radius(radius12).Gap(cardGap)
	panel.Background(t.Surface).Border(space1, hairline(t))
	if v == cardElevated {
		x, y, blur, spread, shadow := elevationShadow(t, elev2)
		panel.Shadow(x, y, blur, spread, shadow).InsetShadow(0, -space1, space1, 0, surfaceHighlight(t))
	} else if v == cardInset {
		panel.Background(t.Surface.Mix(t.Text, alphaFaint))
	} else if v == cardGlass {
		GlassSurface(c, panel, config.DefaultAppearanceConfig(), GlassOpts{Interactive: true, Tint: t.Surface, Radius: radius12})
	}
	panel.Children(body)
	return panel
}

func kitInsetWell(c *ui.Context, t *ui.Theme, body func()) ui.Element {
	well := ui.Column(c).FillWidth().Padding(space8, space10).Radius(radius8).Gap(space8).
		Background(tileFill(t)).Border(space1, hairline(t))
	well.InsetShadow(0, -space1, space1, 0, hairline(t))
	well.Children(body)
	return well
}

func kitDivider(c *ui.Context, t *ui.Theme) {
	ui.Box(c).FillWidth().Height(space1).Background(hairline(t))
}
