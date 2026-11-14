//go:build windows

package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/yeisme/clavis/pkg/core/host"
	"golang.org/x/sys/windows"
)

// Hook robustness for Windows (docs/design/hook-robustness.md): WH_MOUSE_LL click resets,
// a GetLastInputInfo watchdog that reinstalls a silently removed WH_KEYBOARD_LL, a cached
// secure-field (password box) flag and a cached IME conversion flag for the hook callback.

const (
	whMouseLL          = 14
	wmClavisReinstall  = 0x8000 + 0x43 // WM_APP + 'C'
	emGetPasswordChar  = 0x00D2
	gwlStyle           = ^uintptr(15) // GWL_STYLE = -16
	secureFieldMaxAge  = time.Second
	nativeFlagMaxAge   = 500 * time.Millisecond
	passwordCharWaitMs = 50
)

var (
	procGetLastInputInfo  = user32.NewProc("GetLastInputInfo")
	procGetClassNameW     = user32.NewProc("GetClassNameW")
	procGetWindowLongPtrW = user32.NewProc("GetWindowLongPtrW")
	procGetWindowLongW    = user32.NewProc("GetWindowLongW")
)

type msllHookStruct struct {
	Pt          winPOINT
	MouseData   uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type lastInputInfo struct {
	CbSize uint32
	DwTime uint32
}

type hookHealthState struct {
	lastHookTick  atomic.Uint32
	mouseHandle   atomic.Uintptr
	mouseCallback uintptr
	mouseOK       atomic.Bool
	reinstalls    atomic.Int64
	errMu         sync.Mutex
	lastErr       string
	secure        cachedFlag
	native        cachedFlag
	resetCh       chan struct{}
	reinstallRes  chan bool
}

func (h *hookHealthState) setErr(err error) {
	h.errMu.Lock()
	if err == nil {
		h.lastErr = ""
	} else {
		h.lastErr = err.Error()
	}
	h.errMu.Unlock()
}

func (h *hookHealthState) errString() string {
	h.errMu.Lock()
	defer h.errMu.Unlock()
	return h.lastErr
}

// installMouseHook installs WH_MOUSE_LL on the calling (hook) thread. Failure is non-fatal.
func (b *windowsBridge) installMouseHook() {
	b.mu.Lock()
	if b.health.mouseCallback == 0 {
		b.health.mouseCallback = windows.NewCallback(func(nCode int32, wParam uintptr, lParam uintptr) uintptr {
			return b.handleMouseProc(nCode, wParam, lParam)
		})
	}
	cb := b.health.mouseCallback
	b.mu.Unlock()
	h, _, err := procSetWindowsHookExW.Call(whMouseLL, cb, 0, 0)
	if h == 0 {
		b.health.mouseOK.Store(false)
		b.health.setErr(fmt.Errorf("WH_MOUSE_LL: %w", err))
		return
	}
	b.health.mouseHandle.Store(h)
	b.health.mouseOK.Store(true)
}

func (b *windowsBridge) uninstallMouseHook() {
	if h := b.health.mouseHandle.Swap(0); h != 0 {
		procUnhookWindowsHookEx.Call(h)
	}
	b.health.mouseOK.Store(false)
}

// handleMouseProc never blocks: an atomic store plus a non-blocking signal on button presses.
func (b *windowsBridge) handleMouseProc(nCode int32, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 && lParam != 0 {
		info := (*msllHookStruct)(nativePtr(lParam))
		b.health.lastHookTick.Store(info.Time)
		if isMouseButtonDown(wParam) {
			if b.triggers != nil {
				b.triggers.autoSelectMouseDown()
			}
			select {
			case b.health.resetCh <- struct{}{}:
			default:
			}
		} else if isMouseButtonUp(wParam) && b.triggers != nil && b.triggers.autoSelectMouseUp() {
			if enabled, mode := b.triggers.selectionMode(); enabled && mode == "auto_select" {
				// UI Automation/selection reads are never performed in this callback.
				go b.emitWindowsSelectionTrigger()
			}
		}
	}
	r, _, _ := procCallNextHookEx.Call(b.health.mouseHandle.Load(), uintptr(nCode), wParam, lParam)
	return r
}

// reinstallHooksOnThread runs on the hook thread (LL hooks belong to the installing thread).
func (b *windowsBridge) reinstallHooksOnThread() bool {
	b.mu.Lock()
	old := b.hookHandle
	cb := b.hookCallback
	b.mu.Unlock()
	if old != 0 {
		procUnhookWindowsHookEx.Call(old)
	}
	b.uninstallMouseHook()
	hhk, _, err := procSetWindowsHookExW.Call(whKeyboardLL, cb, 0, 0)
	b.mu.Lock()
	b.hookHandle = hhk
	b.mu.Unlock()
	if hhk == 0 {
		b.health.setErr(fmt.Errorf("reinstall WH_KEYBOARD_LL: %w", err))
		return false
	}
	b.health.setErr(nil)
	b.installMouseHook()
	return true
}

// runHookWatchdog compares GetLastInputInfo with the last event our hooks saw.
func (b *windowsBridge) runHookWatchdog(ctx context.Context) {
	ticker := time.NewTicker(hookWatchdogInterval)
	defer ticker.Stop()
	var wd hookWatchdog
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			lii := lastInputInfo{CbSize: uint32(unsafe.Sizeof(lastInputInfo{}))}
			if r, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&lii))); r == 0 {
				continue
			}
			wd.mouseHook = b.health.mouseOK.Load()
			if !wd.Observe(lii.DwTime, b.health.lastHookTick.Load(), now) {
				continue
			}
			b.mu.Lock()
			tid := b.hookThreadID
			b.mu.Unlock()
			ok := false
			if tid != 0 {
				if r, _, err := procPostThreadMessageW.Call(uintptr(tid), wmClavisReinstall, 0, 0); r == 0 {
					b.health.setErr(fmt.Errorf("post reinstall: %w", err))
				} else {
					select {
					case ok = <-b.health.reinstallRes:
					case <-time.After(500 * time.Millisecond):
						b.health.setErr(errors.New("hook thread did not answer reinstall"))
					case <-ctx.Done():
						return
					}
				}
			}
			b.health.reinstalls.Add(1)
			// Treat the reinstall itself as fresh hook activity so we don't loop immediately.
			b.health.lastHookTick.Store(lii.DwTime)
			wd.Reinstalled(ok, now)
		}
	}
}

