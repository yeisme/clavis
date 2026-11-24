package bridge

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/yeisme/clavis/pkg/core/host"
)

// Pure-Go Windows bridge helpers kept untagged so they compile and run unit tests
// on macOS, Linux, and Windows alike.

const (
	winVKShift    uint16 = 0x10
	winVKLShift   uint16 = 0xA0
	winVKRShift   uint16 = 0xA1
	winCFBitmap          = 2
	winCFMetafile        = 3
	winCFPalette         = 9
	winCFEnhMeta         = 14
	winCFOwnerDisp       = 0x0080
	winCFDSPText         = 0x0081
	winCFDSPBitmap       = 0x0082
	winCFDSPMeta         = 0x0083
	winCFDSPEnhMeta      = 0x008E
	winCFPrivateLo       = 0x0200
	winCFGDIObjHi        = 0x03FF
)

// isWinShiftVK reports whether vk is any Shift virtual-key code.
func isWinShiftVK(vk uint16) bool {
	return vk == winVKShift || vk == winVKLShift || vk == winVKRShift
}

// winInputEngineEligible evaluates Windows own-IME eligibility. When forced takeover
// is enabled, native system IME state is overridden, while secure-input fields and
// Clavis's own windows are always protected.
func winInputEngineEligible(forced, nativeIME, secure, selfApp bool) bool {
	return inputEngineEligible(!forced && nativeIME, secure, selfApp)
}

// isWinHGlobalClipboardFormat reports whether a Win32 clipboard format ID carries an
// HGLOBAL memory block that can safely be read with GlobalLock/GlobalSize and restored
// via SetClipboardData. GDI handles (CF_BITMAP, CF_PALETTE, CF_ENHMETAFILE, CF_GDIOBJ*),
// metafile wrappers (CF_METAFILEPICT), owner/display formats (CF_OWNERDISPLAY, CF_DSP*),
// and private handle ranges (CF_PRIVATE*) are excluded so snapshot/restore never passes
// a non-HGLOBAL handle to GlobalLock or corrupts clipboard ownership.
func isWinHGlobalClipboardFormat(fmtID uint32) bool {
	if fmtID == 0 {
		return false
	}
	switch fmtID {
	case winCFBitmap, winCFMetafile, winCFPalette, winCFEnhMeta,
		winCFOwnerDisp, winCFDSPText, winCFDSPBitmap, winCFDSPMeta, winCFDSPEnhMeta:
		return false
	}
	if fmtID >= winCFPrivateLo && fmtID <= winCFGDIObjHi {
		return false
	}
	return true
}

// winShiftTracker tracks whether a physical Shift press was a clean, standalone tap
// eligible to toggle Clavis's own-IME ASCII mode. Any non-Shift modifier (Ctrl/Alt/Win,
// preventing Alt+Shift / Ctrl+Shift Windows input-language switches from falsely flipping
// Clavis ASCII mode), intervening non-modifier key, simultaneous second Shift key, or
// double-tap reset clears the pending tap so stale shiftAlone state can never linger.
type winShiftTracker struct {
	mu      sync.Mutex
	down    bool
	downVK  uint16
	armed   bool
	tainted bool
}

// OnShiftDown records a Shift key-down event.
func (s *winShiftTracker) OnShiftDown(vk uint16, otherModDown bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if otherModDown {
		s.armed = false
		s.tainted = true
		return
	}
	if s.down {
		if s.downVK != vk {
			s.armed = false
			s.tainted = true
		}
		// Auto-repeat of the same held Shift key preserves current armed/tainted state.
		return
	}
	s.down = true
	s.downVK = vk
	s.armed = true
	s.tainted = false
}

// OnShiftUp finishes a Shift press and reports whether it was a clean standalone tap.
func (s *winShiftTracker) OnShiftUp(vk uint16, otherModDown bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sameKey := s.downVK == vk || vk == winVKShift || s.downVK == winVKShift
	toggle := s.down && s.armed && !s.tainted && !otherModDown && sameKey
	s.down = false
	s.downVK = 0
	s.armed = false
	s.tainted = false
	return toggle
}

// OnNonShiftModifier clears any pending Shift tap when Ctrl, Alt, Win, or CapsLock changes.
func (s *winShiftTracker) OnNonShiftModifier() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed = false
	if s.down {
		s.tainted = true
	}
}

// OnInterveningKey clears any pending Shift tap when a non-modifier key is pressed.
func (s *winShiftTracker) OnInterveningKey() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed = false
	if s.down {
		s.tainted = true
	}
}

// Reset drops all in-flight Shift state (used on double-tap capture, focus change, or reset).
func (s *winShiftTracker) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = false
	s.downVK = 0
	s.armed = false
	s.tainted = false
}

// Armed reports whether a clean standalone Shift tap is currently in flight.
func (s *winShiftTracker) Armed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.down && s.armed && !s.tainted
}

// validateWinOpenAppTarget validates an OpenApp target before passing it to ShellExecuteW
// or direct executable lookup. It rejects control characters and shell/redirection
// metacharacters to eliminate any shell-injection surface.
func validateWinOpenAppTarget(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("windows: empty app target")
	}
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("windows: app target contains control characters")
		}
	}
	// Reject characters that are never valid in Windows paths or unencoded URIs.
	if strings.ContainsAny(trimmed, "\"|<>`") {
		return "", errors.New("windows: app target contains disallowed characters")
	}
	if !isWinOpenAppURI(trimmed) && strings.ContainsAny(trimmed, "&;^") {
		return "", errors.New("windows: app target contains shell metacharacters")
	}
	return trimmed, nil
}

