package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/config"
)

type countingCandidateDoer struct {
	calls atomic.Int32
}

func (c *countingCandidateDoer) Do(req *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return nil, errors.New("unexpected_call")
}

func TestProbeLocalModelReadyAndZeroAmbientContext(t *testing.T) {
	var probeCalls atomic.Int32
	var receivedAuth string
	var receivedPath string
	var receivedBodyLen int64
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeCalls.Add(1)
		mu.Lock()
		receivedAuth = r.Header.Get("Authorization")
		receivedPath = r.URL.Path
		receivedBodyLen = r.ContentLength
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"name": "qwen2.5:7b", "model": "qwen2.5:7b"},
				{"name": "llama3:latest", "model": "llama3:latest"},
			},
		})
	}))
	defer srv.Close()

	snap := ProbeLocalModel(context.Background(), LocalProbeConfig{
		Endpoint: srv.URL + "/v1",
		Model:    "qwen2.5:7b",
		Client:   srv.Client(),
	})

	if snap.State != LocalModelReady || snap.Model != "qwen2.5:7b" || !CanRequestLocalModel(snap) {
		t.Fatalf("expected ready snapshot for qwen2.5:7b, got %+v", snap)
	}
	if probeCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 probe request, got %d", probeCalls.Load())
	}
	mu.Lock()
	gotPath := receivedPath
	gotAuth := receivedAuth
	gotBodyLen := receivedBodyLen
	mu.Unlock()
	if gotPath != "/api/tags" {
		t.Fatalf("expected /api/tags probe path, got %q", gotPath)
	}
	if gotAuth != "" || gotBodyLen > 0 {
		t.Fatalf("probe must never send credentials or request body: auth=%q contentLen=%d", gotAuth, gotBodyLen)
	}

	// Base model name without explicit tag matches :latest.
	snapLatest := ProbeLocalModel(context.Background(), LocalProbeConfig{
		Endpoint: srv.URL,
		Model:    "llama3",
		Client:   srv.Client(),
	})
	if snapLatest.State != LocalModelReady || snapLatest.Model != "llama3:latest" {
		t.Fatalf("expected llama3 to match llama3:latest, got %+v", snapLatest)
	}
}

func TestProbeLocalModelMissingModelDegradesWithZeroInferenceRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"name": "mistral:7b"},
			},
		})
	}))
	defer srv.Close()

	doneCh := make(chan LocalModelSnapshot, 4)
	gate := NewLocalRequestGate(LocalProbeConfig{
		Endpoint:      srv.URL,
		Model:         "qwen2.5:7b",
		Client:        srv.Client(),
		OnStateChange: func(s LocalModelSnapshot) { doneCh <- s },
	})
	defer gate.Close()

	// Before probe completes: undetected -> 0 inference requests.
	candidateDoer := &countingCandidateDoer{}
	if executed, err := gate.ExecuteIfReady(context.Background(), func(reqCtx context.Context, snap LocalModelSnapshot) error {
		_, _ = candidateDoer.Do(&http.Request{})
		return nil
	}); executed || !errors.Is(err, ErrLocalModelNotReady) {
		t.Fatalf("expected ErrLocalModelNotReady before probe, got executed=%v err=%v", executed, err)
	}
	if candidateDoer.calls.Load() != 0 {
		t.Fatalf("expected 0 outbound inference requests before probe, got %d", candidateDoer.calls.Load())
	}

	if !gate.ProbeAsync() {
		t.Fatal("expected ProbeAsync to launch background probe")
	}
	select {
	case snap := <-doneCh:
		if snap.State != LocalModelDegraded || snap.ErrKind != "model_missing" {
			t.Fatalf("expected degraded/model_missing for missing model, got %+v", snap)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for async probe result")
	}

	if gate.CanRequest() {
		t.Fatal("CanRequest must be false when configured model is missing")
	}
	executed, err := gate.ExecuteIfReady(context.Background(), func(reqCtx context.Context, snap LocalModelSnapshot) error {
		_, _ = candidateDoer.Do(&http.Request{})
		return nil
	})
	if executed || !errors.Is(err, ErrLocalModelNotReady) || candidateDoer.calls.Load() != 0 {
		t.Fatalf("missing model must issue 0 outbound inference requests: executed=%v err=%v calls=%d", executed, err, candidateDoer.calls.Load())
	}

	// Empty models list -> degraded / no_models with CanRequestLocalModel == false.
	emptySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer emptySrv.Close()
	emptySnap := ProbeLocalModel(context.Background(), LocalProbeConfig{
		Endpoint: emptySrv.URL,
		Client:   emptySrv.Client(),
	})
	if emptySnap.State != LocalModelDegraded || emptySnap.ErrKind != "no_models" || CanRequestLocalModel(emptySnap) {
		t.Fatalf("expected degraded/no_models for empty tags response, got %+v", emptySnap)
	}
}

