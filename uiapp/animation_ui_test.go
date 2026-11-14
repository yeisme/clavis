package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func openOmniboxForAnim(t *testing.T, ctrl *AppController) {
	t.Helper()
	ctrl.Dispatch(engine.Event{
		Type:    engine.EventOpenOmnibox,
		Tab:     config.TabActions,
		Context: &engine.CapsuleContext{AppName: "Notes"},
	})
}

// TestMotionProfileResolution pins the mode → level mapping: explicit
// values force a level, auto follows the system reduce-motion preference,
// and unknown values fall back to auto.
func TestMotionProfileResolution(t *testing.T) {
	full := ui.Preferences{}
	reduce := ui.Preferences{ReduceMotion: true}

	if p := motionProfileFor("full", reduce); !p.MotionEnabled() {
		t.Fatal("full must stay full even under reduce-motion")
	}
	if p := motionProfileFor("reduced", full); p.MotionEnabled() || !p.Animating() {
		t.Fatal("reduced must animate opacity but no motion")
	}
	if p := motionProfileFor("off", full); p.Animating() || p.ExitHideDelay() != 0 {
		t.Fatal("off must not animate nor delay the hide")
	}
	if p := motionProfileFor("auto", full); !p.MotionEnabled() {
		t.Fatal("auto without reduce-motion should be full")
	}
	if p := motionProfileFor("auto", reduce); p.MotionEnabled() {
		t.Fatal("auto with reduce-motion should be reduced")
	}
	if p := motionProfileFor("garbage", full); !p.MotionEnabled() {
		t.Fatal("unknown mode should fall back to auto (=full here)")
	}
	if got := motionProfileFor("full", full).capsuleEnter; got != 160*time.Millisecond {
		t.Fatalf("capsule enter = %v, want 160ms", got)
	}
	if got := motionProfileFor("full", full).capsuleExit; got != 110*time.Millisecond {
		t.Fatalf("capsule exit = %v, want 110ms", got)
	}
	if got := motionProfileFor("full", full).rowStagger; got != 16*time.Millisecond {
		t.Fatalf("row stagger = %v, want 16ms", got)
	}
	if got := motionProfileFor("full", full).cursorPulse; got != 900*time.Millisecond {
		t.Fatalf("cursor pulse = %v, want 900ms", got)
	}
	if frames := shakeKeyframes(motionProfileFor("full", full)); len(frames) != 7 {
		t.Fatalf("shake keyframes = %d, want 7 (two jolts)", len(frames))
	}
	if frames := shakeKeyframes(motionProfileFor("reduced", full)); frames != nil {
		t.Fatal("reduced must not shake")
	}
}

// TestCapsuleOpenAnimationKeepsFramesComing opens the omnibox and verifies
// the enter animation keeps frames coming (60fps repaints) until it
// settles after its 160ms duration — without blocking input delivery,
// which the tester's synchronous event queue demonstrates.
func TestCapsuleOpenAnimationKeepsFramesComing(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	ctrl, _, _, _ := newTestController(t, nil)
	openOmniboxForAnim(t, ctrl)

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame() // phase hidden: an empty capsule is the animation's baseline
	openOmniboxForAnim(t, ctrl)
	tt.Frame() // the card appears: the enter animation starts
	if !tt.FramePending() {
		t.Fatal("expected pending frames right after open (enter animation)")
	}
	// Halfway the animation must still be running.
	tt.Advance(80 * time.Millisecond)
	if !tt.FramePending() {
		t.Fatal("animation should still run at 80ms of 160ms")
	}
	// Past the duration (plus the slowest row stagger) it settles.
	tt.Advance(8*16*time.Millisecond + 200*time.Millisecond)
	if tt.FramePending() {
		t.Fatal("animation should have settled")
	}
	// Input still lands after/during animation frames.
	tt.Key(ui.Cmd, ui.Key2)
	tt.Frame()
	if ctrl.State().OmniboxTab != config.TabClipboard {
		t.Fatalf("input during/after animation did not switch tab, tab=%s", ctrl.State().OmniboxTab)
	}
}

// TestAnimationsDisabledJumpsInstant verifies animations = "off" renders
// the same open with no pending frames and a zero exit-hide delay.
func TestAnimationsDisabledJumpsInstant(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	ctrl, _, _, _ := newTestController(t, nil)
	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.Animations = "off" }); err != nil {
		t.Fatalf("set animations=off: %v", err)
	}
	openOmniboxForAnim(t, ctrl)

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame() // baseline: hidden
	openOmniboxForAnim(t, ctrl)
	tt.Frame() // the card appears — with animations off nothing moves
	if tt.FramePending() {
		t.Fatal("animations=off must not request animation frames")
	}
	if d := ctrl.ExitHideDelay(); d != 0 {
		t.Fatalf("exit hide delay = %v, want 0 when off", d)
	}
}

// TestReducedMotionShortensEnter verifies the system reduce-motion
// preference (auto mode) halves the enter duration and drops the stagger.
func TestReducedMotionShortensEnter(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	ctrl, _, _, _ := newTestController(t, nil)

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTesterWith(ctrl.CapsuleView, w, h, ui.TesterOptions{
		Preferences: ui.Preferences{ReduceMotion: true},
	})
	tt.Frame() // baseline: hidden
	openOmniboxForAnim(t, ctrl)
	tt.Frame() // reduced enter starts (fade only, 80ms)
	tt.Advance(120 * time.Millisecond)
	tt.Frame()
	if tt.FramePending() {
		t.Fatal("reduced-motion enter (80ms) should have settled by 120ms")
	}
}
