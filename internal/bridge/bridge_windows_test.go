//go:build windows

package bridge

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

func TestWindowsBridgeInterfaces(t *testing.T) {
	svc := newPlatformBridge(Options{})
	if _, ok := svc.(ExtendedHostService); !ok {
		t.Fatal("windowsBridge must satisfy ExtendedHostService")
	}
	if _, ok := svc.(InlineIMEForcer); !ok {
		t.Fatal("windowsBridge must satisfy InlineIMEForcer")
	}
	if _, ok := svc.(SelectionProber); !ok {
		t.Fatal("windowsBridge must satisfy SelectionProber")
	}
	if _, ok := svc.(InputEngineLearningHost); !ok {
		t.Fatal("windowsBridge must satisfy InputEngineLearningHost")
	}
	if _, ok := svc.(InputEngineBlocklistHost); !ok {
		t.Fatal("windowsBridge must satisfy InputEngineBlocklistHost")
	}
	if _, ok := svc.(ClipboardSequencer); !ok {
		t.Fatal("windowsBridge must satisfy ClipboardSequencer")
	}
}

func TestWindowsBridgeInlineIMEForcedAndEligibility(t *testing.T) {
	b := newPlatformBridge(Options{}).(*windowsBridge)
	if b.InlineIMEForced() {
		t.Fatal("InlineIMEForced must default to false")
	}

	now := time.Now()
	b.health.secure.Store(false, now)
	b.health.native.Store(true, now) // native Windows IME active
	app := AppIdentity{ProcessName: "notepad.exe"}

	if b.inputEngineEligibleAt(now, app) {
		t.Fatal("native Windows IME must block own engine when not forced")
	}

	b.SetInlineIMEForced(true)
	if !b.InlineIMEForced() {
		t.Fatal("SetInlineIMEForced(true) must stick")
	}
	if !b.inputEngineEligibleAt(now, app) {
		t.Fatal("forced takeover must override native Windows IME")
	}

	// Secure fields and Clavis's own windows stay protected even under forced takeover.
	b.health.secure.Store(true, now)
	if b.inputEngineEligibleAt(now, app) {
		t.Fatal("secure field must block own engine even when forced")
	}
	b.health.secure.Store(false, now)
	if b.inputEngineEligibleAt(now, AppIdentity{ProcessName: "clavis.exe"}) {
		t.Fatal("self app must block own engine even when forced")
	}

	cfg := config.DefaultInputEngineConfig()
	cfg.Enabled = true
	cfg.Mode = "inline_hook"
	cfg.ForceTakeover = false
	b.ConfigureInputEngine(cfg)
	if b.InlineIMEForced() {
		t.Fatal("ConfigureInputEngine must sync ForceTakeover=false")
	}

	cfg.ForceTakeover = true
	b.ReloadInputEngine(cfg)
	if !b.InlineIMEForced() {
		t.Fatal("ReloadInputEngine must sync ForceTakeover=true")
	}
}

func TestWindowsBridgeHookSecureInputFailsClosed(t *testing.T) {
	b := newPlatformBridge(Options{}).(*windowsBridge)
	now := time.Now()

	// Never observed -> fail closed (secure=true).
	if !b.hookSecureInput(now) {
		t.Fatal("unobserved secure-input state must fail closed")
	}
	// Fresh negative observation -> not secure.
	b.health.secure.Store(false, now)
	if b.hookSecureInput(now) {
		t.Fatal("fresh non-secure observation must return false")
	}
	// Stale observation beyond threshold -> fail closed.
	if !b.hookSecureInput(now.Add(secureFieldMaxAge + time.Millisecond)) {
		t.Fatal("stale secure-input state must fail closed")
	}
	// Fresh positive observation -> secure.
	b.health.secure.Store(true, now)
	if !b.hookSecureInput(now) {
		t.Fatal("fresh secure observation must return true")
	}
}

