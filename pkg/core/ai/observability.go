package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"reflect"
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
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("structured_output_invalid_json: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("structured_output_invalid_json: trailing data")
	}
	if err := validateSchema(value, schema, "$"); err != nil {
		return nil, fmt.Errorf("structured_output_schema_mismatch: %w", err)
	}
	return normalizeJSONNumbers(value), nil
}

func normalizeJSONNumbers(value any) any {
	switch v := value.(type) {
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		if integer, ok := exactJSONInteger(v); ok {
			return integer
		}
		if f, err := v.Float64(); err == nil {
			return f
		}
		return v.String()
	case map[string]any:
		for key, child := range v {
			v[key] = normalizeJSONNumbers(child)
		}
		return v
	case []any:
		for i, child := range v {
			v[i] = normalizeJSONNumbers(child)
		}
		return v
	default:
		return value
	}
}
func validateSchema(v any, s map[string]any, path string) error {
	if err := validateSchemaDefinition(s, path); err != nil {
		return err
	}
	if raw, ok := s["enum"]; ok {
		values, _ := raw.([]any)
		matched := false
		for _, candidate := range values {
			if jsonValuesEqual(v, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s is not one of enum values", path)
		}
	}
	typ, _ := s["type"].(string)
	switch typ {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be object", path)
		}
		required, _ := s["required"].([]any)
		for _, raw := range required {
			name, ok := raw.(string)
			if !ok || strings.TrimSpace(name) == "" {
				return fmt.Errorf("%s required must contain only property names", path)
			}
			if _, exists := obj[name]; !exists {
				return fmt.Errorf("%s.%s is required", path, name)
			}
		}
		properties, _ := s["properties"].(map[string]any)
		for name, raw := range properties {
			if child, exists := obj[name]; exists {
				childSchema, ok := raw.(map[string]any)
				if !ok {
					return fmt.Errorf("%s.properties.%s must be schema object", path, name)
				}
				if err := validateSchema(child, childSchema, path+"."+name); err != nil {
					return err
				}
			}
		}
	case "array":
		values, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s must be array", path)
		}
		items, ok := s["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s array schema requires items", path)
		}
		for i, item := range values {
			if err := validateSchema(item, items, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("%s must be string", path)
		}
	case "number":
		if !isJSONNumber(v) {
			return fmt.Errorf("%s must be number", path)
		}
	case "integer":
		if !isJSONInteger(v) {
			return fmt.Errorf("%s must be integer", path)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s must be boolean", path)
		}
	case "null":
		if v != nil {
			return fmt.Errorf("%s must be null", path)
		}
	case "":
	default:
		return fmt.Errorf("%s uses unsupported schema type %q", path, typ)
	}
	return nil
}

func jsonValuesEqual(left any, right any) bool {
	if reflect.DeepEqual(left, right) {
		return true
	}
	leftNumber, leftOK := numericBigFloat(left)
	rightNumber, rightOK := numericBigFloat(right)
	return leftOK && rightOK && leftNumber.Cmp(rightNumber) == 0
}

func isJSONNumber(value any) bool {
	_, ok := numericBigFloat(value)
	return ok
}

func isJSONInteger(value any) bool {
	number, ok := numericBigFloat(value)
	if !ok {
		return false
	}
	return number.IsInt()
}

func exactJSONInteger(number json.Number) (*big.Int, bool) {
	parsed, ok := numericBigFloat(number)
	if !ok || !parsed.IsInt() {
		return nil, false
	}
	integer, _ := parsed.Int(nil)
	return integer, integer != nil
}

func numericBigFloat(value any) (*big.Float, bool) {
	switch n := value.(type) {
	case json.Number:
		number, _, err := big.ParseFloat(n.String(), 10, 256, big.ToNearestEven)
		return number, err == nil
	case int, int8, int16, int32, int64:
		return new(big.Float).SetInt64(reflect.ValueOf(n).Int()), true
	case uint, uint8, uint16, uint32, uint64:
		return new(big.Float).SetUint64(reflect.ValueOf(n).Uint()), true
	case float32, float64:
		return big.NewFloat(reflect.ValueOf(n).Float()), true
	default:
		return nil, false
	}
}

func validateSchemaDefinition(s map[string]any, path string) error {
	if s == nil {
		return fmt.Errorf("%s has no schema", path)
	}
	if raw, exists := s["type"]; exists {
		typ, ok := raw.(string)
		if !ok {
			return fmt.Errorf("%s.type must be a string", path)
		}
		switch typ {
		case "", "object", "array", "string", "number", "integer", "boolean", "null":
		default:
			return fmt.Errorf("%s uses unsupported schema type %q", path, typ)
		}
	}
	if raw, exists := s["required"]; exists {
		required, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("%s.required must be an array", path)
		}
		for _, item := range required {
			if name, ok := item.(string); !ok || strings.TrimSpace(name) == "" {
				return fmt.Errorf("%s.required must contain only property names", path)
			}
		}
	}
	if raw, exists := s["properties"]; exists {
		properties, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.properties must be an object", path)
		}
		for name, child := range properties {
			childSchema, ok := child.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.properties.%s must be schema object", path, name)
			}
			if err := validateSchemaDefinition(childSchema, path+"."+name); err != nil {
				return err
			}
		}
	}
	if raw, exists := s["items"]; exists {
		items, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.items must be schema object", path)
		}
		if err := validateSchemaDefinition(items, path+"[]"); err != nil {
			return err
		}
	}
	if raw, exists := s["enum"]; exists {
		values, ok := raw.([]any)
		if !ok || len(values) == 0 {
			return fmt.Errorf("%s.enum must be a non-empty array", path)
		}
	}
	return nil
}

func (m RequestMetadata) Safe() RequestMetadata {
	m.PromptFingerprint = strings.TrimSpace(m.PromptFingerprint)
	return m
}
