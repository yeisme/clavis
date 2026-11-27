package uiapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/yeisme/clavis/internal/apps"
	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/internal/capture"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/actions"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/automation"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/prompt"
	runtimecoord "github.com/yeisme/clavis/pkg/core/runtime"
)

// MCPManager is the minimal lifecycle contract held by the controller. MCP
// servers remain injected-only; this seam does not start configured servers.
type MCPManager interface {
	StopAll(context.Context) error
}

// Options configures an AppController.
type Options struct {
	ConfigStore              *config.ConfigStore
	DataDir                  string
	LogDir                   string
	ClipboardStore           clipboard.Store
	ClipboardSearcher        ClipboardSearcher
	AIUsageDaily             AIUsageDailyReader
	AIUsageRecorder          ai.UsageRecorder
	LexiconHitsDaily         LexiconHitsDailyReader
	LexiconTopWords          LexiconTopWordsReader
	SecretStore              secrets.APIKeyStore
	Bridge                   bridge.HostService
	AppProvider              apps.AppProvider
	HTTPDoer                 ai.HTTPDoer
	ClipboardWriter          func(text string)
	ClipboardWriterWithError func(text string) error
	BeforeInject             func(ctx context.Context)
	OpenPath                 func(path string) error
	OpenExternal             func(rawURL string) error
	RevealPath               func(path string) error
	FileSearcher             func(ctx context.Context, query string) ([]engine.FileMatch, error)
	HerdrExecutor            agentsession.CommandExecutor
	Vault                    *secrets.Vault
	Capturer                 capture.Capturer
	OnRepaint                func()
	OnPresentPhase           func(phase engine.CapsulePhase)
	OnHide                   func()
	OnOpenSettings           func(tab SettingsTab)
	OnDocumentEdited         func(edited bool)
	OnOpenPromptComposer     func()
	OnClosePromptComposer    func()
	OnShowAnnotateWindow     func()
	OnCloseAnnotateWindow    func()
	SaveCapturePath          func(pngBytes []byte) (string, error)
	AnnotateCopyWriter       func(pngBytes []byte) error
	OnShowIMEPill            func(label string, anchor *host.CursorAnchor)
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
	// a workflow finishes. HUD events remain the default shell. The hook runs
	// after the workflow lifecycle state has been finalized and may call
	// CancelWorkflow, Close, or execute another workflow synchronously.
	OnWorkflowResult func(workflowID string, result automation.ExecutionResult, err error)
	AuditLog         *automation.AuditLog
	// MCPManager is an optional lifecycle seam. The controller never auto-starts
	// configured servers; callers explicitly manage opted-in servers.
	MCPManager MCPManager
}

// uiViewState holds UI-thread widget handles and ephemeral view state.
type uiViewState struct {
	searchHandle           ui.Handle
	leaderHandle           ui.Handle
	followUpHandle         ui.Handle
	listState              ui.ListState
	mdOpts                 markdown.Options
	mdThemeKey             markdownThemeKey
	isComposing            bool
	suppressNextLeaderText string
	lastPhase              engine.CapsulePhase
	lastTab                config.CapsuleTab
	lastSelectedIdx        int
	sourceExpanded         bool
	followUpQuery          string
	apiKeyDraft            string
	settingsPage           string
	imeRuleTargetDraft     string
	imeRuleURLDraft        string
	imeRuleTitleDraft      string
	imeRuleSourceDraft     string
	imeRuleModeDraft       config.IMEMode

	// search is the view state of the search fields (search_keys.go).
	search        searchFieldState
	agentSelected int

	// Clipboard workbench focus mode & inline clear-all confirm.
	clipboardFocusMode    bool
	clipboardClearConfirm bool

	// aiReview caches the diff of an AI rewrite against its source and the
	// view the user picked (see ai_review.go).
	aiReview aiReviewState

	// Animation bookkeeping (view-side, main-thread only).
	lastExitHideDelay  time.Duration
	lastAIError        string
	aiShakeRun         uint64
	lastPreviewSnippet string
	snippetFlashRun    uint64

	// Media preview state belongs to this controller, not a package-global map.
	// It is reset when the selected Omnibox item changes and is only mutated by
	// the Capsule UI thread.
	mediaPreview             mediaPreviewState
	mediaPreviewHandle       ui.Handle
	previewDividerHoverSince time.Time
	previewDividerHot        bool
	previewDividerLastHot    bool
	previewDividerDragging   bool
	previewDividerRun        uint64

	// Pointer hover selection is tracked as index+1 (0 means none) so the zero
	// value needs no initialisation. Keyboard navigation arms a pointer position
	// and hover cannot take control again until the pointer moves at least two
	// DIP, which prevents ScrollIntoView from turning a resting pointer into a
	// synthetic hover event.
	hoverRow        int
	inputMode       selectionInputMode
	pointerArmedPos ui.Point
	lastPointerPos  ui.Point
	pointerPosKnown bool
}

type selectionInputMode uint8

const (
	inputKeyboard selectionInputMode = iota
	inputPointer
)

const pointerRearmDistance float32 = 2

