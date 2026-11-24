package uiapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/template"
)

func TestPromptTemplateOmniboxAndSlotComposerFlow(t *testing.T) {
	f := newInteractionFixture(t, nil)

	// Add a template with text + select + cursor + context vars, plus an app-scoped template.
	cfg := f.ctrl.Config()
	cfg.Prompts.Items = append(cfg.Prompts.Items, config.PromptTemplate{
		ID:          "review-select",
		Trigger:     "/rev",
		Title:       "Review With Mode",
		Category:    "coding",
		Description: "Multi-slot review template",
		Template:    "[{{app}}] {{target}} ({{mode:quick|deep}}) -> {{cursor}}end",
		Slots: []config.PromptSlot{
			{Name: "target", Label: "Target File", Kind: "text", Required: true},
			{Name: "mode", Label: "Review Mode", Kind: "select", Options: []string{"quick", "deep"}, Default: "quick"},
		},
	})
	if err := f.ctrl.ConfigStore.SavePrompts(cfg.Prompts); err != nil {
		t.Fatalf("SavePrompts: %v", err)
	}
	f.ctrl.StateMachine.SetConfig(f.ctrl.ConfigStore.Current())

	openedCount := 0
	closedCount := 0
	var pills []string
	f.ctrl.OnOpenPromptComposer = func() { openedCount++ }
	f.ctrl.OnClosePromptComposer = func() { closedCount++ }
	f.ctrl.OnShowIMEPill = func(label string, _ *host.CursorAnchor) {
		pills = append(pills, label)
	}

	f.ctrl.OpenOmnibox(config.TabActions, "/rev")
	tt := f.omniboxTester(t)
	if !tt.HasText("Review With Mode") {
		t.Fatalf("expected 'Review With Mode' in Omnibox for '/rev', got %v", tt.Texts())
	}

	// Primary action (Enter) closes Omnibox and opens Slot Composer.
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if openedCount != 1 || !f.ctrl.PromptComposerOpen() {
		t.Fatalf("expected PromptComposer opened (count=%d, open=%v)", openedCount, f.ctrl.PromptComposerOpen())
	}
	if f.ctrl.State().Phase != engine.PhaseHidden {
		t.Fatalf("expected Omnibox closed after opening PromptComposer, got phase=%s", f.ctrl.State().Phase)
	}

	pw, ph := PreferredPromptComposerPanelSize(f.ctrl.PromptComposerSlotCount())
	pt := ui.NewTester(f.ctrl.PromptComposerView, pw, ph)
	pt.Frame()

	if !pt.HasText("Review With Mode") || !pt.HasText("Target File") || !pt.HasText("Review Mode") {
		t.Fatalf("missing expected composer labels, got %v", pt.Texts())
	}

	// Required-slot validation blocks commit when 'Target File' is empty.
	pt.Key(0, ui.KeyEnter)
	pt.Frame()
	if !f.ctrl.PromptComposerOpen() {
		t.Fatal("expected required-slot validation to block commit and keep composer open")
	}
	if len(f.bridge.getInjected()) != 0 {
		t.Fatalf("expected no injection when required slot missing, got %+v", f.bridge.getInjected())
	}
	if errMsg := f.ctrl.PromptComposerValidationError(); !strings.Contains(errMsg, "target") {
		t.Fatalf("expected validation error naming 'target', got %q", errMsg)
	}
	if !pt.HasText(f.ctrl.PromptComposerValidationError()) {
		t.Fatalf("expected inline validation notice rendered, got %v", pt.Texts())
	}
	if len(pills) == 0 || !strings.Contains(pills[len(pills)-1], "target") {
		t.Fatalf("expected HUD/toast pill for missing required slot, got %+v", pills)
	}

	// Type into active slot 0 ('Target File').
	pt.Type("queue.go")
	pt.Frame()
	if prev := f.ctrl.PromptComposerPreview(); !strings.Contains(prev.RenderedText, "[TextEdit] queue.go (quick)") {
		t.Fatalf("expected live preview updated with typed value & app context, got %q", prev.RenderedText)
	}

	// Tab to slot 1 ('Review Mode', select) and cycle Right from 'quick' -> 'deep'.
	pt.Key(0, ui.KeyTab)
	pt.Frame()
	if f.ctrl.PromptComposerActiveSlotIndex() != 1 {
		t.Fatalf("expected ActiveSlotIndex=1 after Tab, got %d", f.ctrl.PromptComposerActiveSlotIndex())
	}
	pt.Key(0, ui.KeyRight)
	pt.Frame()
	if prev := f.ctrl.PromptComposerPreview(); !strings.Contains(prev.RenderedText, "[TextEdit] queue.go (deep) -> end") {
		t.Fatalf("expected live preview updated to 'deep' after Right, got %q", prev.RenderedText)
	}

	// Shift+Tab moves back to slot 0.
	pt.Key(ui.Shift, ui.KeyTab)
	pt.Frame()
	if f.ctrl.PromptComposerActiveSlotIndex() != 0 {
		t.Fatalf("expected ActiveSlotIndex=0 after Shift+Tab, got %d", f.ctrl.PromptComposerActiveSlotIndex())
	}

	// Commit injects rendered prompt with CursorBackOffset=3 ("end") and closes composer.
	pt.Key(0, ui.KeyEnter)
	pt.Frame()
	if closedCount != 1 || f.ctrl.PromptComposerOpen() {
		t.Fatalf("expected composer closed on commit (closed=%d, open=%v)", closedCount, f.ctrl.PromptComposerOpen())
	}
	waitUntil(t, 2*time.Second, func() bool {
		return len(f.bridge.getInjected()) >= 1
	}, "prompt composer commit injection")
	inj := f.bridge.getInjected()[0]
	if inj.text != "[TextEdit] queue.go (deep) -> end" || inj.backOffset != 3 {
		t.Fatalf("unexpected injected prompt: %+v", inj)
	}
}

