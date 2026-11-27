package uiapp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yeisme/clavis/internal/secrets"
	internalstorage "github.com/yeisme/clavis/internal/storage"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/prompt"
	runtimecoord "github.com/yeisme/clavis/pkg/core/runtime"
)

// AITestStatus tracks the state of the inline "Test Connection" button in Settings.
type AITestStatus string

const (
	AITestIdle    AITestStatus = "idle"
	AITestLoading AITestStatus = "loading"
	AITestSuccess AITestStatus = "success"
	AITestError   AITestStatus = "error"
)

// AIUsageDay is the UI projection of one daily token aggregate. The storage
// package supplies the same fields, but the UI keeps an injected reader seam so
// rendering tests never need a real database.
type AIUsageDay struct {
	Day                                 string
	Requests, InputTokens, OutputTokens int
}

// AIUsageDailyReader reads a bounded, oldest-first daily usage window.
// Implementations must perform I/O off the UI thread.
type AIUsageDailyReader func(context.Context, int) ([]AIUsageDay, error)

// LexiconHitDay is the UI projection of one daily lexicon hit/miss aggregate.
// The storage package supplies the same fields, while the UI keeps an injected
// reader seam so rendering tests never need a real database.
type LexiconHitDay struct {
	Day          string
	Hits, Misses int
}

// LexiconHitsDailyReader reads a bounded, oldest-first hit/miss window.
// Implementations must perform I/O off the UI thread.
type LexiconHitsDailyReader func(context.Context, int) ([]LexiconHitDay, error)

type LexiconTopWord struct {
	Word     string
	UseCount int
}

type LexiconTopWordsReader func(context.Context, int) ([]LexiconTopWord, error)

type lazyLearningStore struct {
	store clipboard.Store
	once  sync.Once
	ls    *internalstorage.LearningStore
	err   error
}

func (l *lazyLearningStore) get(unavailableMsg string) (*internalstorage.LearningStore, error) {
	sqliteStore, ok := l.store.(*internalstorage.SQLiteClipboardStore)
	if !ok || sqliteStore == nil {
		return nil, errors.New(unavailableMsg)
	}
	l.once.Do(func() {
		l.ls, l.err = internalstorage.NewLearningStore(sqliteStore.DB())
	})
	if l.err != nil {
		return nil, l.err
	}
	return l.ls, nil
}

func defaultAIUsageDailyReader(shared *lazyLearningStore) AIUsageDailyReader {
	return func(ctx context.Context, n int) ([]AIUsageDay, error) {
		learningStore, err := shared.get("AI usage storage is unavailable")
		if err != nil {
			return nil, err
		}
		days, err := learningStore.AIUsageDaily(ctx, n)
		if err != nil {
			return nil, err
		}
		out := make([]AIUsageDay, len(days))
		for i, day := range days {
			out[i] = AIUsageDay{Day: day.Day, Requests: day.Requests, InputTokens: day.InputTokens, OutputTokens: day.OutputTokens}
		}
		return out, nil
	}
}

func defaultLexiconTopWordsReader(shared *lazyLearningStore) LexiconTopWordsReader {
	return func(ctx context.Context, n int) ([]LexiconTopWord, error) {
		learningStore, err := shared.get("lexicon top words storage is unavailable")
		if err != nil {
			return nil, err
		}
		words, err := learningStore.LexiconTopWords(ctx, n)
		if err != nil {
			return nil, err
		}
		out := make([]LexiconTopWord, len(words))
		for i, word := range words {
			out[i] = LexiconTopWord{Word: word.Word, UseCount: word.UseCount}
		}
		return out, nil
	}
}

func defaultLexiconHitsDailyReader(shared *lazyLearningStore) LexiconHitsDailyReader {
	return func(ctx context.Context, n int) ([]LexiconHitDay, error) {
		learningStore, err := shared.get("lexicon hit storage is unavailable")
		if err != nil {
			return nil, err
		}
		days, err := learningStore.LexiconHitsDaily(ctx, n)
		if err != nil {
			return nil, err
		}
		out := make([]LexiconHitDay, len(days))
		for i, day := range days {
			out[i] = LexiconHitDay{Day: day.Day, Hits: day.Hits, Misses: day.Misses}
		}
		return out, nil
	}
}

