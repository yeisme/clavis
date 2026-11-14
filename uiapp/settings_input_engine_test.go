package uiapp

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
)

func TestFuzzyRuleSpellingsAndToggle(t *testing.T) {
	zzh := [2]string{"z", "zh"}
	for _, r := range []string{"z=zh", "zh=z", "z_zh", " Z-ZH "} {
		if !fuzzyRuleIs(r, zzh) {
			t.Errorf("fuzzyRuleIs(%q, z/zh) = false", r)
		}
	}
	for _, r := range []string{"z=z", "zh", "an=ang", "s=sh"} {
		if fuzzyRuleIs(r, zzh) {
			t.Errorf("fuzzyRuleIs(%q, z/zh) = true", r)
		}
	}

	// Toggling off drops every spelling of the pair and keeps the rest.
	got := toggleFuzzyRule([]string{"zh=z", "k=g", "z_zh"}, zzh)
	if !slices.Equal(got, []string{"k=g"}) {
		t.Fatalf("toggle off = %q", got)
	}
	// Toggling on appends the canonical spelling.
	got = toggleFuzzyRule(got, [2]string{"an", "ang"})
	if !slices.Equal(got, []string{"k=g", "an=ang"}) {
		t.Fatalf("toggle on = %q", got)
	}
}

// TestInputEngineCardPersistsAndGates: the engine's options are disabled
// while it is off; once on, a fuzzy chip writes fuzzy_pinyin to
// config.toml and a second click removes it again.
func TestInputEngineCardPersistsAndGates(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 1600)
	ctrl.setSettingsSection(settingsSectionIMEKeymap)
	tt.Frame()
	if ctrl.Config().Main.InputEngine.Enabled {
		t.Skip("the default config enables the input engine; the gate test needs it off")
	}

	chip := "z ⇄ zh"
	r, ok := tt.Find(chip)
	if !ok {
		t.Fatalf("no fuzzy chip %q: %v", chip, tt.Texts())
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if rules := ctrl.Config().Main.InputEngine.FuzzyPinyin; len(rules) != 0 {
		t.Fatalf("a disabled chip changed fuzzy_pinyin to %q", rules)
	}

	if err := tt.Click("Use Clavis Input Engine"); err != nil {
		t.Fatalf("click engine switch: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if !ctrl.Config().Main.InputEngine.Enabled {
		t.Fatal("the engine switch did not enable input_engine")
	}

	if err := tt.Click(chip); err != nil {
		t.Fatalf("click %s: %v", chip, err)
	}
	tt.Frame()
	if rules := ctrl.Config().Main.InputEngine.FuzzyPinyin; !slices.Equal(rules, []string{"z=zh"}) {
		t.Fatalf("fuzzy_pinyin = %q, want [z=zh]", rules)
	}
	if body := readConfigFile(t, ctrl, config.FileMain); !strings.Contains(body, `"z=zh"`) {
		t.Fatalf("config.toml does not persist fuzzy_pinyin:\n%s", body)
	}
	if !tt.HasText("fuzzy_pinyin: z=zh") {
		t.Fatalf("caption does not show the rule: %v", tt.Texts())
	}

	if err := tt.Click(chip); err != nil {
		t.Fatalf("click %s again: %v", chip, err)
	}
	tt.Frame()
	if rules := ctrl.Config().Main.InputEngine.FuzzyPinyin; len(rules) != 0 {
		t.Fatalf("second click left fuzzy_pinyin = %q", rules)
	}
}

// TestCandidateAppearanceFollowsSettings: the candidate window reads its
// layout from a package global, which a settings change must update even
// while the Capsule (which also publishes it) never renders.
func TestCandidateAppearanceFollowsSettings(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.CandidateBarOrientation = "vertical" }); err != nil {
		t.Fatalf("UpdateMainConfig: %v", err)
	}
	if got := CandidateAppearance().CandidateBarOrientation; got != "vertical" {
		t.Fatalf("candidate orientation = %q after a settings change, want vertical", got)
	}
	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.CandidateBarOrientation = "horizontal" }); err != nil {
		t.Fatalf("UpdateMainConfig: %v", err)
	}
	if got := CandidateAppearance().CandidateBarOrientation; got != "horizontal" {
		t.Fatalf("candidate orientation = %q, want horizontal", got)
	}
}
