package ime

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadOrCompileDictsCacheInvalidationAndMissingPath(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	dictA := filepath.Join(dir, "a.dict.yaml")
	if err := os.WriteFile(dictA, []byte("---\nname: a\n...\n词库甲\tci ku jia\t500\n"), 0644); err != nil {
		t.Fatal(err)
	}
	l1, err := LoadOrCompileDicts([]string{dictA}, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := l1.Lookup("cikujia"); len(got) == 0 || got[0].Word != "词库甲" {
		t.Fatalf("l1 lookup = %#v", got)
	}

	// Missing path must fail even when cacheDir already has a valid cache file.
	if _, err := LoadOrCompileDicts([]string{filepath.Join(dir, "missing.dict.yaml")}, cacheDir); err == nil {
		t.Fatal("expected error for missing dictionary path")
	}

	// Updating file content must invalidate the cache key.
	if err := os.WriteFile(dictA, []byte("---\nname: a\n...\n词库乙\tci ku yi\t600\n"), 0644); err != nil {
		t.Fatal(err)
	}
	l2, err := LoadOrCompileDicts([]string{dictA}, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := l2.Lookup("cikuyi"); len(got) == 0 || got[0].Word != "词库乙" {
		t.Fatalf("l2 lookup = %#v", got)
	}
}

func TestExpandFuzzyAndShuangpinHelpers(t *testing.T) {
	got := ExpandFuzzy("zhong", []string{"z=zh"})
	if !slices.Contains(got, "zhong") || !slices.Contains(got, "zong") || slices.Contains(got, "zhhong") {
		t.Fatalf("ExpandFuzzy(zhong, z=zh) = %v", got)
	}
	gotFin := ExpandFuzzy("shan", []string{"an=ang"})
	if !slices.Contains(gotFin, "shang") {
		t.Fatalf("ExpandFuzzy(shan, an=ang) = %v", gotFin)
	}
	if syls := ShuangpinSyllables("xiaohe", "ul"); !slices.Contains(syls, "shuang") || !slices.Contains(syls, "shai") {
		t.Fatalf("ShuangpinSyllables(xiaohe, ul) = %v", syls)
	}
	// Headerless TSV Rime dict compilation.
	l, err := CompileRimeDict(strings.NewReader("无头词条\twu tou ci tiao\t85%\n"))
	if err != nil || len(l.Lookup("wutoucitiao")) == 0 {
		t.Fatalf("headerless CompileRimeDict err=%v got=%#v", err, l)
	}
}