func isWinOpenAppURI(s string) bool {
	lower := strings.ToLower(s)
	for _, prefix := range []string{"http://", "https://", "mailto:", "ms-settings:", "shell:"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// winOpenAppExecCandidates returns safe direct-executable lookup candidates for a
// validated non-URI target (never invoking cmd.exe or batch scripts).
func winOpenAppExecCandidates(target string) []string {
	if target == "" || isWinOpenAppURI(target) {
		return nil
	}
	lower := strings.ToLower(target)
	if strings.HasSuffix(lower, ".bat") || strings.HasSuffix(lower, ".cmd") ||
		strings.HasSuffix(lower, ".ps1") || strings.HasSuffix(lower, ".vbs") {
		return nil
	}
	if strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".com") {
		return []string{target}
	}
	return []string{target + ".exe", target}
}

// isWinDirectExecutablePath reports whether a resolved filesystem path is a safe direct
// binary executable (excluding shell scripts that would spawn cmd.exe/powershell).
func isWinDirectExecutablePath(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))
	return strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".com")
}

// winConsoleProcesses are Windows terminals/consoles where sending Ctrl+C with no active
// selection sends SIGINT and interrupts the foreground process.
var winConsoleProcesses = map[string]struct{}{
	"conhost":         {},
	"openconsole":     {},
	"windowsterminal": {},
	"wt":              {},
	"cmd":             {},
	"powershell":      {},
	"pwsh":            {},
	"mintty":          {},
	"alacritty":       {},
	"wezterm":         {},
	"wezterm-gui":     {},
	"conemu":          {},
	"conemu64":        {},
	"cmder":           {},
	"tabby":           {},
	"putty":           {},
	"wsl":             {},
	"bash":            {},
	"git-bash":        {},
}

// isWinCopyProbeUnsafe reports whether sending synthetic Ctrl+C / Ctrl+A to the
// foreground window is unsafe (secure input, Clavis itself, unknown foreground process,
// or a console/terminal window where Ctrl+C can interrupt a running command).
func isWinCopyProbeUnsafe(processName, className string, secure, selfApp bool) bool {
	if secure || selfApp {
		return true
	}
	norm := normalizeProcessName(processName)
	if norm == "" {
		return true
	}
	if _, console := winConsoleProcesses[norm]; console {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(className)) {
	case "consolewindowclass", "cascadia_hosting_window_class", "mintty", "virtualconsoleclass":
		return true
	}
	return false
}

// winCaptureFieldOps injects OS interactions for CaptureEditableField so the workflow
// can be exhaustively unit-tested without Win32 hooks.
type winCaptureFieldOps struct {
	isSecure       func() bool
	isUnsafeTarget func() bool
	probeSelection func(ctx context.Context, budget time.Duration) (string, error)
	selectAll      func(ctx context.Context) error
}

const (
	winSelectionProbeBudget = 90 * time.Millisecond
	winWholeFieldBudget     = 120 * time.Millisecond
)

func captureWinEditableFieldWithOps(ctx context.Context, ops winCaptureFieldOps) (host.EditableFieldSnapshot, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return host.EditableFieldSnapshot{}, err
		}
	}
	if ops.isSecure != nil && ops.isSecure() {
		return host.EditableFieldSnapshot{}, errors.New("windows: secure input active")
	}
	if ops.isUnsafeTarget != nil && ops.isUnsafeTarget() {
		return host.EditableFieldSnapshot{}, errors.New("windows: foreground window does not support field capture")
	}
	if ops.probeSelection == nil {
		return host.EditableFieldSnapshot{}, errors.New("windows: selection probe unavailable")
	}
	// First check whether the user already has a selection. Avoid Ctrl+A when a
	// selection is present so ReplaceEditableField(..., onlySelection=true) keeps
	// the exact highlighted span intact.
	selected, err := ops.probeSelection(ctx, winSelectionProbeBudget)
	if err != nil {
		return host.EditableFieldSnapshot{}, err
	}
	if selected != "" {
		return host.EditableFieldSnapshot{
			Value:        selected,
			SelectedText: selected,
		}, nil
	}
	if ops.selectAll == nil {
		return host.EditableFieldSnapshot{}, nil
	}
	if err := ops.selectAll(ctx); err != nil {
		return host.EditableFieldSnapshot{}, err
	}
	val, err := ops.probeSelection(ctx, winWholeFieldBudget)
	if err != nil {
		return host.EditableFieldSnapshot{}, err
	}
	return host.EditableFieldSnapshot{
		Value:        val,
		SelectedText: "",
	}, nil
}

// winReplaceFieldOps injects OS interactions for ReplaceEditableField so the workflow
// can be unit-tested on every GOOS.
type winReplaceFieldOps struct {
	isSecure       func() bool
	isUnsafeTarget func() bool
	selectAll      func(ctx context.Context) error
	pasteReplace   func(ctx context.Context, replacement string) error
	deleteSelected func(ctx context.Context) error
}

func replaceWinEditableFieldWithOps(ctx context.Context, replacement string, onlySelection bool, ops winReplaceFieldOps) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if ops.isSecure != nil && ops.isSecure() {
		return errors.New("windows: secure input active")
	}
	if ops.isUnsafeTarget != nil && ops.isUnsafeTarget() {
		return errors.New("windows: foreground window does not support field replacement")
	}
	if !onlySelection && ops.selectAll != nil {
		if err := ops.selectAll(ctx); err != nil {
			return err
		}
	}
	if replacement == "" {
		if ops.deleteSelected == nil {
			return errors.New("windows: delete handler unavailable")
		}
		return ops.deleteSelected(ctx)
	}
	if ops.pasteReplace == nil {
		return errors.New("windows: paste handler unavailable")
	}
	return ops.pasteReplace(ctx, replacement)
}
