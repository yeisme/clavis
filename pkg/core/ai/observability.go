package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
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
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("structured_output_invalid_json: %w", err)
	}
	if err := validateSchema(value, schema, "$"); err != nil {
		return nil, fmt.Errorf("structured_output_schema_mismatch: %w", err)
	}
	return value, nil
}
func validateSchema(v any, s map[string]any, path string) error {
	typ, _ := s["type"].(string)
	switch typ {
	case "object":
		if _, ok := v.(map[string]any); !ok {
			return fmt.Errorf("%s must be object", path)
		}
	case "array":
		if _, ok := v.([]any); !ok {
			return fmt.Errorf("%s must be array", path)
		}
	case "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("%s must be string", path)
		}
	case "number":
		if _, ok := v.(float64); !ok {
			return fmt.Errorf("%s must be number", path)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s must be boolean", path)
		}
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	req, _ := s["required"].([]any)
	for _, r := range req {
		n, _ := r.(string)
		if _, exists := obj[n]; !exists {
			return fmt.Errorf("%s.%s is required", path, n)
		}
	}
	props, _ := s["properties"].(map[string]any)
	for n, rawp := range props {
		if child, exists := obj[n]; exists {
			if p, ok := rawp.(map[string]any); ok {
				if err := validateSchema(child, p, path+"."+n); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (m RequestMetadata) Safe() RequestMetadata {
	m.PromptFingerprint = strings.TrimSpace(m.PromptFingerprint)
	return m
}
