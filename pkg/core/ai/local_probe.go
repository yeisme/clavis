package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/config"
)

const (
	// DefaultLocalOllamaEndpoint is the default loopback Ollama base URL.
	DefaultLocalOllamaEndpoint = "http://127.0.0.1:11434"
	// DefaultLocalTagsPath is the Ollama tags endpoint probed by C1.
	DefaultLocalTagsPath = "/api/tags"

	// DefaultLocalProbeTimeout and MaxLocalProbeTimeout bound a single C1 /api/tags probe (<= 1.5s).
	DefaultLocalProbeTimeout = 1500 * time.Millisecond
	MaxLocalProbeTimeout     = 1500 * time.Millisecond

	// DefaultRemoteAITimeout keeps remote typing-path requests bounded at 1.5s.
	DefaultRemoteAITimeout = 1500 * time.Millisecond
	// DefaultLocalAITimeout is the default C3 request timeout for warmed local models (<= 4s).
	DefaultLocalAITimeout = 4 * time.Second
	// MaxAIRequestTimeout is the hard upper bound for any local AI profile timeout.
	MaxAIRequestTimeout = 10 * time.Second

	// DefaultReprobeCooldown is the minimum delay between automatic background probes.
	DefaultReprobeCooldown = 15 * time.Second
	// DefaultMaxProbeBackoff caps exponential circuit-breaker backoff on consecutive failures.
	DefaultMaxProbeBackoff = 2 * time.Minute
	// DefaultLocalBreakerThreshold is the consecutive failure count that trips the circuit breaker.
	DefaultLocalBreakerThreshold = 3

	// MaxLocalPromptAppNameRunes caps the app name included in C3 local candidate prompts.
	MaxLocalPromptAppNameRunes = 64

	maxLocalTagsResponseBytes = 1 << 20 // 1 MiB
)

// ErrLocalModelNotReady indicates the C1/C3 readiness gate blocked a local request
// without creating or sending any outbound HTTP call.
var ErrLocalModelNotReady = errors.New("local_model_not_ready")

// LocalModelState describes the observable readiness state of a local Ollama endpoint.
type LocalModelState string

const (
	LocalModelUndetected LocalModelState = "undetected"
	LocalModelOffline    LocalModelState = "offline"
	LocalModelWarming    LocalModelState = "warming"
	LocalModelReady      LocalModelState = "ready"
	LocalModelDegraded   LocalModelState = "degraded"
)

// LocalProbeConfig configures bounded C1 asynchronous probing and C3 request gating.
type LocalProbeConfig struct {
	// Endpoint is the local Ollama base URL or /api/tags URL (loopback/localhost only).
	// Defaults to http://127.0.0.1:11434.
	Endpoint string
	// Model is the configured Ollama model name to verify in /api/tags.
	// When empty, any model reported by /api/tags satisfies readiness.
	Model string
	// ProbeTimeout bounds a single /api/tags probe. Defaults to and is clamped <= 1.5s.
	ProbeTimeout time.Duration
	// RequestTimeout bounds a local C3 candidate request. Defaults to and is clamped <= 4s.
	RequestTimeout time.Duration
	// ReprobeCooldown is the base interval between background probes. Defaults to 15s.
	ReprobeCooldown time.Duration
	// MaxBackoff caps exponential backoff on consecutive failures. Defaults to 2m.
	MaxBackoff time.Duration
	// BreakerThreshold is the consecutive failure count before tripping circuit-breaker state.
	// Defaults to 3.
	BreakerThreshold int
	// RequireWarmup holds state in LocalModelWarming after /api/tags succeeds until
	// CompleteWarmup(true, "") transitions the gate to LocalModelReady.
	RequireWarmup bool
	// Client is an optional injectable *http.Client (used when Doer is nil).
	Client *http.Client
	// Doer is an optional injectable HTTPDoer (takes precedence over Client when non-nil).
	Doer HTTPDoer
	// Now overrides time.Now for deterministic tests.
	Now func() time.Time
	// OnStateChange is invoked outside the gate mutex whenever a probe/warmup/breaker updates state.
	OnStateChange func(LocalModelSnapshot)
}

