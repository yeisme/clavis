package daemon

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
)

func TestResidentSupervisorTelemetryAndIdleCompaction(t *testing.T) {
	cfg := config.DefaultDaemonConfig()
	cfg.IdleCompactSeconds = 1
	cfg.WatchdogIntervalMs = 10
	cfg.MaxMemoryMB = 0
	s := NewResidentSupervisor(cfg)
	var compact, probe atomic.Int32
	s.RegisterCompactionHook(func(context.Context) error { compact.Add(1); return nil })
	s.RegisterWatchdogProbe(func(context.Context) (bool, error) { probe.Add(1); return true, nil })
	for i := 1; i <= 100; i++ {
		s.RecordKeystroke(int64(i*1000), 5)
	}
	ss := s.Snapshot()
	if ss.TotalKeystrokes != 100 || ss.P99LatencyUs < 90 {
		t.Fatalf("snapshot=%+v", ss)
	}
	if ss.HealthStatus != HealthStatusInitializing || ss.Healthy {
		t.Fatalf("pre-start snapshot health = (%v, %q), want (false, %q)", ss.Healthy, ss.HealthStatus, HealthStatusInitializing)
	}
	s.Start(context.Background())
	defer s.Stop()
	time.Sleep(1100 * time.Millisecond)
	if compact.Load() != 1 {
		t.Fatalf("compact=%d", compact.Load())
	}
	ss = s.Snapshot()
	if probe.Load() == 0 || ss.SelfHealCount == 0 {
		t.Fatal("probe did not recover")
	}
	if !ss.Healthy || ss.HealthStatus != HealthStatusHealthy {
		t.Fatalf("post-recovery snapshot health = (%v, %q), want (true, %q)", ss.Healthy, ss.HealthStatus, HealthStatusHealthy)
	}
}

