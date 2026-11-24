package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/apps"
	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/internal/daemon"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/internal/storage"
	internalsync "github.com/yeisme/clavis/internal/sync"
	"github.com/yeisme/clavis/internal/watcher"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/automation"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
	coresync "github.com/yeisme/clavis/pkg/core/sync"
	"github.com/yeisme/clavis/uiapp"

	// MyGo controls and menus speak the app language once the zh-Hans
	// catalog is compiled in.
	_ "github.com/egoist/mygo/locales/zh-Hans"
)

const (
	appName                   = "Clavis"
	keychainService           = "com.yeisme.clavis"
	clipPollInterval          = 400 * time.Millisecond
	clipPruneEvery            = 5 * time.Minute
	imePillFadeOut            = 160 * time.Millisecond
	imePillOffsetX            = 8.0
	imePillOffsetY            = 6.0
	defaultPromptHistoryLimit = 500
)

var (
	bgShutdownWaitTimeout      = 2 * time.Second
	daemonTelemetryLogInterval = 5 * time.Minute
	lookupUserFullNameFn       = lookupCurrentUserFullName
)

func main() {
	if hasSelfCheckFlag(os.Args[1:]) {
		if err := runSelfCheck(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "Clavis self-check failed: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if hasSmokeCheckFlag(os.Args[1:]) {
		if err := runSmokeCheck(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "Clavis smoke-check failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	mygo.App.SetName(appName)
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnWindowAllClosed(func() {})

	app := newDesktopApp()

	mygo.App.OnSecondInstance(func([]string, string) {
		app.openLeader()
	})
	mygo.App.WhenReady(func() {
		app.start()
	})
	// MyGo closes every window before it quits, and Clavis's windows only
	// hide when closed (see closeHides): they let the close through while a
	// quit runs, or tray Quit and App.Relaunch would be canceled.
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { app.quitting.Store(true) })
	mygo.App.OnQuitCanceled(func() { app.quitting.Store(false) })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		app.shutdown()
	})

	if err := mygo.App.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Clavis failed to start: %v\n", err)
		os.Exit(1)
	}
}

func hasSmokeCheckFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--smoke-check" {
			return true
		}
	}
	return false
}

// runSmokeCheck validates core config initialization and platform bridge capabilities
// in an isolated temporary directory without mutating ~/.config/clavis.
func runSmokeCheck(w io.Writer) error {
	cfg := config.DefaultConfig()
	if tmpDir, err := os.MkdirTemp("", "clavis-smoke-*"); err == nil {
		defer os.RemoveAll(tmpDir)
		store := config.NewConfigStore(tmpDir)
		if loaded, loadErr := store.LoadOrInitialize(); loadErr == nil {
			cfg = loaded
		}
	}

	br := bridge.NewHostService(
		bridge.WithTriggerConfig(cfg.Main.Trigger),
		bridge.WithKeymapConfig(cfg.Main.Keymap),
		bridge.WithIMEConfig(cfg.Main.IME, cfg.IMERules),
	)
	_ = br.Capabilities()

	_, err := fmt.Fprintf(
		w,
		"Clavis smoke-check ok (leader=%s, os=%s, arch=%s)\n",
		cfg.Main.Trigger.LeaderKey,
		runtime.GOOS,
		runtime.GOARCH,
	)
	return err
}

func resolveConfigDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "clavis")
	}
	if cfgDir, err := os.UserConfigDir(); err == nil && cfgDir != "" {
		return filepath.Join(cfgDir, "clavis")
	}
	return ".clavis"
}

type dynamicBridgeUpdater interface {
	UpdateTriggerConfig(cfg config.TriggerConfig)
	UpdateKeymapConfig(cfg config.KeymapConfig)
	UpdateIMEConfig(imeCfg config.IMEConfig, rules config.IMERulesConfig)
}

type desktopApp struct {
	mu            sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
	cfgStore      *config.ConfigStore
	sqliteStore   *storage.SQLiteClipboardStore
	learningStore *storage.LearningStore
	// learningWriter is the only path hook, commit and UI callers use to
	// learn: RAM mirror at once, SQLite in coalesced background batches.
	learningWriter       *storage.LearningWriter
	blocklistStore       *storage.UserBlocklistStore
	syncStateStore       *internalsync.SQLiteStateStore
	clipStore            clipboard.Store
	mediaStore           *clipboard.MediaStore
	auditLog             *automation.AuditLog
	hostBridge           bridge.HostService
	controller           *uiapp.AppController
	supervisor           *daemon.ResidentSupervisor
	frecencySyncer       *daemon.FrecencySyncer
	configWatcher        *watcher.ConfigWatcher
	dictionaryWatcher    *watcher.DictionaryWatcher
	capsulePanel         *mygo.Window
	promptPanel          *mygo.Window
	imePillPanel         *mygo.Window
	inputUI              *inputEngineUI
	settingsWin          *mygo.Window
	annotateWin          *mygo.Window
	selectionBar         *mygo.Window
	selectionBarPayload  uiapp.SelectionBarPayload
	tray                     *mygo.Tray
	permMenuItem             *mygo.MenuItem
	healthMenuItem           *mygo.MenuItem
	loginMenuItem            *mygo.MenuItem
	trayLabelItems           map[string]*mygo.MenuItem
	permKnown                bool
	permStatus               host.PermissionStatus
	permErr                  error
	permCaps                 host.Capabilities
	bridgeRunning            bool
	shuttingDown             bool
	bgCtx                    context.Context
	bgCancel                 context.CancelFunc
	bgWG                     sync.WaitGroup
	fallbackAccelerators     map[string]host.TriggerKind
	sessionAnchor            *host.CursorAnchor
	// capsuleRevealed is set once the capsule is visibly on screen for the
	// current session (after a Leader's reveal delay), and cleared when the
	// window actually hides.
	capsuleRevealed          bool
	imePillLabel             string
	imePillTimer             *time.Timer
	capsuleRevealTimer       *time.Timer
	capsuleHideTimer         *time.Timer
	imePillGen               atomic.Uint64
	frecencyGen              atomic.Uint64
	presentSeq               atomic.Uint64
	hideSeq                  atomic.Uint64
	presentTransitionUntil   atomic.Int64
	lastPanelHideNano        atomic.Int64
	lastTelemetryStatus      string
	lastTelemetryCompactions uint64
	lastTelemetrySelfHeals   uint64
	lastTelemetryLogAt       time.Time
	shutdownOnce             sync.Once
	quitting                 atomic.Bool

	// The first-run guide (onboarding.go) and the per-machine data
	// directory that records it was done.
	onboardingWin  *mygo.Window
	onboardingOpen atomic.Bool
	dataDir        string
	// bridgeStarting keeps two callers from starting the bridge at once
	// (startBridgeAndFallbackShortcut runs on the main thread, the config
	// watcher and the permission watcher).
	bridgeStarting atomic.Bool
}

var setAppLocale = func(tag string) {
	mygo.App.SetLocale(tag)
}

func sharedAIUsageDailyReader(store *storage.LearningStore) uiapp.AIUsageDailyReader {
	return func(ctx context.Context, n int) ([]uiapp.AIUsageDay, error) {
		days, err := store.AIUsageDaily(ctx, n)
		if err != nil {
			return nil, err
		}
		out := make([]uiapp.AIUsageDay, len(days))
		for i, day := range days {
			out[i] = uiapp.AIUsageDay{
				Day:          day.Day,
				Requests:     day.Requests,
				InputTokens:  day.InputTokens,
				OutputTokens: day.OutputTokens,
			}
		}
		return out, nil
	}
}

func sharedLexiconHitsDailyReader(store *storage.LearningStore) uiapp.LexiconHitsDailyReader {
	return func(ctx context.Context, n int) ([]uiapp.LexiconHitDay, error) {
		days, err := store.LexiconHitsDaily(ctx, n)
		if err != nil {
			return nil, err
		}
		out := make([]uiapp.LexiconHitDay, len(days))
		for i, day := range days {
			out[i] = uiapp.LexiconHitDay{
				Day:    day.Day,
				Hits:   day.Hits,
				Misses: day.Misses,
			}
		}
		return out, nil
	}
}

func sharedLexiconTopWordsReader(store *storage.LearningStore) uiapp.LexiconTopWordsReader {
	return func(ctx context.Context, n int) ([]uiapp.LexiconTopWord, error) {
		words, err := store.LexiconTopWords(ctx, n)
		if err != nil {
			return nil, err
		}
		out := make([]uiapp.LexiconTopWord, len(words))
		for i, word := range words {
			out[i] = uiapp.LexiconTopWord{
				Word:     word.Word,
				UseCount: word.UseCount,
			}
		}
		return out, nil
	}
}

func newDesktopApp() *desktopApp {
	return &desktopApp{}
}

// initDesktopSyncStatus opens the local per-device sync state database at
// <userDataDir>/sync-state.db and returns a read-only SyncStatusReader backed
// by that state store and an optional configured/default shared sync directory.
func initDesktopSyncStatus(ctx context.Context, userDataDir string) (*internalsync.SQLiteStateStore, uiapp.SyncStatusReader) {
	if strings.TrimSpace(userDataDir) == "" {
		return nil, nil
	}
	statePath := filepath.Join(userDataDir, "sync-state.db")
	stateStore, err := internalsync.OpenSQLiteStateStore(ctx, statePath)
	if err != nil {
		return nil, func(context.Context) (coresync.StatusSnapshot, host.OperationResult, error) {
			return coresync.StatusSnapshot{
				AllowedClasses: coresync.Phase1SyncPlan().AllowedClasses(),
			}, host.OperationResult{
				State:      host.StateUnknown,
				Code:       "state_unavailable",
				ObservedAt: time.Now().UTC(),
			}, err
		}
	}
	syncDir := strings.TrimSpace(os.Getenv("CLAVIS_SYNC_DIR"))
	if syncDir == "" {
		candidate := filepath.Join(userDataDir, "sync-dir")
		if info, statErr := os.Lstat(candidate); statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			syncDir = candidate
		}
	}
	return stateStore, internalsync.NewDirStatusReader(stateStore, syncDir)
}

// resolveCurrentUserFullName resolves the OS user's display name without spawning
// macOS-only subprocesses on Linux or Windows. On darwin it tries `id -F` first
// and falls back to os/user.Current().Name; on other platforms it uses
// os/user.Current().Name directly.
func resolveCurrentUserFullName(
	ctx context.Context,
	goos string,
	runDarwinID func(context.Context) (string, error),
	lookupCurrentUser func() (*user.User, error),
) string {
	if goos == "darwin" && runDarwinID != nil {
		if out, err := runDarwinID(ctx); err == nil {
			if name := strings.TrimSpace(out); name != "" {
				return name
			}
		}
	}
	if lookupCurrentUser != nil {
		if u, err := lookupCurrentUser(); err == nil && u != nil {
			name := strings.TrimSpace(u.Name)
			if comma := strings.IndexByte(name, ','); comma >= 0 {
				name = strings.TrimSpace(name[:comma])
			}
			if name != "" {
				return name
			}
		}
	}
	return ""
}

func lookupCurrentUserFullName(ctx context.Context) string {
	return resolveCurrentUserFullName(
		ctx,
		runtime.GOOS,
		func(c context.Context) (string, error) {
			out, err := exec.CommandContext(c, "id", "-F").Output()
			return string(out), err
		},
		user.Current,
	)
}

