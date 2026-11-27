package uiapp

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	coresync "github.com/yeisme/clavis/pkg/core/sync"
)

func makeTinyTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode tiny png: %v", err)
	}
	return buf.Bytes()
}

// TestControllerLifecycleCloseStopsSensitiveAndVaultTimers verifies under -race
// that Close() cleanly disarms pending sensitive clipboard TTL timers and Vault
// timers without firing callbacks or mutating the clipboard after shutdown.
func TestControllerLifecycleCloseStopsSensitiveAndVaultTimers(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	var clearCalls atomic.Int32
	ctrl.ClipboardWriter = func(string) {}
	ctrl.SetSensitiveClipboardClearHook(func() error {
		clearCalls.Add(1)
		return nil
	})

	_, err := ctrl.ExecuteClipboardAction(ClipboardCopyRequest{
		Origin:      ClipboardCopyOriginUser,
		Action:      config.ClipboardActionCopySensitive,
		Item:        clipboard.ClipboardItem{ID: "sec-1", Content: "sk-test-secret-123456"},
		Sensitive:   true,
		SecretLabel: "Test Secret",
		TTL:         80 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("ExecuteClipboardAction sensitive: %v", err)
	}
	if !ctrl.SensitiveClipboardState().Active {
		t.Fatal("expected sensitive clipboard countdown active before Close()")
	}

	ctrl.Close()

	// Wait past the original TTL; timer must have been stopped by Close().
	time.Sleep(150 * time.Millisecond)
	if got := clearCalls.Load(); got != 0 {
		t.Fatalf("sensitive TTL clear hook fired %d time(s) after Close()", got)
	}
	if ctrl.SensitiveClipboardState().Active {
		t.Fatal("sensitive clipboard state remained active after Close()")
	}
}

// TestControllerLifecycleCloseReleasesSubStatesAndBitmaps verifies that Close()
// clears annotate bitmaps, OCR image buffers, hub surface, media caches, and
// capsule UI caches, and is idempotent when called multiple times or on zero-value controllers.
func TestControllerLifecycleCloseReleasesSubStatesAndBitmaps(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)

	pngData := makeTinyTestPNG(t)
	if err := ctrl.OpenAnnotateImage(pngData); err != nil {
		t.Fatalf("OpenAnnotateImage: %v", err)
	}
	if !ctrl.AnnotateOpen() {
		t.Fatal("expected annotate session open")
	}
	if err := ctrl.StartImageOCR(pngData, "image/png", "extract"); err != nil {
		t.Fatalf("StartImageOCR: %v", err)
	}
	ctrl.SetHubSurface(HubSurfaceManage)
	capsuleUI.setPreviewVisible(ctrl, true)

	// Arm a pending HUD timer and original-image debounce timer.
	ctrl.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: "temporary"})
	ctrl.scheduleOriginalBitmap("/nonexistent/test.png")

	ctrl.Close()
	// Idempotent second Close() must never panic or block.
	ctrl.Close()

	if ctrl.AnnotateOpen() {
		t.Fatal("annotate session remained open after Close()")
	}
	if ctrl.ActiveHubSurface() != HubSurfaceNone {
		t.Fatalf("hub surface = %q after Close(), want none", ctrl.ActiveHubSurface())
	}
	if imgs := ctrl.consumeOCRImages(); len(imgs) != 0 {
		t.Fatalf("OCR images not released after Close(): %d", len(imgs))
	}

	// Zero-value controller Close() must also be completely safe and idempotent.
	zero := &AppController{}
	zero.SetHubSurface(HubSurfaceStats)
	zero.Close()
	zero.Close()
	if zero.lifeCtx().Err() == nil {
		t.Fatal("expected zero-value controller lifeCtx to be canceled after Close()")
	}
}

type blockingSyncFocuser struct {
	started chan struct{}
	ctxDone chan error
}

