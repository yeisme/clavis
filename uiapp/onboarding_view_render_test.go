package uiapp

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// TestOnboardingRenderStates exercises the dense guide at both supported
// window sizes and in both appearance modes. The text checks catch a clipped
// scroll body while the shade check catches a collapsed/empty render.
func TestOnboardingRenderStates(t *testing.T) {
	states := []struct {
		name       string
		ax, im     bool
		relaunch   bool
		start      bool
		leaderSeen bool
	}{
		{name: "fresh"},
		{name: "granted", ax: true, im: true},
		{name: "relaunch-needed", ax: true, im: true, relaunch: true},
		{name: "waiting-for-leader", ax: true, im: true, start: true},
		{name: "ready", ax: true, im: true, start: true, leaderSeen: true},
	}
	for _, dark := range []bool{false, true} {
		for _, size := range []struct{ w, h int }{{600, 740}, {520, 520}} {
			for _, state := range states {
				t.Run(state.name, func(t *testing.T) {
					ctrl, br, _, _ := newTestController(t, nil)
					setMockPerms(t, ctrl, br, state.ax, state.im)
					ctrl.SetRelaunchNeeded(state.relaunch)
					if state.start {
						if err := ctrl.Start(context.Background()); err != nil {
							t.Fatalf("Start: %v", err)
						}
					}
					if state.leaderSeen {
						ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerLeaderKey})
					}
					tt := ui.NewTester(func(c *ui.Context) { ctrl.renderOnboarding(c, "darwin") }, size.w, size.h)
					tt.SetDark(dark)
					tt.Frame()
					img := tt.Image()
					if img == nil || img.Bounds().Empty() {
						t.Fatal("rendered no image")
					}
					if shades := distinctShades(img); shades < 8 {
						t.Fatalf("rendered only %d distinct colours", shades)
					}
					for _, title := range []string{i18n.T("onboarding.step.permissions"), i18n.T("onboarding.step.leader"), i18n.T("onboarding.step.shortcuts")} {
						if !tt.HasText(title) {
							t.Errorf("missing step title %q", title)
						}
					}
				})
			}
		}
	}
}

func TestOnboardingFinishDisabledUntilReady(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	called := 0
	ctrl.SetOnboardingHooks(OnboardingHooks{Finish: func() { called++ }})
	tt := onboardingTester(ctrl, "darwin")
	if err := tt.Click(i18n.T("onboarding.footer.start")); err != nil {
		t.Fatalf("click disabled action: %v", err)
	}
	if called != 0 {
		t.Fatal("finish action was enabled before the guide was ready")
	}
}
