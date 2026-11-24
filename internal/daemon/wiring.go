package daemon

import (
	"context"
	"errors"
	"maps"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
)

const (
	sqliteCompactionTimeout   = 5 * time.Second
	DefaultBridgeHealCooldown = 15 * time.Second
	DefaultBridgeHealTimeout  = 5 * time.Second
)

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
	// Heal, when non-nil, is invoked with a bounded timeout whenever IsRunning
	// reports false, rate-limited by HealCooldown (defaults to DefaultBridgeHealCooldown).
	Heal         func(context.Context) error
	HealCooldown time.Duration
	HealTimeout  time.Duration
	// Now optionally overrides time.Now for deterministic cooldown testing.
	Now func() time.Time
}

// NewBridgeWatchdogProbeWithHeal constructs a bridge watchdog probe that actively
// invokes healFn (bounded by DefaultBridgeHealTimeout and throttled by cooldown)
// whenever IsRunning reports false.
func NewBridgeWatchdogProbeWithHeal(
	in BridgeLivenessInput,
	healFn func(context.Context) error,
	cooldown time.Duration,
) WatchdogProbe {
	in.Heal = healFn
	if cooldown > 0 {
		in.HealCooldown = cooldown
	}
	return NewBridgeWatchdogProbe(in)
}

// NewBridgeWatchdogProbe returns a WatchdogProbe that checks bridge running state
// and TapRecoveryCount progression. When the bridge is not running (triggering an
// optional bounded Heal callback if configured and not in cooldown) or a new
// hook/tap recovery occurred since the previous probe, it returns false so the
// supervisor records trouble and increments SelfHealCount upon subsequent steady recovery.
func NewBridgeWatchdogProbe(in BridgeLivenessInput) WatchdogProbe {
	var mu sync.Mutex
	var lastRecovery int64
	var lastHealAttempt time.Time
	var healing bool
	if in.TapRecoveryCount != nil {
		lastRecovery = in.TapRecoveryCount()
	}
	cooldown := in.HealCooldown
	if cooldown <= 0 {
		cooldown = DefaultBridgeHealCooldown
	}
	healTimeout := in.HealTimeout
	if healTimeout <= 0 {
		healTimeout = DefaultBridgeHealTimeout
	}
	nowFn := in.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return func(ctx context.Context) (bool, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if in.IsRunning != nil && !in.IsRunning() {
			if in.Heal == nil {
				return false, nil
			}
			now := nowFn()
			mu.Lock()
			canHeal := !healing && (lastHealAttempt.IsZero() || now.Sub(lastHealAttempt) >= cooldown)
			if canHeal {
				healing = true
				lastHealAttempt = now
			}
			mu.Unlock()
			if !canHeal {
				return false, nil
			}
			healCtx, cancel := context.WithTimeout(ctx, healTimeout)
			healErr := in.Heal(healCtx)
			cancel()
			mu.Lock()
			healing = false
			if in.TapRecoveryCount != nil {
				if cur := in.TapRecoveryCount(); cur >= 0 {
					lastRecovery = cur
				}
			}
			mu.Unlock()
			return false, healErr
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
