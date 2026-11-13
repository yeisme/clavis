package uiapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// readConfigFile returns the on-disk contents of one TOML config file.
func readConfigFile(t *testing.T, ctrl *AppController, kind config.ConfigFileKind) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(ctrl.ConfigStore.Directory(), string(kind)))
	if err != nil {
		t.Fatalf("read %s: %v", kind, err)
	}
	return string(raw)
}

// openSettingsSection clicks a navigation rail entry and settles two frames so
// the section below it is laid out before the test interacts with it.
func openSettingsSection(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("click %q: %v", label, err)
	}
	tt.Frame()
	tt.Frame()
}

// TestSettingsCenter_TriggersAndAppearancePersist drives the interactive
// trigger and appearance editors and checks config.toml follows.
func TestSettingsCenter_TriggersAndAppearancePersist(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()

	// The trigger controls show the live values from config.toml.
	if !tt.HasText("right_option") || !tt.HasText("capsule_shortcut: option+space") {
		t.Fatalf("expected trigger values on General & Triggers, got %v", tt.Texts())
	}

	// Toggle show_preview_pane and check it reaches config.toml.
	if !ctrl.Config().Main.Appearance.ShowPreviewPane {
		t.Fatal("expected show_preview_pane to default to true")
	}
	if err := tt.Click("Show Preview Pane"); err != nil {
		t.Fatalf("click Show Preview Pane: %v", err)
	}
	tt.Frame()
	if ctrl.Config().Main.Appearance.ShowPreviewPane {
		t.Fatal("expected show_preview_pane=false after toggling the switch")
	}
	if body := readConfigFile(t, ctrl, config.FileMain); strings.Contains(body, "show_preview_pane = true") {
		t.Fatalf("config.toml still shows show_preview_pane = true:\n%s", body)
	}

	// Pick the dark theme pill.
	if err := tt.Click("dark"); err != nil {
		t.Fatalf("click dark theme pill: %v", err)
	}
	tt.Frame()
	if got := ctrl.Config().Main.Appearance.Theme; got != "dark" {
		t.Fatalf("appearance.theme = %q, want dark", got)
	}
	if body := readConfigFile(t, ctrl, config.FileMain); !strings.Contains(body, `theme = "dark"`) {
		t.Fatalf("config.toml does not persist theme = dark:\n%s", body)
	}

	// Record a new capsule shortcut chord: option+shift+k.
	if err := tt.Click("Record Shortcut"); err != nil {
		t.Fatalf("click Record Shortcut: %v", err)
	}
	tt.Frame()
	tt.Frame()
	tt.Key(ui.Alt|ui.Shift, ui.KeyK)
	tt.Frame()
	tt.Frame()
	if got := ctrl.Config().Main.Trigger.CapsuleShortcut; got != "option+shift+k" {
		t.Fatalf("capsule_shortcut = %q, want option+shift+k", got)
	}
	if !tt.HasText("capsule_shortcut: option+shift+k") {
		t.Fatalf("expected the recorded shortcut to show, got %v", tt.Texts())
	}
	if body := readConfigFile(t, ctrl, config.FileMain); !strings.Contains(body, `capsule_shortcut = "option+shift+k"`) {
		t.Fatalf("config.toml does not persist the recorded shortcut:\n%s", body)
	}

	// The recorder cancels on Escape without touching config.toml.
	if err := tt.Click("Record Shortcut"); err != nil {
		t.Fatalf("click Record Shortcut again: %v", err)
	}
	tt.Frame()
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if got := ctrl.Config().Main.Trigger.CapsuleShortcut; got != "option+shift+k" {
		t.Fatalf("capsule_shortcut = %q after cancelling, want option+shift+k", got)
	}
}

