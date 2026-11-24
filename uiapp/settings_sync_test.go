package uiapp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
	coresync "github.com/yeisme/clavis/pkg/core/sync"
)

func TestFormatSyncRelativeTime(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)

	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		at     time.Time
		wantZH string
		wantEN string
	}{
		{name: "never", wantZH: "从未同步", wantEN: "Never synced"},
		{name: "recent", at: now.Add(-20 * time.Second), wantZH: "刚刚", wantEN: "Just now"},
		{name: "minutes", at: now.Add(-5 * time.Minute), wantZH: "5 分钟前", wantEN: "5m ago"},
		{name: "hours", at: now.Add(-2 * time.Hour), wantZH: "2 小时前", wantEN: "2h ago"},
		{name: "days", at: now.Add(-3 * 24 * time.Hour), wantZH: "3 天前", wantEN: "3d ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/zh", func(t *testing.T) {
			i18n.SetLanguage(i18n.Chinese)
			if got := formatSyncRelativeTime(tc.at, now); got != tc.wantZH {
				t.Fatalf("formatSyncRelativeTime(%v) = %q, want %q", tc.at, got, tc.wantZH)
			}
		})
		t.Run(tc.name+"/en", func(t *testing.T) {
			i18n.SetLanguage(i18n.English)
			if got := formatSyncRelativeTime(tc.at, now); got != tc.wantEN {
				t.Fatalf("formatSyncRelativeTime(%v) = %q, want %q", tc.at, got, tc.wantEN)
			}
		})
	}
}

func TestSettingsAppearanceSectionIsDedicated(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.setSettingsSection(settingsSectionAppearance)
	tt := ui.NewTester(ctrl.SettingsView, 920, 640)
	tt.Frame()
	if !tt.HasText("[appearance]") {
		t.Fatalf("appearance section missing [appearance] card: %v", tt.Texts())
	}
	if tt.HasText("[trigger]") {
		t.Fatalf("appearance section should not duplicate [trigger] card: %v", tt.Texts())
	}
}

func TestSettingsAISectionWiresContinueEnabledSwitch(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.setSettingsSection(settingsSectionAI)
	tt := ui.NewTester(ctrl.SettingsView, 980, 760)
	tt.Frame()
	if !tt.HasText("Allow AI continuation") && !tt.HasText("允许续写") {
		t.Fatalf("AI settings section missing ContinueEnabled switch: %v", tt.Texts())
	}
}

func TestSyncStatusCardRendersThreeSemanticStates(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	defer SetSyncStatusReader(nil)

	t.Run("zh", func(t *testing.T) {
		i18n.SetLanguage(i18n.Chinese)
		ctrl := &AppController{}
		defer ctrl.Close()

		SetSyncStatusReader(nil)
		tt := ui.NewTester(func(c *ui.Context) { ctrl.renderSyncStatusCard(c, ui.LightTheme()) }, 420, 220)
		tt.Frame()
		if !tt.HasText("未配置同步") {
			t.Fatalf("unconfigured sync state missing: %v", tt.Texts())
		}

		SetSyncStatusReader(func(context.Context) (coresync.StatusSnapshot, host.OperationResult, error) {
			return coresync.StatusSnapshot{}, host.OperationResult{State: host.StateUnknown}, nil
		})
		tt = ui.NewTester(func(c *ui.Context) { ctrl.renderSyncStatusCard(c, ui.LightTheme()) }, 420, 220)
		tt.Frame()
		if !tt.HasText("同步本地状态暂不可用，当前未执行任何同步操作。") {
			t.Fatalf("unknown sync state missing: %v", tt.Texts())
		}

		SetSyncStatusReader(func(context.Context) (coresync.StatusSnapshot, host.OperationResult, error) {
			return coresync.StatusSnapshot{LoggedIn: true, Account: "alice@example.com", DeviceID: "mac-1", LocalVersion: 3, Devices: 2}, host.OperationResult{State: host.StateReady}, nil
		})
		tt = ui.NewTester(func(c *ui.Context) { ctrl.renderSyncStatusCard(c, ui.LightTheme()) }, 420, 320)
		tt.Frame()
		if !tt.HasText("已登录") || !tt.HasText("alice@example.com") {
			t.Fatalf("logged-in sync state missing: %v", tt.Texts())
		}
	})

	t.Run("en", func(t *testing.T) {
		i18n.SetLanguage(i18n.English)
		ctrl := &AppController{}
		defer ctrl.Close()

		assertNoHan := func(texts []string) {
			t.Helper()
			for _, s := range texts {
				for _, r := range s {
					if unicode.Is(unicode.Han, r) {
						t.Fatalf("English sync status card leaked Chinese text: %q (all=%v)", s, texts)
					}
				}
			}
		}

		SetSyncStatusReader(nil)
		tt := ui.NewTester(func(c *ui.Context) { ctrl.renderSyncStatusCard(c, ui.LightTheme()) }, 420, 220)
		tt.Frame()
		if !tt.HasText("Sync not configured") || !tt.HasText("Sync") {
			t.Fatalf("unconfigured English sync state missing: %v", tt.Texts())
		}
		assertNoHan(tt.Texts())

		SetSyncStatusReader(func(context.Context) (coresync.StatusSnapshot, host.OperationResult, error) {
			return coresync.StatusSnapshot{}, host.OperationResult{State: host.StateUnknown}, nil
		})
		tt = ui.NewTester(func(c *ui.Context) { ctrl.renderSyncStatusCard(c, ui.LightTheme()) }, 420, 220)
		tt.Frame()
		if !tt.HasText("Local sync status is temporarily unavailable; no sync actions were performed.") {
			t.Fatalf("unknown English sync state missing: %v", tt.Texts())
		}
		assertNoHan(tt.Texts())

		SetSyncStatusReader(func(context.Context) (coresync.StatusSnapshot, host.OperationResult, error) {
			return coresync.StatusSnapshot{LoggedIn: true, Account: "alice@example.com", DeviceID: "mac-1", LocalVersion: 3, Devices: 2}, host.OperationResult{State: host.StateReady}, nil
		})
		tt = ui.NewTester(func(c *ui.Context) { ctrl.renderSyncStatusCard(c, ui.LightTheme()) }, 420, 320)
		tt.Frame()
		if !tt.HasText("Signed in") || !tt.HasText("alice@example.com") || !tt.HasText("Refresh") {
			t.Fatalf("logged-in English sync state missing: %v", tt.Texts())
		}
		assertNoHan(tt.Texts())
	})
}

