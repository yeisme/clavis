//go:build darwin

package bridge

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

func TestClassifyDarwinClipboard(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		types []string
		want  clipboard.Kind
	}{
		{name: "text wins over file and image", text: "hello", types: []string{"public.file-url", "public.png"}, want: clipboard.KindText},
		{name: "file URL", types: []string{"public.file-url"}, want: clipboard.KindFile},
		{name: "PNG", types: []string{"public.png"}, want: clipboard.KindImage},
		{name: "image MIME", types: []string{"image/png"}, want: clipboard.KindImage},
		{name: "file wins over image", types: []string{"public.png", "public.file-url"}, want: clipboard.KindFile},
		{name: "unknown representations", types: []string{"public.rtf", "org.example.custom"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyDarwinClipboard(tt.text, tt.types); got != tt.want {
				t.Fatalf("classifyDarwinClipboard(%q, %v) = %q, want %q", tt.text, tt.types, got, tt.want)
			}
		})
	}
}

func TestDarwinBridge_PureGoRuntimeCalls(t *testing.T) {
	b := NewHostService().(*darwinBridge)
	ctx := context.Background()

	if err := initDarwin(); err != nil {
		t.Fatalf("initDarwin failed: %v", err)
	}

	if _, err := b.Permissions(ctx); err != nil {
		t.Fatalf("Permissions failed: %v", err)
	}

	sources, err := b.InputSources(ctx)
	if err != nil {
		t.Fatalf("InputSources failed: %v", err)
	}
	if len(sources) == 0 {
		t.Fatal("expected at least one macOS input source")
	}

	cur, err := b.CurrentInputSource(ctx)
	if err != nil {
		t.Fatalf("CurrentInputSource failed: %v", err)
	}
	if cur.ID == "" {
		t.Fatalf("expected non-empty current input source ID, got %+v", cur)
	}

	displays := darwinDisplays()
	if len(displays) == 0 {
		t.Fatal("expected at least one display from darwinDisplays()")
	}

	snap, err := b.SnapshotContext(ctx)
	if err != nil {
		t.Fatalf("SnapshotContext failed: %v", err)
	}
	if snap.CursorAnchor == nil || !snap.CursorAnchor.Valid {
		t.Fatalf("expected valid fallback cursor anchor, got %+v", snap.CursorAnchor)
	}
}

func TestDarwinBridgeInlineSelfAppGuardAndSnippetReset(t *testing.T) {
	b := newPlatformBridge(Options{}).(*darwinBridge)
	q := make(chan host.TriggerEvent, 8)
	b.mu.Lock()
	b.triggerQueue = q
	b.mu.Unlock()

	cfg := config.DefaultInputEngineConfig()
	cfg.Enabled = true
	cfg.Mode = "inline_hook"
	b.ConfigureInputEngine(cfg)
	b.observeInlineInputSource(host.InputSourceInfo{ID: "com.apple.keylayout.ABC", IsASCII: true})
	b.setCachedFrontmostApp(AppIdentity{BundleID: "com.apple.Safari", ProcessName: "Safari"})

	if !b.tryDispatchInlineKeyDown(host.InlineKeyEvent{KeyName: "n", Rune: "n", IsDown: true, TimestampMs: 100}, false) {
		t.Fatal("expected external app ASCII keydown to be consumed by input engine")
	}
	if got := b.ime.eng.Load().Raw(); got != "n" {
		t.Fatalf("engine raw = %q, want %q", got, "n")
	}
	select {
	case ev := <-q:
		if ev.Kind != host.TriggerSnippetReset || ev.Key != "input_engine" {
			t.Fatalf("unexpected trigger event: %+v", ev)
		}
	default:
		t.Fatal("expected TriggerSnippetReset when input engine consumes a key")
	}

	// Focusing Clavis by bundle ID must reject takeover and reset any open composition.
	b.setCachedFrontmostApp(AppIdentity{BundleID: "com.yeisme.clavis", ProcessName: "Clavis"})
	if b.tryDispatchInlineKeyDown(host.InlineKeyEvent{KeyName: "i", Rune: "i", IsDown: true, TimestampMs: 200}, false) {
		t.Fatal("self-app window (bundle ID) must not be hijacked by input engine")
	}
	if got := b.ime.eng.Load().Raw(); got != "" {
		t.Fatalf("self-app guard must reset open composition, got raw=%q", got)
	}

	// Also protect unbundled dev binaries matched by process name alone.
	b.setCachedFrontmostApp(AppIdentity{ProcessName: "clavis"})
	if b.inputEngineHandleInline(host.InlineKeyEvent{KeyName: "a", Rune: "a", IsDown: true, TimestampMs: 250}) {
		t.Fatal("self-app window (process name) must not be hijacked by inputEngineHandleInline")
	}
}

