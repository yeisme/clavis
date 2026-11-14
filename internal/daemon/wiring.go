package daemon

import (
	"context"
	"errors"
	"maps"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
)

const sqliteCompactionTimeout = 5 * time.Second

// NewSQLiteCompactionHook returns a CompactionHook that runs a passive WAL
// checkpoint and PRAGMA optimize against db. If db is nil, it returns a no-op hook.
func NewSQLiteCompactionHook(db *sqlite.DB) CompactionHook {
	if db == nil {
		return func(context.Context) error { return nil }
	}
	return func(ctx context.Context) error {
		if ctx == nil {
			ctx = context.Background()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		cctx, cancel := context.WithTimeout(ctx, sqliteCompactionTimeout)
		defer cancel()

		_, walErr := db.Query(cctx, `PRAGMA wal_checkpoint(PASSIVE)`)
		_, optErr := db.Execute(cctx, `PRAGMA optimize`)
		return errors.Join(walErr, optErr)
	}
}

// NewCacheTrimHook returns a CompactionHook that invokes each non-nil invalidator.
func NewCacheTrimHook(invalidators ...func()) CompactionHook {
	fns := make([]func(), 0, len(invalidators))
	for _, fn := range invalidators {
		if fn != nil {
			fns = append(fns, fn)
		}
	}
	return func(ctx context.Context) error {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		for _, fn := range fns {
			fn()
		}
		return nil
	}
}

// BridgeLivenessInput supplies bridge health signals to NewBridgeWatchdogProbe
// without coupling internal/daemon to platform bridge implementations.
type BridgeLivenessInput struct {
	IsRunning        func() bool
	TapRecoveryCount func() int64
}

// NewBridgeWatchdogProbe returns a WatchdogProbe that checks bridge running state
// and TapRecoveryCount progression. When the bridge is not running or a new hook/tap
// recovery occurred since the previous probe, it returns false so the supervisor
// records trouble and increments SelfHealCount upon subsequent steady recovery.
func NewBridgeWatchdogProbe(in BridgeLivenessInput) WatchdogProbe {
	var mu sync.Mutex
	var lastRecovery int64
	if in.TapRecoveryCount != nil {
		lastRecovery = in.TapRecoveryCount()
	}
	return func(ctx context.Context) (bool, error) {
		if ctx != nil && ctx.Err() != nil {
			return false, ctx.Err()
		}
		if in.IsRunning != nil && !in.IsRunning() {
			return false, nil
		}
		if in.TapRecoveryCount != nil {
			cur := in.TapRecoveryCount()
			if cur < 0 {
				return false, nil
			}
			mu.Lock()
			trouble := cur > lastRecovery
			if cur != lastRecovery {
				lastRecovery = cur
			}
			mu.Unlock()
			if trouble {
				return false, nil
			}
		}
		return true, nil
	}
}

// FrecencySyncer computes frecency snapshots off the UI thread and dispatches
// updates only when the snapshot actually changed.
type FrecencySyncer struct {
	mu       sync.Mutex
	last     map[string]int
	snapshot func(time.Time) map[string]int
	dispatch func(scores map[string]int)
}

// NewFrecencySyncer constructs a FrecencySyncer. Both callbacks may be nil (no-op).
func NewFrecencySyncer(snapshot func(time.Time) map[string]int, dispatch func(scores map[string]int)) *FrecencySyncer {
	return &FrecencySyncer{
		snapshot: snapshot,
		dispatch: dispatch,
	}
}

// Sync recomputes the frecency snapshot at now and dispatches it if it changed
// since the last dispatched snapshot. Returns true when a dispatch occurred.
func (s *FrecencySyncer) Sync(now time.Time) bool {
	if s == nil || s.snapshot == nil {
		return false
	}
	s.mu.Lock()
	scores := s.snapshot(now)
	if maps.Equal(s.last, scores) {
		s.mu.Unlock()
		return false
	}
	s.last = maps.Clone(scores)
	dispatch := s.dispatch
	s.mu.Unlock()

	if dispatch == nil {
		return false
	}
	// Send a fresh copy so the receiver cannot alias s.last.
	dispatch(maps.Clone(scores))
	return true
}