func TestSyncStatusCardCachesAcrossFramesAndNeverBlocksOnSlowReader(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.Chinese)

	defer SetSyncStatusReader(nil)
	ctrl := &AppController{}
	defer ctrl.Close()

	// 1. Counting fast reader: repeated renders within TTL call reader only once.
	var fastCalls atomic.Int32
	SetSyncStatusReader(func(context.Context) (coresync.StatusSnapshot, host.OperationResult, error) {
		n := fastCalls.Add(1)
		return coresync.StatusSnapshot{LoggedIn: true, Account: "cached@example.com", LocalVersion: int64(n)}, host.OperationResult{State: host.StateReady}, nil
	})
	tt := ui.NewTester(func(c *ui.Context) { ctrl.renderSyncStatusCard(c, ui.LightTheme()) }, 420, 320)
	for range 8 {
		tt.Frame()
	}
	if got := fastCalls.Load(); got != 1 {
		t.Fatalf("sync reader called %d times across 8 frames within TTL, want 1", got)
	}
	if !tt.HasText("cached@example.com") {
		t.Fatalf("cached sync status missing: %v", tt.Texts())
	}

	// Explicit Refresh button triggers a new async read.
	if err := tt.Click("刷新"); err != nil {
		t.Fatalf("click sync refresh: %v", err)
	}
	waitUntil(t, time.Second, func() bool { return fastCalls.Load() >= 2 }, "sync refresh button triggers reload")

	// 2. Slow reader: render must stay bounded and never stall on blocked I/O.
	gate := make(chan struct{})
	SetSyncStatusReader(func(context.Context) (coresync.StatusSnapshot, host.OperationResult, error) {
		<-gate
		return coresync.StatusSnapshot{LoggedIn: true, Account: "async@example.com"}, host.OperationResult{State: host.StateReady}, nil
	})
	ttSlow := ui.NewTester(func(c *ui.Context) { ctrl.renderSyncStatusCard(c, ui.LightTheme()) }, 420, 320)
	start1 := time.Now()
	ttSlow.Frame()
	if elapsed := time.Since(start1); elapsed > 120*time.Millisecond {
		t.Fatalf("cold slow sync reader stalled frame 1 for %v", elapsed)
	}
	start2 := time.Now()
	ttSlow.Frame()
	if elapsed := time.Since(start2); elapsed > 30*time.Millisecond {
		t.Fatalf("in-flight slow sync reader stalled frame 2 for %v", elapsed)
	}
	close(gate)
	waitUntil(t, time.Second, func() bool {
		entry := ctrl.syncStatusCache()
		entry.mu.Lock()
		defer entry.mu.Unlock()
		return !entry.inFlight && entry.snapshot.Account == "async@example.com"
	}, "slow sync reader completes asynchronously")
	ttSlow.Frame()
	if !ttSlow.HasText("async@example.com") {
		t.Fatalf("async sync result not rendered after completion: %v", ttSlow.Texts())
	}
}

func TestSettingsSyncCardShowsAllowlistAndConflictCount(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	defer SetSyncStatusReader(nil)

	ctrl := &AppController{}
	defer ctrl.Close()

	ctrl.SetSyncStatusReader(func(context.Context) (coresync.StatusSnapshot, host.OperationResult, error) {
		return coresync.StatusSnapshot{
			LoggedIn:       true,
			Account:        "alice…",
			DeviceID:       "mac-1…",
			LocalVersion:   9,
			Conflicts:      2,
			Devices:        3,
			AllowedClasses: coresync.Phase1SyncPlan().AllowedClasses(),
		}, host.OperationResult{State: host.StateReady}, nil
	})

	i18n.SetLanguage(i18n.Chinese)
	ttZH := ui.NewTester(func(c *ui.Context) { ctrl.renderSyncStatusCard(c, ui.LightTheme()) }, 420, 420)
	ttZH.Frame()
	if !ttZH.HasText("同步范围") || !ttZH.HasText("input_method, learning_data") || !ttZH.HasText("冲突") || !ttZH.HasText("2") {
		t.Fatalf("zh sync card missing allowlist or conflict count: %v", ttZH.Texts())
	}

	i18n.SetLanguage(i18n.English)
	ttEN := ui.NewTester(func(c *ui.Context) { ctrl.renderSyncStatusCard(c, ui.LightTheme()) }, 420, 420)
	ttEN.Frame()
	if !ttEN.HasText("Allowlist") || !ttEN.HasText("input_method, learning_data") || !ttEN.HasText("Conflicts") || !ttEN.HasText("2") {
		t.Fatalf("en sync card missing allowlist or conflict count: %v", ttEN.Texts())
	}
}
