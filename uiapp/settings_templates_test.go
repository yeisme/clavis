package uiapp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/template"
)

func buildTestCatalogDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	enBody := []byte("Summarize {{topic}} in {{tone}} tone.")
	zhBody := []byte("请用{{tone}}语气总结{{topic}}。")
	badBody := []byte("Summarize {{undeclared_placeholder}}.")

	enPath := "solutions/writing/multi-summary/prompts/main.en.md"
	zhPath := "solutions/writing/multi-summary/prompts/main.zh-CN.md"
	badPath := "solutions/writing/broken-contract/prompts/main.en.md"

	for rel, content := range map[string][]byte{
		enPath:  enBody,
		zhPath:  zhBody,
		badPath: badBody,
	} {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	solGood := template.Solution{
		PackageID: "demo/writing",
		ID:        "multi-summary",
		Version:   "1.2.0",
		Category:  "writing",
		Tags:      []string{"modality:text"},
		Rights:    "MIT",
		Maturity:  "beta",
		Locales: map[string]template.Locale{
			"en":    {Title: "Multi-Locale Summary", Summary: "Bilingual summary template"},
			"zh-CN": {Title: "多语言摘要", Summary: "支持中文合同与审阅门控"},
		},
		Templates: []template.Template{
			{Role: "main", Locale: "en", Path: enPath, Digest: template.Digest(enBody)},
			{Role: "main", Locale: "zh-CN", Path: zhPath, Digest: template.Digest(zhBody)},
		},
	}
	solBad := template.Solution{
		PackageID: "demo/writing",
		ID:        "broken-contract",
		Version:   "0.1.0",
		Category:  "writing",
		Tags:      []string{"modality:text"},
		Rights:    "internal",
		Maturity:  "exploratory",
		Locales: map[string]template.Locale{
			"en": {Title: "Broken Contract Template", Summary: "Has undeclared placeholder"},
		},
		Templates: []template.Template{
			{Role: "main", Locale: "en", Path: badPath, Digest: template.Digest(badBody)},
		},
	}

	writeContract := func(s template.Solution, tmpl template.Template, perms []string, inputs []template.InputDefinition) {
		c := template.Contract{
			SchemaVersion:  template.ContractSchema,
			PackageID:      s.PackageID,
			SolutionID:     s.ID,
			Version:        s.Version,
			Role:           tmpl.Role,
			Locale:         tmpl.Locale,
			TemplatePath:   tmpl.Path,
			TemplateDigest: tmpl.Digest,
			License:        s.Rights,
			Permissions:    perms,
			Inputs:         inputs,
		}
		var err error
		c.Digest, err = template.CanonicalContractDigest(c)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		sidecarRel, err := template.ContractPath(tmpl)
		if err != nil {
			t.Fatal(err)
		}
		full := filepath.Join(dir, filepath.FromSlash(sidecarRel))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	writeContract(solGood, solGood.Templates[0], []string{"preview", template.ExecuteRequiresReview}, []template.InputDefinition{
		{Name: "topic", Type: "string", Required: true, Labels: map[string]string{"en": "Topic"}},
		{Name: "tone", Type: "enum", Enum: []string{"concise", "detailed"}, Default: "concise"},
	})
	writeContract(solGood, solGood.Templates[1], []string{"preview", template.ExecuteRequiresReview}, []template.InputDefinition{
		{Name: "topic", Type: "string", Required: true, Labels: map[string]string{"zh-CN": "主题", "en": "Topic"}},
		{Name: "tone", Type: "enum", Enum: []string{"简洁", "详细"}, Default: "简洁", Labels: map[string]string{"zh-CN": "语气"}},
	})
	writeContract(solBad, solBad.Templates[0], []string{"preview"}, nil)

	var err error
	solGood.Digest, err = template.CanonicalSolutionDigest(solGood)
	if err != nil {
		t.Fatal(err)
	}
	solBad.Digest, err = template.CanonicalSolutionDigest(solBad)
	if err != nil {
		t.Fatal(err)
	}

	cat := template.Catalog{
		SchemaVersion: template.SchemaVersion,
		Repository: template.Repository{
			ID:              "test-promptrepo",
			Name:            "Test PromptRepo",
			DefaultLocale:   "en",
			TaxonomyVersion: "v1",
		},
		GeneratedAt: time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC),
		Discovery: []template.Discovery{
			{PackageID: solGood.PackageID, SolutionID: solGood.ID, Role: "main", Locale: "en", Title: "Multi-Locale Summary", Media: []string{"text"}},
			{PackageID: solGood.PackageID, SolutionID: solGood.ID, Role: "main", Locale: "zh-CN", Title: "多语言摘要", Media: []string{"text"}},
			{PackageID: solBad.PackageID, SolutionID: solBad.ID, Role: "main", Locale: "en", Title: "Broken Contract Template", Media: []string{"text"}},
		},
		Solutions: []template.Solution{solGood, solBad},
	}
	cat.Digest, err = template.CanonicalCatalogDigest(cat)
	if err != nil {
		t.Fatal(err)
	}
	rawCat, err := json.Marshal(cat)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "catalog.json"), rawCat, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSettingsTemplatesLocalPromptsCRUDAndRoundTrip(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 1120, 1400)
	tt.Frame()

	// Navigate to Templates & Prompts from the Settings rail.
	openSettingsSection(t, tt, i18n.T("settings.section.templates"))
	if got := settingsPageKey(ctrl); got != "templates" {
		t.Fatalf("selected page = %q, want %q", got, "templates")
	}
	if !tt.HasText(i18n.T("settings.templates.defaults.card")) {
		t.Fatalf("missing optimizer defaults card, got %v", tt.Texts())
	}

	// Update InlineOptimizePrefix and verify persistence to prompts.toml.
	if err := tt.Click(i18n.T("settings.templates.defaults.prefix")); err != nil {
		t.Fatalf("click inline prefix input: %v", err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("!!")
	tt.Frame()
	if err := tt.Click(i18n.T("settings.templates.defaults.save_prefix")); err != nil {
		t.Fatalf("click Apply Prefix: %v", err)
	}
	tt.Frame()
	if got := ctrl.Config().Prompts.InlineOptimizePrefix; got != "!!" {
		t.Fatalf("InlineOptimizePrefix = %q, want !!", got)
	}
	if body := readConfigFile(t, ctrl, config.FilePrompts); !strings.Contains(body, `inline_optimize_prefix = "!!"`) {
		t.Fatalf("prompts.toml missing updated prefix:\n%s", body)
	}

	// Add a brand-new local prompt with {{slot}} placeholders.
	before := len(ctrl.Config().Prompts.Items)
	if err := tt.Click(i18n.T("settings.templates.local.new")); err != nil {
		t.Fatalf("click New Prompt: %v", err)
	}
	tt.Frame()

	for _, field := range []struct{ label, value string }{
		{i18n.T("settings.templates.prompt.id"), "release-brief"},
		{i18n.T("settings.templates.prompt.name"), "Release Brief"},
		{i18n.T("settings.templates.prompt.trigger"), "/rel"},
		{i18n.T("settings.templates.prompt.category"), "writing"},
		{i18n.T("settings.templates.prompt.description"), "Draft a concise release summary"},
		{i18n.T("settings.templates.prompt.content"), "Write release notes for {{product}} v{{version}}."},
	} {
		if err := tt.Click(field.label); err != nil {
			t.Fatalf("click %s: %v", field.label, err)
		}
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type(field.value)
		tt.Frame()
	}

	// Live {{slot}} detection and preview update before saving.
	if !tt.HasText("{{product}}") || !tt.HasText("{{version}}") {
		t.Fatalf("expected detected {{slot}} chips, got %v", tt.Texts())
	}
	if !tt.HasText("Write release notes for <product> v<version>.") {
		t.Fatalf("expected live slot preview, got %v", tt.Texts())
	}

	if err := tt.Click(i18n.T("settings.templates.prompt.save")); err != nil {
		t.Fatalf("click Save Prompt: %v", err)
	}
	tt.Frame()

	items := ctrl.Config().Prompts.Items
	if len(items) != before+1 {
		t.Fatalf("prompts count = %d, want %d", len(items), before+1)
	}
	var saved config.PromptTemplate
	for _, item := range items {
		if item.ID == "release-brief" {
			saved = item
			break
		}
	}
	if saved.Title != "Release Brief" || saved.Trigger != "/rel" || len(saved.Slots) != 2 {
		t.Fatalf("saved prompt mismatch: %+v", saved)
	}
	body := readConfigFile(t, ctrl, config.FilePrompts)
	if !strings.Contains(body, `id = "release-brief"`) || !strings.Contains(body, `trigger = "/rel"`) {
		t.Fatalf("prompts.toml missing saved prompt:\n%s", body)
	}

	// Edit the saved prompt and re-save.
	if err := tt.Click(i18n.T("settings.templates.prompt.name")); err != nil {
		t.Fatalf("click Prompt Name for edit: %v", err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Release Brief v2")
	tt.Frame()
	if err := tt.Click(i18n.T("settings.templates.prompt.save")); err != nil {
		t.Fatalf("click Save Prompt after edit: %v", err)
	}
	tt.Frame()
	if body := readConfigFile(t, ctrl, config.FilePrompts); !strings.Contains(body, `title = "Release Brief v2"`) {
		t.Fatalf("prompts.toml did not persist updated title:\n%s", body)
	}

	// Delete the prompt and verify removal from prompts.toml.
	if err := tt.Click(i18n.T("settings.templates.prompt.delete")); err != nil {
		t.Fatalf("click Delete Prompt: %v", err)
	}
	tt.Frame()
	if got := len(ctrl.Config().Prompts.Items); got != before {
		t.Fatalf("prompts count after delete = %d, want %d", got, before)
	}
	if body := readConfigFile(t, ctrl, config.FilePrompts); strings.Contains(body, `id = "release-brief"`) {
		t.Fatalf("prompts.toml still contains deleted prompt:\n%s", body)
	}
}

func TestSettingsTemplatesCatalogScanImportOverlayAndComposerLaunch(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	catalogDir := buildTestCatalogDir(t)
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.SetTemplateLibraryDir(filepath.Join(t.TempDir(), "templates"))
	ctrl.setSettingsPage("templates")

	tt := ui.NewTester(ctrl.SettingsView, 1180, 1600)
	tt.Frame()

	// Switch to Catalog Library sub-tab.
	if err := tt.Click(i18n.T("settings.templates.tab.catalog")); err != nil {
		t.Fatalf("click Catalog Library sub-tab: %v (texts=%v)", err, tt.Texts())
	}
	tt.Frame()

	// Point to local promptrepo catalog directory and click Refresh Catalog.
	ctrl.SetTemplateCatalogDir(catalogDir)
	tt.Frame()
	if err := tt.Click(i18n.T("settings.templates.catalog.refresh")); err != nil {
		t.Fatalf("click Refresh Catalog: %v", err)
	}
	tt.Frame()
	tt.Frame()

	snap, _, diags := ctrl.TemplateLibrarySnapshot()
	if snap == nil || len(diags) != 0 {
		t.Fatalf("expected verified catalog snapshot with 0 refresh diagnostics, snap=%v diags=%+v", snap, diags)
	}
	if !tt.HasText("Test PromptRepo") || !tt.HasText("多语言摘要") || !tt.HasText("Multi-Locale Summary") {
		t.Fatalf("expected catalog repo and bilingual templates rendered, got %v", tt.Texts())
	}

	// Import the zh-CN variant into CAS and verify validated contract + SHA-256 digest badge.
	if importDiags := ctrl.ImportTemplateVariant("demo/writing", "multi-summary", "main", "zh-CN"); len(importDiags) != 0 {
		t.Fatalf("ImportTemplateVariant(zh-CN) diagnostics: %+v", importDiags)
	}
	tt.Frame()
	tt.Frame()

	_, imported, viewDiags := ctrl.TemplateLibrarySnapshot()
	if len(imported) != 1 || imported[0].ContractStatus != "validated" || imported[0].Contract == nil {
		t.Fatalf("expected 1 validated zh-CN imported item, got imported=%+v diags=%+v", imported, viewDiags)
	}
	if !tt.HasText(i18n.T("settings.templates.catalog.status_validated")) {
		t.Fatalf("expected Contract Validated badge in UI, got %v", tt.Texts())
	}

	// Invalid overlay (attempting to mutate body) must fail closed with diagnostic without leaking secret.
	badOvDiags := ctrl.SaveTemplateOverlay("multi-summary", "main", "zh-CN", `body = "SECRET_OVERRIDE"`)
	if len(badOvDiags) == 0 || badOvDiags[0].Code != template.InvalidOverlay {
		t.Fatalf("expected InvalidOverlay diagnostic, got %+v", badOvDiags)
	}
	// Valid local TOML overlay persists and applies title + slot default.
	goodOv := "title = \"定制中文摘要\"\n[slot.topic]\ndefault = \"默认主题\"\n"
	if ovDiags := ctrl.SaveTemplateOverlay("multi-summary", "main", "zh-CN", goodOv); len(ovDiags) != 0 {
		t.Fatalf("valid overlay failed: %+v", ovDiags)
	}
	tt.Frame()
	if !tt.HasText("定制中文摘要") {
		t.Fatalf("expected overlaid title in UI, got %v", tt.Texts())
	}

	// Click "Open in Composer" -> launches contract-backed zh-CN template with overlay slot default!
	opened := 0
	ctrl.OnOpenPromptComposer = func() { opened++ }
	var aiCalls []string
	ctrl.SetPromptComposerAIExecutor(template.AIExecutorFunc(func(_ context.Context, rendered string) error {
		aiCalls = append(aiCalls, rendered)
		return nil
	}))
	if err := tt.Click(i18n.T("settings.templates.catalog.action_composer")); err != nil {
		t.Fatalf("click Open in Composer: %v (texts=%v)", err, tt.Texts())
	}
	tt.Frame()

	if opened != 1 || !ctrl.PromptComposerOpen() || !ctrl.PromptComposerContractActive() {
		t.Fatalf("expected contract-backed composer opened (opened=%d open=%v contract=%v)", opened, ctrl.PromptComposerOpen(), ctrl.PromptComposerContractActive())
	}
	if prev := ctrl.PromptComposerPreview(); prev.RenderedText != "请用简洁语气总结默认主题。" {
		t.Fatalf("composer preview with overlay default = %q, want 请用简洁语气总结默认主题。", prev.RenderedText)
	}
	if err := ctrl.RequestPromptComposerAIReview(); err != nil {
		t.Fatalf("RequestPromptComposerAIReview: %v", err)
	}
	if err := ctrl.ConfirmPromptComposerAI(context.Background()); err != nil {
		t.Fatalf("ConfirmPromptComposerAI: %v", err)
	}
	if len(aiCalls) != 1 || aiCalls[0] != "请用简洁语气总结默认主题。" {
		t.Fatalf("unexpected AI execution calls: %+v", aiCalls)
	}

	// Importing the broken-contract variant must surface UndeclaredPlaceholder diagnostic and mark contract_invalid.
	brokenDiags := ctrl.ImportTemplateVariant("demo/writing", "broken-contract", "main", "en")
	hasUndeclared := false
	for _, d := range brokenDiags {
		if d.Code == template.UndeclaredPlaceholder {
			hasUndeclared = true
		}
	}
	if !hasUndeclared {
		t.Fatalf("expected UndeclaredPlaceholder diagnostic for broken contract, got %+v", brokenDiags)
	}
	tt.Frame()
	if !tt.HasText(i18n.T("settings.templates.catalog.status_invalid")) {
		t.Fatalf("expected Contract Invalid badge rendered, got %v", tt.Texts())
	}
}

func TestTemplatesAndPromptContractSubstateIsolation(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	ctrl, _, _, _ := newTestController(t, nil)
	openedComposer := 0
	ctrl.OnOpenPromptComposer = func() { openedComposer++ }

	ctrl.OpenSettingsPage("templates")
	tt := ui.NewTester(ctrl.SettingsView, 920, 620)
	tt.Frame()

	// Open PromptComposer from the first prompt template.
	prompts := ctrl.Config().Prompts.Items
	if len(prompts) == 0 {
		t.Fatal("expected default prompt templates")
	}
	ctrl.OpenPromptComposer(prompts[0], ctrl.State().Context)
	if openedComposer != 1 || !ctrl.PromptComposerOpen() {
		t.Fatalf("OpenPromptComposer did not open PromptComposer (opened=%d open=%v)", openedComposer, ctrl.PromptComposerOpen())
	}

	// Zero-value controller isolation for templatesUI and promptContract substates.
	a := &AppController{}
	b := &AppController{}
	defer a.Close()
	defer b.Close()
	a.SetTemplatesSettingsSubTab(templatesSubTabCatalog)
	a.SetTemplateCatalogDir("/tmp/catalog-a")
	if b.templatesUI().activeSubTab != templatesSubTabLocal || b.templatesUI().catalogDir != "" {
		t.Fatal("templatesUI substate leaked between controllers")
	}
	a.promptContractStateFor().active = true
	if b.PromptComposerContractActive() {
		t.Fatal("promptContract substate leaked between controllers")
	}
}
