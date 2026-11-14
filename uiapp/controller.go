package uiapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/yeisme/clavis/internal/apps"
	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/actions"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// SettingsTab identifies one of the 5 native Settings tabs.
type SettingsTab int

const (
	SettingsTabGeneral SettingsTab = iota
	SettingsTabAI
	SettingsTabIMEKeymap
	SettingsTabPermissions
	SettingsTabConfigs
)

// SettingsTabNames returns the ordered display names for the 5 Settings tabs.
func SettingsTabNames() []string {
	return []string{
		i18n.T("settings.tab.general"),
		i18n.T("settings.tab.ai"),
		i18n.T("settings.tab.ime"),
		i18n.T("settings.tab.permissions"),
		i18n.T("settings.tab.configs"),
	}
}

// AITestStatus tracks the state of the inline "Test Connection" button in Settings.
type AITestStatus string

const (
	AITestIdle    AITestStatus = "idle"
	AITestLoading AITestStatus = "loading"
	AITestSuccess AITestStatus = "success"
	AITestError   AITestStatus = "error"
)

// Options configures an AppController.
type Options struct {
	ConfigStore     *config.ConfigStore
	ClipboardStore  clipboard.Store
	SecretStore     secrets.APIKeyStore
	Bridge          bridge.HostService
	AppProvider     apps.AppProvider
	HTTPDoer        ai.HTTPDoer
	ClipboardWriter func(text string)
	BeforeInject    func(ctx context.Context)
	OpenPath        func(path string) error
	OnRepaint       func()
	OnPresentPhase  func(phase engine.CapsulePhase)
	OnHide          func()
	OnOpenSettings  func(tab SettingsTab)
	OnShowIMEPill   func(label string, anchor *host.CursorAnchor)
	// OnLanguageChange fires when a config save or reload switches the UI
	// language, so the shell relabels the tray and window titles.
	OnLanguageChange func()
}

type clipTask struct {
	gen uint64
	fn  func(ctx context.Context) error
}

// uiViewState holds UI-thread widget handles and ephemeral view state.
type uiViewState struct {
	searchHandle           ui.Handle
	leaderHandle           ui.Handle
	followUpHandle         ui.Handle
	listState              ui.ListState
	mdOpts                 markdown.Options
	isComposing            bool
	suppressNextLeaderText string
	lastPhase              engine.CapsulePhase
	lastTab                config.CapsuleTab
	lastSelectedIdx        int
	sourceExpanded         bool
	followUpQuery          string
	apiKeyDraft            string
	settingsTabIdx         int
	imeRuleTargetDraft     string
	imeRuleURLDraft        string
	imeRuleTitleDraft      string
	imeRuleSourceDraft     string
	imeRuleModeDraft       config.IMEMode

	// Animation bookkeeping (view-side, main-thread only).
	lastExitHideDelay  time.Duration
	lastAIError        string
	aiShakeRun         uint64
	lastPreviewSnippet string
	snippetFlashRun    uint64
}

// AppController coordinates Clavis state, host integration, AI streaming,
// clipboard persistence, and native MyGo UI views.
type AppController struct {
	mu sync.Mutex

	ConfigStore    *config.ConfigStore
	StateMachine   *engine.StateMachine
	ClipboardStore clipboard.Store
	SecretStore    secrets.APIKeyStore
	Bridge         bridge.HostService
	AppProvider    apps.AppProvider
	AIClient       *ai.OpenAICompatibleClient
	CLIExecutor    actions.CLIExecutor
	Glossary       engine.GlossaryEngine
	Snippets       *engine.SnippetEngine

	httpDoer         ai.HTTPDoer
	ClipboardWriter  func(text string)
	BeforeInject     func(ctx context.Context)
	OpenPath         func(path string) error
	OnRepaint        func()
	OnPresentPhase   func(phase engine.CapsulePhase)
	OnHide           func()
	OnOpenSettings   func(tab SettingsTab)
	OnShowIMEPill    func(label string, anchor *host.CursorAnchor)
	OnLanguageChange func()

	// Decoded application icon bitmap cache
	iconMu       sync.Mutex
	appIconCache map[string]*ui.Bitmap

	// Leader temporary ASCII IME lock state
	preLeaderIMESourceID string
	imeLockedForCapsule  bool
	skipIMERestore       bool

	// Keymap hold layer reported by the host bridge (e.g. "nav", "hyper", "")
	activeKeymapLayer string

	// AI cancellation & generation guard
	aiCancel context.CancelFunc
	aiGen    uint64

	// HUD auto-dismiss timer & generation guard
	hudTimer *time.Timer
	hudGen   uint64

	// Serialized FIFO clipboard mutation queue + generation guard
	clipMu     sync.Mutex
	clipCond   *sync.Cond
	clipQueue  []clipTask
	clipClosed bool
	clipDone   chan struct{}
	clipGen    atomic.Uint64

	// Runtime diagnostics & settings status
	apiKeySource      secrets.APIKeySource
	permissionStatus  host.PermissionStatus
	monitorRunning    bool
	recoveryCount     int
	activeInputSource host.InputSourceInfo
	lastIMEError      string
	cachedBundleID    string
	cachedProcessName string
	cachedAppName     string
	settingsStatus    string
	aiTestStatus      AITestStatus
	aiTestMessage     string

	// Ephemeral UI state (used on the UI build thread)
	uiState uiViewState
}

// NewAppController constructs and initializes an AppController.
func NewAppController(opts Options) *AppController {
	cfgStore := opts.ConfigStore
	if cfgStore == nil {
		cfgDir := defaultConfigDir()
		cfgStore = config.NewConfigStore(cfgDir)
	}
	cfg := cfgStore.Current()

	clipStore := opts.ClipboardStore
	if clipStore == nil {
		clipStore = clipboard.NewMemoryStore(clipboard.NewPrivacyFilterFromConfig(cfg.Main.Clipboard))
	}

	secStore := opts.SecretStore
	if secStore == nil {
		secStore = secrets.NewMemoryAPIKeyStore()
	}

	appProv := opts.AppProvider
	if appProv == nil {
		appProv = apps.NewMyGoAppProvider()
	}

	glossary := engine.NewGlossaryEngineFromConfig(cfg.Glossary)
	snippets := engine.NewSnippetEngineFromConfig(cfg.Snippets)
	sm := engine.NewStateMachine(cfg)

	c := &AppController{
		ConfigStore:      cfgStore,
		StateMachine:     sm,
		ClipboardStore:   clipStore,
		SecretStore:      secStore,
		Bridge:           opts.Bridge,
		AppProvider:      appProv,
		CLIExecutor:      actions.NewCLIExecutor(),
		Glossary:         glossary,
		Snippets:         snippets,
		httpDoer:         opts.HTTPDoer,
		ClipboardWriter:  opts.ClipboardWriter,
		BeforeInject:     opts.BeforeInject,
		OpenPath:         opts.OpenPath,
		OnRepaint:        opts.OnRepaint,
		OnPresentPhase:   opts.OnPresentPhase,
		OnHide:           opts.OnHide,
		OnOpenSettings:   opts.OnOpenSettings,
		OnShowIMEPill:    opts.OnShowIMEPill,
		OnLanguageChange: opts.OnLanguageChange,
		appIconCache:     make(map[string]*ui.Bitmap),
		clipDone:         make(chan struct{}),
		aiTestStatus:     AITestIdle,
		uiState: uiViewState{
			imeRuleModeDraft: config.IMEModeASCII,
		},
	}
	c.clipCond = sync.NewCond(&c.clipMu)

	c.AIClient = ai.NewOpenAICompatibleClient(
		cfg.Main.AI,
		glossary,
		opts.HTTPDoer,
		c.resolveAPIKey,
	)

	c.refreshAPIKeySourceLocked()
	go c.runClipboardWorker()
	return c
}

func defaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".clavis"
	}
	return filepath.Join(home, ".config", "clavis")
}

