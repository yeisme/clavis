package uiapp

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// SurfaceDismissPolicy describes which window-level exits a floating surface
// accepts. The surface owns the policy wiring, while the caller owns the
// business action performed by Dismiss.
type SurfaceDismissPolicy uint8

const (
	DismissExplicit SurfaceDismissPolicy = 1 << iota
	DismissEscape
	DismissOutsideClick
	// DismissBlur is the specification name for an outside-click dismissal.
	// Keep DismissOutsideClick as the readable call-site alias.
	DismissBlur = DismissOutsideClick
)

// SurfaceRadiusKind keeps a radius tied to its source instead of scattering
// pixel literals through window construction.
type SurfaceRadiusKind uint8

const (
	SurfaceRadiusNone SurfaceRadiusKind = iota
	SurfaceRadiusAppearance
	SurfaceRadiusHalfHeight
)

// SurfaceRadiusSystem leaves the native window corner treatment unchanged.
// It is the explicit spelling for ordinary document windows.
const SurfaceRadiusSystem = SurfaceRadiusNone

type SurfaceRadius struct {
	Kind  SurfaceRadiusKind
	value float32
}

// SurfaceFocusRestore describes where keyboard focus belongs after dismissal.
type SurfaceFocusRestore uint8

const (
	FocusNever SurfaceFocusRestore = iota
	FocusPanel
	FocusPreviousApp
)

func AppearanceSurfaceRadius(appearance config.AppearanceConfig) SurfaceRadius {
	return SurfaceRadius{Kind: SurfaceRadiusAppearance, value: NormalizeCornerRadius(appearance)}
}

func HalfHeightSurfaceRadius() SurfaceRadius {
	return SurfaceRadius{Kind: SurfaceRadiusHalfHeight}
}

func (r SurfaceRadius) resolve(height int) int {
	switch r.Kind {
	case SurfaceRadiusAppearance:
		return int(r.value)
	case SurfaceRadiusHalfHeight:
		if height > 0 {
			return height / 2
		}
	}
	return 0
}

// SurfaceSpec is the complete shared contract for a floating window. Content,
// placement and domain-specific close actions remain with the caller.
type SurfaceSpec struct {
	Elevation int
	Radius    SurfaceRadius
	Blur      mygo.Vibrancy
	Dismiss   SurfaceDismissPolicy
	// FocusRestore requests the caller's restore hook after a successful
	// dismissal. Panel surfaces rely on MyGo's native panel restoration and
	// document surfaces provide the previous-app fallback.
	FocusRestore SurfaceFocusRestore
}

const (
	SurfaceElevationFlat      = elevNone
	SurfaceElevationRaised    = elev2
	SurfaceElevationPanel     = elev3
	SurfaceElevationIndicator = elev4
	// SurfaceElevationFloating is kept as the default for callers that do not
	// need to distinguish the panel and indicator tiers.
	SurfaceElevationFloating = SurfaceElevationIndicator
)

// FloatingSurface applies the shared visual properties to a MyGo window
// option set. Existing options remain authoritative for content, placement,
// focus mode and window lifetime.
func FloatingSurface(opts mygo.WindowOptions, spec SurfaceSpec) mygo.WindowOptions {
	if spec.Radius.Kind != SurfaceRadiusNone {
		opts.CornerRadius = spec.Radius.resolve(opts.Height)
	}
	if spec.Blur != mygo.VibrancyNone {
		opts.Transparent = true
		opts.Vibrancy = spec.Blur
	} else {
		opts.Transparent = false
		opts.Vibrancy = mygo.VibrancyNone
	}
	if opts.Panel {
		// MyGo exposes native shadow as a boolean. The geometry and alpha for
		// inner cards remain owned by elevationStep/elevationShadow.
		opts.DisableShadow = spec.Elevation <= elevNone
	}
	return opts
}

type SurfaceHooks struct {
	Dismiss             func()
	RestoreFocus        func()
	CanDismiss          func() bool
	InPresentTransition func() bool
	Quitting            func() bool
}

// BindFloatingSurface centralizes close and outside-click policy. The close
// callback is deliberately not once-only because a retained window can be
// shown and dismissed repeatedly during the app lifetime.
func BindFloatingSurface(win *mygo.Window, spec SurfaceSpec, hooks SurfaceHooks) {
	if win == nil {
		return
	}
	finishDismiss := func() {
		if hooks.Dismiss != nil {
			hooks.Dismiss()
		}
		if spec.FocusRestore != FocusNever && hooks.RestoreFocus != nil {
			hooks.RestoreFocus()
		}
	}
	if spec.Dismiss&DismissExplicit != 0 || spec.Dismiss&DismissEscape != 0 {
		win.OnClose(func(e *mygo.CloseEvent) {
			if hooks.Quitting != nil && hooks.Quitting() {
				return
			}
			e.PreventDefault()
			finishDismiss()
		})
	}
	if spec.Dismiss&DismissOutsideClick != 0 {
		win.OnBlur(func() {
			if hooks.Quitting != nil && hooks.Quitting() {
				return
			}
			if hooks.InPresentTransition != nil && hooks.InPresentTransition() {
				return
			}
			if hooks.CanDismiss != nil && !hooks.CanDismiss() {
				return
			}
			finishDismiss()
		})
	}
}

// SurfaceEscapeKind names the surface handling an Escape key press.
type SurfaceEscapeKind uint8

const (
	SurfaceEscapeCapsule SurfaceEscapeKind = iota
	SurfaceEscapePrompt
	SurfaceEscapeSettings
)

// surfaceEscape centralizes top-level Escape dismissal across floating surfaces
// so IME composition consistently cancels before any surface closes (SF-09/SF-10).
func (a *AppController) surfaceEscape(kind SurfaceEscapeKind) bool {
	if a == nil {
		return false
	}
	switch kind {
	case SurfaceEscapePrompt:
		if a.promptUI.isComposing {
			return false
		}
		a.ClosePromptComposer()
		return true
	case SurfaceEscapeCapsule:
		if a.uiState.isComposing {
			return false
		}
		a.Dispatch(engine.Event{Type: engine.EventEscape})
		return true
	case SurfaceEscapeSettings:
		return false
	default:
		return false
	}
}

func FloatingSurfaceSpec(appearance config.AppearanceConfig, height int, blur mygo.Vibrancy, dismiss SurfaceDismissPolicy, focusRestore SurfaceFocusRestore) SurfaceSpec {
	return FloatingSurfaceSpecWithElevation(appearance, height, SurfaceElevationFloating, blur, dismiss, focusRestore)
}

// FloatingSurfaceSpecWithElevation builds a surface contract while keeping the
// five shared concerns in one place. Window-specific business behavior stays
// in the caller's hooks.
func FloatingSurfaceSpecWithElevation(appearance config.AppearanceConfig, height, elevation int, blur mygo.Vibrancy, dismiss SurfaceDismissPolicy, focusRestore SurfaceFocusRestore) SurfaceSpec {
	radius := AppearanceSurfaceRadius(appearance)
	if height > 0 && height <= 40 {
		radius = HalfHeightSurfaceRadius()
	}
	return SurfaceSpec{
		Elevation:    elevation,
		Radius:       radius,
		Blur:         blur,
		Dismiss:      dismiss,
		FocusRestore: focusRestore,
	}
}

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
