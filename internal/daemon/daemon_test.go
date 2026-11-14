package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
)

func TestLRUByteBudget(t *testing.T) {
	c := NewLRU[string, string](LRUOptions[string]{MaxBytes: 5, SizeFn: func(v string) int { return len(v) }})
	c.Put("a", "1234")
	c.Put("b", "12")
	if _, ok := c.Get("a"); ok {
		t.Fatal("old value should be evicted")
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("new value missing")
	}
	h, m := c.Stats()
	if h != 1 || m != 1 {
		t.Fatalf("stats=%d,%d", h, m)
	}
}

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
	s.Start(context.Background())
	defer s.Stop()
	time.Sleep(1100 * time.Millisecond)
	if compact.Load() != 1 {
		t.Fatalf("compact=%d", compact.Load())
	}
	if probe.Load() == 0 || s.Snapshot().SelfHealCount == 0 {
		t.Fatal("probe did not recover")
	}
}

func BenchmarkLRUGet(b *testing.B) {
	c := NewLRU[string, int](LRUOptions[int]{MaxEntries: 128})
	c.Put("key", 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Get("key")
	}
}
