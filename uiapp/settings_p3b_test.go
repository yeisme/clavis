package uiapp

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func TestIA09_SettingsNoticeScopedPerPage(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 1100, 900)
	tt.Frame()

	openSettingsSection(t, tt, i18n.T("omnibox.tab.snippets"))
	if err := tt.Click(i18n.T("settings.snippets.new_snippet")); err != nil {
		t.Fatalf("click New Snippet: %v", err)
	}
	tt.Frame()
	for _, fld := range []struct{ label, val string }{
		{i18n.T("settings.snippets.id"), "scoped"},
		{i18n.T("settings.snippets.trigger"), ";scoped"},
	} {
		if err := tt.Click(fld.label); err != nil {
			t.Fatalf("click %s: %v", fld.label, err)
		}
		tt.Type(fld.val)
		tt.Frame()
	}
	if err := tt.Click(i18n.T("settings.snippets.save")); err != nil {
		t.Fatalf("click Save Snippet: %v", err)
	}
	tt.Frame()

	wantNotice := i18n.Tf("settings.status.snippets_saved", len(ctrl.Config().Snippets.Items))
	if !tt.HasText(wantNotice) {
		t.Fatalf("expected snippets header notice %q on snippets page; got %v", wantNotice, tt.Texts())
	}

	openSettingsSection(t, tt, i18n.T("settings.section.permissions"))
	if tt.HasText(wantNotice) {
		t.Fatalf("snippets notice %q leaked onto permissions page", wantNotice)
	}

	openSettingsSection(t, tt, i18n.T("omnibox.tab.snippets"))
	if tt.HasText(wantNotice) {
		t.Fatalf("snippets notice %q reappeared after leaving page; expected cleared", wantNotice)
	}
}

func TestIA11_ImmediateControlShowsInlineSaved1500msNoHeaderPill(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	for _, tc := range []struct {
		name string
		anim string
	}{
		{"full", "full"},
		{"reduced", "reduced"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl, _, _, _ := newTestController(t, nil)
			if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) {
				m.Appearance.Animations = tc.anim
			}); err != nil {
				t.Fatalf("UpdateMainConfig: %v", err)
			}
			tt := ui.NewTester(ctrl.SettingsView, 1100, 900)
			tt.Frame()

			if err := tt.Click(i18n.T("settings.trigger.snippet_expansion")); err != nil {
				t.Fatalf("click snippet expansion switch: %v", err)
			}
			tt.Frame()

			if !tt.HasText(i18n.T("settings.field.saved")) {
				t.Fatalf("expected inline %q feedback on field row; texts=%v", i18n.T("settings.field.saved"), tt.Texts())
			}
			if tt.HasText(i18n.T("settings.status.saved")) {
				t.Fatalf("header must not show %q pill for immediate control; texts=%v", i18n.T("settings.status.saved"), tt.Texts())
			}

			tt.Advance(1000 * time.Millisecond)
			if !tt.HasText(i18n.T("settings.field.saved")) {
				t.Fatalf("expected inline %q feedback to stay visible at 1.0s", i18n.T("settings.field.saved"))
			}

			tt.Advance(600 * time.Millisecond)
			if tt.HasText(i18n.T("settings.field.saved")) {
				t.Fatalf("expected inline %q feedback to clear after 1.5s; texts=%v", i18n.T("settings.field.saved"), tt.Texts())
			}
		})
	}
}