// runMouseResetWorker applies click resets off the hook thread, mirroring darwin's mouse-down
// handling: double-tap/leader detectors and the snippet buffer.
func (b *windowsBridge) runMouseResetWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.health.resetCh:
			b.router.leaderDetector.Reset()
			b.router.doubleTapDetector.Reset()
			if b.inputEngineReset != nil {
				b.inputEngineReset()
			}
			b.emitTriggerEvents([]host.TriggerEvent{{
				Kind:        host.TriggerSnippetReset,
				Key:         "mouse",
				TimestampMs: time.Now().UnixMilli(),
			}})
		}
	}
}

func winGetWindowStyle(hwnd uintptr) uint64 {
	if err := procGetWindowLongPtrW.Find(); err == nil {
		r, _, _ := procGetWindowLongPtrW.Call(hwnd, gwlStyle)
		return uint64(r)
	}
	if err := procGetWindowLongW.Find(); err != nil {
		return 0
	}
	r, _, _ := procGetWindowLongW.Call(hwnd, gwlStyle)
	return uint64(uint32(r))
}

func winClassName(hwnd uintptr) string {
	if err := procGetClassNameW.Find(); err != nil {
		return ""
	}
	buf := make([]uint16, 128)
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// winFocusedSecureSignals collects password-field signals for the foreground thread's focus.
// Runs on the IME observer thread, never inside the hook callback.
func winFocusedSecureSignals(fg uintptr, processName string) secureFieldSignals {
	sig := secureFieldSignals{ProcessName: processName}
	if fg == 0 {
		return sig
	}
	tid, _, _ := procGetWindowThreadProcessId.Call(fg, 0)
	gti := guiThreadInfo{CbSize: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	if r, _, _ := procGetGUIThreadInfo.Call(tid, uintptr(unsafe.Pointer(&gti))); r == 0 || gti.HwndFocus == 0 {
		return sig
	}
	focus := gti.HwndFocus
	sig.ClassName = winClassName(focus)
	sig.Style = winGetWindowStyle(focus)
	if strings.EqualFold(sig.ClassName, "Edit") {
		var ch uintptr
		if r, _, _ := procSendMessageTimeoutW.Call(focus, emGetPasswordChar, 0, 0,
			smtoAbortIfHung, passwordCharWaitMs, uintptr(unsafe.Pointer(&ch))); r != 0 {
			sig.PasswordChar = uint32(ch)
		}
	}
	return sig
}

// hookSecureInput reads the cached secure-field flag (stale → not secure).
func (b *windowsBridge) hookSecureInput(now time.Time) bool {
	v, fresh := b.health.secure.Load(now, secureFieldMaxAge)
	return fresh && v
}

// hookIMEComposing reads the cached native-conversion flag; only a stale cache falls back to
// the synchronous (≤2×100ms) IME query.
func (b *windowsBridge) hookIMEComposing(now time.Time) bool {
	if v, fresh := b.health.native.Load(now, nativeFlagMaxAge); fresh {
		return v
	}
	return winIsNativeConversionMode()
}

func (b *windowsBridge) hookHealthDetails() map[string]string {
	d := map[string]string{
		"hookReinstalls": fmt.Sprintf("%d", b.health.reinstalls.Load()),
		"mouseHook":      fmt.Sprintf("%v", b.health.mouseOK.Load()),
		"secureInput":    fmt.Sprintf("%v", b.hookSecureInput(time.Now())),
	}
	if e := b.health.errString(); e != "" {
		d["hookError"] = e
	}
	return d
}
