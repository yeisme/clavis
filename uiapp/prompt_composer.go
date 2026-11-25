package uiapp

import (
	"context"
	"errors"
	"maps"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/prompt"
	"github.com/yeisme/clavis/pkg/core/template"
)

type promptComposerUIState struct {
	rootHandle    ui.Handle
	slotHandles   []ui.Handle
	needFocusSync bool
	isComposing   bool
}

// promptContractState holds per-controller contract validation and one-shot
// AI execution review gate state for PromptComposer. Owned by controllerSubStates.
type promptContractState struct {
	mu               sync.Mutex
	active           bool
	contract         template.Contract
	diagnostics      []prompt.ContractDiagnostic
	reviewPending    bool
	reviewPromptHash string
	aiExecutor       template.AIExecutor
	aiRunStatus      string
	aiRunError       string
}

// PreferredPromptComposerPanelSize computes comfortable dimensions for the standalone
// prompt slot composer floating panel while honoring PreferredPromptComposerSize width bounds.
func PreferredPromptComposerPanelSize(slotCount int) (width, height int) {
	w, h := PreferredPromptComposerSize(slotCount, true)
	if w < 620 {
		w = 620
	}
	if slotCount < 1 {
		slotCount = 1
	}
	if slotCount > 6 {
		slotCount = 6
	}
	neededH := 280 + slotCount*56
	if neededH > h {
		h = neededH
	}
	if h > 560 {
		h = 560
	}
	return w, h
}

// OpenPromptComposer initializes a SlotComposerSession for tpl and triggers OnOpenPromptComposer.
func (c *AppController) OpenPromptComposer(tpl config.PromptTemplate, capCtx engine.CapsuleContext) {
	c.mu.Lock()
	glossary := c.Glossary
	c.mu.Unlock()

	var glossaryBlock string
	if tpl.InjectGlossary {
		terms := glossary.RelevantTerms(capCtx.SelectedText+"\n"+capCtx.WindowTitle+"\n"+tpl.Template, "", 0)
		glossaryBlock = glossary.FormatPromptBlock(terms)
	}

	renderCtx := prompt.RenderContext{
		Selection:        capCtx.SelectedText,
		Clipboard:        capCtx.ClipboardPreview,
		App:              capCtx.AppName,
		URL:              capCtx.ActiveURL,
		WindowTitle:      capCtx.WindowTitle,
		Glossary:         glossaryBlock,
		SelectedText:     capCtx.SelectedText,
		ClipboardPreview: capCtx.ClipboardPreview,
		AppName:          capCtx.AppName,
		ActiveURL:        capCtx.ActiveURL,
	}

	sess := prompt.NewSlotComposerSession(tpl, renderCtx)

	cs := c.promptContractStateFor()
	cs.mu.Lock()
	cs.active = false
	cs.contract = template.Contract{}
	cs.diagnostics = nil
	cs.reviewPending = false
	cs.reviewPromptHash = ""
	cs.aiRunStatus = ""
	cs.aiRunError = ""
	cs.mu.Unlock()

	c.mu.Lock()
	c.promptSession = sess
	c.promptContext = capCtx
	c.promptValidationError = ""
	c.promptUI.slotHandles = make([]ui.Handle, len(sess.Slots))
	c.promptUI.needFocusSync = true
	c.promptUI.isComposing = false
	onOpen := c.OnOpenPromptComposer
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if onOpen != nil {
		onOpen()
	}
	if onRepaint != nil {
		onRepaint()
	}
}

// OpenContractTemplate opens PromptComposer for a contract-backed template.
// If the template body is present in the local CAS (keyed by contract.TemplateDigest),
// it is loaded and verified from CAS; otherwise a deterministic placeholder body
// matching contract.Inputs is used so strict contract validation and review gating
// always operate fail-closed.
func (c *AppController) OpenContractTemplate(contract template.Contract, title string) {
	body := c.resolveContractBody(contract)
	c.OpenContractTemplateWithDefaults(contract, body, title, nil, engine.CapsuleContext{})
}

// OpenContractTemplateWithBody opens PromptComposer for a contract-backed template
// with an explicit verified body and optional capsule context.
func (c *AppController) OpenContractTemplateWithBody(contract template.Contract, body, title string, capCtx engine.CapsuleContext) {
	c.OpenContractTemplateWithDefaults(contract, body, title, nil, capCtx)
}

