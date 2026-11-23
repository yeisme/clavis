//go:build darwin

package bridge

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/ebitengine/purego"
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

// SetInlineIMEForced makes the own engine take over Chinese typing even while a
// non-ASCII system input source (Apple Pinyin, Sogou, Squirrel …) is active.
func (b *darwinBridge) SetInlineIMEForced(v bool) { b.inlineForced.Store(v) }

// InlineIMEForced reports whether forced takeover is on.
func (b *darwinBridge) InlineIMEForced() bool { return b.inlineForced.Load() }
func (b *darwinBridge) callInline(ev host.InlineKeyEvent) bool {
	b.inlineMu.RLock()
	fn := b.inlineHandler
	b.inlineMu.RUnlock()
	return fn != nil && fn(ev)
}
func (b *darwinBridge) observeInlineInputSource(source host.InputSourceInfo) {
	b.inlineSourceAllowed.Store(source.ID != "" && inlineIMEGateFor(source))
}

var (
	inputSourceObservers  sync.Map // observer token -> *darwinBridge
	inputSourceObserverID atomic.Uintptr
	inputSourceCallback   = sync.OnceValue(func() uintptr {
		return purego.NewCallback(func(_, observer, _, _, _ uintptr) {
			if value, ok := inputSourceObservers.Load(observer); ok {
				source, _ := currentDarwinInputSource()
				value.(*darwinBridge).observeInlineInputSource(source)
			}
		})
	})
)

func (b *darwinBridge) startInputSourceObserver(ctx context.Context) error {
	return onMainThread(ctx, func() {
		id := inputSourceObserverID.Add(1)
		inputSourceObservers.Store(id, b)
		b.inputSourceObserver.Store(id)
		const deliverImmediately = 4
		cfNotificationCenterAddObserver(cfNotificationCenterGetDistributedCenter(), id, inputSourceCallback(), kTISNotifySelectedKeyboardInputSourceChanged, 0, deliverImmediately)
		source, _ := currentDarwinInputSource()
		b.observeInlineInputSource(source)
	})
}

func (b *darwinBridge) stopInputSourceObserver() {
	_ = onMainThread(context.Background(), func() {
		if id := b.inputSourceObserver.Swap(0); id != 0 {
			cfNotificationCenterRemoveObserver(cfNotificationCenterGetDistributedCenter(), id, kTISNotifySelectedKeyboardInputSourceChanged, 0)
			inputSourceObservers.Delete(id)
		}
		b.inlineSourceAllowed.Store(false)
	})
}

// inlineIMEGate reads the last main-thread TIS observation without handing off
// the hook thread. Unknown sources stand down unless takeover was forced.
func (b *darwinBridge) inlineIMEGate() bool {
	return b.inlineForced.Load() || b.inlineSourceAllowed.Load()
}

// inlineIMEGateFor is the pure decision behind inlineIMEGate, split out so the rule is
// unit-testable without a real Carbon/TCC environment.
func inlineIMEGateFor(cur host.InputSourceInfo) bool {
	if cur.ID == "com.yeisme.clavis.inputmethod" {
		return false
	}
	// On Windows-style sources one input source covers both scripts and Mode is the live
	// signal; macOS reports script by input source, so IsASCII is authoritative there.
	if cur.Mode != "" {
		return cur.Mode == host.InputModeASCII
	}
	return cur.IsASCII
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
func (b *darwinBridge) UpdateDaemonShortcut(accel string) {
	if b.triggers != nil {
		b.triggers.setShortcut(accel)
	}
}
func (b *darwinBridge) UpdateSelectionBarConfig(cfg config.SelectionBarConfig) {
	if b.triggers != nil {
		b.triggers.setSelection(cfg)
	}
}
