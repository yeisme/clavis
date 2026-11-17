package uiapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
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

	// Pick the dark theme segment: humanized "Dark", stored as "dark".
	if err := tt.Click("Dark"); err != nil {
		t.Fatalf("click Dark theme segment: %v", err)
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

func TestSettingsCenter_ClipboardMediaControlsPersist(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()
	openSettingsSection(t, tt, "Glossary & Clipboard")

	if !tt.HasText("Capture Images & Files") {
		t.Fatalf("expected media capture setting, got %v", tt.Texts())
	}
	if err := tt.Click("Capture Images & Files"); err != nil {
		t.Fatalf("click media capture: %v", err)
	}
	tt.Frame()
	if ctrl.Config().Main.Clipboard.MediaCapture {
		t.Fatal("expected media capture to be disabled")
	}
	body := readConfigFile(t, ctrl, config.FileMain)
	if !strings.Contains(body, "media_capture = false") {
		t.Fatalf("config.toml does not persist media_capture=false:\n%s", body)
	}

	if err := tt.Click("Media Storage Quota"); err != nil {
		t.Fatalf("click media quota stepper: %v", err)
	}
	tt.Frame()
	if got := ctrl.Config().Main.Clipboard.MediaQuotaMB; got < 0 {
		t.Fatalf("media quota = %d, want non-negative", got)
	}
}

// ---- Workflows editor ----

// workflowByID returns the stored workflow with id.
func workflowByID(ctrl *AppController, id string) (config.Workflow, bool) {
	for _, wf := range ctrl.Config().Workflows.Workflows {
		if wf.ID == id {
			return wf, true
		}
	}
	return config.Workflow{}, false
}

// seedWorkflowScripts registers enabled script components so the workflow
// action registry exposes script.<id> actions to the editor's save path.
func seedWorkflowScripts(t *testing.T, ctrl *AppController, ids ...string) {
	t.Helper()
	components := ctrl.Config().Components
	scripts := make([]config.ScriptComponentSpec, len(components.Scripts), len(components.Scripts)+len(ids))
	copy(scripts, components.Scripts)
	for _, id := range ids {
		scripts = append(scripts, config.ScriptComponentSpec{ID: id, Title: id, Command: "printf", Enabled: true})
	}
	components.Scripts = scripts
	if err := ctrl.ConfigStore.SaveComponents(components); err != nil {
		t.Fatalf("seed workflow scripts: %v", err)
	}
}

// focusByTab presses Tab until the element labelled label holds the focus, so
// a test can drive a section by keyboard alone.
func focusByTab(t *testing.T, tt *ui.Tester, label string, limit int) bool {
	t.Helper()
	for i := 0; i < limit; i++ {
		if tt.Focused(label) {
			return true
		}
		tt.Key(0, ui.KeyTab)
		tt.Frame()
	}
	return tt.Focused(label)
}

// TestSettingsCenter_WorkflowsNavigation opens the Workflows section from the
// rail and checks the empty state, then selects the same section by keyboard.
func TestSettingsCenter_WorkflowsNavigation(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()

	openSettingsSection(t, tt, i18n.T("settings.section.workflows"))
	if got := settingsSectionIndex(ctrl); got != settingsSectionWorkflows {
		t.Fatalf("rail selected section %d, want %d", got, settingsSectionWorkflows)
	}
	if !tt.HasText(i18n.T("settings.section.workflows.hint")) {
		t.Fatalf("workflow pane header missing its hint, got %v", tt.Texts())
	}
	if !tt.HasText(i18n.T("settings.workflows.empty")) {
		t.Fatalf("expected the empty workflow state, got %v", tt.Texts())
	}
	if !tt.HasText(i18n.T("settings.workflows.new_workflow")) {
		t.Fatalf("expected the New Workflow action, got %v", tt.Texts())
	}

	// Keyboard navigation: Tab reaches the rail entry and Enter selects it.
	ctrl.setSettingsSection(settingsSectionConfigs)
	tt = ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()
	if !focusByTab(t, tt, i18n.T("settings.section.workflows"), 40) {
		t.Fatal("Tab never reached the Workflows rail entry")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Frame()
	if got := settingsSectionIndex(ctrl); got != settingsSectionWorkflows {
		t.Fatalf("Enter on the Workflows rail entry selected %d, want %d", got, settingsSectionWorkflows)
	}
	// Tab on to New Workflow and start a draft with Enter.
	if !focusByTab(t, tt, i18n.T("settings.workflows.new_workflow"), 40) {
		t.Fatalf("Tab never reached New Workflow, got focus on %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Frame()
	if !tt.HasText(i18n.T("settings.workflows.enabled")) {
		t.Fatalf("Enter on New Workflow did not open a draft, got %v", tt.Texts())
	}
}

// TestSettingsCenter_WorkflowSaveRoundTrip creates a workflow through the
// editor and checks both the live config and the reloaded workflows.toml.
func TestSettingsCenter_WorkflowSaveRoundTrip(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 1200, 1600)
	tt.Frame()
	openSettingsSection(t, tt, i18n.T("settings.section.workflows"))

	if err := tt.Click(i18n.T("settings.workflows.new_workflow")); err != nil {
		t.Fatalf("click New Workflow: %v", err)
	}
	tt.Frame()
	for _, field := range []struct{ label, value string }{
		{i18n.T("settings.workflows.id"), "summarize-selection"},
		{i18n.T("settings.workflows.name"), "Summarize Selection"},
		{i18n.T("settings.workflows.description"), "Rewrites the selection with the rewrite action."},
	} {
		if err := tt.Click(field.label); err != nil {
			t.Fatalf("click %s: %v", field.label, err)
		}
		tt.Type(field.value)
		tt.Frame()
	}
	// The workflow output offers Insert, which the decoder stores verbatim.
	if err := tt.Click(i18n.T("settings.workflows.out.insert")); err != nil {
		t.Fatalf("click Insert output: %v", err)
	}
	tt.Frame()
	if err := tt.Click(i18n.T("settings.workflows.save")); err != nil {
		t.Fatalf("click Save Workflow: %v", err)
	}
	tt.Frame()

	wf, ok := workflowByID(ctrl, "summarize-selection")
	if !ok {
		t.Fatalf("workflow missing after save: %+v", ctrl.Config().Workflows.Workflows)
	}
	if wf.Name != "Summarize Selection" || wf.Description != "Rewrites the selection with the rewrite action." {
		t.Fatalf("saved workflow = %+v", wf)
	}
	if !wf.Enabled || wf.Source != config.WorkflowSourceClipboard || wf.Output != config.WorkflowOutputInsert ||
		wf.Failure != config.WorkflowFailureStop || wf.Execution != config.WorkflowExecutionSequential {
		t.Fatalf("saved modes = enabled=%v source=%q output=%q failure=%q execution=%q",
			wf.Enabled, wf.Source, wf.Output, wf.Failure, wf.Execution)
	}
	body := readConfigFile(t, ctrl, config.FileWorkflows)
	for _, want := range []string{`id = "summarize-selection"`, `name = "Summarize Selection"`, `output = "insert"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("workflows.toml missing %s:\n%s", want, body)
		}
	}

	reloaded, err := config.DecodeWorkflowsConfig(body)
	if err != nil {
		t.Fatalf("decode workflows.toml: %v", err)
	}
	if len(reloaded.Workflows) != 1 || reloaded.Workflows[0].ID != wf.ID ||
		reloaded.Workflows[0].Output != wf.Output || reloaded.Workflows[0].Name != wf.Name {
		t.Fatalf("reloaded workflows = %+v, want %+v", reloaded.Workflows, wf)
	}
}

// TestSettingsCenter_WorkflowInvalidConfigRejected saves an invalid draft and
// checks the error surfaces while the last valid config and file stay intact.
func TestSettingsCenter_WorkflowInvalidConfigRejected(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	if err := ctrl.ConfigStore.SaveWorkflows(config.WorkflowsConfig{Workflows: []config.Workflow{{
		ID:        "alpha",
		Name:      "Alpha",
		Enabled:   true,
		Source:    config.WorkflowSourceClipboard,
		Output:    config.WorkflowOutputReplace,
		Failure:   config.WorkflowFailureStop,
		Execution: config.WorkflowExecutionSequential,
	}}}); err != nil {
		t.Fatalf("seed workflows: %v", err)
	}
	beforeBody := readConfigFile(t, ctrl, config.FileWorkflows)

	tt := ui.NewTester(ctrl.SettingsView, 1200, 1600)
	tt.Frame()
	openSettingsSection(t, tt, i18n.T("settings.section.workflows"))
	if !tt.HasText("Alpha") {
		t.Fatalf("stored workflow not shown, got %v", tt.Texts())
	}

	// A duplicate ID is rejected by ConfigStore.SaveWorkflows.
	if err := tt.Click(i18n.T("settings.workflows.new_workflow")); err != nil {
		t.Fatalf("click New Workflow: %v", err)
	}
	tt.Frame()
	if err := tt.Click(i18n.T("settings.workflows.id")); err != nil {
		t.Fatalf("click Workflow ID: %v", err)
	}
	tt.Type("alpha")
	tt.Frame()
	if err := tt.Click(i18n.T("settings.workflows.save")); err != nil {
		t.Fatalf("click Save Workflow: %v", err)
	}
	tt.Frame()

	if !tt.HasText("duplicate workflow id") {
		t.Fatalf("expected the duplicate id error to show, got %v", tt.Texts())
	}
	if got := len(ctrl.Config().Workflows.Workflows); got != 1 {
		t.Fatalf("workflows = %d after a rejected save, want 1", got)
	}
	if wf, ok := workflowByID(ctrl, "alpha"); !ok || wf.Name != "Alpha" {
		t.Fatalf("last valid config overwritten: %+v", ctrl.Config().Workflows.Workflows)
	}
	if after := readConfigFile(t, ctrl, config.FileWorkflows); after != beforeBody {
		t.Fatalf("workflows.toml changed after a rejected save:\n%s", after)
	}

	// An identifier that breaks the ID grammar is rejected as well.
	if err := tt.Click(i18n.T("settings.workflows.id")); err != nil {
		t.Fatalf("click Workflow ID for invalid id: %v", err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("bad id!")
	tt.Frame()
	if err := tt.Click(i18n.T("settings.workflows.save")); err != nil {
		t.Fatalf("click Save Workflow for invalid id: %v", err)
	}
	tt.Frame()
	if !tt.HasText("workflow id must be a non-empty identifier") {
		t.Fatalf("expected the invalid id error to show, got %v", tt.Texts())
	}
	if got := len(ctrl.Config().Workflows.Workflows); got != 1 {
		t.Fatalf("workflows = %d after a rejected invalid id, want 1", got)
	}
}

// TestSettingsCenter_WorkflowStepEditing drafts steps, edits their fields, and
// persists them with Save Workflow.
func TestSettingsCenter_WorkflowStepEditing(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	seedWorkflowScripts(t, ctrl, "echo", "upper")
	tt := ui.NewTester(ctrl.SettingsView, 1200, 1800)
	tt.Frame()
	openSettingsSection(t, tt, i18n.T("settings.section.workflows"))

	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatalf("click %q: %v", label, err)
		}
		tt.Frame()
	}
	typeInto := func(label, value string) {
		t.Helper()
		click(label)
		tt.Type(value)
		tt.Frame()
	}

	click(i18n.T("settings.workflows.new_workflow"))
	typeInto(i18n.T("settings.workflows.id"), "flow")
	click(i18n.T("settings.workflows.save"))
	wf, ok := workflowByID(ctrl, "flow")
	if !ok || len(wf.Steps) != 0 {
		t.Fatalf("saved workflow = %+v, want flow without steps", wf)
	}

	// Add a step and edit its fields; the draft stays out of the store.
	click(i18n.T("settings.workflows.add_step"))
	if !tt.HasText(i18n.T("settings.workflows.step_editor")) {
		t.Fatalf("Add Step did not open the step editor, got %v", tt.Texts())
	}
	typeInto(i18n.T("settings.workflows.step_id"), "step-a")
	typeInto(i18n.T("settings.workflows.step_name"), "Echo")
	typeInto(i18n.T("settings.workflows.step_action"), "script.echo")
	typeInto(i18n.T("settings.workflows.step_input"), "text")
	if wf, _ := workflowByID(ctrl, "flow"); len(wf.Steps) != 0 {
		t.Fatalf("step draft reached the store before Save Workflow: %+v", wf.Steps)
	}
	click(i18n.T("settings.workflows.save"))

	wf, _ = workflowByID(ctrl, "flow")
	if len(wf.Steps) != 1 {
		t.Fatalf("steps = %d after save, want 1", len(wf.Steps))
	}
	step := wf.Steps[0]
	if step.ID != "step-a" || step.Name != "Echo" || step.Action != "script.echo" || step.Input != "text" {
		t.Fatalf("saved step = %+v", step)
	}
	if !step.Enabled || step.Source != config.WorkflowSourceClipboard || step.Output != config.WorkflowOutputReplace ||
		step.Failure != config.WorkflowFailureStop || step.Execution != config.WorkflowExecutionSequential {
		t.Fatalf("saved step modes = %+v", step)
	}
	body := readConfigFile(t, ctrl, config.FileWorkflows)
	for _, want := range []string{`id = "step-a"`, `action = "script.echo"`, `input = "text"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("workflows.toml missing %s:\n%s", want, body)
		}
	}

	// Add a second step, depend on the first, then disable it and save.
	click(i18n.T("settings.workflows.add_step"))
	typeInto(i18n.T("settings.workflows.step_id"), "step-b")
	typeInto(i18n.T("settings.workflows.step_action"), "script.upper")
	typeInto(i18n.T("settings.workflows.step_depends_on"), "step-a")
	click(i18n.T("settings.workflows.step_enabled"))
	click(i18n.T("settings.workflows.save"))

	wf, _ = workflowByID(ctrl, "flow")
	if len(wf.Steps) != 2 {
		t.Fatalf("steps = %d after adding step-b, want 2", len(wf.Steps))
	}
	second := wf.Steps[1]
	if second.ID != "step-b" || second.Enabled {
		t.Fatalf("step-b = %+v, want disabled step-b", second)
	}
	if len(second.DependsOn) != 1 || second.DependsOn[0] != "step-a" {
		t.Fatalf("step-b depends_on = %v, want [step-a]", second.DependsOn)
	}
	body = readConfigFile(t, ctrl, config.FileWorkflows)
	if !strings.Contains(body, `depends_on = ["step-a"]`) {
		t.Fatalf("workflows.toml does not persist depends_on:\n%s", body)
	}
	if !strings.Contains(body, "enabled = false") {
		t.Fatalf("workflows.toml does not persist the disabled step:\n%s", body)
	}

	// Delete the selected step and save again.
	click(i18n.T("settings.workflows.step_delete"))
	if wf, _ := workflowByID(ctrl, "flow"); len(wf.Steps) != 2 {
		t.Fatalf("Delete Step reached the store before Save Workflow: %+v", wf.Steps)
	}
	click(i18n.T("settings.workflows.save"))

	wf, _ = workflowByID(ctrl, "flow")
	if len(wf.Steps) != 1 || wf.Steps[0].ID != "step-a" {
		t.Fatalf("steps after delete = %+v, want only step-a", wf.Steps)
	}
	if body := readConfigFile(t, ctrl, config.FileWorkflows); strings.Contains(body, "step-b") {
		t.Fatalf("workflows.toml still holds the deleted step:\n%s", body)
	}
}

// TestSettingsCenter_WorkflowPreviewOmitsSensitiveValues checks the preview
// never shows a step command or prompt, which may carry secrets.
func TestSettingsCenter_WorkflowPreviewOmitsSensitiveValues(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	if err := ctrl.ConfigStore.SaveWorkflows(config.WorkflowsConfig{Workflows: []config.Workflow{{
		ID:        "guarded",
		Name:      "Guarded Workflow",
		Enabled:   true,
		Source:    config.WorkflowSourceSelection,
		Output:    config.WorkflowOutputReplace,
		Failure:   config.WorkflowFailureStop,
		Execution: config.WorkflowExecutionSequential,
		Steps: []config.WorkflowStep{{
			ID:        "run",
			Action:    "script.run",
			Command:   "curl --token super-secret-token",
			Prompt:    "super-secret-prompt",
			Enabled:   true,
			Source:    config.WorkflowSourceSelection,
			Output:    config.WorkflowOutputReplace,
			Failure:   config.WorkflowFailureStop,
			Execution: config.WorkflowExecutionSequential,
		}},
	}}}); err != nil {
		t.Fatalf("seed workflows: %v", err)
	}

	tt := ui.NewTester(ctrl.SettingsView, 1200, 1800)
	tt.Frame()
	openSettingsSection(t, tt, i18n.T("settings.section.workflows"))
	tt.Frame()

	if !tt.HasText(i18n.Tf("settings.workflows.preview_summary", 1, 1)) {
		t.Fatalf("expected the non-sensitive preview summary, got %v", tt.Texts())
	}
	for _, secret := range []string{"super-secret-token", "super-secret-prompt"} {
		if tt.HasText(secret) {
			t.Fatalf("preview leaked %q, texts: %v", secret, tt.Texts())
		}
	}
}

// TestSettingsCenter_WorkflowStepActionValidation checks that Save Workflow
// refuses an empty or unregistered step action, and that the message is inline
// while the stored config and workflows.toml stay untouched.
func TestSettingsCenter_WorkflowStepActionValidation(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	seedWorkflowScripts(t, ctrl, "echo")
	tt := ui.NewTester(ctrl.SettingsView, 1200, 1800)
	tt.Frame()
	openSettingsSection(t, tt, i18n.T("settings.section.workflows"))

	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatalf("click %q: %v", label, err)
		}
		tt.Frame()
	}
	click(i18n.T("settings.workflows.new_workflow"))
	click(i18n.T("settings.workflows.id"))
	tt.Type("flow")
	tt.Frame()
	click(i18n.T("settings.workflows.save"))
	if wf, ok := workflowByID(ctrl, "flow"); !ok {
		t.Fatalf("workflow missing after the initial save: %+v", ctrl.Config().Workflows.Workflows)
	} else if len(wf.Steps) != 0 {
		t.Fatalf("unexpected steps after the initial save: %+v", wf.Steps)
	}
	beforeBody := readConfigFile(t, ctrl, config.FileWorkflows)

	click(i18n.T("settings.workflows.add_step"))
	click(i18n.T("settings.workflows.step_id"))
	tt.Type("run")
	tt.Frame()
	click(i18n.T("settings.workflows.save"))
	stepTitle := "run"
	if want := i18n.Tf("settings.workflows.err_action", stepTitle); !tt.HasText(want) {
		t.Fatalf("expected %q for an empty action; got %v", want, tt.Texts())
	}
	if wf, _ := workflowByID(ctrl, "flow"); len(wf.Steps) != 0 {
		t.Fatalf("empty action reached the store: %+v", wf.Steps)
	}
	if body := readConfigFile(t, ctrl, config.FileWorkflows); body != beforeBody {
		t.Fatalf("workflows.toml changed after an empty action:\n%s", body)
	}

	click(i18n.T("settings.workflows.step_action"))
	tt.Type("script.missing")
	tt.Frame()
	click(i18n.T("settings.workflows.save"))
	if want := i18n.Tf("settings.workflows.unknown_action", stepTitle, "script.missing"); !tt.HasText(want) {
		t.Fatalf("expected %q for an unknown action; got %v", want, tt.Texts())
	}
	if wf, _ := workflowByID(ctrl, "flow"); len(wf.Steps) != 0 {
		t.Fatalf("unknown action reached the store: %+v", wf.Steps)
	}
	if body := readConfigFile(t, ctrl, config.FileWorkflows); body != beforeBody {
		t.Fatalf("workflows.toml changed after an unknown action:\n%s", body)
	}

	// A registered action passes and is persisted with the workflow.
	click(i18n.T("settings.workflows.step_action"))
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("script.echo")
	tt.Frame()
	click(i18n.T("settings.workflows.save"))
	wf, _ := workflowByID(ctrl, "flow")
	if len(wf.Steps) != 1 || wf.Steps[0].Action != "script.echo" {
		t.Fatalf("steps after a valid save = %+v, want script.echo", wf.Steps)
	}
	if body := readConfigFile(t, ctrl, config.FileWorkflows); !strings.Contains(body, `action = "script.echo"`) {
		t.Fatalf("workflows.toml missing the registered action:\n%s", body)
	}
}