// Close stops background workers, cancels any running AI request, and stops HUD timers.
func (c *AppController) Close() {
	c.mu.Lock()
	if c.aiCancel != nil {
		c.aiCancel()
		c.aiCancel = nil
	}
	c.aiGen++
	if c.hudTimer != nil {
		c.hudTimer.Stop()
		c.hudTimer = nil
	}
	c.hudGen++
	c.mu.Unlock()

	c.clipMu.Lock()
	if !c.clipClosed {
		c.clipClosed = true
		c.clipCond.Broadcast()
		c.clipMu.Unlock()
		<-c.clipDone
	} else {
		c.clipMu.Unlock()
	}
}

// Start starts the host bridge (if configured), syncs installed applications, and refreshes initial permissions.
func (c *AppController) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	// Installed-app discovery can touch slow disks (/Applications, registry); run it off the
	// main thread so startup stays responsive. SyncInstalledApps itself remains synchronous
	// whenever callers invoke it directly (tests rely on that).
	if len(c.State().InstalledApps) == 0 && c.AppProvider != nil {
		go c.SyncInstalledApps(ctx)
	}
	if c.Bridge == nil {
		return nil
	}
	if err := c.Bridge.Start(ctx, c.HandleTriggerEvent); err != nil {
		return err
	}
	c.mu.Lock()
	c.monitorRunning = true
	c.mu.Unlock()

	_, _ = c.RefreshPermissions(ctx)
	return nil
}

// Stop stops the host bridge.
func (c *AppController) Stop() error {
	c.mu.Lock()
	c.monitorRunning = false
	br := c.Bridge
	c.mu.Unlock()
	if br != nil {
		return br.Stop()
	}
	return nil
}

// State returns a snapshot of the current Capsule State.
func (c *AppController) State() engine.State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.StateMachine.State()
}

// Config returns the active ClavisConfig.
func (c *AppController) Config() config.ClavisConfig {
	return c.ConfigStore.Current()
}

// SyncInstalledApps queries AppProvider.ListApps and dispatches EventSetInstalledApps.
func (c *AppController) SyncInstalledApps(ctx context.Context) {
	c.mu.Lock()
	prov := c.AppProvider
	c.mu.Unlock()
	if prov == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	appsList, err := prov.ListApps(ctx)
	if err != nil || len(appsList) == 0 {
		return
	}
	c.Dispatch(engine.Event{
		Type:          engine.EventSetInstalledApps,
		InstalledApps: appsList,
	})
}

func (c *AppController) ensureInstalledAppsSyncedLocked() {
	if len(c.StateMachine.State().InstalledApps) > 0 || c.AppProvider == nil {
		return
	}
	prov := c.AppProvider
	c.mu.Unlock()
	appsList, err := prov.ListApps(context.Background())
	c.mu.Lock()
	if err == nil && len(appsList) > 0 && len(c.StateMachine.State().InstalledApps) == 0 {
		_ = c.StateMachine.Handle(engine.Event{
			Type:          engine.EventSetInstalledApps,
			InstalledApps: appsList,
		})
	}
}

// AppIconBitmap decodes and caches the 48px PNG application icon for appPath via AppProvider.
func (c *AppController) AppIconBitmap(appPath string) *ui.Bitmap {
	trimmed := strings.TrimSpace(appPath)
	if trimmed == "" {
		return nil
	}

	c.iconMu.Lock()
	if bmp, ok := c.appIconCache[trimmed]; ok {
		c.iconMu.Unlock()
		return bmp
	}
	c.iconMu.Unlock()

	c.mu.Lock()
	prov := c.AppProvider
	c.mu.Unlock()
	if prov == nil {
		return nil
	}

	pngBytes, err := prov.IconPNG(trimmed, 48)
	if err != nil || len(pngBytes) == 0 {
		return nil
	}
	bmp, err := ui.DecodeBitmap(pngBytes)
	if err != nil || bmp == nil {
		return nil
	}

	c.iconMu.Lock()
	if len(c.appIconCache) >= 256 {
		clear(c.appIconCache)
	}
	c.appIconCache[trimmed] = bmp
	c.iconMu.Unlock()

	return bmp
}

// Dispatch reduces an event on the CapsuleStateMachine and executes any emitted effects.
func (c *AppController) Dispatch(ev engine.Event) []engine.Effect {
	c.mu.Lock()
	if ev.Type == engine.EventOpenLeader || ev.Type == engine.EventOpenOmnibox {
		c.ensureInstalledAppsSyncedLocked()
	}
	prev := c.StateMachine.State()
	// A HUD notice must never destroy an active Capsule session. When a floating pill sink is
	// wired up and the Capsule is open (Leader / Omnibox / AI Streaming), route the notice
	// to the non-activating pill and leave the session intact. PhaseHUD still updates in place.
	if ev.Type == engine.EventShowHUD &&
		prev.Phase != engine.PhaseHidden &&
		prev.Phase != engine.PhaseHUD &&
		c.OnShowIMEPill != nil {
		pillCb := c.OnShowIMEPill
		onRepaint := c.OnRepaint
		c.mu.Unlock()
		pillCb(ev.Message, nil)
		if onRepaint != nil {
			onRepaint()
		}
		return nil
	}
	effects := c.StateMachine.Handle(ev)
	if ev.Type == engine.EventSelectAIProfile {
		c.refreshAPIKeySourceLocked()
	}
	next := c.StateMachine.State()
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if prev.Phase != engine.PhaseLeader && next.Phase == engine.PhaseLeader {
		c.maybeLockASCIIForLeader()
	}

	for _, eff := range effects {
		if eff.Type == engine.EffectRunAction && eff.Action != nil && eff.Action.Type == config.ActionToggleIME {
			c.mu.Lock()
			c.skipIMERestore = true
			c.imeLockedForCapsule = false
			c.preLeaderIMESourceID = ""
			c.mu.Unlock()
		}
	}

	// Any Leader exit (dismiss, tab switch, `/` downgrade to Omnibox, unmatched-key downgrade)
	// restores the user's native/Chinese IME so typing works again immediately downstream.
	if prev.Phase == engine.PhaseLeader && next.Phase != engine.PhaseLeader {
		c.maybeRestoreLockedIME()
	}

	for _, eff := range effects {
		if eff.Type == engine.EffectCancelAI || eff.Type == engine.EffectClose {
			c.executeEffect(eff, next)
		}
	}
	for _, eff := range effects {
		if eff.Type != engine.EffectCancelAI && eff.Type != engine.EffectClose {
			c.executeEffect(eff, next)
		}
	}

	if onRepaint != nil && (len(effects) > 0 || stateChanged(prev, next)) {
		onRepaint()
	}
	return effects
}

// ShowToastOrHUD surfaces a transient notice without destroying an active Capsule session.
// When the Capsule is open and a floating pill sink is wired up, the notice renders in the
// non-activating pill instead of dispatching EventShowHUD (which clears phase fields and would
// wipe an active Omnibox query, Leader path, or AI streaming session).
func (c *AppController) ShowToastOrHUD(message string) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return
	}

	c.mu.Lock()
	phase := c.StateMachine.State().Phase
	pillCb := c.OnShowIMEPill
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	cfg := c.ConfigStore.Current()
	capsuleOpen := phase != engine.PhaseHidden
	if pillCb != nil && (capsuleOpen || cfg.Main.IME.ShowFloatingPill) {
		pillCb(msg, nil)
		if onRepaint != nil {
			onRepaint()
		}
		return
	}

	c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: msg})
}

func stateChanged(a, b engine.State) bool {
	return a.Phase != b.Phase ||
		a.OmniboxTab != b.OmniboxTab ||
		a.OmniboxQuery != b.OmniboxQuery ||
		a.SelectedIndex != b.SelectedIndex ||
		len(a.OmniboxItems) != len(b.OmniboxItems) ||
		len(a.LeaderPath) != len(b.LeaderPath) ||
		len(a.LeaderNodes) != len(b.LeaderNodes) ||
		a.AIOutput != b.AIOutput ||
		a.AIComplete != b.AIComplete ||
		a.AIError != b.AIError ||
		a.ActiveAIProfileID != b.ActiveAIProfileID ||
		len(a.AIHistory) != len(b.AIHistory) ||
		len(a.InstalledApps) != len(b.InstalledApps) ||
		a.HUDMessage != b.HUDMessage
}

