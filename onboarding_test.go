package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/uiapp"
)

// TestPlanOnboarding: the guide shows on a machine's first launch only. An
// install that already has data predates the guide and is recorded as done.
func TestPlanOnboarding(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

	fresh := t.TempDir()
	if !planOnboarding(fresh, false, now) {
		t.Fatal("fresh install: want the guide")
	}
	if !planOnboarding(fresh, false, now) {
		t.Fatal("deciding must not record it as done; finishing does")
	}
	if err := writeOnboardingMarker(fresh, "finished", now); err != nil {
		t.Fatal(err)
	}
	if planOnboarding(fresh, false, now) {
		t.Fatal("finished: no guide")
	}

	existing := t.TempDir()
	if planOnboarding(existing, true, now) {
		t.Fatal("existing install: no guide")
	}
	if !onboardingDone(existing) {
		t.Fatal("existing install: recorded as done so later launches need no database")
	}

	if planOnboarding("", false, now) {
		t.Fatal("no data directory: nowhere to remember it, no guide")
	}

	corrupt := t.TempDir()
	if err := os.WriteFile(filepath.Join(corrupt, onboardingMarkerFile), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if planOnboarding(corrupt, false, now) {
		t.Fatal("unreadable marker: no guide rather than one on every launch")
	}

	older := t.TempDir()
	if err := os.WriteFile(filepath.Join(older, onboardingMarkerFile), []byte(`{"version":0}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !planOnboarding(older, false, now) {
		t.Fatal("marker from an older guide: show the new one")
	}
}

// permBridge is a host bridge whose permissions the test sets and whose
// Start fails, like CGEventTapCreate, until startOK.
type permBridge struct {
	bridge.HostService
	mu      sync.Mutex
	perms   host.PermissionStatus
	startOK bool
	starts  int
}

func (b *permBridge) set(ax, im, startOK bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.perms = host.PermissionStatus{Accessibility: ax, InputMonitoring: im, Granted: ax && im}
	b.startOK = startOK
}

func (b *permBridge) Start(context.Context, func(host.TriggerEvent)) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.starts++
	if !b.startOK {
		return errors.New("CGEventTapCreate failed")
	}
	return nil
}

func (b *permBridge) Stop() error { return nil }

func (b *permBridge) Permissions(context.Context) (host.PermissionStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.perms, nil
}

func (b *permBridge) CurrentInputSource(context.Context) (host.InputSourceInfo, error) {
	return host.InputSourceInfo{}, nil
}

func (b *permBridge) TapRecoveryCount() int64 { return 0 }

func (b *permBridge) ActiveKeymapLayer() string { return "" }

func (b *permBridge) Capabilities() host.Capabilities {
	return host.Capabilities{Accessibility: true, InputMonitoring: true, GlobalHotkeys: true}
}

func (b *permBridge) startCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.starts
}

// TestPermissionsChangedStartsTheMonitor: a launch without permissions
// leaves the keyboard monitor stopped. When the permissions change it is
// started again; when it still fails with all of them granted, the guide
// offers a restart.
func TestPermissionsChangedStartsTheMonitor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	br := &permBridge{}
	ctrl := uiapp.NewAppController(uiapp.Options{
		ConfigStore:     cfgStore,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		SecretStore:     secrets.NewMemoryAPIKeyStore(),
		Bridge:          br,
		ClipboardWriter: func(string) {},
	})
	defer ctrl.Close()
	a := newDesktopApp()
	a.mu.Lock()
	a.ctx, a.cancel, a.controller, a.hostBridge = ctx, cancel, ctrl, br
	a.mu.Unlock()
	running := func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.bridgeRunning
	}
	guide := ui.NewTester(ctrl.OnboardingView, 600, 720)
	offersRestart := func() bool {
		guide.Frame()
		return guide.HasText(i18n.T("onboarding.perm.relaunch"))
	}

	// Launch without permissions.
	a.startBridgeAndFallbackShortcut(ctrl.Config().Main.Trigger)
	if running() || br.startCount() != 1 {
		t.Fatalf("launch without permissions: running=%v starts=%d", running(), br.startCount())
	}

	// Nothing granted yet: no new attempt.
	a.permissionsChanged(host.PermissionStatus{})
	if br.startCount() != 1 {
		t.Fatalf("no permission granted, yet %d starts", br.startCount())
	}

	// Both granted but the tap still fails: macOS wants a new process.
	br.set(true, true, false)
	a.permissionsChanged(host.PermissionStatus{Accessibility: true, InputMonitoring: true})
	if running() || br.startCount() != 2 {
		t.Fatalf("granted, tap fails: running=%v starts=%d", running(), br.startCount())
	}
	if !offersRestart() {
		t.Fatal("granted, tap fails: the guide should offer a restart")
	}

	// The tap works now.
	br.set(true, true, true)
	a.permissionsChanged(host.PermissionStatus{Accessibility: true, InputMonitoring: true})
	if !running() || br.startCount() != 3 {
		t.Fatalf("granted, tap works: running=%v starts=%d", running(), br.startCount())
	}
	if offersRestart() {
		t.Fatal("the monitor runs: no restart")
	}
	if !ctrl.Diagnostics().MonitorRunning {
		t.Fatal("the controller does not know the monitor runs")
	}

	// Running: a later change starts nothing.
	a.permissionsChanged(host.PermissionStatus{Accessibility: true, InputMonitoring: true})
	if br.startCount() != 3 {
		t.Fatalf("running, yet another start (%d)", br.startCount())
	}
}

// TestWatchPermissionsRetriesOnChange: the watcher polls while the monitor
// is stopped and acts once per change.
func TestWatchPermissionsRetriesOnChange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	br := &permBridge{}
	ctrl := uiapp.NewAppController(uiapp.Options{
		ConfigStore:     cfgStore,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		SecretStore:     secrets.NewMemoryAPIKeyStore(),
		Bridge:          br,
		ClipboardWriter: func(string) {},
	})
	defer ctrl.Close()
	a := newDesktopApp()
	a.mu.Lock()
	a.ctx, a.cancel, a.controller, a.hostBridge = ctx, cancel, ctrl, br
	a.mu.Unlock()
	defer func(d time.Duration) { permissionWatchInterval = d }(permissionWatchInterval)
	permissionWatchInterval = 40 * time.Millisecond

	done := make(chan struct{})
	go func() { a.watchPermissions(ctx); close(done) }()

	// Ungranted: the watcher looks but has nothing to start.
	time.Sleep(2*permissionWatchInterval + 200*time.Millisecond)
	if n := br.startCount(); n != 0 {
		t.Fatalf("nothing granted, %d starts", n)
	}
	br.set(true, true, true)
	deadline := time.Now().Add(3 * permissionWatchInterval)
	for time.Now().Before(deadline) && br.startCount() == 0 {
		time.Sleep(50 * time.Millisecond)
	}
	if br.startCount() != 1 {
		t.Fatalf("granted: want one start, got %d", br.startCount())
	}
	time.Sleep(2 * permissionWatchInterval)
	if br.startCount() != 1 {
		t.Fatalf("running and no guide: the watcher should rest, got %d starts", br.startCount())
	}
	cancel()
	<-done
}
