package uiapp

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// AnimationMode is the [appearance].animations setting: "auto" (default)
// follows the system's reduce-motion preference, the rest force a level.
type AnimationMode string

const (
	AnimationAuto    AnimationMode = "auto"
	AnimationFull    AnimationMode = "full"
	AnimationReduced AnimationMode = "reduced"
	AnimationOff     AnimationMode = "off"
)

// AllAnimationModes lists accepted [appearance].animations values.
func AllAnimationModes() []string {
	return []string{string(AnimationAuto), string(AnimationFull), string(AnimationReduced), string(AnimationOff)}
}

// animationLevel is the resolved strength of motion.
type animationLevel int

const (
	animationFull animationLevel = iota
	animationReduced
	animationOff
)

// motionProfile carries the concrete timings the UI animates with. The
// defaults follow the motion spec: capsule 160ms in / 110ms out with a
// 0.96 scale, 16ms list stagger, 80ms HUD slide+fade, 120ms HUD exit,
// 900ms AI cursor pulse, 120ms error shake (two 60ms jolts) and a 120ms
// snippet flash.
type motionProfile struct {
	level animationLevel

	capsuleEnter time.Duration
	capsuleExit  time.Duration
	rowStagger   time.Duration
	selHighlight time.Duration
	hudEnter     time.Duration
	hudExit      time.Duration
	cursorPulse  time.Duration
	shake        time.Duration
	flash        time.Duration
}

var motionProfiles = map[animationLevel]motionProfile{
	animationFull: {
		level:        animationFull,
		capsuleEnter: 160 * time.Millisecond,
		capsuleExit:  110 * time.Millisecond,
		rowStagger:   16 * time.Millisecond,
		selHighlight: 80 * time.Millisecond,
		hudEnter:     80 * time.Millisecond,
		hudExit:      120 * time.Millisecond,
		cursorPulse:  900 * time.Millisecond,
		shake:        120 * time.Millisecond,
		flash:        120 * time.Millisecond,
	},
	animationReduced: {
		level:        animationReduced,
		capsuleEnter: 80 * time.Millisecond, // opacity only
		capsuleExit:  60 * time.Millisecond,
		rowStagger:   0,
		selHighlight: 80 * time.Millisecond, // color, not motion
		hudEnter:     40 * time.Millisecond, // opacity only
		hudExit:      60 * time.Millisecond,
		cursorPulse:  900 * time.Millisecond,
		shake:        0, // no movement under reduced motion
		flash:        60 * time.Millisecond,
	},
	animationOff: {level: animationOff},
}

// resolveAnimationMode normalizes the configured value ("", "auto", "full",
// "reduced", "off"); unknown values fall back to auto, matching the
// validator's warning-only stance.
func resolveAnimationMode(configured string) AnimationMode {
	switch config.NormalizeAnimationMode(configured) {
	case string(AnimationFull):
		return AnimationFull
	case string(AnimationReduced):
		return AnimationReduced
	case string(AnimationOff):
		return AnimationOff
	default:
		return AnimationAuto
	}
}

// motionProfileFor resolves the motion level from the configured mode and
// the live accessibility preferences (auto follows ReduceMotion).
func motionProfileFor(mode string, prefs ui.Preferences) motionProfile {
	switch resolveAnimationMode(mode) {
	case AnimationFull:
		return motionProfiles[animationFull]
	case AnimationReduced:
		return motionProfiles[animationReduced]
	case AnimationOff:
		return motionProfiles[animationOff]
	default: // auto
		if prefs.ReduceMotion {
			return motionProfiles[animationReduced]
		}
		return motionProfiles[animationFull]
	}
}

// ExitHideDelay is how long the shell should keep the capsule window on
// screen after dismissing, so the exit animation is visible. Zero means
// hide at once.
func (p motionProfile) ExitHideDelay() time.Duration {
	switch p.level {
	case animationFull:
		return p.capsuleExit
	case animationReduced:
		return p.capsuleExit
	default:
		return 0
	}
}

// Animating reports whether any visual motion plays at this level.
func (p motionProfile) Animating() bool { return p.level != animationOff }

// MotionEnabled reports whether spatial motion (scale/slide/stagger/shake)
// plays; reduced keeps only opacity and color transitions.
func (p motionProfile) MotionEnabled() bool { return p.level == animationFull }

