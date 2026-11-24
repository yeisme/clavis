package uiapp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
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

func (a *AppController) syncStatusCache() *syncStatusCacheEntry {
	return a.syncStatusCacheStateFor()
}

// SetSyncStatusReader installs the local-only reader used by Settings and
// Management Hub. Passing nil restores the normal unconfigured display.
func (a *AppController) SetSyncStatusReader(reader SyncStatusReader) {
	SetSyncStatusReader(reader)
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
	lifeCtx := a.lifeCtx()
	go func() {
		ctx, cancel := context.WithTimeout(lifeCtx, 2*time.Second)
		defer cancel()
		snap, res, err := reader(ctx)

		entry.mu.Lock()
		_, latestGen := currentSyncStatusReader()
		if lifeCtx.Err() == nil && latestGen == gen {
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

		if lifeCtx.Err() != nil || a.isClosed() {
			return
		}
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
	if a.isClosed() {
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
	if a.isClosed() {
		return
	}
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

func formatSyncAllowlist(classes []coresync.DataClass) string {
	if len(classes) == 0 {
		classes = coresync.Phase1SyncPlan().AllowedClasses()
	}
	parts := make([]string, 0, len(classes))
	for _, c := range classes {
		if c == coresync.Passwords || c == coresync.KeyManager {
			continue
		}
		if s := strings.TrimSpace(string(c)); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "input_method, learning_data"
	}
	return strings.Join(parts, ", ")
}

func (a *AppController) renderSyncStatusCard(c *ui.Context, theme *ui.Theme) {
	zh := i18n.IsChinese()
	svc := c.Services()
	snapshot, result, err, loading := a.cachedSyncStatus(svc)
	reader, _ := currentSyncStatusReader()
	allowlistLabel := pickLang(zh, "同步范围", "Allowlist")
	allowlistSummary := formatSyncAllowlist(snapshot.AllowedClasses)
	settingsSourceCard(c, theme, pickLang(zh, "同步", "Sync"), pickLang(zh, "只读", "Read-only"), func() {
		switch {
		case result.State == host.StateRefreshing && !snapshot.LoggedIn:
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				kitStatusDot(c, kitStateLoadingColor(theme), true)
				kitStatusPill(c, theme, pickLang(zh, "正在读取同步状态…", "Reading sync status…"), chipAccent)
			})
		case result.State == host.StateUnknown:
			kitWarningBanner(c, theme, pickLang(zh,
				"同步本地状态暂不可用，当前未执行任何同步操作。",
				"Local sync status is temporarily unavailable; no sync actions were performed.",
			))
		case !snapshot.LoggedIn:
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				kitStatusDot(c, kitStateEmptyColor(theme), false)
				kitStatusPill(c, theme, pickLang(zh, "未配置同步", "Sync not configured"), chipNeutral)
			})
			settingsHint(c, theme, pickLang(zh,
				"当前未登录同步账号，配置与数据仅保存在本地。",
				"Not signed in to a sync account. Settings and data stay local.",
			))
			settingsKeyValueRow(c, theme, allowlistLabel, allowlistSummary)
		default:
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, pickLang(zh, "登录状态", "Sign-in Status")).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
				ui.Spacer(c)
				kitStatusPill(c, theme, pickLang(zh, "已登录", "Signed in"), chipSuccess)
			})
			settingsKeyValueRow(c, theme, pickLang(zh, "账号", "Account"), snapshot.Account)
			settingsKeyValueRow(c, theme, pickLang(zh, "设备 ID", "Device ID"), snapshot.DeviceID)
			settingsKeyValueRow(c, theme, pickLang(zh, "本地版本", "Local Version"), fmt.Sprintf("%d", snapshot.LocalVersion))
			settingsKeyValueRow(c, theme, pickLang(zh, "待上传", "Pending Uploads"), fmt.Sprintf("%d", snapshot.PendingUploads))
			settingsKeyValueRow(c, theme, pickLang(zh, "待处理删除", "Pending Tombstones"), fmt.Sprintf("%d", snapshot.PendingTombstones))
			settingsKeyValueRow(c, theme, pickLang(zh, "冲突", "Conflicts"), fmt.Sprintf("%d", snapshot.Conflicts))
			settingsKeyValueRow(c, theme, allowlistLabel, allowlistSummary)
			settingsKeyValueRow(c, theme, pickLang(zh, "上次同步", "Last Sync"), formatSyncRelativeTime(snapshot.LastSyncAt, time.Now()))
			settingsKeyValueRow(c, theme, pickLang(zh, "已知设备", "Known Devices"), fmt.Sprintf("%d", snapshot.Devices))
		}
		if err != nil && result.State != host.StateUnknown {
			// Keep diagnostics neutral and avoid rendering raw implementation errors.
			kitWarningBanner(c, theme, pickLang(zh, "同步状态读取暂不可用。", "Sync status is temporarily unavailable."))
		}
		if reader != nil {
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				ui.Spacer(c)
				label := i18n.T("settings.diag.refresh")
				if loading {
					label = i18n.T("stats.status.refreshing")
				}
				if ui.Button(c, label).Key("sync-status-refresh").Disabled(loading).Padding(space2, space8).Shrink(0).Clicked() {
					a.forceRefreshSyncStatus(svc)
				}
			})
		}
	})
}

func formatSyncRelativeTime(at, now time.Time) string {
	zh := i18n.IsChinese()
	if at.IsZero() {
		return pickLang(zh, "从未同步", "Never synced")
	}
	if now.Before(at) || now.Sub(at) < time.Minute {
		return pickLang(zh, "刚刚", "Just now")
	}
	d := now.Sub(at)
	switch {
	case d < time.Hour:
		mins := int(d / time.Minute)
		return pickLang(zh, fmt.Sprintf("%d 分钟前", mins), fmt.Sprintf("%dm ago", mins))
	case d < 24*time.Hour:
		hours := int(d / time.Hour)
		return pickLang(zh, fmt.Sprintf("%d 小时前", hours), fmt.Sprintf("%dh ago", hours))
	default:
		days := int(d / (24 * time.Hour))
		return pickLang(zh, fmt.Sprintf("%d 天前", days), fmt.Sprintf("%dd ago", days))
	}
}