// seedConfiguredUserWords loads pinned configured words once the learning store is ready.
// The full-name lookup is best effort and never delays startup beyond its short timeout.
func seedConfiguredUserWords(ctx context.Context, cfgStore *config.ConfigStore, cfg config.ClavisConfig, store *storage.LearningStore) bool {
	if store == nil {
		return cfg.Main.InputEngine.SeededFullName
	}
	storage.InjectUserWords(ctx, store, cfg.Main.InputEngine.UserWords)
	if !cfg.Main.InputEngine.AutoSeedFullName || cfg.Main.InputEngine.SeededFullName {
		return cfg.Main.InputEngine.SeededFullName
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	name := strings.TrimSpace(lookupUserFullNameFn(lookupCtx))
	if !storage.HasHanzi(name) {
		return false
	}
	if len(storage.InjectUserWords(lookupCtx, store, []string{name})) > 0 {
		return false
	}
	updated := cfg.Main
	updated.InputEngine.SeededFullName = true
	seen := false
	for _, word := range updated.InputEngine.UserWords {
		if strings.TrimSpace(word) == name {
			seen = true
			break
		}
	}
	if !seen {
		updated.InputEngine.UserWords = append(updated.InputEngine.UserWords, name)
	}
	if cfgStore != nil {
		if err := cfgStore.SaveMain(updated); err != nil {
			return false
		}
	}
	return true
}

// ensureLaunchAgent installs the launchd keep-alive job on first run and after the app moves
// to a new path. Failures are non-fatal: the app still works, it just won't self-heal.
func (a *desktopApp) ensureLaunchAgent() {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil && resolved != "" {
		exe = resolved
	}
	path, err := bridge.LaunchAgentPath()
	if err != nil {
		return
	}
	// Reinstall when the plist is missing or points at a stale executable path.
	if data, readErr := os.ReadFile(path); readErr == nil && strings.Contains(string(data), exe) {
		return
	}
	_, _ = bridge.InstallLaunchAgent(exe)
}

func (a *desktopApp) start() {
	ctx, cancel := context.WithCancel(context.Background())

	cfgDir := resolveConfigDir()
	cfgStore := config.NewConfigStore(cfgDir)
	cfg, err := cfgStore.LoadOrInitialize()
	if err != nil {
		cfg = config.DefaultConfig()
	}

	// Keep the process alive. macOS terminates accessory apps it considers idle, and an input
	// method that can vanish is not an input method: the user silently drops back to the system
	// IME. KeepAlive restarts only after an abnormal exit, so Quit still means quit.
	a.ensureLaunchAgent()
	// Resolve the UI language before the first frame renders: an explicit
	// [appearance].language wins; "auto" follows the OS preferred languages.
	i18n.SetPreferredLanguagesProvider(mygo.App.PreferredLanguages)
	i18n.SetLanguage(i18n.ResolveLanguage(cfg.Main.Appearance.Language, i18n.PreferredLanguages()))
	mygo.App.SetLanguages("en", "zh-Hans")
	if tag := i18n.Current().Tag(); tag != "" {
		setAppLocale(tag)
	}

	var sqliteStore *storage.SQLiteClipboardStore
	var learningStore *storage.LearningStore
	var learningWriter *storage.LearningWriter
	var blocklistStore *storage.UserBlocklistStore
	var syncStateStore *internalsync.SQLiteStateStore
	var syncStatusReader uiapp.SyncStatusReader
	var clipStore clipboard.Store
	var mediaStore *clipboard.MediaStore
	var dataDir string
	hadData := false
	if userDataDir, err := mygo.App.Path(mygo.PathUserData); err == nil && userDataDir != "" {
		dataDir = userDataDir
		syncStateStore, syncStatusReader = initDesktopSyncStatus(ctx, userDataDir)
		// Blobs live next to the DB; an in-memory store gets no media (nothing to reference them).
		mediaStore = clipboard.NewMediaStore(filepath.Join(userDataDir, "clipboard-media"))
		dbPath := filepath.Join(userDataDir, "clipboard.db")
		// A database from an earlier launch: not a first run (planOnboarding).
		_, statErr := os.Stat(dbPath)
		hadData = statErr == nil
		if s, openErr := storage.NewSQLiteClipboardStoreFromConfig(ctx, dbPath, cfg.Main.Clipboard); openErr == nil {
			sqliteStore = s
			clipStore = s
			_, _ = s.PruneExpired(ctx, cfg.Main.Clipboard.RetentionDays, cfg.Main.Clipboard.MaxHistoryItems)
			if ls, lsErr := storage.NewLearningStore(s.DB()); lsErr == nil {
				learningStore = ls
				learningWriter = storage.NewLearningWriter(ls, storage.LearningWriterOptions{})
			}
			if bs, bsErr := storage.NewUserBlocklistStore(s.DB()); bsErr == nil {
				blocklistStore = bs
			}
		}
	}
	if learningStore != nil {
		seedConfiguredUserWords(ctx, cfgStore, cfg, learningStore)
		cfg = cfgStore.Current()
	}
	if clipStore == nil {
		clipStore = clipboard.NewMemoryStore(clipboard.NewPrivacyFilterFromConfig(cfg.Main.Clipboard))
	}

	// Keys live in a 0600 file, not the keychain. macOS binds a keychain item to the code
	// signature that stored it; a rebuilt locally signed app no longer matches, so every read
	// raised a SecurityAgent authorisation dialog — mid-typing, on the AI path. The keychain
	// stays reachable only to migrate keys stored by an older build.
	keychainStore := secrets.NewMyGoSecretsStore(keychainService)
	fileStore := secrets.NewFileStore(secrets.DefaultKeysPath(cfgDir))
	secretStore := &secrets.LayeredStore{File: fileStore, Keychain: keychainStore}
	// Keychain fallback stays off: the key now lives in the 0600 file, and reading the
	// keychain from the AI path is exactly what raised an authorisation dialog on every
	// keystroke after this app was rebuilt. Migration was done once by hand.
	appProvider := apps.NewMyGoAppProvider()
	hostBridge := bridge.NewHostService(
		bridge.WithTriggerConfig(cfg.Main.Trigger),
		bridge.WithKeymapConfig(cfg.Main.Keymap),
		bridge.WithIMEConfig(cfg.Main.IME, cfg.IMERules),
		bridge.WithOnInputSourceChanged(func(info host.InputSourceInfo) {
			a.mu.Lock()
			ctrl := a.controller
			a.mu.Unlock()
			if ctrl == nil {
				return
			}
			ctrl.NotifyIMESwitch(info, nil)
		}),
		bridge.WithOnIMESwitchFailed(func(err error) {
			a.mu.Lock()
			ctrl := a.controller
			a.mu.Unlock()
			if ctrl == nil {
				return
			}
			ctrl.NotifyIMESwitchFailed(err)
		}),
		bridge.WithOnKeymapLayerChanged(func(layer string) {
			a.mu.Lock()
			ctrl := a.controller
			a.mu.Unlock()
			if ctrl == nil {
				return
			}
			ctrl.NotifyKeymapLayer(layer)
		}),
	)

	auditLog := &automation.AuditLog{}
	var aiUsageRecorder ai.UsageRecorder
	if learningWriter != nil {
		aiUsageRecorder = learningWriter
	}
	var aiUsageDaily uiapp.AIUsageDailyReader
	var lexiconHitsDaily uiapp.LexiconHitsDailyReader
	var lexiconTopWords uiapp.LexiconTopWordsReader
	if learningStore != nil {
		aiUsageDaily = sharedAIUsageDailyReader(learningStore)
		lexiconHitsDaily = sharedLexiconHitsDailyReader(learningStore)
		lexiconTopWords = sharedLexiconTopWordsReader(learningStore)
	}

	controller := uiapp.NewAppController(uiapp.Options{
		ConfigStore:      cfgStore,
		ClipboardStore:   clipStore,
		AIUsageDaily:     aiUsageDaily,
		AIUsageRecorder:  aiUsageRecorder,
		LexiconHitsDaily: lexiconHitsDaily,
		LexiconTopWords:  lexiconTopWords,
		SecretStore:      secretStore,
		Bridge:           hostBridge,
		AppProvider:      appProvider,
		AuditLog:         auditLog,
		ClipboardWriter: func(text string) {
			mygo.Clipboard.WriteText(text)
		},
		ClipboardWriterWithError: func(text string) error {
			return mygo.Clipboard.Write(transfer.TextData(text))
		},
		BeforeInject: a.beforeInject,
		OpenPath: func(p string) error {
			return mygo.Shell.OpenPath(p)
		},
		OnRepaint:             a.handleRepaint,
		OnPresentPhase:        a.handlePresentPhase,
		OnHide:                a.handleHide,
		OnOpenSettings:        a.handleOpenSettings,
		OnOpenPromptComposer:  a.handleOpenPromptComposer,
		OnClosePromptComposer: a.handleClosePromptComposer,
		OnShowAnnotateWindow:  a.handleShowAnnotateWindow,
		OnCloseAnnotateWindow: a.handleCloseAnnotateWindow,
		OnShowIMEPill:         a.handleShowIMEPill,
		OnLanguageChange:      a.handleLanguageChange,
		OnRecordItemUse:       a.handleRecordItemUse,
		OnRecordPromptVersion: a.handleRecordPromptVersion,
		OnConfigSaved:         a.applySavedConfig,
		OnShowSelectionBar:    a.handleShowSelectionBar,
		OnHideSelectionBar:    a.handleHideSelectionBar,
	})
	if syncStatusReader != nil {
		controller.SetSyncStatusReader(syncStatusReader)
	}

	initW, initH := uiapp.PreferredCapsuleSize(engine.PhaseLeader, cfg.Main.Appearance)
	surfaceSpec := func(height int, elevation int, blur mygo.Vibrancy, dismiss uiapp.SurfaceDismissPolicy, focusRestore uiapp.SurfaceFocusRestore) uiapp.SurfaceSpec {
		return uiapp.FloatingSurfaceSpecWithElevation(cfg.Main.Appearance, height, elevation, blur, dismiss, focusRestore)
	}
	capsuleSpec := surfaceSpec(initH, uiapp.SurfaceElevationPanel, mygo.VibrancyGlass, uiapp.DismissExplicit|uiapp.DismissEscape|uiapp.DismissOutsideClick, uiapp.FocusPanel)
	capsulePanel := mygo.NewWindow(uiapp.FloatingSurface(mygo.WindowOptions{
		Title:         appName,
		Width:         initW,
		Height:        initH,
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		Transparent:   true,
		Vibrancy:      mygo.VibrancyGlass,
		DisableResize: true,
		AlwaysOnTop:   true,
		SkipTaskbar:   true,
		Content:       ui.View(controller.CapsuleView),
	}, capsuleSpec))
	uiapp.BindFloatingSurface(capsulePanel, capsuleSpec, uiapp.SurfaceHooks{
		Dismiss: func() { controller.Dispatch(engine.Event{Type: engine.EventDismiss}) },
		CanDismiss: func() bool {
			if a.isInPresentTransition() {
				return false
			}
			phase := controller.State().Phase
			return phase != engine.PhaseHidden && phase != engine.PhaseHUD
		},
		Quitting: a.quitting.Load,
	})

	promptW, promptH := uiapp.PreferredPromptComposerPanelSize(2)
	promptSpec := surfaceSpec(promptH, uiapp.SurfaceElevationPanel, mygo.VibrancyGlass, uiapp.DismissExplicit|uiapp.DismissEscape|uiapp.DismissOutsideClick, uiapp.FocusPanel)
	promptPanel := mygo.NewWindow(uiapp.FloatingSurface(mygo.WindowOptions{
		Title:         i18n.T("prompts.composer_title"),
		Width:         promptW,
		Height:        promptH,
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		Transparent:   true,
		Vibrancy:      mygo.VibrancyGlass,
		DisableResize: true,
		AlwaysOnTop:   true,
		SkipTaskbar:   true,
		Content:       ui.View(controller.PromptComposerView),
	}, promptSpec))
	uiapp.BindFloatingSurface(promptPanel, promptSpec, uiapp.SurfaceHooks{
		Dismiss:    func() { controller.ClosePromptComposer() },
		CanDismiss: func() bool { return !a.isInPresentTransition() && controller.PromptComposerOpen() },
		Quitting:   a.quitting.Load,
	})

	pillW, pillH := uiapp.PreferredIMEPillSize()
	pillSpec := surfaceSpec(pillH, uiapp.SurfaceElevationIndicator, mygo.VibrancyGlass, uiapp.DismissExplicit, uiapp.FocusNever)
	imePillPanel := mygo.NewWindow(uiapp.FloatingSurface(mygo.WindowOptions{
		Title:         i18n.T("tray.ime_window"),
		Width:         pillW,
		Height:        pillH,
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		Transparent:   true,
		Vibrancy:      mygo.VibrancyGlass,
		DisableResize: true,
		AlwaysOnTop:   true,
		SkipTaskbar:   true,
		Content: ui.View(func(c *ui.Context) {
			uiapp.IMEPillView(c, a.currentIMEPillLabel())
		}),
	}, pillSpec))
	uiapp.BindFloatingSurface(imePillPanel, pillSpec, uiapp.SurfaceHooks{
		Dismiss: imePillPanel.Hide, Quitting: a.quitting.Load,
	})

	sbW, sbH := uiapp.PreferredSelectionBarWidth(nil), 34
	selectionSpec := surfaceSpec(sbH, uiapp.SurfaceElevationRaised, mygo.VibrancyGlass, uiapp.DismissExplicit, uiapp.FocusNever)
	selectionBar := mygo.NewWindow(uiapp.FloatingSurface(mygo.WindowOptions{
		Title:         "Clavis Selection",
		Width:         sbW,
		Height:        sbH,
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		Transparent:   true,
		Vibrancy:      mygo.VibrancyGlass,
		DisableResize: true,
		AlwaysOnTop:   true,
		SkipTaskbar:   true,
		Content: ui.View(func(c *ui.Context) {
			cfg := controller.Config()
			uiapp.SelectionBarViewWithController(c, a.selectionBarSnapshot(), cfg.Main.Appearance, controller)
		}),
	}, selectionSpec))
	uiapp.BindFloatingSurface(selectionBar, selectionSpec, uiapp.SurfaceHooks{
		Dismiss: selectionBar.Hide, Quitting: a.quitting.Load,
	})

	settingsSpec := surfaceSpec(640, uiapp.SurfaceElevationFlat, mygo.VibrancyNone, uiapp.DismissExplicit, uiapp.FocusPreviousApp)
	settingsSpec.Radius = uiapp.SurfaceRadius{Kind: uiapp.SurfaceRadiusSystem}
	settingsWin := mygo.NewWindow(uiapp.FloatingSurface(mygo.WindowOptions{
		Title:     i18n.T("tray.settings_window"),
		Width:     960,
		Height:    640,
		MinWidth:  760,
		MinHeight: 500,
		Hidden:    true,
		StateKey:  "settings",
		Content:   ui.View(controller.SettingsView),
	}, settingsSpec))
	uiapp.BindFloatingSurface(settingsWin, settingsSpec, uiapp.SurfaceHooks{
		Dismiss: settingsWin.Hide, RestoreFocus: a.restorePreviousAppFocus, Quitting: a.quitting.Load,
	})

	onboardingSpec := surfaceSpec(740, uiapp.SurfaceElevationFlat, mygo.VibrancyNone, uiapp.DismissExplicit, uiapp.FocusPreviousApp)
	onboardingSpec.Radius = uiapp.SurfaceRadius{Kind: uiapp.SurfaceRadiusSystem}
	onboardingWin := mygo.NewWindow(uiapp.FloatingSurface(mygo.WindowOptions{
		Title:     i18n.T("tray.onboarding_window"),
		Width:     600,
		Height:    740,
		MinWidth:  520,
		MinHeight: 520,
		Hidden:    true,
		Content:   ui.View(controller.OnboardingView),
	}, onboardingSpec))
	// Closing the guide finishes it, as Get Started does; a quit (a restart
	// for Input Monitoring) closes it without, so it shows again after.
	uiapp.BindFloatingSurface(onboardingWin, onboardingSpec, uiapp.SurfaceHooks{
		Dismiss: a.finishOnboarding, RestoreFocus: a.restorePreviousAppFocus, Quitting: a.quitting.Load,
	})
	controller.SetOnboardingHooks(uiapp.OnboardingHooks{
		Finish:   a.finishOnboardingAndRestore,
		Relaunch: func() { mygo.App.Relaunch() },
	})

	frecencySyncer := daemon.NewFrecencySyncer(
		func(now time.Time) map[string]int {
			a.mu.Lock()
			ls := a.learningStore
			a.mu.Unlock()
			return ls.FrecencySnapshot(now)
		},
		a.dispatchFrecencyScores,
	)

	sup := daemon.NewResidentSupervisor(cfg.Main.Daemon)
	if sqliteStore != nil {
		sup.RegisterCompactionHook(daemon.NewSQLiteCompactionHook(sqliteStore.DB()))
	}
	sup.RegisterCompactionHook(daemon.NewCacheTrimHook(appProvider.InvalidateCache))
	if learningStore != nil {
		sup.RegisterCompactionHook(func(hCtx context.Context) error {
			a.mu.Lock()
			ctrl := a.controller
			a.mu.Unlock()
			maxHist := defaultPromptHistoryLimit
			if ctrl != nil {
				maxHist = promptHistoryRetentionLimit(ctrl.Config().Main.Clipboard)
			}
			err := learningStore.PruneHistory(hCtx, maxHist)
			if fErr := learningStore.PruneFrecency(hCtx, storage.DefaultMaxFrecencyRows, storage.DefaultFrecencyRetentionWindow); err == nil {
				err = fErr
			}
			a.syncFrecency(hCtx)
			return err
		})
	}
	sup.RegisterWatchdogProbe(daemon.NewBridgeWatchdogProbeWithHeal(daemon.BridgeLivenessInput{
		IsRunning: func() bool {
			a.mu.Lock()
			running := a.bridgeRunning
			a.mu.Unlock()
			return running
		},
		TapRecoveryCount: hostBridge.TapRecoveryCount,
	}, a.healBridge, daemon.DefaultBridgeHealCooldown))
	sup.SetOnHealthCheck(a.handleDaemonHealthSnapshot)

	bgCtx, bgCancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.ctx = ctx
	a.cancel = cancel
	a.bgCtx = bgCtx
	a.bgCancel = bgCancel
	a.cfgStore = cfgStore
	a.sqliteStore = sqliteStore
	a.learningStore = learningStore
	a.learningWriter = learningWriter
	a.blocklistStore = blocklistStore
	a.syncStateStore = syncStateStore
	a.clipStore = clipStore
	a.mediaStore = mediaStore
	a.auditLog = auditLog
	a.hostBridge = hostBridge
	a.controller = controller
	a.supervisor = sup
	a.frecencySyncer = frecencySyncer
	a.capsulePanel = capsulePanel
	a.promptPanel = promptPanel
	a.imePillPanel = imePillPanel
	a.settingsWin = settingsWin
	a.selectionBar = selectionBar
	a.onboardingWin = onboardingWin
	a.dataDir = dataDir
	a.mu.Unlock()

	a.setupTray()
	a.startBridgeAndFallbackShortcut(cfg.Main.Trigger)
	if ext, ok := hostBridge.(bridge.ExtendedHostService); ok {
		ext.UpdateDaemonShortcut(cfg.Main.Daemon.InPlaceOptimizeShortcut)
	}
	sup.Start(ctx)
	a.wireInputEngineLearning(hostBridge, learningStore)
	a.syncInputEngineBlocklist(hostBridge, blocklistStore)
	_ = a.setupInputEngine(ctx, hostBridge, cfg.Main.InputEngine, controller.SuggestInputCandidates)

	cw := watcher.NewConfigWatcher(cfgStore, watcher.WithOnReload(func(watcher.ReloadResult) {
		a.applyConfigReload()
	}))
	if err := cw.StartWatching(ctx); err == nil {
		a.mu.Lock()
		a.configWatcher = cw
		a.mu.Unlock()
	}
	dw := watcher.NewDictionaryWatcher(watcher.UserDictionaryDir(), func() {
		a.mu.Lock()
		br := a.hostBridge
		ctrl := a.controller
		a.mu.Unlock()
		if br == nil || ctrl == nil {
			return
		}
		if h, ok := br.(bridge.InputEngineHost); ok {
			h.ReloadInputEngine(ctrl.Config().Main.InputEngine)
		}
	})
	if err := dw.Start(ctx); err == nil {
		a.mu.Lock()
		a.dictionaryWatcher = dw
		a.mu.Unlock()
	}

	// Installed-app discovery runs inside ctrl.Start in the background so the main-thread
	// startup path never blocks on slow /Applications scans.
	a.goBG(func(bgCtx context.Context) { a.syncFrecency(bgCtx) })
	a.goBG(func(bgCtx context.Context) { a.runClipboardPoller(bgCtx) })
	a.goBG(a.watchPermissions)
	a.goTrack(func(context.Context) { a.refreshPermissionsAsync() })
	// The guide is also the permission flow. A machine that finished the guide without
	// granting Accessibility/Input Monitoring would otherwise never be asked again: the
	// marker says done, so every later launch is silent and the keyboard features stay
	// dead with no explanation. Re-show the guide while a required grant is missing.
	if planOnboarding(dataDir, hadData, time.Now()) || a.missingRequiredPermissions() {
		a.showOnboarding()
	}
}

// missingRequiredPermissions reports whether a grant the keyboard features depend on is
// still absent. It is best-effort: an unavailable bridge or a query error counts as
// "not missing", so a transient failure never nags the user with a guide.
func (a *desktopApp) missingRequiredPermissions() bool {
	br := a.hostBridge
	if br == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, err := br.Permissions(ctx)
	if err != nil {
		return false
	}
	return permissionsRequiredMissing(status)
}

// permissionsRequiredMissing is the pure rule behind missingRequiredPermissions, kept
// separable so the launch-time guide decision stays unit-testable without a bridge.
func permissionsRequiredMissing(status host.PermissionStatus) bool {
	return !status.Accessibility || !status.InputMonitoring
}

// closeHides is the close handler of a window that only hides: Clavis lives
// in the tray and keeps its windows for the next time. While the app quits
// the close goes through: MyGo closes every window first, and one that
// refuses cancels the quit.
func (a *desktopApp) closeHides(hide func()) func(*mygo.CloseEvent) {
	return func(e *mygo.CloseEvent) {
		if a.quitting.Load() {
			return
		}
		e.PreventDefault()
		hide()
	}
}

// surfaceWindows returns all windows owned by the desktop shell so focus
// restoration can avoid hiding the app while any surface remains open.
func (a *desktopApp) surfaceWindows() []*mygo.Window {
	a.mu.Lock()
	capsule := a.capsulePanel
	prompt := a.promptPanel
	pill := a.imePillPanel
	selection := a.selectionBar
	settings := a.settingsWin
	onboarding := a.onboardingWin
	annotate := a.annotateWin
	var inputPanel *mygo.Window
	if a.inputUI != nil {
		inputPanel = a.inputUI.panel
	}
	a.mu.Unlock()
	return []*mygo.Window{capsule, prompt, pill, selection, settings, onboarding, annotate, inputPanel}
}

func (a *desktopApp) hasVisibleSurface(isVisible func(*mygo.Window) bool) bool {
	for _, win := range a.surfaceWindows() {
		if win != nil && isVisible(win) {
			return true
		}
	}
	return false
}

// restorePreviousAppFocus returns focus after a document-style surface closes.
// MyGo has no API for activating an arbitrary external bundle, so the safe
// fallback is to hide Clavis and let macOS reveal the previous application.
// Never do this while another Clavis surface is visible, because App.Hide
// would hide that surface as a side effect.
func (a *desktopApp) restorePreviousAppFocus() {
	a.mu.Lock()
	shuttingDown := a.shuttingDown
	a.mu.Unlock()
	if shuttingDown || a.quitting.Load() {
		return
	}
	if a.hasVisibleSurface((*mygo.Window).IsVisible) {
		return
	}
	if mygo.App.IsReady() {
		mygo.App.Hide()
	}
}

func (a *desktopApp) finishOnboardingAndRestore() {
	a.finishOnboarding()
	a.restorePreviousAppFocus()
}

func (a *desktopApp) setupTray() {
	// Monochrome template glyphs beside the tray items, tinted by the OS.
	menuIcon := func(kind uiapp.TrayMenuIconKind) *mygo.MenuIcon {
		icon, err := mygo.NewMenuIcon(uiapp.GenerateTrayMenuIconPNG(kind, 16), true)
		if err != nil {
			return nil
		}
		return icon
	}
	permItem := &mygo.MenuItem{
		Label: i18n.T("tray.permissions_checking"),
		Click: func(*mygo.MenuItem, *mygo.Window) {
			a.openSettings(uiapp.SettingsTabPermissions)
		},
	}
	telemetrySummary := a.daemonTelemetrySummary()
	healthItem := &mygo.MenuItem{
		Label:    "Daemon: " + telemetrySummary,
		Disabled: true,
	}
	loginItem := &mygo.MenuItem{
		Label:   i18n.T("tray.open_at_login"),
		Type:    mygo.MenuItemCheckbox,
		Checked: mygo.App.OpenAtLogin(),
		Click: func(item *mygo.MenuItem, _ *mygo.Window) {
			want := item.IsChecked()
			if err := mygo.App.SetOpenAtLogin(want); err != nil {
				item.SetChecked(!want)
			}
		},
	}

	menu := mygo.NewMenu([]*mygo.MenuItem{
		permItem,
		healthItem,
		mygo.Separator(),
		{
			Label: i18n.T("tray.open_capsule"),
			Icon:  menuIcon(uiapp.TrayMenuIconCapsule),
			Click: func(*mygo.MenuItem, *mygo.Window) {
				a.openOmnibox(config.TabActions)
			},
		},
		{
			Label: i18n.T("tray.clipboard_history"),
			Icon:  menuIcon(uiapp.TrayMenuIconClipboard),
			Click: func(*mygo.MenuItem, *mygo.Window) {
				a.openOmnibox(config.TabClipboard)
			},
		},
		mygo.Separator(),
		{
			Label: i18n.T("common.reload"),
			Icon:  menuIcon(uiapp.TrayMenuIconReload),
			Click: func(*mygo.MenuItem, *mygo.Window) {
				a.reloadConfiguration()
			},
		},
		{
			Label: i18n.T("settings.configs.open_dir"),
			Icon:  menuIcon(uiapp.TrayMenuIconFolder),
			Click: func(*mygo.MenuItem, *mygo.Window) {
				a.mu.Lock()
				ctrl := a.controller
				a.mu.Unlock()
				if ctrl != nil {
					_ = ctrl.OpenConfigDirectory()
				}
			},
		},
		{
			Label: i18n.T("tray.settings"),
			Icon:  menuIcon(uiapp.TrayMenuIconSettings),
			Click: func(*mygo.MenuItem, *mygo.Window) {
				a.openSettings(uiapp.SettingsTabGeneral)
			},
		},
		{
			Label: i18n.T("tray.welcome"),
			Icon:  menuIcon(uiapp.TrayMenuIconGuide),
			Click: func(*mygo.MenuItem, *mygo.Window) {
				a.showOnboarding()
			},
		},
		mygo.Separator(),
		loginItem,
		mygo.Separator(),
		{
			Label: i18n.T("tray.quit"),
			Icon:  menuIcon(uiapp.TrayMenuIconQuit),
			Click: func(*mygo.MenuItem, *mygo.Window) {
				mygo.App.Quit()
			},
		},
	})

	// Keep the localized items addressable so a language switch can relabel
	// them without rebuilding the tray. permItem is relabeled from the last
	// observed permission status rather than reset to permissions_checking.
	labelled := map[string]*mygo.MenuItem{
		"open_capsule":      itemByLabel(menu, i18n.T("tray.open_capsule")),
		"clipboard_history": itemByLabel(menu, i18n.T("tray.clipboard_history")),
		"reload":            itemByLabel(menu, i18n.T("common.reload")),
		"open_config":       itemByLabel(menu, i18n.T("settings.configs.open_dir")),
		"settings":          itemByLabel(menu, i18n.T("tray.settings")),
		"welcome":           itemByLabel(menu, i18n.T("tray.welcome")),
		"quit":              itemByLabel(menu, i18n.T("tray.quit")),
		"open_at_login":     loginItem,
	}
	a.mu.Lock()
	a.trayLabelItems = labelled
	a.mu.Unlock()

	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "clavis-tray",
		Icon:           uiapp.GenerateTrayIconPNG(32),
		IconIsTemplate: true,
		ToolTip:        "Clavis · " + telemetrySummary,
		Menu:           menu,
	})
	if err != nil {
		return
	}

	a.mu.Lock()
	a.tray = tray
	a.permMenuItem = permItem
	a.healthMenuItem = healthItem
	a.loginMenuItem = loginItem
	a.mu.Unlock()
}