func (c LocalProbeConfig) normalized() LocalProbeConfig {
	out := c
	if strings.TrimSpace(out.Endpoint) == "" {
		out.Endpoint = DefaultLocalOllamaEndpoint
	}
	out.Model = strings.TrimSpace(out.Model)
	if out.ProbeTimeout <= 0 || out.ProbeTimeout > MaxLocalProbeTimeout {
		out.ProbeTimeout = DefaultLocalProbeTimeout
	}
	if out.RequestTimeout <= 0 || out.RequestTimeout > DefaultLocalAITimeout {
		out.RequestTimeout = DefaultLocalAITimeout
	}
	if out.ReprobeCooldown <= 0 {
		out.ReprobeCooldown = DefaultReprobeCooldown
	}
	if out.MaxBackoff <= 0 {
		out.MaxBackoff = DefaultMaxProbeBackoff
	}
	if out.MaxBackoff < out.ReprobeCooldown {
		out.MaxBackoff = out.ReprobeCooldown
	}
	if out.BreakerThreshold <= 0 {
		out.BreakerThreshold = DefaultLocalBreakerThreshold
	}
	if out.Now == nil {
		out.Now = time.Now
	}
	return out
}

func (c LocalProbeConfig) httpDoer() HTTPDoer {
	if c.Doer != nil {
		return c.Doer
	}
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: c.ProbeTimeout}
}

// LocalModelSnapshot is an immutable, side-effect-free readiness snapshot.
// ErrKind is a stable diagnostic class and never contains response bodies or credentials.
type LocalModelSnapshot struct {
	State               LocalModelState `json:"state"`
	Endpoint            string          `json:"endpoint"`
	Model               string          `json:"model,omitempty"`
	AvailableModels     []string        `json:"availableModels,omitempty"`
	Enabled             bool            `json:"enabled"`
	Warmed              bool            `json:"warmed"`
	CheckedAt           time.Time       `json:"checkedAt,omitempty"`
	NextProbeAt         time.Time       `json:"nextProbeAt,omitempty"`
	ConsecutiveFailures int             `json:"consecutiveFailures,omitempty"`
	ErrKind             string          `json:"errKind,omitempty"`
}

func (s LocalModelSnapshot) clone() LocalModelSnapshot {
	out := s
	if len(s.AvailableModels) > 0 {
		out.AvailableModels = append([]string(nil), s.AvailableModels...)
	}
	return out
}

// EffectiveAITimeout returns the profile-specific request timeout.
// Remote profiles remain capped at 1.5s; local profiles default to 4s and never exceed 10s.
func EffectiveAITimeout(profile config.AIProfile) time.Duration {
	isLocal := IsKeylessAllowed(profile.Provider, profile.BaseURL) ||
		strings.EqualFold(strings.TrimSpace(profile.Provider), "ollama")
	configured := time.Duration(profile.TimeoutSeconds * float64(time.Second))
	if !isLocal {
		if configured > 0 && configured < DefaultRemoteAITimeout {
			return configured
		}
		return DefaultRemoteAITimeout
	}
	if configured <= 0 {
		return DefaultLocalAITimeout
	}
	if configured > MaxAIRequestTimeout {
		return MaxAIRequestTimeout
	}
	return configured
}

// BuildLocalCandidatePrompt creates the sole allowed C3 local candidate prompt.
// It rejects non-pinyin input, deterministically truncates appName to 64 runes,
// and never appends clipboard, window title, URL, or surrounding text.
func BuildLocalCandidatePrompt(pinyin, appName string) (string, error) {
	raw := strings.ToLower(strings.TrimSpace(pinyin))
	letters := strings.ReplaceAll(raw, "'", "")
	if len(letters) < 2 || len(letters) > 64 {
		return "", errors.New("invalid_pinyin_length")
	}
	for _, r := range raw {
		if (r < 'a' || r > 'z') && r != '\'' {
			return "", errors.New("invalid_pinyin_characters")
		}
	}

	cleanApp := sanitizeLocalAppName(appName)
	var b strings.Builder
	b.WriteString("You are a Chinese pinyin input method. Convert the pinyin into the most likely Simplified Chinese words or phrases.\n")
	b.WriteString("Reply with at most 5 candidates, one per line, Chinese text only, no numbering, no pinyin, no explanation.\n")
	if cleanApp != "" {
		b.WriteString("Application: ")
		b.WriteString(cleanApp)
		b.WriteString("\n")
	}
	b.WriteString("Pinyin: ")
	b.WriteString(raw)
	return b.String(), nil
}

