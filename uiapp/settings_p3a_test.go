package uiapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func TestP3A_SinglePageSourceAndZeroLegacyNavSymbols(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	pages := settingsPages()
	if len(pages) != 14 {
		t.Fatalf("settingsPages() len = %d, want 14", len(pages))
	}
	groups := settingsGroups()
	if len(groups) != 5 {
		t.Fatalf("settingsGroups() len = %d, want 5", len(groups))
	}
	wantGroups := []string{"input", "productivity", "intelligence", "system", "about"}
	for i, g := range groups {
		if g.Key != wantGroups[i] {
			t.Fatalf("settingsGroups()[%d].Key = %q, want %q", i, g.Key, wantGroups[i])
		}
	}

	seen := map[string]bool{}
	for _, p := range pages {
		if p.Key == "" || seen[p.Key] {
			t.Fatalf("duplicate or empty page key %q", p.Key)
		}
		seen[p.Key] = true
		if got, ok := resolveSettingsPageKey(p.Key); !ok || got != p.Key {
			t.Fatalf("resolveSettingsPageKey(%q) = (%q, %v)", p.Key, got, ok)
		}
	}
	aliases := map[string]string{
		"general": "triggers",
		"ime":     "ime-keymap",
		"keymap":  "ime-keymap",
		"prompts": "templates",
		"vault":   "security",
	}
	for raw, want := range aliases {
		if got, ok := resolveSettingsPageKey(raw); !ok || got != want {
			t.Fatalf("resolveSettingsPageKey(%q) = (%q, %v), want (%q, true)", raw, got, ok, want)
		}
	}

	ctrl, _, _, _ := newTestController(t, nil)
	var opened []SettingsTab
	ctrl.OnOpenSettings = func(tab SettingsTab) { opened = append(opened, tab) }

	// First OpenSettings(SettingsTabGeneral) defaults to triggers.
	ctrl.OpenSettings(SettingsTabGeneral)
	if got := ctrl.currentSettingsPageKey(); got != "triggers" {
		t.Fatalf("first OpenSettings(SettingsTabGeneral) -> %q, want triggers", got)
	}

	for tab, wantPage := range map[SettingsTab]string{
		SettingsTabAI:          "ai",
		SettingsTabIMEKeymap:   "ime-keymap",
		SettingsTabPermissions: "permissions",
		SettingsTabConfigs:     "configs",
	} {
		ctrl.OpenSettings(tab)
		if got := ctrl.currentSettingsPageKey(); got != wantPage {
			t.Fatalf("OpenSettings(%v) -> %q, want %q", tab, got, wantPage)
		}
	}

	// OpenSettings(SettingsTabGeneral) preserves last-visited page within General group or active page.
	if !ctrl.OpenSettingsPage("clipboard") || ctrl.currentSettingsPageKey() != "clipboard" {
		t.Fatalf("OpenSettingsPage(clipboard) failed: %q", ctrl.currentSettingsPageKey())
	}
	ctrl.OpenSettings(SettingsTabGeneral)
	if got := ctrl.currentSettingsPageKey(); got != "clipboard" {
		t.Fatalf("reopened OpenSettings(SettingsTabGeneral) -> %q, want remembered clipboard", got)
	}

	// Internal setting command routing works across secondary pages.
	for _, cmd := range []string{"ai", "clipboard", "security", "templates", "appearance", "workflows"} {
		ctrl.handleInternalSettingCommand([]string{cmd})
		if got := ctrl.currentSettingsPageKey(); got != cmd {
			t.Fatalf("handleInternalSettingCommand(%q) -> %q", cmd, got)
		}
	}
	if len(opened) == 0 {
		t.Fatal("expected OnOpenSettings callbacks")
	}

	// Zero legacy nav symbols in production sources under uiapp/ and main.go.
	legacy := []string{"settingsTabIdx", "settingsSectionMeta", "SettingsTabNames", "settingsNavigationSections"}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join("..", "main.go"))
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, sym := range legacy {
			if strings.Contains(string(raw), sym) {
				t.Fatalf("legacy nav symbol %q still present in %s", sym, f)
			}
		}
	}
}

