package toml

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Encode deterministically serializes a *Table into TOML text with sorted keys.
func Encode(table *Table) string {
	if table == nil || len(table.Entries) == 0 {
		return ""
	}
	var lines []string
	encodeTableBody(table, nil, &lines, false)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// EncodeValue formats a single TOML Value inline.
func EncodeValue(v Value) string {
	return formatInlineValue(v)
}

func encodeTableBody(table *Table, path []string, lines *[]string, emitHeader bool) {
	if table == nil {
		return
	}
	keys := table.SortedKeys()

	type kv struct {
		key string
		val Value
	}
	type subTbl struct {
		key string
		tbl *Table
	}
	type aot struct {
		key    string
		tables []*Table
	}

	var scalars []kv
	var subtables []subTbl
	var arraysOfTables []aot

	for _, k := range keys {
		v := table.Entries[k]
		switch v.Kind {
		case KindTable:
			subtables = append(subtables, subTbl{key: k, tbl: v.Table})
		case KindArray:
			if isArrayOfTables(v.Array) {
				tbls := make([]*Table, len(v.Array))
				for i, item := range v.Array {
					tbls[i] = item.Table
				}
				arraysOfTables = append(arraysOfTables, aot{key: k, tables: tbls})
			} else {
				scalars = append(scalars, kv{key: k, val: v})
			}
		default:
			scalars = append(scalars, kv{key: k, val: v})
		}
	}

	if emitHeader && len(path) > 0 && (len(scalars) > 0 || (len(subtables) == 0 && len(arraysOfTables) == 0)) {
		if len(*lines) > 0 {
			*lines = append(*lines, "")
		}
		*lines = append(*lines, "["+formatKeyPath(path)+"]")
	}

	for _, item := range scalars {
		*lines = append(*lines, formatKey(item.key)+" = "+formatInlineValue(item.val))
	}

	for _, sub := range subtables {
		nextPath := append(append([]string(nil), path...), sub.key)
		encodeTableBody(sub.tbl, nextPath, lines, true)
	}

	for _, arr := range arraysOfTables {
		childPath := append(append([]string(nil), path...), arr.key)
		header := "[[" + formatKeyPath(childPath) + "]]"
		for _, elem := range arr.tables {
			if len(*lines) > 0 {
				*lines = append(*lines, "")
			}
			*lines = append(*lines, header)
			encodeTableBody(elem, childPath, lines, false)
		}
	}
}

func isArrayOfTables(items []Value) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if item.Kind != KindTable || item.Table == nil {
			return false
		}
	}
	return true
}

func formatKeyPath(path []string) string {
	parts := make([]string, len(path))
	for i, k := range path {
		parts[i] = formatKey(k)
	}
	return strings.Join(parts, ".")
}

func formatKey(key string) string {
	if key == "" {
		return `""`
	}
	for _, r := range key {
		if !isBareKeyRune(r) {
			return formatBasicString(key)
		}
	}
	return key
}

func formatInlineValue(v Value) string {
	switch v.Kind {
	case KindString:
		return formatBasicString(v.Str)
	case KindInt:
		return strconv.FormatInt(v.Int, 10)
	case KindFloat:
		return formatFloat(v.Float)
	case KindBool:
		if v.Bool {
			return "true"
		}
		return "false"
	case KindArray:
		parts := make([]string, len(v.Array))
		for i, item := range v.Array {
			parts[i] = formatInlineValue(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case KindTable:
		if v.Table == nil || len(v.Table.Entries) == 0 {
			return "{}"
		}
		keys := v.Table.SortedKeys()
		pairs := make([]string, len(keys))
		for i, k := range keys {
			pairs[i] = formatKey(k) + " = " + formatInlineValue(v.Table.Entries[k])
		}
		return "{ " + strings.Join(pairs, ", ") + " }"
	default:
		return `""`
	}
}

func formatFloat(f float64) string {
	if math.IsNaN(f) {
		return "nan"
	}
	if math.IsInf(f, 1) {
		return "inf"
	}
	if math.IsInf(f, -1) {
		return "-inf"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

func formatBasicString(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + 2)
	sb.WriteByte('"')
	for i := 0; i < len(s); {
		r, width := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && width == 1 {
			// Replace invalid UTF-8 byte safely with U+FFFD
			sb.WriteRune(utf8.RuneError)
			i++
			continue
		}
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\b':
			sb.WriteString(`\b`)
		case '\t':
			sb.WriteString(`\t`)
		case '\n':
			sb.WriteString(`\n`)
		case '\f':
			sb.WriteString(`\f`)
		case '\r':
			sb.WriteString(`\r`)
		default:
			if (r >= 0x00 && r < 0x20) || r == 0x7F {
				fmt.Fprintf(&sb, `\u%04X`, r)
			} else {
				sb.WriteRune(r)
			}
		}
		i += width
	}
	sb.WriteByte('"')
	return sb.String()
}
