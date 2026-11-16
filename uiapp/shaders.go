package uiapp

import (
	"math"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
	"github.com/yeisme/clavis/pkg/core/config"
)

// The shaders are package values rather than compiled during a view build. This
// keeps compilation one-time and makes them usable by every surface in the UI.
var SpecularTiltShader = shader.MustCompile(`package main
var Pointer vec2
var Intensity float
var Accent vec4
func Fragment(dst vec4, pos vec2) vec4 {
	s := rectSize()
	uv := pos / s
	d := uv - Pointer
	dist := length(d)
	spot := smoothstep(0.68, 0.02, dist)
	rim := smoothstep(0.56, 0.94, max(uv.x, max(uv.y, max(1.0-uv.x, 1.0-uv.y))))
	light := (0.14 + 0.86 * spot) * Intensity
	return vec4(Accent.rgb * (light + rim * 0.22), Accent.a * (0.14 + light * 0.38))
}`)

var AIStageGlowShader = shader.MustCompile(`package main
var Time float
var Intensity float
var Accent vec4
func Fragment(dst vec4, pos vec2) vec4 {
	s := rectSize()
	uv := pos / s
	edge := min(min(uv.x, 1.0-uv.x), min(uv.y, 1.0-uv.y))
	r := smoothstep(0.12, 0.0, edge)
	wave := 0.5 + 0.5 * sin(Time * 1.7 + uv.x * 7.0 + uv.y * 3.0)
	warm := vec3(1.0, 0.32, 0.86)
	cool := vec3(0.20, 0.70, 1.0)
	colour := mix(Accent.rgb + cool * 0.25, warm, wave)
	return vec4(colour, Accent.a * Intensity * r * (0.45 + wave * 0.55))
}`)

var LiquidOrbShader = shader.MustCompile(`package main
var Time float
var Speed float
var Accent vec4
func Fragment(dst vec4, pos vec2) vec4 {
	s := rectSize()
	uv := pos / s * 2.0 - 1.0
	d := length(uv)
	inside := smoothstep(1.0, 0.76, d)
	caustic := 0.5 + 0.5 * sin((uv.x * 5.0 + sin(uv.y * 4.0 + Time * Speed)) + Time * Speed)
	shine := smoothstep(0.72, 0.05, length(uv - vec2(-0.28, -0.34)))
	colour := Accent.rgb * (0.45 + caustic * 0.42 + shine * 0.55)
	return vec4(colour, inside * Accent.a * (0.48 + shine * 0.32))
}`)

// ShadersActive is deliberately conservative: expensive pixel work is not
// attempted for reduced-motion users, classic surfaces, or software frames.
func ShadersActive(c *ui.Context, p *ui.Painter, cfg config.AppearanceConfig) bool {
	if c == nil || p == nil || !cfg.EnableShaders {
		return false
	}
	prefs := c.Preferences()
	return resolveStagePlan(stageInputs{
		Preset:             cfg.VisualPreset,
		Animations:         cfg.Animations,
		ReducedMotion:      prefs.ReduceMotion,
		ReduceTransparency: prefs.ReduceTransparency,
		HighContrast:       prefs.HighContrast,
		Accelerated:        p.Accelerated(),
		Intensity:          1,
	}).EnabledShaders
}

func DrawSpecularSheen(c *ui.Context, p *ui.Painter, r ui.Rect, pointer ui.Point, intensity float32, accent ui.Color, cfg config.AppearanceConfig) {
	if p == nil || r.W <= 0 || r.H <= 0 {
		return
	}
	if ShadersActive(c, p, cfg) {
		SpecularTiltShader.Draw(p, r, shader.Options{Radius: 14, Uniforms: map[string]any{
			"Pointer": []float32{pointer.X, pointer.Y}, "Intensity": intensity, "Accent": accent,
		}})
		return
	}
	// A single translucent fill is the intentional zero-cost fallback.
	p.Fill(r, accent.Alpha(clamp01(intensity)*0.13), 14)
}

func DrawAIStageGlow(c *ui.Context, p *ui.Painter, r ui.Rect, intensity float32, accent ui.Color, cfg config.AppearanceConfig) {
	if p == nil || r.W <= 0 || r.H <= 0 {
		return
	}
	if ShadersActive(c, p, cfg) {
		AIStageGlowShader.Draw(p, r, shader.Options{Radius: 16, Uniforms: map[string]any{
			"Time": float32(p.Now().UnixNano()) / 1e9, "Intensity": intensity, "Accent": accent,
		}})
		p.AnimationFrame()
		return
	}
	p.Fill(r, accent.Alpha(clamp01(intensity)*0.11), 16)
}

// RenderLiquidOrb creates a compact orb element. Its animated shader is only
// selected when the current painter can accelerate it; headless and reduced
// motion frames use the same clean vector sphere.
func RenderLiquidOrb(c *ui.Context, size float32, active bool, cfg config.AppearanceConfig) ui.Element {
	if size <= 0 {
		size = 36
	}
	accent := c.Theme().Accent
	return ui.Box(c).Size(size, size).Radius(size / 2).Clip().Draw(func(p *ui.Painter, r ui.Rect) {
		if active && ShadersActive(c, p, cfg) {
			LiquidOrbShader.Draw(p, r, shader.Options{Radius: size / 2, Uniforms: map[string]any{
				"Time": float32(p.Now().UnixNano()) / 1e9, "Speed": 0.8, "Accent": accent,
			}})
			p.AnimationFrame()
			return
		}
		p.Fill(r, accent.Alpha(210), size/2)
		p.Fill(ui.Rect{X: r.X + r.W*.2, Y: r.Y + r.H*.14, W: r.W * .3, H: r.H * .22}, ui.RGBA(255, 255, 255, 58.0/255), size/4)
	})
}

func clamp01(v float32) float32 {
	if math.IsNaN(float64(v)) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