func TestP3A_SidebarScrollsAtMinimumHeightWithFixedSearch(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 500)
	tt.Frame()

	searchLabel := i18n.T("settings.search.placeholder")
	searchBefore, ok := tt.Find(searchLabel)
	if !ok {
		t.Fatalf("search input %q not found", searchLabel)
	}
	aboutBefore, ok := tt.Find(i18n.T("settings.section.about"))
	if !ok {
		t.Fatalf("About Clavis rail entry not found")
	}
	if aboutBefore.Y+aboutBefore.H <= 500 {
		t.Fatalf("expected About Clavis below 500px fold at 960x500 so sidebar must scroll, got Y=%.1f H=%.1f", aboutBefore.Y, aboutBefore.H)
	}

	tt.Move(searchBefore.X+searchBefore.W/2, 260)
	tt.Scroll(searchBefore.X+searchBefore.W/2, 260, 0, 240)
	tt.Frame()

	searchAfter, ok := tt.Find(searchLabel)
	if !ok {
		t.Fatal("search input missing after sidebar scroll")
	}
	if searchAfter.Y != searchBefore.Y {
		t.Fatalf("search box scrolled away (before Y=%.1f, after Y=%.1f)", searchBefore.Y, searchAfter.Y)
	}
	aboutAfter, ok := tt.Find(i18n.T("settings.section.about"))
	if !ok {
		t.Fatal("About Clavis missing after sidebar scroll")
	}
	if aboutAfter.Y >= aboutBefore.Y {
		t.Fatalf("sidebar did not scroll upward at 960x500: aboutBefore.Y=%.1f aboutAfter.Y=%.1f", aboutBefore.Y, aboutAfter.Y)
	}
}

func TestP3A_Cmd1To5JumpToGroupFirstPage(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.setSettingsPage("about")
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()

	cases := []struct {
		key  ui.Key
		want string
	}{
		{ui.Key1, "triggers"},
		{ui.Key2, "snippets"},
		{ui.Key3, "ai"},
		{ui.Key4, "appearance"},
		{ui.Key5, "about"},
	}
	for _, tc := range cases {
		tt.Key(primaryMod(), tc.key)
		tt.Frame()
		if got := ctrl.currentSettingsPageKey(); got != tc.want {
			t.Fatalf("Cmd+%v selected %q, want %q", tc.key, got, tc.want)
		}
	}
}

func TestP3A_RightPaneScrollResetsOnPageSwitch(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.setSettingsPage("appearance")
	tt := ui.NewTester(ctrl.SettingsView, 960, 500)
	tt.Frame()

	anchorLabel := i18n.T("settings.appearance.theme")
	r0, ok := tt.Find(anchorLabel)
	if !ok {
		t.Fatalf("missing appearance anchor %q", anchorLabel)
	}

	// Scroll the right pane downward.
	tt.Move(600, 260)
	tt.Scroll(600, 260, 0, 240)
	tt.Frame()
	rScrolled, ok := tt.Find(anchorLabel)
	if !ok {
		t.Fatalf("missing appearance anchor after scroll")
	}
	if rScrolled.Y >= r0.Y {
		t.Fatalf("right pane did not scroll: before Y=%.1f after Y=%.1f", r0.Y, rScrolled.Y)
	}

	// Switch away to clipboard, then back to appearance: scroll must reset to top.
	ctrl.setSettingsPage("clipboard")
	tt.Frame()
	ctrl.setSettingsPage("appearance")
	tt.Frame()
	tt.Frame()

	rReset, ok := tt.Find(anchorLabel)
	if !ok {
		t.Fatalf("missing appearance anchor after switch back")
	}
	if rReset.Y != r0.Y {
		t.Fatalf("right pane scroll did not reset on page switch: initial Y=%.1f reset Y=%.1f", r0.Y, rReset.Y)
	}
}

