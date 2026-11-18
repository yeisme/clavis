package main

import (
	"bytes"
	"context"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/internal/daemon"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/internal/storage"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/uiapp"
)

func TestHasSmokeCheckFlag(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"--help"}, false},
		{[]string{"--smoke-check"}, true},
		{[]string{"--foo", "--smoke-check"}, true},
		{[]string{"--", "--smoke-check"}, false},
	}
	for _, tc := range cases {
		if got := hasSmokeCheckFlag(tc.args); got != tc.want {
			t.Errorf("hasSmokeCheckFlag(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestRunSmokeCheck_OutputAndNoUserConfigMutation(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)

	var out bytes.Buffer
	if err := runSmokeCheck(&out); err != nil {
		t.Fatalf("runSmokeCheck returned error: %v", err)
	}

	got := strings.TrimSpace(out.String())
	wantPrefix := "Clavis smoke-check ok (leader=right_option, os=" + runtime.GOOS + ", arch=" + runtime.GOARCH + ")"
	if got != wantPrefix {
		t.Fatalf("smoke-check output = %q, want %q", got, wantPrefix)
	}

	userCfgDir := filepath.Join(fakeHome, ".config", "clavis")
	if _, err := os.Stat(userCfgDir); !os.IsNotExist(err) {
		t.Fatalf("smoke-check should not create %s, stat err=%v", userCfgDir, err)
	}
}

func TestComputeCapsuleBounds_PlacementAndWorkAreaClamping(t *testing.T) {
	displays := []mygo.Display{
		{
			ID:       1,
			Primary:  true,
			Bounds:   mygo.Rectangle{X: 0, Y: 0, Width: 1440, Height: 900},
			WorkArea: mygo.Rectangle{X: 0, Y: 25, Width: 1440, Height: 825}, // 25pt menu bar, 50pt dock
		},
		{
			ID:       2,
			Primary:  false,
			Bounds:   mygo.Rectangle{X: 1440, Y: 0, Width: 1920, Height: 1080},
			WorkArea: mygo.Rectangle{X: 1440, Y: 0, Width: 1920, Height: 1040},
		},
	}
	capsuleSize := bridge.Size{Width: 680, Height: 340}

	t.Run("places below valid caret anchor", func(t *testing.T) {
		anchor := &host.CursorAnchor{X: 200, Y: 120, Width: 2, Height: 20, Valid: true}
		got := computeCapsuleBounds(anchor, mygo.Point{X: 10, Y: 10}, capsuleSize, displays)

		if got.X != 200 {
			t.Fatalf("X = %d, want 200", got.X)
		}
		wantY := int(120 + 20 + bridge.PopoverAnchorGap)
		if got.Y != wantY {
			t.Fatalf("Y = %d, want %d", got.Y, wantY)
		}
		if got.Width != 680 || got.Height != 340 {
			t.Fatalf("size = (%d,%d), want (680,340)", got.Width, got.Height)
		}
	})

	t.Run("flips above caret near bottom of work area", func(t *testing.T) {
		anchor := &host.CursorAnchor{X: 300, Y: 780, Width: 2, Height: 20, Valid: true}
		got := computeCapsuleBounds(anchor, mygo.Point{}, capsuleSize, displays)

		wantY := int(780 - 340 - bridge.PopoverAnchorGap)
		if got.Y != wantY {
			t.Fatalf("Y = %d, want %d (flipped above anchor)", got.Y, wantY)
		}
	})

	t.Run("clamps horizontally and vertically inside work area with margin", func(t *testing.T) {
		anchor := &host.CursorAnchor{X: 1350, Y: 20, Width: 2, Height: 16, Valid: true}
		got := computeCapsuleBounds(anchor, mygo.Point{}, capsuleSize, displays)

		maxAllowedX := 1440 - 680 - int(bridge.PopoverScreenMargin)
		if got.X != maxAllowedX {
			t.Fatalf("X = %d, want %d", got.X, maxAllowedX)
		}
		minAllowedY := 25 + int(bridge.PopoverScreenMargin)
		if got.Y < minAllowedY {
			t.Fatalf("Y = %d, want >= %d", got.Y, minAllowedY)
		}
	})

	t.Run("uses cursor fallback when anchor is nil or invalid and targets secondary display", func(t *testing.T) {
		invalid := &host.CursorAnchor{X: 0, Y: 0, Valid: false}
		cursor := mygo.Point{X: 1800, Y: 200}
		got := computeCapsuleBounds(invalid, cursor, capsuleSize, displays)

		if got.X != 1800 {
			t.Fatalf("X = %d, want 1800 on secondary display", got.X)
		}
		wantY := int(200 + 18 + bridge.PopoverAnchorGap)
		if got.Y != wantY {
			t.Fatalf("Y = %d, want %d", got.Y, wantY)
		}
	})
}

func TestComputeIMEPillBounds_PlacementAndWorkAreaClamping(t *testing.T) {
	displays := []mygo.Display{
		{
			ID:       1,
			Primary:  true,
			Bounds:   mygo.Rectangle{X: 0, Y: 0, Width: 1440, Height: 900},
			WorkArea: mygo.Rectangle{X: 0, Y: 25, Width: 1440, Height: 825},
		},
		{
			ID:       2,
			Primary:  false,
			Bounds:   mygo.Rectangle{X: 1440, Y: 0, Width: 1920, Height: 1080},
			WorkArea: mygo.Rectangle{X: 1440, Y: 0, Width: 1920, Height: 1040},
		},
	}
	pw, ph := uiapp.PreferredIMEPillSize()
	pillSize := bridge.Size{Width: float64(pw), Height: float64(ph)}

	t.Run("places compactly above and right of valid caret anchor without covering typing line", func(t *testing.T) {
		anchor := &host.CursorAnchor{X: 200, Y: 120, Width: 2, Height: 20, Valid: true}
		got := computeIMEPillBounds(anchor, mygo.Point{X: 10, Y: 10}, pillSize, displays)

		wantX := int(200 + 2 + imePillOffsetX)
		wantY := int(120 - float64(ph) - imePillOffsetY)
		if got.X != wantX || got.Y != wantY {
			t.Fatalf("pos = (%d,%d), want (%d,%d)", got.X, got.Y, wantX, wantY)
		}
		if got.Width != pw || got.Height != ph {
			t.Fatalf("size = (%d,%d), want (%d,%d)", got.Width, got.Height, pw, ph)
		}
		if got.Y+got.Height > int(anchor.Y) {
			t.Fatalf("pill bottom %d overlaps typing line starting at %d", got.Y+got.Height, int(anchor.Y))
		}
	})

	t.Run("flips below typing line near top of work area and clamps right edge", func(t *testing.T) {
		anchor := &host.CursorAnchor{X: 1420, Y: 30, Width: 2, Height: 20, Valid: true}
		got := computeIMEPillBounds(anchor, mygo.Point{}, pillSize, displays)

		maxAllowedX := 1440 - pw - int(bridge.PopoverScreenMargin)
		if got.X != maxAllowedX {
			t.Fatalf("X = %d, want clamped %d", got.X, maxAllowedX)
		}
		wantY := int(30 + 20 + imePillOffsetY)
		if got.Y != wantY {
			t.Fatalf("Y = %d, want flipped below typing line %d", got.Y, wantY)
		}
	})

	t.Run("uses cursor fallback when anchor is nil or invalid on secondary display", func(t *testing.T) {
		invalid := &host.CursorAnchor{X: 0, Y: 0, Valid: false}
		cursor := mygo.Point{X: 1800, Y: 200}
		got := computeIMEPillBounds(invalid, cursor, bridge.Size{}, displays)

		wantX := int(1800 + 1 + imePillOffsetX)
		wantY := int(200 - float64(ph) - imePillOffsetY)
		if got.X != wantX || got.Y != wantY {
			t.Fatalf("pos = (%d,%d), want (%d,%d) on secondary display", got.X, got.Y, wantX, wantY)
		}
		if got.Width != pw || got.Height != ph {
			t.Fatalf("size = (%d,%d), want (%d,%d)", got.Width, got.Height, pw, ph)
		}
	})

	t.Run("sizes status/layer pills wider while clamping to the work area", func(t *testing.T) {
		labelW, labelH := uiapp.PreferredPillSizeForLabel("LAYER • NAV")
		if labelW <= pw || labelH <= 0 {
			t.Fatalf("PreferredPillSizeForLabel('LAYER • NAV') = (%d,%d), want wider than (%d,%d)", labelW, labelH, pw, ph)
		}
		anchor := &host.CursorAnchor{X: 1400, Y: 120, Width: 2, Height: 20, Valid: true}
		got := computeIMEPillBounds(anchor, mygo.Point{}, bridge.Size{Width: float64(labelW), Height: float64(labelH)}, displays)

		if got.Width != labelW || got.Height != labelH {
			t.Fatalf("size = (%d,%d), want (%d,%d)", got.Width, got.Height, labelW, labelH)
		}
		maxAllowedX := 1440 - labelW - int(bridge.PopoverScreenMargin)
		if got.X != maxAllowedX {
			t.Fatalf("X = %d, want clamped %d", got.X, maxAllowedX)
		}
	})
}

func TestGenerateTrayIconPNG(t *testing.T) {
	data := uiapp.GenerateTrayIconPNG(32)
	if len(data) == 0 {
		t.Fatal("uiapp.GenerateTrayIconPNG(32) returned empty bytes")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode failed: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 32 || b.Dy() != 32 {
		t.Fatalf("tray icon size = %dx%d, want 32x32", b.Dx(), b.Dy())
	}

	var opaquePixels, transparentPixels int
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A == 0 {
				transparentPixels++
				continue
			}
			opaquePixels++
			if c.R != 0 || c.G != 0 || c.B != 0 {
				t.Fatalf("template icon pixel (%d,%d) has non-black RGB (%d,%d,%d)", x, y, c.R, c.G, c.B)
			}
		}
	}
	if opaquePixels == 0 || transparentPixels == 0 {
		t.Fatalf("expected both drawn and transparent pixels, got opaque=%d transparent=%d", opaquePixels, transparentPixels)
	}
}

func TestToMyGoAccelerator(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"option+space", "Alt+Space"},
		{"command+shift+k", "Super+Shift+K"},
		{"ctrl+alt+p", "Ctrl+Alt+P"},
		{"cmd+grave", "Super+`"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := toMyGoAccelerator(tc.in); got != tc.want {
			t.Errorf("toMyGoAccelerator(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatPermissionsMenuLabel(t *testing.T) {
	if got := formatPermissionsMenuLabel(host.PermissionStatus{Accessibility: true, InputMonitoring: true}, nil, host.Capabilities{Accessibility: true, InputMonitoring: true}); got != "Permissions: All Granted" {
		t.Fatalf("got %q", got)
	}
	if got := formatPermissionsMenuLabel(host.PermissionStatus{Accessibility: false, InputMonitoring: true}, nil, host.Capabilities{Accessibility: true, InputMonitoring: true}); got != "Permissions: Grant Accessibility" {
		t.Fatalf("got %q", got)
	}
	if got := formatPermissionsMenuLabel(host.PermissionStatus{}, nil, host.Capabilities{}); got != "Permissions: Standard Mode" {
		t.Fatalf("got %q", got)
	}
}

func TestPlanFallbackShortcutsCapsuleAndClipboard(t *testing.T) {
	plan := planFallbackShortcuts(config.TriggerConfig{CapsuleShortcut: "option+space", ClipboardShortcut: "alt+shift+c"})
	if len(plan) != 2 || plan[0].Kind != host.TriggerCapsuleToggle || plan[1].Kind != host.TriggerClipboardToggle {
		t.Fatalf("expected capsule + clipboard fallback shortcuts, got %+v", plan)
	}
	if plan[0].Accelerator == plan[1].Accelerator || plan[1].Accelerator == "" {
		t.Fatalf("expected distinct accelerators, got %+v", plan)
	}

	// Same chord: de-duplicated, capsule wins.
	dup := planFallbackShortcuts(config.TriggerConfig{CapsuleShortcut: "alt+shift+c", ClipboardShortcut: "shift+option+c"})
	if len(dup) != 1 || dup[0].Kind != host.TriggerCapsuleToggle {
		t.Fatalf("expected de-duplicated capsule shortcut, got %+v", dup)
	}

	// Empty clipboard shortcut is skipped.
	only := planFallbackShortcuts(config.TriggerConfig{CapsuleShortcut: "option+space"})
	if len(only) != 1 || only[0].Kind != host.TriggerCapsuleToggle {
		t.Fatalf("expected only capsule shortcut, got %+v", only)
	}
}

func TestNilLearningStoreSafeEverywhere(t *testing.T) {
	var ls *storage.LearningStore
	ctx := context.Background()

	if err := ls.RecordIMECommit(ctx, "ni", "你", "好", true); err != nil {
		t.Fatalf("RecordIMECommit err=%v", err)
	}
	if err := ls.RecordIMECommit(nil, "ni", "你", "", false); err != nil {
		t.Fatalf("RecordIMECommit nil ctx err=%v", err)
	}
	if boost := ls.LookupBoost("ni", "你", "好"); boost != 0 {
		t.Fatalf("LookupBoost = %d, want 0", boost)
	}
	if boost, err := ls.LookupIMEBoost(ctx, "ni", "你", "好"); err != nil || boost != 0 {
		t.Fatalf("LookupIMEBoost = (%d, %v), want (0, nil)", boost, err)
	}
	if words := ls.CustomWordsForPrefix("n"); len(words) != 0 {
		t.Fatalf("CustomWordsForPrefix = %v, want empty", words)
	}
	if err := ls.DeleteLearnedWord(ctx, "ni", "你"); err != nil {
		t.Fatalf("DeleteLearnedWord err=%v", err)
	}
	if st, err := ls.Stats(ctx); err != nil || st != (storage.LexiconStats{}) {
		t.Fatalf("Stats = (%+v, %v)", st, err)
	}
	if err := ls.RecordItemUse(ctx, "clipboard:x", time.Now()); err != nil {
		t.Fatalf("RecordItemUse err=%v", err)
	}
	if snap := ls.FrecencySnapshot(time.Now()); len(snap) != 0 {
		t.Fatalf("FrecencySnapshot = %v, want empty", snap)
	}
	if err := ls.RecordPromptVersion(ctx, "p1", "/p", "raw", "final", "coding"); err != nil {
		t.Fatalf("RecordPromptVersion err=%v", err)
	}
	if hist, err := ls.RecentPromptHistory(ctx, "p1", 5); err != nil || len(hist) != 0 {
		t.Fatalf("RecentPromptHistory = (%v, %v)", hist, err)
	}
	if err := ls.PruneHistory(ctx, 100); err != nil {
		t.Fatalf("PruneHistory err=%v", err)
	}
}

func TestControllerOnRecordItemUseOnClipboardPasteAndCopy(t *testing.T) {
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}
	clipStore := clipboard.NewMemoryStore(clipboard.NewPrivacyFilterFromConfig(cfgStore.Current().Main.Clipboard))
	ctx := context.Background()
	item1, _, err := clipStore.Record(ctx, clipboard.CaptureCandidate{Content: "alpha item", Timestamp: time.Now().UTC()})
	if err != nil {
		t.Fatalf("Record item1: %v", err)
	}
	item2, _, err := clipStore.Record(ctx, clipboard.CaptureCandidate{Content: "beta item", Timestamp: time.Now().UTC().Add(time.Second)})
	if err != nil {
		t.Fatalf("Record item2: %v", err)
	}

	var mu sync.Mutex
	var used []string
	var copied []string

	ctrl := uiapp.NewAppController(uiapp.Options{
		ConfigStore:     cfgStore,
		ClipboardStore:  clipStore,
		SecretStore:     secrets.NewMemoryAPIKeyStore(),
		ClipboardWriter: func(text string) { copied = append(copied, text) },
		OnRecordItemUse: func(itemID string) {
			mu.Lock()
			used = append(used, itemID)
			mu.Unlock()
		},
	})
	defer ctrl.Close()

	// Primary action (copy) on index 0 (item2, most recent).
	ctrl.OpenOmnibox(config.TabClipboard, "")
	ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})

	// Secondary action (paste) on index 0 (item2).
	ctrl.OpenOmnibox(config.TabClipboard, "")
	ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSecondarySelection})

	// SelectAndConfirmIndex primary (copy) on index 1 (item1).
	ctrl.OpenOmnibox(config.TabClipboard, "")
	ctrl.Dispatch(engine.Event{Type: engine.EventSelectAndConfirmIndex, Index: 1})

	mu.Lock()
	gotUsed := slices.Clone(used)
	mu.Unlock()

	wantUsed := []string{
		"clipboard:" + item2.ID,
		"clipboard:" + item2.ID,
		"clipboard:" + item1.ID,
	}
	if !slices.Equal(gotUsed, wantUsed) {
		t.Fatalf("recorded uses = %v, want %v", gotUsed, wantUsed)
	}
	if len(copied) != 2 || copied[0] != "beta item" || copied[1] != "alpha item" {
		t.Fatalf("copied texts = %v", copied)
	}
}

