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

const (
	syncStatusCacheTTL        = 5 * time.Second
	syncFirstFramePrimeBudget = 15 * time.Millisecond
)

var syncStatusReaderState struct {
	sync.RWMutex
	reader SyncStatusReader
	gen    uint64
}

type syncStatusCacheEntry struct {
	mu        sync.Mutex
	hasValue  bool
	cachedGen uint64
	snapshot  coresync.StatusSnapshot
	result    host.OperationResult
	err       error
	fetchedAt time.Time
	inFlight  bool
	done      chan struct{}
}

var syncStatusCacheStore = struct {
	mu    sync.Mutex
	byCtl map[*AppController]*syncStatusCacheEntry
}{
	byCtl: make(map[*AppController]*syncStatusCacheEntry),
}

func (a *AppController) syncStatusCache() *syncStatusCacheEntry {
	syncStatusCacheStore.mu.Lock()
	defer syncStatusCacheStore.mu.Unlock()
	entry, ok := syncStatusCacheStore.byCtl[a]
	if !ok {
		entry = &syncStatusCacheEntry{}
		syncStatusCacheStore.byCtl[a] = entry
	}
	return entry
}

// SetSyncStatusReader installs the local-only reader used by the settings
// card. Passing nil restores the normal unconfigured display.
func SetSyncStatusReader(reader SyncStatusReader) {
	syncStatusReaderState.Lock()
	syncStatusReaderState.reader = reader
	syncStatusReaderState.gen++
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

func currentSyncStatusReader() (SyncStatusReader, uint64) {
	syncStatusReaderState.RLock()
	reader := syncStatusReaderState.reader
	gen := syncStatusReaderState.gen
	syncStatusReaderState.RUnlock()
	return reader, gen
}

func readSyncStatus(ctx context.Context) (coresync.StatusSnapshot, host.OperationResult, error) {
	reader, _ := currentSyncStatusReader()
	if reader == nil {
		return coresync.StatusSnapshot{}, host.OperationResult{State: host.StateReady}, nil
	}
	return reader(ctx)
}

func (a *AppController) startSyncStatusRefreshAsync(entry *syncStatusCacheEntry, reader SyncStatusReader, gen uint64, svc ui.Services) chan struct{} {
	done := make(chan struct{})
	entry.inFlight = true
	entry.done = done
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		snap, res, err := reader(ctx)

		entry.mu.Lock()
		_, latestGen := currentSyncStatusReader()
		if latestGen == gen {
			entry.hasValue = true
			entry.cachedGen = gen
			entry.snapshot = snap
			entry.result = res
			entry.err = err
			entry.fetchedAt = time.Now()
		}
		entry.inFlight = false
		if entry.done == done {
			entry.done = nil
		}
		close(done)
		entry.mu.Unlock()

		svc.Invalidate()
		if a != nil {
			a.mu.Lock()
			repaint := a.OnRepaint
			a.mu.Unlock()
			if repaint != nil {
				repaint()
			}
		}
	}()
	return done
}

func (a *AppController) cachedSyncStatus(svc ui.Services) (coresync.StatusSnapshot, host.OperationResult, error, bool) {
	reader, gen := currentSyncStatusReader()
	if reader == nil {
		return coresync.StatusSnapshot{}, host.OperationResult{State: host.StateReady}, nil, false
	}
	entry := a.syncStatusCache()
	entry.mu.Lock()
	if entry.hasValue && entry.cachedGen == gen {
		snap, res, err := entry.snapshot, entry.result, entry.err
		stale := time.Since(entry.fetchedAt) >= syncStatusCacheTTL
		if stale && !entry.inFlight {
			a.startSyncStatusRefreshAsync(entry, reader, gen, svc)
		}
		loading := entry.inFlight
		entry.mu.Unlock()
		return snap, res, err, loading
	}
	// Cold cache for this reader generation: start background fetch.
	// If a refresh is already running, return immediately without waiting.
	if entry.inFlight {
		entry.mu.Unlock()
		return coresync.StatusSnapshot{}, host.OperationResult{State: host.StateRefreshing}, nil, true
	}
	done := a.startSyncStatusRefreshAsync(entry, reader, gen, svc)
	entry.mu.Unlock()

	// Bounded non-janky prime budget: fast local/fake readers finish in microseconds
	// so first-frame callers see populated status immediately, while slow I/O never
	// stalls rendering and updates asynchronously via repaint.
	timer := time.NewTimer(syncFirstFramePrimeBudget)
	select {
	case <-done:
		timer.Stop()
	case <-timer.C:
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.hasValue && entry.cachedGen == gen {
		return entry.snapshot, entry.result, entry.err, entry.inFlight
	}
	return coresync.StatusSnapshot{}, host.OperationResult{State: host.StateRefreshing}, nil, true
}

func (a *AppController) forceRefreshSyncStatus(svc ui.Services) {
	reader, gen := currentSyncStatusReader()
	if reader == nil {
		return
	}
	entry := a.syncStatusCache()
	entry.mu.Lock()
	if !entry.inFlight {
		a.startSyncStatusRefreshAsync(entry, reader, gen, svc)
	}
	entry.mu.Unlock()
}

func (a *AppController) renderSyncStatusCard(c *ui.Context, theme *ui.Theme) {
	svc := c.Services()
	snapshot, result, err, loading := a.cachedSyncStatus(svc)
	reader, _ := currentSyncStatusReader()
	settingsSourceCard(c, theme, "同步", "只读", func() {
		switch {
		case result.State == host.StateRefreshing && !snapshot.LoggedIn:
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				kitStatusDot(c, kitStateLoadingColor(theme), true)
				kitStatusPill(c, theme, "正在读取同步状态…", chipAccent)
			})
		case result.State == host.StateUnknown:
			kitWarningBanner(c, theme, "同步本地状态暂不可用，当前未执行任何同步操作。")
		case !snapshot.LoggedIn:
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				kitStatusDot(c, kitStateEmptyColor(theme), false)
				kitStatusPill(c, theme, "未配置同步", chipNeutral)
			})
			settingsHint(c, theme, "当前未登录同步账号，配置与数据仅保存在本地。")
		default:
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "登录状态").FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
				ui.Spacer(c)
				kitStatusPill(c, theme, "已登录", chipSuccess)
			})
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
			kitWarningBanner(c, theme, "同步状态读取暂不可用。")
		}
		if reader != nil {
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				ui.Spacer(c)
				label := "刷新"
				if loading {
					label = "刷新中…"
				}
				if ui.Button(c, label).Key("sync-status-refresh").Disabled(loading).Padding(space2, space8).Shrink(0).Clicked() {
					a.forceRefreshSyncStatus(svc)
				}
			})
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
