package uiapp

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// renderGuide renders the first-run guide for a fresh Mac (no permissions)
// or with both permissions granted, at the guide's 520×520 window.
func renderGuide(t *testing.T, granted bool, dark bool) *ui.Tester {
	t.Helper()
	f := newInteractionFixture(t, nil)
	_ = f.ctrl.Stop()
	f.bridge.mu.Lock()
	if granted {
		f.bridge.perms = host.PermissionStatus{Accessibility: true, InputMonitoring: true, Granted: true}
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
	if n := countText(fresh, grant); n != 2 {
		t.Fatalf("fresh guide shows %q %d times, want 2 (one button per permission); texts %q", grant, n, fresh.Texts())
	}
	if n := countText(fresh, ok); n != 0 {
		t.Fatalf("fresh guide claims %q %d times", ok, n)
	}
	done := renderGuide(t, true, false)
	if n := countText(done, grant); n != 0 {
		t.Fatalf("granted guide still shows %q %d times", grant, n)
	}
	if n := countText(done, ok); n != 2 {
		t.Fatalf("granted guide shows %q %d times, want 2", ok, n)
	}
}
