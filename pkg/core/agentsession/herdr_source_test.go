package agentsession

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type mockHerdrSourceExec struct {
	lookErr   error
	lookPath  string
	wsOut     []byte
	wsErr     error
	agentsOut []byte
	agentsErr error
	readOut   []byte
	readErr   error
	calls     [][]string
}

func (m *mockHerdrSourceExec) LookPath(string) (string, error) {
	if m.lookErr != nil {
		return "", m.lookErr
	}
	if m.lookPath != "" {
		return m.lookPath, nil
	}
	return "/usr/local/bin/herdr", nil
}

func (m *mockHerdrSourceExec) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	m.calls = append(m.calls, append([]string(nil), args...))
	if len(args) >= 2 && args[0] == "workspace" && args[1] == "list" {
		return m.wsOut, m.wsErr
	}
	if len(args) >= 2 && args[0] == "agent" && args[1] == "list" {
		return m.agentsOut, m.agentsErr
	}
	if len(args) >= 2 && args[0] == "agent" && args[1] == "read" {
		return m.readOut, m.readErr
	}
	return nil, errors.New("unexpected herdr command")
}

func TestHerdrClientReadOutputAndDiscovery(t *testing.T) {
	missing := NewHerdrClientWithExecutor(&mockHerdrSourceExec{lookErr: errors.New("missing")})
	if _, err := missing.ListWorkspaces(context.Background()); err == nil {
		t.Fatal("expected ListWorkspaces error when herdr missing")
	}
	if _, err := missing.ListAgents(context.Background()); err == nil {
		t.Fatal("expected ListAgents error when herdr missing")
	}
	if _, res, err := missing.ReadOutput(context.Background(), "w4:p1", 10); err == nil || res.Code != "herdr_unavailable" {
		t.Fatalf("expected herdr_unavailable, got res=%+v err=%v", res, err)
	}

	// Empty target fails fast; lines <= 0 or > 200 clamps to 60; ReadOutput strips ANSI & redacts secrets/paths.
	readExec := &mockHerdrSourceExec{
		readOut: []byte(`{"result":{"content":"\u001b[32mOK\u001b[0m /Users/alice/repo/file.go token=sk-secret999999\r\nnext line"}}`),
	}
	client := NewHerdrClientWithExecutor(readExec)
	if _, res, err := client.ReadOutput(context.Background(), "  ", 20); err == nil || res.Code != "invalid_target" {
		t.Fatalf("expected invalid_target on empty target, got res=%+v err=%v", res, err)
	}
	out, res, err := client.ReadOutput(context.Background(), "w4:p1", 999)
	if err != nil || res.Code != "ok" {
		t.Fatalf("ReadOutput failed: res=%+v err=%v", res, err)
	}
	if strings.Contains(out, "\x1b[") || strings.Contains(out, "sk-secret999999") || strings.Contains(out, "/Users/alice") {
		t.Fatalf("ReadOutput leaked ANSI/secret/path: %q", out)
	}
	if !strings.Contains(out, "OK") || !strings.Contains(out, "[redacted]") || !strings.Contains(out, "[path]") {
		t.Fatalf("ReadOutput unexpected sanitized output: %q", out)
	}
	if args := strings.Join(readExec.calls[0], " "); !strings.Contains(args, "--lines 60") {
		t.Fatalf("expected out-of-range lines clamped to 60, got %q", args)
	}

	readErrClient := NewHerdrClientWithExecutor(&mockHerdrSourceExec{readErr: errors.New("pane gone")})
	if _, res, err := readErrClient.ReadOutput(context.Background(), "w4:p1", 10); err == nil || res.Code != "agent_read_failed" || !res.Retryable {
		t.Fatalf("expected agent_read_failed retryable, got res=%+v err=%v", res, err)
	}

	// discoverHerdr maps ListAgents timeout/error to diagnostics and degrades gracefully if ListWorkspaces fails.
	errDisc := &mockHerdrSourceExec{agentsErr: context.DeadlineExceeded}
	ctxTO, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	defer cancel()
	_, diags := discoverHerdr(ctxTO, Options{HerdrExecutor: errDisc, Now: time.Now})
	if len(diags) != 1 || diags[0].Code != "timeout" {
		t.Fatalf("expected timeout diagnostic, got %+v", diags)
	}
	_, diags = discoverHerdr(context.Background(), Options{HerdrExecutor: &mockHerdrSourceExec{agentsErr: errors.New("down")}, Now: time.Now})
	if len(diags) != 1 || diags[0].Code != "unavailable" {
		t.Fatalf("expected unavailable diagnostic, got %+v", diags)
	}

	graceful := &mockHerdrSourceExec{
		wsErr:     errors.New("ws list failed"),
		agentsOut: []byte(`[{"agent":"codex","agent_status":"blocked","cwd":"/work/clavis","pane_id":"w4:p2","display_agent":"codex · clavis","workspace_id":"w4"}]`),
	}
	sessions, diags := discoverHerdr(context.Background(), Options{HerdrExecutor: graceful, Now: time.Now})
	if len(diags) != 0 || len(sessions) != 1 || !sessions[0].PendingReview || sessions[0].WorkspaceLabel != "clavis" {
		t.Fatalf("expected fallback CWD workspace label & waiting pending review, got sessions=%+v diags=%+v", sessions, diags)
	}
}