// AppController coordinates Clavis state, host integration, AI streaming,
// clipboard persistence, and native MyGo UI views.
type AppController struct {
	mu               sync.Mutex
	clipboardStatsMu sync.RWMutex
	clipboardStats   clipboardStatsState

	// Owned per-controller sub-states (replaces package-global *AppController side tables).
	subStateMu sync.Mutex
	subStates  controllerSubStates

	// Controller lifetime context canceled in Close() to stop background tasks cleanly.
	lifeContext context.Context
	lifeCancel  context.CancelFunc

	ConfigStore       *config.ConfigStore
	dataDir           string
	logDir            string
	StateMachine      *engine.StateMachine
	ClipboardStore    clipboard.Store
	ClipboardSearcher ClipboardSearcher
	SecretStore       secrets.APIKeyStore
	Bridge            bridge.HostService
	AppProvider       apps.AppProvider
	AIClient          *ai.OpenAICompatibleClient
	CLIExecutor       actions.Executor
	MCPManager        MCPManager
	AuditLog          *automation.AuditLog
	// workflowRegistryOverride is an injectable registry seam for hosts that
	// enrich configured actions with runtime descriptor metadata (such as output
	// schemas). The normal production path uses workflowActionRegistry below.
	workflowRegistryOverride func(config.ClavisConfig) workflowActionRegistry
	Glossary                 engine.GlossaryEngine
	Snippets                 *engine.SnippetEngine

	httpDoer                 ai.HTTPDoer
	ClipboardWriter          func(text string)
	ClipboardWriterWithError func(text string) error
	BeforeInject             func(ctx context.Context)
	OpenPath                 func(path string) error
	OpenExternal             func(rawURL string) error
	RevealPath               func(path string) error
	FileSearcher             func(ctx context.Context, query string) ([]engine.FileMatch, error)
	OnRepaint                func()
	OnPresentPhase           func(phase engine.CapsulePhase)
	OnHide                   func()
	OnOpenSettings           func(tab SettingsTab)
	OnDocumentEdited         func(edited bool)
	OnOpenPromptComposer     func()
	OnClosePromptComposer    func()
	OnShowAnnotateWindow     func()
	OnCloseAnnotateWindow    func()
	SaveCapturePath          func(pngBytes []byte) (string, error)
	AnnotateCopyWriter       func(pngBytes []byte) error
	capturer                 capture.Capturer
	OnShowIMEPill            func(label string, anchor *host.CursorAnchor)
	OnShowSelectionBar       func(SelectionBarPayload)
	OnHideSelectionBar       func()
	OnLanguageChange         func()
	OnRecordItemUse          func(itemID string)
	OnRecordPromptVersion    func(promptID, trigger, rawDraft, finalOutput, preset string)
	OnConfigSaved            func()
	OnWorkflowResult         func(workflowID string, result automation.ExecutionResult, err error)

	// Decoded application icon bitmap cache
	iconMu              sync.Mutex
	appIconCache        map[string]*ui.Bitmap
	thumbCache          map[string]*ui.Bitmap
	thumbLoading        map[string]bool
	thumbRepaintPending bool
	thumbDecoder        func(string) *ui.Bitmap
	originalCache       map[string]*ui.Bitmap
	originalLoading     map[string]bool
	// originalTimer coalesces original-image decoding while the pointer sweeps
	// across rows: only the item it settles on is decoded.
	originalTimer *time.Timer
	originalPath  string
	originalGen   uint64

	// Leader temporary ASCII IME lock state
	preLeaderIMESourceID string
	imeLockedForCapsule  bool
	// preLeaderIMETakeover remembers whether the engine owned input before the quick panel
	// opened, so the panel can suspend takeover and the close path can restore it.
	preLeaderIMETakeover bool
	// snippetUndo is the last inline expansion, revertible by an immediate
	// Backspace (see snippet_undo.go).
	snippetUndo    snippetUndoState
	skipIMERestore bool

	// Keymap hold layer reported by the host bridge (e.g. "nav", "hyper", "")
	activeKeymapLayer string

	// AI cancellation & generation guard
	agentMu             sync.RWMutex
	herdrExecutor       agentsession.CommandExecutor
	agentSessions       []agentsession.Session
	agentWorkspaces     []agentsession.HerdrWorkspace
	agentDiagnostics    []agentsession.Diagnostic
	agentRefreshStarted bool

	vaultMu       sync.Mutex
	vault         *secrets.Vault
	vaultAudit    *secrets.MemoryAuditSink
	vaultMetaPath string

	aiCancel         context.CancelFunc
	aiDone           func()
	runtime          *runtimecoord.Registry
	runtimeCtx       context.Context
	runtimeCancel    context.CancelFunc
	aiGen            uint64
	aiOriginBundleID string
	aiOriginProcess  string
	transform        *inplaceSession

	// Workflow cancellation and lifecycle guards. A controller owns at most one
	// active workflow run; starting or stopping another run invalidates the old
	// run's completion and output.
	workflowLifecycleMu sync.Mutex
	workflowCancel      context.CancelFunc
	workflowDone        func()
	cliCancel           context.CancelFunc
	cliGeneration       uint64
	workflowRunID       uint64
	workflowTargetGen   uint64
	closed              bool
	backgroundWG        sync.WaitGroup

	// Clipboard paste operations are serialized and invalidated when the foreground
	// target changes. Clipboard writes happen before the asynchronous platform paste;
	// stale work must never send a shortcut into a newer application.
	clipboardPasteGen    uint64
	clipboardPasteCancel context.CancelFunc
	clipboardPasteDone   func()

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

	// Delayed soft-delete state (5s Undo toast before SQLite/MediaStore delete)
	clipDeleteMu         sync.Mutex
	clipPendingDelete    clipboardPendingDelete
	clipDeleteUndoWindow time.Duration

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

	// Debounced async Spotlight/file search state
	fileBaseCtx        context.Context
	fileBaseCancel     context.CancelFunc
	fileSearchSeq      atomic.Uint64
	fileSearchMu       sync.Mutex
	fileSearchTimer    *time.Timer
	fileSearchTimerSeq uint64
	fileSearchDebounce time.Duration
	fileSearchCancel   context.CancelFunc

	// Runtime diagnostics & settings status
	apiKeySource       secrets.APIKeySource
	permissionStatus   host.PermissionStatus
	monitorRunning     bool
	recoveryCount      int
	activeInputSource  host.InputSourceInfo
	inputEngineASCII   bool
	lastIMEError       string
	cachedBundleID     string
	cachedProcessName  string
	cachedAppName      string
	lastDocumentEdited bool
	aiTestStatus       AITestStatus
	aiTestMessage      string

	aiUsageMu             sync.RWMutex
	aiUsageDays           []AIUsageDay
	aiUsageLoaded         bool
	aiUsageRefreshStarted bool
	aiUsageErr            error
	aiUsageReader         AIUsageDailyReader
	aiUsageRecorder       ai.UsageRecorder

	lexiconHitsMu             sync.RWMutex
	lexiconHitDays            []LexiconHitDay
	lexiconHitsLoaded         bool
	lexiconHitsRefreshStarted bool
	lexiconHitsErr            error
	lexiconHitsReader         LexiconHitsDailyReader
	lexiconTopWordsReader     LexiconTopWordsReader
	lexiconTopWordsMu         sync.RWMutex
	lexiconTopWords           []LexiconTopWord
	lexiconTopWordsLoaded     bool
	lexiconTopWordsRefreshing bool
	lexiconTopWordsErr        error

	// Ephemeral UI state (used on the UI build thread)
	uiState uiViewState
	// uiPersist is view state saved to ui-state.json (the preview split).
	uiPersist       uiPersistState
	uiPersistSaveMu sync.Mutex

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
	// The candidate window reads its appearance and [input_engine.style] from
	// package globals; publish them now, not on the first Capsule frame, which
	// may be long after the first composition.
	SetCandidateAppearance(cfg.Main.Appearance)
	SetCandidateStyle(cfg.Main.InputEngine.Style)

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
	herdrExec := opts.HerdrExecutor
	if herdrExec == nil {
		herdrExec = agentsession.NewCommandExecutor()
	}
	capturer := opts.Capturer
	if capturer == nil {
		capturer = capture.NewCapturer()
	}
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	clipBaseCtx, clipBaseCancel := context.WithCancel(lifeCtx)
	fileBaseCtx, fileBaseCancel := context.WithCancel(lifeCtx)
	runtimeCtx, runtimeCancel := context.WithCancel(lifeCtx)

	c := &AppController{
		lifeContext:              lifeCtx,
		lifeCancel:               lifeCancel,
		ConfigStore:              cfgStore,
		dataDir:                  strings.TrimSpace(opts.DataDir),
		logDir:                   strings.TrimSpace(opts.LogDir),
		StateMachine:             sm,
		ClipboardStore:           clipStore,
		ClipboardSearcher:        clipSearcher,
		SecretStore:              secStore,
		Bridge:                   opts.Bridge,
		AppProvider:              appProv,
		CLIExecutor:              actions.NewCLIExecutor(),
		Glossary:                 glossary,
		Snippets:                 snippets,
		httpDoer:                 opts.HTTPDoer,
		ClipboardWriter:          opts.ClipboardWriter,
		ClipboardWriterWithError: opts.ClipboardWriterWithError,
		BeforeInject:             opts.BeforeInject,
		OpenPath:                 opts.OpenPath,
		OpenExternal:             opts.OpenExternal,
		RevealPath:               opts.RevealPath,
		FileSearcher:             opts.FileSearcher,
		herdrExecutor:            herdrExec,
		OnRepaint:                opts.OnRepaint,
		OnPresentPhase:           opts.OnPresentPhase,
		OnHide:                   opts.OnHide,
		OnOpenSettings:           opts.OnOpenSettings,
		OnDocumentEdited:         opts.OnDocumentEdited,
		OnOpenPromptComposer:     opts.OnOpenPromptComposer,
		OnClosePromptComposer:    opts.OnClosePromptComposer,
		OnShowAnnotateWindow:     opts.OnShowAnnotateWindow,
		OnCloseAnnotateWindow:    opts.OnCloseAnnotateWindow,
		SaveCapturePath:          opts.SaveCapturePath,
		AnnotateCopyWriter:       opts.AnnotateCopyWriter,
		capturer:                 capturer,
		OnShowIMEPill:            opts.OnShowIMEPill,
		OnShowSelectionBar:       opts.OnShowSelectionBar,
		OnHideSelectionBar:       opts.OnHideSelectionBar,
		OnLanguageChange:         opts.OnLanguageChange,
		OnRecordItemUse:          opts.OnRecordItemUse,
		OnRecordPromptVersion:    opts.OnRecordPromptVersion,
		OnConfigSaved:            opts.OnConfigSaved,
		OnWorkflowResult:         opts.OnWorkflowResult,
		AuditLog:                 opts.AuditLog,
		MCPManager:               opts.MCPManager,
		appIconCache:             make(map[string]*ui.Bitmap),
		thumbCache:               make(map[string]*ui.Bitmap),
		thumbLoading:             make(map[string]bool),
		originalCache:            make(map[string]*ui.Bitmap),
		originalLoading:          make(map[string]bool),
		clipDone:                 make(chan struct{}),
		clipBaseCtx:              clipBaseCtx,
		clipBaseCancel:           clipBaseCancel,
		clipSearchDebounce:       defaultClipboardSearchDebounce,
		fileBaseCtx:              fileBaseCtx,
		fileBaseCancel:           fileBaseCancel,
		fileSearchDebounce:       defaultFileSearchDebounce,
		aiTestStatus:             AITestIdle,
		runtime:                  runtimecoord.NewRegistry(runtimeCtx),
		runtimeCtx:               runtimeCtx,
		runtimeCancel:            runtimeCancel,
		uiState: uiViewState{
			imeRuleModeDraft: config.IMEModeASCII,
		},
	}
	sharedLearning := &lazyLearningStore{store: clipStore}
	c.aiUsageReader = opts.AIUsageDaily
	c.aiUsageRecorder = opts.AIUsageRecorder
	c.initSecretVault(opts.Vault)
	if c.aiUsageReader == nil {
		c.aiUsageReader = defaultAIUsageDailyReader(sharedLearning)
	}
	c.lexiconHitsReader = opts.LexiconHitsDaily
	if c.lexiconHitsReader == nil {
		c.lexiconHitsReader = defaultLexiconHitsDailyReader(sharedLearning)
	}
	c.lexiconTopWordsReader = opts.LexiconTopWords
	if c.lexiconTopWordsReader == nil {
		c.lexiconTopWordsReader = defaultLexiconTopWordsReader(sharedLearning)
	}
	c.clipCond = sync.NewCond(&c.clipMu)

	c.AIClient = ai.NewOpenAICompatibleClient(
		cfg.Main.AI,
		glossary,
		opts.HTTPDoer,
		c.resolveAPIKey,
	)
	c.AIClient.UsageRecorder = c.aiUsageRecorder

	c.refreshAPIKeySource()
	c.startBackground(c.refreshAgentSessions)
	c.startBackground(c.runClipboardWorker)
	return c
}