func TestDesktopAppDaemonFrecencyReloadAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfgDir := t.TempDir()
	cfgStore := config.NewConfigStore(cfgDir)
	cfg, err := cfgStore.LoadOrInitialize()
	if err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "clipboard.db")
	sqliteStore, err := storage.NewSQLiteClipboardStoreFromConfig(ctx, dbPath, cfg.Main.Clipboard)
	if err != nil {
		t.Fatalf("NewSQLiteClipboardStoreFromConfig: %v", err)
	}
	learningStore, err := storage.NewLearningStore(sqliteStore.DB())
	if err != nil {
		t.Fatalf("NewLearningStore: %v", err)
	}

	app := newDesktopApp()
	ctrl := uiapp.NewAppController(uiapp.Options{
		ConfigStore:     cfgStore,
		ClipboardStore:  sqliteStore,
		SecretStore:     secrets.NewMemoryAPIKeyStore(),
		ClipboardWriter: func(string) {},
		OnRecordItemUse: app.handleRecordItemUse,
	})

	supCfg := cfg.Main.Daemon
	supCfg.WatchdogIntervalMs = 20
	sup := daemon.NewResidentSupervisor(supCfg)
	sup.RegisterCompactionHook(daemon.NewSQLiteCompactionHook(sqliteStore.DB()))
	sup.RegisterWatchdogProbe(daemon.NewBridgeWatchdogProbe(daemon.BridgeLivenessInput{
		IsRunning: func() bool { return true },
	}))

	syncer := daemon.NewFrecencySyncer(
		func(now time.Time) map[string]int {
			app.mu.Lock()
			ls := app.learningStore
			app.mu.Unlock()
			return ls.FrecencySnapshot(now)
		},
		app.dispatchFrecencyScores,
	)

	app.mu.Lock()
	app.ctx = ctx
	app.cancel = cancel
	app.cfgStore = cfgStore
	app.sqliteStore = sqliteStore
	app.learningStore = learningStore
	app.clipStore = sqliteStore
	app.controller = ctrl
	app.supervisor = sup
	app.frecencySyncer = syncer
	app.mu.Unlock()

	sup.Start(ctx)

	// Record a clipboard item and trigger paste via controller -> updates LearningStore -> routes EventSetFrecencyScores.
	item, _, err := sqliteStore.Record(ctx, clipboard.CaptureCandidate{Content: "hello frecency", Timestamp: time.Now().UTC()})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	ctrl.OpenOmnibox(config.TabClipboard, "")
	ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})

	wantKey := "clipboard:" + item.ID
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if ctrl.State().FrecencyScores[wantKey] > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := ctrl.State().FrecencyScores[wantKey]; got <= 0 {
		t.Fatalf("FrecencyScores[%q] = %d, want > 0", wantKey, got)
	}

	// Hot reload updates supervisor thresholds and prunes cleanly.
	app.applyConfigReload()

	// Shutdown stops supervisor and background workers before closing SQLite.
	app.shutdown()

	// Subsequent calls after shutdown must be safe no-ops (no panic, no write to closed DB).
	app.handleRecordItemUse(wantKey)
	app.syncFrecency(context.Background())
}