func TestHerdrHelpersAndSanitizationEdges(t *testing.T) {
	// extractHerdrReadText across JSON variants and plain text.
	for _, tc := range []struct {
		raw, want string
	}{
		{`{"result":{"text":"r-text"}}`, "r-text"},
		{`{"result":{"output":"r-out"}}`, "r-out"},
		{`{"text":"top-text"}`, "top-text"},
		{`{"content":"top-content"}`, "top-content"},
		{`{"output":"top-out"}`, "top-out"},
		{`{invalid`, `{invalid`},
		{`plain lines`, `plain lines`},
	} {
		if got := extractHerdrReadText([]byte(tc.raw)); got != tc.want {
			t.Fatalf("extractHerdrReadText(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}

	// extractHerdrSessionID branches.
	for _, tc := range []struct {
		raw, pane, want string
	}{
		{"/tmp/2026_abc-123.jsonl", "w1:p1", "abc-123"},
		{"/tmp/plainname.jsonl", "w1:p1", "plainname"},
		{"sess-direct", "w1:p1", "sess-direct"},
		{"", "w1:p1", "pane-w1:p1"},
		{"", "", ""},
	} {
		if got := extractHerdrSessionID(tc.raw, tc.pane); got != tc.want {
			t.Fatalf("extractHerdrSessionID(%q, %q) = %q, want %q", tc.raw, tc.pane, got, tc.want)
		}
	}

	// herdrAgentTitle fallback chain.
	if got := herdrAgentTitle(HerdrAgent{Name: "custom"}, "ws", "codex"); got != "custom" {
		t.Fatalf("explicit Name title = %q", got)
	}
	if got := herdrAgentTitle(HerdrAgent{DisplayAgent: "codex · refactor"}, "ws", "codex"); got != "refactor" {
		t.Fatalf("stripped provider prefix title = %q", got)
	}
	if got := herdrAgentTitle(HerdrAgent{TerminalTitle: "vim session"}, "ws", "codex"); got != "vim session" {
		t.Fatalf("TerminalTitle fallback = %q", got)
	}
	if got := herdrAgentTitle(HerdrAgent{}, "clavis", "codex"); got != "clavis · codex" {
		t.Fatalf("wsLabel · provider fallback = %q", got)
	}
	if got := herdrAgentTitle(HerdrAgent{}, "", "codex"); got != "codex" {
		t.Fatalf("bare provider fallback = %q", got)
	}

	// mapHerdrAgentStatus aliases.
	for _, tc := range []struct {
		raw     string
		wantSt  Status
		pending bool
	}{
		{"busy", StatusRunning, false},
		{"approval", StatusWaiting, true},
		{"exited", StatusStopped, false},
		{"ready", StatusWaiting, false},
		{"weird", StatusUnknown, false},
	} {
		st, p := mapHerdrAgentStatus(tc.raw)
		if st != tc.wantSt || p != tc.pending {
			t.Fatalf("mapHerdrAgentStatus(%q) = (%v, %v), want (%v, %v)", tc.raw, st, p, tc.wantSt, tc.pending)
		}
	}

	// SanitizeMultilineOutput: invalid UTF-8, OSC/CSI escapes, CJK rune-safe byte cap, line cap & fence closing.
	if s, trunc := SanitizeMultilineOutput("", 10, 100); s != "" || trunc {
		t.Fatalf("empty sanitize got (%q, %v)", s, trunc)
	}
	dirty := "\xff\x1b]0;WindowTitle\x07\x1b[31m```go\nfmt.Println(\"中文测试内容\")\n" + strings.Repeat("多字节字符", 12)
	sanitized, trunc := SanitizeMultilineOutput(dirty, 5, 48)
	if !trunc || !utf8.ValidString(sanitized) {
		t.Fatalf("expected truncated valid UTF-8 output, got trunc=%v valid=%v str=%q", trunc, utf8.ValidString(sanitized), sanitized)
	}
	if strings.Count(sanitized, "```")%2 != 0 {
		t.Fatalf("expected even backtick fence parity, got %q", sanitized)
	}

	// SanitizeDiagnosticText replaces code blocks and caps at 120 runes.
	diag := SanitizeDiagnosticText("err ```big\ncode\n``` " + strings.Repeat("x", 150))
	if strings.Contains(diag, "big") || !strings.Contains(diag, "[code]") || len([]rune(diag)) > 120 {
		t.Fatalf("SanitizeDiagnosticText failed: len=%d %q", len([]rune(diag)), diag)
	}
}