// OpenContractTemplateWithDefaults opens PromptComposer for a contract-backed
// template, applying optional overlay slot defaults over contract defaults.
func (c *AppController) OpenContractTemplateWithDefaults(
	contract template.Contract,
	body string,
	title string,
	slotDefaults map[string]template.SlotDefault,
	capCtx engine.CapsuleContext,
) {
	resolvedTitle := strings.TrimSpace(title)
	if resolvedTitle == "" {
		resolvedTitle = strings.TrimSpace(contract.SolutionID)
	}
	if resolvedTitle == "" {
		resolvedTitle = i18n.T("prompts.composer_title")
	}
	trigger := ""
	if strings.TrimSpace(contract.SolutionID) != "" {
		trigger = "/" + strings.TrimSpace(contract.SolutionID)
	}

	slots := prompt.PromptSlotsFromContract(contract)
	for i := range slots {
		if ov, ok := slotDefaults[slots[i].Name]; ok && ov.Default != nil {
			slots[i].Default = *ov.Default
		}
	}

	tpl := config.PromptTemplate{
		ID:       strings.TrimSpace(contract.SolutionID),
		Trigger:  trigger,
		Title:    resolvedTitle,
		Template: body,
		Slots:    slots,
	}

	renderCtx := prompt.RenderContext{
		Selection:        capCtx.SelectedText,
		Clipboard:        capCtx.ClipboardPreview,
		App:              capCtx.AppName,
		URL:              capCtx.ActiveURL,
		WindowTitle:      capCtx.WindowTitle,
		SelectedText:     capCtx.SelectedText,
		ClipboardPreview: capCtx.ClipboardPreview,
		AppName:          capCtx.AppName,
		ActiveURL:        capCtx.ActiveURL,
	}

	vals := make(map[string]string, len(slots))
	for _, sl := range slots {
		if sl.Default != "" {
			vals[sl.Name] = sl.Default
		}
	}
	sess := &prompt.SlotComposerSession{
		Template: tpl,
		Parsed:   prompt.ParseTemplate(body),
		Slots:    slots,
		Values:   vals,
		Context:  renderCtx,
	}

	cs := c.promptContractStateFor()
	cs.mu.Lock()
	cs.active = true
	cs.contract = contract
	cs.diagnostics = nil
	cs.reviewPending = false
	cs.reviewPromptHash = ""
	cs.aiRunStatus = ""
	cs.aiRunError = ""
	cs.mu.Unlock()

	c.mu.Lock()
	c.promptSession = sess
	c.promptContext = capCtx
	c.promptValidationError = ""
	c.promptUI.slotHandles = make([]ui.Handle, len(sess.Slots))
	c.promptUI.needFocusSync = true
	c.promptUI.isComposing = false
	onOpen := c.OnOpenPromptComposer
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if onOpen != nil {
		onOpen()
	}
	if onRepaint != nil {
		onRepaint()
	}
}

func (c *AppController) resolveContractBody(contract template.Contract) string {
	if digest := strings.TrimSpace(contract.TemplateDigest); digest != "" {
		if lib, err := template.OpenLibrary(c.TemplateLibraryDir()); err == nil {
			defer lib.Close()
			if raw, rerr := lib.ReadCASObject(digest); rerr == nil && len(raw) > 0 {
				return string(raw)
			}
		}
	}
	if len(contract.Inputs) == 0 {
		if strings.TrimSpace(contract.SolutionID) != "" {
			return strings.TrimSpace(contract.SolutionID)
		}
		return ""
	}
	lines := make([]string, 0, len(contract.Inputs))
	for _, in := range contract.Inputs {
		label := in.LocalizedLabel(contract.Locale)
		if strings.TrimSpace(label) == "" {
			label = in.Name
		}
		lines = append(lines, label+": {{"+in.Name+"}}")
	}
	return strings.Join(lines, "\n")
}

// ClosePromptComposer clears any active slot composer session and fires OnClosePromptComposer.
func (c *AppController) ClosePromptComposer() {
	cs := c.promptContractStateFor()
	cs.mu.Lock()
	cs.active = false
	cs.contract = template.Contract{}
	cs.diagnostics = nil
	cs.reviewPending = false
	cs.reviewPromptHash = ""
	cs.aiRunStatus = ""
	cs.aiRunError = ""
	cs.mu.Unlock()

	c.mu.Lock()
	c.promptSession = nil
	c.promptValidationError = ""
	c.promptUI.slotHandles = nil
	c.promptUI.needFocusSync = false
	c.promptUI.isComposing = false
	onClose := c.OnClosePromptComposer
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if onClose != nil {
		onClose()
	}
	if onRepaint != nil {
		onRepaint()
	}
}

// PromptComposerContractActive reports whether the active composer session is backed by a contract.
func (c *AppController) PromptComposerContractActive() bool {
	cs := c.promptContractStateFor()
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.active
}

// PromptComposerReviewPending reports whether one-shot AI review confirmation is currently armed.
func (c *AppController) PromptComposerReviewPending() bool {
	cs := c.promptContractStateFor()
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.active && cs.reviewPending
}

