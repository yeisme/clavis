package uiapp

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/host"
	coresync "github.com/yeisme/clavis/pkg/core/sync"
)

func TestFormatSyncRelativeTime(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "never", want: "从未同步"},
		{name: "recent", at: now.Add(-20 * time.Second), want: "刚刚"},
		{name: "minutes", at: now.Add(-5 * time.Minute), want: "5 分钟前"},
		{name: "hours", at: now.Add(-2 * time.Hour), want: "2 小时前"},
		{name: "days", at: now.Add(-3 * 24 * time.Hour), want: "3 天前"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatSyncRelativeTime(tc.at, now); got != tc.want {
				t.Fatalf("formatSyncRelativeTime(%v) = %q, want %q", tc.at, got, tc.want)
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
	defer SetSyncStatusReader(nil)
	ctrl := &AppController{}

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
}
