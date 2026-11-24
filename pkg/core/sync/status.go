package sync

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/host"
)

// StatusSnapshot is a read-only view of local sync state for display. It never
// performs network I/O: everything comes from the client and local state store.
type StatusSnapshot struct {
	Account           string
	DeviceID          string
	LoggedIn          bool
	RecoveryReady     bool
	LocalVersion      int64
	PendingUploads    int
	PendingTombstones int
	Conflicts         int
	LastSyncAt        time.Time
	Devices           int
	AllowedClasses    []DataClass
	SyncDir           string
}

// LocalStatus is the optional, non-secret status portion of a StateStore. The
// base StateStore remains compatible with existing durable adapters.
type LocalStatus struct {
	LocalVersion      int64
	PendingUploads    int
	PendingTombstones int
	Conflicts         int
	LastSyncAt        time.Time
	Devices           int
	AllowedClasses    []DataClass
	SyncDir           string
}

// StatusStore exposes additional locally persisted counters for the read-only
// status page. Implementations must not perform network I/O.
type StatusStore interface {
	StateStore
	LoadStatus(ctx context.Context) (LocalStatus, error)
}

func redactIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) > 8 {
		runes = runes[:8]
	}
	return string(runes) + "…"
}

// RedactIdentifier truncates an account or device identifier for non-secret
// status display.
func RedactIdentifier(value string) string {
	return redactIdentifier(value)
}

// StatusSnapshot reads only local state. An unconfigured client is a normal
// ready state; a configured client whose local state cannot be read is
// unknown, not failed. No server method is called by this function.
func (c *Client) StatusSnapshot(ctx context.Context) (StatusSnapshot, host.OperationResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	c.mu.Lock()
	account, device, state := c.accountID, c.deviceID, c.stateStore
	loggedIn := strings.TrimSpace(account) != ""
	plan := c.plan
	conflictCount := len(c.conflicts)
	recoveryReady := c.recoveryReady
	c.mu.Unlock()

	snapshot := StatusSnapshot{
		Account:        redactIdentifier(account),
		DeviceID:       redactIdentifier(device),
		LoggedIn:       loggedIn,
		RecoveryReady:  recoveryReady,
		Conflicts:      conflictCount,
		AllowedClasses: plan.AllowedClasses(),
	}
	result := host.OperationResult{State: host.StateReady, ObservedAt: time.Now().UTC()}

	if !loggedIn {
		return snapshot, result, nil
	}
	if state == nil {
		result.State = host.StateUnknown
		result.Code = "state_unavailable"
		result.Message = "同步本地状态暂不可用"
		return snapshot, result, errors.New("sync: local state store is unavailable")
	}

	watermark, storedReady, err := state.Load(ctx)
	if err != nil {
		result.State = host.StateUnknown
		result.Code = "state_read_failed"
		result.Message = "同步本地状态暂不可用"
		return snapshot, result, err
	}
	if watermark > math.MaxInt64 {
		result.State = host.StateUnknown
		result.Code = "state_version_invalid"
		result.Message = "同步本地状态暂不可用"
		return snapshot, result, errors.New("sync: local version exceeds display range")
	}
	snapshot.LocalVersion = int64(watermark)
	snapshot.RecoveryReady = storedReady

	if statusStore, ok := state.(StatusStore); ok {
		status, err := statusStore.LoadStatus(ctx)
		if err != nil {
			result.State = host.StateUnknown
			result.Code = "state_status_read_failed"
			result.Message = "同步本地状态暂不可用"
			return snapshot, result, err
		}
		// LocalVersion is always the watermark returned by StateStore.Load;
		// optional status data cannot replace that source of truth.
		snapshot.PendingUploads = maxNonNegative(status.PendingUploads)
		snapshot.PendingTombstones = maxNonNegative(status.PendingTombstones)
		if status.Conflicts > snapshot.Conflicts {
			snapshot.Conflicts = status.Conflicts
		}
		snapshot.LastSyncAt = status.LastSyncAt
		snapshot.Devices = maxNonNegative(status.Devices)
		if len(status.AllowedClasses) > 0 {
			snapshot.AllowedClasses = append([]DataClass(nil), status.AllowedClasses...)
		}
		snapshot.SyncDir = status.SyncDir
	}
	return snapshot, result, nil
}

func maxNonNegative(value int) int {
	if value < 0 {
		return 0
	}
	return value
}
