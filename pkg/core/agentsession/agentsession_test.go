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

func TestProviderCompatibilityFieldsAndHumanBoundary(t *testing.T) {
	root := t.TempDir()
	dsh := filepath.Join(root, "dsh")
	if err := os.MkdirAll(dsh, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(dsh, "session.json"), map[string]any{
		"record": map[string]any{
			"identity": map[string]any{"id": "dsh-compat", "cwd": "/work/clavis"},
			"rows": map[string]any{
				"title":          "Compatibility",
				"status":         "waiting",
				"currentAction":  "approve replacement",
				"awaitingHuman":  true,
				"pending_review": false,
				"turnOutline": map[string]any{"val": map[string]any{"turns": []any{
					map[string]any{"response": "Needs confirmation"},
				}}},
			},
		},
	})
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	sessions, diags := Discover(context.Background(), Options{
		DSHDir: dsh, DSHThreadsDir: filepath.Join(root, "threads"), ClaudeDir: filepath.Join(root, "claude"),
		Now: func() time.Time { return now },
	})
	if len(diags) == 0 || len(sessions) != 1 {
		t.Fatalf("sessions=%+v diagnostics=%+v", sessions, diags)
	}
	s := sessions[0]
	if s.CurrentAction != "approve replacement" || s.ResultLine != "Needs confirmation" || !s.AwaitingHuman || s.PendingReview || s.Status != StatusWaiting {
		t.Fatalf("compatibility state=%+v", s)
	}
}

func TestFalsePendingMarkersDoNotSetAwaitingHuman(t *testing.T) {
	root := t.TempDir()
	dsh := filepath.Join(root, "dsh")
	if err := os.MkdirAll(dsh, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(dsh, "session.json"), map[string]any{
		"record": map[string]any{
			"identity": map[string]any{"id": "dsh-no-pending"},
			"rows":     map[string]any{"status": "running", "pending_review": false, "action_request": false},
		},
	})
	sessions, _ := Discover(context.Background(), Options{DSHDir: dsh, ClaudeDir: filepath.Join(root, "claude"), DSHThreadsDir: filepath.Join(root, "threads")})
	if len(sessions) != 1 || sessions[0].AwaitingHuman || sessions[0].PendingReview {
		t.Fatalf("false pending markers changed state=%+v", sessions)
	}
}

func TestExplicitUnknownStatusDoesNotBecomeRunningFromRecentTimestamp(t *testing.T) {
	root := t.TempDir()
	dsh := filepath.Join(root, "dsh")
	if err := os.MkdirAll(dsh, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	writeJSON(t, filepath.Join(dsh, "session.json"), map[string]any{
		"record": map[string]any{
			"identity": map[string]any{"id": "dsh-unknown", "updatedAt": now.Add(-time.Minute).Format(time.RFC3339)},
			"rows":     map[string]any{"status": "unknown"},
		},
	})
	sessions, _ := Discover(context.Background(), Options{DSHDir: dsh, ClaudeDir: filepath.Join(root, "claude"), DSHThreadsDir: filepath.Join(root, "threads"), Now: func() time.Time { return now }})
	if len(sessions) != 1 || sessions[0].Status != StatusUnknown {
		t.Fatalf("explicit unknown status was inferred as active: %+v", sessions)
	}
}

func TestCodexTimeoutDiagnosticPreservesBoundary(t *testing.T) {
	reader := blockingCodexReader{}
	_, diagnostics := Discover(context.Background(), Options{
		CodexDB: "/tmp/state_5.sqlite", CodexReader: reader,
		DSHDir: filepath.Join(t.TempDir(), "dsh"), DSHThreadsDir: filepath.Join(t.TempDir(), "threads"), ClaudeDir: filepath.Join(t.TempDir(), "claude"),
		ProviderTimeout: 10 * time.Millisecond,
	})
	for _, diagnostic := range diagnostics {
		if diagnostic.Provider == "codex" && diagnostic.Code == "timeout" {
			return
		}
	}
	t.Fatalf("timeout was flattened into another diagnostic: %+v", diagnostics)
}

func TestClaudeAndCodexCompatibilityFieldsArePreserved(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, "claude", "project-a")
	if err := os.MkdirAll(claude, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "session.jsonl"), []byte(`{"id":"claude-compat","status":"waiting","currentAction":"answer question","resultLine":"awaiting input","awaitingHuman":true}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	sessions, _ := Discover(context.Background(), Options{
		DSHDir: filepath.Join(root, "dsh"), DSHThreadsDir: filepath.Join(root, "threads"), ClaudeDir: filepath.Dir(claude),
		CodexReader: fakeCodexReader{rows: []CodexThread{{ID: "codex-compat", Status: "running", CurrentAction: "review result", ResultLine: "needs approval", AwaitingHuman: true}}},
		Now:         func() time.Time { return now },
	})
	byProvider := map[string]Session{}
	for _, session := range sessions {
		byProvider[session.Provider] = session
	}
	for provider, want := range map[string]struct {
		id, action, result string
	}{
		"claude": {"claude-compat", "answer question", "awaiting input"},
		"codex":  {"codex-compat", "review result", "needs approval"},
	} {
		got, ok := byProvider[provider]
		if !ok || got.ID != want.id || got.CurrentAction != want.action || got.ResultLine != want.result || !got.AwaitingHuman || got.Status != StatusWaiting {
			t.Fatalf("provider=%s session=%+v all=%+v", provider, got, sessions)
		}
	}
}

func TestSessionCompatibilityFieldsUseStableJSONNames(t *testing.T) {
	encoded, err := json.Marshal(Session{CurrentAction: "review", ResultLine: "ready", AwaitingHuman: true})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["currentAction"] != "review" || fields["resultLine"] != "ready" || fields["awaitingHuman"] != true {
		t.Fatalf("compatibility JSON=%s", encoded)
	}
	if _, ok := fields["CurrentAction"]; ok {
		t.Fatalf("compatibility fields used Go names: %s", encoded)
	}
}

type blockingCodexReader struct{}

func (blockingCodexReader) Threads(ctx context.Context, _ int) ([]CodexThread, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