func TestP3A_SearchIndexMultiHitJumpHighlightAndSecretExclusion(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)

	ctrl, _, _, _ := newTestController(t, nil)
	apiSecret := "sk-p3a-supersecret-987654321"
	vaultSecret := "vault-p3a-secret-987654321"
	if err := ctrl.SaveAPIKey(apiSecret); err != nil {
		t.Fatalf("SaveAPIKey: %v", err)
	}
	if _, err := ctrl.VaultPutSecret("ref-p3a-secret", "clavis.p3a.test", "P3A Staging Service", secrets.SecretKindPassword, vaultSecret); err != nil {
		t.Fatalf("VaultPutSecret: %v", err)
	}
	ctrl.mu.Lock()
	ctrl.uiState.apiKeyDraft = apiSecret
	ctrl.mu.Unlock()

	for _, secQuery := range []string{apiSecret, "supersecret-987654321", vaultSecret, "secret-987654321"} {
		if hits := settingsSearchQuery(secQuery, ctrl.Config()); len(hits) != 0 {
			t.Fatalf("secret query %q leaked %d search results: %+v", secQuery, len(hits), hits)
		}
	}

	// Verify EN and ZH multi-hit search ("clipboard" & "剪贴板") finds both page and shortcut field.
	for _, tc := range []struct {
		lang  i18n.Language
		query string
	}{
		{i18n.English, "clipboard"},
		{i18n.Chinese, "剪贴板"},
	} {
		i18n.SetLanguage(tc.lang)
		results := settingsSearchQuery(tc.query, ctrl.Config())
		var foundPage, foundTriggerField bool
		for _, r := range results {
			if r.Kind == searchKindPage && r.PageKey == "clipboard" {
				foundPage = true
			}
			if r.Kind == searchKindField && r.PageKey == "triggers" && r.FieldKey == "clipboard_shortcut" {
				foundTriggerField = true
			}
		}
		if !foundPage || !foundTriggerField {
			t.Fatalf("query %q (%s) missing expected multi-hits: foundPage=%v foundTriggerField=%v results=%+v",
				tc.query, tc.lang, foundPage, foundTriggerField, results)
		}
	}

	i18n.SetLanguage(i18n.English)
	ctrl.setSettingsPage("about")
	tt := ui.NewTester(ctrl.SettingsView, 960, 500)
	tt.Frame()

	// Cmd+F focuses search.
	tt.Key(primaryMod(), ui.KeyF)
	tt.Frame()
	searchLabel := i18n.T("settings.search.placeholder")
	if !tt.Focused(searchLabel) {
		t.Fatalf("Cmd+F did not focus settings search input")
	}

	// Typing "clipboard" must NOT change the right pane ("about").
	tt.Type("clipboard")
	tt.Frame()
	if got := ctrl.currentSettingsPageKey(); got != "about" {
		t.Fatalf("typing query changed right pane to %q prematurely, want about", got)
	}

	// Select the triggers:clipboard_shortcut field hit with KeyDown + Enter.
	results := settingsSearchQuery("clipboard", ctrl.Config())
	targetIdx := -1
	for i, r := range results {
		if r.Kind == searchKindField && r.PageKey == "triggers" && r.FieldKey == "clipboard_shortcut" {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		t.Fatal("triggers:clipboard_shortcut missing from results")
	}
	for i := 0; i < targetIdx; i++ {
		tt.Key(0, ui.KeyDown)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Frame()

	if got := ctrl.currentSettingsPageKey(); got != "triggers" {
		t.Fatalf("Enter on search result selected %q, want triggers", got)
	}
	se := ctrl.settingsEditor()
	if hf := seGet(se, func(s *settingsEditorState) string { return s.highlightField }); hf != "triggers:clipboard_shortcut" {
		t.Fatalf("highlightField = %q, want triggers:clipboard_shortcut", hf)
	}
	if animated := seGet(se, func(s *settingsEditorState) bool { return s.lastScrollAnimated }); !animated {
		t.Fatal("expected smooth scroll animation when ReduceMotion is off")
	}

	// After 1.2s highlight expires deterministically.
	tt.Advance(1300 * time.Millisecond)
	tt.Frame()
	if hf := seGet(se, func(s *settingsEditorState) string { return s.highlightField }); hf != "" {
		t.Fatalf("highlightField stayed %q past 1.2s", hf)
	}

	// Under ReduceMotion, scrolling must not animate.
	ctrlRM, _, _, _ := newTestController(t, nil)
	ctrlRM.setSettingsPage("about")
	ttRM := ui.NewTesterWith(ctrlRM.SettingsView, 960, 500, ui.TesterOptions{
		Preferences: ui.Preferences{ReduceMotion: true},
	})
	ttRM.Frame()
	ttRM.Key(primaryMod(), ui.KeyF)
	ttRM.Frame()
	ttRM.Type("clipboard_shortcut")
	ttRM.Frame()
	ttRM.Key(0, ui.KeyEnter)
	ttRM.Frame()
	ttRM.Frame()
	if got := ctrlRM.currentSettingsPageKey(); got != "triggers" {
		t.Fatalf("ReduceMotion jump landed on %q, want triggers", got)
	}
	if animated := seGet(ctrlRM.settingsEditor(), func(s *settingsEditorState) bool { return s.lastScrollAnimated }); animated {
		t.Fatal("ReduceMotion jump used scroll animation, want false")
	}
}

func TestP3A_SearchEscClearThenBlurNeverClosesSettings(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.setSettingsPage("about")
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()

	searchLabel := i18n.T("settings.search.placeholder")
	tt.Key(primaryMod(), ui.KeyF)
	tt.Frame()
	tt.Type("clipboard")
	tt.Frame()

	// 1st Esc clears query while keeping focus on search and keeping Settings/right pane intact.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if q := seGet(ctrl.settingsEditor(), func(s *settingsEditorState) string { return s.railQuery }); q != "" {
		t.Fatalf("1st Esc left railQuery = %q, want empty", q)
	}
	if !tt.Focused(searchLabel) {
		t.Fatal("1st Esc unfocused search box; want it focused until empty Esc")
	}
	if ctrl.currentSettingsPageKey() != "about" {
		t.Fatalf("1st Esc altered active page to %q", ctrl.currentSettingsPageKey())
	}

	// 2nd Esc when empty blurs search box, never closes Settings.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.Focused(searchLabel) {
		t.Fatal("2nd Esc on empty search failed to blur search box")
	}
	if !tt.HasText(i18n.T("settings.section.about.hint")) {
		t.Fatal("Settings content disappeared after Esc")
	}

	// 3rd Esc in Settings leaves Settings rendered and responsive.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !tt.HasText(i18n.T("settings.section.about.hint")) {
		t.Fatal("Settings closed after 3rd Esc")
	}
	_ = config.FileMain
}