func TestPromptComposerZeroSlotsAndEscape(t *testing.T) {
	f := newInteractionFixture(t, nil)
	closed := 0
	f.ctrl.OnClosePromptComposer = func() { closed++ }

	tpl := config.PromptTemplate{
		ID:       "ctx-only",
		Trigger:  "/ctx",
		Title:    "Context Summary",
		Template: "Summarize {{selection}} from {{app}}",
	}
	f.ctrl.OpenPromptComposer(tpl, engine.CapsuleContext{
		AppName:      "Notes",
		SelectedText: "important note",
	})

	pw, ph := PreferredPromptComposerPanelSize(0)
	pt := ui.NewTester(f.ctrl.PromptComposerView, pw, ph)
	pt.Frame()
	if !pt.HasText("Summarize important note from Notes") {
		t.Fatalf("expected zero-slot live preview, got %v", pt.Texts())
	}

	// Escape closes without injecting.
	pt.Key(0, ui.KeyEscape)
	pt.Frame()
	if closed != 1 || f.ctrl.PromptComposerOpen() {
		t.Fatalf("expected Escape to close composer (closed=%d, open=%v)", closed, f.ctrl.PromptComposerOpen())
	}
	if len(f.bridge.getInjected()) != 0 {
		t.Fatalf("Escape should not inject, got %+v", f.bridge.getInjected())
	}
}