func TestLocalRequestGateUnreachableServerOfflineWithBackoff(t *testing.T) {
	// Create and immediately close a localhost server so the port refuses connections reliably.
	closedSrv := httptest.NewServer(http.NewServeMux())
	deadURL := closedSrv.URL
	closedSrv.Close()

	var mu sync.Mutex
	fakeNow := time.Date(2026, 11, 24, 20, 0, 0, 0, time.UTC)
	nowFn := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return fakeNow
	}
	advance := func(d time.Duration) {
		mu.Lock()
		fakeNow = fakeNow.Add(d)
		mu.Unlock()
	}

	doneCh := make(chan LocalModelSnapshot, 8)
	gate := NewLocalRequestGate(LocalProbeConfig{
		Endpoint:        deadURL,
		Model:           "qwen2.5:7b",
		ProbeTimeout:    500 * time.Millisecond,
		ReprobeCooldown: 10 * time.Second,
		MaxBackoff:      60 * time.Second,
		Now:             nowFn,
		OnStateChange:   func(s LocalModelSnapshot) { doneCh <- s },
	})
	defer gate.Close()

	// Failure #1 -> offline with 10s cooldown.
	if !gate.ProbeAsync() {
		t.Fatal("expected first ProbeAsync to start")
	}
	snap1 := <-doneCh
	if snap1.State != LocalModelOffline || snap1.ConsecutiveFailures != 1 {
		t.Fatalf("expected offline with 1 failure, got %+v", snap1)
	}
	if gate.CanRequest() {
		t.Fatal("CanRequest must be false when offline")
	}

	// Within cooldown: ProbeAsync must be skipped (non-blocking backoff).
	advance(5 * time.Second)
	if gate.ProbeAsync() {
		t.Fatal("expected ProbeAsync to be suppressed during cooldown")
	}

	// After 10s cooldown elapses: Failure #2 -> exponential backoff 20s.
	advance(6 * time.Second)
	if !gate.ProbeAsync() {
		t.Fatal("expected second ProbeAsync after cooldown")
	}
	snap2 := <-doneCh
	if snap2.State != LocalModelOffline || snap2.ConsecutiveFailures != 2 {
		t.Fatalf("expected offline with 2 failures, got %+v", snap2)
	}

	// Still suppressed at +15s (needs 20s backoff).
	advance(15 * time.Second)
	if gate.ProbeAsync() {
		t.Fatal("expected ProbeAsync suppressed during 20s exponential backoff")
	}

	// Explicit ForceProbeAsync bypasses backoff immediately.
	if !gate.ForceProbeAsync() {
		t.Fatal("expected ForceProbeAsync to bypass backoff")
	}
	snap3 := <-doneCh
	if snap3.ConsecutiveFailures != 3 {
		t.Fatalf("expected 3 consecutive failures after ForceProbeAsync, got %+v", snap3)
	}
}

