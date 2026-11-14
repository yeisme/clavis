package engine

import (
	"strings"
	"testing"
)

// rebuild returns the source (equal+delete) and result (equal+insert) a
// diff describes; every diff must reproduce both texts exactly.
func rebuild(d TextDiff) (src, out string) {
	var a, b strings.Builder
	for _, s := range d.Spans {
		switch s.Op {
		case DiffEqual:
			a.WriteString(s.Text)
			b.WriteString(s.Text)
		case DiffDelete:
			a.WriteString(s.Text)
		case DiffInsert:
			b.WriteString(s.Text)
		}
	}
	return a.String(), b.String()
}

func TestDiffTextReconstructsBothSides(t *testing.T) {
	cases := []struct{ src, out string }{
		{"", ""},
		{"same", "same"},
		{"", "new text"},
		{"old text", ""},
		{"The quick brown fox jumps.", "The quick red fox leaped."},
		{"这段代码的作用是读取配置文件。", "这段代码负责读取并校验配置文件。"},
		{"I has a apple, its tasty", "I have an apple; it's tasty."},
		{"中文 mixed with English 单词", "中文混合 English 词语"},
	}
	for _, tc := range cases {
		d, ok := DiffText(tc.src, tc.out)
		if !ok {
			t.Fatalf("DiffText(%q, %q) refused", tc.src, tc.out)
		}
		src, out := rebuild(d)
		if src != tc.src || out != tc.out {
			t.Fatalf("DiffText(%q, %q) rebuilds (%q, %q)", tc.src, tc.out, src, out)
		}
		for i := 1; i < len(d.Spans); i++ {
			if d.Spans[i].Op == d.Spans[i-1].Op {
				t.Fatalf("adjacent spans share op %d: %+v", d.Spans[i].Op, d.Spans)
			}
		}
	}
}

func TestDiffTextGranularityAndCounts(t *testing.T) {
	// English: whole words change, not letters inside them.
	d, _ := DiffText("The quick brown fox", "The quick red fox")
	want := []DiffSpan{{DiffEqual, "The quick "}, {DiffDelete, "brown"}, {DiffInsert, "red"}, {DiffEqual, " fox"}}
	if len(d.Spans) != len(want) {
		t.Fatalf("spans = %+v", d.Spans)
	}
	for i := range want {
		if d.Spans[i] != want[i] {
			t.Fatalf("span %d = %+v, want %+v", i, d.Spans[i], want[i])
		}
	}
	if d.Deleted != 1 || d.Inserted != 1 {
		t.Fatalf("counts = -%d +%d", d.Deleted, d.Inserted)
	}

	// Chinese: per character.
	d, _ = DiffText("作用是读取", "负责读取")
	src, out := rebuild(d)
	if src != "作用是读取" || out != "负责读取" || d.Deleted != 3 || d.Inserted != 2 {
		t.Fatalf("CJK diff = %+v (-%d +%d)", d.Spans, d.Deleted, d.Inserted)
	}
}

func TestDiffTextSimilaritySeparatesEditsFromTranslations(t *testing.T) {
	edit, _ := DiffText("这段代码的作用是读取配置文件，然后校验字段。", "这段代码的作用是读取配置文件，并校验所有字段。")
	if edit.Similarity < 0.7 {
		t.Fatalf("a light polish scored similarity %.2f", edit.Similarity)
	}
	tr, _ := DiffText("这段代码的作用是读取配置文件。", "This code reads the configuration file.")
	if tr.Similarity > 0.2 {
		t.Fatalf("a translation scored similarity %.2f", tr.Similarity)
	}
	if same, _ := DiffText("abc", "abc"); same.Similarity != 1 || same.Deleted+same.Inserted != 0 {
		t.Fatalf("identical texts = %+v", same)
	}
}

func TestDiffTextRefusesHugeInputs(t *testing.T) {
	a := strings.Repeat("甲", 2000)
	b := strings.Repeat("乙", 2000)
	if _, ok := DiffText(a, b); ok {
		t.Fatal("a 2000×2000 rewrite should not be diffed on the UI thread")
	}
	// A huge but mostly shared text still diffs: prefix/suffix trimming.
	c := strings.Repeat("同", 5000)
	if _, ok := DiffText(c+"旧"+c, c+"新"+c); !ok {
		t.Fatal("trimming the common prefix/suffix should make this cheap")
	}
}

// TestDiffTextMergesIslands: a lone shared rune or space between two edits
// folds into them, so a rewritten phrase reads as one change.
func TestDiffTextMergesIslands(t *testing.T) {
	d, _ := DiffText("The brown fox", "The red cat")
	want := []DiffSpan{{DiffEqual, "The "}, {DiffDelete, "brown fox"}, {DiffInsert, "red cat"}}
	if len(d.Spans) != len(want) {
		t.Fatalf("spans = %+v, want %+v", d.Spans, want)
	}
	for i := range want {
		if d.Spans[i] != want[i] {
			t.Fatalf("span %d = %+v, want %+v", i, d.Spans[i], want[i])
		}
	}

	d, _ = DiffText("甲乙丙丁戊", "甲子丙寅戊")
	src, out := rebuild(d)
	if src != "甲乙丙丁戊" || out != "甲子丙寅戊" {
		t.Fatalf("rebuild = %q, %q", src, out)
	}
	if len(d.Spans) != 4 { // 甲 | -乙丙丁 | +子丙寅 | 戊
		t.Fatalf("CJK island not merged: %+v", d.Spans)
	}

	// A kept word is structure, not coincidence: it stays unchanged, so the
	// review shows exactly what the rewrite touched.
	d, _ = DiffText("然后校验字段并写回", "并校验所有字段后原子写回")
	var kept []string
	for _, s := range d.Spans {
		if s.Op == DiffEqual {
			kept = append(kept, s.Text)
		}
	}
	if strings.Join(kept, "|") != "校验|字段|写回" {
		t.Fatalf("kept runs = %q, want 校验|字段|写回 (spans %+v)", kept, d.Spans)
	}
	// Punctuation next to a space is not blank: "Hello, world" keeps ", ".
	d, _ = DiffText("Hello, world", "Hi, everyone")
	if len(d.Spans) != 5 || d.Spans[2] != (DiffSpan{DiffEqual, ", "}) {
		t.Fatalf("spans = %+v", d.Spans)
	}
}
