//go:build darwin

package filesearch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSearchFilesDarwinRunnerRankingExclusionsAndTimeout(t *testing.T) {
	scope := t.TempDir()
	write := func(rel string, mod time.Time) string {
		p := filepath.Join(scope, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
		return p
	}

	now := time.Now()
	pExact := write("notes", now.Add(-2*time.Hour))
	pPrefix := write("notes-plan.md", now.Add(-1*time.Hour))
	pSubNew := write("docs/my-notes-v2.md", now)
	pSubOld := write("docs/my-notes-v1.md", now.Add(-5*time.Hour))
	pExcluded := write("node_modules/notes.js", now)
	pMissing := filepath.Join(scope, "deleted-notes.md")

	prev := mdfindRunner
	t.Cleanup(func() { mdfindRunner = prev })

	var gotScope, gotPred string
	mdfindRunner = func(_ context.Context, scopeDir, predicate string) ([]string, error) {
		gotScope = scopeDir
		gotPred = predicate
		return []string{pSubOld, pExcluded, pMissing, pSubNew, pPrefix, pExact, pExact}, nil
	}

	got, err := SearchFiles(context.Background(), "notes", Options{ScopeDir: scope, Limit: 3})
	if err != nil {
		t.Fatalf("SearchFiles: %v", err)
	}
	if gotScope != scope {
		t.Fatalf("mdfind scopeDir = %q, want %q", gotScope, scope)
	}
	if !strings.Contains(gotPred, `kMDItemDisplayName == "*notes*"cdw`) {
		t.Fatalf("unexpected mdfind predicate: %q", gotPred)
	}
	if len(got) != 3 {
		t.Fatalf("SearchFiles len = %d (%+v), want 3 (limit honored, excluded/missing dropped)", len(got), got)
	}
	if got[0].Path != pExact || got[1].Path != pPrefix || got[2].Path != pSubNew {
		t.Fatalf("unexpected ranking order: got=[%s %s %s], want=[%s %s %s]",
			got[0].Path, got[1].Path, got[2].Path, pExact, pPrefix, pSubNew)
	}

	// Timeout / cancellation propagates cleanly.
	mdfindRunner = func(ctx context.Context, _ string, _ string) ([]string, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	_, err = SearchFiles(context.Background(), "notes", Options{ScopeDir: scope, Timeout: 20 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}