func sanitizeLocalAppName(appName string) string {
	if appName == "" {
		return ""
	}
	if !utf8.ValidString(appName) {
		appName = strings.ToValidUTF8(appName, "")
	}
	var b strings.Builder
	for _, r := range appName {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	s := strings.Join(strings.Fields(b.String()), " ")
	runes := []rune(s)
	if len(runes) > MaxLocalPromptAppNameRunes {
		return string(runes[:MaxLocalPromptAppNameRunes])
	}
	return s
}

// ResolveLocalTagsURL validates that rawEndpoint targets a loopback/localhost address
// and resolves its Ollama /api/tags probe URL.
func ResolveLocalTagsURL(rawEndpoint string) (string, error) {
	trimmed := strings.TrimSpace(rawEndpoint)
	if trimmed == "" {
		trimmed = DefaultLocalOllamaEndpoint
	}
	u, err := parseAndValidateBaseURL(trimmed)
	if err != nil {
		return "", err
	}
	if !isLoopbackHost(u.Hostname()) {
		return "", &ClientError{
			Kind:    "non_local_endpoint",
			Message: "local model probe only permits localhost/loopback endpoints",
		}
	}
	p := strings.TrimRight(u.Path, "/")
	switch {
	case strings.HasSuffix(p, DefaultLocalTagsPath):
		// Already pointing at /api/tags.
	case strings.HasSuffix(p, "/v1"):
		p = strings.TrimSuffix(p, "/v1") + DefaultLocalTagsPath
	default:
		p += DefaultLocalTagsPath
	}
	u.Path = p
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

type ollamaTagsResponse struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

// ProbeLocalModel executes a single bounded (<= 1.5s) probe against Ollama /api/tags,
// verifying server reachability and configured model presence without sending any
// credentials or ambient user context.
func ProbeLocalModel(ctx context.Context, cfg LocalProbeConfig) LocalModelSnapshot {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg = cfg.normalized()
	now := cfg.Now().UTC()

	tagsURL, err := ResolveLocalTagsURL(cfg.Endpoint)
	if err != nil {
		errKind := "invalid_endpoint"
		var ce *ClientError
		if errors.As(err, &ce) && ce.Kind != "" {
			errKind = ce.Kind
		}
		return LocalModelSnapshot{
			State:     LocalModelOffline,
			Endpoint:  strings.TrimSpace(cfg.Endpoint),
			Model:     cfg.Model,
			Enabled:   true,
			Warmed:    false,
			CheckedAt: now,
			ErrKind:   errKind,
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, cfg.ProbeTimeout)
	defer cancel()

	// Send a minimal, credential-free, ambient-context-free GET /api/tags request.
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, tagsURL, nil)
	if err != nil {
		return LocalModelSnapshot{
			State:     LocalModelOffline,
			Endpoint:  tagsURL,
			Model:     cfg.Model,
			Enabled:   true,
			Warmed:    false,
			CheckedAt: now,
			ErrKind:   "request_build_failed",
		}
	}
	req.Header.Set("Accept", "application/json")

	resp, err := cfg.httpDoer().Do(req)
	if err != nil {
		errKind := "unreachable"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			errKind = "timeout"
		} else if errors.Is(err, context.Canceled) || errors.Is(probeCtx.Err(), context.Canceled) {
			errKind = "cancelled"
		} else {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				errKind = "timeout"
			}
			var urlErr *url.Error
			if errors.As(err, &urlErr) && urlErr.Timeout() {
				errKind = "timeout"
			}
		}
		return LocalModelSnapshot{
			State:     LocalModelOffline,
			Endpoint:  tagsURL,
			Model:     cfg.Model,
			Enabled:   true,
			Warmed:    false,
			CheckedAt: now,
			ErrKind:   errKind,
		}
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxLocalTagsResponseBytes))
	if readErr != nil {
		errKind := "read_failed"
		if errors.Is(readErr, context.DeadlineExceeded) || errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			errKind = "timeout"
		}
		return LocalModelSnapshot{
			State:     LocalModelOffline,
			Endpoint:  tagsURL,
			Model:     cfg.Model,
			Enabled:   true,
			Warmed:    false,
			CheckedAt: now,
			ErrKind:   errKind,
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return LocalModelSnapshot{
			State:     LocalModelOffline,
			Endpoint:  tagsURL,
			Model:     cfg.Model,
			Enabled:   true,
			Warmed:    false,
			CheckedAt: now,
			ErrKind:   fmt.Sprintf("http_%d", resp.StatusCode),
		}
	}

	var parsed ollamaTagsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return LocalModelSnapshot{
			State:     LocalModelOffline,
			Endpoint:  tagsURL,
			Model:     cfg.Model,
			Enabled:   true,
			Warmed:    false,
			CheckedAt: now,
			ErrKind:   "invalid_response",
		}
	}

	models := make([]string, 0, len(parsed.Models))
	seen := make(map[string]bool, len(parsed.Models))
	for _, entry := range parsed.Models {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			name = strings.TrimSpace(entry.Model)
		}
		if name != "" && !seen[name] {
			seen[name] = true
			models = append(models, name)
		}
	}

	if len(models) == 0 {
		return LocalModelSnapshot{
			State:     LocalModelDegraded,
			Endpoint:  tagsURL,
			Model:     cfg.Model,
			Enabled:   true,
			Warmed:    false,
			CheckedAt: now,
			ErrKind:   "no_models",
		}
	}

	matchedModel := ""
	if cfg.Model == "" {
		matchedModel = models[0]
	} else {
		matchedModel = matchOllamaModel(cfg.Model, models)
	}

	if matchedModel == "" {
		return LocalModelSnapshot{
			State:           LocalModelDegraded,
			Endpoint:        tagsURL,
			Model:           cfg.Model,
			AvailableModels: models,
			Enabled:         true,
			Warmed:          false,
			CheckedAt:       now,
			ErrKind:         "model_missing",
		}
	}

	if cfg.RequireWarmup {
		return LocalModelSnapshot{
			State:           LocalModelWarming,
			Endpoint:        tagsURL,
			Model:           matchedModel,
			AvailableModels: models,
			Enabled:         true,
			Warmed:          false,
			CheckedAt:       now,
		}
	}

	return LocalModelSnapshot{
		State:           LocalModelReady,
		Endpoint:        tagsURL,
		Model:           matchedModel,
		AvailableModels: models,
		Enabled:         true,
		Warmed:          true,
		CheckedAt:       now,
	}
}

