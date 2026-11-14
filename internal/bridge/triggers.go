package bridge

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

// triggerRuntime contains only lock-free/configuration state used by OS hooks.
// Hooks call its methods synchronously; expensive context/AX reads are always
// performed by the caller after returning from the callback.
type triggerRuntime struct {
	shortcut  atomic.Pointer[Hotkey]
	mu        sync.RWMutex
	selection config.SelectionBarConfig
	shift     *DoubleTapDetector
	leftDown  atomic.Bool
}

func newTriggerRuntime() *triggerRuntime {
	r := &triggerRuntime{shift: NewDoubleTapDetector("shift", 300)}
	r.selection = config.DefaultSelectionBarConfig()
	return r
}
func (r *triggerRuntime) setShortcut(raw string) {
	h, err := ParseHotkey(raw)
	if err != nil {
		r.shortcut.Store(nil)
		return
	}
	r.shortcut.Store(&h)
}
func (r *triggerRuntime) shortcutMatches(key string, mods []string) bool {
	h := r.shortcut.Load()
	return h != nil && h.Matches(key, mods)
}
func (r *triggerRuntime) setSelection(c config.SelectionBarConfig) {
	r.mu.Lock()
	r.selection = c
	r.mu.Unlock()
	r.shift.Reset()
}
func (r *triggerRuntime) selectionMode() (bool, string) {
	r.mu.RLock()
	c := r.selection
	r.mu.RUnlock()
	mode := strings.ToLower(strings.TrimSpace(c.TriggerMode))
	return c.Enabled && (mode == "double_shift" || mode == "auto_select"), mode
}
func (r *triggerRuntime) doubleShift(mod string, down, other bool, now int64, composing bool) bool {
	enabled, mode := r.selectionMode()
	if !enabled || mode != "double_shift" || composing {
		r.shift.Reset()
		return false
	}
	return r.shift.HandleModifier(mod, down, other, now)
}
func (r *triggerRuntime) autoSelectMouseDown() { r.leftDown.Store(true) }
func (r *triggerRuntime) autoSelectMouseUp() bool {
	return r.leftDown.Swap(false)
}
func selectionEvent(now int64) host.TriggerEvent {
	return host.TriggerEvent{Kind: host.TriggerSelectionQuickBar, TimestampMs: now}
}
func optimizeEvent(now int64) host.TriggerEvent {
	return host.TriggerEvent{Kind: host.TriggerInPlaceOptimize, TimestampMs: now}
}

// resolveTriggerPrecedence is shared policy: an optimize chord is more specific
// than selection, and both outrank the legacy router/keymap paths.
func resolveTriggerPrecedence(optimize, selection bool) host.TriggerKind {
	if optimize {
		return host.TriggerInPlaceOptimize
	}
	if selection {
		return host.TriggerSelectionQuickBar
	}
	return ""
}

// Trigger precedence is intentionally shared by all platforms: the configured
// optimize chord wins before keymaps/IME/router; selection double-shift wins
// only after a complete tap pair and never during composition; router Leader /
// Capsule then runs as before. Synthetic events are filtered by each hook.

// selectionDoubleShiftArmed reports whether a bare-Shift double tap is
// currently routed to the selection bar. Callers that also act on a single
// Shift tap must defer their action while this is true, so one gesture cannot
// trigger both behaviours. The window is the detector's interval.
func (r *triggerRuntime) selectionDoubleShiftArmed() (armed bool, windowMs int64) {
	if r == nil {
		return false, 0
	}
	enabled, mode := r.selectionMode()
	if !enabled || mode != "double_shift" {
		return false, 0
	}
	return true, r.shift.IntervalMs()
}
