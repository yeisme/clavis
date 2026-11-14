package daemon

import (
	"context"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
)

type CompactionHook func(context.Context) error
type WatchdogProbe func(context.Context) (bool, error)
type HealthSnapshot struct {
	AllocMB               float64
	SysMB                 float64
	NumGoroutine          int
	Uptime                time.Duration
	CurrentWPM            float64
	CurrentCharsPerMinute float64
	CurrentWordsPerMinute float64
	P50LatencyUs          float64
	TotalKeystrokes       uint64
	P99LatencyUs          float64
	CompactionRuns        uint64
	SelfHealCount         uint64
}
type telemetryEvent struct {
	at      time.Time
	latency int64
	runes   int
}

// ResidentSupervisor maintains bounded process health and telemetry for a resident daemon.
type ResidentSupervisor struct {
	cfg               config.DaemonConfig
	started           time.Time
	lastInteraction   atomic.Int64
	mu                sync.Mutex
	events            []telemetryEvent
	hooks             []CompactionHook
	probes            []WatchdogProbe
	probeHealthy      []bool
	idleCompacted     bool
	lastMemoryCompact atomic.Int64
	compactions       atomic.Uint64
	heals             atomic.Uint64
	total             atomic.Uint64
	cancel            context.CancelFunc
	done              chan struct{}
}

func NewResidentSupervisor(cfg config.DaemonConfig) *ResidentSupervisor {
	s := &ResidentSupervisor{cfg: cfg, started: time.Now(), done: make(chan struct{})}
	s.lastInteraction.Store(s.started.UnixNano())
	return s
}
func (s *ResidentSupervisor) RegisterCompactionHook(h CompactionHook) {
	if h == nil {
		return
	}
	s.mu.Lock()
	s.hooks = append(s.hooks, h)
	s.mu.Unlock()
}
func (s *ResidentSupervisor) RegisterWatchdogProbe(p WatchdogProbe) {
	if p == nil {
		return
	}
	s.mu.Lock()
	s.probes = append(s.probes, p)
	s.probeHealthy = append(s.probeHealthy, false)
	s.mu.Unlock()
}
func (s *ResidentSupervisor) RecordInteraction() {
	s.lastInteraction.Store(time.Now().UnixNano())
	s.mu.Lock()
	s.idleCompacted = false
	s.mu.Unlock()
}
func (s *ResidentSupervisor) RecordKeystroke(latencyNs int64, committedRunes int) {
	now := time.Now()
	s.lastInteraction.Store(now.UnixNano())
	s.total.Add(1)
	s.mu.Lock()
	s.idleCompacted = false
	s.events = append(s.events, telemetryEvent{now, latencyNs, committedRunes})
	s.trimLocked(now)
	s.mu.Unlock()
}
func (s *ResidentSupervisor) trimLocked(now time.Time) {
	cut := now.Add(-60 * time.Second)
	i := 0
	for i < len(s.events) && s.events[i].at.Before(cut) {
		i++
	}
	if i > 0 {
		s.events = append([]telemetryEvent(nil), s.events[i:]...)
	}
}
func (s *ResidentSupervisor) Start(ctx context.Context) {
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	interval := time.Duration(s.cfg.WatchdogIntervalMs) * time.Millisecond
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.watchdog(ctx)
			}
		}
	}()
}
func (s *ResidentSupervisor) Stop() {
	if s.cancel != nil {
		s.cancel()
		<-s.done
		s.cancel = nil
	}
}
func (s *ResidentSupervisor) watchdog(ctx context.Context) {
	now := time.Now()
	if now.Sub(time.Unix(0, s.lastInteraction.Load())) >= time.Duration(s.cfg.IdleCompactSeconds)*time.Second {
		s.mu.Lock()
		doCompact := !s.idleCompacted
		if doCompact {
			s.idleCompacted = true
		}
		s.mu.Unlock()
		if doCompact {
			s.compact(ctx)
		}
	}
	s.mu.Lock()
	probes := append([]WatchdogProbe(nil), s.probes...)
	healthy := append([]bool(nil), s.probeHealthy...)
	s.mu.Unlock()
	for i, p := range probes {
		ok, err := p(ctx)
		if err != nil {
			ok = false
		}
		if ok && (i >= len(healthy) || !healthy[i]) {
			s.heals.Add(1)
		}
		s.mu.Lock()
		if i < len(s.probeHealthy) {
			s.probeHealthy[i] = ok
		}
		s.mu.Unlock()
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	if s.cfg.MaxMemoryMB > 0 && m.HeapAlloc > uint64(s.cfg.MaxMemoryMB)*1024*1024 {
		last := time.Unix(0, s.lastMemoryCompact.Load())
		if now.Sub(last) >= time.Minute {
			s.lastMemoryCompact.Store(now.UnixNano())
			s.compact(ctx)
			runtime.GC()
		}
	}
}
func (s *ResidentSupervisor) compact(ctx context.Context) {
	s.compactions.Add(1)
	s.mu.Lock()
	hooks := append([]CompactionHook(nil), s.hooks...)
	s.mu.Unlock()
	for _, h := range hooks {
		_ = h(ctx)
	}
}
func (s *ResidentSupervisor) Snapshot() HealthSnapshot {
	now := time.Now()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	s.mu.Lock()
	s.trimLocked(now)
	ev := append([]telemetryEvent(nil), s.events...)
	s.mu.Unlock()
	var runes int
	lat := make([]int64, 0, len(ev))
	for _, e := range ev {
		runes += e.runes
		lat = append(lat, e.latency)
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	var p50, p99 float64
	if len(lat) > 0 {
		p50 = float64(lat[(len(lat)*50+99)/100-1]) / 1000
		p99 = float64(lat[(len(lat)*99+99)/100-1]) / 1000
	}
	// Events are kept to the rolling 60-second window, so their rune count is
	// already a chars/minute value. Avoid scaling by the age of the first event:
	// a burst at startup is still measured over the defined rolling window.
	cpm := float64(runes)
	wpm := cpm / 5
	return HealthSnapshot{AllocMB: float64(m.HeapAlloc) / 1024 / 1024, SysMB: float64(m.Sys) / 1024 / 1024, NumGoroutine: runtime.NumGoroutine(), Uptime: now.Sub(s.started), CurrentWPM: wpm, CurrentCharsPerMinute: cpm, CurrentWordsPerMinute: wpm, P50LatencyUs: p50, TotalKeystrokes: s.total.Load(), P99LatencyUs: p99, CompactionRuns: s.compactions.Load(), SelfHealCount: s.heals.Load()}
}
