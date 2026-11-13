package toml_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/toml"
)

func TestParseScalarsAndKeys(t *testing.T) {
	input := `
# Top-level comment
title = "Clavis" # inline comment
"dotted.key" = 'C:\Users\clavis'
'literal key' = "value\nwith\tnewline"
"" = "empty-key"
count = +42
negative = -1_000
hex_val = 0xDEAD_BEEF
oct_val = 0o755
bin_val = 0b1010_1100
pi = 3.141_59
exp_val = 1.5e-2
pos_inf = inf
neg_inf = -inf
enabled = true
disabled = false
`
	tbl, err := toml.Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertStr(t, tbl, "title", "Clavis")
	assertStr(t, tbl, "dotted.key", `C:\Users\clavis`)
	assertStr(t, tbl, "literal key", "value\nwith\tnewline")
	assertStr(t, tbl, "", "empty-key")
	assertInt(t, tbl, "count", 42)
	assertInt(t, tbl, "negative", -1000)
	assertInt(t, tbl, "hex_val", 0xDEADBEEF)
	assertInt(t, tbl, "oct_val", 0o755)
	assertInt(t, tbl, "bin_val", 0b10101100)
	assertFloat(t, tbl, "pi", 3.14159)
	assertFloat(t, tbl, "exp_val", 0.015)

	v, ok := tbl.Get("pos_inf")
	if !ok || !math.IsInf(v.Float, 1) {
		t.Fatalf("expected +Inf, got %+v", v)
	}
	v, ok = tbl.Get("neg_inf")
	if !ok || !math.IsInf(v.Float, -1) {
		t.Fatalf("expected -Inf, got %+v", v)
	}
	assertBool(t, tbl, "enabled", true)
	assertBool(t, tbl, "disabled", false)
}

func TestParseUnicodeEscapesAndNaN(t *testing.T) {
	input := `
letter = "\u0041"
emoji = "\U0001F600"
not_a_number = nan
`
	tbl, err := toml.Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertStr(t, tbl, "letter", "A")
	assertStr(t, tbl, "emoji", "😀")
	v, ok := tbl.Get("not_a_number")
	if !ok || v.Kind != toml.KindFloat || !math.IsNaN(v.Float) {
		t.Fatalf("expected NaN, got %+v", v)
	}
}

func TestParseMultilineStrings(t *testing.T) {
	input := `
basic = """
Line 1
Line 2 with "quotes" and ""two""\
    joined"""

literal = '''
Raw \n \t ''quotes''
Line 2'''
`
	tbl, err := toml.Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertStr(t, tbl, "basic", "Line 1\nLine 2 with \"quotes\" and \"\"two\"\"joined")
	assertStr(t, tbl, "literal", "Raw \\n \\t ''quotes''\nLine 2")
}

func TestParseArraysAndInlineTables(t *testing.T) {
	input := `
numbers = [
    1, # first
    2,
    3, # trailing comma
]
point = { x = 10, y = 20, meta = { label = "origin" } }
dotted.a.b = "nested"
dotted.a.c = 99
`
	tbl, err := toml.Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	nums, ok := tbl.Get("numbers")
	if !ok || len(nums.Array) != 3 || nums.Array[0].Int != 1 || nums.Array[2].Int != 3 {
		t.Fatalf("unexpected numbers: %+v", nums)
	}
	pt, _ := tbl.Get("point")
	ptTbl, _ := pt.AsTable()
	assertInt(t, ptTbl, "x", 10)
	metaVal, _ := ptTbl.Get("meta")
	metaTbl, _ := metaVal.AsTable()
	assertStr(t, metaTbl, "label", "origin")

	dottedVal, _ := tbl.Get("dotted")
	dottedTbl, _ := dottedVal.AsTable()
	aVal, _ := dottedTbl.Get("a")
	aTbl, _ := aVal.AsTable()
	assertStr(t, aTbl, "b", "nested")
	assertInt(t, aTbl, "c", 99)
}

func TestParseTablesAndArraysOfTables(t *testing.T) {
	input := `
[a.b]
val = 1

[a]
top = true

[[products]]
name = "Hammer"

[[products.variants]]
sku = "H-1"

[[products.variants]]
sku = "H-2"

[[products]]
name = "Nail"

[products.meta]
weight = 0.5
`
	tbl, err := toml.Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aVal, _ := tbl.Get("a")
	aTbl, _ := aVal.AsTable()
	assertBool(t, aTbl, "top", true)
	bVal, _ := aTbl.Get("b")
	bTbl, _ := bVal.AsTable()
	assertInt(t, bTbl, "val", 1)

	prodsVal, _ := tbl.Get("products")
	prods, _ := prodsVal.AsArray()
	if len(prods) != 2 {
		t.Fatalf("expected 2 products, got %d", len(prods))
	}
	assertStr(t, prods[0].Table, "name", "Hammer")
	vVal, _ := prods[0].Table.Get("variants")
	assertStr(t, vVal.Array[0].Table, "sku", "H-1")
	assertStr(t, vVal.Array[1].Table, "sku", "H-2")
	assertStr(t, prods[1].Table, "name", "Nail")
	mVal, _ := prods[1].Table.Get("meta")
	assertFloat(t, mVal.Table, "weight", 0.5)
}

