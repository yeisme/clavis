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
	"github.com/yeisme/clavis/pkg/core/automation"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/prompt"
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

// ClipboardSearcher narrows clipboard querying to Search so AppController can
// route Clipboard tab queries to SQLite FTS5 (or MemoryStore fallback) without
// coupling pkg/core/engine to storage.
type ClipboardSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]clipboard.ClipboardItem, error)
}

const (
	defaultClipboardMirrorLimit    = 50
	defaultClipboardSearchDebounce = 50 * time.Millisecond
)

// Options configures an AppController.
type Options struct {
	ConfigStore           *config.ConfigStore
	ClipboardStore        clipboard.Store
	ClipboardSearcher     ClipboardSearcher
	SecretStore           secrets.APIKeyStore
	Bridge                bridge.HostService
	AppProvider           apps.AppProvider
	HTTPDoer              ai.HTTPDoer
	ClipboardWriter       func(text string)
	BeforeInject          func(ctx context.Context)
	OpenPath              func(path string) error
	OnRepaint             func()
	OnPresentPhase        func(phase engine.CapsulePhase)
	OnHide                func()
	OnOpenSettings        func(tab SettingsTab)
	OnOpenPromptComposer  func()
	OnClosePromptComposer func()
	OnShowIMEPill         func(label string, anchor *host.CursorAnchor)
	// OnShowSelectionBar presents actions for the current focused-field selection.
	// The callback is optional so headless and non-native shells remain supported.
	OnShowSelectionBar func(SelectionBarPayload)
	OnHideSelectionBar func()
	// OnLanguageChange fires when a config save or reload switches the UI
	// language, so the shell relabels the tray and window titles.
	OnLanguageChange func()
	// OnRecordItemUse fires when a clipboard item is pasted or copied from the Omnibox.
	OnRecordItemUse func(itemID string)
	// OnRecordPromptVersion fires when a prompt template is rendered and injected or an
	// AI-upgraded draft is committed.
	OnRecordPromptVersion func(promptID, trigger, rawDraft, finalOutput, preset string)
	// OnConfigSaved fires after Clavis saved config.toml itself (Settings,
	// the first-run guide), for the host to apply what runs outside the
	// controller (trigger keys, input engine, clipboard filter): its config
	// watcher skips Clavis's own writes.
	OnConfigSaved func()
	// OnWorkflowResult receives the provider-independent execution result after
	// a workflow finishes. HUD events remain the default shell.
	OnWorkflowResult func(workflowID string, result automation.ExecutionResult, err error)
}

type clipTask struct {
	gen       uint64
	syncAfter bool
	fn        func(ctx context.Context) error
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

	// aiReview caches the diff of an AI rewrite against its source and the
	// view the user picked (see ai_review.go).
	aiReview aiReviewState

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

	ConfigStore       *config.ConfigStore
	StateMachine      *engine.StateMachine
	ClipboardStore    clipboard.Store
	ClipboardSearcher ClipboardSearcher
	SecretStore       secrets.APIKeyStore
	Bridge            bridge.HostService
	AppProvider       apps.AppProvider
	AIClient          *ai.OpenAICompatibleClient
	CLIExecutor       actions.CLIExecutor
	Glossary          engine.GlossaryEngine
	Snippets          *engine.SnippetEngine

	httpDoer              ai.HTTPDoer
	ClipboardWriter       func(text string)
	BeforeInject          func(ctx context.Context)
	OpenPath              func(path string) error
	OnRepaint             func()
	OnPresentPhase        func(phase engine.CapsulePhase)
	OnHide                func()
	OnOpenSettings        func(tab SettingsTab)
	OnOpenPromptComposer  func()
	OnClosePromptComposer func()
	OnShowIMEPill         func(label string, anchor *host.CursorAnchor)
	OnShowSelectionBar    func(SelectionBarPayload)
	OnHideSelectionBar    func()
	OnLanguageChange      func()
	OnRecordItemUse       func(itemID string)
	OnRecordPromptVersion func(promptID, trigger, rawDraft, finalOutput, preset string)
	OnConfigSaved         func()
	OnWorkflowResult      func(workflowID string, result automation.ExecutionResult, err error)

	// Decoded application icon bitmap cache
	iconMu        sync.Mutex
	appIconCache  map[string]*ui.Bitmap
	thumbCache    map[string]*ui.Bitmap
	originalCache map[string]*ui.Bitmap

	// Leader temporary ASCII IME lock state
	preLeaderIMESourceID string
	imeLockedForCapsule  bool
	// snippetUndo is the last inline expansion, revertible by an immediate
	// Backspace (see snippet_undo.go).
	snippetUndo    snippetUndoState
	skipIMERestore bool

	// Keymap hold layer reported by the host bridge (e.g. "nav", "hyper", "")
	activeKeymapLayer string

	// AI cancellation & generation guard
	aiCancel  context.CancelFunc
	aiGen     uint64
	transform *inplaceSession

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

	// Generation-based incremental clipboard mirror sync state
	clipSyncMu       sync.Mutex
	clipSynced       bool
	clipSyncedHasGen bool
	clipSyncedGen    uint64
	clipSyncedLimit  int

	// Debounced async clipboard search state
	clipBaseCtx        context.Context
	clipBaseCancel     context.CancelFunc
	clipSearchSeq      atomic.Uint64
	clipSearchMu       sync.Mutex
	clipSearchTimer    *time.Timer
	clipSearchTimerSeq uint64
	clipSearchDebounce time.Duration
	clipSearchCancel   context.CancelFunc

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

	// Active prompt slot composer session & UI handles
	promptSession         *prompt.SlotComposerSession
	promptContext         engine.CapsuleContext
	promptValidationError string
	promptUI              promptComposerUIState

	// onboarding is the first-run guide's live state (onboarding_view.go).
	onboarding onboardingState
}