// TestSettingsCenter_KeymapRemapCRUD adds and deletes a custom keymap remap.
func TestSettingsCenter_KeymapRemapCRUD(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()
	openSettingsSection(t, tt, "IME & Keymap")

	if !tt.HasText("tap_hold_threshold_ms: 200 ms") {
		t.Fatalf("expected tap_hold_threshold_ms on IME & Keymap, got %v", tt.Texts())
	}

	for _, field := range []struct{ label, value string }{
		{"Remap From", "caps_lock"},
		{"Remap Tap", "escape"},
		{"Remap Hold", "hyper"},
		{"Remap Apps", "com.apple.Safari, com.apple.Terminal"},
	} {
		if err := tt.Click(field.label); err != nil {
			t.Fatalf("click %s: %v", field.label, err)
		}
		tt.Type(field.value)
		tt.Frame()
	}

	if err := tt.Click("Add Remap"); err != nil {
		t.Fatalf("click Add Remap: %v", err)
	}
	tt.Frame()

	remaps := ctrl.Config().Main.Keymap.Remaps
	if len(remaps) != 1 {
		t.Fatalf("keymap.remaps = %+v, want one remap", remaps)
	}
	if remaps[0].From != "caps_lock" || remaps[0].Tap != "escape" || remaps[0].Hold != "hyper" {
		t.Fatalf("remap = %+v, want caps_lock→escape/hyper", remaps[0])
	}
	if len(remaps[0].AppBundleIDs) != 2 {
		t.Fatalf("remap app_bundle_ids = %v, want 2", remaps[0].AppBundleIDs)
	}
	body := readConfigFile(t, ctrl, config.FileMain)
	if !strings.Contains(body, `from = "caps_lock"`) || !strings.Contains(body, "[[keymap.remaps]]") {
		t.Fatalf("config.toml does not persist [[keymap.remaps]]:\n%s", body)
	}

	// The remap row delete button comes before the IME rule delete buttons.
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("click Delete on remap: %v", err)
	}
	tt.Frame()
	if got := ctrl.Config().Main.Keymap.Remaps; len(got) != 0 {
		t.Fatalf("keymap.remaps = %+v, want none after delete", got)
	}
	if got := len(ctrl.Config().IMERules.Rules); got != 2 {
		t.Fatalf("deleting a remap changed the IME rules: %d rules", got)
	}
}