func TestIA12_IA13_WriteFailureBannerRetryRevealAndRailRedDot(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	ctrl, _, _, _ := newTestController(t, nil)
	revealed := ""
	ctrl.RevealPath = func(path string) error {
		revealed = path
		return nil
	}
	tt := ui.NewTester(ctrl.SettingsView, 1100, 900)
	tt.Frame()

	beforeExp := ctrl.Config().Main.Trigger.SnippetExpansionEnabled
	ctrl.ConfigStore.SetWriteErrorForTest(config.FileMain, errors.New("disk full"))

	if err := tt.Click(i18n.T("settings.trigger.snippet_expansion")); err != nil {
		t.Fatalf("click snippet expansion: %v", err)
	}
	tt.Frame()

	if ctrl.Config().Main.Trigger.SnippetExpansionEnabled != beforeExp {
		t.Fatal("failed save must leave control and store at old value")
	}
	joined := strings.Join(tt.Texts(), "\n")
	wantFullPath := filepath.Join(ctrl.ConfigStore.Directory(), string(config.FileMain))
	if !strings.Contains(joined, i18n.T("settings.trigger.snippet_expansion")) ||
		!strings.Contains(joined, "disk full") ||
		!strings.Contains(joined, wantFullPath) {
		t.Fatalf("failure banner missing field/reason/fullpath:\n%s", joined)
	}
	if !tt.HasText(i18n.T("settings.failure.retry")) || !tt.HasText(i18n.T("settings.failure.reveal_finder")) {
		t.Fatalf("failure banner missing Retry / Reveal buttons: %v", tt.Texts())
	}

	if err := tt.Click(i18n.T("settings.failure.reveal_finder")); err != nil {
		t.Fatalf("click Reveal in Finder: %v", err)
	}
	tt.Frame()
	if revealed != wantFullPath {
		t.Fatalf("RevealPath = %q, want %q", revealed, wantFullPath)
	}

	// Clear write error and save an unrelated field: failure banner for first field stays intact (IA-12).
	ctrl.ConfigStore.SetWriteErrorForTest(config.FileMain, nil)
	if err := tt.Click(i18n.T("settings.trigger.snippet_backspace_undo")); err != nil {
		t.Fatalf("click snippet backspace undo: %v", err)
	}
	tt.Frame()
	if !tt.HasText(i18n.T("settings.failure.retry")) {
		t.Fatal("unrelated field save must not clear previous field's failure banner")
	}

	// Switch to Permissions: triggers rail entry retains its red error dot (IA-13).
	openSettingsSection(t, tt, i18n.T("settings.section.permissions"))
	if _, ok := tt.Find("settings-rail-error-dot:triggers"); !ok {
		t.Fatal("expected triggers rail row to keep red error dot across page switches")
	}

	// Switch back and Retry: persists first change and atomically clears banner and rail red dot.
	openSettingsSection(t, tt, i18n.T("settings.section.general"))
	if err := tt.Click(i18n.T("settings.failure.retry")); err != nil {
		t.Fatalf("click Retry: %v", err)
	}
	tt.Frame()
	if ctrl.Config().Main.Trigger.SnippetExpansionEnabled == beforeExp {
		t.Fatal("Retry did not persist snippet_expansion_enabled toggle")
	}
	if tt.HasText(i18n.T("settings.failure.retry")) {
		t.Fatalf("failure banner remained after successful Retry: %v", tt.Texts())
	}
	if _, ok := tt.Find("settings-rail-error-dot:triggers"); ok {
		t.Fatal("triggers rail error dot remained after successful Retry")
	}
}

