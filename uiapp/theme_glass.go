package uiapp

import (
	"runtime"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
)

// GlassOpts controls the platform-dependent portions of a glass surface.
type GlassOpts struct {
	Interactive bool
	Native      bool
	Tint        ui.Color
	Radius      float32
}

// GlassSurface applies Liquid Glass while retaining an opaque, accessible
// surface when the desktop asks the app to reduce transparency.
func GlassSurface(c *ui.Context, el ui.Element, cfg config.AppearanceConfig, opts GlassOpts) ui.Element {
	radius := opts.Radius
	if radius <= 0 {
		radius = NormalizeCornerRadius(cfg)
	}
	el.Radius(radius)
	if c.Preferences().ReduceTransparency {
		el.Background(c.Theme().Surface).Border(1, c.Theme().Border)
		return el
	}
	el.Material(glass.Glass{
		Style: glass.Regular, Tint: opts.Tint, Interactive: opts.Interactive,
		Native: opts.Native, Clarity: float32(cfg.GlassClarity),
	})
	// The border is intentionally a hairline rather than a heavy card edge.
	el.Border(1, c.Theme().Border.Alpha(0.32))
	return el
}

// RenderMacInsetTitleBar adds a title-bar-safe draggable header. Constructors
// append their elements to the current build parent, so this helper is safe to
// call from a view's Children callback.
func RenderMacInsetTitleBar(c *ui.Context, theme *ui.Theme, title string, subtitle string, trailing func()) {
	if theme == nil {
		theme = c.Theme()
	}
	bar := c.TitleBar()
	left := bar.Left
	if runtime.GOOS == "darwin" && left < 78 {
		left = 78
	}
	ui.Row(c).Height(maxf(bar.Height, 34)).Shrink(0).AlignItems(ui.Center).
		Padding(0, bar.Right+16, 0, left+12).DragWindow().Children(func() {
		ui.Column(c).Gap(1).Grow(1).Children(func() {
			ui.Text(c, title).FontSize(14).FontWeight(600).TextColor(theme.Text)
			if subtitle != "" {
				ui.Text(c, subtitle).FontSize(11).TextColor(theme.TextMuted)
			}
		})
		if trailing != nil {
			trailing()
		}
	})
	RenderScrollEdgeMask(c)
}

// RenderScrollEdgeMask paints the soft macOS scroll-edge pocket over content
// below an inset title bar.
func RenderScrollEdgeMask(c *ui.Context) {
	bar := c.TitleBar()
	height := maxf(bar.Height+10, 42)
	ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(height).PassThrough().Material(glass.ScrollEdge{})
}

// RenderGlassPillGroup merges neighboring panes through one Glass group, as
// NSGlassEffectContainerView does. build receives an addPane function so each
// caller retains ownership of its content and hit target.
func RenderGlassPillGroup(c *ui.Context, cfg config.AppearanceConfig, gap float32, build func(addPane func(ui.Element))) {
	group := glass.NewGroup(c)
	group.Gap(gap).Radius(NormalizeCornerRadius(cfg))
	group.Clarity = float32(cfg.GlassClarity)
	group.Style = glass.Regular
	if c.Preferences().ReduceTransparency {
		group.Style = glass.Regular
		group.Background(c.Theme().Surface)
	}
	group.Children(func() {
		if build != nil {
			build(func(el ui.Element) { group.Pane(el) })
		}
	})
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
