//go:build windows

package bridge

import (
	"context"
	"fmt"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"os/exec"
	"strings"
)

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
func (b *windowsBridge) callInline(ev host.InlineKeyEvent) bool {
	b.inlineMu.RLock()
	fn := b.inlineHandler
	b.inlineMu.RUnlock()
	return fn != nil && fn(ev)
}
func (b *windowsBridge) FastCaretAnchor(ctx context.Context) (host.CursorAnchor, error) {
	return host.CursorAnchor{}, unsupported(ctx)
}
func (b *windowsBridge) CaptureEditableField(ctx context.Context) (host.EditableFieldSnapshot, error) {
	return host.EditableFieldSnapshot{}, unsupported(ctx)
}
func (b *windowsBridge) ReplaceEditableField(ctx context.Context, replacement string, onlySelection bool) error {
	return unsupported(ctx)
}
func (b *windowsBridge) UpdateDaemonShortcut(string)                        {}
func (b *windowsBridge) UpdateSelectionBarConfig(config.SelectionBarConfig) {}

var _ = strings.TrimSpace