// startBackground owns a controller-scoped goroutine so Close can wait for it
// after cancellation. Test controllers and short-lived UI surfaces otherwise
// leave agent refresh work running into the next test.
func (c *AppController) startBackground(fn func()) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.backgroundWG.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.backgroundWG.Done()
		fn()
	}()
}

func defaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".clavis"
	}
	return filepath.Join(home, ".config", "clavis")
}

// Close stops background workers, cancels any running AI/workflow/async tasks,
// stops all timers, and releases owned sub-states and cached bitmaps.
func (c *AppController) Close() {
	if c == nil {
		return
	}
	c.flushUIState()
	c.cancelLifeCtx()
	c.StopAll("close")
	c.mu.Lock()
	mcpManager := c.MCPManager
	c.mu.Unlock()
	if mcpManager != nil {
		_ = mcpManager.StopAll(context.Background())
	}
	c.mu.Lock()
	if c.runtimeCancel != nil {
		c.runtimeCancel()
		c.runtimeCancel = nil
	}
	c.mu.Unlock()
	c.workflowLifecycleMu.Lock()
	if c.workflowCancel != nil {
		c.workflowCancel()
		c.workflowCancel = nil
	}
	c.workflowRunID++
	c.mu.Lock()
	if c.clipboardPasteCancel != nil {
		c.clipboardPasteCancel()
		c.clipboardPasteCancel = nil
	}
	c.clipboardPasteGen++
	c.closed = true
	c.mu.Unlock()
	c.workflowLifecycleMu.Unlock()

	c.mu.Lock()
	if c.aiCancel != nil {
		c.aiCancel()
		c.aiCancel = nil
	}
	c.aiGen++
	if c.cliCancel != nil {
		c.cliCancel()
		c.cliCancel = nil
	}
	selectionHide := c.OnHideSelectionBar
	if c.hudTimer != nil {
		c.hudTimer.Stop()
		c.hudTimer = nil
	}
	c.hudGen++
	if c.originalTimer != nil {
		c.originalTimer.Stop()
		c.originalTimer = nil
	}
	c.originalGen++
	c.originalPath = ""
	clear(c.thumbCache)
	clear(c.thumbLoading)
	clear(c.originalCache)
	clear(c.originalLoading)
	c.mu.Unlock()

	c.iconMu.Lock()
	clear(c.appIconCache)
	c.iconMu.Unlock()

	c.closeSubStates()

	if selectionHide != nil {
		selectionHide()
	}

	c.FlushPendingClipboardDelete()

	cancelSeq := c.clipSearchSeq.Add(1)
	c.cancelClipboardSearch(cancelSeq)
	if c.clipBaseCancel != nil {
		c.clipBaseCancel()
	}
	cancelFileSeq := c.fileSearchSeq.Add(1)
	c.cancelFileSearch(cancelFileSeq)
	if c.fileBaseCancel != nil {
		c.fileBaseCancel()
	}

	c.clipMu.Lock()
	if !c.clipClosed {
		c.clipClosed = true
		if c.clipCond != nil {
			c.clipCond.Broadcast()
		}
		done := c.clipDone
		c.clipMu.Unlock()
		if done != nil {
			<-done
		}
	} else {
		c.clipMu.Unlock()
	}
	c.backgroundWG.Wait()
}