func formatDaemonTelemetrySummary(snap daemon.HealthSnapshot) string {
	status := snap.HealthStatus
	if status == "" {
		if snap.Healthy {
			status = daemon.HealthStatusHealthy
		} else {
			status = daemon.HealthStatusStopped
		}
	}
	return fmt.Sprintf(
		"status=%s wpm=%.1f cpm=%.0f p50=%.0fus p99=%.0fus compactions=%d self_heals=%d mem=%.1fMB goroutines=%d",
		status,
		snap.CurrentWPM,
		snap.CurrentCharsPerMinute,
		snap.P50LatencyUs,
		snap.P99LatencyUs,
		snap.CompactionRuns,
		snap.SelfHealCount,
		snap.AllocMB,
		snap.NumGoroutine,
	)
}

func (a *desktopApp) daemonTelemetrySummary() string {
	a.mu.Lock()
	sup := a.supervisor
	a.mu.Unlock()
	if sup == nil {
		return formatDaemonTelemetrySummary(daemon.HealthSnapshot{HealthStatus: daemon.HealthStatusStopped})
	}
	return formatDaemonTelemetrySummary(sup.Snapshot())
}

func (a *desktopApp) handleDaemonHealthSnapshot(snap daemon.HealthSnapshot) {
	summary := formatDaemonTelemetrySummary(snap)
	now := time.Now()

	a.mu.Lock()
	if a.shuttingDown {
		a.mu.Unlock()
		return
	}
	shouldLog := a.lastTelemetryLogAt.IsZero() ||
		snap.HealthStatus != a.lastTelemetryStatus ||
		snap.CompactionRuns != a.lastTelemetryCompactions ||
		snap.SelfHealCount != a.lastTelemetrySelfHeals ||
		now.Sub(a.lastTelemetryLogAt) >= daemonTelemetryLogInterval
	if shouldLog {
		a.lastTelemetryStatus = snap.HealthStatus
		a.lastTelemetryCompactions = snap.CompactionRuns
		a.lastTelemetrySelfHeals = snap.SelfHealCount
		a.lastTelemetryLogAt = now
	}
	hasTray := a.tray != nil || a.healthMenuItem != nil
	a.mu.Unlock()

	if shouldLog {
		fmt.Fprintf(os.Stderr, "Clavis daemon health: %s\n", summary)
	}
	if !hasTray || !mygo.App.IsReady() {
		return
	}
	a.goTrack(func(ctx context.Context) {
		if ctx.Err() != nil || !mygo.App.IsReady() {
			return
		}
		mygo.RunOnMain(func() {
			a.mu.Lock()
			tray := a.tray
			item := a.healthMenuItem
			shuttingDown := a.shuttingDown
			a.mu.Unlock()
			if shuttingDown {
				return
			}
			if tray != nil {
				tray.SetToolTip("Clavis · " + summary)
			}
			if item != nil {
				item.SetLabel("Daemon: " + summary)
			}
		})
	})
}