func TestParseRejectsErrorsWithLineAndColumn(t *testing.T) {
	tests := []struct {
		name string
		src  string
		line int
		col  int
	}{
		{
			name: "duplicate key",
			src:  "a = 1\na = 2\n",
			line: 2,
			col:  1,
		},
		{
			name: "invalid escape",
			src:  "first = \"ok\"\nsecond = \"bad\\q\"\n",
			line: 2,
			col:  15,
		},
		{
			name: "reopen dotted table",
			src:  "a.b = 1\n[a]\nc = 2\n",
			line: 2,
			col:  1,
		},
		{
			name: "trailing comma inline table",
			src:  "t = { a = 1, }\n",
			line: 1,
			col:  0,
		},
		{
			name: "leading zero",
			src:  "n = 012\n",
			line: 1,
			col:  5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := toml.Parse(tc.src)
			if err == nil {
				t.Fatalf("expected error")
			}
			var pe *toml.ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("expected *toml.ParseError, got %T: %v", err, err)
			}
			if pe.Line != tc.line {
				t.Fatalf("expected line %d, got %d (msg=%s)", tc.line, pe.Line, pe.Message)
			}
			if tc.col > 0 && pe.Column != tc.col {
				t.Fatalf("expected column %d, got %d (msg=%s)", tc.col, pe.Column, pe.Message)
			}
		})
	}
}

func TestEncoderRoundTripAndDeterminism(t *testing.T) {
	doc := toml.NewTable()
	doc.Set("name", toml.StringVal("Clavis\nTest\t\"Quoted\""))
	doc.Set("version", toml.IntVal(2))
	doc.Set("ratio", toml.FloatVal(1.0))
	doc.Set("enabled", toml.BoolVal(true))
	doc.Set("tags", toml.ArrayVal([]toml.Value{toml.StringVal("mac"), toml.StringVal("cli")}))
	doc.Set("empty_table", toml.TableVal(toml.NewTable()))

	nested := toml.NewTable()
	nested.Set("special.key", toml.StringVal("escaped-key"))
	sub := toml.NewTable()
	sub.Set("flag", toml.BoolVal(false))
	nested.Set("sub", toml.TableVal(sub))
	doc.Set("nested", toml.TableVal(nested))

	node1 := toml.NewTable()
	node1.Set("key", toml.StringVal("w"))
	child := toml.NewTable()
	child.Set("key", toml.StringVal("h"))
	node1.Set("children", toml.ArrayVal([]toml.Value{toml.TableVal(child)}))

	node2 := toml.NewTable()
	node2.Set("key", toml.StringVal("c"))
	node2.Set("children", toml.ArrayVal(nil))

	doc.Set("nodes", toml.ArrayVal([]toml.Value{toml.TableVal(node1), toml.TableVal(node2)}))

	enc1 := toml.Encode(doc)
	enc2 := toml.Encode(doc)
	if enc1 != enc2 {
		t.Fatalf("encoder not deterministic:\n%s\n---\n%s", enc1, enc2)
	}
	decoded, err := toml.Parse(enc1)
	if err != nil {
		t.Fatalf("round-trip parse failed: %v\n%s", err, enc1)
	}
	if !decoded.Equal(doc) {
		t.Fatalf("round-trip mismatch:\nencoded:\n%s", enc1)
	}
}

func TestEncoderCRLFAndControlScalars(t *testing.T) {
	doc := toml.NewTable()
	doc.Set("crlf", toml.StringVal("line1\r\nline2"))
	doc.Set("controls", toml.StringVal("a\u0008b\u000Cc\u001Fd\u007Fe"))
	encoded := toml.Encode(doc)
	if !strings.Contains(encoded, `crlf = "line1\r\nline2"`) {
		t.Fatalf("expected escaped CRLF in:\n%s", encoded)
	}
	if !strings.Contains(encoded, `\u001F`) || !strings.Contains(encoded, `\u007F`) {
		t.Fatalf("expected control scalar escapes in:\n%s", encoded)
	}
	decoded, err := toml.Parse(encoded)
	if err != nil {
		t.Fatalf("round-trip failed: %v", err)
	}
	if !decoded.Equal(doc) {
		t.Fatalf("round-trip mismatch")
	}
}

func assertStr(t *testing.T, tbl *toml.Table, key, want string) {
	t.Helper()
	v, ok := tbl.Get(key)
	if !ok || v.Kind != toml.KindString || v.Str != want {
		t.Fatalf("key %q: got (%v, %+v), want %q", key, ok, v, want)
	}
}

func assertInt(t *testing.T, tbl *toml.Table, key string, want int64) {
	t.Helper()
	v, ok := tbl.Get(key)
	if !ok || v.Kind != toml.KindInt || v.Int != want {
		t.Fatalf("key %q: got (%v, %+v), want %d", key, ok, v, want)
	}
}

func assertFloat(t *testing.T, tbl *toml.Table, key string, want float64) {
	t.Helper()
	v, ok := tbl.Get(key)
	if !ok || v.Kind != toml.KindFloat || math.Abs(v.Float-want) > 1e-9 {
		t.Fatalf("key %q: got (%v, %+v), want %v", key, ok, v, want)
	}
}

func assertBool(t *testing.T, tbl *toml.Table, key string, want bool) {
	t.Helper()
	v, ok := tbl.Get(key)
	if !ok || v.Kind != toml.KindBool || v.Bool != want {
		t.Fatalf("key %q: got (%v, %+v), want %v", key, ok, v, want)
	}
}