func (c *AppController) executeEffect(eff engine.Effect, state engine.State) {
	switch eff.Type {
	case engine.EffectPresentCapsule:
		c.handlePresentCapsule(state.Phase)

	case engine.EffectClose:
		c.stopHUDTimer()
		c.maybeRestoreLockedIME()
		c.mu.Lock()
		onHide := c.OnHide
		c.mu.Unlock()
		if onHide != nil {
			onHide()
		}

	case engine.EffectStartAI:
		c.startAIStream(eff)

	case engine.EffectCancelAI:
		c.cancelAIStream()

	case engine.EffectApplyAIOutput:
		c.injectTextAsync(eff.Text, 0, 0)

	case engine.EffectPasteClipboardItem:
		c.injectTextAsync(eff.ClipboardContent, 0, 0)

	case engine.EffectCopyText:
		c.writeClipboardText(eff.Text)
		c.Dispatch(engine.Event{
			Type:    engine.EventShowHUD,
			Message: i18n.T("hud.copied"),
		})

	case engine.EffectPinClipboardItem:
		id := eff.ClipboardID
		pinned := eff.IsPinned
		c.enqueueClipboardTask(func(ctx context.Context) error {
			return c.ClipboardStore.Pin(ctx, id, pinned)
		})

	case engine.EffectDeleteClipboard:
		id := eff.ClipboardID
		c.enqueueClipboardTask(func(ctx context.Context) error {
			return c.ClipboardStore.Delete(ctx, id)
		})

	case engine.EffectRunAction:
		if eff.Action != nil {
			var actionCtx engine.CapsuleContext
			if eff.Context != nil {
				actionCtx = *eff.Context
			} else {
				actionCtx = state.Context
			}
			c.runAction(*eff.Action, actionCtx)
		}

	case engine.EffectBindAppIMERule:
		c.bindAppIMERule(eff, state)
	}
}

// bindAppIMERule persists (or removes) the per-app IME auto-switch rule requested by the Omnibox.
func (c *AppController) bindAppIMERule(eff engine.Effect, state engine.State) {
	appCtx := state.Context
	if eff.Context != nil {
		appCtx = *eff.Context
	}
	bundleID := strings.TrimSpace(appCtx.BundleID)
	processName := strings.TrimSpace(appCtx.ProcessName)
	appName := strings.TrimSpace(appCtx.AppName)
	if bundleID == "" && processName == "" {
		c.ShowToastOrHUD(i18n.T("hud.no_active_app"))
		return
	}
	if appName == "" {
		if bundleID != "" {
			appName = bundleID
		} else {
			appName = processName
		}
	}
	label := appName
	if trimmed := strings.TrimSpace(appCtx.AppName); trimmed != "" {
		label = trimmed
	}

	cfg := c.ConfigStore.Current()
	mode := eff.IMEMode
	if isClavisProcess(bundleID, processName) {
		c.ShowToastOrHUD(i18n.T("hud.no_active_app"))
		return
	}
	kept := buildAppIMERules(cfg.IMERules.Rules, bundleID, processName, appName, mode, cfg.Main.IME.DefaultInputSourceID)

	rulesCfg := config.IMERulesConfig{Rules: kept}
	if err := c.ConfigStore.SaveIMERules(rulesCfg); err != nil {
		c.ShowToastOrHUD(fmt.Sprintf("Failed to save IME rule: %v", err))
		return
	}

	var notice string
	if mode == "" {
		notice = i18n.Tf("hud.ime_unbound", label)
	} else {
		notice = i18n.Tf("hud.ime_bound", label, strings.ToUpper(string(mode)))
	}

	updated := c.ConfigStore.Current()
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	c.settingsStatus = notice
	br := c.Bridge
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if br != nil {
		br.UpdateIMEConfig(updated.Main.IME, updated.IMERules)
		if mode != "" {
			if err := br.SetIMEMode(context.Background(), mode); err != nil && !errors.Is(err, errors.ErrUnsupported) {
				c.NotifyIMESwitchFailed(err)
			}
		}
	}
	c.ShowToastOrHUD(notice)
	if onRepaint != nil {
		onRepaint()
	}
}

// imeRuleTargetsApp reports whether rule is a plain per-app rule (no URL/title pattern)
// for the given bundle ID (preferred) or process name.
func imeRuleTargetsApp(rule config.IMERule, bundleID, processName string) bool {
	if strings.TrimSpace(rule.URLPattern) != "" || strings.TrimSpace(rule.TitlePattern) != "" {
		return false
	}
	if bundleID != "" {
		return strings.TrimSpace(rule.BundleID) == bundleID
	}
	if processName != "" {
		return strings.TrimSpace(rule.BundleID) == "" && normalizeRuleProcess(rule.ProcessName) == normalizeRuleProcess(processName)
	}
	return false
}

// normalizeRuleProcess compares Windows process names case-insensitively with ".exe" optional,
// matching the bridge's rule matcher so re-binding "code.exe" replaces an existing "Code.exe" rule.
func normalizeRuleProcess(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	return strings.TrimSuffix(n, ".exe")
}

func isClavisProcess(bundleID, processName string) bool {
	return strings.EqualFold(strings.TrimSpace(bundleID), "com.yeisme.clavis") || normalizeRuleProcess(processName) == "clavis"
}

// buildAppIMERules replaces any app-scoped rule for (bundleID|processName) with a new rule for
// mode, or only removes it when mode is empty. On Windows bundleID is empty, so the stored rule
// targets process_name (e.g. "Code.exe"), which the bridge matches against the foreground exe.
func buildAppIMERules(rules []config.IMERule, bundleID, processName, appName string, mode config.IMEMode, asciiSourceID string) []config.IMERule {
	kept := make([]config.IMERule, 0, len(rules)+1)
	for _, rule := range rules {
		if imeRuleTargetsApp(rule, bundleID, processName) {
			continue
		}
		kept = append(kept, rule)
	}
	if mode == "" {
		return kept
	}
	newRule := config.IMERule{
		BundleID:       bundleID,
		ProcessName:    processName,
		AppName:        appName,
		PreferredMode:  mode,
		RestoreOnLeave: true,
	}
	if mode == config.IMEModeASCII {
		newRule.InputSourceID = strings.TrimSpace(asciiSourceID)
	}
	return append(kept, newRule)
}

func (c *AppController) handlePresentCapsule(phase engine.CapsulePhase) {
	c.stopHUDTimer()
	if phase == engine.PhaseHUD {
		durMs := c.ConfigStore.Current().Main.Appearance.HUDDurationMs
		if durMs <= 0 {
			durMs = defaultHUDDurationMs
		}
		c.mu.Lock()
		c.hudGen++
		gen := c.hudGen
		c.hudTimer = time.AfterFunc(time.Duration(durMs)*time.Millisecond, func() {
			c.mu.Lock()
			if c.hudGen != gen || c.StateMachine.State().Phase != engine.PhaseHUD {
				c.mu.Unlock()
				return
			}
			c.mu.Unlock()
			c.Dispatch(engine.Event{Type: engine.EventDismiss})
		})
		c.mu.Unlock()
	}

	c.mu.Lock()
	onPresent := c.OnPresentPhase
	c.mu.Unlock()
	if onPresent != nil {
		onPresent(phase)
	}
}

func (c *AppController) stopHUDTimer() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hudGen++
	if c.hudTimer != nil {
		c.hudTimer.Stop()
		c.hudTimer = nil
	}
}

// FormatIMEPillLabel formats a short floating pill label ("EN", "中", or input source name).
func FormatIMEPillLabel(info host.InputSourceInfo) string {
	// A shared CJK IME (Windows Microsoft Pinyin) reports its live 中/英 conversion mode.
	switch info.Mode {
	case host.InputModeASCII:
		return "EN"
	case host.InputModeNative:
		if lang := strings.ToLower(strings.TrimSpace(info.Language)); strings.HasPrefix(lang, "ja") {
			return "あ"
		} else if strings.HasPrefix(lang, "ko") {
			return "한"
		}
		return "中"
	}
	if info.IsASCII {
		return "EN"
	}
	lowerID := strings.ToLower(strings.TrimSpace(info.ID))
	lowerName := strings.ToLower(strings.TrimSpace(info.Name))
	lowerLang := strings.ToLower(strings.TrimSpace(info.Language))
	if strings.Contains(lowerID, "scim") ||
		strings.Contains(lowerID, "pinyin") ||
		strings.Contains(lowerID, "rime") ||
		strings.Contains(lowerID, "sogou") ||
		strings.Contains(lowerName, "pinyin") ||
		strings.Contains(info.Name, "拼") ||
		strings.Contains(info.Name, "中") ||
		strings.HasPrefix(lowerLang, "zh") {
		return "中"
	}
	if strings.TrimSpace(info.Name) != "" {
		return strings.TrimSpace(info.Name)
	}
	if strings.TrimSpace(info.ID) != "" {
		return strings.TrimSpace(info.ID)
	}
	return "EN"
}