func TestBridgeWatchdogProbeActiveSelfHealAndCooldown(t *testing.T) {
	var running atomic.Bool
	var recCount atomic.Int64
	var healCalls atomic.Int32
	var shouldFail atomic.Bool
	shouldFail.Store(true)

	var clockMu sync.Mutex
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	advance := func(d time.Duration) {
		clockMu.Lock()
		now = now.Add(d)
		clockMu.Unlock()
	}
	nowFn := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}

	probe := NewBridgeWatchdogProbeWithHeal(
		BridgeLivenessInput{
			IsRunning:        running.Load,
			TapRecoveryCount: recCount.Load,
			Now:              nowFn,
		},
		func(ctx context.Context) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("expected heal context to have a bounded deadline")
			}
			healCalls.Add(1)
			if shouldFail.Load() {
				return errors.New("permission denied")
			}
			recCount.Add(1) // simulate tap counter update during restart
			running.Store(true)
			return nil
		},
		10*time.Second,
	)

	// Beat 1: bridge down -> triggers first heal attempt (fails).
	ok, err := probe(context.Background())
	if ok || err == nil {
		t.Fatalf("beat 1 = (%v, %v), want (false, non-nil err)", ok, err)
	}
	if got := healCalls.Load(); got != 1 {
		t.Fatalf("healCalls after beat 1 = %d, want 1", got)
	}

	// Beat 2: still within 10s cooldown -> heal suppressed.
	advance(4 * time.Second)
	ok, err = probe(context.Background())
	if ok || err != nil {
		t.Fatalf("beat 2 (cooldown) = (%v, %v), want (false, nil)", ok, err)
	}
	if got := healCalls.Load(); got != 1 {
		t.Fatalf("healCalls during cooldown = %d, want 1", got)
	}

	// Beat 3: cooldown elapsed -> second heal attempt succeeds and starts bridge.
	shouldFail.Store(false)
	advance(7 * time.Second)
	ok, err = probe(context.Background())
	if ok || err != nil {
		t.Fatalf("beat 3 (healed) = (%v, %v), want (false, nil)", ok, err)
	}
	if got := healCalls.Load(); got != 2 {
		t.Fatalf("healCalls after beat 3 = %d, want 2", got)
	}

	// Beat 4: bridge steady running -> healthy even though recCount incremented during heal.
	ok, err = probe(context.Background())
	if !ok || err != nil {
		t.Fatalf("beat 4 (steady) = (%v, %v), want (true, nil)", ok, err)
	}

	// Bridge drops again inside cooldown -> suppressed until cooldown passes, then healed again.
	running.Store(false)
	advance(5 * time.Second)
	ok, err = probe(context.Background())
	if ok || err != nil || healCalls.Load() != 2 {
		t.Fatalf("drop within cooldown = (%v, %v) calls=%d, want (false, nil) calls=2", ok, err, healCalls.Load())
	}
	advance(6 * time.Second)
	ok, err = probe(context.Background())
	if ok || err != nil || healCalls.Load() != 3 {
		t.Fatalf("drop after cooldown = (%v, %v) calls=%d, want (false, nil) calls=3", ok, err, healCalls.Load())
	}
	ok, err = probe(context.Background())
	if !ok || err != nil {
		t.Fatalf("steady after second heal = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestResidentSupervisorActiveSelfHealAndHealthSnapshot(t *testing.T) {
	var running atomic.Bool
	running.Store(true)
	var healCalls atomic.Int32
	var healShouldSucceed atomic.Bool
	healShouldSucceed.Store(true)

	cfg := config.DefaultDaemonConfig()
	cfg.WatchdogIntervalMs = 10
	cfg.IdleCompactSeconds = 0
	cfg.MaxMemoryMB = 0

	sup := NewResidentSupervisor(cfg)
	sup.RegisterWatchdogProbe(NewBridgeWatchdogProbeWithHeal(
		BridgeLivenessInput{IsRunning: running.Load},
		func(ctx context.Context) error {
			healCalls.Add(1)
			if !healShouldSucceed.Load() {
				return errors.New("still down")
			}
			running.Store(true)
			return nil
		},
		40*time.Millisecond,
	))

	var healthCallbacks atomic.Int32
	var lastStatusMu sync.Mutex
	var lastSnap HealthSnapshot
	sup.SetOnHealthCheck(func(snap HealthSnapshot) {
		healthCallbacks.Add(1)
		lastStatusMu.Lock()
		lastSnap = snap
		lastStatusMu.Unlock()
	})

	sup.RecordKeystroke(250_000, 10)
	sup.Start(context.Background())
	defer sup.Stop()

	// Wait for initial healthy beat.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		ss := sup.Snapshot()
		if ss.Healthy && ss.SelfHealCount >= 1 && healthCallbacks.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ss := sup.Snapshot()
	if !ss.Healthy || ss.HealthStatus != HealthStatusHealthy || ss.SelfHealCount < 1 {
		t.Fatalf("initial healthy snapshot = %+v", ss)
	}

	// Drop the bridge: watchdog actively triggers healFn and recovers.
	running.Store(false)
	deadline = time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(deadline) {
		if healCalls.Load() >= 1 && sup.Snapshot().SelfHealCount >= 2 && sup.Snapshot().Healthy {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ss = sup.Snapshot()
	if healCalls.Load() < 1 || ss.SelfHealCount < 2 || !ss.Healthy || ss.HealthStatus != HealthStatusHealthy {
		t.Fatalf("post-heal snapshot = %+v, healCalls=%d", ss, healCalls.Load())
	}

	// Persistent failure reports degraded status while cooldown throttles heal attempts.
	healShouldSucceed.Store(false)
	beforeCalls := healCalls.Load()
	running.Store(false)
	deadline = time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if sup.Snapshot().HealthStatus == HealthStatusDegraded && healCalls.Load() > beforeCalls {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ss = sup.Snapshot()
	if ss.Healthy || ss.HealthStatus != HealthStatusDegraded {
		t.Fatalf("degraded snapshot = %+v, want Healthy=false status=%q", ss, HealthStatusDegraded)
	}

	lastStatusMu.Lock()
	gotLast := lastSnap
	lastStatusMu.Unlock()
	if gotLast.TotalKeystrokes != 1 || gotLast.CurrentCharsPerMinute < 10 || gotLast.P50LatencyUs < 200 {
		t.Fatalf("callback telemetry snapshot missing metrics: %+v", gotLast)
	}
}