// healBridge attempts a bounded restart of the host bridge when the watchdog
// observes bridgeRunning == false. It passes the long-lived appCtx to ctrl.Start
// so the restarted bridge is not canceled when healCtx's short deadline expires.
func (a *desktopApp) healBridge(healCtx context.Context) error {
	if healCtx != nil && healCtx.Err() != nil {
		return healCtx.Err()
	}
	a.mu.Lock()
	shuttingDown := a.shuttingDown
	ctrl := a.controller
	appCtx := a.ctx
	running := a.bridgeRunning
	a.mu.Unlock()
	if shuttingDown || ctrl == nil || appCtx == nil || appCtx.Err() != nil {
		return context.Canceled
	}
	if running {
		return nil
	}
	if !a.bridgeStarting.CompareAndSwap(false, true) {
		return nil
	}
	defer a.bridgeStarting.Store(false)

	a.mu.Lock()
	shuttingDown = a.shuttingDown
	running = a.bridgeRunning
	a.mu.Unlock()
	if shuttingDown {
		return context.Canceled
	}
	if running {
		return nil
	}

	_ = ctrl.Stop()
	if err := ctrl.Start(appCtx); err != nil {
		fmt.Fprintf(os.Stderr, "Clavis watchdog bridge self-heal failed: %v\n", err)
		return err
	}
	a.mu.Lock()
	if a.shuttingDown {
		a.mu.Unlock()
		_ = ctrl.Stop()
		return context.Canceled
	}
	a.bridgeRunning = true
	a.mu.Unlock()

	trigger := ctrl.Config().Main.Trigger
	if mygo.App.IsReady() {
		a.goTrack(func(ctx context.Context) {
			if ctx.Err() != nil || !mygo.App.IsReady() {
				return
			}
			mygo.RunOnMain(func() {
				a.syncFallbackShortcuts(trigger)
			})
		})
	} else {
		a.syncFallbackShortcuts(trigger)
	}
	fmt.Fprintf(os.Stderr, "Clavis watchdog bridge self-heal succeeded\n")
	return nil
}

func (a *desktopApp) startBridgeAndFallbackShortcut(trigger config.TriggerConfig) {
	a.mu.Lock()
	ctrl := a.controller
	ctx := a.ctx
	running := a.bridgeRunning
	a.mu.Unlock()
	if ctrl == nil || ctx == nil {
		return
	}

	if !running {
		if !a.bridgeStarting.CompareAndSwap(false, true) {
			return // another caller is starting it and sets the fallbacks
		}
		if err := ctrl.Start(ctx); err == nil {
			a.mu.Lock()
			a.bridgeRunning = true
			a.mu.Unlock()
		} else {
			// Keep startup failures observable; permission refreshes can call this
			// method again after the user grants access.
			fmt.Fprintf(os.Stderr, "Clavis bridge start failed: %v\n", err)
		}
		a.bridgeStarting.Store(false)
	}
	a.syncFallbackShortcuts(trigger)
}

// syncFallbackShortcuts registers the capsule and clipboard shortcuts with
// MyGo when the bridge cannot provide them (not running, or no global
// hotkeys), and drops them when it can.
func (a *desktopApp) syncFallbackShortcuts(trigger config.TriggerConfig) {
	a.mu.Lock()
	br := a.hostBridge
	running := a.bridgeRunning
	a.mu.Unlock()
	caps := host.Capabilities{}
	if br != nil {
		caps = br.Capabilities()
	}
	if running && caps.GlobalHotkeys {
		a.unregisterFallbackShortcuts()
		return
	}
	a.registerFallbackShortcuts(planFallbackShortcuts(trigger))
}

// fallbackShortcut binds one MyGo accelerator to a trigger kind.
type fallbackShortcut struct {
	Accelerator string
	Kind        host.TriggerKind
}

// planFallbackShortcuts converts trigger config into the de-duplicated set of MyGo accelerators
// used when the host bridge cannot provide global hotkeys. Capsule wins over Clipboard when both
// resolve to the same accelerator; unparsable or empty shortcuts are skipped.
func planFallbackShortcuts(trigger config.TriggerConfig) []fallbackShortcut {
	candidates := []fallbackShortcut{
		{Accelerator: toMyGoAccelerator(trigger.CapsuleShortcut), Kind: host.TriggerCapsuleToggle},
		{Accelerator: toMyGoAccelerator(trigger.ClipboardShortcut), Kind: host.TriggerClipboardToggle},
	}
	if acc := toMyGoAccelerator(trigger.ScreenshotShortcut); acc != "" {
		candidates = append(candidates, fallbackShortcut{Accelerator: acc, Kind: host.TriggerScreenshotAnnotate})
	}
	seen := make(map[string]struct{}, len(candidates))
	out := make([]fallbackShortcut, 0, len(candidates))
	for _, c := range candidates {
		if c.Accelerator == "" {
			continue
		}
		if _, dup := seen[c.Accelerator]; dup {
			continue
		}
		seen[c.Accelerator] = struct{}{}
		out = append(out, c)
	}
	return out
}

func (a *desktopApp) registerFallbackShortcuts(want []fallbackShortcut) {
	if !mygo.App.IsReady() {
		return
	}
	wanted := make(map[string]host.TriggerKind, len(want))
	for _, w := range want {
		wanted[w.Accelerator] = w.Kind
	}

	a.mu.Lock()
	prev := a.fallbackAccelerators
	a.mu.Unlock()

	next := make(map[string]host.TriggerKind, len(want))
	// Drop accelerators that are no longer wanted or whose action changed.
	for acc, kind := range prev {
		if wk, ok := wanted[acc]; ok && wk == kind && mygo.GlobalShortcut.IsRegistered(acc) {
			next[acc] = kind
			continue
		}
		mygo.GlobalShortcut.Unregister(acc)
	}
	for _, w := range want {
		if _, ok := next[w.Accelerator]; ok {
			continue
		}
		kind := w.Kind
		if err := mygo.GlobalShortcut.Register(w.Accelerator, func() {
			a.fireTrigger(kind)
		}); err == nil {
			next[w.Accelerator] = kind
		}
	}

	a.mu.Lock()
	a.fallbackAccelerators = next
	a.mu.Unlock()
}

func (a *desktopApp) unregisterFallbackShortcuts() {
	a.mu.Lock()
	prev := a.fallbackAccelerators
	a.fallbackAccelerators = nil
	a.mu.Unlock()
	if !mygo.App.IsReady() {
		return
	}
	for acc := range prev {
		mygo.GlobalShortcut.Unregister(acc)
	}
}

func (a *desktopApp) openLeader() {
	a.mu.Lock()
	ctrl := a.controller
	sup := a.supervisor
	a.mu.Unlock()
	if sup != nil {
		sup.RecordInteraction()
	}
	if ctrl == nil {
		return
	}
	a.goTrack(func(context.Context) {
		ctrl.HandleTriggerEvent(host.TriggerEvent{
			Kind:        host.TriggerLeaderKey,
			TimestampMs: time.Now().UnixMilli(),
		})
	})
}

func (a *desktopApp) openOmnibox(tab config.CapsuleTab) {
	a.mu.Lock()
	ctrl := a.controller
	sup := a.supervisor
	a.mu.Unlock()
	if sup != nil {
		sup.RecordInteraction()
	}
	if ctrl == nil {
		return
	}
	a.goTrack(func(context.Context) {
		ctrl.OpenOmnibox(tab, "")
	})
}

// fireTrigger routes a fallback-shortcut press through the same path as bridge hotkeys.
func (a *desktopApp) fireTrigger(kind host.TriggerKind) {
	a.mu.Lock()
	ctrl := a.controller
	sup := a.supervisor
	a.mu.Unlock()
	if sup != nil {
		sup.RecordInteraction()
	}
	if ctrl == nil {
		return
	}
	a.goTrack(func(context.Context) {
		ctrl.HandleTriggerEvent(host.TriggerEvent{
			Kind:        kind,
			TimestampMs: time.Now().UnixMilli(),
		})
	})
}

func (a *desktopApp) openSettings(tab uiapp.SettingsTab) {
	a.mu.Lock()
	ctrl := a.controller
	a.mu.Unlock()
	if ctrl != nil {
		ctrl.Dispatch(engine.Event{Type: engine.EventDismiss})
		ctrl.OpenSettings(tab)
		return
	}
	a.handleOpenSettings(tab)
}

func (a *desktopApp) reloadConfiguration() {
	a.applyConfigReload()
	a.mu.Lock()
	ctrl := a.controller
	a.mu.Unlock()
	if ctrl == nil {
		return
	}
	if len(ctrl.ConfigStore.LastErrors()) == 0 {
		ctrl.Dispatch(engine.Event{
			Type:    engine.EventShowHUD,
			Message: "Configuration reloaded",
		})
	} else {
		ctrl.Dispatch(engine.Event{
			Type:    engine.EventShowHUD,
			Message: "Configuration reload failed",
		})
	}
}