// ActiveAIProfile returns the currently active AIProfile (honoring State.ActiveAIProfileID and config.toml).
func (c *AppController) ActiveAIProfile() config.AIProfile {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.activeAIProfileLocked()
}

func (c *AppController) activeAIProfileLocked() config.AIProfile {
	cfg := c.ConfigStore.Current()
	st := c.StateMachine.State()
	if id := strings.TrimSpace(st.ActiveAIProfileID); id != "" {
		if p, ok := cfg.Main.AI.ProfileByID(id); ok {
			if p.Protocol == "" {
				p.Protocol = config.ProtocolOpenAICompatible
			}
			return p
		}
	}
	return cfg.Main.AI.ActiveProfile()
}

// SelectAIProfile switches the active AI profile by ID, persists it to config.toml if writable,
// dispatches EventSelectAIProfile, and refreshes the API key source.
func (c *AppController) SelectAIProfile(id string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return errors.New("profile ID cannot be empty")
	}

	cfg := c.ConfigStore.Current()
	profile, ok := cfg.Main.AI.ProfileByID(trimmed)
	if !ok {
		return fmt.Errorf("unknown AI profile: %s", trimmed)
	}

	mainCfg := cfg.Main
	mainCfg.AI.ActiveProfileID = profile.ID
	mainCfg.AI.Provider = profile.Provider
	mainCfg.AI.Protocol = profile.Protocol
	mainCfg.AI.BaseURL = profile.BaseURL
	mainCfg.AI.Model = profile.Model
	mainCfg.AI.Temperature = profile.Temperature
	mainCfg.AI.TimeoutSeconds = profile.TimeoutSeconds
	mainCfg.AI.SystemPrompt = profile.SystemPrompt
	mainCfg.AI.KeychainAccount = profile.KeychainAccount
	mainCfg.AI.APIKeyEnvVar = profile.APIKeyEnvVar

	saveErr := c.ConfigStore.SaveMain(mainCfg)
	updatedCfg := c.ConfigStore.Current()

	c.mu.Lock()
	c.StateMachine.SetConfig(updatedCfg)
	_ = c.StateMachine.Handle(engine.Event{
		Type:      engine.EventSelectAIProfile,
		ProfileID: profile.ID,
	})
	if c.AIClient != nil {
		c.AIClient.Config = updatedCfg.Main.AI
	}
	c.aiTestStatus = AITestIdle
	c.aiTestMessage = ""
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if saveErr != nil {
		c.recordSaveFailure("ai", config.FileMain, "ai.active_profile_id", i18n.T("settings.ai.active_profile"), saveErr, func() {
			_ = c.SelectAIProfile(id)
		})
	} else {
		c.clearSaveFailure(config.FileMain, "ai.active_profile_id")
		c.setPageNotice("ai", i18n.Tf("settings.status.active_profile", profile.Name), chipNeutral)
	}
	c.refreshAPIKeySource()
	if onRepaint != nil {
		onRepaint()
	}
	return saveErr
}

func (c *AppController) resolveAPIKey(_ context.Context) (string, error) {
	profile := c.ActiveAIProfile()
	return secrets.ResolveProfileAPIKey(c.SecretStore, profile)
}

func formatAIError(err error, profile config.AIProfile) string {
	code := ai.ErrorCode(err)
	if code == ai.ErrorCodeMissingAPIKey {
		envVar := strings.TrimSpace(profile.APIKeyEnvVar)
		if envVar == "" {
			envVar = secrets.DefaultEnvVarForProvider(profile.Provider)
		}
		if envVar == "" {
			envVar = "API_KEY"
		}
		provider := strings.TrimSpace(profile.Provider)
		if provider == "" {
			provider = "AI"
		}
		return fmt.Sprintf("[%s] %s", code, i18n.Tf("prompts.ai_missing_key", provider, envVar))
	}
	message := ai.SafeErrorMessage(err)
	if message == "" {
		message = "AI request failed"
	}
	return fmt.Sprintf("[%s] %s", code, message)
}

func convertChatHistory(turns []engine.ChatTurn) []ai.ChatMessage {
	if len(turns) == 0 {
		return nil
	}
	out := make([]ai.ChatMessage, len(turns))
	for i, t := range turns {
		out[i] = ai.ChatMessage{
			Role:    t.Role,
			Content: t.Content,
		}
	}
	return out
}

