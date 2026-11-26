package uiapp

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// renderGuide renders the first-run guide for a fresh Mac (no permissions)
// or with all required permissions granted, at the guide's 520×520 window.
func renderGuide(t *testing.T, granted bool, dark bool) *ui.Tester {
	t.Helper()
	f := newInteractionFixture(t, nil)
	_ = f.ctrl.Stop()
	f.bridge.mu.Lock()
	if granted {
		f.bridge.perms = host.PermissionStatus{Accessibility: true, InputMonitoring: true, ScreenRecording: true, Granted: true, Details: map[string]string{"screenRecording": "granted"}}
	} else {
		f.bridge.perms = host.PermissionStatus{}
	}
	f.bridge.mu.Unlock()
	_, _ = f.ctrl.RefreshPermissions(context.Background())
	tt := ui.NewTester(func(c *ui.Context) { f.ctrl.renderOnboarding(c, "darwin") }, 520, 520)
	tt.SetDark(dark)
	tt.Frame()
	return tt
}

func countText(tt *ui.Tester, want string) int {
	n := 0
	for _, s := range tt.Texts() {
		if s == want {
			n++
		}
	}
	return n
}

// TestOnboardingPermissionRowsSayGrantOnce renders the guide: a missing
// permission shows its "Grant…" button and nothing else saying the same (a
// status pill beside it repeated the label word for word); a granted one
// shows the "Granted" pill and no button.
func TestOnboardingPermissionRowsSayGrantOnce(t *testing.T) {
	grant, ok := i18n.T("onboarding.perm.grant"), i18n.T("settings.perm.granted")
	fresh := renderGuide(t, false, false)
	if n := countText(fresh, grant); n != 3 {
		t.Fatalf("fresh guide shows %q %d times, want 3 (one button per permission); texts %q", grant, n, fresh.Texts())
	}
	if n := countText(fresh, ok); n != 0 {
		t.Fatalf("fresh guide claims %q %d times", ok, n)
	}
	done := renderGuide(t, true, false)
	if n := countText(done, grant); n != 0 {
		t.Fatalf("granted guide still shows %q %d times", grant, n)
	}
	if n := countText(done, ok); n != 3 {
		t.Fatalf("granted guide shows %q %d times, want 3", ok, n)
	}
}

// TestOnboardingDecorationIsNeutral reads pixels off the fresh guide, light
// and dark: the progress rail and the step numbers (everything left of the
// step titles) and the permission tiles (left of their labels) carry no
// accent. The accent is left to the primary "Grant…" button.
func TestOnboardingDecorationIsNeutral(t *testing.T) {
	for _, dark := range []bool{false, true} {
		tt := renderGuide(t, false, dark)
		// One system blue in both appearances (theme_macos.go).
		accent := macAccent
		title, ok := tt.Find(i18n.T("onboarding.step.permissions"))
		if !ok {
			t.Fatalf("step title missing; texts %q", tt.Texts())
		}
		header, _ := tt.Find(i18n.T("onboarding.title"))
		// Rail dots and step badges: the strip left of the step titles,
		// below the welcome header.
		if n := accentPixels(tt, 0, int(header.Y+header.H), int(title.X)-2, 520, accent); n != 0 {
			t.Fatalf("dark=%v: %d accent pixels in the progress rail / step numbers", dark, n)
		}
		label, ok := tt.Find(i18n.T("settings.perm.accessibility"))
		if !ok {
			t.Fatal("accessibility row missing")
		}
		if n := accentPixels(tt, int(label.X)-int(space32)-int(space8), int(label.Y)-int(space8), int(label.X)-2, int(label.Y+label.H)+int(space8), accent); n != 0 {
			t.Fatalf("dark=%v: the primary permission's tile paints %d accent pixels", dark, n)
		}
		// The primary button is the one place that keeps the accent.
		btn, ok := tt.Find(i18n.T("onboarding.perm.grant"))
		if !ok || accentPixels(tt, int(btn.X), int(btn.Y), int(btn.X+btn.W), int(btn.Y+btn.H), accent) == 0 {
			t.Fatalf("dark=%v: the primary Grant button lost its accent fill (found=%v)", dark, ok)
		}
	}
}