func TestInlineOptimizeDraftStreamingAndInject(t *testing.T) {
	var capturedSystem string
	var capturedUser string

	doer := mockHTTPDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(req.Body)
			var payload struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.Unmarshal(raw, &payload)
			for _, m := range payload.Messages {
				if m.Role == "system" {
					capturedSystem = m.Content
				}
				if m.Role == "user" {
					capturedUser = m.Content
				}
			}
			return sseResponse(`Role: Go Engineer\n`, `Task: Write table-driven tests for Clavis.`), nil
		},
	}

	f := newInteractionFixture(t, doer)
	// Add glossary term so optimizer injects <glossary>.
	cfg := f.ctrl.Config()
	cfg.Glossary.Terms = []config.GlossaryTerm{
		{Term: "Clavis", Aliases: []string{"clavis"}, Category: "product", Hint: "Desktop assistant"},
	}
	if err := f.ctrl.ConfigStore.SaveGlossary(cfg.Glossary); err != nil {
		t.Fatalf("SaveGlossary: %v", err)
	}
	f.ctrl.Glossary = engine.NewGlossaryEngineFromConfig(cfg.Glossary)
	f.ctrl.AIClient.GlossaryEngine = f.ctrl.Glossary
	f.ctrl.StateMachine.SetConfig(f.ctrl.ConfigStore.Current())

	f.ctrl.OpenOmnibox(config.TabActions, "//write tests for clavis prompt engine")
	tt := f.omniboxTester(t)

	if st := f.ctrl.State(); len(st.OmniboxItems) == 0 || st.OmniboxItems[0].AIMode != "optimize" {
		t.Fatalf("expected optimize row for '//draft', got %+v", st.OmniboxItems)
	}

	// Confirm row 0 -> streams upgraded prompt using coding SystemPromptOverride.
	tt.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "inline optimize streaming completion")

	st := f.ctrl.State()
	if st.AIError != "" {
		t.Fatalf("unexpected AIError: %s", st.AIError)
	}
	if st.AIOutput != "Role: Go Engineer\nTask: Write table-driven tests for Clavis." {
		t.Fatalf("unexpected streamed output: %q", st.AIOutput)
	}
	if !strings.Contains(capturedSystem, "prompt optimizer for software engineering") {
		t.Fatalf("expected coding optimizer SystemPromptOverride, got:\n%s", capturedSystem)
	}
	if !strings.Contains(capturedSystem, "<glossary>") || !strings.Contains(capturedSystem, "Clavis") {
		t.Fatalf("expected glossary injected into optimizer system prompt, got:\n%s", capturedSystem)
	}
	if !strings.Contains(capturedUser, "write tests for clavis prompt engine") {
		t.Fatalf("expected draft in user prompt, got:\n%s", capturedUser)
	}

	// Render AI streaming surface and confirm with Enter -> injects upgraded prompt at caret.
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		return len(f.bridge.getInjected()) >= 1
	}, "accept optimized prompt injection")

	if got := f.bridge.getInjected()[0].text; got != "Role: Go Engineer\nTask: Write table-driven tests for Clavis." {
		t.Fatalf("injected optimized prompt = %q", got)
	}
}

func TestInlineOptimizeMissingKeyDegradesGracefully(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("CLAVIS_API_KEY", "")

	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}
	emptySec := secrets.NewMemoryAPIKeyStore() // intentionally no key saved
	ctrl := NewAppController(Options{
		ConfigStore: cfgStore,
		SecretStore: emptySec,
	})
	defer ctrl.Close()

	ctrl.OpenOmnibox(config.TabActions, "//upgrade this prompt")
	ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})

	waitUntil(t, 2*time.Second, func() bool {
		st := ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "missing key graceful failure")

	st := ctrl.State()
	if st.AIError == "" {
		t.Fatal("expected clear AIError when no API key is configured")
	}
	if !strings.Contains(st.AIError, "API") {
		t.Fatalf("expected clear API key guidance, got %q", st.AIError)
	}

	// Also verify nil AIClient degrades gracefully without panic.
	ctrl.mu.Lock()
	ctrl.AIClient = nil
	ctrl.mu.Unlock()
	ctrl.OpenOmnibox(config.TabActions, "//another draft")
	ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
	st = ctrl.State()
	if st.AIError != i18n.T("settings.ai.not_configured") {
		t.Fatalf("expected not_configured error for nil AIClient, got %q", st.AIError)
	}
}

func TestPromptComposerChineseLocalization(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	i18n.SetLanguage(i18n.Chinese)

	f := newInteractionFixture(t, nil)
	tpl := config.DefaultPromptsConfig().Items[0]
	f.ctrl.OpenPromptComposer(tpl, engine.CapsuleContext{AppName: "Xcode"})

	pw, ph := PreferredPromptComposerPanelSize(len(tpl.Slots))
	pt := ui.NewTester(f.ctrl.PromptComposerView, pw, ph)
	pt.Frame()

	for _, want := range []string{"提示词编辑器", "实时预览", "必填", "⏎ 插入", "Esc 取消"} {
		if !pt.HasText(want) {
			t.Fatalf("Chinese prompt composer missing %q, got %v", want, pt.Texts())
		}
	}
}

