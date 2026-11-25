package config

import (
	"fmt"
	"math"
	"strings"

	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/toml"
)

// ConfigFileKind identifies one of the declarative Clavis TOML files.
type ConfigFileKind string

const (
	FileMain       ConfigFileKind = "config.toml"
	FileLeader     ConfigFileKind = "leader.toml"
	FileSnippets   ConfigFileKind = "snippets.toml"
	FileGlossary   ConfigFileKind = "glossary.toml"
	FileIMERules   ConfigFileKind = "ime-rules.toml"
	FilePrompts    ConfigFileKind = "prompts.toml"
	FileComponents ConfigFileKind = "components.toml"
	FileWorkflows  ConfigFileKind = "workflows.toml"
	FileQuickJump  ConfigFileKind = "quickjump.toml"
	FileIdentities ConfigFileKind = "identities.toml"
)

// AllConfigFileKinds returns all config file kinds in deterministic order.
func AllConfigFileKinds() []ConfigFileKind {
	return []ConfigFileKind{
		FileMain,
		FileLeader,
		FileSnippets,
		FileGlossary,
		FileIMERules,
		FilePrompts,
		FileComponents,
		FileWorkflows,
	}
}

// DecodingError reports a schema or type mismatch when decoding TOML into config structs.
type DecodingError struct {
	KeyPath string
	Message string
}

func (e *DecodingError) Error() string {
	if e.KeyPath == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.KeyPath, e.Message)
}

// ClavisConfig holds all declarative Clavis configurations.
type ClavisConfig struct {
	Main       MainConfig       `json:"main"`
	Leader     LeaderConfig     `json:"leader"`
	Snippets   SnippetsConfig   `json:"snippets"`
	Glossary   GlossaryConfig   `json:"glossary"`
	IMERules   IMERulesConfig   `json:"imeRules"`
	Prompts    PromptsConfig    `json:"prompts"`
	Components ComponentsConfig `json:"components"`
	Workflows  WorkflowsConfig  `json:"workflows"`
	QuickJump  QuickJumpConfig  `json:"quickJump"`
	Identities IdentitiesConfig `json:"identities"`
}

// DefaultConfig returns the complete default ClavisConfig.
func DefaultConfig() ClavisConfig {
	return ClavisConfig{
		Main:       DefaultMainConfig(),
		Leader:     DefaultLeaderConfig(),
		Snippets:   DefaultSnippetsConfig(),
		Glossary:   DefaultGlossaryConfig(),
		IMERules:   DefaultIMERulesConfig(),
		Prompts:    DefaultPromptsConfig(),
		Components: DefaultComponentsConfig(),
		Workflows:  DefaultWorkflowsConfig(),
		QuickJump:  DefaultQuickJumpConfig(),
		Identities: DefaultIdentitiesConfig(),
	}
}

// NormalizeAnimationMode trims and lowercases a configured animations mode
// ("auto", "full", "reduced", "off"); the empty string is "auto".
func NormalizeAnimationMode(s string) string {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" {
		return string("auto")
	}
	return t
}

// defaultStageIntensity is the stage intensity used when a configured value
// cannot be interpreted (NaN) or when the key is absent.
const defaultStageIntensity = 0.7

// NormalizeStageIntensity clamps a configured stage_intensity into [0, 1].
// NaN has no ordering against the endpoints, so it falls back to the default
// 0.7; +Inf clamps to 1 and -Inf clamps to 0, the same as any finite value
// past either end of the range.
func NormalizeStageIntensity(v float64) float64 {
	switch {
	case math.IsNaN(v):
		return defaultStageIntensity
	case math.IsInf(v, 1), v > 1:
		return 1
	case math.IsInf(v, -1), v < 0:
		return 0
	default:
		return v
	}
}