func TestWindowsBridgeShiftToggleRespectsEligibility(t *testing.T) {
	b := newPlatformBridge(Options{}).(*windowsBridge)
	cfg := config.DefaultInputEngineConfig()
	cfg.Enabled = true
	cfg.Mode = "inline_hook"
	cfg.ShiftToggleASCII = true
	b.ConfigureInputEngine(cfg)
	eng := b.ime.eng.Load()
	if eng == nil {
		t.Fatal("expected engine to be configured")
	}
	initialASCII := eng.ASCII()
	now := time.Now()

	// Ineligible: secure field is active -> Shift tap must never mutate eng.ASCII().
	b.setCachedProcessName("notepad.exe")
	b.health.native.Store(false, now)
	b.health.secure.Store(true, now)
	b.inputEngineHandleKey(vkLShift, 0, true, now)
	b.inputEngineHandleKey(vkLShift, 0, false, now)
	if eng.ASCII() != initialASCII {
		t.Fatal("Shift tap in secure field mutated eng.ASCII()")
	}

	// Ineligible: Clavis self app -> Shift tap must never mutate eng.ASCII().
	b.health.secure.Store(false, now)
	b.setCachedProcessName("clavis.exe")
	b.inputEngineHandleKey(vkLShift, 0, true, now)
	b.inputEngineHandleKey(vkLShift, 0, false, now)
	if eng.ASCII() != initialASCII {
		t.Fatal("Shift tap in Clavis window mutated eng.ASCII()")
	}

	// Ineligible: native Chinese IME active without ForceTakeover -> no toggle.
	b.setCachedProcessName("notepad.exe")
	b.health.native.Store(true, now)
	b.SetInlineIMEForced(false)
	b.inputEngineHandleKey(vkLShift, 0, true, now)
	b.inputEngineHandleKey(vkLShift, 0, false, now)
	if eng.ASCII() != initialASCII {
		t.Fatal("Shift tap while native IME active mutated eng.ASCII()")
	}

	// App-switch / reset drops any armed shift state cleanly.
	b.shiftTracker.OnShiftDown(vkLShift, false)
	b.resetInputEngine()
	if b.shiftTracker.Armed() {
		t.Fatal("resetInputEngine must clear armed shiftTracker")
	}
}

func TestWindowsBridgeParityContextAndSecureGuards(t *testing.T) {
	b := newPlatformBridge(Options{}).(*windowsBridge)
	cctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := b.FastCaretAnchor(cctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("FastCaretAnchor(canceled) = %v, want context.Canceled", err)
	}
	if _, err := b.ProbeSelection(cctx, 50*time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("ProbeSelection(canceled) = %v, want context.Canceled", err)
	}
	if _, err := b.CaptureEditableField(cctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CaptureEditableField(canceled) = %v, want context.Canceled", err)
	}
	if err := b.ReplaceEditableField(cctx, "x", true); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReplaceEditableField(canceled) = %v, want context.Canceled", err)
	}
	if err := b.OpenApp(cctx, "notepad.exe"); !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenApp(canceled) = %v, want context.Canceled", err)
	}

	// Reject shell-injection OpenApp targets before touching ShellExecuteW.
	if err := b.OpenApp(context.Background(), "notepad & calc.exe"); err == nil {
		t.Fatal("expected shell metacharacter target to be rejected")
	}

	// Secure input guard blocks field capture & replacement immediately without SendInput.
	b.health.secure.Store(true, time.Now())
	if _, err := b.CaptureEditableField(context.Background()); err == nil {
		t.Fatal("CaptureEditableField must fail when secure input is active")
	}
	if err := b.ReplaceEditableField(context.Background(), "hello", true); err == nil {
		t.Fatal("ReplaceEditableField must fail when secure input is active")
	}
}

func TestWindowsBridgeStopWaitsForBackgroundWorkers(t *testing.T) {
	b := newPlatformBridge(Options{}).(*windowsBridge)
	// Stopping before Start must be a clean no-op.
	if err := b.Stop(); err != nil {
		t.Fatalf("Stop before Start: %v", err)
	}

	// Simulate active watchdog + mouse-reset workers tracked by bgWG and verify
	// Stop() cancels and waits for all background goroutines to exit cleanly.
	runCtx, cancel := context.WithCancel(context.Background())
	var exited atomic.Int32
	b.mu.Lock()
	b.started = true
	b.stopCancel = cancel
	b.hookDone = make(chan struct{})
	b.imeDone = make(chan struct{})
	b.triggerDone = make(chan struct{})
	close(b.hookDone)
	close(b.imeDone)
	close(b.triggerDone)
	b.mu.Unlock()

	b.bgWG.Add(2)
	go func() {
		defer b.bgWG.Done()
		defer exited.Add(1)
		b.runHookWatchdog(runCtx)
	}()
	go func() {
		defer b.bgWG.Done()
		defer exited.Add(1)
		b.runMouseResetWorker(runCtx)
	}()

	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := exited.Load(); got != 2 {
		t.Fatalf("Stop returned before background workers exited: exited=%d, want 2", got)
	}
}

var _ host.CursorAnchor
