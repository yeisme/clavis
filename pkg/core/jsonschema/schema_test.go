package jsonschema

import (
	"strings"
	"testing"
)

func TestValidateNestedObjectArrayPaths(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{"type": "object", "properties": map[string]any{
					"name": map[string]any{"type": "string"},
				}},
			},
		},
	}
	err := Validate(map[string]any{"items": []any{map[string]any{"name": 3}}}, schema)
	if err == nil || !strings.Contains(err.Error(), "$.items[0].name") {
		t.Fatalf("error = %v, want nested array/object path", err)
	}
}

func TestValidateDefinitionRejectsInvalidSchema(t *testing.T) {
	err := ValidateDefinition(map[string]any{"type": "object", "properties": map[string]any{
		"value": map[string]any{"type": "not-supported"},
	}})
	if err == nil || !strings.Contains(err.Error(), "$.value") {
		t.Fatalf("error = %v, want invalid nested definition path", err)
	}
}

func TestValidateNilAgainstRequiredType(t *testing.T) {
	if err := Validate(nil, map[string]any{"type": "string"}); err == nil {
		t.Fatal("nil unexpectedly matched string schema")
	}
}