func TestLocalProbeTimeoutEnforcementAndTimeoutTiers(t *testing.T) {
	blockCh := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-blockCh:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(blockCh)
		srv.Close()
	}()

	start := time.Now()
	snap := ProbeLocalModel(context.Background(), LocalProbeConfig{
		Endpoint:     srv.URL,
		Model:        "qwen2.5:7b",
		ProbeTimeout: 120 * time.Millisecond,
		Client:       &http.Client{},
	})
	elapsed := time.Since(start)
	if snap.State != LocalModelOffline || snap.ErrKind != "timeout" {
		t.Fatalf("expected offline/timeout on slow server, got %+v", snap)
	}
	if elapsed > time.Second {
		t.Fatalf("probe timeout not enforced promptly: took %v", elapsed)
	}

	// Config clamps oversized ProbeTimeout (>1.5s) and RequestTimeout (>4s).
	gate := NewLocalRequestGate(LocalProbeConfig{
		Endpoint:       srv.URL,
		ProbeTimeout:   30 * time.Second,
		RequestTimeout: 30 * time.Second,
	})
	defer gate.Close()
	normCfg := gate.Config()
	if normCfg.ProbeTimeout > MaxLocalProbeTimeout {
		t.Fatalf("ProbeTimeout exceeded hard cap 1.5s: got %v", normCfg.ProbeTimeout)
	}
	if normCfg.RequestTimeout > DefaultLocalAITimeout {
		t.Fatalf("RequestTimeout exceeded 4s tier: got %v", normCfg.RequestTimeout)
	}

	// Non-localhost endpoint is rejected without any network I/O.
	nonLocal := ProbeLocalModel(context.Background(), LocalProbeConfig{
		Endpoint: "https://api.example.com/v1",
		Model:    "qwen2.5:7b",
	})
	if nonLocal.State != LocalModelOffline || nonLocal.ErrKind != "non_local_endpoint" {
		t.Fatalf("expected non_local_endpoint rejection, got %+v", nonLocal)
	}

	// Verify EffectiveAITimeout tiers (remote stays 1.5s even if TimeoutSeconds=30; local default 4s, max 10s).
	remote := config.AIProfile{Provider: "deepseek", BaseURL: "https://api.deepseek.com/v1", TimeoutSeconds: 30}
	if got := EffectiveAITimeout(remote); got != DefaultRemoteAITimeout {
		t.Fatalf("remote timeout must stay 1.5s, got %v", got)
	}
	localDefault := config.AIProfile{Provider: "ollama", BaseURL: "http://127.0.0.1:11434/v1", TimeoutSeconds: 0}
	if got := EffectiveAITimeout(localDefault); got != DefaultLocalAITimeout {
		t.Fatalf("local default timeout must be 4s, got %v", got)
	}
	localClamped := config.AIProfile{Provider: "ollama", BaseURL: "http://127.0.0.1:11434/v1", TimeoutSeconds: 30}
	if got := EffectiveAITimeout(localClamped); got != MaxAIRequestTimeout {
		t.Fatalf("local timeout must clamp at 10s, got %v", got)
	}
}

