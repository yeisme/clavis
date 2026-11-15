package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"

	"github.com/yeisme/clavis/pkg/core/host"
)

// The host side of the first-run guide (uiapp.OnboardingView): its window,
// the per-machine record that it was done, and the permission watcher that
// starts the keyboard monitor once macOS allows it.

// onboardingMarkerFile, in the per-machine data directory, records that the
// guide was done. It is not in config.toml: that may sync between machines,
// and each machine asks for its own permissions.
const onboardingMarkerFile = "onboarding.json"

// onboardingVersion is bumped when the guide changes enough to show again.
const onboardingVersion = 1

type onboardingMarker struct {
	Version int       `json:"version"`
	DoneAt  time.Time `json:"doneAt"`
	// How is "finished" (Get Started or closed) or "existing-install".
	How string `json:"how"`
}

// onboardingDone reports whether the guide was done on this machine. An
// unreadable marker counts as done: better no guide than one on every launch.
func onboardingDone(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, onboardingMarkerFile))
	if err != nil {
		return false
	}
	var m onboardingMarker
	if json.Unmarshal(data, &m) != nil {
		return true
	}
	return m.Version >= onboardingVersion
}

// writeOnboardingMarker records that the guide was done, atomically.
func writeOnboardingMarker(dir, how string, now time.Time) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(onboardingMarker{Version: onboardingVersion, DoneAt: now.UTC(), How: how}, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, onboardingMarkerFile+".tmp")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, onboardingMarkerFile))
}

// planOnboarding decides at launch whether to show the guide. dir is the
// per-machine data directory ("" when unknown: nowhere to remember it, so no
// guide). hadData reports data left by an earlier launch: an install from
// before the guide, which is recorded as done instead of greeted.
func planOnboarding(dir string, hadData bool, now time.Time) bool {
	if dir == "" || onboardingDone(dir) {
		return false
	}
	if hadData {
		_ = writeOnboardingMarker(dir, "existing-install", now)
		return false
	}
	return true
}

// showOnboarding opens the guide.
func (a *desktopApp) showOnboarding() {
	a.mu.Lock()
	win := a.onboardingWin
	a.mu.Unlock()
	if win == nil {
		return
	}
	a.onboardingOpen.Store(true)
	go mygo.RunOnMain(func() {
		mygo.App.Show()
		win.Show()
		win.Focus()
		win.Invalidate()
	})
}

// finishOnboarding closes the guide and records it as done on this
// machine: Get Started and closing the window both finish it, and the tray
// opens it again.
func (a *desktopApp) finishOnboarding() {
	a.onboardingOpen.Store(false)
	a.mu.Lock()
	win := a.onboardingWin
	dir := a.dataDir
	a.mu.Unlock()
	if dir != "" {
		_ = writeOnboardingMarker(dir, "finished", time.Now())
	}
	if win != nil {
		win.Hide()
	}
}

// permissionWatchInterval is how often watchPermissions looks (a variable
// for tests).
var permissionWatchInterval = time.Second

// watchPermissions follows the permissions while they matter: while the
// keyboard monitor is not running (a launch without the permissions leaves
// it stopped, and nothing else retried before a config reload) and while the
// guide shows them. On a change it hands over to permissionsChanged.
func (a *desktopApp) watchPermissions(ctx context.Context) {
	t := time.NewTicker(permissionWatchInterval)
	defer t.Stop()
	var last host.PermissionStatus
	known := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.mu.Lock()
		br := a.hostBridge
		running := a.bridgeRunning
		a.mu.Unlock()
		if br == nil || running && !a.onboardingOpen.Load() {
			known = false
			continue
		}
		st, err := br.Permissions(ctx)
		if err != nil || known && st.Accessibility == last.Accessibility && st.InputMonitoring == last.InputMonitoring {
			continue
		}
		last, known = st, true
		a.permissionsChanged(st)
	}
}

// permissionsChanged starts the keyboard monitor if the permissions now
// allow it, tells the guide when only a restart would (macOS gives Input
// Monitoring to processes started after it), and refreshes the status the
// guide, Settings and the tray show.
func (a *desktopApp) permissionsChanged(st host.PermissionStatus) {
	a.mu.Lock()
	ctrl := a.controller
	running := a.bridgeRunning
	a.mu.Unlock()
	if ctrl == nil {
		return
	}
	if !running && (st.Accessibility || st.InputMonitoring) {
		a.startBridgeAndFallbackShortcut(ctrl.Config().Main.Trigger)
		a.mu.Lock()
		running = a.bridgeRunning
		a.mu.Unlock()
	}
	ctrl.SetRelaunchNeeded(!running && st.Accessibility && st.InputMonitoring)
	a.refreshPermissionsAsync()
}
