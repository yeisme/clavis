// Package jsonschema validates the conservative JSON Schema subset used by
// Clavis core packages. It has no dependencies on providers or UI packages.
package jsonschema

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"
)

// Validate checks value against schema.
func Validate(value any, schema map[string]any) error {
	return validate(value, schema, "$")
}

// ValidateDefinition checks that schema uses a supported schema definition.
func ValidateDefinition(schema map[string]any) error {
	return validateDefinition(schema, "$")
}

// NormalizeJSONNumbers converts decoder numbers to exact Go numeric values,
// retaining int64 precision and representing larger values with big numbers.
func NormalizeJSONNumbers(value any) any {
	switch v := value.(type) {
	case json.Number:
		if integer, ok := exactJSONInteger(v); ok {
			if integer.IsInt64() {
				return integer.Int64()
			}
			return integer
		}
		if rational, ok := numericRat(v); ok {
			return rational
		}
		return v.String()
	case map[string]any:
		for key, child := range v {
			v[key] = NormalizeJSONNumbers(child)
		}
		return v
	case []any:
		for i, child := range v {
			v[i] = NormalizeJSONNumbers(child)
		}
		return v
	default:
		return value
	}
}

func validate(value any, schema map[string]any, path string) error {
	if err := validateDefinition(schema, path); err != nil {
		return err
	}
	if raw, ok := schema["enum"]; ok {
		values, _ := raw.([]any)
		matched := false
		for _, candidate := range values {
			if jsonValuesEqual(value, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s is not one of enum values", path)
		}
	}
	typ, _ := schema["type"].(string)
	switch typ {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be object", path)
		}
		required, _ := schema["required"].([]any)
		for _, raw := range required {
			name, ok := raw.(string)
			if !ok || strings.TrimSpace(name) == "" {
				return fmt.Errorf("%s required must contain only property names", path)
			}
			if _, exists := obj[name]; !exists {
				return fmt.Errorf("%s.%s is required", path, name)
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		for name, raw := range properties {
			if child, exists := obj[name]; exists {
				childSchema, ok := raw.(map[string]any)
				if !ok {
					return fmt.Errorf("%s.properties.%s must be schema object", path, name)
				}
				if err := validate(child, childSchema, path+"."+name); err != nil {
					return err
				}
			}
		}
	case "array":
		values, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be array", path)
		}
		items, ok := schema["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s array schema requires items", path)
		}
		for i, item := range values {
			if err := validate(item, items, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be string", path)
		}
	case "number":
		if !isJSONNumber(value) {
			return fmt.Errorf("%s must be number", path)
		}
	case "integer":
		if !isJSONInteger(value) {
			return fmt.Errorf("%s must be integer", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be boolean", path)
		}
	case "null":
		if value != nil {
			return fmt.Errorf("%s must be null", path)
		}
	case "":
	default:
		return fmt.Errorf("%s uses unsupported schema type %q", path, typ)
	}
	return nil
}

func jsonValuesEqual(left, right any) bool {
	if reflect.DeepEqual(left, right) {
		return true
	}
	leftNumber, leftOK := numericRat(left)
	rightNumber, rightOK := numericRat(right)
	return leftOK && rightOK && leftNumber.Cmp(rightNumber) == 0
}

func isJSONNumber(value any) bool {
	_, ok := numericRat(value)
	return ok
}

func isJSONInteger(value any) bool {
	number, ok := numericRat(value)
	return ok && number.IsInt()
}

func exactJSONInteger(number json.Number) (*big.Int, bool) {
	parsed, ok := numericRat(number)
	if !ok || !parsed.IsInt() {
		return nil, false
	}
	return new(big.Int).Quo(parsed.Num(), parsed.Denom()), true
}

// numericRat converts JSON numbers and Go numeric enum values to exact rational
// values. Go floats use their shortest decimal spelling so a schema enum float64
// (0.1) compares equal to JSON's decimal 0.1 rather than its binary expansion.
func numericRat(value any) (*big.Rat, bool) {
	var text string
	switch n := value.(type) {
	case json.Number:
		if len(n.String()) > 4096 {
			return nil, false
		}
		text = n.String()
	case float32:
		text = strconv.FormatFloat(float64(n), 'g', -1, 32)
	case float64:
		text = strconv.FormatFloat(n, 'g', -1, 64)
	default:
		rv := reflect.ValueOf(value)
		if !rv.IsValid() {
			return nil, false
		}
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return new(big.Rat).SetInt64(rv.Int()), true
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return new(big.Rat).SetUint64(rv.Uint()), true
		default:
			return nil, false
		}
	}
	rat, ok := new(big.Rat).SetString(text)
	return rat, ok
}

func validateDefinition(schema map[string]any, path string) error {
	if schema == nil {
		return fmt.Errorf("%s has no schema", path)
	}
	if raw, exists := schema["type"]; exists {
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
	if raw, exists := schema["required"]; exists {
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
	if raw, exists := schema["properties"]; exists {
		properties, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.properties must be an object", path)
		}
		for name, child := range properties {
			childSchema, ok := child.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.properties.%s must be schema object", path, name)
			}
			if err := validateDefinition(childSchema, path+"."+name); err != nil {
				return err
			}
		}
	}
	if raw, exists := schema["items"]; exists {
		items, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.items must be schema object", path)
		}
		if err := validateDefinition(items, path+"[]"); err != nil {
			return err
		}
	}
	if raw, exists := schema["enum"]; exists {
		values, ok := raw.([]any)
		if !ok || len(values) == 0 {
			return fmt.Errorf("%s.enum must be a non-empty array", path)
		}
	}
	return nil
}
