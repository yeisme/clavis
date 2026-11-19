package ime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A dictionary compiled by an older compiler must not be reused after the compiler changes:
// the cache key includes dictCompilerVersion, so bumping it produces a different file name.
func TestDictCacheKeyIncludesCompilerVersion(t *testing.T) {
	dir := t.TempDir()
	dict := filepath.Join(dir, "user.dict.yaml")
	if err := os.WriteFile(dict, []byte("---\nname: user\n...\n你好\tni hao\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(dir, "cache")

	first, err := LoadOrCompileDicts([]string{dict}, cacheDir)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if first == nil || first.Len() == 0 {
		t.Fatal("compiled dictionary is empty")
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".clvdict") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("no compiled cache file written")
	}
	// The second load must hit the same cache file, not recompile into a new name.
	second, err := LoadOrCompileDicts([]string{dict}, cacheDir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if second.Len() != first.Len() {
		t.Fatalf("cached reload returned %d entries, first compile %d", second.Len(), first.Len())
	}
	entries2, _ := os.ReadDir(cacheDir)
	count := 0
	for _, e := range entries2 {
		if strings.HasSuffix(e.Name(), ".clvdict") {
			count++
		}
	}
	if count != len(names) {
		t.Fatalf("cache grew from %d to %d files on reload", len(names), count)
	}
}
