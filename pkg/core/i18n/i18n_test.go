package i18n_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/i18n"
)

func TestTableParity(t *testing.T) {
	if missing := i18n.MissingKeys(); len(missing) > 0 {
		t.Fatalf("string tables drifted apart:\n%s", strings.Join(missing, "\n"))
	}
}

func TestNormalizeLanguage(t *testing.T) {
	cases := map[string]i18n.Language{
		"":                 i18n.Auto,
		"auto":             i18n.Auto,
		"AUTO":             i18n.Auto,
		"en":               i18n.English,
		"en-US":            i18n.English,
		"en_US.UTF-8":      i18n.English,
		"zh":               i18n.Chinese,
		"zh-CN":            i18n.Chinese,
		"zh-cn":            i18n.Chinese,
		"zh_Hans":          i18n.Chinese,
		"zh-CN.UTF-8":      i18n.Chinese,
		"zh-Hant":          i18n.Chinese,
		"fr":               i18n.English,
		"garbage \t value": i18n.English,
	}
	for in, want := range cases {
		if got := i18n.NormalizeLanguage(in); got != want {
			t.Errorf("NormalizeLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveLanguageAutoPrefersSystem(t *testing.T) {
	if got := i18n.ResolveLanguage("auto", []string{"zh-Hans-CN"}); got != i18n.Chinese {
		t.Errorf("auto + zh-Hans-CN = %q, want zh-CN", got)
	}
	if got := i18n.ResolveLanguage("auto", []string{"en-US", "zh-CN"}); got != i18n.English {
		t.Errorf("auto + en-US = %q, want en", got)
	}
	if got := i18n.ResolveLanguage("auto", nil); got != i18n.English {
		t.Errorf("auto + none = %q, want en", got)
	}
	// An explicit choice ignores the system preference.
	if got := i18n.ResolveLanguage("zh-CN", []string{"en-US"}); got != i18n.Chinese {
		t.Errorf("explicit zh-CN = %q, want zh-CN", got)
	}
}

func TestPreferredLanguagesFromEnv(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "zh_CN.UTF-8")
	if got := i18n.ResolveLanguage("auto", i18n.PreferredLanguages()); got != i18n.Chinese {
		t.Errorf("LANG=zh_CN.UTF-8 resolved %q, want zh-CN", got)
	}
	t.Setenv("LANG", "C")
	if got := i18n.ResolveLanguage("auto", i18n.PreferredLanguages()); got != i18n.English {
		t.Errorf("LANG=C resolved %q, want en", got)
	}
}

func TestTInterpolationAndFallback(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	i18n.SetLanguage(i18n.Chinese)
	if got := i18n.T("common.settings"); got != "设置" {
		t.Errorf("zh common.settings = %q", got)
	}
	if got := i18n.Tf("diag.snippet.id.duplicate", "sig"); got != "Snippet id 'sig' 重复" {
		t.Errorf("zh duplicate diag = %q", got)
	}
	// A key missing from both tables renders as the key itself.
	if got := i18n.T("nonexistent.key"); got != "nonexistent.key" {
		t.Errorf("missing key returned %q", got)
	}
	i18n.SetLanguage(i18n.English)
	if got := i18n.T("common.settings"); got != "Settings" {
		t.Errorf("en common.settings = %q", got)
	}
	// Both format arguments must land in the zh message.
	i18n.SetLanguage(i18n.Chinese)
	got := i18n.Tf("diag.leader.key.duplicate", "a", 3)
	if !strings.Contains(got, "'a'") || !strings.Contains(got, "3") {
		t.Errorf("zh two-arg diag = %q", got)
	}
}

// TestCallSiteKeysExist walks the repository's non-test sources for i18n.T /
// i18n.Tf calls with literal keys and asserts every key exists in both
// tables, so a sweep can't silently ship a raw key to the screen.
func TestCallSiteKeysExist(t *testing.T) {
	type use struct{ key, file string }
	var uses []use
	err := filepath.WalkDir(repoRoot(t), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == "build" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", path, err)
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "T" && sel.Sel.Name != "Tf") {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok || ident.Name != "i18n" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true // dynamic key: covered by the package's own tests
			}
			key := strings.Trim(lit.Value, "`\"")
			uses = append(uses, use{key, filepath.ToSlash(strings.TrimPrefix(path, repoRoot(t)+string(os.PathSeparator)))})
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(uses) == 0 {
		// The sweep has not landed yet; keep the scan honest once it has.
		t.Log("no i18n.T call sites yet")
	}
	for _, u := range uses {
		if !i18n.HasKey(u.key) {
			t.Errorf("key %q used at %s is missing from the tables", u.key, u.file)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(thisFile))))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root lookup from %s: %v", thisFile, err)
	}
	return root
}
