package uiapp

import (
	"strings"
	"sync"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

var (
	candidateAppearanceMu sync.RWMutex
	candidateAppearance   = config.DefaultAppearanceConfig()
)

// SetCandidateAppearance publishes the active AppearanceConfig for cursor-side
// candidate sizing and rendering callers that use the zero-config entry points.
func SetCandidateAppearance(cfg config.AppearanceConfig) {
	candidateAppearanceMu.Lock()
	candidateAppearance = cfg
	candidateAppearanceMu.Unlock()
}

// CandidateAppearance returns the active AppearanceConfig used by
// PreferredCandidatePanelSize and InputCandidateView.
func CandidateAppearance() config.AppearanceConfig {
	candidateAppearanceMu.RLock()
	cfg := candidateAppearance
	candidateAppearanceMu.RUnlock()
	return cfg
}

func isClassicPreset(cfg config.AppearanceConfig) bool {
	return strings.EqualFold(strings.TrimSpace(cfg.VisualPreset), "classic")
}

func isLiquidGlassPreset(cfg config.AppearanceConfig) bool {
	return strings.EqualFold(strings.TrimSpace(cfg.VisualPreset), "liquid_glass")
}

func isCandidateVertical(cfg config.AppearanceConfig) bool {
	return strings.EqualFold(strings.TrimSpace(cfg.CandidateBarOrientation), "vertical")
}

// effectiveSpatialAppearance resolves the per-frame AppearanceConfig passed to
// spatial3d/shader helpers so Animations:"reduced"/"off", ReduceMotion,
// "liquid_glass" (planar 2D glass + shaders), and "classic" degrade cleanly
// without mutating shared kit internals.
func effectiveSpatialAppearance(c *ui.Context, cfg config.AppearanceConfig) config.AppearanceConfig {
	eff := cfg
	if c == nil {
		eff.Enable3DEffects = false
		eff.EnableShaders = false
		return eff
	}
	prefs := c.Preferences()
	plan := resolveStagePlan(stageInputs{
		Preset:             cfg.VisualPreset,
		Animations:         cfg.Animations,
		ReducedMotion:      prefs.ReduceMotion,
		ReduceTransparency: prefs.ReduceTransparency,
		Accelerated:        true,
		Intensity:          1,
	})
	// The config capability remains an adapter-level gate; stageInputs keeps
	// the resolver focused on geometry and runtime accessibility inputs.
	eff.Enable3DEffects = cfg.Enable3DEffects && plan.Enabled3D
	eff.EnableShaders = cfg.EnableShaders && plan.EnabledShaders
	if isClassicPreset(cfg) {
		eff.VisualPreset = "classic"
	}
	return eff
}

// capsuleStageIndex maps interactive Capsule phases onto a symmetric -1..+1
// stage axis so Omnibox rests flat (0) and Leader (-1) / AI (+1) stay within ±5°.
func capsuleStageIndex(phase engine.CapsulePhase) (int, bool) {
	switch phase {
	case engine.PhaseLeader:
		return -1, true
	case engine.PhaseOmnibox:
		return 0, true
	case engine.PhaseAIStreaming:
		return 1, true
	default:
		return 0, false
	}
}

// applyCapsuleCardSurface applies Liquid Glass (with opaque accessibility
// fallback) to the Capsule card, or preserves the exact pre-spatial surface for
// the "classic" preset.
// glassBodyTint is the wash laid over the native material behind panel text. 0.92 keeps the
// surface recognisably glass at its edges while pushing the body close to opaque, which is what
// text legibility needs; Apple's materials guidance likewise keeps content backplates opaque.
func glassBodyTint(theme *ui.Theme) ui.Color {
	if theme == nil {
		return ui.RGBA(0, 0, 0, 0)
	}
	if theme.Dark {
		return theme.Background.Alpha(0.92)
	}
	return theme.Background.Alpha(0.94)
}

func applyCapsuleCardSurface(c *ui.Context, card ui.Element, theme *ui.Theme, cfg config.AppearanceConfig) ui.Element {
	prefs := c.Preferences()
	radius := NormalizeCornerRadius(cfg)
	if isClassicPreset(cfg) {
		card.Radius(radius)
		if !prefs.ReduceTransparency {
			card.Material(glass.Glass{Native: c.Vibrancy()})
			if prefs.HighContrast {
				card.Border(1.5, theme.Border)
			} else {
				card.Border(1, withAlpha(theme.Border, 0.5))
			}
		} else {
			borderW := float32(1)
			if prefs.HighContrast {
				borderW = 1.5
			}
			card.Background(theme.Background).Border(borderW, theme.Border)
		}
		return card
	}

	// Tint the glass toward the theme background so the desktop behind cannot read through the
	// body text. Without it a terminal behind the panel shows through between glyphs, which was
	// reported as "the text bleeds together / colours look wrong" — the glyphs were competing
	// with whatever sat behind them rather than with each other.
	//
	// Not fully opaque: the outer edge keeps the material's translucency, which is the look the
	// brief asks for ("glass for the floating surface"), while the reading area stays legible.
	GlassSurface(c, card, cfg, GlassOpts{
		Native: c.Vibrancy(),
		Radius: radius,
		Tint:   glassBodyTint(c.Theme()),
	})
	if prefs.HighContrast {
		card.Border(1.5, theme.Border)
	}
	return card
}

// drawCapsuleAIHeaderGlow paints the AIStageGlowShader aura (or its zero-cost
// CPU/static fallback) behind the Capsule header during PhaseAIStreaming.
func drawCapsuleAIHeaderGlow(
	c *ui.Context,
	p *ui.Painter,
	r ui.Rect,
	theme *ui.Theme,
	cfg config.AppearanceConfig,
	state engine.State,
) {
	if state.Phase != engine.PhaseAIStreaming || isClassicPreset(cfg) || c == nil || p == nil {
		return
	}
	if c.Preferences().ReduceTransparency {
		return
	}
	glowCfg := effectiveSpatialAppearance(c, cfg)
	accent := theme.Accent
	intensity := float32(0.62)
	if state.AIError != "" {
		accent = theme.Danger
		intensity = 0.24
		glowCfg.EnableShaders = false
	} else if state.AIComplete {
		intensity = 0.28
		glowCfg.EnableShaders = false
	}
	DrawAIStageGlow(c, p, r, intensity, accent, glowCfg)
}