func TestIA14_IA15_IA16_DirtyDotsUnsavedSwitchAndDocumentEdited(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	ctrl, _, _, _ := newTestController(t, nil)
	if err := ctrl.SaveSnippetsConfig(config.SnippetsConfig{
		Items: []config.SnippetItem{
			{ID: "alpha", Trigger: ";alpha", Description: "First", Content: "alpha-body", WordBoundary: true, AppBundleIDs: []string{}, ProcessNames: []string{}},
			{ID: "beta", Trigger: ";beta", Description: "Second", Content: "beta-body", WordBoundary: true, AppBundleIDs: []string{}, ProcessNames: []string{}},
		},
	}); err != nil {
		t.Fatalf("seed snippets: %v", err)
	}
	var docEdited bool
	ctrl.OnDocumentEdited = func(edited bool) {
		docEdited = edited
	}

	tt := ui.NewTester(ctrl.SettingsView, 1100, 900)
	tt.Frame()
	openSettingsSection(t, tt, i18n.T("omnibox.tab.snippets"))

	// Clean state: no dirty dots, OnDocumentEdited false.
	if docEdited {
		t.Fatal("expected OnDocumentEdited=false when snippets draft is clean")
	}
	if _, ok := tt.Find("settings-rail-dirty-dot:snippets"); ok {
		t.Fatal("unexpected rail dirty dot before edits")
	}
	if _, ok := tt.Find("snippet-editor-dirty-dot"); ok {
		t.Fatal("unexpected editor dirty dot before edits")
	}

	// Modify alpha draft.
	if err := tt.Click(i18n.T("settings.snippets.description")); err != nil {
		t.Fatalf("click description: %v", err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("First Edited")
	tt.Frame()

	if !docEdited {
		t.Fatal("expected OnDocumentEdited=true after draft edit")
	}
	if _, ok := tt.Find("settings-rail-dirty-dot:snippets"); !ok {
		t.Fatal("expected rail dirty dot on snippets after draft edit")
	}
	if _, ok := tt.Find("snippet-editor-dirty-dot"); !ok {
		t.Fatal("expected editor card dirty dot on snippets after draft edit")
	}

	// Page switch and return preserves draft and rail dot (IA-16).
	openSettingsSection(t, tt, i18n.T("settings.section.permissions"))
	if _, ok := tt.Find("settings-rail-dirty-dot:snippets"); !ok {
		t.Fatal("expected snippets rail dirty dot to stay visible while viewing another page")
	}
	if !docEdited {
		t.Fatal("expected OnDocumentEdited=true across page switches")
	}
	openSettingsSection(t, tt, i18n.T("omnibox.tab.snippets"))
	if got := seGet(ctrl.settingsEditor(), func(s *settingsEditorState) string { return s.snippetDraft.Description }); got != "First Edited" {
		t.Fatalf("draft lost after page switch: got %q, want %q", got, "First Edited")
	}

	// Simulated close/reopen keeps draft and dirty dot.
	ttReopen := ui.NewTester(ctrl.SettingsView, 1100, 900)
	ttReopen.Frame()
	if _, ok := ttReopen.Find("settings-rail-dirty-dot:snippets"); !ok {
		t.Fatal("expected snippets rail dirty dot after reopening settings view")
	}

	// Click beta while alpha is dirty -> stays on alpha, shows 3-button banner (IA-15).
	if err := tt.Click(";beta"); err != nil {
		t.Fatalf("click ;beta: %v", err)
	}
	tt.Frame()
	if got := seGet(ctrl.settingsEditor(), func(s *settingsEditorState) string { return s.snippetSelected }); got != "alpha" {
		t.Fatalf("selection changed despite dirty draft: got %q, want alpha", got)
	}
	for _, btn := range []string{i18n.T("settings.draft.save"), i18n.T("settings.draft.discard_short"), i18n.T("settings.draft.keep_editing")} {
		if !tt.HasText(btn) {
			t.Fatalf("unsaved switch banner missing %q button; texts=%v", btn, tt.Texts())
		}
	}

	// Escape == Keep Editing.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText(i18n.T("settings.draft.discard_short")) {
		t.Fatal("Esc did not dismiss unsaved switch banner")
	}
	if got := seGet(ctrl.settingsEditor(), func(s *settingsEditorState) string { return s.snippetSelected }); got != "alpha" {
		t.Fatalf("Esc switched selection away from alpha: %q", got)
	}

	// Keep Editing button keeps alpha and draft.
	if err := tt.Click(";beta"); err != nil {
		t.Fatalf("click ;beta again: %v", err)
	}
	tt.Frame()
	if err := tt.Click(i18n.T("settings.draft.keep_editing")); err != nil {
		t.Fatalf("click Keep Editing: %v", err)
	}
	tt.Frame()
	if got := seGet(ctrl.settingsEditor(), func(s *settingsEditorState) string { return s.snippetSelected }); got != "alpha" {
		t.Fatalf("Keep Editing switched selection: %q", got)
	}

	// Discard button drops changes and switches to beta.
	if err := tt.Click(";beta"); err != nil {
		t.Fatalf("click ;beta for Discard: %v", err)
	}
	tt.Frame()
	if err := tt.Click(i18n.T("settings.draft.discard_short")); err != nil {
		t.Fatalf("click Discard: %v", err)
	}
	tt.Frame()
	if got := seGet(ctrl.settingsEditor(), func(s *settingsEditorState) string { return s.snippetSelected }); got != "beta" {
		t.Fatalf("Discard did not switch to beta: got %q", got)
	}
	if docEdited {
		t.Fatal("expected OnDocumentEdited=false after Discard")
	}

	// Save button persists beta edits and switches to alpha.
	if err := tt.Click(i18n.T("settings.snippets.description")); err != nil {
		t.Fatalf("click description for beta: %v", err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Second Saved")
	tt.Frame()
	if err := tt.Click(";alpha"); err != nil {
		t.Fatalf("click ;alpha with dirty beta: %v", err)
	}
	tt.Frame()
	if err := tt.Click(i18n.T("settings.draft.save")); err != nil {
		t.Fatalf("click Save in unsaved switch banner: %v", err)
	}
	tt.Frame()
	if got := seGet(ctrl.settingsEditor(), func(s *settingsEditorState) string { return s.snippetSelected }); got != "alpha" {
		t.Fatalf("Save did not switch to alpha: got %q", got)
	}
	if ctrl.Config().Snippets.Items[1].Description != "Second Saved" {
		t.Fatalf("beta description = %q, want Second Saved", ctrl.Config().Snippets.Items[1].Description)
	}
	if docEdited {
		t.Fatal("expected OnDocumentEdited=false after Save & switch")
	}
}

func TestIA17_IA18_EntityDeleteConfirmVsInlineImmediateDelete(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	ctrl, _, _, _ := newTestController(t, nil)
	if err := ctrl.SaveSnippetsConfig(config.SnippetsConfig{
		Items: []config.SnippetItem{{ID: "s1", Trigger: ";s1", Content: "hi", WordBoundary: true}},
	}); err != nil {
		t.Fatalf("seed snippets: %v", err)
	}
	if err := ctrl.SaveGlossaryConfig(config.GlossaryConfig{
		Terms: []config.GlossaryTerm{{Term: "Kube", Aliases: []string{"k8s"}, Category: "infra"}},
	}); err != nil {
		t.Fatalf("seed glossary: %v", err)
	}
	if err := ctrl.ConfigStore.SaveWorkflows(config.WorkflowsConfig{
		Workflows: []config.Workflow{{ID: "wf1", Name: "WF1", Enabled: true}},
	}); err != nil {
		t.Fatalf("seed workflows: %v", err)
	}
	if err := ctrl.SaveAPIKey("sk-ia17-test"); err != nil {
		t.Fatalf("seed API key: %v", err)
	}
	if err := ctrl.AddIMERule(config.IMERule{BundleID: "com.apple.Safari", AppName: "Safari", PreferredMode: config.IMEModeASCII}); err != nil {
		t.Fatalf("seed IME rule: %v", err)
	}
	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) {
		m.Keymap.Remaps = []config.KeyRemapRule{{From: "caps_lock", Tap: "escape"}}
		m.Clipboard.ExcludedBundleIDs = []string{"com.example.secret"}
	}); err != nil {
		t.Fatalf("seed remap/exclusion: %v", err)
	}

	tt := ui.NewTester(ctrl.SettingsView, 1200, 1400)
	tt.Frame()

	assertConfirmFlow := func(pageLabel, deleteBtn, confirmWellKey, cancelBtn, confirmBtn, wantSubstr string, countFn func() int) {
		t.Helper()
		openSettingsSection(t, tt, pageLabel)
		before := countFn()
		if err := tt.Click(deleteBtn); err != nil {
			t.Fatalf("%s: click %q: %v", pageLabel, deleteBtn, err)
		}
		tt.Frame()
		if countFn() != before {
			t.Fatalf("%s: entity deleted before confirm", pageLabel)
		}
		if _, ok := tt.Find(confirmWellKey); !ok {
			t.Fatalf("%s: expected kitInsetWell %q", pageLabel, confirmWellKey)
		}
		if wantSubstr != "" && !strings.Contains(strings.Join(tt.Texts(), "\n"), wantSubstr) {
			t.Fatalf("%s: missing confirmation prompt substring %q in %v", pageLabel, wantSubstr, tt.Texts())
		}
		if !tt.Focused(cancelBtn) {
			t.Fatalf("%s: expected %q button to have initial focus", pageLabel, cancelBtn)
		}
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		if _, ok := tt.Find(confirmWellKey); ok {
			t.Fatalf("%s: Esc did not cancel delete confirm", pageLabel)
		}
		if countFn() != before {
			t.Fatalf("%s: Esc deleted entity", pageLabel)
		}
		if err := tt.Click(deleteBtn); err != nil {
			t.Fatalf("%s: click %q second time: %v", pageLabel, deleteBtn, err)
		}
		tt.Frame()
		if err := tt.Click(confirmBtn); err != nil {
			t.Fatalf("%s: click confirm %q: %v", pageLabel, confirmBtn, err)
		}
		tt.Frame()
		if countFn() != before-1 {
			t.Fatalf("%s: entity count after confirm = %d, want %d", pageLabel, countFn(), before-1)
		}
	}

	// 1. Leader node with children shows child count notice
	openSettingsSection(t, tt, i18n.T("settings.section.leader"))
	if err := tt.Click("AI Rewrite"); err != nil {
		t.Fatalf("click AI Rewrite leader node: %v", err)
	}
	tt.Frame()
	assertConfirmFlow(i18n.T("settings.section.leader"), i18n.T("settings.leader.delete_node"),
		"leader-delete-confirm", i18n.T("settings.delete.cancel"), i18n.T("settings.delete.confirm_action"),
		"child node(s)", func() int { return len(ctrl.Config().Leader.Nodes) })

	// 2. Snippet
	assertConfirmFlow(i18n.T("omnibox.tab.snippets"), i18n.T("settings.snippets.delete"),
		"snippet-delete-confirm", i18n.T("settings.delete.cancel"), i18n.T("settings.delete.confirm_action"),
		"snippets.toml", func() int { return len(ctrl.Config().Snippets.Items) })

	// 3. Glossary
	assertConfirmFlow(i18n.T("settings.section.glossary"), i18n.T("settings.glossary.delete"),
		"glossary-delete-confirm", i18n.T("settings.delete.cancel"), i18n.T("settings.delete.confirm_action"),
		"glossary.toml", func() int { return len(ctrl.Config().Glossary.Terms) })

	// 4. Workflow (uses kitInsetWell workflow-delete-confirm)
	assertConfirmFlow(i18n.T("settings.section.workflows"), i18n.T("settings.workflows.delete"),
		"workflow-delete-confirm", i18n.T("settings.workflows.delete_cancel"), i18n.T("settings.workflows.confirm_delete"),
		"wf1", func() int { return len(ctrl.Config().Workflows.Workflows) })

	// 5. AI Profile
	assertConfirmFlow(i18n.T("settings.section.ai"), i18n.T("settings.ai.delete_profile"),
		"ai-profile-delete-confirm", i18n.T("settings.delete.cancel"), i18n.T("settings.delete.confirm_action"),
		"config.toml", func() int { return len(ctrl.Config().Main.AI.Profiles) })

	// 6. API Key (save for the currently active profile after profile switch above)
	if err := ctrl.SaveAPIKey("sk-ia17-test"); err != nil {
		t.Fatalf("seed API key for active profile: %v", err)
	}
	tt.Frame()
	assertConfirmFlow(i18n.T("settings.section.ai"), i18n.T("settings.ai.clear_key"),
		"api-key-delete-confirm", i18n.T("settings.delete.cancel"), i18n.T("settings.delete.confirm_action"),
		"Keychain", func() int {
			if ctrl.APIKeyStatus() == secrets.KeySourceNone {
				return 0
			}
			return 1
		})

	// IA-18: Inline value deletes stay immediate without confirmation.
	openSettingsSection(t, tt, i18n.T("settings.section.ime"))
	if err := tt.Click(i18n.T("common.delete")); err != nil {
		t.Fatalf("click inline delete remap: %v", err)
	}
	tt.Frame()
	if got := len(ctrl.Config().Main.Keymap.Remaps); got != 0 {
		t.Fatalf("remap count = %d after 1-click delete, want 0", got)
	}
	beforeIME := len(ctrl.Config().IMERules.Rules)
	if err := tt.Click(i18n.T("common.delete")); err != nil {
		t.Fatalf("click inline delete IME rule: %v", err)
	}
	tt.Frame()
	if got := len(ctrl.Config().IMERules.Rules); got != beforeIME-1 {
		t.Fatalf("IME rules count = %d after 1-click delete, want %d", got, beforeIME-1)
	}

	openSettingsSection(t, tt, i18n.T("settings.section.clipboard"))
	if err := tt.Click(i18n.T("settings.clipboard.remove")); err != nil {
		t.Fatalf("click immediate Remove exclusion: %v", err)
	}
	tt.Frame()
	if got := len(ctrl.Config().Main.Clipboard.ExcludedBundleIDs); got != 0 {
		t.Fatalf("excluded apps = %d after 1-click delete, want 0", got)
	}
}

func TestIA23_EmptyStatesIconBodyAndPrimaryCTA(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	ctrl, _, _, _ := newTestController(t, nil)
	_ = ctrl.SaveLeaderConfig(config.LeaderConfig{Nodes: []config.LeaderNode{}})
	_ = ctrl.SaveSnippetsConfig(config.SnippetsConfig{Items: []config.SnippetItem{}})
	_ = ctrl.SaveGlossaryConfig(config.GlossaryConfig{Terms: []config.GlossaryTerm{}})
	_ = ctrl.ConfigStore.SaveWorkflows(config.WorkflowsConfig{Workflows: []config.Workflow{}})
	_ = ctrl.SaveIMERulesConfig(config.IMERulesConfig{Rules: []config.IMERule{}})

	tt := ui.NewTester(ctrl.SettingsView, 1100, 1200)
	tt.Frame()

	for _, tc := range []struct {
		page    string
		title   string
		body    string
		cta     string
		checkFn func() bool
	}{
		{
			page:  i18n.T("omnibox.tab.snippets"),
			title: i18n.T("omnibox.empty.snippets"),
			body:  i18n.T("settings.empty.snippets.body"),
			cta:   i18n.T("settings.snippets.new_snippet"),
			checkFn: func() bool {
				return seGet(ctrl.settingsEditor(), func(s *settingsEditorState) bool { return s.snippetIsNew })
			},
		},
		{
			page:  i18n.T("settings.section.glossary"),
			title: i18n.T("omnibox.empty.glossary"),
			body:  i18n.T("settings.empty.glossary.body"),
			cta:   i18n.T("settings.glossary.new_term"),
			checkFn: func() bool {
				return seGet(ctrl.settingsEditor(), func(s *settingsEditorState) bool { return s.glossaryIsNew })
			},
		},
		{
			page:  i18n.T("settings.section.workflows"),
			title: i18n.T("settings.workflows.empty"),
			body:  i18n.T("settings.empty.workflows.body"),
			cta:   i18n.T("settings.workflows.new_workflow"),
			checkFn: func() bool {
				return seGet(ctrl.settingsEditor(), func(s *settingsEditorState) bool { return s.workflowIsNew })
			},
		},
		{
			page:  i18n.T("settings.section.leader"),
			title: i18n.T("settings.empty.leader.title"),
			body:  i18n.T("settings.empty.leader.body"),
			cta:   i18n.T("settings.empty.leader.cta"),
			checkFn: func() bool {
				return tt.Focused(i18n.T("settings.leader.new_key"))
			},
		},
		{
			page:  i18n.T("settings.section.ime"),
			title: i18n.T("settings.ime.no_rules"),
			body:  i18n.T("settings.empty.ime_rules.body"),
			cta:   i18n.T("settings.empty.ime_rules.cta"),
			checkFn: func() bool {
				return tt.Focused(i18n.T("settings.ime.rule_target"))
			},
		},
	} {
		t.Run(tc.page, func(t *testing.T) {
			openSettingsSection(t, tt, tc.page)
			if !tt.HasText(tc.title) {
				t.Fatalf("missing empty title %q; texts=%v", tc.title, tt.Texts())
			}
			if !tt.HasText(tc.body) {
				t.Fatalf("missing empty body %q; texts=%v", tc.body, tt.Texts())
			}
			if err := tt.Click(tc.cta); err != nil {
				t.Fatalf("click CTA %q: %v", tc.cta, err)
			}
			tt.Frame()
			if !tc.checkFn() {
				t.Fatalf("clicking CTA %q did not enter creation flow", tc.cta)
			}
		})
	}
}

func TestIA24_PermissionsPageNoTrueFalseAndStatusDots(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	ctrl, br, _, _ := newTestController(t, nil)
	br.mu.Lock()
	br.perms = host.PermissionStatus{
		Accessibility:   true,
		InputMonitoring: false,
		ScreenRecording: false,
		Details: map[string]string{
			"secureInput":     "false",
			"mouseHook":       "true",
			"screenRecording": "denied",
		},
	}
	br.mu.Unlock()
	_, _ = ctrl.RefreshPermissions(t.Context())
	tt := ui.NewTester(ctrl.SettingsView, 1100, 900)
	tt.Frame()

	openSettingsSection(t, tt, i18n.T("settings.section.permissions"))
	for _, txt := range tt.Texts() {
		lower := strings.ToLower(strings.TrimSpace(txt))
		if lower == "true" || lower == "false" {
			t.Fatalf("IA-24 rendered raw boolean string %q on Permissions page", txt)
		}
	}
	if !tt.HasText(i18n.T("settings.perm.granted")) || !tt.HasText(i18n.T("settings.perm.not_granted")) {
		t.Fatalf("expected localized Granted/Not Granted text, got %v", tt.Texts())
	}
	if !tt.HasText(i18n.T("settings.perm.open_settings")) {
		t.Fatalf("expected Open Settings button when permission is not granted, got %v", tt.Texts())
	}
}