func TestLocalRequestGateWarmupCircuitBreakerAndPromptPrivacy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{{"name": "qwen2.5:7b"}},
		})
	}))
	defer srv.Close()

	doneCh := make(chan LocalModelSnapshot, 8)
	gate := NewLocalRequestGate(LocalProbeConfig{
		Endpoint:         srv.URL,
		Model:            "qwen2.5:7b",
		RequireWarmup:    true,
		BreakerThreshold: 2,
		Client:           srv.Client(),
		OnStateChange:    func(s LocalModelSnapshot) { doneCh <- s },
	})
	defer gate.Close()

	// Probe succeeds -> warming (NOT ready yet, 0 requests).
	if !gate.ProbeAsync() {
		t.Fatal("expected ProbeAsync to launch")
	}
	snapWarming := <-doneCh
	if snapWarming.State != LocalModelWarming || gate.CanRequest() {
		t.Fatalf("expected warming state blocking requests, got %+v", snapWarming)
	}

	// Warmup failure closes gate to offline.
	gate.CompleteWarmup(false, "warmup_timeout")
	snapFailed := <-doneCh
	if snapFailed.State != LocalModelOffline || gate.CanRequest() {
		t.Fatalf("expected offline after warmup failure, got %+v", snapFailed)
	}

	// Explicit re-probe + successful warmup -> ready (allows request).
	if !gate.ForceProbeAsync() {
		t.Fatal("expected ForceProbeAsync to launch")
	}
	<-doneCh // warming
	gate.CompleteWarmup(true, "")
	snapReady := <-doneCh
	if snapReady.State != LocalModelReady || !gate.CanRequest() {
		t.Fatalf("expected ready after warmup success, got %+v", snapReady)
	}

	// User toggle off -> immediately blocks requests.
	gate.SetEnabled(false)
	if gate.CanRequest() {
		t.Fatal("CanRequest must be false when Enabled=false")
	}
	gate.SetEnabled(true)

	// Ready request succeeds and gets a bounded deadline <= 4s.
	var calls int
	executed, err := gate.ExecuteIfReady(context.Background(), func(reqCtx context.Context, snap LocalModelSnapshot) error {
		calls++
		deadline, ok := reqCtx.Deadline()
		if !ok || time.Until(deadline) > DefaultLocalAITimeout+100*time.Millisecond {
			t.Fatalf("expected request context deadline <= 4s, ok=%v remaining=%v", ok, time.Until(deadline))
		}
		return nil
	})
	if !executed || err != nil || calls != 1 {
		t.Fatalf("expected 1 executed request when ready, got executed=%v err=%v calls=%d", executed, err, calls)
	}

	// Two consecutive runtime failures trip circuit breaker (threshold=2) -> zero further requests.
	_, _ = gate.ExecuteIfReady(context.Background(), func(context.Context, LocalModelSnapshot) error {
		return errors.New("boom-1")
	})
	_, _ = gate.ExecuteIfReady(context.Background(), func(context.Context, LocalModelSnapshot) error {
		return errors.New("boom-2")
	})
	snapTripped := <-doneCh
	if snapTripped.State != LocalModelDegraded || gate.CanRequest() {
		t.Fatalf("expected circuit breaker to trip to degraded, got %+v", snapTripped)
	}
	executedAfterTrip, errAfterTrip := gate.ExecuteIfReady(context.Background(), func(context.Context, LocalModelSnapshot) error {
		calls++
		return nil
	})
	if executedAfterTrip || !errors.Is(errAfterTrip, ErrLocalModelNotReady) || calls != 1 {
		t.Fatalf("tripped breaker must block requests: executed=%v err=%v calls=%d", executedAfterTrip, errAfterTrip, calls)
	}

	// BuildLocalCandidatePrompt privacy check: only pinyin + 64-rune truncated app name.
	longApp := strings.Repeat("微", 80) + "\x00\n"
	prompt, err := BuildLocalCandidatePrompt("nihao'shijie", longApp)
	if err != nil {
		t.Fatalf("BuildLocalCandidatePrompt failed: %v", err)
	}
	if !strings.Contains(prompt, "Pinyin: nihao'shijie") {
		t.Fatalf("prompt missing pinyin: %q", prompt)
	}
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "Application: ") {
			appVal := strings.TrimPrefix(line, "Application: ")
			if utf8.RuneCountInString(appVal) != MaxLocalPromptAppNameRunes {
				t.Fatalf("expected appName truncated to %d runes, got %d", MaxLocalPromptAppNameRunes, utf8.RuneCountInString(appVal))
			}
		}
	}
	if _, err := BuildLocalCandidatePrompt("hello world; clipboard", "Notes"); err == nil {
		t.Fatal("expected non-pinyin input to be rejected")
	}
}

func TestLocalRequestGateConcurrentHotPathAndCloseUnderRace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{{"name": "qwen2.5:7b"}},
		})
	}))
	defer srv.Close()

	gate := NewLocalRequestGate(LocalProbeConfig{
		Endpoint:        srv.URL,
		Model:           "qwen2.5:7b",
		ProbeTimeout:    500 * time.Millisecond,
		ReprobeCooldown: 10 * time.Millisecond,
		Client:          srv.Client(),
	})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Simulated typing hot-path readers calling CanRequest, Snapshot, and ExecuteIfReady concurrently.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = gate.CanRequest()
					_ = gate.Snapshot()
					_, _ = gate.ExecuteIfReady(context.Background(), func(context.Context, LocalModelSnapshot) error {
						return nil
					})
				}
			}
		}()
	}

	// Concurrent background probe triggers.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if id%2 == 0 {
					gate.ProbeAsync()
				} else {
					gate.ForceProbeAsync()
				}
				time.Sleep(2 * time.Millisecond)
			}
		}(i)
	}

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	// Close must cleanly cancel any in-flight probe and be idempotent.
	if err := gate.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if err := gate.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
	if gate.ProbeAsync() || gate.CanRequest() {
		t.Fatal("closed gate must reject ProbeAsync and CanRequest")
	}
}