func joinPath(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

func requiredString(tbl *toml.Table, key, path string) (string, error) {
	v, ok := tbl.Get(key)
	if !ok {
		return "", &DecodingError{KeyPath: joinPath(path, key), Message: i18n.T("decode.missing_string")}
	}
	if v.Kind != toml.KindString {
		return "", &DecodingError{KeyPath: joinPath(path, key), Message: i18n.T("decode.expected_string")}
	}
	return v.Str, nil
}

func optionalString(tbl *toml.Table, key, path string) (string, bool, error) {
	v, ok := tbl.Get(key)
	if !ok {
		return "", false, nil
	}
	if v.Kind != toml.KindString {
		return "", false, &DecodingError{KeyPath: joinPath(path, key), Message: i18n.T("decode.expected_string")}
	}
	return v.Str, true, nil
}

func optionalInt(tbl *toml.Table, key, path string) (int, bool, error) {
	v, ok := tbl.Get(key)
	if !ok {
		return 0, false, nil
	}
	if v.Kind != toml.KindInt {
		return 0, false, &DecodingError{KeyPath: joinPath(path, key), Message: i18n.T("decode.expected_integer")}
	}
	return int(v.Int), true, nil
}

func optionalFloat(tbl *toml.Table, key, path string) (float64, bool, error) {
	v, ok := tbl.Get(key)
	if !ok {
		return 0, false, nil
	}
	n, isNum := v.AsNumber()
	if !isNum {
		return 0, false, &DecodingError{KeyPath: joinPath(path, key), Message: i18n.T("decode.expected_number")}
	}
	return n, true, nil
}

func optionalBool(tbl *toml.Table, key, path string) (bool, bool, error) {
	v, ok := tbl.Get(key)
	if !ok {
		return false, false, nil
	}
	if v.Kind != toml.KindBool {
		return false, false, &DecodingError{KeyPath: joinPath(path, key), Message: i18n.T("decode.expected_boolean")}
	}
	return v.Bool, true, nil
}

func optionalStringArray(tbl *toml.Table, key, path string) ([]string, bool, error) {
	v, ok := tbl.Get(key)
	if !ok {
		return nil, false, nil
	}
	if v.Kind != toml.KindArray {
		return nil, false, &DecodingError{KeyPath: joinPath(path, key), Message: i18n.T("decode.expected_string_array")}
	}
	out := make([]string, 0, len(v.Array))
	for i, elem := range v.Array {
		if elem.Kind != toml.KindString {
			return nil, false, &DecodingError{
				KeyPath: fmt.Sprintf("%s[%d]", joinPath(path, key), i),
				Message: i18n.T("decode.expected_string_element"),
			}
		}
		out = append(out, elem.Str)
	}
	return out, true, nil
}

func optionalTable(tbl *toml.Table, key, path string) (*toml.Table, bool, error) {
	v, ok := tbl.Get(key)
	if !ok {
		return nil, false, nil
	}
	if v.Kind != toml.KindTable || v.Table == nil {
		return nil, false, &DecodingError{KeyPath: joinPath(path, key), Message: i18n.T("decode.expected_table")}
	}
	return v.Table, true, nil
}

func optionalTableArray(tbl *toml.Table, key, path string) ([]*toml.Table, bool, error) {
	v, ok := tbl.Get(key)
	if !ok {
		return nil, false, nil
	}
	if v.Kind != toml.KindArray {
		return nil, false, &DecodingError{KeyPath: joinPath(path, key), Message: i18n.T("decode.expected_table_array")}
	}
	out := make([]*toml.Table, 0, len(v.Array))
	for i, elem := range v.Array {
		if elem.Kind != toml.KindTable || elem.Table == nil {
			return nil, false, &DecodingError{
				KeyPath: fmt.Sprintf("%s[%d]", joinPath(path, key), i),
				Message: i18n.T("decode.expected_table_element"),
			}
		}
		out = append(out, elem.Table)
	}
	return out, true, nil
}

func stringsToTOMLArray(items []string) toml.Value {
	vals := make([]toml.Value, len(items))
	for i, s := range items {
		vals[i] = toml.StringVal(s)
	}
	return toml.ArrayVal(vals)
}
