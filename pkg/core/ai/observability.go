package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/jsonschema"
)

// RenderDiagnostics is safe to persist: it contains classifications and counts,
// never rendered prompt contents or credentials.
type RenderDiagnostics struct {
	TemplateRef  string   `json:"templateRef,omitempty"`
	Fingerprint  string   `json:"fingerprint,omitempty"`
	Missing      []string `json:"missing,omitempty"`
	WarningCount int      `json:"warningCount,omitempty"`
}

// ProviderUsage is the token accounting a provider reports for one request.
// It is independent of pricing; callers may apply a price table later without
// rewriting this historical accounting. Reported distinguishes a real zero
// from a response that omitted usage entirely.
type ProviderUsage struct {
	InputTokens  int
	OutputTokens int
	CachedTokens int
	Reported     bool
}

// UsageRecord is the safe, pricing-independent accounting event emitted after
// an AI request. At is the request start time; callers persist it asynchronously.
type UsageRecord struct {
	At                               time.Time
	Profile, Provider, Model, Action string
	Usage                            ProviderUsage
	Latency                          time.Duration
}

// UsageRecorder receives completed request accounting. Implementations must
// return quickly; the AI client invokes this callback outside response parsing.
type UsageRecorder interface {
	RecordAIUsage(UsageRecord)
}

// RequestMetadata records safe request facts and the provider's optional usage.
type RequestMetadata struct {
	PromptFingerprint string            `json:"promptFingerprint"`
	Provider          string            `json:"provider"`
	Model             string            `json:"model"`
	Diagnostics       RenderDiagnostics `json:"renderDiagnostics,omitempty"`
	StartedAt         time.Time         `json:"startedAt"`
	LatencyMS         int64             `json:"latencyMs,omitempty"`
	InputTokens       int               `json:"inputTokens,omitempty"`
	OutputTokens      int               `json:"outputTokens,omitempty"`
	CachedTokens      int               `json:"cachedTokens,omitempty"`
	UsageReported     bool              `json:"usageReported,omitempty"`
}

// ParseProviderUsage extracts common OpenAI-compatible usage fields. Presence
// of a JSON usage object sets Reported even when all reported counts are zero.
// Missing or malformed usage returns the zero, unavailable value.
func ParseProviderUsage(payload []byte) ProviderUsage {
	var envelope struct {
		Usage *struct {
			PromptTokens     *int `json:"prompt_tokens"`
			CompletionTokens *int `json:"completion_tokens"`
			InputTokens      *int `json:"input_tokens"`
			OutputTokens     *int `json:"output_tokens"`
			CachedTokens     *int `json:"cached_tokens"`
			CacheReadTokens  *int `json:"cache_read_input_tokens"`
			PromptDetails    struct {
				CachedTokens *int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			InputDetails struct {
				CachedTokens *int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Usage == nil {
		return ProviderUsage{}
	}
	u := ProviderUsage{Reported: true}
	if envelope.Usage.InputTokens != nil {
		u.InputTokens = *envelope.Usage.InputTokens
	}
	if envelope.Usage.PromptTokens != nil {
		u.InputTokens = *envelope.Usage.PromptTokens
	}
	if envelope.Usage.OutputTokens != nil {
		u.OutputTokens = *envelope.Usage.OutputTokens
	}
	if envelope.Usage.CompletionTokens != nil {
		u.OutputTokens = *envelope.Usage.CompletionTokens
	}
	if envelope.Usage.CachedTokens != nil {
		u.CachedTokens = *envelope.Usage.CachedTokens
	}
	if envelope.Usage.CacheReadTokens != nil {
		u.CachedTokens = *envelope.Usage.CacheReadTokens
	}
	if envelope.Usage.PromptDetails.CachedTokens != nil {
		u.CachedTokens = *envelope.Usage.PromptDetails.CachedTokens
	}
	if envelope.Usage.InputDetails.CachedTokens != nil {
		u.CachedTokens = *envelope.Usage.InputDetails.CachedTokens
	}
	return u
}

// ParseProviderUsageResponse reads a completed non-streaming response and
// extracts only its usage object. It closes the body and never guesses usage.
func ParseProviderUsageResponse(resp *http.Response) (ProviderUsage, error) {
	if resp == nil || resp.Body == nil {
		return ProviderUsage{}, fmt.Errorf("nil provider response")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ProviderUsage{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ProviderUsage{}, fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	return ParseProviderUsage(body), nil
}

// Fingerprint returns a stable digest without retaining prompt text.
func Fingerprint(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// ParseStructuredOutput validates JSON syntax and a conservative JSON-schema subset.
// Callers must check the error before handing output to automation.
func ParseStructuredOutput(raw string, schema map[string]any) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("structured_output_invalid_json: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("structured_output_invalid_json: trailing data")
	}
	if err := jsonschema.Validate(value, schema); err != nil {
		return nil, fmt.Errorf("structured_output_schema_mismatch: %w", err)
	}
	return jsonschema.NormalizeJSONNumbers(value), nil
}

func (m RequestMetadata) Safe() RequestMetadata {
	m.PromptFingerprint = strings.TrimSpace(m.PromptFingerprint)
	return m
}
