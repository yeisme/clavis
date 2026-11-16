package uiapp

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// onboardingTester renders the guide as goos shows it.
func onboardingTester(ctrl *AppController, goos string) *ui.Tester {
	tt := ui.NewTester(func(c *ui.Context) { ctrl.renderOnboarding(c, goos) }, 600, 720)
	tt.Frame()
	return tt
}

func setMockPerms(t *testing.T, ctrl *AppController, br *mockBridge, ax, im bool) {
	t.Helper()
	br.mu.Lock()
	br.perms = host.PermissionStatus{Accessibility: ax, InputMonitoring: im, Granted: ax && im}
	br.mu.Unlock()
	if _, err := ctrl.RefreshPermissions(context.Background()); err != nil {
		t.Fatalf("RefreshPermissions: %v", err)
	}
}

// TestOnboardingPermissionsStep walks step 1 on macOS: a Grant button per
// missing permission that opens its settings, a "starting" note once both
// are granted, a restart button when only a new process would start the
// keyboard monitor, and a check once it runs.
func TestOnboardingPermissionsStep(t *testing.T) {
	ctrl, br, _, _ := newTestController(t, nil)
	relaunched := 0
	ctrl.SetOnboardingHooks(OnboardingHooks{Relaunch: func() { relaunched++ }})
	setMockPerms(t, ctrl, br, false, false)

	tt := onboardingTester(ctrl, "darwin")
	for _, want := range []string{
		i18n.T("settings.perm.accessibility"), i18n.T("onboarding.perm.why_accessibility"),
		i18n.T("settings.perm.input_monitoring"), i18n.T("onboarding.perm.why_input_monitoring"),
		i18n.T("onboarding.leader.blocked"),
	} {
		if !tt.HasText(want) {
			t.Errorf("fresh guide misses %q", want)
		}
	}
	if err := tt.Click(i18n.T("onboarding.perm.grant")); err != nil {
		t.Fatalf("click Grant: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		br.mu.Lock()
		defer br.mu.Unlock()
		return slices.Contains(br.openedPerms, "accessibility")
	}, "Grant opens the Accessibility settings (the mock bridge has no guide)")

	setMockPerms(t, ctrl, br, true, true)
	tt.Frame()
	if tt.HasText(i18n.T("onboarding.perm.grant")) {
		t.Error("Grant still offered with both permissions granted")
	}
	if !tt.HasText(i18n.T("onboarding.perm.starting")) {
		t.Error("granted but not listening: want the starting note")
	}
	if tt.HasText(i18n.T("onboarding.perm.relaunch")) {
		t.Error("restart offered before a start attempt failed")
	}

	ctrl.SetRelaunchNeeded(true)
	tt.Frame()
	if !tt.HasText(i18n.T("onboarding.perm.relaunch_hint")) {
		t.Fatal("start failed with every permission granted: want the restart hint")
	}
	if err := tt.Click(i18n.T("onboarding.perm.relaunch")); err != nil || relaunched != 1 {
		t.Fatalf("click Restart: err=%v relaunched=%d", err, relaunched)
	}

	if err := ctrl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	tt.Frame()
	if tt.HasText(i18n.T("onboarding.perm.relaunch")) || tt.HasText(i18n.T("onboarding.perm.starting")) {
		t.Error("the monitor runs: no restart or starting note")
	}
	if !tt.HasText(i18n.T("onboarding.leader.waiting")) {
		t.Error("the monitor runs: the Leader step should wait for the key")
	}
}

// TestOnboardingPermissionsOnWindows: Windows asks for nothing; the step
// says so, or that the keyboard monitor is not running.
func TestOnboardingPermissionsOnWindows(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := onboardingTester(ctrl, "windows")
	if !tt.HasText(i18n.T("onboarding.perm.monitor_stopped")) || tt.HasText(i18n.T("onboarding.perm.grant")) {
		t.Error("Windows, monitor stopped: want the stopped note and no Grant button")
	}
	if err := ctrl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	tt.Frame()
	if !tt.HasText(i18n.T("onboarding.perm.none_needed")) || tt.HasText(i18n.T("settings.perm.accessibility")) {
		t.Error("Windows, monitor running: want 'no permissions needed' and no permission rows")
	}
}

