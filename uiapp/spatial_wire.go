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

// glassBodyTint is the wash laid over the native material of the small floating notices (HUD
// line, IME pill, selection bar), whose whole face is one short label: 0.92 keeps the surface
// recognisably glass at its edges while pushing the label's backplate close to opaque.
func glassBodyTint(theme *ui.Theme) ui.Color {
	if theme == nil {
		return ui.RGBA(0, 0, 0, 0)
	}
	if theme.Dark {
		return theme.Background.Alpha(0.92)
	}
	return theme.Background.Alpha(0.94)
}

// shellGlassTint is the tint of a floating panel's Liquid Glass shell — the part of the panel
// that is chrome (title bar, tabs, edge), never the reading area: #F5F5F7 at 82% in light mode,
// #1E1E20 at 80% in dark mode. Everything a user reads sits on contentSurface instead.
func shellGlassTint(theme *ui.Theme) ui.Color {
	if theme == nil {
		return ui.RGBA(0, 0, 0, 0)
	}
	if theme.Dark {
		return ui.Hex("#1E1E20").Alpha(0.80)
	}
	return ui.Hex("#F5F5F7").Alpha(0.82)
}

// contentSurface is the backplate of every reading area — lists, previews, AI answers, dialog
// bodies, settings panes. It is the theme's content color at full opacity: the owner's rule is
// glass for floating shells only, so wallpaper and the windows underneath never show through
// text.
func contentSurface(theme *ui.Theme) ui.Color {
	bg := theme.Background
	bg.A = 255
	return bg
}

// applyCapsuleCardSurface makes the Capsule card a floating Liquid Glass shell (with an opaque
// accessibility fallback), or keeps the plain native material of the "classic" preset. Only
// the shell is glass: CapsuleView lays the body over contentSurface.
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

	// The shell (title bar, tabs, edge) is the translucent part of the panel. The reading area is
	// not: CapsuleView paints it with the opaque contentSurface, because a tinted-but-translucent
	// body let a terminal behind the panel show through between glyphs ("the text bleeds
	// together / colours look wrong").
	GlassSurface(c, card, cfg, GlassOpts{
		Native: c.Vibrancy(),
		Radius: radius,
		Tint:   shellGlassTint(theme),
	})
	if prefs.HighContrast {
		card.Border(1.5, theme.Border)
	}
	return card
}