// SetPromptComposerAIExecutor injects a custom template.AIExecutor for contract AI execution
// (primarily for deterministic unit testing). Passing nil restores the default AI stream executor.
func (c *AppController) SetPromptComposerAIExecutor(exec template.AIExecutor) {
	cs := c.promptContractStateFor()
	cs.mu.Lock()
	cs.aiExecutor = exec
	cs.mu.Unlock()
}

// PromptComposerContractDiagnostics evaluates and returns the current contract diagnostics
// for the active session, or nil if the session is not contract-backed.
func (c *AppController) PromptComposerContractDiagnostics() []prompt.ContractDiagnostic {
	_, diags, _, isContract := c.evaluatePromptComposer()
	if !isContract {
		return nil
	}
	return diags
}

func contractDiagnosticsFatal(diags []prompt.ContractDiagnostic) bool {
	for _, d := range diags {
		if d.Code != "builtin_conflict" {
			return true
		}
	}
	return false
}

func formatContractDiagnosticsMessage(diags []prompt.ContractDiagnostic) string {
	parts := make([]string, 0, len(diags))
	for _, d := range diags {
		if d.Code == "builtin_conflict" {
			continue
		}
		if d.Name != "" {
			parts = append(parts, d.Code+":"+d.Name+" ("+d.Message+")")
		} else {
			parts = append(parts, d.Code+" ("+d.Message+")")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return i18n.Tf("prompts.contract.invalid_inputs", strings.Join(parts, "; "))
}

func (c *AppController) evaluatePromptComposer() (prompt.PromptRenderResult, []prompt.ContractDiagnostic, bool, bool) {
	c.mu.Lock()
	sess := c.promptSession
	if sess == nil {
		c.mu.Unlock()
		return prompt.PromptRenderResult{}, nil, false, false
	}
	tpl := sess.Template
	vals := maps.Clone(sess.Values)
	ctx := sess.Context
	c.mu.Unlock()

	cs := c.promptContractStateFor()
	cs.mu.Lock()
	active := cs.active
	contract := cs.contract
	cs.mu.Unlock()

	if !active {
		return prompt.RenderPrompt(tpl, vals, ctx), nil, false, false
	}

	res := prompt.RenderContractPrompt(contract, tpl.Template, vals, ctx)
	fatal := contractDiagnosticsFatal(res.Diagnostics)
	cs.mu.Lock()
	cs.diagnostics = append([]prompt.ContractDiagnostic(nil), res.Diagnostics...)
	cs.mu.Unlock()

	return prompt.PromptRenderResult{
		RenderedText:     res.RenderedText,
		CursorBackOffset: res.CursorBackOffset,
		MissingRequired:  append([]string(nil), res.MissingRequired...),
	}, res.Diagnostics, fatal, true
}

// RequestPromptComposerAIReview validates the contract template fail-closed and arms
// the one-shot human review gate before AI execution. It never invokes the AI executor.
func (c *AppController) RequestPromptComposerAIReview() error {
	res, diags, fatal, isContract := c.evaluatePromptComposer()
	if !isContract {
		return &template.ExecutionError{Code: template.ExecutionPermissionDenied, Message: "no active contract template"}
	}
	if fatal || strings.TrimSpace(res.RenderedText) == "" {
		msg := formatContractDiagnosticsMessage(diags)
		if msg == "" {
			msg = i18n.T("prompts.contract.preview_blocked")
		}
		c.setPromptValidationError(msg)
		return &template.ExecutionError{Code: template.ExecutionRenderFailed, Message: "template rendering failed"}
	}

	cs := c.promptContractStateFor()
	cs.mu.Lock()
	contract := cs.contract
	cs.mu.Unlock()

	if !contract.RequiresReview() {
		msg := i18n.T("prompts.contract.ai_denied")
		c.setPromptValidationError(msg)
		return &template.ExecutionError{
			Code:       template.ExecutionPermissionDenied,
			Permission: template.ExecuteRequiresReview,
			Message:    "template is not authorized for AI execution",
		}
	}

	hash := template.Digest([]byte(res.RenderedText))
	cs.mu.Lock()
	cs.reviewPending = true
	cs.reviewPromptHash = hash
	cs.aiRunError = ""
	cs.aiRunStatus = ""
	cs.mu.Unlock()

	c.mu.Lock()
	c.promptValidationError = ""
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// CancelPromptComposerAIReview cancels any pending one-shot AI review confirmation.
func (c *AppController) CancelPromptComposerAIReview() {
	cs := c.promptContractStateFor()
	cs.mu.Lock()
	cs.reviewPending = false
	cs.reviewPromptHash = ""
	cs.mu.Unlock()

	c.mu.Lock()
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
}

// ConfirmPromptComposerAI executes the rendered contract prompt through template.Contract.ExecuteAI
// strictly after an explicit one-shot review confirmation. Confirmation is consumed immediately
// and never persisted across calls.
func (c *AppController) ConfirmPromptComposerAI(ctx context.Context) error {
	if ctx == nil {
		ctx = c.lifeCtx()
	}
	res, diags, fatal, isContract := c.evaluatePromptComposer()
	if !isContract {
		return &template.ExecutionError{Code: template.ExecutionPermissionDenied, Message: "no active contract template"}
	}

	cs := c.promptContractStateFor()
	cs.mu.Lock()
	contract := cs.contract
	pending := cs.reviewPending
	armedHash := cs.reviewPromptHash
	customExec := cs.aiExecutor
	// Always consume one-shot confirmation immediately so it can never be reused.
	cs.reviewPending = false
	cs.reviewPromptHash = ""
	cs.mu.Unlock()

	if fatal || strings.TrimSpace(res.RenderedText) == "" {
		msg := formatContractDiagnosticsMessage(diags)
		if msg == "" {
			msg = i18n.T("prompts.contract.preview_blocked")
		}
		c.setPromptValidationError(msg)
		return &template.ExecutionError{Code: template.ExecutionRenderFailed, Message: "template rendering failed"}
	}

	confirmed := pending && armedHash != "" && armedHash == template.Digest([]byte(res.RenderedText))
	executor := customExec
	if executor == nil {
		executor = template.AIExecutorFunc(func(_ context.Context, renderedPrompt string) error {
			c.mu.Lock()
			capCtx := c.promptContext
			c.mu.Unlock()
			c.ClosePromptComposer()
			c.Dispatch(engine.Event{
				Type:    engine.EventOpenOmnibox,
				Tab:     config.TabAI,
				Context: &capCtx,
			})
			c.Dispatch(engine.Event{
				Type:    engine.EventStartAI,
				Mode:    "ask",
				Prompt:  renderedPrompt,
				Context: &capCtx,
			})
			return nil
		})
	}

	err := contract.ExecuteAI(ctx, res.RenderedText, confirmed, executor)
	if err != nil {
		var execErr *template.ExecutionError
		msg := i18n.Tf("prompts.contract.ai_failed", err.Error())
		if errors.As(err, &execErr) && execErr != nil && execErr.Code == template.ExecutionPermissionDenied {
			msg = i18n.T("prompts.contract.ai_denied")
		}
		cs.mu.Lock()
		cs.aiRunError = msg
		cs.aiRunStatus = ""
		cs.mu.Unlock()
		c.setPromptValidationError(msg)
		return err
	}

	profileName := c.ActiveAIProfile().Name
	if strings.TrimSpace(profileName) == "" {
		profileName = c.ActiveAIProfile().ID
	}
	status := i18n.Tf("prompts.contract.ai_sent", profileName)
	cs.mu.Lock()
	cs.aiRunStatus = status
	cs.aiRunError = ""
	cs.mu.Unlock()
	c.ShowToastOrHUD(status)
	return nil
}

func (c *AppController) setPromptValidationError(msg string) {
	c.mu.Lock()
	c.promptValidationError = msg
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	c.ShowToastOrHUD(msg)
	if onRepaint != nil {
		onRepaint()
	}
}

// PromptComposerOpen reports whether a slot composer session is active.
func (c *AppController) PromptComposerOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.promptSession != nil
}

// PromptComposerSlotCount returns the number of slots in the active session.
func (c *AppController) PromptComposerSlotCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.promptSession == nil {
		return 0
	}
	return len(c.promptSession.Slots)
}

// PromptComposerActiveSlotIndex returns the active slot index in the active session.
func (c *AppController) PromptComposerActiveSlotIndex() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.promptSession == nil {
		return 0
	}
	return c.promptSession.ActiveSlotIndex
}