func (a *desktopApp) applyConfigReload() {
	a.mu.Lock()
	ctrl := a.controller
	sqliteStore := a.sqliteStore
	learningStore := a.learningStore
	ms := a.mediaStore
	a.mu.Unlock()
	if ctrl == nil {
		return
	}

	ok := ctrl.ReloadConfig()
	cfg := ctrl.Config()
	if ok {
		a.applyRuntimeConfig(cfg)
		if sqliteStore != nil || learningStore != nil {
			clipCfg := cfg.Main.Clipboard
			a.goBG(func(ctx context.Context) {
				if sqliteStore != nil && clipCfg.Enabled {
					_, _ = sqliteStore.PruneExpired(ctx, clipCfg.RetentionDays, clipCfg.MaxHistoryItems)
					maintainClipboardMedia(ctx, sqliteStore, ms, clipCfg)
				}
				if learningStore != nil {
					_ = learningStore.PruneHistory(ctx, promptHistoryRetentionLimit(clipCfg))
					_ = learningStore.PruneFrecency(ctx, storage.DefaultMaxFrecencyRows, storage.DefaultFrecencyRetentionWindow)
				}
				a.syncFrecency(ctx)
			})
		}
		a.startBridgeAndFallbackShortcut(cfg.Main.Trigger)
	}
	// A language change on disk takes effect on the next reload.
	a.syncLanguageFromConfig(cfg)
	a.goTrack(func(context.Context) { a.refreshPermissionsAsync() })
}

// applyRuntimeConfig hands cfg to what runs outside the controller: the
// bridge's trigger keys, keymap and IME rules, the input engine, the
// in-place optimize shortcut, the clipboard privacy filter and the daemon.
func (a *desktopApp) applyRuntimeConfig(cfg config.ClavisConfig) {
	a.mu.Lock()
	ctx := a.ctx
	br := a.hostBridge
	ctrl := a.controller
	sqliteStore := a.sqliteStore
	blocklistStore := a.blocklistStore
	sup := a.supervisor
	a.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	a.resyncUserWords(cfg)
	if updater, ok := br.(dynamicBridgeUpdater); ok {
		updater.UpdateTriggerConfig(cfg.Main.Trigger)
		updater.UpdateKeymapConfig(cfg.Main.Keymap)
		updater.UpdateIMEConfig(cfg.Main.IME, cfg.IMERules)
	}
	a.syncInputEngineBlocklist(br, blocklistStore)
	if h, ok := br.(bridge.InputEngineHost); ok {
		if cfg.Main.InputEngine.Enabled {
			var suggest suggestFunc
			if ctrl != nil {
				suggest = ctrl.SuggestInputCandidates
			}
			a.ensureInputEngineUI(ctx, br, cfg.Main.InputEngine, suggest)
			if f, ok := br.(bridge.InlineIMEForcer); ok && f != nil {
				f.SetInlineIMEForced(cfg.Main.InputEngine.ForceTakeover)
			}
		}
		h.ConfigureInputEngine(cfg.Main.InputEngine)
	}
	if ext, ok := br.(bridge.ExtendedHostService); ok {
		ext.UpdateDaemonShortcut(cfg.Main.Daemon.InPlaceOptimizeShortcut)
	}
	if sqliteStore != nil {
		sqliteStore.SetPrivacyFilter(clipboard.NewPrivacyFilterFromConfig(cfg.Main.Clipboard))
	}
	if sup != nil {
		sup.UpdateConfig(cfg.Main.Daemon)
	}
}

// applySavedConfig applies a configuration Clavis saved itself (Settings,
// the first-run guide). The config watcher skips Clavis's own writes, so
// without it a new Leader key, input engine setting or clipboard filter
// waited for the next restart.
func (a *desktopApp) applySavedConfig() {
	a.mu.Lock()
	ctrl := a.controller
	a.mu.Unlock()
	if ctrl == nil {
		return
	}
	cfg := ctrl.Config()
	a.applyRuntimeConfig(cfg)
	a.syncFallbackShortcuts(cfg.Main.Trigger)
}

// resyncUserWords pins every configured word and unpins words that were removed, so editing the
// list takes effect without a restart.
func (a *desktopApp) resyncUserWords(cfg config.ClavisConfig) {
	a.mu.Lock()
	store := a.learningStore
	a.mu.Unlock()
	if store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	keep := make(map[string]bool, len(cfg.Main.InputEngine.UserWords))
	for _, w := range cfg.Main.InputEngine.UserWords {
		if trimmed := strings.TrimSpace(w); trimmed != "" {
			keep[trimmed] = true
		}
	}
	unpinned := make(map[string]struct{})
	for _, pinned := range store.PinnedIMEWords(ctx) {
		if !keep[pinned] {
			if _, done := unpinned[pinned]; !done {
				_ = store.UnpinIMEWord(ctx, pinned)
				unpinned[pinned] = struct{}{}
			}
		}
	}
	storage.InjectUserWords(ctx, store, cfg.Main.InputEngine.UserWords)
}

// syncLanguageFromConfig re-resolves the UI language after a config change
// and refreshes what the toolkit holds outside the view tree (tray labels,
// window titles, MyGo's own controls).
func (a *desktopApp) syncLanguageFromConfig(cfg config.ClavisConfig) {
	prev := i18n.Current()
	i18n.SetLanguage(i18n.ResolveLanguage(cfg.Main.Appearance.Language, i18n.PreferredLanguages()))
	if i18n.Current() == prev {
		return
	}
	if tag := i18n.Current().Tag(); tag != "" {
		setAppLocale(tag)
	}
	if mygo.App.IsReady() {
		mygo.RunOnMain(func() {
			a.refreshTrayLabels()
			if a.settingsWin != nil {
				a.settingsWin.SetTitle(i18n.T("tray.settings_window"))
			}
			if a.onboardingWin != nil {
				a.onboardingWin.SetTitle(i18n.T("tray.onboarding_window"))
			}
			if a.imePillPanel != nil {
				a.imePillPanel.SetTitle(i18n.T("tray.ime_window"))
			}
			if a.promptPanel != nil {
				a.promptPanel.SetTitle(i18n.T("prompts.composer_title"))
			}
		})
	}
	a.handleRepaint()
}

// handleLanguageChange is the controller hook fired when the language is
// changed from the Settings UI.
func (a *desktopApp) handleLanguageChange() {
	a.mu.Lock()
	ctrl := a.controller
	a.mu.Unlock()
	if ctrl == nil {
		return
	}
	a.syncLanguageFromConfig(ctrl.Config())
}

func (a *desktopApp) currentPermissionsMenuLabel() string {
	a.mu.Lock()
	known := a.permKnown
	status := a.permStatus
	err := a.permErr
	caps := a.permCaps
	a.mu.Unlock()
	if !known {
		return i18n.T("tray.permissions_checking")
	}
	return formatPermissionsMenuLabel(status, err, caps)
}

func (a *desktopApp) refreshTrayLabels() {
	a.mu.Lock()
	items := a.trayLabelItems
	permItem := a.permMenuItem
	a.mu.Unlock()
	for key, item := range items {
		if item != nil {
			item.SetLabel(i18n.T("tray." + key))
		}
	}
	if permItem != nil {
		permItem.SetLabel(a.currentPermissionsMenuLabel())
	}
}

func (a *desktopApp) refreshPermissionsAsync() {
	a.mu.Lock()
	ctrl := a.controller
	ctx := a.ctx
	br := a.hostBridge
	shuttingDown := a.shuttingDown
	a.mu.Unlock()
	if shuttingDown || ctrl == nil || ctx == nil || ctx.Err() != nil {
		return
	}
	status, err := ctrl.RefreshPermissions(ctx)
	if ctx.Err() != nil {
		return
	}
	var caps host.Capabilities
	if br != nil {
		caps = br.Capabilities()
	}
	a.mu.Lock()
	shuttingDown = a.shuttingDown
	a.permKnown = true
	a.permStatus = status
	a.permErr = err
	a.permCaps = caps
	a.mu.Unlock()
	if shuttingDown {
		return
	}
	label := formatPermissionsMenuLabel(status, err, caps)
	if !mygo.App.IsReady() {
		return // no tray yet (or tests): RunOnMain would wait for a loop
	}
	mygo.RunOnMain(func() {
		a.mu.Lock()
		item := a.permMenuItem
		a.mu.Unlock()
		if item != nil {
			item.SetLabel(label)
		}
	})
}

func (a *desktopApp) runClipboardPoller(ctx context.Context) {
	pollTicker := time.NewTicker(clipPollInterval)
	pruneTicker := time.NewTicker(clipPruneEvery)
	defer pollTicker.Stop()
	defer pruneTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-pollTicker.C:
			a.mu.Lock()
			ctrl := a.controller
			br := a.hostBridge
			store := a.clipStore
			ms := a.mediaStore
			a.mu.Unlock()
			if ctrl == nil || br == nil || store == nil {
				continue
			}
			cfg := ctrl.Config()
			if !cfg.Main.Clipboard.Enabled {
				continue
			}
			cand, observedSeq, err := br.PollClipboardCandidate(ctx)
			if err != nil || cand == nil {
				continue
			}
			a.recordPolledClipboardCandidate(ctx, ctrl, br, store, ms, cfg, cand, observedSeq)
		case <-pruneTicker.C:
			a.mu.Lock()
			ctrl := a.controller
			store := a.clipStore
			ms := a.mediaStore
			ls := a.learningStore
			a.mu.Unlock()
			if ctrl == nil || store == nil {
				continue
			}
			cfg := ctrl.Config()
			if cfg.Main.Clipboard.Enabled {
				pruned, _ := store.PruneExpired(ctx, cfg.Main.Clipboard.RetentionDays, cfg.Main.Clipboard.MaxHistoryItems)
				maintainClipboardMedia(ctx, store, ms, cfg.Main.Clipboard)
				if ls != nil {
					_ = ls.PruneHistory(ctx, promptHistoryRetentionLimit(cfg.Main.Clipboard))
					_ = ls.PruneFrecency(ctx, storage.DefaultMaxFrecencyRows, storage.DefaultFrecencyRetentionWindow)
				}
				if pruned > 0 {
					a.syncFrecency(ctx)
				}
			}
		}
	}
}

func (a *desktopApp) recordPolledClipboardCandidate(
	ctx context.Context,
	ctrl *uiapp.AppController,
	br bridge.HostService,
	store clipboard.Store,
	ms *clipboard.MediaStore,
	cfg config.ClavisConfig,
	cand *clipboard.CaptureCandidate,
	observedSeq int64,
) bool {
	if cand == nil || ctrl == nil || store == nil {
		return false
	}
	if ctrl.ShouldSkipClipboardHistory(*cand) {
		return false
	}
	if cand.IsMedia() && ms != nil {
		src := mediaSource{
			ReadPNG:   func() ([]byte, error) { return mygo.Clipboard.ReadFormat(transfer.PNG) },
			ReadFiles: mygo.Clipboard.ReadFiles,
		}
		if prober, ok := br.(bridge.FileMediaProber); ok {
			src.Probe = prober.FileProbe()
		}
		if seqr, ok := br.(bridge.ClipboardSequencer); ok {
			src.Sequence = seqr.ClipboardSequence
		}
		src.ObservedSequence = observedSeq
		if keep, _ := enrichMediaCandidate(cand, cfg.Main.Clipboard, ms, src); !keep {
			return false
		}
	}
	if _, changed, recErr := store.Record(ctx, *cand); recErr == nil && changed {
		a.syncFrecency(ctx)
		return true
	}
	return false
}

func promptHistoryRetentionLimit(cfg config.ClipboardConfig) int {
	if cfg.MaxHistoryItems > defaultPromptHistoryLimit {
		return cfg.MaxHistoryItems
	}
	return defaultPromptHistoryLimit
}

// ensureBGContextLocked returns the shutdown-canceled background context,
// initializing it lazily from a.ctx (or context.Background()) when needed.
// Caller must hold a.mu.
func (a *desktopApp) ensureBGContextLocked() context.Context {
	if a.bgCtx == nil {
		parent := a.ctx
		if parent == nil {
			parent = context.Background()
		}
		a.bgCtx, a.bgCancel = context.WithCancel(parent)
	}
	return a.bgCtx
}

// goTrack launches fn on a tracked background goroutine bound to the app's
// shutdown-canceled context and bgWG. Returns false when shutdown has begun.
func (a *desktopApp) goTrack(fn func(context.Context)) bool {
	if fn == nil {
		return false
	}
	a.mu.Lock()
	if a.shuttingDown {
		a.mu.Unlock()
		return false
	}
	if a.ctx != nil && a.ctx.Err() != nil {
		a.mu.Unlock()
		return false
	}
	bgCtx := a.ensureBGContextLocked()
	if bgCtx.Err() != nil {
		a.mu.Unlock()
		return false
	}
	a.bgWG.Add(1)
	a.mu.Unlock()
	go func() {
		defer a.bgWG.Done()
		fn(bgCtx)
	}()
	return true
}

