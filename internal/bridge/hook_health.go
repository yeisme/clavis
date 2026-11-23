package bridge

import (
	"strings"
	"sync/atomic"
	"time"
)

// Pure (untagged) hook-health logic so it is unit-testable on every GOOS.
// Design: docs/design/hook-robustness.md §2/§4/§5.

const (
	hookWatchdogInterval    = 2 * time.Second
	hookStaleThresholdMs    = 2000
	hookStaleNoMouseMs      = 30000
	hookReinstallMaxBackoff = 60 * time.Second
)

// hookWatchdog decides when the low-level keyboard hook was probably removed by Windows
// (LowLevelHooksTimeout). GetLastInputInfo reports the last system input (keyboard or mouse);
// if it is well past the last event our hooks saw on two consecutive heartbeats, reinstall.
type hookWatchdog struct {
	mouseHook bool // WH_MOUSE_LL installed; otherwise mouse-only input looks like a dead hook
	suspect   int
	backoff   time.Duration
	nextTry   time.Time
}

// Observe returns true when the hook should be reinstalled now. Ticks are GetTickCount
// milliseconds; uint32 subtraction handles the 49.7-day wraparound.
func (w *hookWatchdog) Observe(lastInputTick, lastHookTick uint32, now time.Time) bool {
	threshold := uint32(hookStaleThresholdMs)
	if !w.mouseHook {
		threshold = hookStaleNoMouseMs
	}
	lag := lastInputTick - lastHookTick
	// lastHook newer than lastInput (lag wraps to a huge value) or within threshold: healthy.
	if lag == 0 || lag > 1<<31 || lag <= threshold {
		w.suspect = 0
		return false
	}
	w.suspect++
	if w.suspect < 2 || now.Before(w.nextTry) {
		return false
	}
	return true
}

// Reinstalled records a reinstall attempt result and updates the exponential backoff.
func (w *hookWatchdog) Reinstalled(ok bool, now time.Time) {
	w.suspect = 0
	if ok {
		w.backoff = 0
		w.nextTry = time.Time{}
		return
	}
	if w.backoff == 0 {
		w.backoff = hookWatchdogInterval
	} else {
		w.backoff = min(w.backoff*2, hookReinstallMaxBackoff)
	}
	w.nextTry = now.Add(w.backoff)
}

// isMouseButtonDown reports whether a WH_MOUSE_LL wParam is a left/right button press.
func isMouseButtonDown(wParam uintptr) bool {
	const wmLButtonDown, wmRButtonDown = 0x0201, 0x0204
	return wParam == wmLButtonDown || wParam == wmRButtonDown
}

func isMouseButtonUp(wParam uintptr) bool {
	const wmLButtonUp, wmRButtonUp = 0x0202, 0x0205
	return wParam == wmLButtonUp || wParam == wmRButtonUp
}

// secureFieldSignals are the cheap Win32 signals gathered for the focused control.
type secureFieldSignals struct {
	ClassName    string
	Style        uint64
	PasswordChar uint32
	ProcessName  string
}

const esPassword = 0x0020

// secureFieldProcesses are foreground processes whose input is always treated as secret.
var secureFieldProcesses = map[string]struct{}{
	"credentialuibroker": {}, "consent": {}, "logonui": {}, "lockapp": {},
	"1password": {}, "keepass": {}, "keepassxc": {}, "bitwarden": {},
}

// isSecureField is conservative: any signal marks the field secret (snippet off, no candidates).
func isSecureField(s secureFieldSignals) bool {
	if _, ok := secureFieldProcesses[normalizeProcessName(s.ProcessName)]; ok {
		return true
	}
	cls := strings.ToLower(s.ClassName)
	isEdit := cls == "edit" || strings.HasPrefix(cls, "richedit") || strings.HasPrefix(cls, "windowsforms10.edit")
	if !isEdit {
		return false
	}
	return s.Style&esPassword != 0 || s.PasswordChar != 0
}

// cachedFlag is a lock-free bool with an observation timestamp, written by the IME observer
// thread and read inside the hook callback (which must never block).
type cachedFlag struct {
	val atomic.Bool
	at  atomic.Int64 // unix nanos; 0 = never observed
}

func (f *cachedFlag) Store(v bool, now time.Time) {
	f.val.Store(v)
	f.at.Store(now.UnixNano())
}

// Load returns the value and whether it is fresh (observed within maxAge).
func (f *cachedFlag) Load(now time.Time, maxAge time.Duration) (bool, bool) {
	at := f.at.Load()
	if at == 0 {
		return false, false
	}
	return f.val.Load(), now.Sub(time.Unix(0, at)) <= maxAge
}

func (f *cachedFlag) activeOrUnknown(now time.Time, maxAge time.Duration) bool {
	active, fresh := f.Load(now, maxAge)
	return !fresh || active
}
