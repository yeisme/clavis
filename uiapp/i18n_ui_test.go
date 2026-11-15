package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// TestChineseLocalizationRendering switches the active language to Chinese
// and verifies the Capsule and the Settings center render localized text:
// omnibox tabs, search placeholder, leader footer, settings rail and pane
// header. English is the default everywhere else, which the rest of the
// suite (asserting English literals) pins down.
func TestChineseLocalizationRendering(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	i18n.SetLanguage(i18n.Chinese)

	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.Dispatch(engine.Event{
		Type:    engine.EventOpenOmnibox,
		Tab:     config.TabActions,
		Context: &engine.CapsuleContext{AppName: "Notes"},
	})

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()

	for _, want := range []string{
		"动作",         // Actions tab
		"剪贴板",        // Clipboard tab
		"代码片段",       // Snippets tab
		"术语表",        // Glossary tab
		"Omnibox 搜索", // search field accessibility label
		"运行动作",       // footer: the chosen row's primary action (↵)
		"数字直选",       // footer: ⌥1–9 quick select
		"左半屏",        // stock leader label shown in Chinese
	} {
		if !tt.HasText(want) {
			t.Fatalf("Chinese omnibox missing %q; texts: %q", want, tt.Texts())
		}
	}

	// Leader view localizes its footer and empty state.
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader, Context: &engine.CapsuleContext{AppName: "Notes"}})
	tt.Frame()
	for _, want := range []string{"触发", "搜索", "返回", "关闭", "窗口"} {
		if !tt.HasText(want) {
			t.Fatalf("Chinese leader footer/labels missing %q; texts: %q", want, tt.Texts())
		}
	}

	// Settings center rail and header localize.
	st := ui.NewTester(ctrl.SettingsView, 960, 640)
	st.Frame()
	for _, want := range []string{"配置中心", "通用与触发键", "输入法与键位", "权限与诊断"} {
		if !st.HasText(want) {
			t.Fatalf("Chinese settings missing %q; texts: %q", want, st.Texts())
		}
	}
}

// TestPreferredPillSizeForLabelCJK pins the CJK-aware width estimator:
// the same rune count renders roughly twice as wide in Chinese, while the
// compact short-badge path ("EN", "中") is untouched.
func TestPreferredPillSizeForLabelCJK(t *testing.T) {
	if w, h := PreferredPillSizeForLabel("中"); w != 68 || h != 30 {
		t.Fatalf("short CJK badge = (%d,%d), want (68,30)", w, h)
	}
	wCJK, hCJK := PreferredPillSizeForLabel("中中中中")
	wLatin, hLatin := PreferredPillSizeForLabel("aaaa")
	if wCJK != pillTextBasePad+8*pillRuneWidthEst || hCJK != 32 {
		t.Fatalf("CJK pill width = (%d,%d), want (%d,32)", wCJK, hCJK, pillTextBasePad+8*pillRuneWidthEst)
	}
	if wLatin != pillTextBasePad+4*pillRuneWidthEst || hLatin != 32 {
		t.Fatalf("Latin pill width = (%d,%d), want (%d,32)", wLatin, hLatin, pillTextBasePad+4*pillRuneWidthEst)
	}
	if wCJK <= wLatin {
		t.Fatalf("CJK width %d should exceed Latin %d", wCJK, wLatin)
	}
}

// TestLanguageSwitchThroughSettings drives the real save path: choosing a
// language in Settings persists it, switches the active language and fires
// the shell hook so tray/window labels can be rebuilt.
func TestLanguageSwitchThroughSettings(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	i18n.SetLanguage(i18n.English)

	ctrl, _, _, _ := newTestController(t, nil)
	fired := 0
	ctrl.OnLanguageChange = func() { fired++ }

	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.Language = "zh-CN" }); err != nil {
		t.Fatalf("UpdateMainConfig: %v", err)
	}
	if got := ctrl.Config().Main.Appearance.Language; got != "zh-CN" {
		t.Fatalf("persisted language = %q, want zh-CN", got)
	}
	if i18n.Current() != i18n.Chinese {
		t.Fatalf("active language = %q, want zh-CN", i18n.Current())
	}
	if fired != 1 {
		t.Fatalf("OnLanguageChange fired %d times, want 1", fired)
	}

	// Saving an unrelated setting must not fire the hook again.
	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.CornerRadius = 20 }); err != nil {
		t.Fatalf("UpdateMainConfig(corner): %v", err)
	}
	if fired != 1 {
		t.Fatalf("OnLanguageChange fired %d times after unrelated save, want 1", fired)
	}

	// Switching back to English fires it once more.
	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.Language = "en" }); err != nil {
		t.Fatalf("UpdateMainConfig(en): %v", err)
	}
	if i18n.Current() != i18n.English || fired != 2 {
		t.Fatalf("switch back: language=%q fired=%d, want en/2", i18n.Current(), fired)
	}
}
