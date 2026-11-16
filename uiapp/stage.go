package uiapp

import (
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// stageInputs is the pure input to resolveStagePlan. Intensity and ObliqueRows
// are deliberately kept out of AppearanceConfig: they describe the stage,
// rather than the persisted appearance settings.
type stageInputs struct {
	Phase              engine.CapsulePhase
	Preset             string
	Animations         string
	Intensity          float64
	ObliqueRows        bool
	Composing          bool
	ReducedMotion      bool
	ReduceTransparency bool
	HighContrast       bool
	Accelerated        bool
}

// stagePlan is the renderer's complete decision for a frame of the stage.
type stagePlan struct {
	Enabled3D      bool
	EnabledShaders bool
	Frozen         bool
	Yaw            float32
	Pitch          float32
	Perspective    float32
	RowSkew        float32
	Parallax       float32
	Specular       float32
}

func stageIdentity() stagePlan {
	return stagePlan{Frozen: true}
}

func stageClamp01(v float64) float32 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return float32(v)
}

// resolveStagePlan is intentionally pure. All accessibility and renderer
// capabilities needed to make a stage decision are supplied by stageInputs.
func resolveStagePlan(in stageInputs) stagePlan {
	preset := strings.ToLower(strings.TrimSpace(in.Preset))
	animations := strings.ToLower(strings.TrimSpace(in.Animations))
	motionOff := in.ReducedMotion || animations == "reduced" || animations == "off"
	if motionOff {
		return stageIdentity()
	}
	if preset == "classic" {
		return stageIdentity()
	}

	k := stageClamp01(in.Intensity)
	plan := stagePlan{Frozen: false}
	if preset == "liquid_glass" {
		// Liquid Glass is explicitly planar; it can still use a shader sheen.
		plan.Enabled3D = false
	} else {
		// Empty and unknown presets intentionally use the signature spatial
		// treatment, so a future preset cannot silently lose the stage.
		plan.Enabled3D = true
		plan.Perspective = 1000
		plan.Yaw = 7 * k
		plan.Pitch = 2.5 * k
		if in.ObliqueRows {
			plan.RowSkew = 5 * k
		}
		plan.Parallax = .55 * k
	}
	plan.Specular = .7 * k
	plan.EnabledShaders = !in.ReduceTransparency && in.Accelerated

	if in.Composing {
		plan.Frozen = true
		plan.Yaw = 0
		plan.Pitch = 0
		plan.RowSkew = 0
		plan.Parallax = 0
		plan.Specular = 0
	}
	if in.HighContrast {
		plan.Pitch = 0
		plan.Specular = 0
	}
	if k == 0 {
		plan.Yaw = 0
		plan.Pitch = 0
		plan.RowSkew = 0
		plan.Parallax = 0
	}
	return plan
}

// stagePhaseIndex is the stage-only spelling of capsuleStageIndex. Keeping the
// mapping in one place avoids phase-specific camera drift between renderers.
func stagePhaseIndex(phase engine.CapsulePhase) int {
	index, _ := capsuleStageIndex(phase)
	return index
}

func stageCameraPose(plan stagePlan, phaseIndex int) (yaw, pitch float32) {
	return plan.Yaw * float32(phaseIndex), plan.Pitch
}

// stageApplyTransform applies the camera pose without introducing a second
// animation source. Frozen plans deliberately use immediate values; callers
// can therefore render IME preedit and reduced-motion frames synchronously.
func stageApplyTransform(_ *ui.Context, el ui.Element, plan stagePlan, phaseIndex int) ui.Element {
	if !plan.Enabled3D {
		return el
	}
	yaw, pitch := stageCameraPose(plan, phaseIndex)
	return el.Perspective(plan.Perspective).
		RotateY(yaw).
		RotateX(pitch).
		TranslateZ(float32(phaseIndex) * 4)
}

func stageRowPose(plan stagePlan, index, selected int, _ *ui.Context, _ ui.Element) (skew, z float32) {
	delta := float32(index - selected)
	if delta == 0 {
		return 0, 0
	}
	skew = delta * 1.2
	if skew > plan.RowSkew {
		skew = plan.RowSkew
	}
	if skew < -plan.RowSkew {
		skew = -plan.RowSkew
	}
	if delta < 0 {
		delta = -delta
	}
	if delta > 6 {
		delta = 6
	}
	return skew, -delta * 2
}

func stageApplyRow(_ *ui.Context, el ui.Element, plan stagePlan, index, selected int) ui.Element {
	if !plan.Enabled3D {
		return el
	}
	skew, z := stageRowPose(plan, index, selected, nil, el)
	return el.Perspective(plan.Perspective).RotateY(skew).TranslateZ(z)
}

// stageSpecular adapts the existing shader/fallback helper to the stage's
// already-resolved intensity and shader capability.
func stageSpecular(c *ui.Context, p *ui.Painter, r ui.Rect, plan stagePlan, accent ui.Color) {
	if plan.Specular <= 0 {
		return
	}
	cfg := config.DefaultAppearanceConfig()
	cfg.VisualPreset = "spatial_3d"
	cfg.EnableShaders = plan.EnabledShaders
	DrawSpecularSheen(c, p, r, ui.Point{X: .35, Y: .28}, plan.Specular, accent, cfg)
}

func stageRequestsAnimationFrames(plan stagePlan) bool {
	return !plan.Frozen
}