func (a *desktopApp) goBG(fn func(context.Context)) {
	a.goTrack(fn)
}

// stopTimerLocked stops and clears a tracked timer slot. Caller must hold a.mu.
func (a *desktopApp) stopTimerLocked(slot **time.Timer) {
	if slot != nil && *slot != nil {
		(*slot).Stop()
		*slot = nil
	}
}

// scheduleTrackedTimerLocked replaces *slot with a cancellable timer that runs
// fn inside a.goTrack when delay elapses. Caller must hold a.mu.
func (a *desktopApp) scheduleTrackedTimerLocked(slot **time.Timer, delay time.Duration, fn func(context.Context)) bool {
	if slot == nil || fn == nil {
		return false
	}
	a.stopTimerLocked(slot)
	if a.shuttingDown {
		return false
	}
	if a.ctx != nil && a.ctx.Err() != nil {
		return false
	}
	bgCtx := a.ensureBGContextLocked()
	if bgCtx.Err() != nil {
		return false
	}
	var t *time.Timer
	t = time.AfterFunc(delay, func() {
		a.mu.Lock()
		if *slot == t {
			*slot = nil
		}
		a.mu.Unlock()
		a.goTrack(fn)
	})
	*slot = t
	return true
}

func (a *desktopApp) scheduleTrackedTimer(slot **time.Timer, delay time.Duration, fn func(context.Context)) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.scheduleTrackedTimerLocked(slot, delay, fn)
}

// waitBG waits for all tracked background goroutines up to timeout.
// Returns true if every goroutine finished before the deadline.
func (a *desktopApp) waitBG(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		a.bgWG.Wait()
		close(done)
	}()
	if timeout <= 0 {
		<-done
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func (a *desktopApp) handleRecordItemUse(itemID string) {
	if itemID == "" {
		return
	}
	a.mu.Lock()
	sup := a.supervisor
	a.mu.Unlock()
	if sup != nil {
		sup.RecordInteraction()
	}
	if lw := a.learningWriterForUse(); lw != nil {
		// RAM mirror now (the frecency sync below sees this use), SQLite later
		// in a coalesced batch: ~12.6ms per native write on Windows.
		now := time.Now()
		lw.QueueItemUse(itemID, now)
		if strings.HasPrefix(itemID, "glossary:") {
			lw.QueueLexiconHit(now, true)
		}
	}
	a.goBG(func(ctx context.Context) {
		a.syncFrecency(ctx)
	})
}

func (a *desktopApp) handleRecordPromptVersion(promptID, trigger, rawDraft, finalOutput, preset string) {
	if promptID == "" && finalOutput == "" {
		return
	}
	a.mu.Lock()
	sup := a.supervisor
	a.mu.Unlock()
	if sup != nil {
		sup.RecordInteraction()
	}
	if lw := a.learningWriterForUse(); lw != nil {
		lw.QueuePromptVersion(promptID, trigger, rawDraft, finalOutput, preset)
	}
}

// learningWriterForUse returns the async learning writer, starting it on
// first use when the app was assembled with a learning store but no writer
// (tests, alternate setups). It never starts one once shutdown began, so a
// late event cannot schedule writes against a closing database.
func (a *desktopApp) learningWriterForUse() *storage.LearningWriter {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.learningWriter == nil && a.learningStore != nil && !a.shuttingDown {
		a.learningWriter = storage.NewLearningWriter(a.learningStore, storage.LearningWriterOptions{})
	}
	return a.learningWriter
}

func (a *desktopApp) syncInputEngineBlocklist(br bridge.HostService, bs *storage.UserBlocklistStore) {
	if br == nil || bs == nil {
		return
	}
	bh, ok := br.(bridge.InputEngineBlocklistHost)
	if !ok {
		return
	}
	bh.UpdateInputEngineBlockedWords(bs.Words())
}

func (a *desktopApp) wireInputEngineLearning(br bridge.HostService, ls *storage.LearningStore) {
	lh, ok := br.(bridge.InputEngineLearningHost)
	if !ok || ls == nil {
		return
	}
	adapter := storage.NewUserFrequencyAdapter(ls)
	lh.UpdateInputEngineLearning(adapter, func(c bridge.InputEngineCommit) {
		a.mu.Lock()
		sup := a.supervisor
		shuttingDown := a.shuttingDown
		a.mu.Unlock()
		if shuttingDown {
			return
		}
		writer := a.learningWriterForUse()
		// Resident telemetry: this runs on the view worker, never the hook.
		if sup != nil {
			sup.RecordKeystroke(c.LatencyNs, utf8.RuneCountInString(c.Word))
		}
		// The view worker drains commits right before it refreshes the
		// candidate window, so this must not wait on SQLite (one or two
		// native round trips, >=12.6ms each on Windows): queue it.
		if writer != nil {
			writer.QueueIMECommit(c.PinyinKey, c.Word, c.PrevWord, c.Pinned)
			writer.QueueLexiconHit(time.Now(), c.FirstChoice)
		}
	})
}

func (a *desktopApp) syncFrecency(ctx context.Context) {
	if ctx != nil && ctx.Err() != nil {
		return
	}
	a.mu.Lock()
	syncer := a.frecencySyncer
	shuttingDown := a.shuttingDown
	a.mu.Unlock()
	if shuttingDown || syncer == nil {
		return
	}
	syncer.Sync(time.Now())
}

func (a *desktopApp) dispatchFrecencyScores(scores map[string]int) {
	gen := a.frecencyGen.Add(1)
	run := func() {
		if a.frecencyGen.Load() != gen {
			return
		}
		a.mu.Lock()
		shuttingDown := a.shuttingDown
		ctx := a.ctx
		ctrl := a.controller
		a.mu.Unlock()
		if shuttingDown || ctrl == nil || (ctx != nil && ctx.Err() != nil) {
			return
		}
		ctrl.Dispatch(engine.Event{
			Type:           engine.EventSetFrecencyScores,
			FrecencyScores: scores,
		})
	}
	if mygo.App.IsReady() {
		a.goTrack(func(ctx context.Context) {
			if ctx.Err() != nil || !mygo.App.IsReady() {
				return
			}
			mygo.RunOnMain(run)
		})
		return
	}
	run()
}

func (a *desktopApp) handleRepaint() {
	a.mu.Lock()
	if a.shuttingDown {
		a.mu.Unlock()
		return
	}
	capWin := a.capsulePanel
	promptWin := a.promptPanel
	setWin := a.settingsWin
	pillWin := a.imePillPanel
	guideWin := a.onboardingWin
	annotateWin := a.annotateWin
	selectionBar := a.selectionBar
	ctrl := a.controller
	imeUI := a.inputUI
	a.mu.Unlock()

	// Floating surfaces must not stack. The quick panel is the primary surface while it is up:
	// the candidate panel would otherwise paint over it, and the IME pill would report a mode
	// that the panel has already suspended. Ordering is decided here, in one place, instead of
	// each window guessing from its own visibility.
	if imeUI != nil && capWin != nil {
		quickUp := capWin.IsVisible() && ctrl != nil && ctrl.State().Phase != engine.PhaseHidden
		imeUI.SetSuppressed(quickUp)
		if quickUp && pillWin != nil && pillWin.IsVisible() {
			a.stopIMEPillTimer()
			pillWin.Hide()
		}
	}
	if guideWin != nil && a.onboardingOpen.Load() {
		guideWin.Invalidate()
	}
	if capWin != nil {
		if ctrl != nil && capWin.IsVisible() {
			state := ctrl.State()
			cfg := ctrl.Config()
			w, h := uiapp.PreferredCapsuleSizeForState(state, cfg.Main.Appearance)
			if w > 0 && h > 0 {
				curW, curH := capWin.Size()
				if curW != w || curH != h {
					capWin.SetSize(w, h)
				}
			}
		}
		capWin.Invalidate()
	}
	if promptWin != nil && promptWin.IsVisible() {
		promptWin.Invalidate()
	}
	if setWin != nil {
		setWin.Invalidate()
	}
	if pillWin != nil && pillWin.IsVisible() {
		pillWin.Invalidate()
	}
	if annotateWin != nil && annotateWin.IsVisible() {
		annotateWin.Invalidate()
	}
	if selectionBar != nil && selectionBar.IsVisible() {
		selectionBar.Invalidate()
	}
}

func (a *desktopApp) currentIMEPillLabel() string {
	a.mu.Lock()
	lbl := strings.TrimSpace(a.imePillLabel)
	a.mu.Unlock()
	if lbl == "" {
		return "EN"
	}
	return lbl
}

func (a *desktopApp) stopIMEPillTimer() {
	a.mu.Lock()
	a.stopTimerLocked(&a.imePillTimer)
	a.mu.Unlock()
}

func (a *desktopApp) handleShowIMEPill(label string, anchor *host.CursorAnchor) {
	trimmed := strings.TrimSpace(label)
	if trimmed == "" {
		trimmed = "EN"
	}
	gen := a.imePillGen.Add(1)

	a.mu.Lock()
	a.imePillLabel = trimmed
	a.stopTimerLocked(&a.imePillTimer)
	br := a.hostBridge
	prevAnchor := a.sessionAnchor
	a.mu.Unlock()

	anchorCopy := cloneValidAnchor(anchor, prevAnchor)
	a.goTrack(func(bgCtx context.Context) {
		a.presentIMEPillAsync(bgCtx, gen, br, anchorCopy)
	})
}

func cloneValidAnchor(primary, fallback *host.CursorAnchor) *host.CursorAnchor {
	if primary != nil && primary.Valid {
		copied := *primary
		return &copied
	}
	if fallback != nil && fallback.Valid {
		copied := *fallback
		return &copied
	}
	return nil
}

func (a *desktopApp) presentIMEPillAsync(ctx context.Context, gen uint64, br bridge.HostService, resolvedAnchor *host.CursorAnchor) {
	if ctx != nil && ctx.Err() != nil {
		return
	}
	if resolvedAnchor == nil && br != nil && ctx != nil {
		snapCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
		snap, err := br.SnapshotContext(snapCtx)
		cancel()
		if err == nil && snap.CursorAnchor != nil && snap.CursorAnchor.Valid {
			copied := *snap.CursorAnchor
			resolvedAnchor = &copied
		}
	}
	if (ctx != nil && ctx.Err() != nil) || a.imePillGen.Load() != gen || !mygo.App.IsReady() {
		return
	}

	mygo.RunOnMain(func() {
		if a.imePillGen.Load() != gen {
			return
		}
		a.mu.Lock()
		pill := a.imePillPanel
		a.mu.Unlock()
		if pill == nil {
			return
		}

		pillW, pillH := uiapp.PreferredPillSizeForLabel(a.currentIMEPillLabel())
		displays := mygo.Screen.Displays()
		cursorPt := mygo.Screen.CursorScreenPoint()
		bounds := computeIMEPillBounds(resolvedAnchor, cursorPt, bridge.Size{
			Width:  float64(pillW),
			Height: float64(pillH),
		}, displays)

		pill.SetBounds(bounds)
		pill.SetOpacity(1) // undo a fade-out the new label interrupted
		pill.ShowInactive()
		pill.Invalidate()
		a.scheduleIMEPillAutoHide(gen)
	})
}

func (a *desktopApp) scheduleIMEPillAutoHide(gen uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.imePillGen.Load() != gen {
		return
	}
	// A mode badge is glanceable; sentences and failures stay longer.
	hold := uiapp.PillDisplayDuration(a.imePillLabel)
	a.scheduleTrackedTimerLocked(&a.imePillTimer, hold, func(ctx context.Context) {
		if a.imePillGen.Load() != gen {
			return
		}
		a.runFadeOutIMEPill(ctx, gen)
	})
}

// fadeOutIMEPill fades the pill window out over imePillFadeOut and hides it,
// instead of letting it vanish mid-glance. A newer pill (gen changed) or
// shutdown context cancellation stops the fade; presentIMEPillAsync restores
// full opacity before showing it. With [appearance].animations = "off" the pill
// hides at once.
func (a *desktopApp) fadeOutIMEPill(gen uint64) {
	a.goTrack(func(ctx context.Context) {
		a.runFadeOutIMEPill(ctx, gen)
	})
}

func (a *desktopApp) runFadeOutIMEPill(ctx context.Context, gen uint64) {
	if ctx.Err() != nil || a.imePillGen.Load() != gen {
		return
	}
	const steps = 8
	animate := true
	a.mu.Lock()
	if a.cfgStore != nil {
		animate = config.NormalizeAnimationMode(a.cfgStore.Current().Main.Appearance.Animations) != "off"
	}
	a.mu.Unlock()

	hide := func() {
		if ctx.Err() != nil || a.imePillGen.Load() != gen || !mygo.App.IsReady() {
			return
		}
		mygo.RunOnMain(func() {
			if a.imePillGen.Load() != gen {
				return
			}
			a.mu.Lock()
			p := a.imePillPanel
			a.mu.Unlock()
			if p != nil && p.IsVisible() {
				p.Hide()
				p.SetOpacity(1)
			}
		})
	}
	if !animate {
		hide()
		return
	}
	ticker := time.NewTicker(imePillFadeOut / steps)
	defer ticker.Stop()
	for i := 1; i < steps; i++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if a.imePillGen.Load() != gen {
			return
		}
		opacity := 1 - float64(i)/steps
		if !mygo.App.IsReady() {
			return
		}
		mygo.RunOnMain(func() {
			if a.imePillGen.Load() != gen {
				return
			}
			a.mu.Lock()
			p := a.imePillPanel
			a.mu.Unlock()
			if p != nil {
				p.SetOpacity(opacity)
			}
		})
	}
	select {
	case <-ctx.Done():
		return
	case <-ticker.C:
	}
	hide()
}

