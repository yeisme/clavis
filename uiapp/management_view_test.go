package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func TestManagementHubSettingsButtonsOpenExactTargetSections(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)
	ctrl, _, _, _ := newTestController(t, nil)

	openCalls := 0
	ctrl.mu.Lock()
	ctrl.OnOpenSettings = func(SettingsTab) {
		openCalls++
	}
	ctrl.mu.Unlock()

	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	tt := ui.NewTester(ctrl.CapsuleView, 960, 760)

	cases := []struct {
		name        string
		buttonLabel string
		wantPage    string
	}{
		{"Sync Details -> About", "Sync Details", "about"},
		{"Policy -> Clipboard", "Policy", "clipboard"},
		{"Glossary -> Glossary", "Glossary", "glossary"},
		{"Security -> Security", "Security", "security"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Start on triggers so we prove the button actually navigates away from triggers.
			ctrl.setSettingsPage("triggers")
			ctrl.SetHubSurface(HubSurfaceManage)
			beforeCalls := openCalls
			tt.Frame()
			tt.Frame()

			var target ui.AccessInfo
			found := false
			for _, info := range tt.AccessAll() {
				if info.Role == ui.RoleButton && info.Name == tc.buttonLabel {
					if !found || info.Bounds.Y > target.Bounds.Y {
						target = info
						found = true
					}
				}
			}
			if found {
				tt.ClickAt(target.Bounds.X+target.Bounds.W/2, target.Bounds.Y+target.Bounds.H/2)
			} else if err := tt.Click(tc.buttonLabel); err != nil {
				t.Fatalf("click %q in Management Hub: %v (texts=%v)", tc.buttonLabel, err, tt.Texts())
			}
			tt.Frame()

			if openCalls != beforeCalls+1 {
				t.Fatalf("clicking %q invoked OnOpenSettings %d times, want +1", tc.buttonLabel, openCalls-beforeCalls)
			}
			if got := settingsPageKey(ctrl); got != tc.wantPage {
				t.Fatalf("clicking %q opened settings page %q, want %q", tc.buttonLabel, got, tc.wantPage)
			}
		})
	}
}