func (b *blockingSyncFocuser) Name() string { return "blocking-focuser" }
func (b *blockingSyncFocuser) Available(ctx context.Context) (bool, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	select {
	case b.ctxDone <- ctx.Err():
	default:
	}
	return false, ctx.Err()
}
func (b *blockingSyncFocuser) Focus(context.Context, string) (host.OperationResult, error) {
	return host.OperationResult{State: host.StateReady}, nil
}

// TestControllerLifecycleCloseCancelsBackgroundGoroutines verifies under -race
// that async goroutines in management_view, settings_sync, and agent_sessions
// receive context cancellation immediately when AppController.Close() runs.
func TestControllerLifecycleCloseCancelsBackgroundGoroutines(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)

	// 1. Sync reader blocked on context cancellation (used by management_view & settings_sync).
	syncStarted := make(chan struct{}, 2)
	syncCanceled := make(chan error, 2)
	SetSyncStatusReader(func(ctx context.Context) (coresync.StatusSnapshot, host.OperationResult, error) {
		select {
		case syncStarted <- struct{}{}:
		default:
		}
		<-ctx.Done()
		select {
		case syncCanceled <- ctx.Err():
		default:
		}
		return coresync.StatusSnapshot{}, host.OperationResult{State: host.StateUnknown}, ctx.Err()
	})
	defer SetSyncStatusReader(nil)

	// 2. Agent focuser blocked on context cancellation (agent_sessions.go).
	focuser := &blockingSyncFocuser{
		started: make(chan struct{}, 1),
		ctxDone: make(chan error, 1),
	}
	restoreFocuser := setAgentFocuserForTest(focuser)
	defer restoreFocuser()

	// Trigger manageSync background goroutine and focusAgentSession goroutine.
	ctrl.ensureManageSyncRefresh()
	ctrl.focusAgentSession(agentsession.Session{ID: "sess-cancel", Provider: "codex"})

	// Wait until both background goroutines have entered their blocking I/O.
	for _, ch := range []chan struct{}{syncStarted, focuser.started} {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for background goroutine to start")
		}
	}

	// Closing controller must immediately cancel lifeCtx() and unblock both goroutines.
	ctrl.Close()

	select {
	case err := <-syncCanceled:
		if err == nil {
			t.Fatal("expected non-nil cancellation error for manageSync goroutine")
		}
	case <-time.After(time.Second):
		t.Fatal("manageSync goroutine did not cancel on Close()")
	}

	select {
	case err := <-focuser.ctxDone:
		if err == nil {
			t.Fatal("expected non-nil cancellation error for focusAgentSession goroutine")
		}
	case <-time.After(time.Second):
		t.Fatal("focusAgentSession goroutine did not cancel on Close()")
	}
}

// TestControllerLifecycleConcurrentSubStateAccessAndClose hammers sub-state
// accessors concurrently with Close() to prove race-freedom under -race.
func TestControllerLifecycleConcurrentSubStateAccessAndClose(t *testing.T) {
	ctrl := &AppController{}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for range 50 {
				ctrl.SetHubSurface(HubSurfaceStats)
				_ = ctrl.ActiveHubSurface()
				capsuleUI.setPreviewVisible(ctrl, id%2 == 0)
				_ = capsuleUI.previewFor(ctrl, false)
				ctrl.SetMediaSearchQuery("q")
				_ = ctrl.MediaSearchQuery()
				setAgentOperationResult(ctrl, host.OperationResult{State: host.StateReady})
				_ = ctrl.agentOperationResult()
				_ = ctrl.lifeCtx()
			}
		}(i)
	}
	time.Sleep(5 * time.Millisecond)
	ctrl.Close()
	wg.Wait()

	// Calling settings_sync render after Close() must not spawn leaked fetches.
	SetSyncStatusReader(func(ctx context.Context) (coresync.StatusSnapshot, host.OperationResult, error) {
		return coresync.StatusSnapshot{LoggedIn: true, Account: "after-close"}, host.OperationResult{State: host.StateReady}, nil
	})
	defer SetSyncStatusReader(nil)
	tt := ui.NewTester(func(c *ui.Context) { ctrl.renderSyncStatusCard(c, ui.LightTheme()) }, 420, 220)
	tt.Frame()
}