// Start starts the host bridge (if configured), syncs installed applications, and refreshes initial permissions.
func (c *AppController) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = c.lifeCtx()
	}
	// Installed-app discovery can touch slow disks (/Applications, registry); run it off the
	// main thread so startup stays responsive. SyncInstalledApps itself remains synchronous
	// whenever callers invoke it directly (tests rely on that).
	if len(c.State().InstalledApps) == 0 && c.AppProvider != nil {
		c.startBackground(func() { c.SyncInstalledApps(ctx) })
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

// RegisterCancellable exposes the cancellation seam for MCP and agent adapters.
func (c *AppController) RegisterCancellable(kind runtimecoord.Kind, cancel context.CancelFunc, label string) func() {
	return c.ensureRuntime().Register(kind, cancel, label)
}

func (c *AppController) ensureRuntime() *runtimecoord.Registry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lifeContext == nil {
		c.lifeContext, c.lifeCancel = context.WithCancel(context.Background())
		if c.closed {
			c.lifeCancel()
		}
	}
	if c.runtime == nil {
		c.runtimeCtx, c.runtimeCancel = context.WithCancel(c.lifeContext)
		c.runtime = runtimecoord.NewRegistry(c.runtimeCtx)
	}
	return c.runtime
}

// StopAll cancels all registered work without waiting for providers to return.
func (c *AppController) StopAll(reason string) runtimecoord.Report {
	report := c.ensureRuntime().StopAll(reason)
	c.cancelAIStream()
	c.CancelWorkflow()
	c.mu.Lock()
	if c.clipboardPasteCancel != nil {
		c.clipboardPasteCancel()
		c.clipboardPasteCancel = nil
	}
	c.clipboardPasteGen++
	if c.cliCancel != nil {
		c.cliCancel()
		c.cliCancel = nil
	}
	c.mu.Unlock()
	return report
}

