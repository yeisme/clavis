//go:build darwin

package bridge

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

func listUnixPorts(ctx context.Context) ([]host.ListeningPort, error) {
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN").Output()
	if err != nil {
		return nil, err
	}
	return ParseLsofListeningPorts(string(out)), nil
}
func killUnix(ctx context.Context, pid int, force bool) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	return syscall.Kill(pid, sig)
}
func (b *darwinBridge) ListListeningPorts(ctx context.Context) ([]host.ListeningPort, error) {
	return listUnixPorts(ctx)
}
func (b *darwinBridge) KillProcess(ctx context.Context, pid int, force bool) error {
	return killUnix(ctx, pid, force)
}
func (b *darwinBridge) CommitText(ctx context.Context, text string, d, c int) error {
	return b.InjectText(ctx, text, d, c)
}
func (b *darwinBridge) SetInlineIMEHandler(fn func(host.InlineKeyEvent) bool) {
	b.inlineMu.Lock()
	b.inlineHandler = fn
	b.inlineMu.Unlock()
}
func (b *darwinBridge) SetInlineIMEEnabled(v bool) { b.inlineEnabled.Store(v) }
func (b *darwinBridge) callInline(ev host.InlineKeyEvent) bool {
	b.inlineMu.RLock()
	fn := b.inlineHandler
	b.inlineMu.RUnlock()
	return fn != nil && fn(ev)
}
func (b *darwinBridge) isIMKInputSource() bool {
	cur, e := b.CurrentInputSource(context.Background())
	return e == nil && cur.ID == "com.yeisme.clavis.inputmethod"
}
func (b *darwinBridge) FastCaretAnchor(ctx context.Context) (host.CursorAnchor, error) {
	if err := ctx.Err(); err != nil {
		return host.CursorAnchor{}, err
	}
	if err := initDarwin(); err != nil {
		return host.CursorAnchor{}, err
	}
	el := axUIElementCreateSystemWide()
	if el == 0 {
		return host.CursorAnchor{}, fmt.Errorf("no system element")
	}
	defer cfRelease(el)
	var focused uintptr
	if axUIElementCopyAttributeValue(el, axAttr("AXFocusedUIElement"), &focused) != 0 {
		return host.CursorAnchor{}, fmt.Errorf("no focused element")
	}
	defer cfRelease(focused)
	if a, ok := resolveAXCaretAnchor(focused); ok {
		return a, nil
	}
	if a, ok := resolveMouseAnchor(); ok {
		return a, nil
	}
	return host.CursorAnchor{}, fmt.Errorf("no caret anchor")
}
func (b *darwinBridge) CaptureEditableField(ctx context.Context) (host.EditableFieldSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return host.EditableFieldSnapshot{}, err
	}
	if err := initDarwin(); err != nil {
		return host.EditableFieldSnapshot{}, err
	}
	el := axUIElementCreateSystemWide()
	if el == 0 {
		return host.EditableFieldSnapshot{}, fmt.Errorf("no system element")
	}
	defer cfRelease(el)
	var focused uintptr
	if axUIElementCopyAttributeValue(el, axAttr("AXFocusedUIElement"), &focused) != 0 {
		return host.EditableFieldSnapshot{}, fmt.Errorf("no focused element")
	}
	defer cfRelease(focused)
	return host.EditableFieldSnapshot{Value: copyAXStringAttr(focused, "AXValue", 0), SelectedText: copyAXStringAttr(focused, "AXSelectedText", 0)}, nil
}
func (b *darwinBridge) ReplaceEditableField(ctx context.Context, replacement string, onlySelection bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if onlySelection && trySetAXSelectedText(replacement) {
		return nil
	}
	return b.InjectText(ctx, replacement, 0, 0)
}
func (b *darwinBridge) UpdateDaemonShortcut(string)                        {}
func (b *darwinBridge) UpdateSelectionBarConfig(config.SelectionBarConfig) {}