type secureInputBridge interface {
	IsSecureInputEnabled() bool
}

// isSecureInputEnabled reports whether the host OS currently reports Secure Input active
// (e.g. a password field or Secure Keyboard Entry). It checks the optional IsSecureInputEnabled
// bridge method first, then falls back to PermissionStatus.Details["secureInput"].
func (c *AppController) isSecureInputEnabled() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	br := c.Bridge
	cached := c.permissionStatus
	c.mu.Unlock()
	if br != nil {
		if checker, ok := br.(secureInputBridge); ok {
			return checker.IsSecureInputEnabled()
		}
		ctx, cancel := context.WithTimeout(c.lifeCtx(), 150*time.Millisecond)
		defer cancel()
		if status, err := br.Permissions(ctx); err == nil {
			return strings.EqualFold(strings.TrimSpace(status.Details["secureInput"]), "true")
		}
	}
	return strings.EqualFold(strings.TrimSpace(cached.Details["secureInput"]), "true")
}

var (
	aiPEMPrivateKeyBlockRe  = regexp.MustCompile(`(?is)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`)
	aiPEMPrivateKeyHeaderRe = regexp.MustCompile(`(?is)-----BEGIN [^-]*PRIVATE KEY-----[\s\S]*$`)
	aiSecretTokenRe         = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_\-]{6,}|sk_(?:live|test)_[A-Za-z0-9_\-]{6,}|ghp_[A-Za-z0-9_]{6,}|github_pat_[A-Za-z0-9_]{6,}|xox[bp]-[A-Za-z0-9\-]{6,})`)
)

// scrubAutoAttachedSecrets removes obvious secret tokens and PEM private keys from
// automatically attached context fields unless the user explicitly typed that exact token.
func scrubAutoAttachedSecrets(text, userPrompt string) (string, bool) {
	if strings.TrimSpace(text) == "" {
		return text, false
	}
	changed := false
	replaceIfNotTyped := func(match string) string {
		if userPrompt != "" && strings.Contains(userPrompt, match) {
			return match
		}
		changed = true
		return "[REDACTED]"
	}
	out := aiPEMPrivateKeyBlockRe.ReplaceAllStringFunc(text, replaceIfNotTyped)
	out = aiPEMPrivateKeyHeaderRe.ReplaceAllStringFunc(out, replaceIfNotTyped)
	out = aiSecretTokenRe.ReplaceAllStringFunc(out, replaceIfNotTyped)
	return out, changed
}

func (c *AppController) startAIStream(eff engine.Effect) {
	cfg := c.ConfigStore.Current()
	profile := c.ActiveAIProfile()
	if id := strings.TrimSpace(eff.ProfileID); id != "" {
		if p, ok := cfg.Main.AI.ProfileByID(id); ok {
			profile = p
		}
	}

	timeoutSec := profile.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = cfg.Main.AI.TimeoutSeconds
	}
	timeout := time.Duration(timeoutSec * float64(time.Second))
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	regCtx, done := c.ensureRuntime().Begin(runtimecoord.AIStream, "AI stream")
	ctx, timeoutCancel := context.WithTimeout(regCtx, timeout)
	c.mu.Lock()
	brForOrigin := c.Bridge
	c.mu.Unlock()
	originBundle, originProc := resolveAIOriginTarget(ctx, brForOrigin, eff.Context)

	c.mu.Lock()
	if c.aiCancel != nil {
		c.aiCancel()
		if c.aiDone != nil {
			c.aiDone()
			c.aiDone = nil
		}
	}
	// A new AI surface supersedes any pending focused-field write-back.
	if eff.Mode != "custom" {
		c.transform = nil
	}
	transformMode := ""
	if c.transform != nil {
		transformMode = c.transform.mode
	}
	c.aiGen++
	gen := c.aiGen
	c.aiDone = done
	c.aiCancel = timeoutCancel
	c.aiOriginBundleID = originBundle
	c.aiOriginProcess = originProc
	hasClient := c.AIClient != nil
	var streamClient ai.OpenAICompatibleClient
	if hasClient {
		streamClient = *c.AIClient
	}
	secStore := c.SecretStore
	c.mu.Unlock()

	if !hasClient {
		timeoutCancel()
		done()
		c.Dispatch(engine.Event{
			Type:  engine.EventAIFailed,
			Error: i18n.T("settings.ai.not_configured"),
		})
		return
	}
	hasSensitiveContext := strings.TrimSpace(eff.SourceText) != "" || transformMode != "" ||
		(eff.Context != nil && (strings.TrimSpace(eff.Context.SelectedText) != "" ||
			strings.TrimSpace(eff.Context.ClipboardPreview) != "" ||
			strings.TrimSpace(eff.Context.SurroundingText) != ""))
	if hasSensitiveContext && c.isSecureInputEnabled() {
		timeoutCancel()
		done()
		c.Dispatch(engine.Event{
			Type:  engine.EventAIFailed,
			Error: pickLang(i18n.IsChinese(), "安全输入模式已开启，已阻止将选中文本或剪贴板内容发送至 AI", "Secure Input is active; sending selected or clipboard text to AI was blocked"),
		})
		return
	}
	var ocrImages []ai.ImageAttachment
	if eff.Mode == "ocr" {
		ocrImages = c.consumeOCRImages()
		if !profile.Capabilities.Vision {
			visProf, visErr := ai.ResolveVisionProfile(cfg.Main.AI)
			if visErr != nil {
				timeoutCancel()
				done()
				c.Dispatch(engine.Event{
					Type:  engine.EventAIFailed,
					Error: visErr.Error(),
				})
				return
			}
			profile = visProf
		}
	}

	streamClient.Config = cfg.Main.AI
	streamClient.APIKeyProvider = func(context.Context) (string, error) {
		return secrets.ResolveProfileAPIKey(secStore, profile)
	}
	history := convertChatHistory(eff.History)
	profCopy := profile

	var sanitizedCtx *engine.CapsuleContext
	if eff.Context != nil {
		ctxCopy := *eff.Context
		if ctxCopy.SelectedText != "" {
			ctxCopy.SelectedText, _ = scrubAutoAttachedSecrets(ctxCopy.SelectedText, eff.Prompt)
		}
		if ctxCopy.ClipboardPreview != "" {
			ctxCopy.ClipboardPreview, _ = scrubAutoAttachedSecrets(ctxCopy.ClipboardPreview, eff.Prompt)
		}
		if ctxCopy.SurroundingText != "" {
			ctxCopy.SurroundingText, _ = scrubAutoAttachedSecrets(ctxCopy.SurroundingText, eff.Prompt)
		}
		sanitizedCtx = &ctxCopy
	}

	req := ai.CompletionRequest{
		Mode:       eff.Mode,
		Prompt:     eff.Prompt,
		SourceText: eff.SourceText,
		Context:    sanitizedCtx,
		History:    history,
		Profile:    &profCopy,
		Images:     ocrImages,
	}
	if eff.Mode == "translate" && len(history) == 0 {
		dir := c.translateDirectionFor(eff.SourceText)
		sysPrompt, instr := engine.BuildTranslatePrompt(eff.SourceText, dir)
		req.SystemPromptOverride = sysPrompt
		if strings.TrimSpace(req.Prompt) == "" {
			req.Prompt = instr
		}
	}
	if eff.Mode == "custom" && transformMode == "optimize" {
		presetID := strings.TrimSpace(cfg.Prompts.DefaultOptimizerPreset)
		if presetID == "" {
			presetID = prompt.PresetCoding
		}
		optReq := prompt.BuildOptimizationRequest(eff.Prompt, presetID, "", cfg.Glossary.Terms, nil)
		req.Mode = "optimize"
		req.Prompt, req.SourceText, req.GlossaryTerms, req.SystemPromptOverride = optReq.Prompt, "", optReq.GlossaryTerms, optReq.SystemPromptOverride
	} else if eff.Mode == "optimize" {
		presetID := strings.TrimSpace(eff.OptimizerPreset)
		if presetID == "" {
			presetID = cfg.Prompts.DefaultOptimizerPreset
		}
		if len(history) == 0 {
			var ctxLines []string
			if sanitizedCtx != nil {
				if sanitizedCtx.AppName != "" {
					ctxLines = append(ctxLines, "App: "+sanitizedCtx.AppName)
				}
				if sanitizedCtx.WindowTitle != "" {
					ctxLines = append(ctxLines, "Window: "+sanitizedCtx.WindowTitle)
				}
				if sanitizedCtx.ActiveURL != "" {
					ctxLines = append(ctxLines, "URL: "+sanitizedCtx.ActiveURL)
				}
				if sanitizedCtx.SelectedText != "" {
					ctxLines = append(ctxLines, "Selection:\n"+sanitizedCtx.SelectedText)
				}
			}
			optReq := prompt.BuildOptimizationRequest(
				eff.Prompt,
				presetID,
				strings.Join(ctxLines, "\n"),
				cfg.Glossary.Terms,
				nil,
			)
			req.Prompt = optReq.Prompt
			req.SourceText = ""
			req.GlossaryTerms = optReq.GlossaryTerms
			req.SystemPromptOverride = optReq.SystemPromptOverride
		} else {
			req.GlossaryTerms = cfg.Glossary.Terms
			req.SystemPromptOverride = prompt.GetPreset(presetID).SystemPrompt
		}
	} else if (eff.Mode == "custom" && transformMode != "") || eff.Mode == "polish" || eff.Mode == "translate" || eff.Mode == "explain" {
		mode := eff.Mode
		if mode == "custom" {
			mode = transformMode
		}
		req.Mode = mode
		req.SourceText = eff.SourceText
		eff.Mode = mode
		if strings.TrimSpace(req.SystemPromptOverride) == "" {
			req.SystemPromptOverride = map[string]string{
				"polish":    "Rewrite the text for clarity, grammar, and flow while preserving its meaning. Return only the rewritten text.",
				"translate": "Translate the text between Chinese and English naturally. Return only the translation.",
				"explain":   "Explain the text clearly and concisely. Return only the explanation.",
			}[mode]
		}
	}

	go func() {
		defer done()
		defer timeoutCancel()
		err := streamClient.StreamCompletion(ctx, req, func(delta string) {
			if delta == "" {
				return
			}
			c.mu.Lock()
			active := c.aiGen == gen
			c.mu.Unlock()
			if !active {
				return
			}
			c.Dispatch(engine.Event{
				Type:  engine.EventAIChunk,
				Chunk: delta,
			})
		})

		c.mu.Lock()
		active := c.aiGen == gen
		c.mu.Unlock()
		if !active {
			return
		}

		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			errMsg := formatAIError(err, profCopy)
			c.Dispatch(engine.Event{
				Type:  engine.EventAIFailed,
				Error: errMsg,
			})
			return
		}

		c.invalidateAIUsage()
		c.Dispatch(engine.Event{
			Type: engine.EventAICompleted,
		})
	}()
}

func (c *AppController) cancelAIStream() {
	c.mu.Lock()
	c.aiGen++
	c.transform = nil
	cancel := c.aiCancel
	c.aiCancel = nil
	done := c.aiDone
	c.aiDone = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		done()
	}
}

// TestAIConnection sends a lightweight "Reply with OK" prompt for the currently active AI profile.
func (c *AppController) TestAIConnection(ctx context.Context) error {
	return c.TestAIConnectionForProfile(ctx, c.ActiveAIProfile().ID)
}

// TestAIConnectionForProfile sends a lightweight "Reply with OK" prompt for a specific AI profile ID.
func (c *AppController) TestAIConnectionForProfile(ctx context.Context, profileID string) error {
	if ctx == nil {
		ctx = c.lifeCtx()
	}
	ctx, cancel := context.WithCancel(ctx)
	unregister := c.RegisterCancellable(runtimecoord.AIConnectionTest, cancel, "AI connection test")
	defer unregister()
	defer cancel()
	cfg := c.ConfigStore.Current()
	profile := c.ActiveAIProfile()
	if trimmed := strings.TrimSpace(profileID); trimmed != "" {
		if p, ok := cfg.Main.AI.ProfileByID(trimmed); ok {
			profile = p
		}
	}

	c.mu.Lock()
	c.aiTestStatus = AITestLoading
	c.aiTestMessage = i18n.T("settings.ai.testing")
	hasClient := c.AIClient != nil
	var streamClient ai.OpenAICompatibleClient
	if hasClient {
		streamClient = *c.AIClient
	}
	secStore := c.SecretStore
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}

	if !hasClient {
		err := errors.New(i18n.T("settings.ai.not_configured"))
		c.setAITestResult(AITestError, err.Error())
		return err
	}
	streamClient.Config = cfg.Main.AI
	streamClient.APIKeyProvider = func(context.Context) (string, error) {
		return secrets.ResolveProfileAPIKey(secStore, profile)
	}
	profCopy := profile

	var reply strings.Builder
	err := streamClient.StreamCompletion(ctx, ai.CompletionRequest{
		Mode:    "test",
		Prompt:  "Reply with OK",
		Profile: &profCopy,
	}, func(delta string) {
		reply.WriteString(delta)
	})
	if err != nil {
		c.setAITestResult(AITestError, formatAIError(err, profCopy))
		return err
	}

	summary := strings.TrimSpace(reply.String())
	if len(summary) > 48 {
		summary = summary[:48] + "..."
	}
	if summary == "" {
		summary = "OK"
	}
	c.setAITestResult(AITestSuccess, i18n.Tf("settings.ai.connected", summary))
	return nil
}

func (c *AppController) setAITestResult(status AITestStatus, msg string) {
	c.mu.Lock()
	c.aiTestStatus = status
	c.aiTestMessage = msg
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
}

// SaveAPIKey stores apiKey in SecretStore under the active profile's keychain account without exposing it in UI text.
func (c *AppController) SaveAPIKey(apiKey string) error {
	if c.SecretStore == nil {
		return errors.New("secret store is not configured")
	}
	profile := c.ActiveAIProfile()
	acct := strings.TrimSpace(profile.KeychainAccount)
	if acct == "" {
		acct = c.ConfigStore.Current().Main.AI.KeychainAccount
	}
	if err := c.SecretStore.SaveKey(acct, apiKey); err != nil {
		c.setPageNotice("ai", i18n.Tf("settings.status.save_api_key_failed", err), chipWarning)
		return err
	}
	c.refreshAPIKeySource()
	c.mu.Lock()
	c.uiState.apiKeyDraft = ""
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	c.setPageNotice("ai", i18n.T("settings.status.api_key_saved"), chipSuccess)
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// DeleteAPIKey removes the stored API key for the active profile's keychain account.
func (c *AppController) DeleteAPIKey() error {
	if c.SecretStore == nil {
		return errors.New("secret store is not configured")
	}
	profile := c.ActiveAIProfile()
	acct := strings.TrimSpace(profile.KeychainAccount)
	if acct == "" {
		acct = c.ConfigStore.Current().Main.AI.KeychainAccount
	}
	if err := c.SecretStore.DeleteKey(acct); err != nil && !errors.Is(err, secrets.ErrSecretNotFound) {
		c.setPageNotice("ai", i18n.Tf("settings.status.delete_api_key_failed", err), chipWarning)
		return err
	}
	c.refreshAPIKeySource()
	c.mu.Lock()
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	c.setPageNotice("ai", i18n.T("settings.status.api_key_cleared"), chipNeutral)
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// APIKeyStatus returns the current masked key source (keychain / env / none) for the active AI profile.
func (c *AppController) APIKeyStatus() secrets.APIKeySource {
	c.refreshAPIKeySource()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.apiKeySource
}

// refreshAPIKeySource re-resolves where the active AI profile's key comes
// from (keychain, environment or none) and reports whether that changed.
// Callers must not hold c.mu: the keychain is read without it. On the main
// thread MyGo's Secrets.Get waits in a nested event loop, whose redraws
// take c.mu; read under the lock, Settings' first frame held it while the
// first-run guide's frame waited for it, and Clavis hung at launch. A result
// for a profile that is no longer active is dropped.
func (c *AppController) refreshAPIKeySource() bool {
	c.mu.Lock()
	store := c.SecretStore
	profile := c.activeAIProfileLocked()
	c.mu.Unlock()

	src := secrets.KeySourceNone
	if store != nil {
		if s, err := secrets.ResolveProfileAPIKeySource(store, profile); err == nil {
			src = s
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.activeAIProfileLocked() != profile || c.apiKeySource == src {
		return false
	}
	c.apiKeySource = src
	return true
}
