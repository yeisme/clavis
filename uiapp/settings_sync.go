package uiapp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/host"
	coresync "github.com/yeisme/clavis/pkg/core/sync"
)

// SyncStatusReader is the UI's read-only sync status seam. The host may wire
// it to Client.StatusSnapshot; the settings page never performs sync writes.
type SyncStatusReader func(context.Context) (coresync.StatusSnapshot, host.OperationResult, error)

var syncStatusReaderState struct {
	sync.RWMutex
	reader SyncStatusReader
}

// SetSyncStatusReader installs the local-only reader used by the settings
// card. Passing nil restores the normal unconfigured display.
func SetSyncStatusReader(reader SyncStatusReader) {
	syncStatusReaderState.Lock()
	syncStatusReaderState.reader = reader
	syncStatusReaderState.Unlock()
}

// SetSyncStatusClient wires a sync client's local-only snapshot reader into
// Settings. It does not start sync, pair a device, or perform network I/O.
func SetSyncStatusClient(client *coresync.Client) {
	if client == nil {
		SetSyncStatusReader(nil)
		return
	}
	SetSyncStatusReader(client.StatusSnapshot)
}

func readSyncStatus(ctx context.Context) (coresync.StatusSnapshot, host.OperationResult, error) {
	syncStatusReaderState.RLock()
	reader := syncStatusReaderState.reader
	syncStatusReaderState.RUnlock()
	if reader == nil {
		return coresync.StatusSnapshot{}, host.OperationResult{State: host.StateReady}, nil
	}
	return reader(ctx)
}

func (a *AppController) renderSyncStatusCard(c *ui.Context, theme *ui.Theme) {
	snapshot, result, err := readSyncStatus(context.Background())
	settingsSourceCard(c, theme, "同步", "只读", func() {
		switch {
		case result.State == host.StateUnknown:
			settingsHint(c, theme, "同步本地状态暂不可用，当前未执行任何同步操作。")
		case !snapshot.LoggedIn:
			settingsHint(c, theme, "未配置同步")
		default:
			settingsKeyValueRow(c, theme, "登录状态", "已登录")
			settingsKeyValueRow(c, theme, "账号", snapshot.Account)
			settingsKeyValueRow(c, theme, "设备 ID", snapshot.DeviceID)
			settingsKeyValueRow(c, theme, "本地版本", fmt.Sprintf("%d", snapshot.LocalVersion))
			settingsKeyValueRow(c, theme, "待上传", fmt.Sprintf("%d", snapshot.PendingUploads))
			settingsKeyValueRow(c, theme, "待处理删除", fmt.Sprintf("%d", snapshot.PendingTombstones))
			settingsKeyValueRow(c, theme, "上次同步", formatSyncRelativeTime(snapshot.LastSyncAt, time.Now()))
			settingsKeyValueRow(c, theme, "已知设备", fmt.Sprintf("%d", snapshot.Devices))
		}
		if err != nil && result.State != host.StateUnknown {
			// Keep diagnostics neutral and avoid rendering raw implementation errors.
			settingsHint(c, theme, "同步状态读取暂不可用。")
		}
	})
}

func formatSyncRelativeTime(at, now time.Time) string {
	if at.IsZero() {
		return "从未同步"
	}
	if now.Before(at) {
		return "刚刚"
	}
	d := now.Sub(at)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d/time.Hour))
	default:
		return fmt.Sprintf("%d 天前", int(d/(24*time.Hour)))
	}
}
