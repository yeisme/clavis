package uiapp

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func nearlyStage(t *testing.T, name string, got, want float32) {
	t.Helper()
	if math.Abs(float64(got-want)) > 1e-3 {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func TestStageResolutionMatrix(t *testing.T) {
	presets := []string{"classic", "liquid_glass", "spatial_3d", "", "bogus"}
	animations := []string{"full", "reduced", "off"}
	for _, preset := range presets {
		for _, animation := range animations {
			for _, reducedMotion := range []bool{false, true} {
				for _, reduceTransparency := range []bool{false, true} {
					for _, highContrast := range []bool{false, true} {
						for _, accelerated := range []bool{false, true} {
							for _, composing := range []bool{false, true} {
								for _, intensity := range []float64{0, .35, .7, 1} {
									in := stageInputs{Preset: preset, Animations: animation, Intensity: intensity, ObliqueRows: true, Composing: composing, ReducedMotion: reducedMotion, ReduceTransparency: reduceTransparency, HighContrast: highContrast, Accelerated: accelerated}
									plan := resolveStagePlan(in)
									name := preset + "/" + animation
									motionIdentity := reducedMotion || animation == "reduced" || animation == "off"
									classicIdentity := preset == "classic"
									if motionIdentity || classicIdentity {
										if plan != (stagePlan{Frozen: true}) {
											t.Fatalf("%s: identity plan = %+v", name, plan)
										}
										continue
									}
									k := stageClamp01(intensity)
									spatial := preset != "liquid_glass"
									if plan.Enabled3D != spatial || (!composing && plan.Frozen) || plan.EnabledShaders != (accelerated && !reduceTransparency) {
										t.Fatalf("%s: gates -> %+v", name, plan)
									}
									nearlyStage(t, name+" yaw", plan.Yaw, func() float32 {
										if spatial && !composing {
											return 7 * k
										}
										return 0
									}())
									nearlyStage(t, name+" pitch", plan.Pitch, func() float32 {
										if spatial && !highContrast && !composing {
											return 2.5 * k
										}
										return 0
									}())
									nearlyStage(t, name+" skew", plan.RowSkew, func() float32 {
										if spatial && !composing {
											return 5 * k
										}
										return 0
									}())
									nearlyStage(t, name+" parallax", plan.Parallax, func() float32 {
										if spatial && !composing {
											return .55 * k
										}
										return 0
									}())
									nearlyStage(t, name+" specular", plan.Specular, func() float32 {
										if !highContrast && !composing {
											return .7 * k
										}
										return 0
									}())
									if plan.Perspective != 1000 && spatial {
										t.Fatalf("%s: Perspective = %v, want 1000", name, plan.Perspective)
									}
									if plan.Perspective != 0 && !spatial {
										t.Fatalf("%s: planar Perspective = %v", name, plan.Perspective)
									}
									if composing {
										if !plan.Frozen || plan.Yaw != 0 || plan.Pitch != 0 || plan.RowSkew != 0 || plan.Parallax != 0 || plan.Specular != 0 {
											t.Fatalf("%s: composing did not flatten plan %+v", name, plan)
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

func TestStageFanGeometry(t *testing.T) {
	plan := stagePlan{Enabled3D: true, RowSkew: 5}
	selectedSkew, selectedZ := stageRowPose(plan, 3, 3, nil, ui.Element{})
	if selectedSkew != 0 || selectedZ != 0 {
		t.Fatalf("selected row pose = (%v, %v), want (0, 0)", selectedSkew, selectedZ)
	}
	previousZ := float32(0)
	for distance := 1; distance <= 8; distance++ {
		left, leftZ := stageRowPose(plan, 3-distance, 3, nil, ui.Element{})
		right, rightZ := stageRowPose(plan, 3+distance, 3, nil, ui.Element{})
		nearlyStage(t, "fan symmetry", left, -right)
		if leftZ != rightZ || leftZ > previousZ {
			t.Fatalf("distance %d z values = %v/%v, want monotonic depth", distance, leftZ, rightZ)
		}
		previousZ = leftZ
	}
	left, _ := stageRowPose(plan, -20, 0, nil, ui.Element{})
	right, _ := stageRowPose(plan, 20, 0, nil, ui.Element{})
	nearlyStage(t, "negative clamp", left, -5)
	nearlyStage(t, "positive clamp", right, 5)
}

func TestStageFrozenRequestsNoFrames(t *testing.T) {
	for _, in := range []stageInputs{
		{Preset: "spatial_3d", Animations: "reduced", Accelerated: true},
		{Preset: "spatial_3d", Animations: "off", Accelerated: true},
		{Preset: "spatial_3d", Animations: "full", Composing: true, Accelerated: true},
	} {
		if stageRequestsAnimationFrames(resolveStagePlan(in)) {
			t.Fatalf("frozen input requested animation frames: %+v", in)
		}
	}
	if !stageRequestsAnimationFrames(resolveStagePlan(stageInputs{Preset: "spatial_3d", Animations: "full", Intensity: 1, Accelerated: true})) {
		t.Fatal("active spatial plan did not request animation frames")
	}
}

func TestStagePhaseIndex(t *testing.T) {
	for phase, want := range map[engine.CapsulePhase]int{engine.PhaseLeader: -1, engine.PhaseOmnibox: 0, engine.PhaseAIStreaming: 1} {
		if got := stagePhaseIndex(phase); got != want {
			t.Fatalf("stagePhaseIndex(%v) = %d, want %d", phase, got, want)
		}
	}
}

func TestStageSpatialAdapterAgreement(t *testing.T) {
	for _, tc := range []struct {
		name, preset, animations string
		prefs                    ui.Preferences
	}{
		{"spatial", "spatial_3d", "full", ui.Preferences{}},
		{"unknown", "future_stage", "full", ui.Preferences{}},
		{"liquid", "liquid_glass", "full", ui.Preferences{}},
		{"classic", "classic", "full", ui.Preferences{}},
		{"reduced", "spatial_3d", "reduced", ui.Preferences{}},
		{"reduce-motion", "spatial_3d", "full", ui.Preferences{ReduceMotion: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultStageAdapterConfig(tc.preset, tc.animations)
			var got bool
			tester := ui.NewTesterWith(func(c *ui.Context) {
				got = Spatial3DEnabled(c, cfg)
				ui.Box(c).Fill()
			}, 80, 40, ui.TesterOptions{Preferences: tc.prefs})
			tester.Frame()
			want := resolveStagePlan(stageInputs{Preset: tc.preset, Animations: tc.animations, ReducedMotion: tc.prefs.ReduceMotion, Intensity: 1}).Enabled3D
			if got != want {
				t.Fatalf("Spatial3DEnabled = %v, resolver = %v", got, want)
			}
		})
	}
}

func defaultStageAdapterConfig(preset, animations string) config.AppearanceConfig {
	cfg := config.DefaultAppearanceConfig()
	cfg.VisualPreset = preset
	cfg.Animations = animations
	return cfg
}
