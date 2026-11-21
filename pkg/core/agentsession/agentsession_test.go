package agentsession

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverProviderFailureDoesNotHideOtherProviders(t *testing.T) {
	root := t.TempDir()
	dsh := filepath.Join(root, "dsh")
	claude := filepath.Join(root, "claude")
	if err := os.MkdirAll(dsh, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(claude, "project-a"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dsh, "broken.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "project-a", "session.jsonl"), []byte(`{"id":"claude-1","status":"completed","timestamp":"2026-11-21T10:00:00Z","content":"done"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sessions, diagnostics := Discover(context.Background(), Options{DSHDir: dsh, DSHThreadsDir: filepath.Join(root, "threads"), CodexDB: filepath.Join(root, "missing.sqlite"), ClaudeDir: claude})
	if len(sessions) != 1 || sessions[0].Provider != "claude" {
		t.Fatalf("sessions=%+v", sessions)
	}
	found := false
	for _, d := range diagnostics {
		if d.Provider == "dsh" && d.Code == "invalid_json" {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics=%+v", diagnostics)
	}
}

func TestLastMessageIsTruncatedAndSanitized(t *testing.T) {
	root := t.TempDir()
	dsh := filepath.Join(root, "dsh")
	if err := os.MkdirAll(dsh, 0700); err != nil {
		t.Fatal(err)
	}
	message := "```secret code``` token=sk-abcdefghijklmnopqrstuvwxyz /Users/alice/private " + strings.Repeat("很长", 100)
	writeJSON(t, filepath.Join(dsh, "session.json"), map[string]any{"record": map[string]any{"identity": map[string]any{"id": "dsh-1", "createdAt": "2026-11-21T10:00:00Z"}, "rows": map[string]any{"title": "Title", "lastMessage": message}}})
	sessions, _ := Discover(context.Background(), Options{DSHDir: dsh, DSHThreadsDir: filepath.Join(root, "threads"), CodexDB: filepath.Join(root, "missing"), ClaudeDir: filepath.Join(root, "none")})
	if len(sessions) != 1 {
		t.Fatalf("sessions=%+v", sessions)
	}
	got := sessions[0].LastMessage
	if len([]rune(got)) > 120 || strings.Contains(got, "sk-abcdefghijklmnopqrstuvwxyz") || strings.Contains(got, "/Users/alice") || strings.Contains(got, "```") {
		t.Fatalf("unsanitized message=%q", got)
	}
}

type fakeCodexReader struct {
	rows []CodexThread
	err  error
}

func (r fakeCodexReader) Threads(context.Context, int) ([]CodexThread, error) { return r.rows, r.err }

func TestCodexReaderFeedsAggregationWithoutSQLiteInCore(t *testing.T) {
	now := time.Date(2026, 11, 21, 11, 0, 0, 0, time.UTC)
	sessions, diags := Discover(context.Background(), Options{
		DSHDir: filepath.Join(t.TempDir(), "dsh"), DSHThreadsDir: filepath.Join(t.TempDir(), "threads"), ClaudeDir: filepath.Join(t.TempDir(), "claude"),
		CodexReader: fakeCodexReader{rows: []CodexThread{{ID: "cx-1", Title: "Codex", Workspace: "/work/project", Status: "completed", LastActivityAt: now, LastMessage: "finished"}}},
		Now:         func() time.Time { return now }, ProviderTimeout: 500 * time.Millisecond,
	})
	if len(sessions) != 1 || sessions[0].Provider != "codex" || sessions[0].ID != "cx-1" || sessions[0].Status != StatusStopped {
		t.Fatalf("sessions=%+v diags=%+v", sessions, diags)
	}
	for _, d := range diags {
		if d.Provider == "codex" {
			t.Fatalf("unexpected codex diagnostics=%+v", diags)
		}
	}
}

func TestCodexRejectsImmutableURI(t *testing.T) {
	_, diags := Discover(context.Background(), Options{CodexDB: "file:/tmp/state_5.sqlite?immutable=1", DSHDir: filepath.Join(t.TempDir(), "dsh"), DSHThreadsDir: filepath.Join(t.TempDir(), "threads"), ClaudeDir: filepath.Join(t.TempDir(), "claude")})
	found := false
	for _, d := range diags {
		if d.Provider == "codex" && d.Code == "unsafe_read_mode" {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics=%+v", diags)
	}
}
