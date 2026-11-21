package inputengine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/ime"
)

func TestDictionaryPriorityCustomLearnedStarterBuiltin(t *testing.T) {
	records := []CandidateRecord{
		{Pinyin: "ni hao", Word: "你好", Source: SourceBuiltin, Frequency: 999, OriginalOrdinal: 4},
		{Pinyin: "ni hao", Word: "你好", Source: SourceStarter, Frequency: 1, OriginalOrdinal: 3},
		{Pinyin: "ni hao", Word: "你好", Source: SourceLearned, Frequency: 1, OriginalOrdinal: 2},
		{Pinyin: "ni hao", Word: "你好", Source: SourceCustom, Frequency: 1, OriginalOrdinal: 1},
		{Pinyin: "ni hao", Word: "另一读音", Source: SourceCustom, Frequency: 1, OriginalOrdinal: 0},
	}
	got := ResolveCandidate(records)
	if len(got) != 2 {
		t.Fatalf("custom priority/dedup = %+v", got)
	}
	var customWords []string
	for _, record := range got {
		if record.Source != SourceCustom {
			t.Fatalf("lower-priority duplicate survived: %+v", got)
		}
		customWords = append(customWords, record.Word)
	}
	if !reflect.DeepEqual(customWords, []string{"另一读音", "你好"}) {
		t.Fatalf("same-source byte/ordinal order = %+v", customWords)
	}
}

func TestDictionaryTieBreakPinnedFrequencyOrdinalBytes(t *testing.T) {
	base := []CandidateRecord{
		{Pinyin: "a", Word: "乙", Source: SourceLearned, Frequency: 20, OriginalOrdinal: 9},
		{Pinyin: "a", Word: "甲", Source: SourceLearned, Frequency: 20, OriginalOrdinal: 3},
		{Pinyin: "b", Word: "普通", Source: SourceLearned, Frequency: 1, OriginalOrdinal: 8},
		{Pinyin: "c", Word: "固定", Source: SourceLearned, Frequency: 1, Pinned: true, OriginalOrdinal: 99},
	}
	got := ResolveCandidate(base)
	if got[0].Word != "固定" || got[1].Word != "甲" || got[2].Word != "乙" {
		t.Fatalf("tie-break order = %+v", got)
	}
	again := ResolveCandidate(append([]CandidateRecord(nil), base...))
	if !reflect.DeepEqual(got, again) {
		t.Fatalf("repeated resolution changed: %#v vs %#v", got, again)
	}
}

func writeDiagnosticDict(t *testing.T, path, word string) {
	t.Helper()
	lex, err := ime.CompileRimeDict(strings.NewReader("---\nname: test\n...\n" + word + "\tni hao\t999\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, lex.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDictionaryDiagnosticsFallsBackWithoutPartialCustom(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.dict.yaml")
	bad := filepath.Join(dir, "bad.dict.yaml")
	missing := filepath.Join(dir, "missing.dict.yaml")
	writeDiagnosticDict(t, valid, "自定义诊断词")
	if err := os.WriteFile(bad, []byte("not a dictionary\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(missing, []byte("\tni hao\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var diagnostics []DictionaryDiagnostic
	dict, err := LoadDictionaryWithDiagnostics([]string{valid, bad, missing, filepath.Join(dir, "absent.dict.yaml")}, filepath.Join(dir, "cache"), func(d DictionaryDiagnostic) {
		diagnostics = append(diagnostics, d)
	})
	if err != nil || dict == nil {
		t.Fatalf("load = %v, %v", dict, err)
	}
	if len(diagnostics) != 3 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	kinds := map[string]bool{}
	for _, d := range diagnostics {
		kinds[d.Kind] = true
		if d.Fallback != "starter+builtin" || d.Path == "" || d.Message == "" {
			t.Fatalf("incomplete diagnostic: %+v", d)
		}
	}
	for _, kind := range []string{"malformed", "missing_field", "unreadable"} {
		if !kinds[kind] {
			t.Fatalf("missing diagnostic kind %q: %+v", kind, diagnostics)
		}
	}
	var got []string
	dict.LookupPhrases([]string{"ni", "hao"}, func(word string) bool { got = append(got, word); return true })
	if len(got) == 0 || got[0] != "自定义诊断词" {
		t.Fatalf("valid custom dictionary was not retained: %v", got)
	}
}
