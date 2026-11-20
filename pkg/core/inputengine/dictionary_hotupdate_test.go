package inputengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/ime"
)

func writeTestClvDict(t *testing.T, path, word string) {
	t.Helper()
	lex, err := ime.CompileRimeDict(strings.NewReader("---\nname: test\n...\n" + word + "\tni hao\t999999\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, lex.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func phraseCandidates(dict Dictionary) []string {
	var words []string
	dict.LookupPhrases([]string{"ni", "hao"}, func(word string) bool {
		words = append(words, word)
		return true
	})
	return words
}

func TestUserDictionaryPriorityAndFallbackDiagnostics(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "clavis", "dicts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "personal.clvdict")
	writeTestClvDict(t, path, "自定义词")

	dict, status := LoadUserDictionary()
	if status.Source != "user" || status.Count == 0 || !strings.Contains(status.Path, path) {
		t.Fatalf("status = %+v", status)
	}
	if got := phraseCandidates(dict); len(got) == 0 || got[0] != "自定义词" {
		t.Fatalf("user dictionary did not take priority: %v", got)
	}

	if err := os.WriteFile(path, []byte("not a clvdict"), 0600); err != nil {
		t.Fatal(err)
	}
	fallback, status := LoadUserDictionary()
	if status.Source != "embedded" || len(status.Diagnostics) == 0 || status.Count == 0 {
		t.Fatalf("invalid user dictionary did not diagnose/fallback: %+v", status)
	}
	if fallback == nil || len(phraseCandidates(fallback)) == 0 {
		t.Fatal("embedded fallback is unusable")
	}
}

func TestUserDictionaryDeleteFallsBackToEmbedded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "clavis", "dicts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "personal.clvdict")
	writeTestClvDict(t, path, "删除前词")
	if status := func() DictionaryStatus { _, s := LoadUserDictionary(); return s }(); status.Source != "user" {
		t.Fatalf("before delete status = %+v", status)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, status := LoadUserDictionary()
	if status.Source != "embedded" || status.Count == 0 {
		t.Fatalf("after delete status = %+v", status)
	}
}

func TestUserDictionaryLookupDoesNotReadAfterLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "clavis", "dicts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	writeTestClvDict(t, filepath.Join(dir, "personal.clvdict"), "热更新词")
	dict, _ := LoadUserDictionary()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if got := phraseCandidates(dict); len(got) == 0 || got[0] != "热更新词" {
		t.Fatalf("loaded dictionary depended on disk after load: %v", got)
	}
}
