package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func TestTriggerKeyLabelPerPlatform(t *testing.T) {
	prev := i18n.Current()
	defer i18n.SetLanguage(prev)
	i18n.SetLanguage(i18n.English)

	cases := []struct{ token, goos, want string }{
		{"right_option", "darwin", "Right ⌥ Option"},
		{"left_command", "darwin", "Left ⌘ Command"},
		{"right_control", "darwin", "Right ⌃ Control"},
		{"right_option", "windows", "Right Alt"},
		{"right_command", "windows", "Right Win"},
		{"right_control", "linux", "Right Ctrl"},
		{"caps_lock", "windows", "⇪ Caps Lock"},
		{"hyper", "darwin", "hyper"},                 // unknown: shown as is
		{"middle_option", "darwin", "middle_option"}, // not a side
	}
	for _, tc := range cases {
		if got := triggerKeyLabel(tc.token, tc.goos); got != tc.want {
			t.Errorf("triggerKeyLabel(%q, %s) = %q, want %q", tc.token, tc.goos, got, tc.want)
		}
	}

	i18n.SetLanguage(i18n.Chinese)
	if got := triggerKeyLabel("right_option", "darwin"); got != "右 ⌥ Option" {
		t.Errorf("zh right_option = %q", got)
	}
	// Every preset gets a human label, and the values stay config tokens.
	for _, o := range triggerKeyOptions("darwin") {
		if o.label == o.value {
			t.Errorf("preset %q has no human label", o.value)
		}
	}
}

func TestFormatSettingNumber(t *testing.T) {
	cases := []struct {
		value, step float64
		unit, want  string
	}{
		{300, 10, "ms", "300 ms"},
		{8, 1, "", "8"},
		{0.2, 0.1, "", "0.2"},
		{0.7000000001, 0.1, "", "0.7"},
		{30, 5, "s", "30 s"},
		{0.25, 0.05, "", "0.25"},
	}
	for _, tc := range cases {
		if got := formatSettingNumber(tc.value, tc.step, tc.unit); got != tc.want {
			t.Errorf("formatSettingNumber(%v, %v, %q) = %q, want %q", tc.value, tc.step, tc.unit, got, tc.want)
		}
	}
	if got := settingCaption("leader_key", "right_option"); got != "leader_key: right_option" {
		t.Errorf("settingCaption = %q", got)
	}
	if got := settingCaption("show_key_hints", ""); got != "show_key_hints" {
		t.Errorf("settingCaption without value = %q", got)
	}
}

// TestSettingsTitleTogglesItsSwitch: a setting's name labels its control,
// so a click on the name flips the switch, as <label for> does.
func TestSettingsTitleTogglesItsSwitch(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()
	if !ctrl.Config().Main.Trigger.SnippetExpansionEnabled {
		t.Fatal("snippet expansion should default to on")
	}
	r, ok := tt.Find("Snippet Expansion")
	if !ok {
		t.Fatalf("no Snippet Expansion title: %v", tt.Texts())
	}
	tt.ClickAt(r.X+4, r.Y+r.H/2) // the title's first letters, far from the switch
	tt.Frame()
	tt.Frame()
	if ctrl.Config().Main.Trigger.SnippetExpansionEnabled {
		t.Fatal("clicking the title did not toggle snippet_expansion_enabled")
	}
	if !tt.HasText("snippet_expansion_enabled") {
		t.Fatal("the config key should stay visible as the caption")
	}
}

// TestSettingsListsKeepTheirLayout guards two layout bugs: list rows ran
// their texts together (";sigStandard email sign-off"), and Leader tree
// children were pushed half across the card by a growing spacer.
func TestSettingsListsKeepTheirLayout(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)

	ctrl.setSettingsPage("snippets")
	tt.Frame()
	trig, ok1 := tt.Find(";sig")
	desc, ok2 := tt.Find("Standard email sign-off")
	if !ok1 || !ok2 {
		t.Fatalf("snippet row texts missing: %v", tt.Texts())
	}
	if gap := desc.X - (trig.X + trig.W); gap < 4 {
		t.Fatalf("snippet trigger and description touch (gap %.1f)", gap)
	}

	ctrl.setSettingsPage("leader")
	tt.Frame()
	parent, ok1 := tt.Find("Window")
	child, ok2 := tt.Find("Left Half")
	if !ok1 || !ok2 {
		t.Fatalf("leader tree texts missing: %v", tt.Texts())
	}
	if indent := child.X - parent.X; indent < 8 || indent > 40 {
		t.Fatalf("child indent = %.1f DIPs, want one step (8..40)", indent)
	}
}

// TestAIProfileEditorShowsNumbers: temperature and timeout show their
// values next to the steppers, which used to show bare arrows.
func TestAIProfileEditorShowsNumbers(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	ctrl.setSettingsPage("ai")
	tt.Frame()
	p := ctrl.ActiveAIProfile()
	for _, want := range []string{
		formatSettingNumber(p.Temperature, 0.1, ""),
		formatSettingNumber(p.TimeoutSeconds, 5, "s"),
	} {
		if _, ok := tt.Find(want); !ok {
			t.Errorf("AI profile editor does not show %q: %v", want, tt.Texts())
		}
	}
}