func TestDarwinBridgeMouseDownResetsInputEngine(t *testing.T) {
	b := newPlatformBridge(Options{}).(*darwinBridge)
	q := make(chan host.TriggerEvent, 4)
	b.mu.Lock()
	b.triggerQueue = q
	b.mu.Unlock()

	cfg := config.DefaultInputEngineConfig()
	cfg.Enabled = true
	cfg.Mode = "inline_hook"
	b.ConfigureInputEngine(cfg)
	b.observeInlineInputSource(host.InputSourceInfo{ID: "com.apple.keylayout.ABC", IsASCII: true})
	b.setCachedFrontmostApp(AppIdentity{BundleID: "com.apple.TextEdit", ProcessName: "TextEdit"})

	if !b.tryDispatchInlineKeyDown(host.InlineKeyEvent{KeyName: "n", Rune: "n", IsDown: true, TimestampMs: 10}, false) {
		t.Fatal("expected key to start composition")
	}
	<-q // drain input_engine snippet reset

	// Drain any dirty signal from ConfigureInputEngine / keydown.
	select {
	case <-b.ime.dirty:
	default:
	}

	b.handleMouseDownReset(true, 20)
	if got := b.ime.eng.Load().Raw(); got != "" {
		t.Fatalf("mouse click must reset open composition, got raw=%q", got)
	}
	select {
	case <-b.ime.dirty:
	default:
		t.Fatal("mouse click reset must notify input engine view to hide candidate panel")
	}
	select {
	case ev := <-q:
		if ev.Kind != host.TriggerSnippetReset {
			t.Fatalf("expected TriggerSnippetReset on click, got %+v", ev)
		}
	default:
		t.Fatal("expected TriggerSnippetReset on mouse down")
	}
}

func TestDarwinBridgeIMEObserverAppSwitchAndManualIME(t *testing.T) {
	var notified []host.InputSourceInfo
	b := newPlatformBridge(Options{
		IMEConfig: config.IMEConfig{Enabled: false},
		OnInputSourceChanged: func(info host.InputSourceInfo) {
			notified = append(notified, info)
		},
	}).(*darwinBridge)
	q := make(chan host.TriggerEvent, 8)
	b.mu.Lock()
	b.triggerQueue = q
	b.mu.Unlock()

	cfg := config.DefaultInputEngineConfig()
	cfg.Enabled = true
	cfg.Mode = "inline_hook"
	b.ConfigureInputEngine(cfg)
	b.observeInlineInputSource(host.InputSourceInfo{ID: "com.apple.keylayout.ABC", IsASCII: true})

	curApp := AppIdentity{BundleID: "com.apple.Safari", ProcessName: "Safari"}
	curSrc := host.InputSourceInfo{ID: "com.apple.keylayout.ABC", Name: "ABC", IsASCII: true}
	var st darwinIMEObserverState
	ops := darwinIMEObserverOps{
		frontmostApp: func() AppIdentity { return curApp },
		currentInput: func(context.Context) (host.InputSourceInfo, error) { return curSrc, nil },
		applySwitch:  func(context.Context, IMEDecision) bool { return false },
		waitDelay:    func(context.Context, time.Duration) bool { return true },
	}
	ctx := context.Background()

	// Start a composition in Safari.
	b.setCachedFrontmostApp(curApp)
	b.tryDispatchInlineKeyDown(host.InlineKeyEvent{KeyName: "n", Rune: "n", IsDown: true, TimestampMs: 1}, false)
	<-q

	// Tick 1: primes observer with Safari + ABC without firing a spurious pill.
	b.stepIMEObserver(ctx, &st, ops)
	if len(notified) != 0 {
		t.Fatalf("priming tick should not notify onIME, got %+v", notified)
	}
	if b.ime.eng.Load().Raw() != "n" {
		t.Fatalf("same-app tick must preserve composition, got %q", b.ime.eng.Load().Raw())
	}

	// Tick 2: user manually switches macOS input source while auto-switch is disabled.
	curSrc = host.InputSourceInfo{ID: "com.apple.inputmethod.SCIM.ITABC", Name: "Pinyin - Simplified", IsASCII: false}
	b.stepIMEObserver(ctx, &st, ops)
	if len(notified) != 1 || notified[0].ID != curSrc.ID {
		t.Fatalf("manual IME switch must notify onIME, got %+v", notified)
	}

	// Tick 3: unchanged input source must not re-notify.
	b.stepIMEObserver(ctx, &st, ops)
	if len(notified) != 1 {
		t.Fatalf("unchanged IME must not re-notify, got %+v", notified)
	}

	// Tick 4: switching to another app must reset the open composition and emit snippet reset.
	curApp = AppIdentity{BundleID: "com.mitchellh.ghostty", ProcessName: "Ghostty"}
	b.stepIMEObserver(ctx, &st, ops)
	if got := b.ime.eng.Load().Raw(); got != "" {
		t.Fatalf("app switch must reset open composition, got raw=%q", got)
	}
	select {
	case ev := <-q:
		if ev.Kind != host.TriggerSnippetReset {
			t.Fatalf("expected TriggerSnippetReset on app switch, got %+v", ev)
		}
	default:
		t.Fatal("expected TriggerSnippetReset on app switch")
	}

	// Tick 5: start composition in Ghostty then focus Clavis -> composition resets, pill does NOT flash.
	curSrc = host.InputSourceInfo{ID: "com.apple.keylayout.ABC", Name: "ABC", IsASCII: true}
	b.stepIMEObserver(ctx, &st, ops) // notifies switch back to ABC -> len(notified) == 2
	b.tryDispatchInlineKeyDown(host.InlineKeyEvent{KeyName: "w", Rune: "w", IsDown: true, TimestampMs: 2}, false)
	<-q
	curApp = AppIdentity{BundleID: "com.yeisme.clavis", ProcessName: "Clavis"}
	curSrc = host.InputSourceInfo{ID: "com.apple.inputmethod.SCIM.ITABC", Name: "Pinyin", IsASCII: false}
	b.stepIMEObserver(ctx, &st, ops)
	if got := b.ime.eng.Load().Raw(); got != "" {
		t.Fatalf("switching to Clavis must reset open composition, got raw=%q", got)
	}
	if len(notified) != 2 {
		t.Fatalf("Clavis self-window must not trigger onIME pill notifications, got %+v", notified)
	}
}

