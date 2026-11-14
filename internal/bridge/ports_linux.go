//go:build linux

package bridge

import (
	"context"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

func (b *linuxBridge) ListListeningPorts(ctx context.Context) ([]host.ListeningPort, error) {
	return listUnixPorts(ctx)
}
func (b *linuxBridge) KillProcess(ctx context.Context, pid int, force bool) error {
	return killUnix(ctx, pid, force)
}
func (b *linuxBridge) CommitText(ctx context.Context, text string, d, c int) error {
	return b.InjectText(ctx, text, d, c)
}
func (b *linuxBridge) SetInlineIMEHandler(func(host.InlineKeyEvent) bool) {}
func (b *linuxBridge) SetInlineIMEEnabled(bool)                           {}
func (b *linuxBridge) FastCaretAnchor(ctx context.Context) (host.CursorAnchor, error) {
	return host.CursorAnchor{}, unsupported(ctx)
}
func (b *linuxBridge) CaptureEditableField(ctx context.Context) (host.EditableFieldSnapshot, error) {
	return host.EditableFieldSnapshot{}, unsupported(ctx)
}
func (b *linuxBridge) ReplaceEditableField(ctx context.Context, replacement string, onlySelection bool) error {
	return unsupported(ctx)
}
func (b *linuxBridge) UpdateDaemonShortcut(string)                        {}
func (b *linuxBridge) UpdateSelectionBarConfig(config.SelectionBarConfig) {}