func TestDesktopAppPromptHistoryRecordedOnCommitAndAIOptimize(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfgStore := config.NewConfigStore(t.TempDir())
	cfg, err := cfgStore.LoadOrInitialize()
	if err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "prompt_hist.db")
	sqliteStore, err := storage.NewSQLiteClipboardStoreFromConfig(ctx, dbPath, cfg.Main.Clipboard)
	if err != nil {
		t.Fatalf("NewSQLiteClipboardStoreFromConfig: %v", err)
	}
	learningStore, err := storage.NewLearningStore(sqliteStore.DB())
	if err != nil {
		t.Fatalf("NewLearningStore: %v", err)
	}

	app := newDesktopApp()
	var injMu sync.Mutex
	var injected []string
	br := &mockMainBridge{
		HostService: bridge.NewHostService(),
		onInject: func(text string) {
			injMu.Lock()
			injected = append(injected, text)
			injMu.Unlock()
		},
	}
	ctrl := uiapp.NewAppController(uiapp.Options{
		ConfigStore:           cfgStore,
		ClipboardStore:        sqliteStore,
		SecretStore:           secrets.NewMemoryAPIKeyStore(),
		Bridge:                br,
		ClipboardWriter:       func(string) {},
		OnRecordPromptVersion: app.handleRecordPromptVersion,
	})

	app.mu.Lock()
	app.ctx = ctx
	app.cancel = cancel
	app.cfgStore = cfgStore
	app.sqliteStore = sqliteStore
	app.learningStore = learningStore
	app.clipStore = sqliteStore
	app.controller = ctrl
	app.mu.Unlock()
	defer app.shutdown()

	// 1. Prompt template rendered + injected via PromptComposer -> writes to prompt_history.
	tpl := config.PromptTemplate{
		ID:              "tpl-review",
		Trigger:         "/rev",
		Title:           "Review",
		Template:        "Review {{target}}",
		OptimizerPreset: "coding",
		Slots:           []config.PromptSlot{{Name: "target", Label: "Target", Kind: "text", Required: true}},
	}
	ctrl.OpenPromptComposer(tpl, engine.CapsuleContext{AppName: "GoLand"})
	ctrl.SetPromptSlotValue("target", "main.go")
	if !ctrl.CommitPromptComposer() {
		t.Fatal("CommitPromptComposer returned false")
	}

	deadline := time.Now().Add(2 * time.Second)
	var hist []storage.PromptHistoryEntry
	for time.Now().Before(deadline) {
		hist, err = learningStore.RecentPromptHistory(ctx, "tpl-review", 5)
		if err == nil && len(hist) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || len(hist) != 1 {
		t.Fatalf("expected 1 prompt history entry for tpl-review, got len=%d err=%v", len(hist), err)
	}
	if hist[0].Trigger != "/rev" || hist[0].RawDraft != "Review {{target}}" ||
		hist[0].FinalOutput != "Review main.go" || hist[0].Preset != "coding" {
		t.Fatalf("unexpected prompt template history row: %+v", hist[0])
	}

	// 2. AI-upgraded draft committed via EventAcceptAIOutput -> writes to prompt_history.
	ctrl.OpenOmnibox(config.TabActions, "//fix flaky retry test")
	ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
	ctrl.Dispatch(engine.Event{Type: engine.EventAIChunk, Chunk: "Write a deterministic unit test for retry backoff."})
	ctrl.Dispatch(engine.Event{Type: engine.EventAICompleted})
	ctrl.Dispatch(engine.Event{Type: engine.EventAcceptAIOutput})

	deadline = time.Now().Add(2 * time.Second)
	var optHist []storage.PromptHistoryEntry
	for time.Now().Before(deadline) {
		optHist, err = learningStore.RecentPromptHistory(ctx, "optimize", 5)
		if err == nil && len(optHist) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || len(optHist) != 1 {
		t.Fatalf("expected 1 prompt history entry for optimize, got len=%d err=%v", len(optHist), err)
	}
	if optHist[0].Trigger != "//" || optHist[0].RawDraft != "fix flaky retry test" ||
		optHist[0].FinalOutput != "Write a deterministic unit test for retry backoff." ||
		optHist[0].Preset != "coding" {
		t.Fatalf("unexpected optimize history row: %+v", optHist[0])
	}
}

type mockMainBridge struct {
	bridge.HostService
	onInject func(string)
}

func (m *mockMainBridge) InjectText(_ context.Context, text string, _ int, _ int) error {
	if m.onInject != nil {
		m.onInject(text)
	}
	return nil
}

// TestCloseHidesLetsQuitThrough: closing one of Clavis's windows hides it,
// but while the app quits the close must go through: MyGo closes every
// window before quitting and a refused close cancels the quit, which left
// tray Quit (and App.Relaunch) doing nothing.
func TestCloseHidesLetsQuitThrough(t *testing.T) {
	a := newDesktopApp()
	hidden := 0
	onClose := a.closeHides(func() { hidden++ })

	e := &mygo.CloseEvent{}
	onClose(e)
	if !e.DefaultPrevented() || hidden != 1 {
		t.Fatalf("close outside a quit: prevented=%v hidden=%d; want the window kept and hidden", e.DefaultPrevented(), hidden)
	}

	a.quitting.Store(true)
	e = &mygo.CloseEvent{}
	onClose(e)
	if e.DefaultPrevented() || hidden != 1 {
		t.Fatalf("close during a quit: prevented=%v hidden=%d; want it to go through", e.DefaultPrevented(), hidden)
	}

	// A canceled quit restores hiding.
	a.quitting.Store(false)
	e = &mygo.CloseEvent{}
	onClose(e)
	if !e.DefaultPrevented() || hidden != 2 {
		t.Fatalf("close after a canceled quit: prevented=%v hidden=%d", e.DefaultPrevented(), hidden)
	}
}