func TestP3A_NarrowHeader760WrapsWithoutTruncatingTitle(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)

	for _, lang := range []i18n.Language{i18n.English, i18n.Chinese} {
		i18n.SetLanguage(lang)
		ctrl, _, _, _ := newTestController(t, nil)
		for _, meta := range settingsPages() {
			ctrl.setSettingsPage(meta.Key)
			tt := ui.NewTester(ctrl.SettingsView, 760, 500)
			tt.Frame()
			tt.Frame()

			wantTitle := settingsGroupLabel(meta.Group) + " / " + meta.Label
			rTitle, ok := tt.Find(wantTitle)
			if !ok {
				t.Fatalf("lang=%s page=%s missing full header title %q in texts=%v", lang, meta.Key, wantTitle, tt.Texts())
			}
			if rTitle.W <= 0 || rTitle.H <= 0 || rTitle.X+rTitle.W > 760.5 {
				t.Fatalf("lang=%s page=%s title bounds invalid at 760px: %+v", lang, meta.Key, rTitle)
			}
			for _, txt := range tt.Texts() {
				if strings.HasPrefix(txt, wantTitle[:min(len(wantTitle), 6)]) && (strings.Contains(txt, "...") || strings.Contains(txt, "…")) && txt != meta.Hint {
					t.Fatalf("lang=%s page=%s header text appears truncated: %q", lang, meta.Key, txt)
				}
			}
		}
	}
}