// FormatInputSourceStatus renders a readable "<name> · 中/EN" line for settings and diagnostics.
func FormatInputSourceStatus(info host.InputSourceInfo) string {
	name := strings.TrimSpace(info.Name)
	if name == "" {
		name = strings.TrimSpace(info.ID)
	}
	if name == "" {
		return i18n.T("settings.diag.unknown")
	}
	label := FormatIMEPillLabel(info)
	if label == name {
		return name
	}
	return name + " · " + label
}

// NotifyIMESwitchFailed records an IME switch the bridge could not apply and surfaces it in the
// HUD/pill and the settings diagnostics card, so failures are never silent.
func (c *AppController) NotifyIMESwitchFailed(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	c.lastIMEError = err.Error()
	c.mu.Unlock()
	c.ShowToastOrHUD(i18n.Tf("hud.ime_switch_failed", err.Error()))
}

// NotifyIMESwitch updates the active input source and triggers OnShowIMEPill when ShowFloatingPill is enabled.
func (c *AppController) NotifyIMESwitch(info host.InputSourceInfo, anchor *host.CursorAnchor) {
	cfg := c.ConfigStore.Current()
	c.mu.Lock()
	if info.ID != "" || info.Name != "" {
		c.activeInputSource = info
	}
	pillCb := c.OnShowIMEPill
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if cfg.Main.IME.ShowFloatingPill && pillCb != nil {
		pillCb(FormatIMEPillLabel(info), anchor)
	}
	if onRepaint != nil {
		onRepaint()
	}
}

// NotifyKeymapLayer records the current keymap hold layer and surfaces a transient pill notice
// when entering a non-empty layer (e.g. "nav", "hyper").
func (c *AppController) NotifyKeymapLayer(layer string) {
	trimmed := strings.TrimSpace(layer)
	c.mu.Lock()
	c.activeKeymapLayer = trimmed
	pillCb := c.OnShowIMEPill
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if trimmed != "" && pillCb != nil && c.ConfigStore.Current().Main.IME.ShowFloatingPill {
		pillCb("LAYER • "+strings.ToUpper(trimmed), nil)
	}
	if onRepaint != nil {
		onRepaint()
	}
}

func (c *AppController) maybeLockASCIIForLeader() {
	cfg := c.ConfigStore.Current()
	if !cfg.Main.IME.Enabled || !cfg.Main.IME.LockASCIIOnLeader {
		return
	}

	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	if br == nil {
		return
	}

	ctx := context.Background()
	var prevID string
	if curBefore, curErr := br.CurrentInputSource(ctx); curErr == nil && !curBefore.IsASCII {
		prevID = strings.TrimSpace(curBefore.ID)
	}
	changed, asciiInfo, err := br.LockTemporaryASCII(ctx)
	if err != nil {
		// Bridges without temporary-lock support fall back to the configured default source.
		changed, asciiInfo = c.legacyLockASCII(cfg, br)
	}
	if !changed {
		return
	}

	c.mu.Lock()
	if prevID != "" && c.preLeaderIMESourceID == "" {
		c.preLeaderIMESourceID = prevID
	}
	c.imeLockedForCapsule = true
	c.skipIMERestore = false
	c.activeInputSource = asciiInfo
	pillCb := c.OnShowIMEPill
	c.mu.Unlock()

	if cfg.Main.IME.ShowFloatingPill && pillCb != nil {
		pillCb(FormatIMEPillLabel(asciiInfo), nil)
	}
}

// legacyLockASCII selects cfg.Main.IME.DefaultInputSourceID directly and remembers the
// previous non-ASCII source so a legacy restore path can still work.
func (c *AppController) legacyLockASCII(cfg config.ClavisConfig, br bridge.HostService) (bool, host.InputSourceInfo) {
	defaultASCII := strings.TrimSpace(cfg.Main.IME.DefaultInputSourceID)
	if defaultASCII == "" {
		return false, host.InputSourceInfo{}
	}
	ctx := context.Background()
	cur, err := br.CurrentInputSource(ctx)
	if err != nil || cur.IsASCII || strings.TrimSpace(cur.ID) == "" || cur.ID == defaultASCII {
		return false, cur
	}
	if err := br.SelectInputSource(ctx, defaultASCII); err != nil {
		return false, cur
	}

	asciiInfo := host.InputSourceInfo{
		ID:      defaultASCII,
		Name:    "ABC",
		IsASCII: true,
	}
	if curAfter, err := br.CurrentInputSource(ctx); err == nil && curAfter.ID != "" {
		asciiInfo = curAfter
	}

	c.mu.Lock()
	c.preLeaderIMESourceID = cur.ID
	c.mu.Unlock()
	return true, asciiInfo
}