// PromptComposerValidationError returns the current validation error message, if any.
func (c *AppController) PromptComposerValidationError() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.promptValidationError
}

// PromptComposerPreview returns the live-rendered preview of the active session.
func (c *AppController) PromptComposerPreview() prompt.PromptRenderResult {
	res, _, _, _ := c.evaluatePromptComposer()
	return res
}

// SetPromptSlotValue sets the value for the named slot and clears prior validation errors.
func (c *AppController) SetPromptSlotValue(name, value string) {
	cs := c.promptContractStateFor()
	cs.mu.Lock()
	cs.reviewPending = false
	cs.reviewPromptHash = ""
	cs.mu.Unlock()

	c.mu.Lock()
	if c.promptSession == nil {
		c.mu.Unlock()
		return
	}
	if c.promptSession.Values == nil {
		c.promptSession.Values = map[string]string{}
	}
	c.promptSession.Values[name] = value
	c.promptValidationError = ""
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if onRepaint != nil {
		onRepaint()
	}
}

// PromptComposerHandleKey routes Tab, Shift+Tab, Left, and Right into SlotComposerSession.
func (c *AppController) PromptComposerHandleKey(key string) {
	cs := c.promptContractStateFor()
	cs.mu.Lock()
	cs.reviewPending = false
	cs.reviewPromptHash = ""
	cs.mu.Unlock()

	c.mu.Lock()
	if c.promptSession == nil {
		c.mu.Unlock()
		return
	}
	prevIdx := c.promptSession.ActiveSlotIndex
	c.promptSession.HandleKey(key)
	if c.promptSession.ActiveSlotIndex != prevIdx {
		c.promptUI.needFocusSync = true
	}
	c.promptValidationError = ""
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if onRepaint != nil {
		onRepaint()
	}
}

