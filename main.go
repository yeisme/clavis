package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
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
	"github.com/yeisme/clavis/internal/watcher"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
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
	clipStore            clipboard.Store
	mediaStore           *clipboard.MediaStore
	hostBridge           bridge.HostService
	controller           *uiapp.AppController
	supervisor           *daemon.ResidentSupervisor
	frecencySyncer       *daemon.FrecencySyncer
	configWatcher        *watcher.ConfigWatcher
	capsulePanel         *mygo.Window
	promptPanel          *mygo.Window
	imePillPanel         *mygo.Window
	inputUI              *inputEngineUI
	settingsWin          *mygo.Window
	selectionBar         *mygo.Window
	selectionBarPayload  uiapp.SelectionBarPayload
	tray                 *mygo.Tray
	permMenuItem         *mygo.MenuItem
	loginMenuItem        *mygo.MenuItem
	trayLabelItems       map[string]*mygo.MenuItem
	bridgeRunning        bool
	shuttingDown         bool
	bgWG                 sync.WaitGroup
	fallbackAccelerators map[string]host.TriggerKind
	sessionAnchor        *host.CursorAnchor
	// capsuleRevealed is set once the capsule is visibly on screen for the
	// current session (after a Leader's reveal delay), and cleared when the
	// window actually hides.
	capsuleRevealed        bool
	imePillLabel           string
	imePillTimer           *time.Timer
	imePillGen             atomic.Uint64
	frecencyGen            atomic.Uint64
	presentSeq             atomic.Uint64
	hideSeq                atomic.Uint64
	presentTransitionUntil atomic.Int64
	lastPanelHideNano      atomic.Int64
	shutdownOnce           sync.Once
}

func newDesktopApp() *desktopApp {
	return &desktopApp{}
}