func (c *AppController) maybeRestoreLockedIME() {
	c.mu.Lock()
	locked := c.imeLockedForCapsule
	skip := c.skipIMERestore
	restoreID := strings.TrimSpace(c.preLeaderIMESourceID)
	br := c.Bridge
	c.imeLockedForCapsule = false
	c.skipIMERestore = false
	c.preLeaderIMESourceID = ""
	c.mu.Unlock()

	if !locked || skip || br == nil {
		return
	}

	ctx := context.Background()
	restored, info, err := br.RestoreTemporaryIME(ctx)
	if (err != nil || !restored) && restoreID != "" {
		// Fallback to the remembered pre-Leader non-ASCII source ID if needed.
		if selErr := br.SelectInputSource(ctx, restoreID); selErr == nil {
			if cur, curErr := br.CurrentInputSource(ctx); curErr == nil && cur.ID != "" {
				info, restored = cur, true
			}
		}
	}
	if restored && info.ID != "" {
		c.mu.Lock()
		c.activeInputSource = info
		c.mu.Unlock()
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
	c.refreshAPIKeySourceLocked()
	c.aiTestStatus = AITestIdle
	c.aiTestMessage = ""
	c.settingsStatus = i18n.Tf("settings.status.active_profile", profile.Name)
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if onRepaint != nil {
		onRepaint()
	}
	return saveErr
}

func (c *AppController) resolveAPIKey(_ context.Context) (string, error) {
	profile := c.ActiveAIProfile()
	return secrets.ResolveProfileAPIKey(c.SecretStore, profile)
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

	c.mu.Lock()
	if c.aiCancel != nil {
		c.aiCancel()
	}
	c.aiGen++
	gen := c.aiGen
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	c.aiCancel = cancel
	hasClient := c.AIClient != nil
	var streamClient ai.OpenAICompatibleClient
	if hasClient {
		streamClient = *c.AIClient
	}
	secStore := c.SecretStore
	c.mu.Unlock()

	if !hasClient {
		cancel()
		c.Dispatch(engine.Event{
			Type:  engine.EventAIFailed,
			Error: i18n.T("settings.ai.not_configured"),
		})
		return
	}
	streamClient.Config = cfg.Main.AI
	streamClient.APIKeyProvider = func(context.Context) (string, error) {
		return secrets.ResolveProfileAPIKey(secStore, profile)
	}
	history := convertChatHistory(eff.History)
	profCopy := profile

	go func() {
		defer cancel()
		err := streamClient.StreamCompletion(ctx, ai.CompletionRequest{
			Mode:       eff.Mode,
			Prompt:     eff.Prompt,
			SourceText: eff.SourceText,
			Context:    eff.Context,
			History:    history,
			Profile:    &profCopy,
		}, func(delta string) {
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
			c.Dispatch(engine.Event{
				Type:  engine.EventAIFailed,
				Error: err.Error(),
			})
			return
		}

		c.Dispatch(engine.Event{
			Type: engine.EventAICompleted,
		})
	}()
}

func (c *AppController) cancelAIStream() {
	c.mu.Lock()
	c.aiGen++
	cancel := c.aiCancel
	c.aiCancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *AppController) runClipboardWorker() {
	defer close(c.clipDone)
	for {
		c.clipMu.Lock()
		for len(c.clipQueue) == 0 && !c.clipClosed {
			c.clipCond.Wait()
		}
		if c.clipClosed && len(c.clipQueue) == 0 {
			c.clipMu.Unlock()
			return
		}
		task := c.clipQueue[0]
		c.clipQueue = c.clipQueue[1:]
		c.clipMu.Unlock()

		ctx := context.Background()
		if c.ClipboardStore != nil {
			_ = task.fn(ctx)
			if c.clipGen.Load() == task.gen {
				c.syncClipboardEntries(ctx)
			}
		}
	}
}

func (c *AppController) enqueueClipboardTask(fn func(ctx context.Context) error) {
	gen := c.clipGen.Add(1)
	c.clipMu.Lock()
	if !c.clipClosed {
		c.clipQueue = append(c.clipQueue, clipTask{gen: gen, fn: fn})
		c.clipCond.Signal()
	}
	c.clipMu.Unlock()
}

func (c *AppController) syncClipboardEntries(ctx context.Context) {
	if c.ClipboardStore == nil {
		return
	}
	limit := c.ConfigStore.Current().Main.Clipboard.MaxHistoryItems
	if limit <= 0 {
		limit = 200
	}
	items, err := c.ClipboardStore.Recent(ctx, limit)
	if err != nil {
		return
	}
	entries := make([]engine.OmniboxClipboardEntry, len(items))
	for i, it := range items {
		entries[i] = engine.OmniboxClipboardEntry{
			ID:            it.ID,
			Content:       it.Content,
			SourceAppName: it.SourceAppName,
			IsPinned:      it.IsPinned,
		}
	}
	c.Dispatch(engine.Event{
		Type:             engine.EventSetClipboardEntries,
		ClipboardEntries: entries,
	})
}

func (c *AppController) injectTextAsync(text string, deleteCount, cursorBackOffset int) {
	c.mu.Lock()
	br := c.Bridge
	beforeInject := c.BeforeInject
	c.mu.Unlock()
	if br == nil {
		return
	}
	go func() {
		ctx := context.Background()
		if beforeInject != nil {
			beforeInject(ctx)
		}
		_ = br.InjectText(ctx, text, deleteCount, cursorBackOffset)
	}()
}

func (c *AppController) writeClipboardText(text string) {
	c.mu.Lock()
	writer := c.ClipboardWriter
	c.mu.Unlock()
	if writer != nil {
		writer(text)
		return
	}
	if mygo.App.IsReady() {
		mygo.Clipboard.WriteText(text)
	}
}

func (c *AppController) runAction(act config.LeaderAction, actionCtx engine.CapsuleContext) {
	switch act.Type {
	case config.ActionWindow:
		c.mu.Lock()
		br := c.Bridge
		c.mu.Unlock()
		if br != nil {
			go func() {
				_ = br.ExecuteWindowCommand(context.Background(), act.WindowCommand)
			}()
		}

	case config.ActionCLI:
		if act.Command == "clavis-setting" {
			c.handleInternalSettingCommand(act.Args)
			return
		}
		c.mu.Lock()
		execer := c.CLIExecutor
		c.mu.Unlock()
		go func() {
			res, err := execer.Execute(context.Background(), act.Command, act.Args, actionCtx, 0)
			if err != nil {
				c.Dispatch(engine.Event{
					Type:    engine.EventShowHUD,
					Message: i18n.Tf("hud.command_failed", err),
				})
				return
			}
			if res.TimedOut {
				c.Dispatch(engine.Event{
					Type:    engine.EventShowHUD,
					Message: i18n.T("hud.command_timeout"),
				})
				return
			}
			if res.ExitCode != 0 {
				c.Dispatch(engine.Event{
					Type:    engine.EventShowHUD,
					Message: i18n.Tf("hud.command_exit_code", res.ExitCode),
				})
			}
		}()

	case config.ActionOpenApp:
		c.mu.Lock()
		br := c.Bridge
		c.mu.Unlock()
		if br != nil {
			go func() {
				_ = br.OpenApp(context.Background(), act.Target)
			}()
		}

	case config.ActionSnippet:
		cfg := c.ConfigStore.Current()
		for _, item := range cfg.Snippets.Items {
			if item.ID == act.SnippetID {
				rendered := engine.RenderSnippet(item.Content, engine.RenderOptions{
					Now:           time.Now(),
					ClipboardText: actionCtx.ClipboardPreview,
					AppName:       actionCtx.AppName,
				})
				c.injectTextAsync(rendered.RenderedText, 0, rendered.CursorBackOffset)
				return
			}
		}
		c.Dispatch(engine.Event{
			Type:    engine.EventShowHUD,
			Message: i18n.Tf("hud.snippet_not_found", act.SnippetID),
		})

	case config.ActionToggleIME:
		c.mu.Lock()
		c.skipIMERestore = true
		c.imeLockedForCapsule = false
		c.preLeaderIMESourceID = ""
		c.mu.Unlock()
		c.toggleIME()

	case config.ActionNormalizeGlossary:
		c.normalizeGlossary(actionCtx)
	}
}

func (c *AppController) handleInternalSettingCommand(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch sub {
	case "reload-config":
		if c.ReloadConfig() {
			c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.T("hud.config_reloaded")})
		} else {
			c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.T("hud.config_reload_failed")})
		}
	case "open-config-dir":
		if err := c.OpenConfigDirectory(); err != nil {
			c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.Tf("hud.open_folder_failed", err)})
		}
	case "ai":
		c.OpenSettings(SettingsTabAI)
	case "ime", "keymap", "ime-keymap":
		c.OpenSettings(SettingsTabIMEKeymap)
	case "permissions":
		c.OpenSettings(SettingsTabPermissions)
	case "configs":
		c.OpenSettings(SettingsTabConfigs)
	default:
		c.OpenSettings(SettingsTabGeneral)
	}
}

// OpenSettings selects the requested settings tab and invokes OnOpenSettings.
func (c *AppController) OpenSettings(tab SettingsTab) {
	c.mu.Lock()
	c.uiState.settingsTabIdx = int(tab)
	fn := c.OnOpenSettings
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if fn != nil {
		fn(tab)
	}
	if onRepaint != nil {
		onRepaint()
	}
}

func (c *AppController) toggleIME() {
	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	if br == nil {
		return
	}
	go func() {
		ctx := context.Background()
		sources, err := br.InputSources(ctx)
		if err != nil || len(sources) == 0 {
			c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.T("hud.no_input_sources")})
			return
		}
		current, err := br.CurrentInputSource(ctx)
		next := sources[0]
		if err == nil {
			// Prefer toggling between ASCII and non-ASCII when available.
			foundAlt := false
			for _, s := range sources {
				if s.ID != current.ID && s.IsASCII != current.IsASCII {
					next = s
					foundAlt = true
					break
				}
			}
			if !foundAlt {
				for i, s := range sources {
					if s.ID == current.ID {
						next = sources[(i+1)%len(sources)]
						break
					}
				}
			}
		}
		if err := br.SelectInputSource(ctx, next.ID); err != nil {
			c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.Tf("hud.ime_switch_failed", err)})
			return
		}
		c.NotifyIMESwitch(next, nil)
		name := next.Name
		if name == "" {
			name = next.ID
		}
		c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.Tf("hud.ime_switched", name)})
	}()
}

func (c *AppController) normalizeGlossary(actionCtx engine.CapsuleContext) {
	c.mu.Lock()
	glossary := c.Glossary
	c.mu.Unlock()

	if strings.TrimSpace(actionCtx.SelectedText) != "" {
		normalized := glossary.NormalizeAliases(actionCtx.SelectedText, true, true)
		c.injectTextAsync(normalized, 0, 0)
		c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.T("hud.normalized_glossary")})
		return
	}
	if strings.TrimSpace(actionCtx.ClipboardPreview) != "" {
		normalized := glossary.NormalizeAliases(actionCtx.ClipboardPreview, true, true)
		c.writeClipboardText(normalized)
		c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.T("hud.normalized_clipboard")})
		return
	}
	c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.T("hud.no_selection")})
}