func matchOllamaModel(want string, available []string) string {
	want = strings.TrimSpace(want)
	if want == "" {
		return ""
	}
	wantLower := strings.ToLower(want)
	hasExplicitTag := strings.Contains(wantLower, ":")
	for _, m := range available {
		if strings.EqualFold(m, want) {
			return m
		}
	}
	if !hasExplicitTag {
		wantLatest := wantLower + ":latest"
		for _, m := range available {
			if strings.ToLower(m) == wantLatest {
				return m
			}
		}
	}
	return ""
}

// CanRequestLocalModel is a pure, side-effect-free readiness predicate.
// Returning false guarantees no C3 request body is constructed and no outbound
// HTTP call is issued.
func CanRequestLocalModel(snapshot LocalModelSnapshot) bool {
	if snapshot.State != LocalModelReady {
		return false
	}
	if strings.TrimSpace(snapshot.Model) == "" {
		return false
	}
	if !snapshot.Enabled {
		return false
	}
	if !snapshot.Warmed {
		return false
	}
	return true
}

// LocalRequestGate coordinates asynchronous C1 background probing, zero-request
// readiness gating on the typing hot path, warm-up tracking, circuit-breaker
// exponential backoff, and clean shutdown.
type LocalRequestGate struct {
	mu       sync.RWMutex
	cfg      LocalProbeConfig
	snapshot LocalModelSnapshot
	probing  bool
	closed   bool
	baseCtx  context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewLocalRequestGate creates a concurrency-safe gate starting in LocalModelUndetected.
// It performs ZERO synchronous network I/O during construction.
func NewLocalRequestGate(cfg LocalProbeConfig) *LocalRequestGate {
	norm := cfg.normalized()
	endpoint := strings.TrimSpace(norm.Endpoint)
	if resolved, err := ResolveLocalTagsURL(endpoint); err == nil {
		endpoint = resolved
	}
	baseCtx, cancel := context.WithCancel(context.Background())
	return &LocalRequestGate{
		cfg: norm,
		snapshot: LocalModelSnapshot{
			State:    LocalModelUndetected,
			Endpoint: endpoint,
			Model:    norm.Model,
			Enabled:  true,
			Warmed:   !norm.RequireWarmup,
		},
		baseCtx: baseCtx,
		cancel:  cancel,
	}
}

// Snapshot returns an immutable copy of the current readiness snapshot.
func (g *LocalRequestGate) Snapshot() LocalModelSnapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.snapshot.clone()
}