func (a *desktopApp) start() {
	ctx, cancel := context.WithCancel(context.Background())

	cfgDir := resolveConfigDir()
	cfgStore := config.NewConfigStore(cfgDir)
	cfg, err := cfgStore.LoadOrInitialize()
	if err != nil {
		cfg = config.DefaultConfig()
	}
	// Resolve the UI language before the first frame renders: an explicit
	// [appearance].language wins; "auto" follows the OS preferred languages.
	i18n.SetPreferredLanguagesProvider(mygo.App.PreferredLanguages)
	i18n.SetLanguage(i18n.ResolveLanguage(cfg.Main.Appearance.Language, i18n.PreferredLanguages()))
	mygo.App.SetLanguages("en", "zh-Hans")
	if tag := i18n.Current().Tag(); tag != "en" {
		mygo.App.SetLocale(tag)
	}

	var sqliteStore *storage.SQLiteClipboardStore
	var learningStore *storage.LearningStore
	var learningWriter *storage.LearningWriter
	var clipStore clipboard.Store
	var mediaStore *clipboard.MediaStore
	if userDataDir, err := mygo.App.Path(mygo.PathUserData); err == nil && userDataDir != "" {
		// Blobs live next to the DB; an in-memory store gets no media (nothing to reference them).
		mediaStore = clipboard.NewMediaStore(filepath.Join(userDataDir, "clipboard-media"))
		dbPath := filepath.Join(userDataDir, "clipboard.db")
		if s, openErr := storage.NewSQLiteClipboardStoreFromConfig(ctx, dbPath, cfg.Main.Clipboard); openErr == nil {
			sqliteStore = s
			clipStore = s
			_, _ = s.PruneExpired(ctx, cfg.Main.Clipboard.RetentionDays, cfg.Main.Clipboard.MaxHistoryItems)
			if ls, lsErr := storage.NewLearningStore(s.DB()); lsErr == nil {
				learningStore = ls
				learningWriter = storage.NewLearningWriter(ls, storage.LearningWriterOptions{})
			}
		}
	}
	if clipStore == nil {
		clipStore = clipboard.NewMemoryStore(clipboard.NewPrivacyFilterFromConfig(cfg.Main.Clipboard))
	}

	secretStore := secrets.NewMyGoSecretsStore(keychainService)
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

	controller := uiapp.NewAppController(uiapp.Options{
		ConfigStore:    cfgStore,
		ClipboardStore: clipStore,
		SecretStore:    secretStore,
		Bridge:         hostBridge,
		AppProvider:    appProvider,
		ClipboardWriter: func(text string) {
			mygo.Clipboard.WriteText(text)
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
		OnShowIMEPill:         a.handleShowIMEPill,
		OnLanguageChange:      a.handleLanguageChange,
		OnRecordItemUse:       a.handleRecordItemUse,
		OnRecordPromptVersion: a.handleRecordPromptVersion,
		OnShowSelectionBar:    a.handleShowSelectionBar,
		OnHideSelectionBar:    a.handleHideSelectionBar,
	})

	initW, initH := uiapp.PreferredCapsuleSize(engine.PhaseLeader, cfg.Main.Appearance)
	capsulePanel := mygo.NewWindow(mygo.WindowOptions{
		Title:         appName,
		Width:         initW,
		Height:        initH,
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		Transparent:   true,
		Vibrancy:      mygo.VibrancyGlass,
		CornerRadius:  int(uiapp.NormalizeCornerRadius(cfg.Main.Appearance)),
		DisableResize: true,
		AlwaysOnTop:   true,
		SkipTaskbar:   true,
		Content:       ui.View(controller.CapsuleView),
	})
	capsulePanel.OnClose(func(e *mygo.CloseEvent) {
		e.PreventDefault()
		controller.Dispatch(engine.Event{Type: engine.EventDismiss})
	})
	capsulePanel.OnBlur(func() {
		if a.isInPresentTransition() {
			return
		}
		phase := controller.State().Phase
		if phase == engine.PhaseHidden || phase == engine.PhaseHUD {
			return
		}
		controller.Dispatch(engine.Event{Type: engine.EventDismiss})
	})

	promptW, promptH := uiapp.PreferredPromptComposerPanelSize(2)
	promptPanel := mygo.NewWindow(mygo.WindowOptions{
		Title:         i18n.T("prompts.composer_title"),
		Width:         promptW,
		Height:        promptH,
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		Transparent:   true,
		Vibrancy:      mygo.VibrancyGlass,
		CornerRadius:  int(uiapp.NormalizeCornerRadius(cfg.Main.Appearance)),
		DisableResize: true,
		AlwaysOnTop:   true,
		SkipTaskbar:   true,
		Content:       ui.View(controller.PromptComposerView),
	})
	promptPanel.OnClose(func(e *mygo.CloseEvent) {
		e.PreventDefault()
		controller.ClosePromptComposer()
	})
	promptPanel.OnBlur(func() {
		if a.isInPresentTransition() {
			return
		}
		if controller.PromptComposerOpen() {
			controller.ClosePromptComposer()
		}
	})

	pillW, pillH := uiapp.PreferredIMEPillSize()
	imePillPanel := mygo.NewWindow(mygo.WindowOptions{
		Title:         i18n.T("tray.ime_window"),
		Width:         pillW,
		Height:        pillH,
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		Transparent:   true,
		Vibrancy:      mygo.VibrancyGlass,
		CornerRadius:  15,
		DisableResize: true,
		AlwaysOnTop:   true,
		SkipTaskbar:   true,
		Content: ui.View(func(c *ui.Context) {
			uiapp.IMEPillView(c, a.currentIMEPillLabel())
		}),
	})
	imePillPanel.OnClose(func(e *mygo.CloseEvent) {
		e.PreventDefault()
		imePillPanel.Hide()
	})

	sbW, sbH := uiapp.PreferredSelectionBarWidth(nil), 34
	selectionBar := mygo.NewWindow(mygo.WindowOptions{
		Title:         "Clavis Selection",
		Width:         sbW,
		Height:        sbH,
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		Transparent:   true,
		Vibrancy:      mygo.VibrancyGlass,
		CornerRadius:  17,
		DisableResize: true,
		AlwaysOnTop:   true,
		SkipTaskbar:   true,
		Content: ui.View(func(c *ui.Context) {
			cfg := controller.Config()
			uiapp.SelectionBarView(c, a.selectionBarSnapshot(), cfg.Main.Appearance)
		}),
	})
	selectionBar.OnClose(func(e *mygo.CloseEvent) {
		e.PreventDefault()
		selectionBar.Hide()
	})

	settingsWin := mygo.NewWindow(mygo.WindowOptions{
		Title:     i18n.T("tray.settings_window"),
		Width:     960,
		Height:    640,
		MinWidth:  760,
		MinHeight: 500,
		Hidden:    true,
		StateKey:  "settings",
		Content:   ui.View(controller.SettingsView),
	})
	settingsWin.OnClose(func(e *mygo.CloseEvent) {
		e.PreventDefault()
		settingsWin.Hide()
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
	sup.RegisterWatchdogProbe(daemon.NewBridgeWatchdogProbe(daemon.BridgeLivenessInput{
		IsRunning: func() bool {
			a.mu.Lock()
			running := a.bridgeRunning
			a.mu.Unlock()
			return running
		},
		TapRecoveryCount: hostBridge.TapRecoveryCount,
	}))

	a.mu.Lock()
	a.ctx = ctx
	a.cancel = cancel
	a.cfgStore = cfgStore
	a.sqliteStore = sqliteStore
	a.learningStore = learningStore
	a.learningWriter = learningWriter
	a.clipStore = clipStore
	a.mediaStore = mediaStore
	a.hostBridge = hostBridge
	a.controller = controller
	a.supervisor = sup
	a.frecencySyncer = frecencySyncer
	a.capsulePanel = capsulePanel
	a.promptPanel = promptPanel
	a.imePillPanel = imePillPanel
	a.settingsWin = settingsWin
	a.selectionBar = selectionBar
	a.mu.Unlock()

	a.setupTray()
	a.startBridgeAndFallbackShortcut(cfg.Main.Trigger)
	if ext, ok := hostBridge.(bridge.ExtendedHostService); ok {
		ext.UpdateDaemonShortcut(cfg.Main.Daemon.InPlaceOptimizeShortcut)
	}
	sup.Start(ctx)
	a.wireInputEngineLearning(hostBridge, learningStore)
	if ui := a.setupInputEngine(ctx, hostBridge, cfg.Main.InputEngine, controller.SuggestInputCandidates); ui != nil {
		a.mu.Lock()
		a.inputUI = ui
		a.mu.Unlock()
	}

	cw := watcher.NewConfigWatcher(cfgStore, watcher.WithOnReload(func(watcher.ReloadResult) {
		a.applyConfigReload()
	}))
	if err := cw.StartWatching(ctx); err == nil {
		a.mu.Lock()
		a.configWatcher = cw
		a.mu.Unlock()
	}

	// Installed-app discovery runs inside ctrl.Start in the background so the main-thread
	// startup path never blocks on slow /Applications scans.
	a.goBG(func(bgCtx context.Context) { a.syncFrecency(bgCtx) })
	a.goBG(func(bgCtx context.Context) { a.runClipboardPoller(bgCtx) })
	go a.refreshPermissionsAsync()
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
	// them without rebuilding the tray.
	labelled := map[string]*mygo.MenuItem{
		"permissions_checking": permItem,
		"open_capsule":         itemByLabel(menu, i18n.T("tray.open_capsule")),
		"clipboard_history":    itemByLabel(menu, i18n.T("tray.clipboard_history")),
		"reload":               itemByLabel(menu, i18n.T("common.reload")),
		"open_config":          itemByLabel(menu, i18n.T("settings.configs.open_dir")),
		"settings":             itemByLabel(menu, i18n.T("tray.settings")),
		"quit":                 itemByLabel(menu, i18n.T("tray.quit")),
		"open_at_login":        loginItem,
	}
	a.mu.Lock()
	a.trayLabelItems = labelled
	a.mu.Unlock()

	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "clavis-tray",
		Icon:           uiapp.GenerateTrayIconPNG(32),
		IconIsTemplate: true,
		ToolTip:        "Clavis",
		Menu:           menu,
	})
	if err != nil {
		return
	}

	a.mu.Lock()
	a.tray = tray
	a.permMenuItem = permItem
	a.loginMenuItem = loginItem
	a.mu.Unlock()
}

func (a *desktopApp) startBridgeAndFallbackShortcut(trigger config.TriggerConfig) {
	a.mu.Lock()
	ctrl := a.controller
	br := a.hostBridge
	ctx := a.ctx
	running := a.bridgeRunning
	a.mu.Unlock()
	if ctrl == nil || ctx == nil {
		return
	}

	if !running {
		if err := ctrl.Start(ctx); err == nil {
			a.mu.Lock()
			a.bridgeRunning = true
			running = true
			a.mu.Unlock()
		}
	}

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
	go ctrl.HandleTriggerEvent(host.TriggerEvent{
		Kind:        host.TriggerLeaderKey,
		TimestampMs: time.Now().UnixMilli(),
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
	go ctrl.OpenOmnibox(tab, "")
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
	go ctrl.HandleTriggerEvent(host.TriggerEvent{
		Kind:        kind,
		TimestampMs: time.Now().UnixMilli(),
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
	br := a.hostBridge
	sqliteStore := a.sqliteStore
	learningStore := a.learningStore
	sup := a.supervisor
	ms := a.mediaStore
	a.mu.Unlock()
	if ctrl == nil {
		return
	}

	ok := ctrl.ReloadConfig()
	cfg := ctrl.Config()
	if ok {
		if updater, canUpdate := br.(dynamicBridgeUpdater); canUpdate {
			updater.UpdateTriggerConfig(cfg.Main.Trigger)
			updater.UpdateKeymapConfig(cfg.Main.Keymap)
			updater.UpdateIMEConfig(cfg.Main.IME, cfg.IMERules)
		}
		if h, ok := br.(bridge.InputEngineHost); ok {
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
	go a.refreshPermissionsAsync()
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
	if tag := i18n.Current().Tag(); tag != "en" {
		mygo.App.SetLocale(tag)
	}
	mygo.RunOnMain(func() {
		a.refreshTrayLabels()
		if a.settingsWin != nil {
			a.settingsWin.SetTitle(i18n.T("tray.settings_window"))
		}
		if a.imePillPanel != nil {
			a.imePillPanel.SetTitle(i18n.T("tray.ime_window"))
		}
		if a.promptPanel != nil {
			a.promptPanel.SetTitle(i18n.T("prompts.composer_title"))
		}
	})
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

func (a *desktopApp) refreshTrayLabels() {
	a.mu.Lock()
	items := a.trayLabelItems
	a.mu.Unlock()
	for key, item := range items {
		if item != nil {
			item.SetLabel(i18n.T("tray." + key))
		}
	}
}

func (a *desktopApp) refreshPermissionsAsync() {
	a.mu.Lock()
	ctrl := a.controller
	ctx := a.ctx
	br := a.hostBridge
	a.mu.Unlock()
	if ctrl == nil || ctx == nil {
		return
	}
	status, err := ctrl.RefreshPermissions(ctx)
	var caps host.Capabilities
	if br != nil {
		caps = br.Capabilities()
	}
	label := formatPermissionsMenuLabel(status, err, caps)
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
			cand, _, err := br.PollClipboardCandidate(ctx)
			if err != nil || cand == nil {
				continue
			}
			if cand.IsMedia() && sqliteStoreFor(store) {
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
				if keep, _ := enrichMediaCandidate(cand, cfg.Main.Clipboard, ms, src); !keep {
					continue
				}
			} else if cand.IsMedia() {
				continue
			}
			if _, changed, recErr := store.Record(ctx, *cand); recErr == nil && changed {
				a.syncFrecency(ctx)
			}
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

func promptHistoryRetentionLimit(cfg config.ClipboardConfig) int {
	if cfg.MaxHistoryItems > defaultPromptHistoryLimit {
		return cfg.MaxHistoryItems
	}
	return defaultPromptHistoryLimit
}

func (a *desktopApp) goBG(fn func(context.Context)) {
	a.mu.Lock()
	if a.shuttingDown || a.ctx == nil || a.ctx.Err() != nil {
		a.mu.Unlock()
		return
	}
	ctx := a.ctx
	a.bgWG.Add(1)
	a.mu.Unlock()
	go func() {
		defer a.bgWG.Done()
		fn(ctx)
	}()
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
		lw.QueueItemUse(itemID, time.Now())
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
		go mygo.RunOnMain(run)
		return
	}
	run()
}

func (a *desktopApp) handleRepaint() {
	a.mu.Lock()
	capWin := a.capsulePanel
	promptWin := a.promptPanel
	setWin := a.settingsWin
	pillWin := a.imePillPanel
	ctrl := a.controller
	a.mu.Unlock()
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
	if a.imePillTimer != nil {
		a.imePillTimer.Stop()
		a.imePillTimer = nil
	}
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
	if a.imePillTimer != nil {
		a.imePillTimer.Stop()
		a.imePillTimer = nil
	}
	br := a.hostBridge
	ctx := a.ctx
	prevAnchor := a.sessionAnchor
	a.mu.Unlock()

	anchorCopy := cloneValidAnchor(anchor, prevAnchor)
	go a.presentIMEPillAsync(ctx, gen, br, anchorCopy)
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
	if resolvedAnchor == nil && br != nil && ctx != nil {
		snapCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
		snap, err := br.SnapshotContext(snapCtx)
		cancel()
		if err == nil && snap.CursorAnchor != nil && snap.CursorAnchor.Valid {
			copied := *snap.CursorAnchor
			resolvedAnchor = &copied
		}
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
	if a.imePillTimer != nil {
		a.imePillTimer.Stop()
	}
	// A mode badge is glanceable; sentences and failures stay longer.
	hold := uiapp.PillDisplayDuration(a.imePillLabel)
	a.imePillTimer = time.AfterFunc(hold, func() {
		if a.imePillGen.Load() != gen {
			return
		}
		a.fadeOutIMEPill(gen)
	})
}

// fadeOutIMEPill fades the pill window out over imePillFadeOut and hides it,
// instead of letting it vanish mid-glance. A newer pill (gen changed) stops
// the fade; presentIMEPillAsync restores full opacity before showing it.
// With [appearance].animations = "off" the pill hides at once.
func (a *desktopApp) fadeOutIMEPill(gen uint64) {
	const steps = 8
	animate := true
	a.mu.Lock()
	if a.cfgStore != nil {
		animate = config.NormalizeAnimationMode(a.cfgStore.Current().Main.Appearance.Animations) != "off"
	}
	a.mu.Unlock()

	hide := func() {
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
	go func() {
		ticker := time.NewTicker(imePillFadeOut / steps)
		defer ticker.Stop()
		for i := 1; i < steps; i++ {
			<-ticker.C
			if a.imePillGen.Load() != gen {
				return
			}
			opacity := 1 - float64(i)/steps
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
		<-ticker.C
		hide()
	}()
}

func (a *desktopApp) handlePresentPhase(phase engine.CapsulePhase) {
	seq := a.presentSeq.Add(1)
	a.hideSeq.Add(1) // cancel any pending delayed hide from an exit animation
	a.markPresentTransition(350 * time.Millisecond)

	a.mu.Lock()
	br := a.hostBridge
	ctx := a.ctx
	prevAnchor := a.sessionAnchor
	a.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}

	go func() {
		var anchor *host.CursorAnchor
		if prevAnchor != nil && prevAnchor.Valid {
			copied := *prevAnchor
			anchor = &copied
		} else if br != nil {
			snapCtx, cancel := context.WithTimeout(ctx, 120*time.Millisecond)
			snap, err := br.SnapshotContext(snapCtx)
			cancel()
			if err == nil && snap.CursorAnchor != nil && snap.CursorAnchor.Valid {
				copied := *snap.CursorAnchor
				anchor = &copied
				a.setSessionAnchor(anchor)
			}
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
	}()
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
// moved on (presentSeq changed: a newer present, an executed key or a hide).
func (a *desktopApp) scheduleCapsuleReveal(seq uint64, delay time.Duration, fade bool) {
	time.AfterFunc(delay, func() {
		if a.presentSeq.Load() != seq {
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
			<-ticker.C
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
	hide := func() {
		if a.hideSeq.Load() != seq {
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
		hide()
		return
	}
	time.AfterFunc(delay, hide)
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

	go mygo.RunOnMain(func() {
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
}

func (a *desktopApp) handleClosePromptComposer() {
	go mygo.RunOnMain(func() {
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
}

func (a *desktopApp) handleOpenSettings(_ uiapp.SettingsTab) {
	go mygo.RunOnMain(func() {
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
		a.stopIMEPillTimer()

		a.mu.Lock()
		a.shuttingDown = true
		cancel := a.cancel
		cw := a.configWatcher
		sup := a.supervisor
		ctrl := a.controller
		sqliteStore := a.sqliteStore
		learningWriter := a.learningWriter
		tray := a.tray
		a.bridgeRunning = false
		a.mu.Unlock()

		if cancel != nil {
			cancel()
		}
		if cw != nil {
			cw.Stop()
		}
		if sup != nil {
			sup.Stop()
		}
		if ctrl != nil {
			_ = ctrl.Stop()
			ctrl.Close()
		}
		a.bgWG.Wait()
		if learningWriter != nil {
			// The app context is cancelled by now: flush on a fresh one.
			flushCtx, cancelFlush := context.WithTimeout(context.Background(), 2*time.Second)
			_ = learningWriter.Close(flushCtx)
			cancelFlush()
		}
		if sqliteStore != nil {
			_ = sqliteStore.Close()
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
	go mygo.RunOnMain(func() {
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
}

// handleHideSelectionBar hides the floating action bar.
func (a *desktopApp) handleHideSelectionBar() {
	a.mu.Lock()
	win := a.selectionBar
	a.mu.Unlock()
	if win == nil {
		return
	}
	go mygo.RunOnMain(func() {
		if win.IsVisible() {
			win.Hide()
		}
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
	case "backspace":
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