// OpenOmnibox syncs clipboard history, installed apps, and frontmost context before opening the Omnibox.
func (c *AppController) OpenOmnibox(tab config.CapsuleTab, query string) {
	ctx := context.Background()
	c.syncClipboardEntries(ctx)
	if len(c.State().InstalledApps) == 0 {
		c.SyncInstalledApps(ctx)
	}
	capCtx := c.resolveTriggerContext(nil)
	c.Dispatch(engine.Event{
		Type:    engine.EventOpenOmnibox,
		Tab:     tab,
		Query:   query,
		Context: &capCtx,
	})
}

// OpenLeader syncs clipboard history, installed apps, and frontmost context before opening the Leader panel.
func (c *AppController) OpenLeader() {
	ctx := context.Background()
	c.syncClipboardEntries(ctx)
	if len(c.State().InstalledApps) == 0 {
		c.SyncInstalledApps(ctx)
	}
	capCtx := c.resolveTriggerContext(nil)
	c.Dispatch(engine.Event{
		Type:    engine.EventOpenLeader,
		Context: &capCtx,
	})
}

// HandleTriggerEvent processes global keyboard/hotkey triggers from bridge.HostService.
func (c *AppController) HandleTriggerEvent(ev host.TriggerEvent) {
	switch ev.Kind {
	case host.TriggerLeaderKey, host.TriggerDoubleTap:
		ctx := context.Background()
		c.syncClipboardEntries(ctx)
		if len(c.State().InstalledApps) == 0 {
			c.SyncInstalledApps(ctx)
		}
		capCtx := c.resolveTriggerContext(ev.Context)
		c.Dispatch(engine.Event{
			Type:    engine.EventOpenLeader,
			Context: &capCtx,
		})

	case host.TriggerCapsuleToggle:
		phase := c.State().Phase
		if phase == engine.PhaseLeader || phase == engine.PhaseOmnibox || phase == engine.PhaseAIStreaming {
			c.Dispatch(engine.Event{Type: engine.EventDismiss})
			return
		}
		ctx := context.Background()
		c.syncClipboardEntries(ctx)
		if len(c.State().InstalledApps) == 0 {
			c.SyncInstalledApps(ctx)
		}
		capCtx := c.resolveTriggerContext(ev.Context)
		c.Dispatch(engine.Event{
			Type:    engine.EventOpenOmnibox,
			Tab:     config.TabActions,
			Context: &capCtx,
		})

	case host.TriggerClipboardToggle:
		// Maccy-style: the dedicated hotkey jumps straight to Clipboard History; pressing it again
		// while that tab is showing closes the Capsule.
		state := c.State()
		if state.Phase == engine.PhaseOmnibox && state.OmniboxTab == config.TabClipboard {
			c.Dispatch(engine.Event{Type: engine.EventDismiss})
			return
		}
		ctx := context.Background()
		c.syncClipboardEntries(ctx)
		if len(c.State().InstalledApps) == 0 {
			c.SyncInstalledApps(ctx)
		}
		capCtx := c.resolveTriggerContext(ev.Context)
		c.Dispatch(engine.Event{
			Type:    engine.EventOpenOmnibox,
			Tab:     config.TabClipboard,
			Context: &capCtx,
		})

	case host.TriggerSnippetKey:
		cfg := c.ConfigStore.Current()
		if !cfg.Main.Trigger.SnippetExpansionEnabled || ev.Rune == "" {
			return
		}
		bundleID, processName, appName, clipText := c.cachedSnippetContext(ev.Context)
		now := time.Now()
		opts := engine.FeedOptions{
			BundleID:      bundleID,
			ProcessName:   processName,
			Now:           now,
			ClipboardText: clipText,
			AppName:       appName,
		}
		for _, r := range ev.Rune {
			if match, ok := c.feedSnippetRune(r, opts); ok && match != nil {
				renderedText := match.RenderedText
				delCount := match.DeleteCount
				backOffset := match.CursorBackOffset
				if strings.Contains(match.Snippet.Content, "{{clipboard}}") || strings.Contains(match.Snippet.Content, "{{app}}") {
					capCtx := c.resolveTriggerContext(ev.Context)
					rendered := engine.RenderSnippet(match.Snippet.Content, engine.RenderOptions{
						TriggerLength: match.DeleteCount,
						Now:           now,
						ClipboardText: capCtx.ClipboardPreview,
						AppName:       capCtx.AppName,
					})
					renderedText = rendered.RenderedText
					delCount = rendered.DeleteCount
					backOffset = rendered.CursorBackOffset
				}
				c.injectTextAsync(renderedText, delCount, backOffset)
				break
			}
		}

	case host.TriggerSnippetBack:
		c.mu.Lock()
		c.Snippets.Backspace()
		c.mu.Unlock()

	case host.TriggerSnippetReset:
		// Cursor moved, app changed, IME composed, or a click landed: the typed trigger prefix
		// can no longer be relied on, so drop it instead of risking a wrong expansion.
		c.mu.Lock()
		if c.Snippets != nil {
			c.Snippets.Reset()
		}
		c.mu.Unlock()
	}
}

func (c *AppController) cachedSnippetContext(snap *host.ContextSnapshot) (bundleID, processName, appName, clipText string) {
	if snap != nil {
		c.mu.Lock()
		c.cachedBundleID = snap.BundleID
		c.cachedProcessName = snap.ProcessName
		c.cachedAppName = snap.AppName
		c.mu.Unlock()
		return snap.BundleID, snap.ProcessName, snap.AppName, snap.ClipboardPreview
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cachedBundleID, c.cachedProcessName, c.cachedAppName, ""
}

func (c *AppController) feedSnippetRune(r rune, opts engine.FeedOptions) (*engine.SnippetMatch, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Snippets.Feed(r, opts)
}

func (c *AppController) resolveTriggerContext(snap *host.ContextSnapshot) engine.CapsuleContext {
	var resolved host.ContextSnapshot
	if snap != nil {
		resolved = *snap
	} else if c.Bridge != nil {
		if s, err := c.Bridge.SnapshotContext(context.Background()); err == nil {
			resolved = s
		}
	}
	c.mu.Lock()
	if resolved.BundleID != "" || resolved.ProcessName != "" || resolved.AppName != "" {
		c.cachedBundleID = resolved.BundleID
		c.cachedProcessName = resolved.ProcessName
		c.cachedAppName = resolved.AppName
	}
	if resolved.ActiveInputSource != nil {
		c.activeInputSource = *resolved.ActiveInputSource
	}
	c.mu.Unlock()
	if resolved.ActiveInputSource == nil && c.Bridge != nil {
		if src, err := c.Bridge.CurrentInputSource(context.Background()); err == nil && src.ID != "" {
			c.mu.Lock()
			c.activeInputSource = src
			c.mu.Unlock()
		}
	}
	return engine.CapsuleContext{
		BundleID:         resolved.BundleID,
		ProcessName:      resolved.ProcessName,
		AppName:          resolved.AppName,
		WindowTitle:      resolved.WindowTitle,
		ActiveURL:        resolved.ActiveURL,
		SelectedText:     resolved.SelectedText,
		SurroundingText:  resolved.SurroundingText,
		ClipboardPreview: resolved.ClipboardPreview,
	}
}

// TestAIConnection sends a lightweight "Reply with OK" prompt for the currently active AI profile.
func (c *AppController) TestAIConnection(ctx context.Context) error {
	return c.TestAIConnectionForProfile(ctx, c.ActiveAIProfile().ID)
}

// TestAIConnectionForProfile sends a lightweight "Reply with OK" prompt for a specific AI profile ID.
func (c *AppController) TestAIConnectionForProfile(ctx context.Context, profileID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
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
		c.setAITestResult(AITestError, err.Error())
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
		c.setSettingsStatus(i18n.Tf("settings.status.save_api_key_failed", err))
		return err
	}
	c.mu.Lock()
	c.refreshAPIKeySourceLocked()
	c.settingsStatus = i18n.T("settings.status.api_key_saved")
	c.uiState.apiKeyDraft = ""
	onRepaint := c.OnRepaint
	c.mu.Unlock()
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
		c.setSettingsStatus(i18n.Tf("settings.status.delete_api_key_failed", err))
		return err
	}
	c.mu.Lock()
	c.refreshAPIKeySourceLocked()
	c.settingsStatus = i18n.T("settings.status.api_key_cleared")
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// APIKeyStatus returns the current masked key source (keychain / env / none) for the active AI profile.
func (c *AppController) APIKeyStatus() secrets.APIKeySource {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshAPIKeySourceLocked()
	return c.apiKeySource
}