// NewAppController constructs and initializes an AppController.
func NewAppController(opts Options) *AppController {
	cfgStore := opts.ConfigStore
	if cfgStore == nil {
		cfgDir := defaultConfigDir()
		cfgStore = config.NewConfigStore(cfgDir)
	}
	cfg := cfgStore.Current()
	// The candidate window reads its appearance from a package global; publish
	// it now, not on the first Capsule frame, which may be long after the
	// first composition.
	SetCandidateAppearance(cfg.Main.Appearance)

	clipStore := opts.ClipboardStore
	if clipStore == nil {
		clipStore = clipboard.NewMemoryStore(clipboard.NewPrivacyFilterFromConfig(cfg.Main.Clipboard))
	}
	clipSearcher := opts.ClipboardSearcher
	if clipSearcher == nil && clipStore != nil {
		clipSearcher = clipStore
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
	if clipSearcher != nil {
		sm.SetExternalClipboardSearch(true)
	}
	clipBaseCtx, clipBaseCancel := context.WithCancel(context.Background())

	c := &AppController{
		ConfigStore:           cfgStore,
		StateMachine:          sm,
		ClipboardStore:        clipStore,
		ClipboardSearcher:     clipSearcher,
		SecretStore:           secStore,
		Bridge:                opts.Bridge,
		AppProvider:           appProv,
		CLIExecutor:           actions.NewCLIExecutor(),
		Glossary:              glossary,
		Snippets:              snippets,
		httpDoer:              opts.HTTPDoer,
		ClipboardWriter:       opts.ClipboardWriter,
		BeforeInject:          opts.BeforeInject,
		OpenPath:              opts.OpenPath,
		OnRepaint:             opts.OnRepaint,
		OnPresentPhase:        opts.OnPresentPhase,
		OnHide:                opts.OnHide,
		OnOpenSettings:        opts.OnOpenSettings,
		OnOpenPromptComposer:  opts.OnOpenPromptComposer,
		OnClosePromptComposer: opts.OnClosePromptComposer,
		OnShowIMEPill:         opts.OnShowIMEPill,
		OnShowSelectionBar:    opts.OnShowSelectionBar,
		OnHideSelectionBar:    opts.OnHideSelectionBar,
		OnLanguageChange:      opts.OnLanguageChange,
		OnRecordItemUse:       opts.OnRecordItemUse,
		OnRecordPromptVersion: opts.OnRecordPromptVersion,
		OnConfigSaved:         opts.OnConfigSaved,
		OnWorkflowResult:      opts.OnWorkflowResult,
		appIconCache:          make(map[string]*ui.Bitmap),
		thumbCache:            make(map[string]*ui.Bitmap),
		originalCache:         make(map[string]*ui.Bitmap),
		clipDone:              make(chan struct{}),
		clipBaseCtx:           clipBaseCtx,
		clipBaseCancel:        clipBaseCancel,
		clipSearchDebounce:    defaultClipboardSearchDebounce,
		aiTestStatus:          AITestIdle,
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

	c.refreshAPIKeySource()
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
	selectionHide := c.OnHideSelectionBar
	if c.hudTimer != nil {
		c.hudTimer.Stop()
		c.hudTimer = nil
	}
	c.hudGen++
	c.mu.Unlock()
	if selectionHide != nil {
		selectionHide()
	}

	cancelSeq := c.clipSearchSeq.Add(1)
	c.cancelClipboardSearch(cancelSeq)
	if c.clipBaseCancel != nil {
		c.clipBaseCancel()
	}

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
	next := c.StateMachine.State()
	onRepaint := c.OnRepaint
	hasSearcher := c.ClipboardSearcher != nil

	var searchSeq uint64
	var searchQuery string
	var searchImmediate bool
	var cancelSearchSeq uint64
	if hasSearcher {
		if next.Phase == engine.PhaseOmnibox && next.OmniboxTab == config.TabClipboard {
			trimmedNext := strings.TrimSpace(next.OmniboxQuery)
			trimmedPrev := strings.TrimSpace(prev.OmniboxQuery)
			if trimmedNext == "" {
				if prev.Phase == engine.PhaseOmnibox && prev.OmniboxTab == config.TabClipboard && trimmedPrev != "" {
					cancelSearchSeq = c.clipSearchSeq.Add(1)
				}
			} else if ev.Type == engine.EventOpenOmnibox || ev.Type == engine.EventSwitchTab {
				searchSeq = c.clipSearchSeq.Add(1)
				searchQuery = trimmedNext
				searchImmediate = true
			} else if (ev.Type == engine.EventUpdateQuery || ev.Type == engine.EventBackspace) &&
				(prev.Phase != engine.PhaseOmnibox || prev.OmniboxTab != config.TabClipboard || trimmedNext != trimmedPrev) {
				searchSeq = c.clipSearchSeq.Add(1)
				searchQuery = trimmedNext
			}
		} else if prev.Phase == engine.PhaseOmnibox && prev.OmniboxTab == config.TabClipboard {
			cancelSearchSeq = c.clipSearchSeq.Add(1)
		}
	}
	c.mu.Unlock()

	if ev.Type == engine.EventSelectAIProfile {
		c.refreshAPIKeySource()
	}
	if cancelSearchSeq != 0 {
		c.cancelClipboardSearch(cancelSearchSeq)
	}
	if searchSeq != 0 {
		c.scheduleClipboardSearch(searchSeq, searchQuery, searchImmediate)
	}

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

	var actedClipboardID string
	if prev.Phase == engine.PhaseOmnibox {
		idx := prev.SelectedIndex
		if ev.Type == engine.EventSelectAndConfirmIndex {
			idx = ev.Index
		}
		if idx >= 0 && idx < len(prev.OmniboxItems) {
			if item := prev.OmniboxItems[idx]; item.Kind == engine.ItemClipboardEntry {
				actedClipboardID = item.ClipboardID
			}
		}
	}

	var optimizeAccepted bool
	if prev.Phase == engine.PhaseAIStreaming && prev.AIMode == "optimize" &&
		(ev.Type == engine.EventAcceptAIOutput || ev.Type == engine.EventCopyAIOutput) {
		for _, eff := range effects {
			if eff.Type == engine.EffectApplyAIOutput || eff.Type == engine.EffectCopyText {
				optimizeAccepted = true
				break
			}
		}
	}

	// A focused-field transform replaces the field only after a fresh value check.
	transformAccepted := false
	if ev.Type == engine.EventAcceptAIOutput && prev.Phase == engine.PhaseAIStreaming {
		c.mu.Lock()
		ts := c.transform
		c.mu.Unlock()
		if ts != nil && prev.AIOutput != "" {
			transformAccepted = true
			c.applyInPlaceResult(ts, prev.AIOutput)
			for i := range effects {
				if effects[i].Type == engine.EffectApplyAIOutput {
					effects[i].Type = engine.EffectClose
				}
			}
		}
	}

	for _, eff := range effects {
		if eff.Type == engine.EffectCancelAI || eff.Type == engine.EffectClose {
			c.executeEffect(eff, next)
		}
	}
	for _, eff := range effects {
		if eff.Type != engine.EffectCancelAI && eff.Type != engine.EffectClose {
			if eff.Type == engine.EffectCopyText && eff.ClipboardID == "" && actedClipboardID != "" {
				eff.ClipboardID = actedClipboardID
			}
			c.executeEffect(eff, next)
		}
	}
	if optimizeAccepted && !transformAccepted {
		trigger := c.ConfigStore.Current().Prompts.InlineOptimizePrefix
		if strings.TrimSpace(trigger) == "" {
			trigger = "//"
		}
		c.recordPromptVersion("optimize", trigger, prev.AIPrompt, prev.AIOutput, prev.AIOptimizerPreset)
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
		a.ClipboardSearchQuery != b.ClipboardSearchQuery ||
		!omniboxItemIDsEqual(a.OmniboxItems, b.OmniboxItems) ||
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

func omniboxItemIDsEqual(a, b []engine.OmniboxItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].KeyHint != b[i].KeyHint {
			return false
		}
	}
	return true
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
		onHideSelection := c.OnHideSelectionBar
		c.mu.Unlock()
		if onHide != nil {
			onHide()
		}
		if onHideSelection != nil {
			onHideSelection()
		}

	case engine.EffectStartAI:
		c.startAIStream(eff)

	case engine.EffectCancelAI:
		c.cancelAIStream()

	case engine.EffectApplyAIOutput:
		c.injectTextAsync(eff.Text, 0, 0)

	case engine.EffectPasteClipboardItem:
		if eff.ClipboardMedia != nil && eff.ClipboardMedia.IsMedia() {
			c.pasteClipboardMediaAsync(*eff.ClipboardMedia)
		} else {
			c.injectTextAsync(eff.ClipboardContent, 0, 0)
		}
		if eff.ClipboardID != "" {
			c.recordItemUse("clipboard:" + eff.ClipboardID)
		}

	case engine.EffectCopyText:
		if eff.ClipboardMedia != nil && eff.ClipboardMedia.IsMedia() {
			if err := writeClipboardMedia(*eff.ClipboardMedia); err != nil {
				c.ShowToastOrHUD(err.Error())
				return
			}
		} else {
			c.writeClipboardText(eff.Text)
		}
		if eff.ClipboardID != "" {
			c.recordItemUse("clipboard:" + eff.ClipboardID)
		}
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
		// Media GC remains on the serialized desktop poller/prune path. Running it
		// here could race a just-captured blob that has not yet been persisted.
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

	case engine.EffectOpenPromptComposer:
		if eff.PromptTemplate != nil {
			appCtx := state.Context
			if eff.Context != nil {
				appCtx = *eff.Context
			}
			c.OpenPromptComposer(*eff.PromptTemplate, appCtx)
		}
	}
}

func (c *AppController) recordItemUse(itemID string) {
	if itemID == "" {
		return
	}
	c.mu.Lock()
	fn := c.OnRecordItemUse
	c.mu.Unlock()
	if fn != nil {
		fn(itemID)
	}
}

func (c *AppController) recordPromptVersion(promptID, trigger, rawDraft, finalOutput, preset string) {
	if promptID == "" && finalOutput == "" {
		return
	}
	c.mu.Lock()
	fn := c.OnRecordPromptVersion
	c.mu.Unlock()
	if fn != nil {
		fn(promptID, trigger, rawDraft, finalOutput, preset)
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
	c.aiTestStatus = AITestIdle
	c.aiTestMessage = ""
	c.settingsStatus = i18n.Tf("settings.status.active_profile", profile.Name)
	onRepaint := c.OnRepaint
	c.mu.Unlock()

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
	// A new AI surface supersedes any pending focused-field write-back.
	if eff.Mode != "custom" {
		c.transform = nil
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

	req := ai.CompletionRequest{
		Mode:       eff.Mode,
		Prompt:     eff.Prompt,
		SourceText: eff.SourceText,
		Context:    eff.Context,
		History:    history,
		Profile:    &profCopy,
	}
	c.mu.Lock()
	transformMode := ""
	if c.transform != nil {
		transformMode = c.transform.mode
	}
	c.mu.Unlock()
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
			if eff.Context != nil {
				if eff.Context.AppName != "" {
					ctxLines = append(ctxLines, "App: "+eff.Context.AppName)
				}
				if eff.Context.WindowTitle != "" {
					ctxLines = append(ctxLines, "Window: "+eff.Context.WindowTitle)
				}
				if eff.Context.ActiveURL != "" {
					ctxLines = append(ctxLines, "URL: "+eff.Context.ActiveURL)
				}
				if eff.Context.SelectedText != "" {
					ctxLines = append(ctxLines, "Selection:\n"+eff.Context.SelectedText)
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
		req.SystemPromptOverride = map[string]string{
			"polish":    "Rewrite the text for clarity, grammar, and flow while preserving its meaning. Return only the rewritten text.",
			"translate": "Translate the text between Chinese and English naturally. Return only the translation.",
			"explain":   "Explain the text clearly and concisely. Return only the explanation.",
		}[mode]
	}

	go func() {
		defer cancel()
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
			errMsg := err.Error()
			if errors.Is(err, secrets.ErrSecretNotFound) {
				envVar := strings.TrimSpace(profCopy.APIKeyEnvVar)
				if envVar == "" {
					envVar = secrets.DefaultEnvVarForProvider(profCopy.Provider)
				}
				if envVar == "" {
					envVar = "API_KEY"
				}
				provName := strings.TrimSpace(profCopy.Provider)
				if provName == "" {
					provName = "AI"
				}
				errMsg = i18n.Tf("prompts.ai_missing_key", provName, envVar)
			}
			c.Dispatch(engine.Event{
				Type:  engine.EventAIFailed,
				Error: errMsg,
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
	c.transform = nil
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
		if task.fn != nil && (c.ClipboardStore != nil || !task.syncAfter) {
			_ = task.fn(ctx)
		}
		if task.syncAfter && c.ClipboardStore != nil && c.clipGen.Load() == task.gen {
			c.syncClipboardEntries(ctx)
			c.refreshActiveClipboardSearch(ctx)
		}
	}
}

func (c *AppController) enqueueClipboardTask(fn func(ctx context.Context) error) {
	gen := c.clipGen.Add(1)
	c.clipMu.Lock()
	if !c.clipClosed {
		c.clipQueue = append(c.clipQueue, clipTask{gen: gen, syncAfter: true, fn: fn})
		c.clipCond.Signal()
	}
	c.clipMu.Unlock()
}

func (c *AppController) enqueueClipboardSearchTask(fn func(ctx context.Context) error) {
	c.clipMu.Lock()
	if !c.clipClosed {
		c.clipQueue = append(c.clipQueue, clipTask{syncAfter: false, fn: fn})
		c.clipCond.Signal()
	}
	c.clipMu.Unlock()
}

func (c *AppController) clipboardMirrorLimit() int {
	limit := c.ConfigStore.Current().Main.Clipboard.MaxHistoryItems
	if limit <= 0 {
		limit = 200
	}
	if limit > defaultClipboardMirrorLimit {
		limit = defaultClipboardMirrorLimit
	}
	return limit
}

func (c *AppController) syncClipboardEntries(ctx context.Context) {
	if c.ClipboardStore == nil {
		return
	}
	limit := c.clipboardMirrorLimit()

	var gen uint64
	var hasGen bool
	if gt, ok := c.ClipboardStore.(clipboard.GenerationTracker); ok {
		gen = gt.Generation()
		hasGen = true
		c.clipSyncMu.Lock()
		unchanged := c.clipSynced && c.clipSyncedHasGen && c.clipSyncedGen == gen && c.clipSyncedLimit == limit
		c.clipSyncMu.Unlock()
		if unchanged {
			return
		}
	}

	items, err := c.ClipboardStore.Recent(ctx, limit)
	if err != nil {
		return
	}

	c.clipSyncMu.Lock()
	c.clipSynced = true
	c.clipSyncedHasGen = hasGen
	c.clipSyncedGen = gen
	c.clipSyncedLimit = limit
	c.clipSyncMu.Unlock()

	c.Dispatch(engine.Event{
		Type:             engine.EventSetClipboardEntries,
		ClipboardEntries: clipboardItemsToEntries(items),
	})
}

func clipboardItemsToEntries(items []clipboard.ClipboardItem) []engine.OmniboxClipboardEntry {
	entries := make([]engine.OmniboxClipboardEntry, len(items))
	for i, it := range items {
		entries[i] = engine.OmniboxClipboardEntry{
			ID:            it.ID,
			Content:       it.Content,
			SourceAppName: it.SourceAppName,
			IsPinned:      it.IsPinned,
			Media:         engineClipboardMedia(it.Media),
		}
	}
	return entries
}

func (c *AppController) cancelClipboardSearch(seq uint64) {
	c.clipSearchMu.Lock()
	if seq < c.clipSearchSeq.Load() {
		c.clipSearchMu.Unlock()
		return
	}
	if c.clipSearchTimer != nil {
		c.clipSearchTimer.Stop()
		c.clipSearchTimer = nil
	}
	if c.clipSearchCancel != nil {
		c.clipSearchCancel()
		c.clipSearchCancel = nil
	}
	c.clipSearchMu.Unlock()
}

func (c *AppController) scheduleClipboardSearch(seq uint64, query string, immediate bool) {
	c.clipSearchMu.Lock()
	if seq < c.clipSearchSeq.Load() {
		c.clipSearchMu.Unlock()
		return
	}
	if c.clipSearchTimer != nil {
		c.clipSearchTimer.Stop()
		c.clipSearchTimer = nil
	}
	if c.clipSearchCancel != nil {
		c.clipSearchCancel()
		c.clipSearchCancel = nil
	}
	c.clipSearchTimerSeq = seq
	debounce := c.clipSearchDebounce
	if immediate || debounce <= 0 {
		baseCtx := c.clipBaseCtx
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		searchCtx, cancel := context.WithCancel(baseCtx)
		c.clipSearchCancel = cancel
		c.clipSearchMu.Unlock()
		c.enqueueClipboardSearchTask(func(context.Context) error {
			c.executeClipboardSearch(searchCtx, seq, query)
			return nil
		})
		return
	}

	c.clipSearchTimer = time.AfterFunc(debounce, func() {
		c.clipSearchMu.Lock()
		if c.clipSearchSeq.Load() != seq || c.clipSearchTimerSeq != seq {
			c.clipSearchMu.Unlock()
			return
		}
		c.clipSearchTimer = nil
		baseCtx := c.clipBaseCtx
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		searchCtx, cancel := context.WithCancel(baseCtx)
		c.clipSearchCancel = cancel
		c.clipSearchMu.Unlock()

		c.enqueueClipboardSearchTask(func(context.Context) error {
			c.executeClipboardSearch(searchCtx, seq, query)
			return nil
		})
	})
	c.clipSearchMu.Unlock()
}

func (c *AppController) refreshActiveClipboardSearch(ctx context.Context) {
	st := c.State()
	if st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabClipboard {
		return
	}
	query := strings.TrimSpace(st.OmniboxQuery)
	if query == "" {
		return
	}
	seq := c.clipSearchSeq.Load()
	c.executeClipboardSearch(ctx, seq, query)
}

func (c *AppController) executeClipboardSearch(ctx context.Context, seq uint64, query string) {
	if c.clipSearchSeq.Load() != seq || ctx.Err() != nil {
		return
	}
	st := c.State()
	if st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabClipboard || strings.TrimSpace(st.OmniboxQuery) != query {
		return
	}
	c.mu.Lock()
	searcher := c.ClipboardSearcher
	c.mu.Unlock()
	if searcher == nil {
		return
	}

	var genBefore uint64
	var hasGen bool
	if gt, ok := c.ClipboardStore.(clipboard.GenerationTracker); ok {
		genBefore = gt.Generation()
		hasGen = true
	}

	items, err := searcher.Search(ctx, query, c.clipboardMirrorLimit())
	if err != nil {
		return
	}

	// Generation + query stale-result guards.
	if c.clipSearchSeq.Load() != seq || ctx.Err() != nil {
		return
	}
	if hasGen {
		if gt, ok := c.ClipboardStore.(clipboard.GenerationTracker); ok {
			if gt.Generation() != genBefore {
				return
			}
		}
	}
	stAfter := c.State()
	if stAfter.Phase != engine.PhaseOmnibox || stAfter.OmniboxTab != config.TabClipboard || strings.TrimSpace(stAfter.OmniboxQuery) != query {
		return
	}

	c.Dispatch(engine.Event{
		Type:             engine.EventSetClipboardSearchResults,
		Query:            query,
		ClipboardEntries: clipboardItemsToEntries(items),
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

// ExecuteWorkflow validates and plans a configured workflow, reports the plan via
// the normal HUD event, and starts its provider-independent runner in the
// background. Only actions already represented by the safe CLI/script adapters
// are exposed to workflows; workflow strings are never passed through a shell.
func (c *AppController) ExecuteWorkflow(workflowID string, actionCtx engine.CapsuleContext) error {
	return c.executeWorkflowContext(context.Background(), workflowID, actionCtx)
}

// RunWorkflow is an explicit-context alias for ExecuteWorkflow. It is useful to
// callers which use Run as the vocabulary for other asynchronous controller work.
func (c *AppController) RunWorkflow(ctx context.Context, workflowID string, actionCtx engine.CapsuleContext) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return c.executeWorkflowContext(ctx, workflowID, actionCtx)
}

type workflowAction struct {
	leader *config.LeaderAction
	script *config.ScriptComponentSpec
}

type workflowActionRegistry struct {
	actions     map[string]workflowAction
	descriptors automation.MapActionRegistry
}

func (r workflowActionRegistry) GetAction(id string) (automation.ActionDescriptor, bool) {
	d, ok := r.descriptors[id]
	return d, ok
}

func (c *AppController) executeWorkflowContext(parent context.Context, workflowID string, actionCtx engine.CapsuleContext) error {
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" {
		return errors.New("workflow ID cannot be empty")
	}
	cfg := c.ConfigStore.Current()
	var workflow config.Workflow
	found := false
	for _, candidate := range cfg.Workflows.Workflows {
		if candidate.ID == workflowID {
			workflow = candidate
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("workflow %q not found", workflowID)
	}
	if !workflow.Enabled {
		return fmt.Errorf("workflow %q is disabled", workflowID)
	}

	registry := c.workflowActionRegistry(cfg)
	definition := workflowDefinition(workflow)
	plan, err := automation.BuildExecutionPlanWithRegistry(definition, registry)
	if err != nil {
		c.workflowHUD(fmt.Sprintf("Workflow %s failed to plan: %v", workflowID, err))
		return err
	}
	c.workflowHUD(fmt.Sprintf("Workflow %s planned (%d steps)", workflowID, len(plan)))

	inputs := workflowInputs(workflow, actionCtx)
	actionCtxCopy := actionCtx
	go func() {
		provider := workflowProvider{controller: c, actions: registry.actions, actionCtx: actionCtxCopy}
		result, runErr := automation.NewRunner(provider).Run(parent, plan, inputs)
		c.reportWorkflow(workflow, actionCtxCopy, result, runErr)
	}()
	return nil
}

func (c *AppController) workflowActionRegistry(cfg config.ClavisConfig) workflowActionRegistry {
	registry := workflowActionRegistry{
		actions:     make(map[string]workflowAction),
		descriptors: make(automation.MapActionRegistry),
	}
	register := func(id string, action workflowAction, provider automation.ProviderType, name string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, exists := registry.actions[id]; exists {
			return
		}
		registry.actions[id] = action
		registry.descriptors[id] = automation.ActionDescriptor{ID: id, Name: name, Provider: provider, Risk: automation.RiskMedium}
	}
	var visit func([]config.LeaderNode, string)
	visit = func(nodes []config.LeaderNode, prefix string) {
		for _, node := range nodes {
			id := strings.TrimSpace(prefix + node.Key)
			if node.Action.Type == config.ActionGroup {
				visit(node.Action.Children, id+".")
				continue
			}
			action := node.Action
			if action.Type != config.ActionCLI {
				continue
			}
			register(id, workflowAction{leader: &action}, automation.ProviderCLI, node.Label)
			// Short IDs are convenient in workflows and are only aliases when they
			// cannot shadow another action.
			register(strings.TrimPrefix(id, "leader."), workflowAction{leader: &action}, automation.ProviderCLI, node.Label)
		}
	}
	visit(cfg.Leader.Nodes, "leader.")
	for i := range cfg.Components.Scripts {
		script := cfg.Components.Scripts[i]
		action := workflowAction{script: &script}
		register(script.ID, action, automation.ProviderScript, script.Title)
		register("script."+script.ID, action, automation.ProviderScript, script.Title)
	}
	return registry
}

func workflowDefinition(workflow config.Workflow) automation.WorkflowDefinition {
	definition := automation.WorkflowDefinition{ID: workflow.ID, Name: workflow.Name, Description: workflow.Description}
	for _, input := range workflow.Inputs {
		definition.Inputs = append(definition.Inputs, automation.WorkflowInput{ID: input.ID, Name: input.Name, Type: input.Type, Required: input.Required, Default: input.Default})
	}
	for _, step := range workflow.Steps {
		if !step.Enabled {
			continue
		}
		failure := automation.FailureStop
		if step.Failure == config.WorkflowFailureContinue || step.Failure == config.WorkflowFailureNotify {
			failure = automation.FailureContinue
		}
		output := automation.OutputCapture
		switch step.Output {
		case config.WorkflowOutputNone:
			output = automation.OutputDiscard
		case config.WorkflowOutputReplace:
			output = automation.OutputReplace
		case config.WorkflowOutputNotify:
			output = automation.OutputNotify
		}
		input := map[string]any{}
		if step.Input != "" {
			input["input"] = step.Input
		}
		if step.Prompt != "" {
			input["prompt"] = step.Prompt
		}
		definition.Steps = append(definition.Steps, automation.WorkflowStep{ID: step.ID, ActionID: step.Action, Input: input, DependsOn: append([]string(nil), step.DependsOn...), FailureMode: failure, OutputMode: output})
	}
	return definition
}

func workflowInputs(workflow config.Workflow, actionCtx engine.CapsuleContext) map[string]any {
	values := map[string]any{
		"selection": actionCtx.SelectedText, "selected_text": actionCtx.SelectedText,
		"clipboard": actionCtx.ClipboardPreview, "clipboard_text": actionCtx.ClipboardPreview,
		"text": actionCtx.SelectedText, "prompt": actionCtx.SelectedText,
		"app": actionCtx.AppName, "app_name": actionCtx.AppName, "bundle_id": actionCtx.BundleID,
		"url": actionCtx.ActiveURL, "window_title": actionCtx.WindowTitle,
	}
	for _, input := range workflow.Inputs {
		if _, ok := values[input.ID]; !ok {
			values[input.ID] = input.Default
		}
	}
	return values
}

type workflowProvider struct {
	controller *AppController
	actions    map[string]workflowAction
	actionCtx  engine.CapsuleContext
}

func (p workflowProvider) Invoke(ctx context.Context, invocation automation.ActionInvocation) (automation.ActionResult, error) {
	action, ok := p.actions[invocation.ActionID]
	if !ok {
		return automation.ActionResult{ActionID: invocation.ActionID, Status: automation.ResultFailed, Error: "action is not available"}, nil
	}
	if action.script != nil {
		if !action.script.Enabled {
			return automation.ActionResult{ActionID: invocation.ActionID, Status: automation.ResultSkipped, Error: "script is disabled"}, nil
		}
		result, err := actions.ExecuteScriptComponent(ctx, *action.script, p.actionCtx)
		return cliActionResult(result, err), nil
	}
	if action.leader == nil || action.leader.Type != config.ActionCLI {
		return automation.ActionResult{ActionID: invocation.ActionID, Status: automation.ResultFailed, Error: "workflow action is not a CLI or script action"}, nil
	}
	return p.invokeCLI(ctx, action.leader.Command, action.leader.Args, 0)
}

func (p workflowProvider) invokeCLI(ctx context.Context, command string, args []string, timeout time.Duration) (automation.ActionResult, error) {
	executor := actions.NewCLIExecutor()
	if p.controller != nil {
		p.controller.mu.Lock()
		executor = p.controller.CLIExecutor
		p.controller.mu.Unlock()
	}
	result, err := executor.Execute(ctx, command, args, p.actionCtx, timeout)
	return cliActionResult(result, err), nil
}

func cliActionResult(result actions.CLIExecutionResult, err error) automation.ActionResult {
	output := result.Stdout
	if output == "" {
		output = result.Stderr
	}
	actionResult := automation.ActionResult{Status: automation.ResultSucceeded, Output: output, Metadata: map[string]any{"exitCode": result.ExitCode, "stderr": result.Stderr, "timedOut": result.TimedOut}}
	if err != nil {
		actionResult.Status = automation.ResultFailed
		actionResult.Error = err.Error()
		return actionResult
	}
	if result.TimedOut || result.ExitCode != 0 {
		actionResult.Status = automation.ResultFailed
		if result.TimedOut {
			actionResult.Error = "command timed out"
		} else {
			actionResult.Error = fmt.Sprintf("command exited with code %d", result.ExitCode)
		}
	}
	return actionResult
}

func (c *AppController) reportWorkflow(workflow config.Workflow, actionCtx engine.CapsuleContext, result automation.ExecutionResult, runErr error) {
	if runErr != nil {
		c.workflowHUD(fmt.Sprintf("Workflow %s failed: %v", workflow.ID, runErr))
	} else if result.Stopped {
		message := "workflow failed"
		for _, step := range result.Steps {
			if step.Result.Status == automation.ResultFailed {
				message = fmt.Sprintf("step %s failed: %s", step.StepID, step.Result.Error)
				break
			}
		}
		c.workflowHUD(fmt.Sprintf("Workflow %s %s", workflow.ID, message))
	} else {
		c.workflowHUD(fmt.Sprintf("Workflow %s completed", workflow.ID))
		c.applyWorkflowOutput(workflow.Output, actionCtx, result)
	}
	c.mu.Lock()
	hook := c.OnWorkflowResult
	c.mu.Unlock()
	if hook != nil {
		hook(workflow.ID, result, runErr)
	}
}

func (c *AppController) workflowHUD(message string) {
	c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: message})
}

func (c *AppController) applyWorkflowOutput(mode config.WorkflowOutput, actionCtx engine.CapsuleContext, result automation.ExecutionResult) {
	if mode == config.WorkflowOutputNone || mode == config.WorkflowOutputDiscard {
		return
	}
	var output any
	for _, step := range result.Steps {
		if step.Result.Status == automation.ResultSucceeded {
			output = step.Result.Output
		}
	}
	text := strings.TrimSpace(fmt.Sprint(output))
	if text == "" {
		return
	}
	switch mode {
	case config.WorkflowOutputReplace:
		c.injectTextAsync(text, len([]rune(actionCtx.SelectedText)), 0)
	case config.WorkflowOutputInsert:
		c.injectTextAsync(text, 0, 0)
	case config.WorkflowOutputClipboard:
		c.writeClipboardText(text)
	case config.WorkflowOutputNotify:
		c.workflowHUD(text)
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
	// The key may have changed outside Clavis: look again, off the main
	// thread, where reading the keychain needs no nested event loop.
	go func() {
		if c.refreshAPIKeySource() && onRepaint != nil {
			onRepaint()
		}
	}()
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
		c.noteOnboardingLeader()
		ctx := context.Background()
		c.syncClipboardEntries(ctx)
		if len(c.State().InstalledApps) == 0 {
			c.SyncInstalledApps(ctx)
		}
		capCtx := c.resolveSelectionContext(ev.Context)
		c.Dispatch(engine.Event{
			Type:    engine.EventOpenLeader,
			Context: &capCtx,
		})

	case host.TriggerInPlaceOptimize:
		go c.startInPlaceTransform(false, "")

	case host.TriggerSelectionQuickBar:
		go c.showSelectionQuickBar()

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
		capCtx := c.resolveSelectionContext(ev.Context)
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
		// Any typed key ends the window in which Backspace undoes the last
		// expansion.
		c.disarmSnippetUndo()
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
				c.armSnippetUndo(match.TypedTrigger, renderedText, backOffset)
				break
			}
		}

	case host.TriggerSnippetBack:
		if c.tryUndoSnippet() {
			return
		}
		c.mu.Lock()
		c.Snippets.Backspace()
		c.mu.Unlock()

	case host.TriggerSnippetReset:
		// Cursor moved, app changed, IME composed, or a click landed: the typed trigger prefix
		// can no longer be relied on, so drop it instead of risking a wrong expansion. The
		// caret may no longer sit after the expansion either, so Backspace can't undo it.
		c.disarmSnippetUndo()
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
	return c.resolveContext(snap, false)
}

// resolveSelectionContext is resolveTriggerContext for triggers that open
// actions on the selection (Leader, double-tap, Capsule): when the platform
// could not read the selection, the bridge is asked to copy it.
func (c *AppController) resolveSelectionContext(snap *host.ContextSnapshot) engine.CapsuleContext {
	return c.resolveContext(snap, true)
}

// selectionProbeBudget is how long a trigger waits for the frontmost app to
// copy its selection when accessibility could not read it. The Leader
// panel takes the keyboard focus only afterwards, so the budget is short:
// an app with text selected answers the copy shortcut within a few tens of
// milliseconds, and only an app with nothing selected makes the probe wait
// all of it.
const selectionProbeBudget = 90 * time.Millisecond

// probeSelection asks a bridge that can (bridge.SelectionProber) to copy
// the frontmost app's selection; "" when it cannot or nothing is selected.
func (c *AppController) probeSelection() string {
	p, ok := c.Bridge.(bridge.SelectionProber)
	if !ok {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*selectionProbeBudget)
	defer cancel()
	text, err := p.ProbeSelection(ctx, selectionProbeBudget)
	if err != nil || strings.TrimSpace(text) == "" {
		return ""
	}
	return text
}

func (c *AppController) resolveContext(snap *host.ContextSnapshot, probe bool) engine.CapsuleContext {
	var resolved host.ContextSnapshot
	if snap != nil {
		resolved = *snap
	} else if c.Bridge != nil {
		if s, err := c.Bridge.SnapshotContext(context.Background()); err == nil {
			resolved = s
		}
	}
	if probe && resolved.SelectedText == "" && !resolved.SelectionKnown {
		resolved.SelectedText = c.probeSelection()
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
	c.refreshAPIKeySource()
	c.mu.Lock()
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
	c.refreshAPIKeySource()
	c.mu.Lock()
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
	SetCandidateAppearance(updated.Main.Appearance)
	c.mu.Lock()
	c.StateMachine.SetConfig(updated)
	if c.AIClient != nil {
		c.AIClient.Config = updated.Main.AI
	}
	c.settingsStatus = i18n.T("settings.status.saved")
	br := c.Bridge
	onRepaint := c.OnRepaint
	onLanguageChange := c.OnLanguageChange
	onConfigSaved := c.OnConfigSaved
	langChanged := syncAppearanceLanguage(updated)
	c.mu.Unlock()

	c.refreshAPIKeySource()
	if br != nil {
		br.UpdateKeymapConfig(updated.Main.Keymap)
		br.UpdateIMEConfig(updated.Main.IME, updated.IMERules)
	}
	if onConfigSaved != nil {
		onConfigSaved()
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
		SetCandidateAppearance(cfg.Main.Appearance)
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

	if ok {
		c.refreshAPIKeySource()
	}
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