// Config returns the normalized bounded LocalProbeConfig.
func (g *LocalRequestGate) Config() LocalProbeConfig {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cfg
}

// SetEnabled toggles user-level C3 local candidate authorization without blocking.
func (g *LocalRequestGate) SetEnabled(enabled bool) {
	g.mu.Lock()
	g.snapshot.Enabled = enabled
	g.mu.Unlock()
}

// CanRequest reports whether a local C3 inference request may proceed right now.
// It is lock-bounded, never performs I/O, and returns false for every state != ready.
func (g *LocalRequestGate) CanRequest() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.closed {
		return false
	}
	return CanRequestLocalModel(g.snapshot)
}

// CanRequestWithContext additionally checks that ctx has not been cancelled.
func (g *LocalRequestGate) CanRequestWithContext(ctx context.Context) bool {
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	return g.CanRequest()
}

// ProbeAsync triggers a non-blocking background /api/tags probe if the gate is open,
// no probe is currently running, and the cooldown/circuit-breaker backoff window has elapsed.
// Returns true if a background probe was launched.
func (g *LocalRequestGate) ProbeAsync() bool {
	return g.triggerProbe(false)
}

// ForceProbeAsync triggers a non-blocking background /api/tags probe immediately,
// bypassing cooldown/backoff (for explicit user retry). Returns false if closed or
// a probe is already in flight.
func (g *LocalRequestGate) ForceProbeAsync() bool {
	return g.triggerProbe(true)
}

func (g *LocalRequestGate) triggerProbe(force bool) bool {
	g.mu.Lock()
	if g.closed || g.probing {
		g.mu.Unlock()
		return false
	}
	now := g.cfg.Now().UTC()
	if !force && !g.snapshot.NextProbeAt.IsZero() && now.Before(g.snapshot.NextProbeAt) {
		g.mu.Unlock()
		return false
	}
	g.probing = true
	cfg := g.cfg
	baseCtx := g.baseCtx
	g.wg.Add(1)
	g.mu.Unlock()

	go func() {
		defer g.wg.Done()
		result := ProbeLocalModel(baseCtx, cfg)
		g.applyProbeResult(result)
	}()
	return true
}

func (g *LocalRequestGate) applyProbeResult(result LocalModelSnapshot) {
	g.mu.Lock()
	g.probing = false
	if g.closed {
		g.mu.Unlock()
		return
	}

	now := result.CheckedAt
	if now.IsZero() {
		now = g.cfg.Now().UTC()
	}
	enabled := g.snapshot.Enabled
	prevFailures := g.snapshot.ConsecutiveFailures

	result.Enabled = enabled
	switch result.State {
	case LocalModelReady, LocalModelWarming:
		result.ConsecutiveFailures = 0
		result.NextProbeAt = now.Add(g.cfg.ReprobeCooldown)
	default:
		failures := prevFailures + 1
		result.ConsecutiveFailures = failures
		result.Warmed = false
		backoff := g.backoffDuration(failures)
		result.NextProbeAt = now.Add(backoff)
	}

	g.snapshot = result.clone()
	snap := g.snapshot.clone()
	cb := g.cfg.OnStateChange
	g.mu.Unlock()

	if cb != nil {
		cb(snap)
	}
}

func (g *LocalRequestGate) backoffDuration(failures int) time.Duration {
	if failures <= 1 {
		return g.cfg.ReprobeCooldown
	}
	shift := failures - 1
	if shift > 10 {
		shift = 10
	}
	d := g.cfg.ReprobeCooldown * time.Duration(1<<shift)
	if d <= 0 || d > g.cfg.MaxBackoff {
		return g.cfg.MaxBackoff
	}
	return d
}

// StartWarmup transitions a ready/warming gate into LocalModelWarming while warm-up runs.
func (g *LocalRequestGate) StartWarmup() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	if g.snapshot.State != LocalModelWarming && g.snapshot.State != LocalModelReady {
		return false
	}
	g.snapshot.State = LocalModelWarming
	g.snapshot.Warmed = false
	return true
}