func (c *AppController) refreshAPIKeySourceLocked() {
	if c.SecretStore == nil {
		c.apiKeySource = secrets.KeySourceNone
		return
	}
	profile := c.activeAIProfileLocked()
	src, err := secrets.ResolveProfileAPIKeySource(c.SecretStore, profile)
	if err != nil {
		c.apiKeySource = secrets.KeySourceNone
		return
	}
	c.apiKeySource = src
}

func (c *AppController) setSettingsStatus(msg string) {
	c.mu.Lock()
	c.settingsStatus = msg
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
}

// UpdateMainConfig mutates and persists config.toml via ConfigStore.SaveMain,
// then propagates keymap and IME updates to the host bridge.
func (c *AppController) UpdateMainConfig(mutate func(main *config.MainConfig)) error {
	if mutate == nil {
		return nil
	}
	cfg := c.ConfigStore.Current()
	mainCfg := cfg.Main
	mutate(&mainCfg)

	if err := c.ConfigStore.SaveMain(mainCfg); err != nil {
		c.setSettingsStatus(i18n.Tf("settings.status.save_failed", err))
		return err
	}

	updated := c.ConfigStore.Current()
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	if c.AIClient != nil {
		c.AIClient.Config = updated.Main.AI
	}
	c.refreshAPIKeySourceLocked()
	c.settingsStatus = i18n.T("settings.status.saved")
	br := c.Bridge
	onRepaint := c.OnRepaint
	onLanguageChange := c.OnLanguageChange
	langChanged := syncAppearanceLanguage(updated)
	c.mu.Unlock()

	if br != nil {
		br.UpdateKeymapConfig(updated.Main.Keymap)
		br.UpdateIMEConfig(updated.Main.IME, updated.IMERules)
	}
	if langChanged && onLanguageChange != nil {
		onLanguageChange()
	}
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// ExitHideDelay reports how long the shell should keep the capsule window
// visible after a dismiss so the exit animation lands; 0 hides at once.
// The value is published by the last CapsuleView frame (fresh config and
// accessibility preferences).
func (c *AppController) ExitHideDelay() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.uiState.lastExitHideDelay
}

// syncAppearanceLanguage applies [appearance].language of cfg, resolving
// "auto" against the system preference, and reports whether the language
// changed. It runs on config save/reload so views, tray and diagnostics
// speak one language.
func syncAppearanceLanguage(cfg config.ClavisConfig) bool {
	prev := i18n.Current()
	i18n.SetLanguage(i18n.ResolveLanguage(cfg.Main.Appearance.Language, i18n.PreferredLanguages()))
	return i18n.Current() != prev
}

