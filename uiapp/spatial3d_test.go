package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
	"github.com/yeisme/clavis/pkg/core/config"
)

func testSpatialConfig(preset, animations string) config.AppearanceConfig {
	cfg := config.DefaultAppearanceConfig()
	cfg.VisualPreset = preset
	cfg.Animations = animations
	return cfg
}

func TestT6ShadersCompileAndRenderHeadless(t *testing.T) {
	cfg := testSpatialConfig("spatial_3d", "full")
	for _, reduced := range []bool{false, true} {
		reduced := reduced
		t.Run(map[bool]string{false: "normal", true: "reduced-motion"}[reduced], func(t *testing.T) {
			tt := ui.NewTesterWith(func(c *ui.Context) {
				ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
					SpecularTiltShader.Draw(p, ui.Rect{X: 8, Y: 8, W: 80, H: 40}, shaderOptions("SpecularTiltShader"))
					AIStageGlowShader.Draw(p, ui.Rect{X: 96, Y: 8, W: 80, H: 40}, shaderOptions("AIStageGlowShader"))
					LiquidOrbShader.Draw(p, ui.Rect{X: 52, Y: 56, W: 44, H: 44}, shaderOptions("LiquidOrbShader"))
					DrawSpecularSheen(c, p, r, ui.Point{X: .4, Y: .5}, .8, c.Theme().Accent, cfg)
					DrawAIStageGlow(c, p, r, .6, c.Theme().Accent, cfg)
				})
				RenderLiquidOrb(c, 32, true, cfg)
			}, 240, 140, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: reduced}})
			if tt == nil || tt.Image() == nil {
				t.Fatal("headless shader frame did not render")
			}
		})
	}
}

func shaderOptions(_ string) shader.Options {
	return shader.Options{Radius: 8, Uniforms: map[string]any{
		"Pointer": []float32{.5, .5}, "Intensity": float32(.7), "Time": float32(.2),
		"Speed": float32(.8), "Accent": ui.RGBA(80, 170, 255, .8),
	}}
}

func TestT6SpatialPrimitivesAcrossAccessibilityModes(t *testing.T) {
	for _, tc := range []struct {
		preset, animations string
		prefs              ui.Preferences
	}{
		{"spatial_3d", "full", ui.Preferences{}},
		{"liquid_glass", "reduced", ui.Preferences{}},
		{"classic", "full", ui.Preferences{}},
		{"spatial_3d", "off", ui.Preferences{}},
		{"spatial_3d", "full", ui.Preferences{ReduceMotion: true, ReduceTransparency: true}},
	} {
		t.Run(tc.preset+"-"+tc.animations, func(t *testing.T) {
			cfg := testSpatialConfig(tc.preset, tc.animations)
			tt := ui.NewTesterWith(func(c *ui.Context) {
				ui.Column(c).Fill().Gap(8).Padding(12).Children(func() {
					TiltCard3D(c, "card", cfg, TiltCardOpts{Width: 150, Height: 70, Accent: c.Theme().Accent}, func() {
						ui.Text(c, "Bento")
					})
					StageContainer3D(c, "ai", 1, cfg, func() { ui.Text(c, "Stage") })
					CoverFlowDeck3D(c, 3, 1, cfg, func(i int, active bool) { ui.Text(c, string(rune('A'+i))) })
					ui.Row(c).Gap(4).Children(func() {
						TactileKeycap3D(c, "1", true, true, cfg)
						TactileKeycap3D(c, "2", false, false, cfg)
					})
					TelemetryGauge3D(c, "ram", .72, "RAM", "72%", c.Theme().Accent, cfg)
					ColorSwatch3D(c, "#42a5f5", 36, cfg)
					RenderGlassPillGroup(c, cfg, 6, func(add func(ui.Element)) {
						add(ui.Text(c, "One"))
						add(ui.Text(c, "Two"))
					})
				})
				RenderMacInsetTitleBar(c, c.Theme(), "Clavis", "Unified input", func() { ui.Text(c, "⌘") })
			}, 640, 480, ui.TesterOptions{Preferences: tc.prefs})
			if tt.Image() == nil {
				t.Fatal("spatial frame did not render")
			}
		})
	}
}

func TestT6LayoutSizingBounds(t *testing.T) {
	for _, orientation := range []string{"horizontal", "vertical", "HORIZONTAL", "unknown"} {
		for _, count := range []int{-4, 0, 1, 9, 100} {
			w, h := PreferredCandidateBarSize(count, orientation, count%2 == 0)
			if w <= 0 || h <= 0 || w > 620 || h > 460 {
				t.Fatalf("candidate %q/%d = %dx%d outside bounds", orientation, count, w, h)
			}
		}
	}
	for _, preview := range []bool{false, true} {
		for _, slots := range []int{-1, 1, 8, 100} {
			w, h := PreferredPromptComposerSize(slots, preview)
			if w < 420 || w > 960 || h < 116 || h > 360 {
				t.Fatalf("composer %d/%t = %dx%d outside bounds", slots, preview, w, h)
			}
		}
	}
	for _, actions := range []int{-1, 0, 2, 8, 100} {
		w, h := PreferredSelectionBarSize(actions)
		if w < 120 || w > 680 || h != 48 {
			t.Fatalf("selection %d = %dx%d outside bounds", actions, w, h)
		}
	}
}