// capsuleEnterTransition builds the capsule card's enter/exit transition.
// Full: scale 0.96→1 + fade over 160ms, ease-out cubic; exit reversed over
// 110ms. Reduced: fades only. Off: nothing (frame is instant).
func (p motionProfile) capsuleEnterTransition() ui.ElementTransition {
	if !p.Animating() {
		return ui.ElementTransition{Duration: -1}
	}
	tr := ui.ElementTransition{
		Duration:     p.capsuleEnter,
		Ease:         ui.EaseOut, // cubic ease-out per the spec
		ExitDuration: p.capsuleExit,
		ExitEase:     ui.EaseIn,
	}
	if p.MotionEnabled() {
		tr.Enter = &ui.Motion{Scale: 0.96}
		tr.Exit = &ui.Motion{Scale: 0.98, Opacity: 0}
	} else {
		tr.Enter = &ui.Motion{Opacity: 0}
		tr.Exit = &ui.Motion{Opacity: 0}
	}
	return tr
}

// rowEnterAnimation staggers list rows in: each row fades (and slides 4px
// under full motion) after i·stagger, capped so long lists settle quickly.
// Rows leave at once — ExitDelay below zero keeps teardown snappy.
func (p motionProfile) rowEnterAnimation(index int) *ui.Animation {
	if !p.Animating() {
		return nil
	}
	delay := time.Duration(index) * p.rowStagger
	if delay > 8*p.rowStagger && p.rowStagger > 0 {
		delay = 8 * p.rowStagger
	}
	return &ui.Animation{Duration: 180 * time.Millisecond, Delay: delay, Ease: ui.EaseOut}
}

// rowEnterMotion is where a row comes from as it appears.
func (p motionProfile) rowEnterMotion() *ui.Motion {
	if p.MotionEnabled() {
		return &ui.Motion{Y: 4, Opacity: 0}
	}
	if p.Animating() {
		return &ui.Motion{Opacity: 0}
	}
	return nil
}

// selectionAnimation colors the selected row's background over 80ms —
// color transitions stay smooth even under reduced motion.
func (p motionProfile) selectionAnimation() ui.Animation {
	if p.selHighlight <= 0 {
		return ui.Animation{Duration: -1}
	}
	return ui.Animation{Duration: p.selHighlight, Ease: ui.EaseOut}
}

// hudEnterTransition fades the HUD in (sliding 4px under full motion) over
// 80ms; its exit is the shell's delayed hide plus a 120ms fade.
func (p motionProfile) hudEnterTransition() ui.ElementTransition {
	if !p.Animating() {
		return ui.ElementTransition{Duration: -1}
	}
	tr := ui.ElementTransition{
		Duration:     p.hudEnter,
		Ease:         ui.EaseOut,
		ExitDuration: p.hudExit,
		ExitEase:     ui.EaseIn,
		Exit:         &ui.Motion{Opacity: 0},
	}
	if p.MotionEnabled() {
		tr.Enter = &ui.Motion{Y: -4, Opacity: 0}
	} else {
		tr.Enter = &ui.Motion{Opacity: 0}
	}
	return tr
}

// cursorOpacity returns the AI streaming caret's opacity for the current
// pulse phase (0..1), driven by a 900ms loop. Off/reduced show a steady
// dim caret; the pulse is opacity-only, so it may keep looping under
// reduced motion without moving anything.
func cursorOpacity(el ui.Element, p motionProfile) float32 {
	if !p.Animating() || p.cursorPulse <= 0 {
		return 0.55
	}
	return 0.25 + 0.75*el.Loop("ai-cursor", p.cursorPulse, ui.EaseInOut)
}

// shakeKeyframes builds the two-jolt error shake over 120ms: the value
// oscillates ±1 twice; the caller maps it to a ±3px X offset. Reduced and
// off levels shake nothing (nil frames).
func shakeKeyframes(p motionProfile) []ui.Keyframe {
	if !p.MotionEnabled() || p.shake <= 0 {
		return nil
	}
	half := p.shake / 2
	return []ui.Keyframe{
		{At: 0, Value: 0},
		{At: half / 4, Value: 1, Ease: ui.EaseInOut},
		{At: half / 2, Value: -1, Ease: ui.EaseInOut},
		{At: half * 3 / 4, Value: 1, Ease: ui.EaseInOut},
		{At: half, Value: -1, Ease: ui.EaseInOut},
		{At: half + half/2, Value: 1, Ease: ui.EaseInOut},
		{At: p.shake, Value: 0, Ease: ui.EaseInOut},
	}
}

// DisableAnimationsFor turns the context instant for the off level, so any
// stragglers jump to their targets.
func DisableAnimationsFor(c *ui.Context, p motionProfile) {
	if p.level == animationOff {
		c.DisableAnimations()
	}
}

// appMotionProfile resolves the live motion profile for the active config
// and accessibility preferences.
func appMotionProfile(c *ui.Context, appearance config.AppearanceConfig) motionProfile {
	return motionProfileFor(appearance.Animations, c.Preferences())
}

var _ = i18n.T // keep the i18n import while the package settles