func TestReplaceEditableFieldWholeFieldAndSelection(t *testing.T) {
	ctx := context.Background()

	t.Run("whole field prefers AXValue when settable", func(t *testing.T) {
		var calls []string
		err := replaceEditableFieldWithOps(ctx, "rewritten", false, replaceFieldOps{
			setSelectedText: func(string) bool { calls = append(calls, "ax_selected"); return true },
			setValue:        func(s string) bool { calls = append(calls, "ax_value:"+s); return true },
			selectAll:       func(context.Context) error { calls = append(calls, "select_all"); return nil },
			inject:          func(context.Context, string, int, int) error { calls = append(calls, "inject"); return nil },
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !slices.Equal(calls, []string{"ax_value:rewritten"}) {
			t.Fatalf("calls = %v, want only ax_value", calls)
		}
	})

	t.Run("whole field selects all before inject when AXValue fails", func(t *testing.T) {
		var calls []string
		err := replaceEditableFieldWithOps(ctx, "rewritten", false, replaceFieldOps{
			setValue:  func(string) bool { calls = append(calls, "ax_value"); return false },
			selectAll: func(context.Context) error { calls = append(calls, "select_all"); return nil },
			inject: func(_ context.Context, text string, del, back int) error {
				calls = append(calls, "inject:"+text)
				if del != 0 || back != 0 {
					t.Fatalf("unexpected del=%d back=%d", del, back)
				}
				return nil
			},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		want := []string{"ax_value", "select_all", "inject:rewritten"}
		if !slices.Equal(calls, want) {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
	})

	t.Run("whole field empty replacement deletes after select all", func(t *testing.T) {
		var delCount int
		err := replaceEditableFieldWithOps(ctx, "", false, replaceFieldOps{
			setValue:  func(string) bool { return false },
			selectAll: func(context.Context) error { return nil },
			inject: func(_ context.Context, text string, del, _ int) error {
				delCount = del
				if text != "" {
					t.Fatalf("unexpected text %q", text)
				}
				return nil
			},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if delCount != 1 {
			t.Fatalf("deleteCount = %d, want 1", delCount)
		}
	})

	t.Run("selectAll error aborts before inject", func(t *testing.T) {
		wantErr := errors.New("no permission")
		injected := false
		err := replaceEditableFieldWithOps(ctx, "x", false, replaceFieldOps{
			setValue:  func(string) bool { return false },
			selectAll: func(context.Context) error { return wantErr },
			inject:    func(context.Context, string, int, int) error { injected = true; return nil },
		})
		if !errors.Is(err, wantErr) || injected {
			t.Fatalf("err=%v injected=%v, want %v and injected=false", err, injected, wantErr)
		}
	})

	t.Run("onlySelection uses AXSelectedText then falls back to inject without selectAll", func(t *testing.T) {
		var calls []string
		err := replaceEditableFieldWithOps(ctx, "piece", true, replaceFieldOps{
			setSelectedText: func(string) bool { calls = append(calls, "ax_selected"); return false },
			setValue:        func(string) bool { calls = append(calls, "ax_value"); return true },
			selectAll:       func(context.Context) error { calls = append(calls, "select_all"); return nil },
			inject: func(_ context.Context, text string, _, _ int) error {
				calls = append(calls, "inject:"+text)
				return nil
			},
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		want := []string{"ax_selected", "inject:piece"}
		if !slices.Equal(calls, want) {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
	})
}

func TestDarwinBridge_LastExternalAppSkipsSelfClavis(t *testing.T) {
	b := newPlatformBridge(Options{}).(*darwinBridge)
	b.setCachedFrontmostApp(AppIdentity{BundleID: "com.apple.Safari", ProcessName: "Safari"})
	b.setCachedFrontmostApp(AppIdentity{BundleID: "com.yeisme.clavis", ProcessName: "Clavis"})
	ext := b.LastExternalApp()
	if ext.BundleID != "com.apple.Safari" || ext.ProcessName != "Safari" {
		t.Fatalf("LastExternalApp = %+v, want com.apple.Safari / Safari", ext)
	}
}