// CommitPromptComposer validates required slots (and strict contract rules when contract-backed),
// and on success closes the composer and injects the rendered prompt at the caret into the frontmost app.
func (c *AppController) CommitPromptComposer() bool {
	res, diags, fatal, isContract := c.evaluatePromptComposer()

	c.mu.Lock()
	sess := c.promptSession
	if sess == nil {
		c.mu.Unlock()
		return false
	}
	if isContract {
		if fatal {
			msg := formatContractDiagnosticsMessage(diags)
			c.promptValidationError = msg
			onRepaint := c.OnRepaint
			c.mu.Unlock()
			c.ShowToastOrHUD(msg)
			if onRepaint != nil {
				onRepaint()
			}
			return false
		}
	} else if len(res.MissingRequired) > 0 {
		msg := i18n.Tf("prompts.missing_required", strings.Join(res.MissingRequired, ", "))
		c.promptValidationError = msg
		onRepaint := c.OnRepaint
		c.mu.Unlock()
		c.ShowToastOrHUD(msg)
		if onRepaint != nil {
			onRepaint()
		}
		return false
	}

	rendered := res.RenderedText
	backOffset := res.CursorBackOffset
	tpl := sess.Template
	c.promptSession = nil
	c.promptValidationError = ""
	c.promptUI.slotHandles = nil
	c.promptUI.needFocusSync = false
	c.promptUI.isComposing = false
	onClose := c.OnClosePromptComposer
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	cs := c.promptContractStateFor()
	cs.mu.Lock()
	cs.active = false
	cs.contract = template.Contract{}
	cs.diagnostics = nil
	cs.reviewPending = false
	cs.reviewPromptHash = ""
	cs.mu.Unlock()

	if onClose != nil {
		onClose()
	}
	c.injectTextAsync(rendered, 0, backOffset)
	promptID := strings.TrimSpace(tpl.ID)
	if promptID == "" {
		promptID = strings.TrimSpace(tpl.Title)
	}
	c.recordPromptVersion(promptID, tpl.Trigger, tpl.Template, rendered, tpl.OptimizerPreset)
	if onRepaint != nil {
		onRepaint()
	}
	return true
}

// CopyPromptComposer validates required slots (and strict contract rules when contract-backed),
// and on success closes the composer and copies the rendered prompt to the clipboard.
func (c *AppController) CopyPromptComposer() bool {
	res, diags, fatal, isContract := c.evaluatePromptComposer()

	c.mu.Lock()
	sess := c.promptSession
	if sess == nil {
		c.mu.Unlock()
		return false
	}
	if isContract {
		if fatal {
			msg := formatContractDiagnosticsMessage(diags)
			c.promptValidationError = msg
			onRepaint := c.OnRepaint
			c.mu.Unlock()
			c.ShowToastOrHUD(msg)
			if onRepaint != nil {
				onRepaint()
			}
			return false
		}
	} else if len(res.MissingRequired) > 0 {
		msg := i18n.Tf("prompts.missing_required", strings.Join(res.MissingRequired, ", "))
		c.promptValidationError = msg
		onRepaint := c.OnRepaint
		c.mu.Unlock()
		c.ShowToastOrHUD(msg)
		if onRepaint != nil {
			onRepaint()
		}
		return false
	}

	rendered := res.RenderedText
	c.promptSession = nil
	c.promptValidationError = ""
	c.promptUI.slotHandles = nil
	c.promptUI.needFocusSync = false
	c.promptUI.isComposing = false
	onClose := c.OnClosePromptComposer
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	cs := c.promptContractStateFor()
	cs.mu.Lock()
	cs.active = false
	cs.contract = template.Contract{}
	cs.diagnostics = nil
	cs.reviewPending = false
	cs.reviewPromptHash = ""
	cs.mu.Unlock()

	if onClose != nil {
		onClose()
	}
	c.writeClipboardText(rendered)
	c.ShowToastOrHUD(i18n.T("hud.copied"))
	if onRepaint != nil {
		onRepaint()
	}
	return true
}