// Stop stops the host bridge and cancels any active workflow.
func (c *AppController) Stop() error {
	c.CancelWorkflow()
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
		ctx = c.lifeCtx()
	}
	appsList, err := prov.ListApps(ctx)
	if err != nil || len(appsList) == 0 || ctx.Err() != nil {
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
	appsList, err := prov.ListApps(c.lifeCtx())
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
	if c.appIconCache == nil {
		c.appIconCache = make(map[string]*ui.Bitmap)
	}
	if len(c.appIconCache) >= 256 {
		clear(c.appIconCache)
	}
	c.appIconCache[trimmed] = bmp
	c.iconMu.Unlock()

	return bmp
}

// hoverSelect moves the selection to the row under the pointer. Hovering only
// selects: it never confirms the item, so moving the pointer cannot copy, paste
// or delete anything. After keyboard navigation, a resting pointer is ignored
// until its real position moves by at least pointerRearmDistance DIP.
func (a *AppController) hoverSelect(index int, positions ...ui.Point) {
	if index < 0 {
		return
	}
	a.mu.Lock()
	if len(positions) > 0 {
		a.uiState.lastPointerPos = positions[0]
		a.uiState.pointerPosKnown = true
	}
	if a.uiState.inputMode == inputKeyboard && a.uiState.pointerPosKnown {
		if manhattanPointerDistance(a.uiState.lastPointerPos, a.uiState.pointerArmedPos) < pointerRearmDistance {
			a.mu.Unlock()
			return
		}
		a.uiState.inputMode = inputPointer
	}
	if a.uiState.hoverRow == index+1 {
		a.mu.Unlock()
		return
	}
	a.uiState.hoverRow = index + 1
	a.mu.Unlock()
	a.Dispatch(engine.Event{Type: engine.EventHoverItem, Index: index})
}

func (a *AppController) notePointerPosition(pos ui.Point) {
	a.mu.Lock()
	a.uiState.lastPointerPos = pos
	a.uiState.pointerPosKnown = true
	a.mu.Unlock()
}

func manhattanPointerDistance(a, b ui.Point) float32 {
	dx, dy := a.X-b.X, a.Y-b.Y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}

