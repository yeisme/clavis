package filesearch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEscapeMDQueryLiteralAndBuildMDQuery(t *testing.T) {
	raw := "foo\"bar\\baz*?'\n\r\t\x7f"
	escaped := EscapeMDQueryLiteral(raw)
	if strings.ContainsAny(escaped, "\n\r\t\x7f") {
		t.Fatalf("control chars remained in escaped literal: %q", escaped)
	}
	wantEsc := `foo\"bar\\baz\*\?\'`
	if escaped != wantEsc {
		t.Fatalf("EscapeMDQueryLiteral = %q, want %q", escaped, wantEsc)
	}

	pred := BuildMDQuery(`x" || kMDItemFSName == "*`)
	if !strings.HasPrefix(pred, `kMDItemDisplayName == "*`) || !strings.HasSuffix(pred, `*"cdw`) {
		t.Fatalf("unexpected predicate envelope: %q", pred)
	}
	// Every internal quote inside the literal must be escaped.
	inner := strings.TrimSuffix(strings.TrimPrefix(pred, `kMDItemDisplayName == "*`), `*"cdw`)
	for i := 0; i < len(inner); i++ {
		if inner[i] == '"' && (i == 0 || inner[i-1] != '\\') {
			t.Fatalf("unescaped quote inside predicate literal %q", pred)
		}
	}
}

func TestIsExcludedPath(t *testing.T) {
	scope := filepath.Join("/", "Users", "alice")
	if !isExcludedPath(filepath.Join(scope, "proj", ".git", "HEAD"), scope, DefaultExcludeDirs()) {
		t.Fatal("expected .git subpath excluded")
	}
	if !isExcludedPath(filepath.Join(scope, "app", "node_modules", "pkg", "index.js"), scope, DefaultExcludeDirs()) {
		t.Fatal("expected node_modules excluded")
	}
	if !isExcludedPath(filepath.Join(scope, "Library", "Caches", "com.test", "db"), scope, DefaultExcludeDirs()) {
		t.Fatal("expected Library/Caches excluded")
	}
	if isExcludedPath(filepath.Join(scope, "Documents", "gitignore-notes.md"), scope, DefaultExcludeDirs()) {
		t.Fatal("non-exact filename gitignore-notes.md must not be excluded by .git")
	}
	if isExcludedPath(filepath.Join(scope, "Library", "Preferences", "app.plist"), scope, DefaultExcludeDirs()) {
		t.Fatal("Library/Preferences must not be excluded by Library/Caches")
	}
}

func TestListPathPrefixHomeAbsoluteHiddenSortedAndCapped(t *testing.T) {
	scope := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(scope, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite := func(rel string) {
		p := filepath.Join(scope, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustMkdir(".git")
	mustMkdir("docs")
	mustMkdir(".hidden-dir")
	mustWrite("alpha.txt")
	mustWrite("Beta.txt")
	mustWrite(".secret-dot")
	mustWrite("docs/report.md")
	mustWrite("docs/readme.txt")

	matches, err := listPathPrefix(context.Background(), "~/", Options{ScopeDir: scope, Limit: 20})
	if err != nil {
		t.Fatalf("listPathPrefix(~/) err: %v", err)
	}
	if len(matches) != 3 {
		t.Fatalf("~/ matches len = %d (%+v), want 3 (docs + alpha.txt + Beta.txt)", len(matches), matches)
	}
	if !matches[0].IsDir || matches[0].Name != "docs" {
		t.Fatalf("expected dir docs first, got %+v", matches[0])
	}
	if matches[1].Name != "alpha.txt" || matches[2].Name != "Beta.txt" {
		t.Fatalf("unexpected file order: %+v", matches[1:])
	}

	// Prefix + dotfile opt-in.
	dotMatches, err := SearchFiles(context.Background(), "~/.", Options{ScopeDir: scope, Limit: 20})
	if err != nil {
		t.Fatalf("listPathPrefix(~/.) err: %v", err)
	}
	if len(dotMatches) != 2 {
		t.Fatalf("~/. matches len = %d (%+v), want 2 (.hidden-dir, .secret-dot; .git excluded)", len(dotMatches), dotMatches)
	}

	// Absolute prefix matching + Limit cap.
	absQuery := filepath.Join(scope, "docs", "re")
	absMatches, err := listPathPrefix(context.Background(), absQuery, Options{ScopeDir: scope, Limit: 1})
	if err != nil {
		t.Fatalf("listPathPrefix(%q) err: %v", absQuery, err)
	}
	if len(absMatches) != 1 || absMatches[0].Name != "readme.txt" {
		t.Fatalf("absolute prefix matches = %+v, want [readme.txt]", absMatches)
	}
}