// PromptComposerView renders the standalone slot-composer panel for the active prompt template.
func (a *AppController) PromptComposerView(c *ui.Context) {
	cfg := a.Config()
	theme := resolveAppearanceTheme(c, cfg.Main.Appearance.Theme)
	c.SetTheme(theme)
	prefs := c.Preferences()
	radius := NormalizeCornerRadius(cfg.Main.Appearance)
	profile := appMotionProfile(c, cfg.Main.Appearance)
	DisableAnimationsFor(c, profile)

	a.mu.Lock()
	sess := a.promptSession
	if sess == nil {
		a.mu.Unlock()
		ui.Box(c).Fill()
		return
	}
	tpl := sess.Template
	slots := append([]config.PromptSlot(nil), sess.Slots...)
	activeSlotIdx := sess.ActiveSlotIndex
	slotValues := maps.Clone(sess.Values)
	appCtx := a.promptContext
	validationErr := a.promptValidationError
	needFocusSync := a.promptUI.needFocusSync
	if len(a.promptUI.slotHandles) != len(slots) {
		a.promptUI.slotHandles = make([]ui.Handle, len(slots))
	}
	a.mu.Unlock()

	preview, contractDiags, contractFatal, contractActive := a.evaluatePromptComposer()
	cs := a.promptContractStateFor()
	cs.mu.Lock()
	contract := cs.contract
	reviewPending := cs.reviewPending
	cs.mu.Unlock()

	root := c.Root()
	if !prefs.ReduceTransparency {
		root.Radius(radius)
	}

	card := kitFloatingShell(c, theme, applyTextLocale(ui.Column(c).Fill().Radius(radius).Clip()), cfg.Main.Appearance)

	// Window-level primary+Enter commits from anywhere; Escape closes.
	c.OnWindowShortcut(primaryMod(), ui.KeyEnter, func() {
		a.CommitPromptComposer()
	})

	card.Children(func() {
		// Header: the shared dialog title bar. The template is the title, the
		// composer kind its secondary line; trigger and app are neutral meta.
		kitDialogHeader(c, theme, "wand.and.stars", tpl.Title, i18n.T("prompts.composer_title"), func() {
			if contractActive {
				kitMetaChip(c, theme, i18n.Tf("prompts.contract.badge", contract.Locale))
				if contract.RequiresReview() {
					kitStatusPill(c, theme, i18n.T("prompts.contract.ai_requires_review"), chipAccent)
				} else {
					kitStatusPill(c, theme, i18n.T("prompts.contract.ai_copy_insert_only"), chipNeutral)
				}
			}
			if tpl.Trigger != "" {
				kitMetaChip(c, theme, tpl.Trigger)
			}
			if appCtx.AppName != "" {
				kitMetaChip(c, theme, appCtx.AppName)
			}
		})

		body := kitDialogBody(c)
		if len(slots) == 0 {
			body.Bind(&a.promptUI.rootHandle).Focusable().AutoFocus().FocusRing(false)
			if needFocusSync {
				a.promptUI.rootHandle.RequestFocus(c)
				a.mu.Lock()
				a.promptUI.needFocusSync = false
				a.mu.Unlock()
			}
		}
		body.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind != ui.InputKeyDown || a.promptUI.isComposing {
				return false
			}
			switch {
			case ev.Key == ui.KeyEscape && ev.Mods == 0:
				a.ClosePromptComposer()
				return true
			case ev.Key == ui.KeyEnter && ev.Mods == 0:
				a.CommitPromptComposer()
				return true
			case ev.Key == ui.KeyTab && ev.Mods == 0:
				a.PromptComposerHandleKey("Tab")
				return true
			case ev.Key == ui.KeyTab && ev.Mods == ui.Shift:
				a.PromptComposerHandleKey("Shift+Tab")
				return true
			case ev.Key == ui.KeyLeft && ev.Mods == 0:
				a.PromptComposerHandleKey("Left")
				return true
			case ev.Key == ui.KeyRight && ev.Mods == 0:
				a.PromptComposerHandleKey("Right")
				return true
			}
			return false
		})

		body.Children(func() {
			if len(slots) == 0 {
				ui.Box(c).
					FillWidth().
					Padding(space10, space12).
					Radius(radius8).
					Background(theme.Surface).
					Border(1, withAlpha(theme.Border, alphaStrong)).
					Children(func() {
						ui.Text(c, i18n.T("prompts.no_slots")).FontSize(typeLabel).TextColor(theme.TextMuted)
					})
			} else {
				ui.Column(c).FillWidth().Gap(8).Children(func() {
					for i, slot := range slots {
						slotIdx := i
						sl := slot
						label := strings.TrimSpace(sl.Label)
						if label == "" {
							label = sl.Name
						}
						isActive := slotIdx == activeSlotIdx

						ui.Column(c).Key("prompt-slot:" + sl.Name).FillWidth().Gap(space4).Children(func() {
							// The active slot shows through its field's focus ring or
							// accent edge; the label row carries only the neutral
							// "required" tag (a lone accent tick there read as a caret).
							kitControlRow(c, theme, label, func() {
								if sl.Required {
									ui.Box(c).Padding(space1, space4).Radius(radius4).
										Background(tileFill(theme)).Border(space1, hairline(theme)).Children(func() {
										ui.Text(c, i18n.T("prompts.required_badge")).FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
									})
								}
							})

							if sl.Kind == "select" && len(sl.Options) > 0 {
								selRow := ui.Row(c).
									Bind(&a.promptUI.slotHandles[slotIdx]).
									Focusable().
									FocusRing(false).
									FillWidth().
									Gap(6).
									Padding(space4, space6).
									Radius(radius8).
									Background(theme.Surface)
								if isActive {
									selRow.Border(1, withAlpha(theme.Accent, alphaStrong))
								} else {
									selRow.Border(1, withAlpha(theme.Border, alphaStrong))
								}
								selRow.HandleInput(func(ev ui.InputEvent) bool {
									if ev.Kind != ui.InputKeyDown {
										return false
									}
									switch {
									case ev.Key == ui.KeyLeft && ev.Mods == 0:
										a.PromptComposerHandleKey("Left")
										return true
									case ev.Key == ui.KeyRight && ev.Mods == 0:
										a.PromptComposerHandleKey("Right")
										return true
									case ev.Key == ui.KeyTab && ev.Mods == 0:
										a.PromptComposerHandleKey("Tab")
										return true
									case ev.Key == ui.KeyTab && ev.Mods == ui.Shift:
										a.PromptComposerHandleKey("Shift+Tab")
										return true
									case ev.Key == ui.KeyEnter && ev.Mods == 0:
										a.CommitPromptComposer()
										return true
									case ev.Key == ui.KeyEscape && ev.Mods == 0:
										a.ClosePromptComposer()
										return true
									}
									return false
								})
								if !needFocusSync && selRow.Focused() && activeSlotIdx != slotIdx {
									a.mu.Lock()
									if a.promptSession != nil {
										a.promptSession.ActiveSlotIndex = slotIdx
									}
									a.mu.Unlock()
								}
								selRow.Children(func() {
									curVal := slotValues[sl.Name]
									for _, opt := range sl.Options {
										optVal := opt
										chosen := optVal == curVal
										pill := ui.ButtonBase(c).
											Key("opt:"+sl.Name+":"+optVal).
											Padding(space4, space10).
											Radius(radiusChip).
											FocusRing(false)
										if chosen {
											pill.Background(theme.Accent)
										} else {
											pill.Background(theme.Background).Border(1, withAlpha(theme.Border, alphaStrong))
										}
										pill.Children(func() {
											t := ui.Text(c, optVal).FontSize(typeLabel).SingleLine()
											if chosen {
												t.TextColor(theme.AccentText).FontWeight(600)
											} else {
												t.TextColor(theme.Text)
											}
										})
										if pill.Clicked() {
											a.mu.Lock()
											if a.promptSession != nil {
												a.promptSession.ActiveSlotIndex = slotIdx
											}
											a.mu.Unlock()
											a.SetPromptSlotValue(sl.Name, optVal)
											a.promptUI.slotHandles[slotIdx].RequestFocus(c)
										}
									}
								})
							} else {
								val := slotValues[sl.Name]
								in := ui.TextInput(c, &val).
									Bind(&a.promptUI.slotHandles[slotIdx]).
									Placeholder(i18n.Tf("prompts.slot_placeholder", label)).
									Label(label).
									FillWidth()
								if slotIdx == 0 {
									in.AutoFocus()
								}
								in.ObserveInput(func(ev ui.InputEvent) {
									switch ev.Kind {
									case ui.InputCompose:
										a.promptUI.isComposing = ev.Text != ""
									case ui.InputText, ui.InputCommand:
										a.promptUI.isComposing = false
									}
								})
								if !in.Focused() {
									a.promptUI.isComposing = false
								} else {
									a.promptUI.isComposing = in.Composing()
									if !needFocusSync && activeSlotIdx != slotIdx {
										a.mu.Lock()
										if a.promptSession != nil {
											a.promptSession.ActiveSlotIndex = slotIdx
										}
										a.mu.Unlock()
									}
								}
								in.OnTextKey(func(k ui.TextKey) bool {
									if a.promptUI.isComposing {
										switch k.Key {
										case ui.KeyEnter, ui.KeyTab, ui.KeyEscape:
											return true
										default:
											return false
										}
									}
									switch {
									case k.Key == ui.KeyTab && k.Mods == 0:
										a.PromptComposerHandleKey("Tab")
										return true
									case k.Key == ui.KeyTab && k.Mods == ui.Shift:
										a.PromptComposerHandleKey("Shift+Tab")
										return true
									case k.Key == ui.KeyEnter && k.Mods == 0:
										a.CommitPromptComposer()
										return true
									case k.Key == ui.KeyEscape && k.Mods == 0:
										a.ClosePromptComposer()
										return true
									}
									return false
								})
								if in.Changed() && val != slotValues[sl.Name] {
									a.SetPromptSlotValue(sl.Name, val)
								}
							}
						})
					}

					if needFocusSync {
						if activeSlotIdx >= 0 && activeSlotIdx < len(a.promptUI.slotHandles) {
							a.promptUI.slotHandles[activeSlotIdx].RequestFocus(c)
						}
						a.mu.Lock()
						a.promptUI.needFocusSync = false
						a.mu.Unlock()
					}
				})
			}

			if validationErr != "" {
				ui.Box(c).
					FillWidth().
					Padding(space8, space10).
					Radius(radius8).
					Background(withAlpha(theme.Danger, alphaFaint)).
					Border(space1, withAlpha(theme.Danger, alphaHalf)).
					Children(func() {
						ui.Text(c, validationErr).FontSize(typeLabel).FontWeight(600).TextColor(statusInk(theme, theme.Danger))
					})
			}

			if contractActive && reviewPending {
				prof := a.ActiveAIProfile()
				profLabel := strings.TrimSpace(prof.Name)
				if profLabel == "" {
					profLabel = strings.TrimSpace(prof.ID)
				}
				ui.Box(c).
					Key("prompt-contract-review-banner").
					FillWidth().
					Padding(space8, space10).
					Radius(radius8).
					Background(theme.Warning.Alpha(alphaSubtle)).
					Border(space1, theme.Warning.Alpha(alphaHalf)).
					Children(func() {
						ui.Column(c).FillWidth().Gap(space6).Children(func() {
							ui.Text(c, i18n.Tf("prompts.contract.review_banner", profLabel)).
								FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).Wrap()
							ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
								if ui.PrimaryButton(c, i18n.T("prompts.contract.confirm_send")).Key("prompt-contract-confirm-ai").Clicked() {
									_ = a.ConfirmPromptComposerAI(a.lifeCtx())
								}
								if ui.Button(c, i18n.T("prompts.contract.cancel_review")).Key("prompt-contract-cancel-review").Clicked() {
									a.CancelPromptComposerAIReview()
								}
							})
						})
					})
			}

			// Live-rendered preview is an inset kit card so the composer reads as
			// one elevated surface with a clearly separated output well.
			kitCard(c, theme, cardInset, func() {
				ui.Text(c, i18n.T("prompts.preview")).FontSize(typeMeta).FontWeight(700).TextColor(theme.TextMuted).SingleLine()
				if contractActive && contractFatal {
					ui.Text(c, i18n.T("prompts.contract.preview_blocked")).FontSize(typeLabel).TextColor(statusInk(theme, theme.Danger)).Wrap()
					for _, d := range contractDiags {
						if d.Code == "builtin_conflict" {
							continue
						}
						ui.Text(c, d.Error()).FontSize(typeMeta).TextColor(theme.TextMuted).Wrap()
					}
				} else {
					ui.Scroll(c).FillWidth().Grow(1).Children(func() {
						renderPreviewLines(c, theme, preview.RenderedText, 40)
					})
				}
			})
		})

		actions := []dialogAction{
			{Label: i18n.T("common.copy"), OnClick: func() { a.CopyPromptComposer() }},
		}
		if contractActive {
			actions = append(actions, dialogAction{
				Label:    i18n.T("prompts.contract.send_ai"),
				Key:      "prompt-contract-send-ai",
				Disabled: !contract.RequiresReview() || contractFatal,
				OnClick:  func() { _ = a.RequestPromptComposerAIReview() },
			})
		}
		actions = append(actions,
			dialogAction{Label: i18n.T("prompts.cancel"), OnClick: a.ClosePromptComposer},
			dialogAction{Label: i18n.T("prompts.insert"), Primary: true, OnClick: func() { a.CommitPromptComposer() }},
		)
		kitDialogFooter(c, theme, i18n.T("prompts.footer.hint"), actions...)
	})
}