func TestPromptComposerContractFailClosedAndExecuteAIReviewGate(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	f := newInteractionFixture(t, nil)

	minCount, maxCount := 1.0, 5.0
	zhContract := template.Contract{
		SchemaVersion: template.ContractSchema,
		PackageID:     "demo/writing",
		SolutionID:    "zh-brief",
		Version:       "1.0.0",
		Role:          "main",
		Locale:        "zh-CN",
		License:       "MIT",
		Permissions:   []string{"preview", template.ExecuteRequiresReview},
		Inputs: []template.InputDefinition{
			{Name: "topic", Type: "string", Required: true, Labels: map[string]string{"zh-CN": "主题", "en": "Topic"}},
			{Name: "tone", Type: "enum", Enum: []string{"正式", "简洁"}, Default: "正式", Labels: map[string]string{"zh-CN": "语气"}},
			{Name: "points", Type: "integer", Min: &minCount, Max: &maxCount, Default: "3"},
		},
	}

	var execPrompts []string
	fakeExec := template.AIExecutorFunc(func(_ context.Context, rendered string) error {
		execPrompts = append(execPrompts, rendered)
		return nil
	})
	f.ctrl.SetPromptComposerAIExecutor(fakeExec)
	f.ctrl.OpenContractTemplateWithBody(zhContract, "请用{{tone}}语气写出关于{{topic}}的{{points}}条要点。", "中文摘要合同", engine.CapsuleContext{AppName: "Notes"})

	if !f.ctrl.PromptComposerOpen() || !f.ctrl.PromptComposerContractActive() {
		t.Fatal("expected contract-backed prompt composer to be open")
	}

	pw, ph := PreferredPromptComposerPanelSize(f.ctrl.PromptComposerSlotCount())
	pt := ui.NewTester(f.ctrl.PromptComposerView, pw, ph)
	pt.Frame()

	// Chinese localized slot labels and contract badge render cleanly.
	if !pt.HasText("中文摘要合同") || !pt.HasText("主题") || !pt.HasText("Contract · zh-CN") {
		t.Fatalf("expected contract title, localized Chinese label, and locale badge, got %v", pt.Texts())
	}

	// 1. Fail-closed on missing required slot: blocks preview, commit, copy, and AI review.
	if prev := f.ctrl.PromptComposerPreview(); prev.RenderedText != "" {
		t.Fatalf("expected fail-closed empty preview when required slot missing, got %q", prev.RenderedText)
	}
	if f.ctrl.CommitPromptComposer() {
		t.Fatal("CommitPromptComposer must fail closed when required slot is missing")
	}
	if f.ctrl.CopyPromptComposer() {
		t.Fatal("CopyPromptComposer must fail closed when required slot is missing")
	}
	if err := f.ctrl.RequestPromptComposerAIReview(); err == nil {
		t.Fatal("RequestPromptComposerAIReview must fail closed when required slot is missing")
	}
	if len(execPrompts) != 0 {
		t.Fatalf("AI executor must not be called on failed render, calls=%d", len(execPrompts))
	}

	// 2. Fail-closed on invalid enum or out-of-range integer.
	f.ctrl.SetPromptSlotValue("topic", "模板平台")
	f.ctrl.SetPromptSlotValue("tone", "非法选项")
	if prev := f.ctrl.PromptComposerPreview(); prev.RenderedText != "" {
		t.Fatalf("invalid enum must fail closed, got %q", prev.RenderedText)
	}
	if f.ctrl.CommitPromptComposer() {
		t.Fatal("CommitPromptComposer must reject invalid enum value")
	}

	f.ctrl.SetPromptSlotValue("tone", "简洁")
	f.ctrl.SetPromptSlotValue("points", "99")
	if prev := f.ctrl.PromptComposerPreview(); prev.RenderedText != "" {
		t.Fatalf("out-of-range integer must fail closed, got %q", prev.RenderedText)
	}
	f.ctrl.SetPromptSlotValue("points", "3")

	// 3. Valid zh-CN slots render live preview.
	pt.Frame()
	wantRendered := "请用简洁语气写出关于模板平台的3条要点。"
	if prev := f.ctrl.PromptComposerPreview(); prev.RenderedText != wantRendered {
		t.Fatalf("rendered zh-CN contract = %q, want %q", prev.RenderedText, wantRendered)
	}
	if !pt.HasText(wantRendered) {
		t.Fatalf("rendered zh-CN preview missing from UI: %v", pt.Texts())
	}

	// 4. ConfirmPromptComposerAI WITHOUT prior review request MUST fail closed (0 calls).
	if err := f.ctrl.ConfirmPromptComposerAI(context.Background()); err == nil {
		t.Fatal("ConfirmPromptComposerAI without review must fail closed")
	} else {
		var execErr *template.ExecutionError
		if !errors.As(err, &execErr) || execErr.Code != template.ExecutionReviewRequired {
			t.Fatalf("unexpected error = %v, want ExecutionReviewRequired", err)
		}
	}
	if len(execPrompts) != 0 {
		t.Fatalf("AI executor called without review: %d", len(execPrompts))
	}

	// 5. Clicking Send to AI arms one-shot review banner without calling AI yet.
	if err := pt.Click(i18n.T("prompts.contract.send_ai")); err != nil {
		t.Fatalf("click Send to AI: %v (texts=%v)", err, pt.Texts())
	}
	pt.Frame()
	if !f.ctrl.PromptComposerReviewPending() || !pt.HasText(i18n.T("prompts.contract.confirm_send")) {
		t.Fatalf("expected review confirmation banner armed, pending=%v texts=%v", f.ctrl.PromptComposerReviewPending(), pt.Texts())
	}
	if len(execPrompts) != 0 {
		t.Fatalf("arming review must not call AI executor, calls=%d", len(execPrompts))
	}

	// Editing any slot invalidates the armed review confirmation!
	f.ctrl.SetPromptSlotValue("topic", "更新后的主题")
	if f.ctrl.PromptComposerReviewPending() {
		t.Fatal("editing a slot must invalidate armed review confirmation")
	}

	// Re-arm review and click Confirm Send -> calls executor exactly once, then consumes confirmation.
	pt.Frame()
	if err := pt.Click(i18n.T("prompts.contract.send_ai")); err != nil {
		t.Fatalf("re-click Send to AI: %v", err)
	}
	pt.Frame()
	if err := pt.Click(i18n.T("prompts.contract.confirm_send")); err != nil {
		t.Fatalf("click Confirm Send: %v", err)
	}
	pt.Frame()
	if len(execPrompts) != 1 || execPrompts[0] != "请用简洁语气写出关于更新后的主题的3条要点。" {
		t.Fatalf("unexpected AI execution calls: %+v", execPrompts)
	}
	if f.ctrl.PromptComposerReviewPending() {
		t.Fatal("one-shot review confirmation must be cleared after execution")
	}
	// Second confirm without fresh review must fail closed (never remembered).
	if err := f.ctrl.ConfirmPromptComposerAI(context.Background()); err == nil || len(execPrompts) != 1 {
		t.Fatalf("second confirm must not reuse confirmation: err=%v calls=%d", err, len(execPrompts))
	}
}