// TestSettingsCenter_WorkflowDeleteConfirmation checks the destructive action
// asks first: Cancel leaves the stored workflow and file intact, and the
// explicit confirm deletes it.
func TestSettingsCenter_WorkflowDeleteConfirmation(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	if err := ctrl.ConfigStore.SaveWorkflows(config.WorkflowsConfig{Workflows: []config.Workflow{{
		ID: "alpha", Name: "Alpha", Enabled: true,
	}}}); err != nil {
		t.Fatalf("seed workflows: %v", err)
	}
	beforeBody := readConfigFile(t, ctrl, config.FileWorkflows)

	tt := ui.NewTester(ctrl.SettingsView, 1200, 1600)
	tt.Frame()
	openSettingsSection(t, tt, i18n.T("settings.section.workflows"))
	if err := tt.Click(i18n.T("settings.workflows.delete")); err != nil {
		t.Fatalf("click Delete Workflow: %v", err)
	}
	tt.Frame()
	confirmText := i18n.Tf("settings.workflows.delete_confirm", "alpha")
	if !tt.HasText(confirmText) {
		t.Fatalf("expected delete confirmation %q; got %v", confirmText, tt.Texts())
	}
	if _, ok := workflowByID(ctrl, "alpha"); !ok {
		t.Fatal("workflow deleted before the confirmation")
	}
	if body := readConfigFile(t, ctrl, config.FileWorkflows); body != beforeBody {
		t.Fatalf("workflows.toml changed before the confirmation:\n%s", body)
	}

	if err := tt.Click(i18n.T("settings.workflows.delete_cancel")); err != nil {
		t.Fatalf("click Cancel: %v", err)
	}
	tt.Frame()
	if tt.HasText(confirmText) {
		t.Fatalf("confirmation still shown after Cancel: %v", tt.Texts())
	}
	if _, ok := workflowByID(ctrl, "alpha"); !ok {
		t.Fatal("Cancel deleted the workflow")
	}

	if err := tt.Click(i18n.T("settings.workflows.delete")); err != nil {
		t.Fatalf("click Delete Workflow again: %v", err)
	}
	tt.Frame()
	if err := tt.Click(i18n.T("settings.workflows.confirm_delete")); err != nil {
		t.Fatalf("click Confirm Delete: %v", err)
	}
	tt.Frame()
	if _, ok := workflowByID(ctrl, "alpha"); ok {
		t.Fatalf("workflow still stored after confirm: %+v", ctrl.Config().Workflows.Workflows)
	}
	if body := readConfigFile(t, ctrl, config.FileWorkflows); strings.Contains(body, "alpha") {
		t.Fatalf("workflows.toml still holds the confirmed delete:\n%s", body)
	}
}

// TestSettingsCenter_WorkflowKeyboardRowActivation extends the keyboard flow to
// the master list: Tab reaches a workflow row and Enter loads it into the
// editor, the same activation a pointer click performs.
func TestSettingsCenter_WorkflowKeyboardRowActivation(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	if err := ctrl.ConfigStore.SaveWorkflows(config.WorkflowsConfig{Workflows: []config.Workflow{
		{ID: "alpha", Name: "First Flow", Enabled: true},
		{ID: "beta", Name: "Second Flow", Enabled: true},
	}}); err != nil {
		t.Fatalf("seed workflows: %v", err)
	}
	tt := ui.NewTester(ctrl.SettingsView, 1200, 1600)
	tt.Frame()
	openSettingsSection(t, tt, i18n.T("settings.section.workflows"))

	if !focusByTab(t, tt, "Second Flow", 60) {
		t.Fatalf("Tab never reached the beta workflow row, texts: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Frame()
	if got := seGet(ctrl.settingsEditor(), func(s *settingsEditorState) string { return s.workflowSelected }); got != "beta" {
		t.Fatalf("Enter on the beta row selected %q, want beta", got)
	}
}
