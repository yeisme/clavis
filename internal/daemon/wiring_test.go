package daemon

import (
	"context"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/config"
)

func TestResidentSupervisorStartStopRestartAndNilSafety(t *testing.T) {
	var nilSup *ResidentSupervisor
	nilSup.Start(context.Background())
	nilSup.Stop()
	nilSup.UpdateConfig(config.DefaultDaemonConfig())
	nilSup.RegisterCompactionHook(func(context.Context) error { return nil })
	nilSup.RegisterWatchdogProbe(func(context.Context) (bool, error) { return true, nil })
	nilSup.RecordInteraction()
	nilSup.RecordKeystroke(1000, 1)
	if ss := nilSup.Snapshot(); ss.TotalKeystrokes != 0 {
		t.Fatalf("nil snapshot = %+v", ss)
	}

	// Stop on unstarted supervisor is safe.
	cfg := config.DefaultDaemonConfig()
	cfg.WatchdogIntervalMs = 15
	cfg.IdleCompactSeconds = 1
	s := NewResidentSupervisor(cfg)
	s.Stop()

	baseGoroutines := runtime.NumGoroutine()

	for cycle := 0; cycle < 3; cycle++ {
		s.Start(context.Background())
		// Duplicate Start must be a no-op.
		s.Start(context.Background())

		newCfg := cfg
		newCfg.WatchdogIntervalMs = 10 + cycle*5
		newCfg.MaxMemoryMB = 48 + cycle
		s.UpdateConfig(newCfg)

		time.Sleep(35 * time.Millisecond)
		s.Stop()
		// Duplicate Stop must be a no-op.
		s.Stop()
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseGoroutines+1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > baseGoroutines+2 {
		t.Fatalf("goroutine leak after Start/Stop cycles: before=%d after=%d", baseGoroutines, got)
	}
}

func TestSQLiteCompactionHook(t *testing.T) {
	if err := NewSQLiteCompactionHook(nil)(context.Background()); err != nil {
		t.Fatalf("nil db hook returned error: %v", err)
	}

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "daemon-test.db")
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatalf("sqlite.Open failed: %v", err)
	}
	defer db.Close()

	if _, err := db.Execute(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		t.Fatalf("enable WAL failed: %v", err)
	}
	if _, err := db.Execute(ctx, `CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create table failed: %v", err)
	}
	if _, err := db.Execute(ctx, `INSERT INTO kv(k, v) VALUES('a', 'b')`); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	hook := NewSQLiteCompactionHook(db)
	if err := hook(ctx); err != nil {
		t.Fatalf("NewSQLiteCompactionHook failed: %v", err)
	}
}

func TestCacheTrimHook(t *testing.T) {
	var calls atomic.Int32
	hook := NewCacheTrimHook(nil, func() { calls.Add(1) }, func() { calls.Add(2) })
	if err := hook(context.Background()); err != nil {
		t.Fatalf("hook failed: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestBridgeWatchdogProbeAndSelfHeal(t *testing.T) {
	var running atomic.Bool
	var recCount atomic.Int64

	probe := NewBridgeWatchdogProbe(BridgeLivenessInput{
		IsRunning:        running.Load,
		TapRecoveryCount: recCount.Load,
	})

	// Bridge not running -> unhealthy (false).
	ok, err := probe(context.Background())
	if err != nil || ok {
		t.Fatalf("expected (false, nil) when stopped, got (%v, %v)", ok, err)
	}

	// Bridge starts -> healthy (true).
	running.Store(true)
	ok, err = probe(context.Background())
	if err != nil || !ok {
		t.Fatalf("expected (true, nil) when running, got (%v, %v)", ok, err)
	}

	// Tap recovery count increases -> reports trouble (false) on that beat.
	recCount.Store(1)
	ok, err = probe(context.Background())
	if err != nil || ok {
		t.Fatalf("expected (false, nil) when tap recovery incremented, got (%v, %v)", ok, err)
	}

	// Next beat steady -> healthy again (true).
	ok, err = probe(context.Background())
	if err != nil || !ok {
		t.Fatalf("expected (true, nil) after recovery stabilized, got (%v, %v)", ok, err)
	}

	// Verify inside ResidentSupervisor that SelfHealCount increments on recovery.
	running.Store(false)
	cfg := config.DefaultDaemonConfig()
	cfg.WatchdogIntervalMs = 10
	cfg.IdleCompactSeconds = 0
	cfg.MaxMemoryMB = 0
	sup := NewResidentSupervisor(cfg)
	sup.RegisterWatchdogProbe(NewBridgeWatchdogProbe(BridgeLivenessInput{
		IsRunning:        running.Load,
		TapRecoveryCount: recCount.Load,
	}))
	sup.Start(context.Background())
	defer sup.Stop()

	time.Sleep(35 * time.Millisecond)
	if heals := sup.Snapshot().SelfHealCount; heals != 0 {
		t.Fatalf("expected 0 heals while bridge down, got %d", heals)
	}

	running.Store(true)
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) && sup.Snapshot().SelfHealCount < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if heals := sup.Snapshot().SelfHealCount; heals != 1 {
		t.Fatalf("expected SelfHealCount=1 after bridge came up, got %d", heals)
	}

	recCount.Add(1)
	deadline = time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) && sup.Snapshot().SelfHealCount < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if heals := sup.Snapshot().SelfHealCount; heals < 2 {
		t.Fatalf("expected SelfHealCount>=2 after tap recovery stabilized, got %d", heals)
	}
}

func TestFrecencySyncerOnlyDispatchesOnChange(t *testing.T) {
	current := map[string]int{}
	var dispatches []map[string]int

	syncer := NewFrecencySyncer(
		func(time.Time) map[string]int {
			out := make(map[string]int, len(current))
			for k, v := range current {
				out[k] = v
			}
			return out
		},
		func(scores map[string]int) {
			dispatches = append(dispatches, scores)
			// Mutate delivered map to ensure syncer state isn't aliased.
			scores["mutated"] = 999
		},
	)

	// Empty snapshot matches initial state -> no dispatch.
	if syncer.Sync(time.Now()) {
		t.Fatal("expected no dispatch for initial empty snapshot")
	}
	if len(dispatches) != 0 {
		t.Fatalf("dispatches = %d, want 0", len(dispatches))
	}

	// Non-empty change -> dispatches.
	current["clipboard:c1"] = 3
	if !syncer.Sync(time.Now()) {
		t.Fatal("expected dispatch when scores changed")
	}
	if len(dispatches) != 1 || dispatches[0]["clipboard:c1"] != 3 {
		t.Fatalf("unexpected dispatches: %+v", dispatches)
	}

	// Same scores again -> no dispatch.
	if syncer.Sync(time.Now()) {
		t.Fatal("expected no dispatch when scores are unchanged")
	}
	if len(dispatches) != 1 {
		t.Fatalf("dispatches = %d, want 1", len(dispatches))
	}

	// Updated score -> dispatches again.
	current["clipboard:c1"] = 5
	if !syncer.Sync(time.Now()) {
		t.Fatal("expected dispatch when score increased")
	}
	if len(dispatches) != 2 || dispatches[1]["clipboard:c1"] != 5 {
		t.Fatalf("unexpected dispatches: %+v", dispatches)
	}
}