// CompleteWarmup finishes a warm-up attempt. On failure, the gate closes immediately
// into LocalModelOffline with circuit-breaker backoff so subsequent keystrokes issue
// zero outbound requests until an explicit re-probe succeeds.
func (g *LocalRequestGate) CompleteWarmup(ok bool, errKind string) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	now := g.cfg.Now().UTC()
	if ok && strings.TrimSpace(g.snapshot.Model) != "" {
		g.snapshot.State = LocalModelReady
		g.snapshot.Warmed = true
		g.snapshot.ErrKind = ""
		g.snapshot.ConsecutiveFailures = 0
		g.snapshot.CheckedAt = now
		g.snapshot.NextProbeAt = now.Add(g.cfg.ReprobeCooldown)
	} else {
		g.snapshot.ConsecutiveFailures++
		g.snapshot.State = LocalModelOffline
		g.snapshot.Warmed = false
		if strings.TrimSpace(errKind) == "" {
			errKind = "warmup_failed"
		}
		g.snapshot.ErrKind = errKind
		g.snapshot.CheckedAt = now
		g.snapshot.NextProbeAt = now.Add(g.backoffDuration(g.snapshot.ConsecutiveFailures))
	}
	snap := g.snapshot.clone()
	cb := g.cfg.OnStateChange
	g.mu.Unlock()

	if cb != nil {
		cb(snap)
	}
}

// RecordRequestSuccess resets consecutive failure tracking after a successful local request.
func (g *LocalRequestGate) RecordRequestSuccess() {
	g.mu.Lock()
	if !g.closed {
		g.snapshot.ConsecutiveFailures = 0
	}
	g.mu.Unlock()
}

// RecordRequestFailure records a local inference error and trips the circuit breaker
// once ConsecutiveFailures reaches BreakerThreshold, blocking further outbound requests
// until backoff elapses and a probe recovers readiness.
func (g *LocalRequestGate) RecordRequestFailure(errKind string) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	now := g.cfg.Now().UTC()
	g.snapshot.ConsecutiveFailures++
	kind := strings.TrimSpace(errKind)
	if kind == "" {
		kind = "request_failed"
	}
	g.snapshot.ErrKind = kind
	tripped := g.snapshot.ConsecutiveFailures >= g.cfg.BreakerThreshold
	if tripped {
		if kind == "timeout" || kind == "unreachable" {
			g.snapshot.State = LocalModelOffline
		} else {
			g.snapshot.State = LocalModelDegraded
		}
		if g.cfg.RequireWarmup {
			g.snapshot.Warmed = false
		}
		g.snapshot.CheckedAt = now
		g.snapshot.NextProbeAt = now.Add(g.backoffDuration(g.snapshot.ConsecutiveFailures))
	}
	snap := g.snapshot.clone()
	cb := g.cfg.OnStateChange
	g.mu.Unlock()

	if tripped && cb != nil {
		cb(snap)
	}
}

// ExecuteIfReady runs fn with a bounded RequestTimeout (<= 4s) ONLY if the gate is ready.
// When not ready, fn is NEVER called (guaranteeing zero outbound requests) and
// (false, ErrLocalModelNotReady) is returned.
func (g *LocalRequestGate) ExecuteIfReady(
	ctx context.Context,
	fn func(reqCtx context.Context, snap LocalModelSnapshot) error,
) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	g.mu.RLock()
	closed := g.closed
	snap := g.snapshot.clone()
	reqTimeout := g.cfg.RequestTimeout
	g.mu.RUnlock()

	if closed || !CanRequestLocalModel(snap) {
		return false, ErrLocalModelNotReady
	}
	if fn == nil {
		return false, nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, reqTimeout)
	defer cancel()

	err := fn(reqCtx, snap)
	if err != nil {
		errKind := "request_failed"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			errKind = "timeout"
		}
		g.RecordRequestFailure(errKind)
		return true, err
	}
	g.RecordRequestSuccess()
	return true, nil
}

// Close cancels any in-flight background probe, prevents new probes or requests,
// and waits for all background goroutines to exit cleanly. Safe to call multiple times.
func (g *LocalRequestGate) Close() error {
	g.mu.Lock()
	if !g.closed {
		g.closed = true
		g.cancel()
	}
	g.mu.Unlock()
	g.wg.Wait()
	return nil
}