func (a *desktopApp) handlePresentPhase(phase engine.CapsulePhase) {
	seq := a.presentSeq.Add(1)
	a.hideSeq.Add(1) // cancel any pending delayed hide from an exit animation
	a.markPresentTransition(350 * time.Millisecond)

	a.mu.Lock()
	a.stopTimerLocked(&a.capsuleRevealTimer)
	a.stopTimerLocked(&a.capsuleHideTimer)
	br := a.hostBridge
	prevAnchor := a.sessionAnchor
	a.mu.Unlock()

	a.goTrack(func(bgCtx context.Context) {
		var anchor *host.CursorAnchor
		if prevAnchor != nil && prevAnchor.Valid {
			copied := *prevAnchor
			anchor = &copied
		} else if br != nil {
			snapCtx, cancel := context.WithTimeout(bgCtx, 120*time.Millisecond)
			snap, err := br.SnapshotContext(snapCtx)
			cancel()
			if err == nil && snap.CursorAnchor != nil && snap.CursorAnchor.Valid {
				copied := *snap.CursorAnchor
				anchor = &copied
				a.setSessionAnchor(anchor)
			}
		}
		if bgCtx.Err() != nil || a.presentSeq.Load() != seq || !mygo.App.IsReady() {
			return
		}

		mygo.RunOnMain(func() {
			if a.presentSeq.Load() != seq {
				return
			}
			a.mu.Lock()
			panel := a.capsulePanel
			ctrl := a.controller
			a.mu.Unlock()
			if panel == nil || ctrl == nil {
				return
			}
			state := ctrl.State()
			if state.Phase == engine.PhaseHidden {
				return
			}

			a.markPresentTransition(180 * time.Millisecond)

			cfg := ctrl.Config()
			w, h := uiapp.PreferredCapsuleSizeForState(state, cfg.Main.Appearance)
			displays := mygo.Screen.Displays()
			cursorPt := mygo.Screen.CursorScreenPoint()
			bounds := computeCapsuleBounds(anchor, cursorPt, bridge.Size{
				Width:  float64(w),
				Height: float64(h),
			}, displays)

			panel.SetBounds(bounds)
			if state.Phase == engine.PhaseHUD {
				panel.SetOpacity(1)
				panel.ShowInactive()
			} else {
				// A Leader menu not yet on screen takes the keyboard focus
				// at once but stays transparent for the reveal delay: a fast
				// key sequence runs without the menu ever flashing.
				a.mu.Lock()
				revealed := a.capsuleRevealed
				a.mu.Unlock()
				delay := capsuleRevealDelay(state.Phase, revealed, cfg.Main.Trigger)
				if delay > 0 {
					panel.SetOpacity(0)
				} else {
					panel.SetOpacity(1)
					a.mu.Lock()
					a.capsuleRevealed = true
					a.mu.Unlock()
				}
				panel.Show()
				panel.Focus()
				if delay > 0 {
					fade := config.NormalizeAnimationMode(cfg.Main.Appearance.Animations) != "off"
					a.scheduleCapsuleReveal(seq, delay, fade)
				}
			}
			panel.Invalidate()
		})
	})
}

// capsuleRevealFade is the Leader menu's fade-in once its delay elapses.
const capsuleRevealFade = 120 * time.Millisecond

// capsuleRevealDelay is how long the capsule stays invisible after it is
// presented. Only a Leader menu that is not on screen yet waits (which-key
// style), so a quick key sequence runs without the menu flashing; every
// other phase, and a Leader already visible (drilling into a group), shows
// at once.
func capsuleRevealDelay(phase engine.CapsulePhase, revealed bool, trigger config.TriggerConfig) time.Duration {
	if phase != engine.PhaseLeader || revealed {
		return 0
	}
	ms := min(max(trigger.LeaderRevealDelayMs, 0), config.MaxLeaderRevealDelayMs)
	return time.Duration(ms) * time.Millisecond
}

// scheduleCapsuleReveal fades the capsule in after delay unless the session
// moved on (presentSeq changed: a newer present, an executed key or a hide)
// or shutdown canceled the tracked timer/context.
func (a *desktopApp) scheduleCapsuleReveal(seq uint64, delay time.Duration, fade bool) {
	a.scheduleTrackedTimer(&a.capsuleRevealTimer, delay, func(ctx context.Context) {
		if ctx.Err() != nil || a.presentSeq.Load() != seq {
			return
		}
		a.mu.Lock()
		a.capsuleRevealed = true
		panel := a.capsulePanel
		a.mu.Unlock()
		if panel == nil {
			return
		}
		setOpacity := func(v float64) {
			if ctx.Err() != nil || a.presentSeq.Load() != seq || !mygo.App.IsReady() {
				return
			}
			mygo.RunOnMain(func() {
				if a.presentSeq.Load() == seq {
					panel.SetOpacity(v)
				}
			})
		}
		if !fade {
			setOpacity(1)
			return
		}
		const steps = 6
		ticker := time.NewTicker(capsuleRevealFade / steps)
		defer ticker.Stop()
		for i := 1; i <= steps; i++ {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if a.presentSeq.Load() != seq {
				return
			}
			setOpacity(float64(i) / steps)
		}
	})
}

func (a *desktopApp) handleHide() {
	a.mu.Lock()
	ctrl := a.controller
	a.mu.Unlock()
	if ctrl != nil && ctrl.State().Phase != engine.PhaseHidden {
		return
	}
	a.mu.Lock()
	a.stopTimerLocked(&a.capsuleRevealTimer)
	a.mu.Unlock()
	a.presentSeq.Add(1)
	a.setSessionAnchor(nil)

	// Keep the window on screen briefly so the exit animation (fade /
	// scale-down of the capsule card) is visible; a re-presentation bumps
	// hideSeq and cancels the pending hide.
	seq := a.hideSeq.Add(1)
	delay := time.Duration(0)
	if ctrl != nil {
		delay = ctrl.ExitHideDelay()
	}
	hide := func(ctx context.Context) {
		if ctx != nil && ctx.Err() != nil {
			return
		}
		if a.hideSeq.Load() != seq || !mygo.App.IsReady() {
			return
		}
		mygo.RunOnMain(func() {
			a.mu.Lock()
			panel := a.capsulePanel
			ctrl := a.controller
			a.mu.Unlock()
			if panel == nil {
				return
			}
			if ctrl != nil && ctrl.State().Phase != engine.PhaseHidden {
				return
			}
			if panel.IsVisible() {
				panel.Hide()
				a.lastPanelHideNano.Store(time.Now().UnixNano())
			}
			// Hidden for real: the next Leader waits for its reveal again.
			a.mu.Lock()
			a.capsuleRevealed = false
			a.mu.Unlock()
			panel.SetOpacity(1)
		})
	}
	if delay <= 0 {
		a.mu.Lock()
		a.stopTimerLocked(&a.capsuleHideTimer)
		a.mu.Unlock()
		hide(nil)
		return
	}
	a.scheduleTrackedTimer(&a.capsuleHideTimer, delay, hide)
}

func (a *desktopApp) beforeInject(ctx context.Context) {
	a.mu.Lock()
	panel := a.capsulePanel
	promptWin := a.promptPanel
	a.mu.Unlock()
	if panel == nil && promptWin == nil {
		return
	}
	wasVisible := false
	mygo.RunOnMain(func() {
		if panel != nil && panel.IsVisible() {
			wasVisible = true
			panel.Hide()
			a.lastPanelHideNano.Store(time.Now().UnixNano())
		}
		if promptWin != nil && promptWin.IsVisible() {
			wasVisible = true
			promptWin.Hide()
			a.lastPanelHideNano.Store(time.Now().UnixNano())
		}
	})
	const focusRestoreDelay = 35 * time.Millisecond
	var wait time.Duration
	if wasVisible {
		wait = focusRestoreDelay
	} else if lastHide := a.lastPanelHideNano.Load(); lastHide > 0 {
		elapsed := time.Since(time.Unix(0, lastHide))
		if elapsed < focusRestoreDelay {
			wait = focusRestoreDelay - elapsed
		}
	}
	if wait > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
}

func (a *desktopApp) handleOpenPromptComposer() {
	a.markPresentTransition(350 * time.Millisecond)
	a.mu.Lock()
	anchor := a.sessionAnchor
	a.mu.Unlock()

	var anchorCopy *host.CursorAnchor
	if anchor != nil && anchor.Valid {
		c := *anchor
		anchorCopy = &c
	}

	a.goTrack(func(ctx context.Context) {
		if ctx.Err() != nil || !mygo.App.IsReady() {
			return
		}
		mygo.RunOnMain(func() {
			a.mu.Lock()
			win := a.promptPanel
			ctrl := a.controller
			a.mu.Unlock()
			if win == nil || ctrl == nil || !ctrl.PromptComposerOpen() {
				return
			}
			a.markPresentTransition(180 * time.Millisecond)
			w, h := uiapp.PreferredPromptComposerPanelSize(ctrl.PromptComposerSlotCount())
			bounds := computeCapsuleBounds(anchorCopy, mygo.Screen.CursorScreenPoint(), bridge.Size{
				Width:  float64(w),
				Height: float64(h),
			}, mygo.Screen.Displays())
			win.SetBounds(bounds)
			win.Show()
			win.Focus()
			win.Invalidate()
		})
	})
}

func (a *desktopApp) handleClosePromptComposer() {
	a.goTrack(func(ctx context.Context) {
		if ctx.Err() != nil || !mygo.App.IsReady() {
			return
		}
		mygo.RunOnMain(func() {
			a.mu.Lock()
			win := a.promptPanel
			a.mu.Unlock()
			if win == nil {
				return
			}
			if win.IsVisible() {
				win.Hide()
				a.lastPanelHideNano.Store(time.Now().UnixNano())
			}
		})
	})
}

func (a *desktopApp) handleShowAnnotateWindow() {
	a.goTrack(func(ctx context.Context) {
		if ctx.Err() != nil || !mygo.App.IsReady() {
			return
		}
		mygo.RunOnMain(func() {
			a.mu.Lock()
			win := a.annotateWin
			ctrl := a.controller
			a.mu.Unlock()
			if ctrl == nil {
				return
			}
			if win == nil {
				spec := uiapp.FloatingSurfaceSpecWithElevation(ctrl.Config().Main.Appearance, 660, uiapp.SurfaceElevationFlat, mygo.VibrancyNone, uiapp.DismissExplicit, uiapp.FocusPreviousApp)
				spec.Radius = uiapp.SurfaceRadius{Kind: uiapp.SurfaceRadiusSystem}
				win = mygo.NewWindow(uiapp.FloatingSurface(mygo.WindowOptions{
					Title:     "Clavis Annotate",
					Width:     920,
					Height:    660,
					MinWidth:  640,
					MinHeight: 460,
					Hidden:    true,
					StateKey:  "annotate",
					Content:   ui.View(ctrl.AnnotateView),
				}, spec))
				uiapp.BindFloatingSurface(win, spec, uiapp.SurfaceHooks{
					Dismiss: func() {
						if win.IsVisible() {
							win.Hide()
						}
						ctrl.CloseAnnotateWindow()
					},
					RestoreFocus: a.restorePreviousAppFocus,
					Quitting:     a.quitting.Load,
				})
				a.mu.Lock()
				a.annotateWin = win
				a.mu.Unlock()
			}
			win.Show()
			win.Focus()
			win.Invalidate()
		})
	})
}

func (a *desktopApp) handleCloseAnnotateWindow() {
	a.goTrack(func(ctx context.Context) {
		if ctx.Err() != nil || !mygo.App.IsReady() {
			return
		}
		mygo.RunOnMain(func() {
			a.mu.Lock()
			win := a.annotateWin
			a.mu.Unlock()
			if win != nil && win.IsVisible() {
				win.Hide()
				a.restorePreviousAppFocus()
			}
		})
	})
}

func (a *desktopApp) handleOpenSettings(_ uiapp.SettingsTab) {
	a.goTrack(func(ctx context.Context) {
		if ctx.Err() != nil || !mygo.App.IsReady() {
			return
		}
		mygo.RunOnMain(func() {
			a.mu.Lock()
			win := a.settingsWin
			a.mu.Unlock()
			if win == nil {
				return
			}
			mygo.App.Show()
			win.Show()
			win.Focus()
			win.Invalidate()
		})
	})
}