// TestOnboardingLeaderCheck: step 2 names the configured Leader key, shows
// the first entries of the user's Leader menu, and passes when a Leader
// trigger arrives.
func TestOnboardingLeaderCheck(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	if err := ctrl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cfg := ctrl.Config()
	tt := onboardingTester(ctrl, "darwin")
	if !tt.HasText(triggerKeyLabel(cfg.Main.Trigger.LeaderKey, "darwin")) {
		t.Errorf("Leader key %q is not named", triggerKeyLabel(cfg.Main.Trigger.LeaderKey, "darwin"))
	}
	nodes := cfg.Leader.Nodes
	if len(nodes) <= onboardingLeaderPreview {
		t.Fatalf("default Leader menu has %d entries; the test needs more than %d", len(nodes), onboardingLeaderPreview)
	}
	for _, n := range nodes[:onboardingLeaderPreview] {
		if !tt.HasText(i18n.DisplayLabel(n.Label)) {
			t.Errorf("Leader entry %q missing", i18n.DisplayLabel(n.Label))
		}
	}
	if tt.HasText(i18n.DisplayLabel(nodes[onboardingLeaderPreview].Label)) {
		t.Errorf("shows more than %d Leader entries", onboardingLeaderPreview)
	}
	if !tt.HasText(i18n.T("onboarding.leader.waiting")) || tt.HasText(i18n.T("onboarding.leader.seen")) {
		t.Fatal("before the key: want the waiting note")
	}

	ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerLeaderKey})
	tt.Frame()
	if !tt.HasText(i18n.T("onboarding.leader.seen")) {
		t.Error("a Leader trigger did not pass the check")
	}
}

// TestOnboardingFinish: Get Started hands over to the host.
func TestOnboardingFinish(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	finished := 0
	ctrl.SetOnboardingHooks(OnboardingHooks{Finish: func() { finished++ }})
	tt := onboardingTester(ctrl, "darwin")
	if err := tt.Click(i18n.T("onboarding.footer.start")); err != nil || finished != 1 {
		t.Fatalf("click Get Started: err=%v finished=%d", err, finished)
	}
}

func TestShortcutKeyLabels(t *testing.T) {
	cases := []struct {
		shortcut, goos string
		want           []string
	}{
		{"option+space", "darwin", []string{"⌥", "Space"}},
		{"option+space", "windows", []string{"Alt", "Space"}},
		{"alt+shift+c", "darwin", []string{"⌥", "⇧", "C"}},
		{"alt+shift+c", "windows", []string{"Alt", "Shift", "C"}},
		{"command+shift+k", "darwin", []string{"⇧", "⌘", "K"}},
		{"command+shift+k", "windows", []string{"Win", "Shift", "K"}},
		{"ctrl+alt+return", "darwin", []string{"⌃", "⌥", "↵"}},
		{"ctrl+f12", "windows", []string{"Ctrl", "F12"}},
		{"", "darwin", nil},
		{"option+", "darwin", nil},
	}
	for _, c := range cases {
		if got := shortcutKeyLabels(c.shortcut, c.goos); !slices.Equal(got, c.want) {
			t.Errorf("shortcutKeyLabels(%q, %s) = %q, want %q", c.shortcut, c.goos, got, c.want)
		}
	}
}

// TestOnboardingLeaderPicker: a keyboard without the default Leader key
// picks another in the guide. It is saved and handed to the host at once
// (OnConfigSaved: the config watcher skips Clavis's own writes), and the
// check waits for the new key. Caps Lock is not offered: the default keymap
// uses it.
func TestOnboardingLeaderPicker(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	saved := 0
	ctrl.OnConfigSaved = func() { saved++ }
	if err := ctrl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerLeaderKey})
	tt := onboardingTester(ctrl, "darwin")
	if !tt.HasText(i18n.T("onboarding.leader.seen")) {
		t.Fatal("precondition: the Leader check passed")
	}

	if err := tt.Click(i18n.T("settings.trigger.leader_key")); err != nil {
		t.Fatalf("open the Leader key picker: %v", err)
	}
	tt.Frame()
	if tt.HasText(i18n.T("settings.key.caps_lock")) {
		t.Error("the guide offers Caps Lock, which the default keymap uses")
	}
	if err := tt.Click(triggerKeyLabel("left_option", "darwin")); err != nil {
		t.Fatalf("pick left Option: %v", err)
	}
	tt.Frame()
	if got := ctrl.Config().Main.Trigger.LeaderKey; got != "left_option" {
		t.Fatalf("leader_key = %q after picking left Option", got)
	}
	if saved == 0 {
		t.Error("the new key was not handed to the host (OnConfigSaved)")
	}
	if !tt.HasText(i18n.T("onboarding.leader.waiting")) || !tt.HasText(triggerKeyLabel("left_option", "darwin")) {
		t.Error("after a new key the check should wait for it, naming it")
	}
}