func TestPromptComposerContractWithoutExecutePermissionDeniesAI(t *testing.T) {
	prevLang := i18n.Current()
	defer i18n.SetLanguage(prevLang)
	i18n.SetLanguage(i18n.English)

	f := newInteractionFixture(t, nil)
	copyOnlyContract := template.Contract{
		SchemaVersion: template.ContractSchema,
		SolutionID:    "en-copy-only",
		Locale:        "en",
		Permissions:   []string{"preview"}, // deliberately omits execute_requires_review
		Inputs:        []template.InputDefinition{{Name: "subject", Type: "string", Required: true}},
	}
	calls := 0
	f.ctrl.SetPromptComposerAIExecutor(template.AIExecutorFunc(func(_ context.Context, _ string) error {
		calls++
		return nil
	}))

	// OpenContractTemplate directly (without explicit body -> synthesizes placeholder body from Inputs).
	f.ctrl.OpenContractTemplate(copyOnlyContract, "Copy Only Template")
	f.ctrl.SetPromptSlotValue("subject", "release notes")

	if err := f.ctrl.RequestPromptComposerAIReview(); err == nil {
		t.Fatal("contract without execute_requires_review must deny AI review")
	} else {
		var execErr *template.ExecutionError
		if !errors.As(err, &execErr) || execErr.Code != template.ExecutionPermissionDenied {
			t.Fatalf("error = %v, want ExecutionPermissionDenied", err)
		}
	}
	if err := f.ctrl.ConfirmPromptComposerAI(context.Background()); err == nil || calls != 0 {
		t.Fatalf("contract without execute_requires_review must never call AI: err=%v calls=%d", err, calls)
	}

	// Copy still succeeds cleanly.
	var copied string
	f.ctrl.ClipboardWriter = func(s string) { copied = s }
	if !f.ctrl.CopyPromptComposer() {
		t.Fatal("CopyPromptComposer should succeed for valid preview-only contract")
	}
	if !strings.Contains(copied, "release notes") {
		t.Fatalf("copied text = %q, want 'release notes'", copied)
	}
}