func (a *desktopApp) setSessionAnchor(anchor *host.CursorAnchor) {
	a.mu.Lock()
	if anchor == nil {
		a.sessionAnchor = nil
	} else {
		copied := *anchor
		a.sessionAnchor = &copied
	}
	a.mu.Unlock()
}

func (a *desktopApp) markPresentTransition(d time.Duration) {
	deadline := time.Now().Add(d).UnixNano()
	for {
		cur := a.presentTransitionUntil.Load()
		if deadline <= cur {
			return
		}
		if a.presentTransitionUntil.CompareAndSwap(cur, deadline) {
			return
		}
	}
}

func (a *desktopApp) isInPresentTransition() bool {
	return time.Now().UnixNano() < a.presentTransitionUntil.Load()
}

func (a *desktopApp) shutdown() {
	a.shutdownOnce.Do(func() {
		a.unregisterFallbackShortcuts()
		a.imePillGen.Add(1)
		a.presentSeq.Add(1)
		a.hideSeq.Add(1)

		a.mu.Lock()
		a.shuttingDown = true
		a.stopTimerLocked(&a.imePillTimer)
		a.stopTimerLocked(&a.capsuleRevealTimer)
		a.stopTimerLocked(&a.capsuleHideTimer)
		bgCancel := a.bgCancel
		cancel := a.cancel
		cw := a.configWatcher
		dw := a.dictionaryWatcher
		sup := a.supervisor
		ctrl := a.controller
		inputUI := a.inputUI
		a.inputUI = nil
		sqliteStore := a.sqliteStore
		learningWriter := a.learningWriter
		syncStateStore := a.syncStateStore
		tray := a.tray
		a.bridgeRunning = false
		a.mu.Unlock()

		if bgCancel != nil {
			bgCancel()
		}
		if cancel != nil {
			cancel()
		}
		if inputUI != nil {
			inputUI.Stop()
		}
		if cw != nil {
			cw.Stop()
		}
		if dw != nil {
			dw.Stop()
		}
		if sup != nil {
			sup.Stop()
		}
		if ctrl != nil {
			_ = ctrl.Stop()
			ctrl.Close()
		}
		if !a.waitBG(bgShutdownWaitTimeout) {
			fmt.Fprintf(os.Stderr, "Clavis shutdown: timed out waiting for background tasks after %s\n", bgShutdownWaitTimeout)
		}
		if learningWriter != nil {
			// The app context is cancelled by now: flush on a fresh one.
			flushCtx, cancelFlush := context.WithTimeout(context.Background(), 2*time.Second)
			_ = learningWriter.Close(flushCtx)
			cancelFlush()
		}
		if sqliteStore != nil {
			_ = sqliteStore.Close()
		}
		if syncStateStore != nil {
			_ = syncStateStore.Close()
		}
		if tray != nil {
			tray.Destroy()
		}
	})
}

// selectionBarSnapshot returns the payload the floating bar renders.
func (a *desktopApp) selectionBarSnapshot() uiapp.SelectionBarPayload {
	a.mu.Lock()
	p := a.selectionBarPayload
	a.mu.Unlock()
	return p
}

// handleShowSelectionBar presents the floating action bar next to the selection.
// It never takes focus: the window is shown inactive so the caret and the
// frontmost app keep their state.
func (a *desktopApp) handleShowSelectionBar(p uiapp.SelectionBarPayload) {
	a.mu.Lock()
	win := a.selectionBar
	a.selectionBarPayload = p
	a.mu.Unlock()
	if win == nil || len(p.Actions) == 0 {
		return
	}
	a.goTrack(func(ctx context.Context) {
		if ctx.Err() != nil || !mygo.App.IsReady() {
			return
		}
		mygo.RunOnMain(func() {
			w, h := uiapp.SelectionBarSize(p)
			displays := mygo.Screen.Displays()
			cursorPt := mygo.Screen.CursorScreenPoint()
			anchor := p.Anchor
			var anchorPtr *host.CursorAnchor
			if anchor.Valid {
				anchorPtr = &anchor
			}
			bounds := computeIMEPillBounds(anchorPtr, cursorPt, bridge.Size{
				Width:  float64(w),
				Height: float64(h),
			}, displays)
			win.SetBounds(bounds)
			win.Invalidate()
			win.ShowInactive()
		})
	})
}

// handleHideSelectionBar hides the floating action bar.
func (a *desktopApp) handleHideSelectionBar() {
	a.mu.Lock()
	win := a.selectionBar
	a.mu.Unlock()
	if win == nil {
		return
	}
	a.goTrack(func(ctx context.Context) {
		if ctx.Err() != nil || !mygo.App.IsReady() {
			return
		}
		mygo.RunOnMain(func() {
			if win.IsVisible() {
				win.Hide()
			}
		})
	})
}

// ---- Coordinate & Tray Helpers ----

// computeCapsuleBounds maps a caret CursorAnchor (or fallback cursor screen point)
// and MyGo displays (top-left DIPs) through bridge.PlacePopover so the Capsule opens
// cleanly under or above the anchor and remains clamped inside the active monitor's WorkArea.
func computeCapsuleBounds(
	anchor *host.CursorAnchor,
	cursorFallback mygo.Point,
	size bridge.Size,
	displays []mygo.Display,
) mygo.Rectangle {
	anchorRect := resolveAnchorRect(anchor, cursorFallback)
	bridgeDisplays := toBridgeDisplays(displays)
	placed := bridge.PlacePopover(anchorRect, size, bridgeDisplays)
	return mygo.Rectangle{
		X:      int(math.Round(placed.X)),
		Y:      int(math.Round(placed.Y)),
		Width:  int(math.Round(placed.Width)),
		Height: int(math.Round(placed.Height)),
	}
}

// computeIMEPillBounds places the floating IME micro-pill compactly just above and to the right
// of the caret anchor (or fallback cursor point) without covering the active typing line,
// clamping the pill inside the target monitor's WorkArea.
func computeIMEPillBounds(
	anchor *host.CursorAnchor,
	cursorFallback mygo.Point,
	size bridge.Size,
	displays []mygo.Display,
) mygo.Rectangle {
	if size.Width <= 0 || size.Height <= 0 {
		pw, ph := uiapp.PreferredIMEPillSize()
		if size.Width <= 0 {
			size.Width = float64(pw)
		}
		if size.Height <= 0 {
			size.Height = float64(ph)
		}
	}

	anchorRect := resolveAnchorRect(anchor, cursorFallback)
	x := anchorRect.MaxX() + imePillOffsetX
	y := anchorRect.Y - size.Height - imePillOffsetY

	bridgeDisplays := toBridgeDisplays(displays)
	if len(bridgeDisplays) > 0 {
		target := selectBridgeDisplayForPoint(anchorRect.Center(), bridgeDisplays)
		wa := target.WorkArea
		if wa.IsZero() {
			wa = target.Bounds
		}
		minY := wa.Y + bridge.PopoverScreenMargin
		maxY := wa.MaxY() - bridge.PopoverScreenMargin
		if y < minY {
			belowY := anchorRect.MaxY() + imePillOffsetY
			if belowY+size.Height <= maxY {
				y = belowY
			}
		}
		x = clampScreenAxis(x, size.Width, wa.X, wa.Width, bridge.PopoverScreenMargin)
		y = clampScreenAxis(y, size.Height, wa.Y, wa.Height, bridge.PopoverScreenMargin)
	}

	return mygo.Rectangle{
		X:      int(math.Round(x)),
		Y:      int(math.Round(y)),
		Width:  int(math.Round(size.Width)),
		Height: int(math.Round(size.Height)),
	}
}

func resolveAnchorRect(anchor *host.CursorAnchor, cursorFallback mygo.Point) bridge.Rect {
	if anchor != nil && anchor.Valid {
		w := anchor.Width
		if w < 1 {
			w = 1
		}
		h := anchor.Height
		if h < 1 {
			h = 18
		}
		return bridge.Rect{
			X:      anchor.X,
			Y:      anchor.Y,
			Width:  w,
			Height: h,
		}
	}
	return bridge.Rect{
		X:      float64(cursorFallback.X),
		Y:      float64(cursorFallback.Y),
		Width:  1,
		Height: 18,
	}
}

func toBridgeDisplays(displays []mygo.Display) []bridge.DisplayInfo {
	bridgeDisplays := make([]bridge.DisplayInfo, len(displays))
	for i, d := range displays {
		bridgeDisplays[i] = bridge.DisplayInfo{
			ID: d.ID,
			Bounds: bridge.Rect{
				X:      float64(d.Bounds.X),
				Y:      float64(d.Bounds.Y),
				Width:  float64(d.Bounds.Width),
				Height: float64(d.Bounds.Height),
			},
			WorkArea: bridge.Rect{
				X:      float64(d.WorkArea.X),
				Y:      float64(d.WorkArea.Y),
				Width:  float64(d.WorkArea.Width),
				Height: float64(d.WorkArea.Height),
			},
			Primary: d.Primary,
		}
	}
	return bridgeDisplays
}

func selectBridgeDisplayForPoint(p bridge.Point, displays []bridge.DisplayInfo) bridge.DisplayInfo {
	for _, d := range displays {
		b := d.Bounds
		if p.X >= b.X && p.X <= b.MaxX() && p.Y >= b.Y && p.Y <= b.MaxY() {
			return d
		}
	}
	bestIdx := -1
	bestDist := math.Inf(1)
	for i, d := range displays {
		b := d.Bounds
		dx := max(max(b.X-p.X, 0), p.X-b.MaxX())
		dy := max(max(b.Y-p.Y, 0), p.Y-b.MaxY())
		dist := dx*dx + dy*dy
		if dist < bestDist {
			bestDist = dist
			bestIdx = i
		}
	}
	if bestIdx >= 0 {
		return displays[bestIdx]
	}
	for _, d := range displays {
		if d.Primary {
			return d
		}
	}
	return displays[0]
}

func clampScreenAxis(pos, size, start, span, margin float64) float64 {
	minPos := start + margin
	maxPos := start + span - size - margin
	if maxPos < minPos {
		return minPos
	}
	if pos < minPos {
		return minPos
	}
	if pos > maxPos {
		return maxPos
	}
	return pos
}

func formatPermissionsMenuLabel(status host.PermissionStatus, err error, caps host.Capabilities) string {
	if err != nil {
		return i18n.T("tray.permissions_failed")
	}
	if !caps.Accessibility && !caps.InputMonitoring {
		return i18n.T("tray.permissions_standard")
	}
	if status.Accessibility && status.InputMonitoring {
		return i18n.T("tray.permissions_all")
	}
	var missing []string
	if caps.Accessibility && !status.Accessibility {
		missing = append(missing, i18n.T("settings.perm.accessibility"))
	}
	if caps.InputMonitoring && !status.InputMonitoring {
		missing = append(missing, i18n.T("settings.perm.input_monitoring"))
	}
	if len(missing) == 0 {
		return i18n.T("tray.permissions_granted")
	}
	return i18n.Tf("tray.permissions_grant", strings.Join(missing, " & "))
}

// itemByLabel finds the menu item showing label, for tray relabeling.
func itemByLabel(menu *mygo.Menu, label string) *mygo.MenuItem {
	for _, item := range menu.Items() {
		if item.Label == label {
			return item
		}
	}
	return nil
}

func toMyGoAccelerator(shortcut string) string {
	hk, err := bridge.ParseHotkey(shortcut)
	if err != nil {
		return ""
	}
	var parts []string
	for _, m := range hk.Modifiers {
		switch m {
		case "command":
			parts = append(parts, "Super")
		case "control":
			parts = append(parts, "Ctrl")
		case "option":
			parts = append(parts, "Alt")
		case "shift":
			parts = append(parts, "Shift")
		}
	}
	key := mapShortcutKeyToMyGo(hk.Key)
	if key == "" {
		return ""
	}
	parts = append(parts, key)
	return strings.Join(parts, "+")
}

func mapShortcutKeyToMyGo(k string) string {
	switch k {
	case "space":
		return "Space"
	case "return":
		return "Enter"
	case "escape":
		return "Escape"
	case "tab":
		return "Tab"
	case "backspace", "delete":
		// Delete is accepted as the recorder/runtime alias for Backspace.
		return "Backspace"
	case "forwarddelete":
		return "Delete"
	case "left":
		return "Left"
	case "right":
		return "Right"
	case "up":
		return "Up"
	case "down":
		return "Down"
	case "home":
		return "Home"
	case "end":
		return "End"
	case "pageup":
		return "PageUp"
	case "pagedown":
		return "PageDown"
	case "grave":
		return "`"
	case "minus":
		return "-"
	case "equal":
		return "="
	case "leftbracket":
		return "["
	case "rightbracket":
		return "]"
	case "semicolon":
		return ";"
	case "quote":
		return "'"
	case "comma":
		return ","
	case "period":
		return "."
	case "slash":
		return "/"
	case "backslash":
		return `\`
	}
	if strings.HasPrefix(k, "f") && len(k) >= 2 {
		return strings.ToUpper(k)
	}
	if len(k) == 1 {
		return strings.ToUpper(k)
	}
	return ""
}