// AddIMERule validates and appends an IME auto-switch rule to ime-rules.toml,
// then updates the host bridge.
func (c *AppController) AddIMERule(rule config.IMERule) error {
	cfg := c.ConfigStore.Current()
	rule.BundleID = strings.TrimSpace(rule.BundleID)
	rule.ProcessName = strings.TrimSpace(rule.ProcessName)
	rule.AppName = strings.TrimSpace(rule.AppName)
	rule.URLPattern = strings.TrimSpace(rule.URLPattern)
	rule.TitlePattern = strings.TrimSpace(rule.TitlePattern)
	rule.InputSourceID = strings.TrimSpace(rule.InputSourceID)

	if rule.InputSourceID == "" {
		rule.InputSourceID = strings.TrimSpace(cfg.Main.IME.DefaultInputSourceID)
		if rule.InputSourceID == "" {
			rule.InputSourceID = "com.apple.keylayout.ABC"
		}
	}
	if rule.PreferredMode == "" {
		rule.PreferredMode = config.IMEModeASCII
	}
	if rule.AppName == "" {
		if rule.BundleID != "" {
			rule.AppName = rule.BundleID
		} else {
			rule.AppName = rule.ProcessName
		}
	}

	rulesCfg := cfg.IMERules
	rulesCfg.Rules = append(append([]config.IMERule(nil), rulesCfg.Rules...), rule)

	if err := c.ConfigStore.SaveIMERules(rulesCfg); err != nil {
		c.setSettingsStatus(i18n.Tf("settings.status.add_ime_rule_failed", err))
		return err
	}

	updated := c.ConfigStore.Current()
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	c.uiState.imeRuleTargetDraft = ""
	c.uiState.imeRuleURLDraft = ""
	c.uiState.imeRuleTitleDraft = ""
	c.uiState.imeRuleSourceDraft = ""
	c.settingsStatus = i18n.T("settings.status.ime_rule_added")
	br := c.Bridge
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if br != nil {
		br.UpdateIMEConfig(updated.Main.IME, updated.IMERules)
	}
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// DeleteIMERule removes the IME rule at index from ime-rules.toml and updates the host bridge.
func (c *AppController) DeleteIMERule(index int) error {
	cfg := c.ConfigStore.Current()
	if index < 0 || index >= len(cfg.IMERules.Rules) {
		return fmt.Errorf("IME rule index %d out of range", index)
	}

	rules := make([]config.IMERule, 0, len(cfg.IMERules.Rules)-1)
	for i, r := range cfg.IMERules.Rules {
		if i != index {
			rules = append(rules, r)
		}
	}
	rulesCfg := config.IMERulesConfig{Rules: rules}

	if err := c.ConfigStore.SaveIMERules(rulesCfg); err != nil {
		c.setSettingsStatus(i18n.Tf("settings.status.delete_ime_rule_failed", err))
		return err
	}

	updated := c.ConfigStore.Current()
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	c.settingsStatus = i18n.T("settings.status.ime_rule_deleted")
	br := c.Bridge
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	if br != nil {
		br.UpdateIMEConfig(updated.Main.IME, updated.IMERules)
	}
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// SaveLeaderConfig persists leader.toml, refreshes the state machine, and repaints.
func (c *AppController) SaveLeaderConfig(leaderCfg config.LeaderConfig) error {
	if err := c.ConfigStore.SaveLeader(leaderCfg); err != nil {
		c.setSettingsStatus(i18n.Tf("settings.status.save_leader_failed", err))
		return err
	}
	updated := c.ConfigStore.Current()
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	c.settingsStatus = i18n.Tf("settings.status.leader_saved", len(updated.Leader.Nodes))
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// SaveSnippetsConfig persists snippets.toml, hot-reloads the snippet engine, and repaints.
func (c *AppController) SaveSnippetsConfig(snippetsCfg config.SnippetsConfig) error {
	if err := c.ConfigStore.SaveSnippets(snippetsCfg); err != nil {
		c.setSettingsStatus(i18n.Tf("settings.status.save_snippets_failed", err))
		return err
	}
	updated := c.ConfigStore.Current()
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	if c.Snippets != nil {
		c.Snippets.UpdateSnippets(updated.Snippets.Items)
	}
	c.settingsStatus = i18n.Tf("settings.status.snippets_saved", len(updated.Snippets.Items))
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// SaveGlossaryConfig persists glossary.toml, hot-reloads the glossary and AI client engines, and repaints.
func (c *AppController) SaveGlossaryConfig(glossaryCfg config.GlossaryConfig) error {
	if err := c.ConfigStore.SaveGlossary(glossaryCfg); err != nil {
		c.setSettingsStatus(i18n.Tf("settings.status.save_glossary_failed", err))
		return err
	}
	updated := c.ConfigStore.Current()
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	c.Glossary = engine.NewGlossaryEngineFromConfig(updated.Glossary)
	if c.AIClient != nil {
		c.AIClient.GlossaryEngine = c.Glossary
	}
	c.settingsStatus = i18n.Tf("settings.status.glossary_saved", len(updated.Glossary.Terms))
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// SaveIMERulesConfig persists ime-rules.toml, refreshes the state machine, and updates the host bridge.
func (c *AppController) SaveIMERulesConfig(imeRulesCfg config.IMERulesConfig) error {
	if err := c.ConfigStore.SaveIMERules(imeRulesCfg); err != nil {
		c.setSettingsStatus(i18n.Tf("settings.status.save_ime_rules_failed", err))
		return err
	}
	updated := c.ConfigStore.Current()
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	c.settingsStatus = i18n.Tf("settings.status.ime_rules_saved", len(updated.IMERules.Rules))
	br := c.Bridge
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if br != nil {
		br.UpdateIMEConfig(updated.Main.IME, updated.IMERules)
	}
	if onRepaint != nil {
		onRepaint()
	}
	return nil
}

// CurrentInputSource returns the live input source from the bridge (falling back to the last
// observed one), used by the Settings IME card.
func (c *AppController) CurrentInputSource(ctx context.Context) (host.InputSourceInfo, error) {
	c.mu.Lock()
	br := c.Bridge
	last := c.activeInputSource
	c.mu.Unlock()
	if br == nil {
		return last, nil
	}
	cur, err := br.CurrentInputSource(ctx)
	if err != nil {
		return last, err
	}
	return cur, nil
}

// AvailableInputSources returns the OS input sources for Settings pickers (empty on error/unavailable).
func (c *AppController) AvailableInputSources(ctx context.Context) []host.InputSourceInfo {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	if br == nil {
		return nil
	}
	sources, err := br.InputSources(ctx)
	if err != nil {
		return nil
	}
	return sources
}

// ReloadConfig reloads all 5 TOML config files from ConfigStore and updates engines.
func (c *AppController) ReloadConfig() bool {
	ok := c.ConfigStore.Reload()
	cfg := c.ConfigStore.Current()

	c.mu.Lock()
	var br bridge.HostService
	if ok {
		c.StateMachine.SetConfig(cfg)
		if c.Snippets != nil {
			c.Snippets.UpdateSnippets(cfg.Snippets.Items)
		}
		c.Glossary = engine.NewGlossaryEngineFromConfig(cfg.Glossary)
		c.AIClient = ai.NewOpenAICompatibleClient(
			cfg.Main.AI,
			c.Glossary,
			c.httpDoer,
			c.resolveAPIKey,
		)
		c.refreshAPIKeySourceLocked()
		c.settingsStatus = i18n.T("hud.config_reloaded")
		br = c.Bridge
	} else {
		errs := c.ConfigStore.LastErrors()
		c.settingsStatus = i18n.Tf("settings.status.reload_failed", len(errs))
	}
	onRepaint := c.OnRepaint
	onLanguageChange := c.OnLanguageChange
	langChanged := syncAppearanceLanguage(cfg)
	c.mu.Unlock()

	if ok && br != nil {
		br.UpdateKeymapConfig(cfg.Main.Keymap)
		br.UpdateIMEConfig(cfg.Main.IME, cfg.IMERules)
	}
	if langChanged && onLanguageChange != nil {
		onLanguageChange()
	}
	if onRepaint != nil {
		onRepaint()
	}
	return ok
}

// RefreshPermissions queries the host bridge for current OS permissions and active IME.
func (c *AppController) RefreshPermissions(ctx context.Context) (host.PermissionStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	if br == nil {
		return host.PermissionStatus{}, nil
	}
	status, err := br.Permissions(ctx)
	var activeIME host.InputSourceInfo
	if ime, imeErr := br.CurrentInputSource(ctx); imeErr == nil {
		activeIME = ime
	}
	rec := int(br.TapRecoveryCount())
	c.mu.Lock()
	if err == nil {
		c.permissionStatus = status
	}
	if activeIME.ID != "" {
		c.activeInputSource = activeIME
	}
	if rec > c.recoveryCount {
		c.recoveryCount = rec
	}
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
	return status, err
}

// PromptPermission requests OS permission (accessibility / input_monitoring) via host bridge.
func (c *AppController) PromptPermission(kind string) (host.PermissionStatus, error) {
	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	if br == nil {
		return host.PermissionStatus{}, nil
	}
	status, err := br.PromptPermission(kind)
	c.mu.Lock()
	if err == nil {
		c.permissionStatus = status
	}
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
	return status, err
}

// OpenPermissionSettings opens OS privacy/permission settings for kind.
func (c *AppController) OpenPermissionSettings(kind string) error {
	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	if br == nil {
		return nil
	}
	return br.OpenPermissionSettings(kind)
}

// OpenConfigDirectory opens the Clavis configuration directory in the OS file manager.
func (c *AppController) OpenConfigDirectory() error {
	return c.openLocalPath(c.ConfigStore.Directory())
}

// OpenConfigFile opens one of the 5 TOML configuration files.
func (c *AppController) OpenConfigFile(kind config.ConfigFileKind) error {
	return c.openLocalPath(filepath.Join(c.ConfigStore.Directory(), string(kind)))
}

func (c *AppController) openLocalPath(path string) error {
	cleanPath := filepath.Clean(path)
	c.mu.Lock()
	fn := c.OpenPath
	c.mu.Unlock()
	if fn != nil {
		return fn(cleanPath)
	}
	if mygo.App.IsReady() {
		return mygo.Shell.OpenPath(cleanPath)
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", cleanPath).Start()
	case "windows":
		return exec.Command("explorer.exe", cleanPath).Start()
	default:
		return exec.Command("xdg-open", cleanPath).Start()
	}
}

// SelectIndex sets the Omnibox selected row index safely.
func (c *AppController) SelectIndex(idx int) {
	c.mu.Lock()
	st := c.StateMachine.State()
	if st.Phase != engine.PhaseOmnibox || idx < 0 || idx >= len(st.OmniboxItems) || idx == st.SelectedIndex {
		c.mu.Unlock()
		return
	}
	delta := idx - st.SelectedIndex
	evType := engine.EventSelectNext
	steps := delta
	if delta < 0 {
		evType = engine.EventSelectPrevious
		steps = -delta
	}
	for range steps {
		c.StateMachine.Handle(engine.Event{Type: evType})
	}
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
}

// RecordMonitorRecovery increments the monitor recovery counter (for diagnostics UI).
func (c *AppController) RecordMonitorRecovery() {
	c.mu.Lock()
	c.recoveryCount++
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
}

// DiagnosticsSnapshot captures permission and runtime diagnostic state for UI rendering.
type DiagnosticsSnapshot struct {
	Permissions       host.PermissionStatus
	MonitorRunning    bool
	RecoveryCount     int
	ActiveInputSource host.InputSourceInfo
	ActiveKeymapLayer string
	// LastIMEError is the most recent IME switch failure reported by the bridge.
	LastIMEError string
}

// Diagnostics returns a thread-safe snapshot of permission and monitor diagnostics.
func (c *AppController) Diagnostics() DiagnosticsSnapshot {
	c.mu.Lock()
	br := c.Bridge
	snap := DiagnosticsSnapshot{
		Permissions:       c.permissionStatus,
		MonitorRunning:    c.monitorRunning,
		RecoveryCount:     c.recoveryCount,
		ActiveInputSource: c.activeInputSource,
		ActiveKeymapLayer: c.activeKeymapLayer,
		LastIMEError:      c.lastIMEError,
	}
	c.mu.Unlock()
	if br != nil {
		if brRec := int(br.TapRecoveryCount()); brRec > snap.RecoveryCount {
			snap.RecoveryCount = brRec
		}
		if layer := strings.TrimSpace(br.ActiveKeymapLayer()); layer != "" {
			snap.ActiveKeymapLayer = layer
		}
	}
	return snap
}

// ForegroundIMEStatus returns the IME state of the last non-Clavis foreground app when the
// platform bridge tracks it (Windows). ok is false elsewhere or before the first observation.
func (c *AppController) ForegroundIMEStatus() (bridge.ForegroundIMEStatus, bool) {
	c.mu.Lock()
	br := c.Bridge
	c.mu.Unlock()
	rep, ok := br.(bridge.ForegroundIMEReporter)
	if !ok || rep == nil {
		return bridge.ForegroundIMEStatus{}, false
	}
	return rep.ForegroundIMEStatus()
}