func (s *uiViewState) armKeyboardSelection() {
	s.inputMode = inputKeyboard
	if s.pointerPosKnown {
		s.pointerArmedPos = s.lastPointerPos
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

func (c *AppController) Dispatch(ev engine.Event) []engine.Effect {
	c.mu.Lock()
	// Keyboard navigation wins over a resting pointer: the row under the pointer
	// must not pull the selection back until the pointer enters another row.
	switch ev.Type {
	case engine.EventSelectNext, engine.EventSelectPrevious, engine.EventSelectIndex:
		c.uiState.armKeyboardSelection()
	case engine.EventOpenOmnibox, engine.EventSwitchTab, engine.EventUpdateQuery, engine.EventBackspace:
		c.uiState.hoverRow = 0
		c.uiState.armKeyboardSelection()
		if ev.Type == engine.EventOpenOmnibox || ev.Type == engine.EventSwitchTab || ev.Type == engine.EventUpdateQuery || ev.Type == engine.EventBackspace {
			if ev.Type != engine.EventOpenOmnibox || ev.Tab == config.TabAgent {
				c.uiState.agentSelected = 0
			}
		}
	}
	if ev.Type == engine.EventOpenLeader || ev.Type == engine.EventOpenOmnibox {
		c.ensureInstalledAppsSyncedLocked()
		// A new foreground capsule establishes a new output target. Workflow and
		// clipboard completion must not inject into a target opened after their run began.
		c.workflowTargetGen++
		if c.clipboardPasteCancel != nil {
			c.clipboardPasteCancel()
			c.clipboardPasteCancel = nil
			if c.clipboardPasteDone != nil {
				c.clipboardPasteDone()
				c.clipboardPasteDone = nil
			}
		}
		c.clipboardPasteGen++
	}
	prev := c.StateMachine.State()
	// A HUD notice must never destroy an active Capsule session. When a floating pill sink is
	// wired up and the Capsule is open (Leader / Omnibox / AI Streaming), route the notice
	// to the non-activating pill and leave the session intact. PhaseHUD still updates in place.
	if ev.Type == engine.EventShowHUD &&
		prev.Phase != engine.PhaseHidden &&
		prev.Phase != engine.PhaseHUD &&
		c.OnShowIMEPill != nil {
		onRepaint := c.OnRepaint
		c.mu.Unlock()
		c.showPillNotice(ev.Message, nil)
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
	var fileSearchSeq uint64
	var fileSearchQuery string
	var fileSearchImmediate bool
	var cancelFileSeq uint64
	if c.FileSearcher != nil {
		if next.Phase == engine.PhaseOmnibox && next.OmniboxTab == config.TabActions {
			trimmedNext := strings.TrimSpace(next.OmniboxQuery)
			trimmedPrev := strings.TrimSpace(prev.OmniboxQuery)
			needle, explicit, fileOK := engine.ParseFileSearchQuery(trimmedNext)
			nonFileCount := 0
			for _, it := range next.OmniboxItems {
				if it.Kind != engine.ItemFile && it.Kind != engine.ItemAIMode {
					nonFileCount++
				}
			}
			wantFileSearch := fileOK && (needle != "" || strings.HasPrefix(trimmedNext, "~")) && (explicit || nonFileCount < 5)
			if !wantFileSearch {
				if prev.Phase == engine.PhaseOmnibox && prev.OmniboxTab == config.TabActions && trimmedPrev != "" {
					cancelFileSeq = c.fileSearchSeq.Add(1)
				}
			} else if ev.Type == engine.EventOpenOmnibox || ev.Type == engine.EventSwitchTab {
				fileSearchSeq = c.fileSearchSeq.Add(1)
				fileSearchQuery = trimmedNext
				fileSearchImmediate = true
			} else if (ev.Type == engine.EventUpdateQuery || ev.Type == engine.EventBackspace) &&
				(prev.Phase != engine.PhaseOmnibox || prev.OmniboxTab != config.TabActions || trimmedNext != trimmedPrev) {
				fileSearchSeq = c.fileSearchSeq.Add(1)
				fileSearchQuery = trimmedNext
			}
		} else if prev.Phase == engine.PhaseOmnibox && prev.OmniboxTab == config.TabActions {
			cancelFileSeq = c.fileSearchSeq.Add(1)
		}
	}

	if prev.Phase == engine.PhaseOmnibox && prev.OmniboxTab == config.TabClipboard &&
		(next.Phase != engine.PhaseOmnibox || next.OmniboxTab != config.TabClipboard) {
		c.uiState.clipboardFocusMode = false
		c.uiState.clipboardClearConfirm = false
	}
	enteredAgent := next.Phase == engine.PhaseOmnibox && next.OmniboxTab == config.TabAgent &&
		(ev.Type == engine.EventOpenOmnibox || prev.Phase != engine.PhaseOmnibox || prev.OmniboxTab != config.TabAgent)
	c.mu.Unlock()

	if enteredAgent {
		c.AgentRefresh()
	}

	if ev.Type == engine.EventSelectAIProfile {
		c.refreshAPIKeySource()
	}
	if cancelSearchSeq != 0 {
		c.cancelClipboardSearch(cancelSearchSeq)
	}
	if searchSeq != 0 {
		c.scheduleClipboardSearch(searchSeq, searchQuery, searchImmediate)
	}
	if cancelFileSeq != 0 {
		c.cancelFileSearch(cancelFileSeq)
	}
	if fileSearchSeq != 0 {
		c.scheduleFileSearch(fileSearchSeq, fileSearchQuery, fileSearchImmediate)
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
		if eff.Type == engine.EffectStopAll || eff.Type == engine.EffectCancelAI || eff.Type == engine.EffectClose {
			c.executeEffect(eff, next)
		}
	}
	for _, eff := range effects {
		if eff.Type != engine.EffectStopAll && eff.Type != engine.EffectCancelAI && eff.Type != engine.EffectClose {
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
	onRepaint := c.OnRepaint
	c.mu.Unlock()

	cfg := c.ConfigStore.Current()
	capsuleOpen := phase != engine.PhaseHidden
	if c.hasPillSink() && (capsuleOpen || cfg.Main.IME.ShowFloatingPill) {
		c.showPillNotice(msg, nil)
		if onRepaint != nil {
			onRepaint()
		}
		return
	}

	c.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: msg})
}

// notifyInPlace surfaces a transient notice without transitioning an active
// Capsule phase (e.g. PhaseOmnibox -> PhaseHUD).
func (c *AppController) notifyInPlace(message string) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return
	}
	if c.hasPillSink() {
		c.showPillNotice(msg, nil)
	}
	c.triggerRepaint()
}

func stopKindLabel(kind runtimecoord.Kind) string {
	switch kind {
	case runtimecoord.AIStream, runtimecoord.AISuggestion, runtimecoord.AIConnectionTest:
		return "AI"
	case runtimecoord.MCPRequest:
		return "MCP"
	case runtimecoord.AgentWait:
		return "Agent"
	case runtimecoord.WorkflowRun:
		return "工作流"
	case runtimecoord.ClipboardPaste:
		return "剪贴板"
	default:
		return string(kind)
	}
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

	case engine.EffectActivateClipboardItem:
		if eff.ClipboardActivation != nil {
			c.activateClipboardItem(*eff.ClipboardActivation)
		}

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
		if eff.Mode == "ocr" && eff.ClipboardMedia != nil && eff.ClipboardMedia.IsMedia() {
			if err := c.stageClipboardMediaForOCR(*eff.ClipboardMedia); err != nil {
				c.Dispatch(engine.Event{
					Type:  engine.EventAIFailed,
					Error: err.Error(),
				})
				return
			}
		}
		c.startAIStream(eff)

	case engine.EffectCancelAI:
		c.cancelAIStream()

	case engine.EffectStopAll:
		report := c.StopAll("user")
		if len(report.Operations) > 0 && state.Phase != engine.PhaseHidden {
			seen := make(map[string]struct{})
			labels := make([]string, 0, len(report.Operations))
			for _, op := range report.Operations {
				label := stopKindLabel(op.Kind)
				if _, ok := seen[label]; ok {
					continue
				}
				seen[label] = struct{}{}
				labels = append(labels, label)
			}
			c.ShowToastOrHUD("已停止 " + strings.Join(labels, " / "))
		}

	case engine.EffectApplyAIOutput:
		c.injectAIOutputAsync(eff.Text)

	case engine.EffectPasteClipboardItem:
		itemID := eff.ClipboardID
		pOpts := pasteOptions{keepOpen: eff.KeepOpen}
		if eff.ClipboardMedia != nil && eff.ClipboardMedia.IsMedia() {
			c.pasteClipboardMediaAsyncWithOptions(*eff.ClipboardMedia, pOpts, func() {
				if itemID != "" {
					c.recordItemUse("clipboard:" + itemID)
				}
			})
		} else {
			c.pasteClipboardTextAsyncWithOptions(eff.ClipboardContent, pOpts, func() {
				if itemID != "" {
					c.recordItemUse("clipboard:" + itemID)
				}
			})
		}

	case engine.EffectCopyText:
		if eff.Sensitive {
			label := strings.TrimSpace(eff.SecretLabel)
			if label == "" {
				label = "Clavis Generated Credential"
			}
			res, err := c.ExecuteClipboardAction(ClipboardCopyRequest{
				Origin:      ClipboardCopyOriginUser,
				Action:      config.ClipboardActionCopySensitive,
				Item:        clipboard.ClipboardItem{Content: eff.Text},
				Sensitive:   true,
				SecretLabel: label,
			})
			if err != nil {
				c.ShowToastOrHUD(err.Error())
				return
			}
			if res.AutoClearTTL > 0 {
				c.ShowToastOrHUD(fmt.Sprintf("%s (%ds TTL)", i18n.T("hud.copied"), int(res.AutoClearTTL.Seconds())))
			}
			return
		}
		if eff.ClipboardMedia != nil && eff.ClipboardMedia.IsMedia() {
			if err := writeClipboardMedia(*eff.ClipboardMedia); err != nil {
				c.ShowToastOrHUD(err.Error())
				return
			}
		} else if err := c.writeClipboardText(eff.Text); err != nil {
			c.ShowToastOrHUD(err.Error())
			return
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
			err := c.ClipboardStore.Delete(ctx, id)
			if err == nil {
				c.invalidateClipboardStats()
			}
			return err
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

	case engine.EffectRunWorkflow:
		var actionCtx engine.CapsuleContext
		if eff.Context != nil {
			actionCtx = *eff.Context
		} else {
			actionCtx = state.Context
		}
		if err := c.ExecuteWorkflow(eff.WorkflowID, actionCtx); err != nil {
			c.workflowHUD(fmt.Sprintf("Workflow %s failed: %v", eff.WorkflowID, err))
		}

	case engine.EffectBindAppIMERule:
		c.bindAppIMERule(eff, state)

	case engine.EffectRevealPath:
		target := strings.TrimSpace(eff.Text)
		if target == "" {
			return
		}
		if c.RevealPath != nil {
			if err := c.RevealPath(target); err != nil {
				c.ShowToastOrHUD(err.Error())
			}
			return
		}
		if mygo.App.IsReady() {
			mygo.Shell.ShowItemInFolder(target)
			return
		}
		if c.OpenPath != nil {
			_ = c.OpenPath(filepath.Dir(target))
		}

	case engine.EffectOpenPath:
		target := strings.TrimSpace(eff.Text)
		if target == "" {
			return
		}
		if c.OpenPath != nil {
			if err := c.OpenPath(target); err != nil {
				c.ShowToastOrHUD(err.Error())
			}
			return
		}
		if mygo.App.IsReady() {
			if err := mygo.Shell.OpenPath(target); err != nil {
				c.ShowToastOrHUD(err.Error())
			}
		}

	case engine.EffectOpenExternal:
		target := strings.TrimSpace(eff.Text)
		if target != "" {
			c.openExternalURL(target)
		}

	case engine.EffectFocusHerdrTarget:
		target := strings.TrimSpace(eff.Text)
		if target == "" || c.isClosed() {
			return
		}
		if eff.Mode == "workspace" {
			go c.FocusHerdrWorkspace(target)
			return
		}
		lifeCtx := c.lifeCtx()
		go func() {
			hc := agentsession.NewHerdrController(c.currentHerdrExecutor())
			ctx, cancel := context.WithTimeout(lifeCtx, 2*time.Second)
			defer cancel()
			if res, err := hc.FocusPane(ctx, target); err != nil && lifeCtx.Err() == nil {
				c.ShowToastOrHUD(nonEmptyOr(res.Message, err.Error()))
			}
		}()

	case engine.EffectOpenPromptComposer:
		if eff.PromptTemplate != nil {
			appCtx := state.Context
			if eff.Context != nil {
				appCtx = *eff.Context
			}
			c.OpenPromptComposer(*eff.PromptTemplate, appCtx)
		}

	case engine.EffectBlockClipboardApp:
		targetID := strings.TrimSpace(eff.BundleID)
		if targetID == "" {
			targetID = strings.TrimSpace(eff.Text)
		}
		if targetID == "" {
			return
		}
		c.blockClipboardApp(targetID, strings.TrimSpace(eff.Text))

	case engine.EffectSaveClipboardAsSnippet:
		c.openClipboardContentAsSnippet(eff.Text)

	case engine.EffectOpenInTerminal:
		if target := strings.TrimSpace(eff.Text); target != "" {
			c.openDirectoryInTerminal(target)
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
			if c.hudGen != gen || c.closed || c.StateMachine == nil || c.StateMachine.State().Phase != engine.PhaseHUD {
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

// HideSelectionBar dismisses the floating selection action bar if a host callback is wired.
func (c *AppController) HideSelectionBar() {
	if c == nil {
		return
	}
	c.mu.Lock()
	fn := c.OnHideSelectionBar
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// handleKeyBindingAction runs the action a [[keymap.bindings]] chord resolved to. The action
// registry is closed set, so an unknown ID is a config bug reported at load time, not here.
func (c *AppController) handleKeyBindingAction(action string) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "ai_optimize":
		go c.startInPlaceTransform(false, "optimize")
	case "ai_explain":
		go c.startInPlaceTransform(false, "explain")
	case "ai_translate":
		go c.startInPlaceTransform(false, "translate")
	case "ai_polish":
		go c.startInPlaceTransform(false, "polish")
	case "ai_continue":
		go c.startAIContinue()
	case "toggle_ime":
		c.toggleInlineIMEForced()
	}
}

// OpenOmnibox syncs clipboard history, installed apps, and frontmost context before opening the Omnibox.
func (c *AppController) OpenOmnibox(tab config.CapsuleTab, query string) {
	ctx := c.lifeCtx()
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
	ctx := c.lifeCtx()
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
		c.setClipboardFocusMode(false)
		ctx := c.lifeCtx()
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

	case host.TriggerKeyBinding:
		c.handleKeyBindingAction(ev.Action)

	case host.TriggerSelectionQuickBar:
		go c.showSelectionQuickBar()

	case host.TriggerCapsuleToggle:
		c.setClipboardFocusMode(false)
		phase := c.State().Phase
		if phase == engine.PhaseLeader || phase == engine.PhaseOmnibox || phase == engine.PhaseAIStreaming {
			c.Dispatch(engine.Event{Type: engine.EventDismiss})
			return
		}
		ctx := c.lifeCtx()
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
		// Maccy-style: the dedicated hotkey jumps straight to Clipboard History
		// in Focus Mode (hides 6-tab chrome, assigns ⌘1..9 to rows); pressing it
		// again while that tab is showing closes the Capsule.
		state := c.State()
		if state.Phase == engine.PhaseOmnibox && state.OmniboxTab == config.TabClipboard {
			c.setClipboardFocusMode(false)
			c.Dispatch(engine.Event{Type: engine.EventDismiss})
			return
		}
		ctx := c.lifeCtx()
		c.syncClipboardEntries(ctx)
		if len(c.State().InstalledApps) == 0 {
			c.SyncInstalledApps(ctx)
		}
		c.setClipboardFocusMode(true)
		capCtx := c.resolveTriggerContext(ev.Context)
		c.Dispatch(engine.Event{
			Type:    engine.EventOpenOmnibox,
			Tab:     config.TabClipboard,
			Context: &capCtx,
		})

	case host.TriggerInputEngineToggle:
		c.toggleInlineIMEForced()
		return

	case host.TriggerScreenshotAnnotate:
		go c.CaptureAndAnnotate()
		return

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
	ctx, cancel := context.WithTimeout(c.lifeCtx(), 2*selectionProbeBudget)
	defer cancel()
	text, err := p.ProbeSelection(ctx, selectionProbeBudget)
	if err != nil || strings.TrimSpace(text) == "" {
		return ""
	}
	return text
}

func (c *AppController) resolveContext(snap *host.ContextSnapshot, probe bool) engine.CapsuleContext {
	var resolved host.ContextSnapshot
	lifeCtx := c.lifeCtx()
	if snap != nil {
		resolved = *snap
	} else if c.Bridge != nil {
		if s, err := c.Bridge.SnapshotContext(lifeCtx); err == nil {
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
		if src, err := c.Bridge.CurrentInputSource(lifeCtx); err == nil && src.ID != "" {
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
