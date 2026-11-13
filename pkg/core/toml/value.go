package toml

import (
	"math"
	"sort"
)

// Kind identifies the concrete TOML value variant.
type Kind uint8

const (
	KindString Kind = iota + 1
	KindInt
	KindFloat
	KindBool
	KindArray
	KindTable
)

// Value represents a TOML value.
type Value struct {
	Kind  Kind
	Str   string
	Int   int64
	Float float64
	Bool  bool
	Array []Value
	Table *Table
}

// Table represents a TOML table mapping string keys to Values.
type Table struct {
	Entries map[string]Value
}

// NewTable constructs an empty Table.
func NewTable() *Table {
	return &Table{Entries: make(map[string]Value)}
}

// StringVal constructs a string Value.
func StringVal(s string) Value {
	return Value{Kind: KindString, Str: s}
}

// IntVal constructs an integer Value.
func IntVal(i int64) Value {
	return Value{Kind: KindInt, Int: i}
}

// FloatVal constructs a float Value.
func FloatVal(f float64) Value {
	return Value{Kind: KindFloat, Float: f}
}

// BoolVal constructs a boolean Value.
func BoolVal(b bool) Value {
	return Value{Kind: KindBool, Bool: b}
}

// ArrayVal constructs an array Value.
func ArrayVal(items []Value) Value {
	copied := make([]Value, len(items))
	copy(copied, items)
	return Value{Kind: KindArray, Array: copied}
}

// TableVal constructs a table Value.
func TableVal(t *Table) Value {
	if t == nil {
		t = NewTable()
	}
	return Value{Kind: KindTable, Table: t}
}

// AsString returns the string value if Kind == KindString.
func (v Value) AsString() (string, bool) {
	if v.Kind == KindString {
		return v.Str, true
	}
	return "", false
}

// AsInt returns the int64 value if Kind == KindInt.
func (v Value) AsInt() (int64, bool) {
	if v.Kind == KindInt {
		return v.Int, true
	}
	return 0, false
}

// AsFloat returns the float64 value if Kind == KindFloat (or KindInt promoted to float64 if allowInt).
func (v Value) AsFloat() (float64, bool) {
	if v.Kind == KindFloat {
		return v.Float, true
	}
	return 0, false
}

// AsNumber returns float64 for either KindFloat or KindInt.
func (v Value) AsNumber() (float64, bool) {
	switch v.Kind {
	case KindFloat:
		return v.Float, true
	case KindInt:
		return float64(v.Int), true
	default:
		return 0, false
	}
}

// AsBool returns the bool value if Kind == KindBool.
func (v Value) AsBool() (bool, bool) {
	if v.Kind == KindBool {
		return v.Bool, true
	}
	return false, false
}

// AsArray returns the slice if Kind == KindArray.
func (v Value) AsArray() ([]Value, bool) {
	if v.Kind == KindArray {
		return v.Array, true
	}
	return nil, false
}

// AsTable returns the *Table if Kind == KindTable.
func (v Value) AsTable() (*Table, bool) {
	if v.Kind == KindTable && v.Table != nil {
		return v.Table, true
	}
	return nil, false
}

// Get retrieves a key from the table.
func (t *Table) Get(key string) (Value, bool) {
	if t == nil || t.Entries == nil {
		return Value{}, false
	}
	v, ok := t.Entries[key]
	return v, ok
}

// Set stores a key-value pair in the table.
func (t *Table) Set(key string, v Value) {
	if t.Entries == nil {
		t.Entries = make(map[string]Value)
	}
	t.Entries[key] = v
}

// SortedKeys returns the table keys in ascending lexicographical order.
func (t *Table) SortedKeys() []string {
	if t == nil || len(t.Entries) == 0 {
		return nil
	}
	keys := make([]string, 0, len(t.Entries))
	for k := range t.Entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ToAny converts the Table into a plain map[string]any.
func (t *Table) ToAny() map[string]any {
	if t == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(t.Entries))
	for k, v := range t.Entries {
		out[k] = v.ToAny()
	}
	return out
}

// ToAny converts a Value into standard Go types (string, int64, float64, bool, []any, map[string]any).
func (v Value) ToAny() any {
	switch v.Kind {
	case KindString:
		return v.Str
	case KindInt:
		return v.Int
	case KindFloat:
		return v.Float
	case KindBool:
		return v.Bool
	case KindArray:
		arr := make([]any, len(v.Array))
		for i, item := range v.Array {
			arr[i] = item.ToAny()
		}
		return arr
	case KindTable:
		return v.Table.ToAny()
	default:
		return nil
	}
}

// Equal reports deep structural equality between two Values (including NaN == NaN).
func (v Value) Equal(other Value) bool {
	if v.Kind != other.Kind {
		return false
	}
	switch v.Kind {
	case KindString:
		return v.Str == other.Str
	case KindInt:
		return v.Int == other.Int
	case KindFloat:
		if math.IsNaN(v.Float) && math.IsNaN(other.Float) {
			return true
		}
		return v.Float == other.Float
	case KindBool:
		return v.Bool == other.Bool
	case KindArray:
		if len(v.Array) != len(other.Array) {
			return false
		}
		for i := range v.Array {
			if !v.Array[i].Equal(other.Array[i]) {
				return false
			}
		}
		return true
	case KindTable:
		return v.Table.Equal(other.Table)
	default:
		return true
	}
}

// Equal reports deep structural equality between two Tables.
func (t *Table) Equal(other *Table) bool {
	if t == other {
		return true
	}
	if t == nil || other == nil {
		return (t == nil || len(t.Entries) == 0) && (other == nil || len(other.Entries) == 0)
	}
	if len(t.Entries) != len(other.Entries) {
		return false
	}
	for k, v := range t.Entries {
		ov, ok := other.Entries[k]
		if !ok || !v.Equal(ov) {
			return false
		}
	}
	return true
}
