package uiapp

import (
	"math"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
)

// TiltCardOpts describes the common card geometry while keeping the helper
// useful to Bento tiles and capsule previews alike.
type TiltCardOpts struct {
	Width, Height float32
	Radius        float32
	Accent        ui.Color
	Interactive   bool
}

func Spatial3DEnabled(c *ui.Context, cfg config.AppearanceConfig) bool {
	if c == nil || c.Preferences().ReduceMotion || !cfg.Enable3DEffects ||
		cfg.VisualPreset == "classic" || resolveAnimationMode(cfg.Animations) == AnimationOff {
		return false
	}
	return cfg.VisualPreset == "spatial_3d" || cfg.VisualPreset == "liquid_glass" || cfg.VisualPreset == ""
}

func TiltCard3D(c *ui.Context, key string, cfg config.AppearanceConfig, opts TiltCardOpts, children func()) ui.Element {
	w, h := opts.Width, opts.Height
	if w <= 0 {
		w = 180
	}
	if h <= 0 {
		h = 110
	}
	r := opts.Radius
	if r <= 0 {
		r = NormalizeCornerRadius(cfg)
	}
	accent := opts.Accent
	if accent.A == 0 {
		accent = c.Theme().Accent
	}
	card := ui.Box(c).Size(w, h).Radius(r).Background(c.Theme().Surface).
		Shadow(0, 4, 18, 0, ui.RGBA(0, 0, 0, 42)).Key(key)
	if !Spatial3DEnabled(c, cfg) {
		card.Children(children)
		return card
	}
	// A spring channel is cheap to evaluate and naturally honors reduced-motion
	// settings through the enabled gate above. Only interactive cards lift on
	// hover; static Bento tiles retain the spatial perspective without motion.
	target := float32(0)
	if opts.Interactive && card.Hovered() {
		target = 1
	}
	lift := card.AnimateTo(key+"-lift", target, ui.Snappy)
	card.TranslateZ(14*lift).RotateX(-2*lift).RotateY(3*lift).Perspective(860).
		BackfaceHidden().Shadow(0, 8*lift+4, 22, 0, ui.RGBA(0, 0, 0, 40+30*lift))
	card.Children(func() {
		if children != nil {
			children()
		}
		ui.Box(c).Absolute().Fill().PassThrough().DrawOver(func(p *ui.Painter, box ui.Rect) {
			DrawSpecularSheen(c, p, box, ui.Point{X: .35, Y: .28}, .7, accent, cfg)
		})
	})
	return card
}

func StageContainer3D(c *ui.Context, phaseKey string, stageIndex int, cfg config.AppearanceConfig, children func()) ui.Element {
	stage := ui.Box(c).Fill().Key("stage-" + phaseKey)
	if Spatial3DEnabled(c, cfg) {
		stage.Perspective(860)
		turn := stage.AnimateTo("stage-turn-"+phaseKey, float32(stageIndex), ui.Snappy)
		stage.RotateY(turn * 5).RotateX(float32(math.Sin(float64(turn))) * 1.5).TranslateZ(8)
	}
	stage.Children(children)
	return stage
}

func CoverFlowDeck3D(c *ui.Context, items int, selected int, cfg config.AppearanceConfig, renderCard func(i int, active bool)) ui.Element {
	if items < 0 {
		items = 0
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= items && items > 0 {
		selected = items - 1
	}
	deck := ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Gap(12)
	if Spatial3DEnabled(c, cfg) {
		deck.Perspective(860)
	}
	deck.Children(func() {
		for i := 0; i < items; i++ {
			delta := float32(i - selected)
			active := i == selected
			card := ui.Box(c).Width(180).Height(120).Key("coverflow-" + strconv.Itoa(i))
			if Spatial3DEnabled(c, cfg) {
				z := card.AnimateTo("cover-z", float32(1-absf(delta)), ui.Snappy)
				card.TranslateZ(28 * z).RotateY(-delta * 18).Perspective(860).BackfaceHidden()
				card.AnimatePose(ui.Pose{Scale: 0.92 + .08*z}, ui.Snappy)
			}
			card.Children(func() {
				if renderCard != nil {
					renderCard(i, active)
				}
			})
		}
	})
	return deck
}

func TactileKeycap3D(c *ui.Context, keyLabel string, active bool, accent bool, cfg config.AppearanceConfig) ui.Element {
	fill := c.Theme().Surface
	if accent {
		fill = c.Theme().Accent
	}
	cap := ui.Box(c).MinWidth(30).Height(30).Padding(4, 8).Radius(8).Background(fill).
		InsetShadow(0, 1, 2, 0, ui.RGBA(255, 255, 255, 50)).
		Shadow(0, 3, 2, 0, ui.RGBA(0, 0, 0, 65)).Center()
	if Spatial3DEnabled(c, cfg) {
		target := float32(0)
		if active {
			target = 1
		}
		pressed := cap.AnimateTo("press", target, ui.Snappy)
		cap.TranslateZ(-2 * pressed).Perspective(500)
		cap.Translate(0, 1.5*pressed).Scale(.96+.04*(1-pressed), .96+.04*(1-pressed))
	}
	cap.Children(func() { ui.Text(c, keyLabel).TextColor(c.Theme().Text).FontSize(12).FontWeight(600) })
	return cap
}

func TelemetryGauge3D(c *ui.Context, key string, value01 float32, label string, metricText string, accent ui.Color, cfg config.AppearanceConfig) ui.Element {
	value01 = clamp01(value01)
	gauge := ui.Column(c).Width(104).Height(104).Key(key).AlignItems(ui.Center).Justify(ui.Center).Gap(2)
	sweep := value01
	if Spatial3DEnabled(c, cfg) {
		sweep = gauge.AnimateTo(key+"-sweep", value01, ui.Snappy)
		gauge.RotateX(4).RotateY(-4).Perspective(800)
	}
	gauge.Children(func() {
		ui.Box(c).Size(76, 76).Draw(func(p *ui.Painter, r ui.Rect) {
			var path ui.Path
			const n = 28
			start, sweep := float64(-math.Pi*.75), float64(math.Pi*1.5)*float64(sweep)
			for i := 0; i <= n; i++ {
				a := start + sweep*float64(i)/n
				x := r.X + r.W/2 + float32(math.Cos(a))*r.W*.39
				y := r.Y + r.H/2 + float32(math.Sin(a))*r.H*.39
				if i == 0 {
					path.MoveTo(x, y)
				} else {
					path.LineTo(x, y)
				}
			}
			p.StrokePath(&path, 5, accent)
			p.Text(r.X, r.Y+r.H*.38, metricText, 16, accent)
		})
		ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted)
	})
	return gauge
}

func ColorSwatch3D(c *ui.Context, hexColor string, size float32, cfg config.AppearanceConfig) ui.Element {
	if size <= 0 {
		size = 44
	}
	colour := ui.Hex(strings.TrimSpace(hexColor))
	swatch := ui.Box(c).Size(size, size).Radius(size*.22).Background(colour).Border(1, ui.RGBA(255, 255, 255, 76)).
		Shadow(0, 4, 10, 0, ui.RGBA(0, 0, 0, 48))
	if Spatial3DEnabled(c, cfg) {
		swatch.RotateX(4).RotateY(-5).TranslateZ(4).Perspective(600)
	}
	swatch.InsetShadow(-1, -1, 3, 0, ui.RGBA(255, 255, 255, 64))
	return swatch
}

func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
