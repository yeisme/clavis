//go:build windows

package bridge

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

const winSelectionLateWatch = 500 * time.Millisecond

func (b *windowsBridge) ListListeningPorts(ctx context.Context) ([]host.ListeningPort, error) {
	out, e := exec.CommandContext(ctx, "netstat", "-ano").Output()
	if e != nil {
		return nil, e
	}
	tasks, _ := exec.CommandContext(ctx, "tasklist").Output()
	return ParseNetstatListeningPorts(string(out), string(tasks)), nil
}

func (b *windowsBridge) KillProcess(ctx context.Context, pid int, force bool) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	args := []string{"/PID", fmt.Sprint(pid)}
	if force {
		args = append(args, "/F")
	}
	return exec.CommandContext(ctx, "taskkill", args...).Run()
}

func (b *windowsBridge) CommitText(ctx context.Context, text string, d, c int) error {
	return b.InjectText(ctx, text, d, c)
}

func (b *windowsBridge) SetInlineIMEHandler(fn func(host.InlineKeyEvent) bool) {
	b.inlineMu.Lock()
	b.inlineHandler = fn
	b.inlineMu.Unlock()
}

func (b *windowsBridge) SetInlineIMEEnabled(v bool) { b.inlineEnabled.Store(v) }

// SetInlineIMEForced makes the own input engine take over Chinese typing even while
// Windows' native IME is in native conversion mode.
func (b *windowsBridge) SetInlineIMEForced(v bool) { b.inlineForced.Store(v) }

// InlineIMEForced reports whether forced own-IME takeover is enabled.
func (b *windowsBridge) InlineIMEForced() bool { return b.inlineForced.Load() }

func (b *windowsBridge) callInline(ev host.InlineKeyEvent) bool {
	b.inlineMu.RLock()
	fn := b.inlineHandler
	b.inlineMu.RUnlock()
	return fn != nil && fn(ev)
}

// FastCaretAnchor returns the foreground Win32 caret anchor when available, falling back
// to the pointer anchor (matching darwinBridge.FastCaretAnchor).
func (b *windowsBridge) FastCaretAnchor(ctx context.Context) (host.CursorAnchor, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return host.CursorAnchor{}, err
		}
	}
	hwnd, _, _ := procGetForegroundWindow.Call()
	if a, ok := resolveWinCaretAnchor(hwnd); ok {
		return a, nil
	}
	if a, ok := resolveWinMouseAnchor(); ok {
		return a, nil
	}
	return host.CursorAnchor{}, errors.New("windows: no caret anchor")
}

// ProbeSelection implements SelectionProber on Windows using the shared CopyProbe
// (clipboard snapshot -> tagged Ctrl+C -> CF_UNICODETEXT decode -> clipboard restore).
// Console/terminal windows, password/secure fields, and Clavis's own windows are skipped.
func (b *windowsBridge) ProbeSelection(ctx context.Context, budget time.Duration) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if budget <= 0 {
		budget = winSelectionProbeBudget
	}
	app, fg := winForegroundApp()
	if fg == 0 {
		return "", nil
	}
	className := winClassName(fg)
	if isWinCopyProbeUnsafe(app.ProcessName, className, b.currentSecureInput(time.Now()), isSelfClavisApp(app)) {
		return "", nil
	}
	p := CopyProbe{
		Backend:   &windowsPasteboardBackend{bridge: b},
		SendCopy:  func() { sendWinCombo(vkControl, 'C') },
		Ignore:    b.IgnoreSelfPasteboardChange,
		LateWatch: winSelectionLateWatch,
	}
	return p.Probe(ctx, budget)
}

func (b *windowsBridge) selectAllFocusedField(ctx context.Context) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	sendWinCombo(vkControl, 'A')
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(25 * time.Millisecond):
		return nil
	}
}

func (b *windowsBridge) replaceByPaste(ctx context.Context, replacement string) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	snap, snapErr := b.pbRestorer.Capture()
	writtenSeq, err := b.writeClipboardUnicodeText(replacement)
	if err != nil {
		return err
	}
	sendWinCombo(vkControl, 'V')
	select {
	case <-ctx.Done():
		if snapErr == nil {
			_, _ = b.pbRestorer.RestoreIfUnmodified(snap, writtenSeq)
		}
		return ctx.Err()
	case <-time.After(45 * time.Millisecond):
	}
	if snapErr == nil {
		_, _ = b.pbRestorer.RestoreIfUnmodified(snap, writtenSeq)
	}
	return nil
}

func (b *windowsBridge) isForegroundFieldUnsafe() bool {
	app, fg := winForegroundApp()
	if fg == 0 {
		return true
	}
	return isWinCopyProbeUnsafe(app.ProcessName, winClassName(fg), false, isSelfClavisApp(app))
}

// CaptureEditableField reads the focused field's selection (or full text via Ctrl+A/Ctrl+C
// when nothing is selected) while preserving the clipboard and honoring Secure Input.
func (b *windowsBridge) CaptureEditableField(ctx context.Context) (host.EditableFieldSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return captureWinEditableFieldWithOps(ctx, winCaptureFieldOps{
		isSecure:       func() bool { return b.currentSecureInput(time.Now()) },
		isUnsafeTarget: b.isForegroundFieldUnsafe,
		probeSelection: b.ProbeSelection,
		selectAll:      b.selectAllFocusedField,
	})
}

// ReplaceEditableField replaces either the active selection or the entire focused field
// via tagged SendInput (Ctrl+A / Ctrl+V) with clipboard snapshot/restore and Secure Input guard.
func (b *windowsBridge) ReplaceEditableField(ctx context.Context, replacement string, onlySelection bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return replaceWinEditableFieldWithOps(ctx, replacement, onlySelection, winReplaceFieldOps{
		isSecure: func() bool { return b.currentSecureInput(time.Now()) },
		isUnsafeTarget: func() bool {
			app, fg := winForegroundApp()
			if fg == 0 || isSelfClavisApp(app) {
				return true
			}
			if !onlySelection {
				return isWinCopyProbeUnsafe(app.ProcessName, winClassName(fg), false, false)
			}
			return false
		},
		selectAll:    b.selectAllFocusedField,
		pasteReplace: b.replaceByPaste,
		deleteSelected: func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			sendWinKeyTap(vkBack)
			return nil
		},
	})
}

func (b *windowsBridge) UpdateDaemonShortcut(accel string) {
	if b.triggers != nil {
		b.triggers.setShortcut(accel)
	}
}

func (b *windowsBridge) UpdateSelectionBarConfig(cfg config.SelectionBarConfig) {
	if b.triggers != nil {
		b.triggers.setSelection(cfg)
	}
}

var (
	_ ExtendedHostService = (*windowsBridge)(nil)
	_ InlineIMEForcer     = (*windowsBridge)(nil)
	_ SelectionProber     = (*windowsBridge)(nil)
)
