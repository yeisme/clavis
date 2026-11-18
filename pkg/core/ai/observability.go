package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
type RequestMetadata struct {
	PromptFingerprint string            `json:"promptFingerprint"`
	Provider          string            `json:"provider"`
	Model             string            `json:"model"`
	Diagnostics       RenderDiagnostics `json:"renderDiagnostics,omitempty"`
	StartedAt         time.Time         `json:"startedAt"`
	LatencyMS         int64             `json:"latencyMs,omitempty"`
	InputTokens       int               `json:"inputTokens,omitempty"`
	OutputTokens      int               `json:"outputTokens,omitempty"`
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