// TestSettingsCenter_LeaderTreeCRUD adds a root node and deletes it again.
func TestSettingsCenter_LeaderTreeCRUD(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()
	openSettingsSection(t, tt, "Leader Tree")

	before := len(ctrl.Config().Leader.Nodes)
	if !tt.HasText("Window") || !tt.HasText("AI Rewrite") {
		t.Fatalf("expected the default leader tree, got %v", tt.Texts())
	}

	if err := tt.Click("New Node Key"); err != nil {
		t.Fatalf("click New Node Key: %v", err)
	}
	tt.Type("z")
	tt.Frame()
	if err := tt.Click("New Node Label"); err != nil {
		t.Fatalf("click New Node Label: %v", err)
	}
	tt.Type("Zed")
	tt.Frame()
	if err := tt.Click("Add Root Node"); err != nil {
		t.Fatalf("click Add Root Node: %v", err)
	}
	tt.Frame()

	nodes := ctrl.Config().Leader.Nodes
	if len(nodes) != before+1 {
		t.Fatalf("leader nodes = %d, want %d", len(nodes), before+1)
	}
	if last := nodes[len(nodes)-1]; last.Key != "z" || last.Label != "Zed" {
		t.Fatalf("added node = %+v, want key z label Zed", last)
	}
	body := readConfigFile(t, ctrl, config.FileLeader)
	if !strings.Contains(body, `label = "Zed"`) || !strings.Contains(body, `key = "z"`) {
		t.Fatalf("leader.toml does not persist the new node:\n%s", body)
	}

	// A duplicate key is rejected with a validation banner.
	if err := tt.Click("New Node Key"); err != nil {
		t.Fatalf("click New Node Key: %v", err)
	}
	tt.Type("w")
	tt.Frame()
	if err := tt.Click("New Node Label"); err != nil {
		t.Fatalf("click New Node Label: %v", err)
	}
	tt.Type("Dupe")
	tt.Frame()
	if err := tt.Click("Add Root Node"); err != nil {
		t.Fatalf("click Add Root Node for duplicate: %v", err)
	}
	tt.Frame()
	if len(ctrl.Config().Leader.Nodes) != before+1 {
		t.Fatal("a duplicate leader key must not be persisted")
	}
	if !tt.HasText("Duplicate key") {
		t.Fatalf("expected a duplicate key validation banner, got %v", tt.Texts())
	}

	// Add a child node inside the "AI Rewrite" group.
	if err := tt.Click("AI Rewrite"); err != nil {
		t.Fatalf("select the AI Rewrite group: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Child Node Key"); err != nil {
		t.Fatalf("click Child Node Key: %v", err)
	}
	tt.Type("x")
	tt.Frame()
	if err := tt.Click("Child Node Label"); err != nil {
		t.Fatalf("click Child Node Label: %v", err)
	}
	tt.Type("Extra")
	tt.Frame()
	if err := tt.Click("Add Child Node"); err != nil {
		t.Fatalf("click Add Child Node: %v", err)
	}
	tt.Frame()

	group, ok := findLeaderNode(ctrl.Config().Leader.Nodes, []string{"a"})
	if !ok {
		t.Fatal("AI Rewrite group missing")
	}
	if len(group.Action.Children) != 3 {
		t.Fatalf("AI Rewrite children = %d, want 3", len(group.Action.Children))
	}
	if body := readConfigFile(t, ctrl, config.FileLeader); !strings.Contains(body, `label = "Extra"`) {
		t.Fatalf("leader.toml does not persist the child node:\n%s", body)
	}

	// A duplicate child key inside the same group is rejected.
	if err := tt.Click("Child Node Key"); err != nil {
		t.Fatalf("click Child Node Key: %v", err)
	}
	tt.Type("p")
	tt.Frame()
	if err := tt.Click("Child Node Label"); err != nil {
		t.Fatalf("click Child Node Label: %v", err)
	}
	tt.Type("Dupe Child")
	tt.Frame()
	if err := tt.Click("Add Child Node"); err != nil {
		t.Fatalf("click Add Child Node for duplicate: %v", err)
	}
	tt.Frame()
	group, ok = findLeaderNode(ctrl.Config().Leader.Nodes, []string{"a"})
	if !ok || len(group.Action.Children) != 3 {
		t.Fatal("a duplicate child key must not be persisted")
	}

	// Select the new node and delete it.
	if err := tt.Click("Zed"); err != nil {
		t.Fatalf("click the Zed node: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Delete Node"); err != nil {
		t.Fatalf("click Delete Node: %v", err)
	}
	tt.Frame()
	if got := len(ctrl.Config().Leader.Nodes); got != before {
		t.Fatalf("leader nodes = %d after delete, want %d", got, before)
	}
	if body := readConfigFile(t, ctrl, config.FileLeader); strings.Contains(body, `label = "Zed"`) {
		t.Fatalf("leader.toml still holds the deleted node:\n%s", body)
	}
}

// TestSettingsCenter_SnippetsCRUDAndPreview creates a snippet with a
// placeholder chip and checks the live preview and snippets.toml.
func TestSettingsCenter_SnippetsCRUDAndPreview(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()
	openSettingsSection(t, tt, "Snippets")

	before := len(ctrl.Config().Snippets.Items)
	if err := tt.Click("New Snippet"); err != nil {
		t.Fatalf("click New Snippet: %v", err)
	}
	tt.Frame()

	for _, field := range []struct{ label, value string }{
		{"Snippet ID", "greet"},
		{"Snippet Trigger", ";greet"},
		{"Snippet Description", "Greeting"},
	} {
		if err := tt.Click(field.label); err != nil {
			t.Fatalf("click %s: %v", field.label, err)
		}
		tt.Type(field.value)
		tt.Frame()
	}

	if err := tt.Click("Snippet Content"); err != nil {
		t.Fatalf("click Snippet Content: %v", err)
	}
	tt.Type("Hi ")
	tt.Frame()

	// The {{app}} chip appends to the content and the preview re-renders.
	if err := tt.Click("{{app}}"); err != nil {
		t.Fatalf("click the {{app}} placeholder chip: %v", err)
	}
	tt.Frame()

	wantPreview := engine.RenderSnippet("Hi {{app}}", engine.RenderOptions{AppName: "Preview"}).RenderedText
	if !tt.HasText(wantPreview) {
		t.Fatalf("expected live preview %q, got %v", wantPreview, tt.Texts())
	}

	if err := tt.Click("Save Snippet"); err != nil {
		t.Fatalf("click Save Snippet: %v", err)
	}
	tt.Frame()

	items := ctrl.Config().Snippets.Items
	if len(items) != before+1 {
		t.Fatalf("snippets = %d items, want %d", len(items), before+1)
	}
	var saved config.SnippetItem
	for _, item := range items {
		if item.ID == "greet" {
			saved = item
		}
	}
	if saved.Trigger != ";greet" || saved.Content != "Hi {{app}}" {
		t.Fatalf("saved snippet = %+v, want trigger ;greet content 'Hi {{app}}'", saved)
	}
	body := readConfigFile(t, ctrl, config.FileSnippets)
	if !strings.Contains(body, `trigger = ";greet"`) || !strings.Contains(body, `content = "Hi {{app}}"`) {
		t.Fatalf("snippets.toml does not persist the new snippet:\n%s", body)
	}
}

// TestSettingsCenter_GlossaryCRUD adds and deletes a glossary term.
func TestSettingsCenter_GlossaryCRUD(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()
	openSettingsSection(t, tt, "Glossary & Clipboard")

	before := len(ctrl.Config().Glossary.Terms)
	if err := tt.Click("New Term"); err != nil {
		t.Fatalf("click New Term: %v", err)
	}
	tt.Frame()

	for _, field := range []struct{ label, value string }{
		{"Glossary Term", "Kubernetes"},
		{"Glossary Aliases", "k8s, kube"},
		{"Glossary Category", "infra"},
		{"Glossary Hint", "Always capitalize the K."},
	} {
		if err := tt.Click(field.label); err != nil {
			t.Fatalf("click %s: %v", field.label, err)
		}
		tt.Type(field.value)
		tt.Frame()
	}

	if err := tt.Click("Save Term"); err != nil {
		t.Fatalf("click Save Term: %v", err)
	}
	tt.Frame()

	terms := ctrl.Config().Glossary.Terms
	if len(terms) != before+1 {
		t.Fatalf("glossary terms = %d, want %d", len(terms), before+1)
	}
	var saved config.GlossaryTerm
	for _, term := range terms {
		if term.Term == "Kubernetes" {
			saved = term
		}
	}
	if len(saved.Aliases) != 2 || saved.Category != "infra" {
		t.Fatalf("saved glossary term = %+v", saved)
	}
	body := readConfigFile(t, ctrl, config.FileGlossary)
	if !strings.Contains(body, `term = "Kubernetes"`) || !strings.Contains(body, `category = "infra"`) {
		t.Fatalf("glossary.toml does not persist the new term:\n%s", body)
	}

	if err := tt.Click("Delete Term"); err != nil {
		t.Fatalf("click Delete Term: %v", err)
	}
	tt.Frame()
	if got := len(ctrl.Config().Glossary.Terms); got != before {
		t.Fatalf("glossary terms = %d after delete, want %d", got, before)
	}
	if body := readConfigFile(t, ctrl, config.FileGlossary); strings.Contains(body, "Kubernetes") {
		t.Fatalf("glossary.toml still holds the deleted term:\n%s", body)
	}
}

// TestSettingsCenter_AIProfileEditAndAdd edits the active profile's model and
// appends a custom profile, both persisted to config.toml.
func TestSettingsCenter_AIProfileEditAndAdd(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()
	openSettingsSection(t, tt, "AI & Keychain")

	activeID := ctrl.ActiveAIProfile().ID
	if activeID != "deepseek" {
		t.Fatalf("ActiveAIProfile().ID = %q, want deepseek", activeID)
	}

	// Appending into the model field is enough to prove the edit persists.
	if err := tt.Click("Profile Model"); err != nil {
		t.Fatalf("click Profile Model: %v", err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("deepseek-chat-v2")
	tt.Frame()

	if got := ctrl.ActiveAIProfile().Model; got != "deepseek-chat-v2" {
		t.Fatalf("model = %q, want deepseek-chat-v2", got)
	}
	if body := readConfigFile(t, ctrl, config.FileMain); !strings.Contains(body, `model = "deepseek-chat-v2"`) {
		t.Fatalf("config.toml does not persist the edited model:\n%s", body)
	}

	// Add a custom profile through the editor form.
	before := len(ctrl.Config().Main.AI.Profiles)
	for _, field := range []struct{ label, value string }{
		{"New Profile ID", "my-profile"},
		{"New Profile Provider", "openai"},
		{"New Profile Base URL", "https://api.example.com/v1"},
		{"New Profile Model", "my-model"},
	} {
		if err := tt.Click(field.label); err != nil {
			t.Fatalf("click %s: %v", field.label, err)
		}
		tt.Type(field.value)
		tt.Frame()
	}

	if err := tt.Click("Add Profile"); err != nil {
		t.Fatalf("click Add Profile: %v", err)
	}
	tt.Frame()

	profiles := ctrl.Config().Main.AI.Profiles
	if len(profiles) != before+1 {
		t.Fatalf("AI profiles = %d, want %d", len(profiles), before+1)
	}
	added, ok := ctrl.Config().Main.AI.ProfileByID("my-profile")
	if !ok {
		t.Fatalf("custom profile my-profile missing from %+v", profiles)
	}
	if added.Model != "my-model" || added.Provider != "openai" {
		t.Fatalf("added profile = %+v", added)
	}
	if got := ctrl.Config().Main.AI.ActiveProfileID; got != "my-profile" {
		t.Fatalf("active_profile_id = %q, want my-profile", got)
	}
	body := readConfigFile(t, ctrl, config.FileMain)
	if !strings.Contains(body, `id = "my-profile"`) || !strings.Contains(body, `base_url = "https://api.example.com/v1"`) {
		t.Fatalf("config.toml does not persist the custom profile:\n%s", body)
	}
}
