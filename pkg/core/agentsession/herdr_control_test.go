package agentsession

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/host"
)

type mockHerdrControlExec struct {
	mu      sync.Mutex
	calls   []string
	listOut string
	sendErr error
}

func (m *mockHerdrControlExec) LookPath(string) (string, error) { return "/usr/local/bin/herdr", nil }
func (m *mockHerdrControlExec) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	joined := strings.Join(args, " ")
	m.mu.Lock()
	m.calls = append(m.calls, joined)
	m.mu.Unlock()
	if len(args) >= 2 && args[0] == "agent" && args[1] == "list" {
		return []byte(m.listOut), nil
	}
	if len(args) >= 2 && args[0] == "agent" && args[1] == "send" && m.sendErr != nil {
		return nil, m.sendErr
	}
	return []byte("{}"), nil
}

func TestHerdrControllerSecurityGatesAndCommands(t *testing.T) {
	exec := &mockHerdrControlExec{
		listOut: `{"result":{"agents":[{"pane_id":"w4:p1","name":"coder","agent":"codex","agent_status":"idle"}]}}`,
	}
	ctrl := NewHerdrController(exec)

	// 1. Unconfirmed SendPrompt MUST fail without touching herdr CLI.
	res, err := ctrl.SendPrompt(context.Background(), SendPromptRequest{Target: "w4:p1", Message: "run tests", Confirmed: false})
	if err == nil || res.Code != "confirmation_required" || res.Retryable {
		t.Fatalf("expected confirmation_required failure, got res=%+v err=%v", res, err)
	}

	// 2. Secret-containing SendPrompt MUST fail cleanly.
	res, err = ctrl.SendPrompt(context.Background(), SendPromptRequest{Target: "w4:p1", Message: "export KEY=sk-abcdefghijklmnopqrstuvwxyz", Confirmed: true})
	if err == nil || res.Code != "secret_blocked" {
		t.Fatalf("expected secret_blocked failure, got res=%+v err=%v", res, err)
	}

	// 3. Unknown target MUST fail after live check.
	res, err = ctrl.SendPrompt(context.Background(), SendPromptRequest{Target: "w99:missing", Message: "hello", Confirmed: true})
	if err == nil || res.Code != "target_not_found" {
		t.Fatalf("expected target_not_found, got res=%+v err=%v", res, err)
	}

	// 4. Valid confirmed SendPrompt sends message cleanly.
	res, err = ctrl.SendPrompt(context.Background(), SendPromptRequest{Target: "w4:p1", Message: "continue step 2", Confirmed: true})
	if err != nil || res.State != host.StateReady {
		t.Fatalf("expected valid SendPrompt ready, got res=%+v err=%v", res, err)
	}

	// 5. Ambiguous SendPrompt failure returns StateUnknown with Retryable=false.
	exec.sendErr = errors.New("broken pipe")
	res, err = ctrl.SendPrompt(context.Background(), SendPromptRequest{Target: "w4:p1", Message: "retry check", Confirmed: true})
	if err == nil || res.State != host.StateUnknown || res.Retryable {
		t.Fatalf("expected StateUnknown & non-retryable on ambiguous send failure, got res=%+v err=%v", res, err)
	}

	// 6. StartAgent allowlists CLIs cleanly and blocks untrusted binaries.
	if _, err := ctrl.StartAgent(context.Background(), "w4", "rm", ""); err == nil {
		t.Fatal("expected StartAgent to reject disallowed CLI")
	}
	if res, err := ctrl.StartAgent(context.Background(), "w4", "codex", "helper"); err != nil || res.State != host.StateReady {
		t.Fatalf("StartAgent codex failed: res=%+v err=%v", res, err)
	}

	// 7. Rename & WaitState & FocusWorkspace run expected commands.
	if res, err := ctrl.RenameAgent(context.Background(), "w4:p1", "builder"); err != nil || res.State != host.StateReady {
		t.Fatalf("RenameAgent failed: %+v err=%v", res, err)
	}
	if res, err := ctrl.FocusWorkspace(context.Background(), "w4"); err != nil || res.State != host.StateReady {
		t.Fatalf("FocusWorkspace failed: %+v err=%v", res, err)
	}
	if res, err := ctrl.WaitState(context.Background(), "w4:p1", "idle", 5*time.Second); err != nil || res.State != host.StateReady {
		t.Fatalf("WaitState failed: %+v err=%v", res, err)
	}
}

type mockHerdrMatrixExec struct {
	mu          sync.Mutex
	lookPathErr error
	listOut     string
	listErr     error
	err         error
	block       bool
	calls       [][]string
}

func (m *mockHerdrMatrixExec) LookPath(string) (string, error) {
	if m.lookPathErr != nil {
		return "", m.lookPathErr
	}
	return "/usr/local/bin/herdr", nil
}

func (m *mockHerdrMatrixExec) Output(ctx context.Context, _ string, args ...string) ([]byte, error) {
	m.mu.Lock()
	m.calls = append(m.calls, append([]string(nil), args...))
	m.mu.Unlock()
	if len(args) >= 2 && args[0] == "agent" && args[1] == "list" {
		if m.listErr != nil {
			return nil, m.listErr
		}
		return []byte(m.listOut), nil
	}
	if m.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if m.err != nil {
		return nil, m.err
	}
	return []byte("{}"), nil
}

func TestHerdrFocusPaneControllerAndClient(t *testing.T) {
	exec := &mockHerdrMatrixExec{}
	ctrl := NewHerdrController(exec)

	// Empty target fails without invoking LookPath/Output.
	if res, err := ctrl.FocusPane(context.Background(), "   "); err == nil || res.Code != "invalid_target" {
		t.Fatalf("expected invalid_target, got res=%+v err=%v", res, err)
	}
	// Missing herdr binary fails cleanly.
	noBin := NewHerdrController(&mockHerdrMatrixExec{lookPathErr: errors.New("not found")})
	if res, err := noBin.FocusPane(context.Background(), "w4:p1"); err == nil || res.Code != "herdr_unavailable" {
		t.Fatalf("expected herdr_unavailable, got res=%+v err=%v", res, err)
	}
	// Success through controller -> HerdrClient.FocusPane issues `agent focus w4:p1`.
	if res, err := ctrl.FocusPane(context.Background(), " w4:p1 "); err != nil || res.State != host.StateReady {
		t.Fatalf("FocusPane success failed: res=%+v err=%v", res, err)
	}
	if got := strings.Join(exec.calls[len(exec.calls)-1], " "); got != "agent focus w4:p1" {
		t.Fatalf("FocusPane args = %q", got)
	}
	// Deadline exceeded reports StateUnknown & Retryable=false.
	timedOutCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	defer cancel()
	timeoutExec := &mockHerdrMatrixExec{block: true}
	if res, err := NewHerdrController(timeoutExec).FocusPane(timedOutCtx, "w4:p1"); err == nil || res.State != host.StateUnknown || res.Code != "focus_timeout" || res.Retryable {
		t.Fatalf("expected focus_timeout StateUnknown, got res=%+v err=%v", res, err)
	}
	// Command failure redacts secrets/paths in message & marks Retryable=true.
	errExec := &mockHerdrMatrixExec{err: errors.New("failed at /Users/alice/secret token=sk-supersecretkey1234")}
	if res, err := NewHerdrController(errExec).FocusPane(context.Background(), "w4:p1"); err == nil || res.Code != "focus_failed" || !res.Retryable || strings.Contains(res.Message, "sk-supersecretkey1234") || strings.Contains(res.Message, "/Users/alice") {
		t.Fatalf("FocusPane error should sanitize message & allow retry, got res=%+v err=%v", res, err)
	}
}

func TestHerdrControlErrorAndBoundaryPaths(t *testing.T) {
	noBin := NewHerdrController(&mockHerdrMatrixExec{lookPathErr: errors.New("missing")})
	expiredCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	defer cancel()

	// FocusWorkspace guards & errors.
	if res, err := noBin.FocusWorkspace(context.Background(), ""); err == nil || res.Code != "invalid_workspace" {
		t.Fatalf("FocusWorkspace empty: %+v %v", res, err)
	}
	if res, err := noBin.FocusWorkspace(context.Background(), "w4"); err == nil || res.Code != "herdr_unavailable" {
		t.Fatalf("FocusWorkspace no binary: %+v %v", res, err)
	}
	if res, err := NewHerdrController(&mockHerdrMatrixExec{block: true}).FocusWorkspace(expiredCtx, "w4"); err == nil || res.State != host.StateUnknown || res.Code != "workspace_focus_timeout" || res.Retryable {
		t.Fatalf("FocusWorkspace timeout: %+v %v", res, err)
	}
	if res, err := NewHerdrController(&mockHerdrMatrixExec{err: errors.New("boom")}).FocusWorkspace(context.Background(), "w4"); err == nil || res.Code != "workspace_focus_failed" || !res.Retryable {
		t.Fatalf("FocusWorkspace error: %+v %v", res, err)
	}

	// RenameAgent guards, newline sanitization, and timeout/error paths.
	if res, err := noBin.RenameAgent(context.Background(), "w4:p1", ""); err == nil || res.Code != "invalid_name" {
		t.Fatalf("RenameAgent empty name: %+v %v", res, err)
	}
	if res, err := noBin.RenameAgent(context.Background(), "w4:p1", strings.Repeat("名", 41)); err == nil || res.Code != "invalid_name" {
		t.Fatalf("RenameAgent >40 runes: %+v %v", res, err)
	}
	if res, err := noBin.RenameAgent(context.Background(), "w4:p1", "ok"); err == nil || res.Code != "herdr_unavailable" {
		t.Fatalf("RenameAgent no binary: %+v %v", res, err)
	}
	renameExec := &mockHerdrMatrixExec{}
	if res, err := NewHerdrController(renameExec).RenameAgent(context.Background(), "w4:p1", "line1\r\nline2"); err != nil || res.State != host.StateReady {
		t.Fatalf("RenameAgent newline sanitization failed: %+v %v", res, err)
	}
	if args := renameExec.calls[len(renameExec.calls)-1]; args[3] != "line1  line2" {
		t.Fatalf("expected sanitized rename arg without CRLF, got %#v", args)
	}
	if res, err := NewHerdrController(&mockHerdrMatrixExec{block: true}).RenameAgent(expiredCtx, "w4:p1", "n"); err == nil || res.Code != "rename_timeout" || res.State != host.StateUnknown {
		t.Fatalf("RenameAgent timeout: %+v %v", res, err)
	}
	if res, err := NewHerdrController(&mockHerdrMatrixExec{err: errors.New("boom")}).RenameAgent(context.Background(), "w4:p1", "n"); err == nil || res.Code != "rename_failed" || !res.Retryable {
		t.Fatalf("RenameAgent error: %+v %v", res, err)
	}

	// SendPrompt: empty, multiple secret patterns, ListAgents failure, match by Name/Session.
	if res, err := noBin.SendPrompt(context.Background(), SendPromptRequest{Target: "w4:p1", Message: "  ", Confirmed: true}); err == nil || res.Code != "invalid_send_request" {
		t.Fatalf("SendPrompt empty msg: %+v %v", res, err)
	}
	for _, secret := range []string{
		"ghp_1234567890abcdef",
		"glpat-1234567890abcdef",
		"AKIA1234567890ABCDEF",
		"-----BEGIN RSA PRIVATE KEY-----",
	} {
		if res, err := noBin.SendPrompt(context.Background(), SendPromptRequest{Target: "w4:p1", Message: "use " + secret, Confirmed: true}); err == nil || res.Code != "secret_blocked" {
			t.Fatalf("SendPrompt failed to block %q: %+v %v", secret, res, err)
		}
	}
	if res, err := noBin.SendPrompt(context.Background(), SendPromptRequest{Target: "w4:p1", Message: "hi", Confirmed: true}); err == nil || res.Code != "herdr_unavailable" {
		t.Fatalf("SendPrompt no binary: %+v %v", res, err)
	}
	listErrCtrl := NewHerdrController(&mockHerdrMatrixExec{listErr: errors.New("rpc fail")})
	if res, err := listErrCtrl.SendPrompt(context.Background(), SendPromptRequest{Target: "w4:p1", Message: "hi", Confirmed: true}); err == nil || res.Code != "target_verify_failed" || !res.Retryable {
		t.Fatalf("SendPrompt verify failure: %+v %v", res, err)
	}
	byNameExec := &mockHerdrMatrixExec{
		listOut: `[{"pane_id":"w4:p1","name":"builder","agent_session":{"value":"sess-99"}}]`,
	}
	if res, err := NewHerdrController(byNameExec).SendPrompt(context.Background(), SendPromptRequest{Target: "sess-99", Message: "status?", Confirmed: true}); err != nil || res.Code != "sent" {
		t.Fatalf("SendPrompt match by session value failed: %+v %v", res, err)
	}

	// StartAgent optional flags, case-insensitive allowlist, and errors.
	if res, err := noBin.StartAgent(context.Background(), "w4", "codex", ""); err == nil || res.Code != "herdr_unavailable" {
		t.Fatalf("StartAgent no binary: %+v %v", res, err)
	}
	startExec := &mockHerdrMatrixExec{}
	if res, err := NewHerdrController(startExec).StartAgent(context.Background(), "", " CoDeX ", ""); err != nil || res.Code != "started" {
		t.Fatalf("StartAgent case-insensitive codex failed: %+v %v", res, err)
	}
	if got := strings.Join(startExec.calls[0], " "); got != "agent start --agent codex" {
		t.Fatalf("StartAgent without workspace/name should omit optional flags, got %q", got)
	}
	if res, err := NewHerdrController(&mockHerdrMatrixExec{err: errors.New("spawn err")}).StartAgent(context.Background(), "w4", "agy", "a1"); err == nil || res.Code != "agent_start_failed" || !res.Retryable {
		t.Fatalf("StartAgent execution failure: %+v %v", res, err)
	}

	// WaitState empty target, defaults, timeout, and error.
	if res, err := noBin.WaitState(context.Background(), "  ", "", 0); err == nil || res.Code != "invalid_target" {
		t.Fatalf("WaitState empty target: %+v %v", res, err)
	}
	if res, err := noBin.WaitState(context.Background(), "w4:p1", "", 0); err == nil || res.Code != "herdr_unavailable" {
		t.Fatalf("WaitState no binary: %+v %v", res, err)
	}
	waitExec := &mockHerdrMatrixExec{}
	if res, err := NewHerdrController(waitExec).WaitState(context.Background(), "w4:p1", "", 0); err != nil || res.Code != "matched" {
		t.Fatalf("WaitState defaults failed: %+v %v", res, err)
	}
	if got := strings.Join(waitExec.calls[0], " "); got != "agent wait w4:p1 --state idle --timeout 30" {
		t.Fatalf("WaitState default args = %q", got)
	}
	if res, err := NewHerdrController(&mockHerdrMatrixExec{block: true}).WaitState(expiredCtx, "w4:p1", "idle", time.Second); err == nil || res.Code != "wait_timeout" || res.State != host.StateUnknown {
		t.Fatalf("WaitState timeout: %+v %v", res, err)
	}
	if res, err := NewHerdrController(&mockHerdrMatrixExec{err: errors.New("closed")}).WaitState(context.Background(), "w4:p1", "idle", time.Second); err == nil || res.Code != "wait_failed" || res.Retryable {
		t.Fatalf("WaitState error: %+v %v", res, err)
	}
}

func TestHerdrSendPromptBlocksUnifiedSecretPatterns(t *testing.T) {
	exec := &mockHerdrMatrixExec{
		listOut: `{"result":{"agents":[{"pane_id":"w4:p1","name":"coder","agent":"codex","agent_status":"idle"}]}}`,
	}
	ctrl := NewHerdrController(exec)

	blocked := []struct {
		name string
		msg  string
	}{
		{"bearer_token", "curl -H 'Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig'"},
		{"bearer_quoted", `use bearer "my-secret-token-value"`},
		{"github_pat", "export TOKEN=github_pat_11ABCDEF1234567890"},
		{"gho_token", "gho_1234567890abcdef"},
		{"ghu_token", "ghu_1234567890abcdef"},
		{"ghs_token", "ghs_1234567890abcdef"},
		{"slack_bot", "send xoxb-1234567890-abcdef"},
		{"slack_user", "send xoxp-1234567890-abcdef"},
		{"api_key_eq", "api_key=supersecret123"},
		{"api_key_quoted", `api_key="quotedsecret123"`},
		{"secret_key_colon", "secret_key: topsecret123"},
		{"access_token_eq", "access_token=tok_abcdef123"},
		{"password_eq", "password=hunter2"},
		{"passwd_quoted", "passwd='my-secret-pass'"},
		{"token_eq", "token=xyz987654"},
		{"openai_sk", "sk-12345678"},
		{"gitlab_pat", "glpat-1234567890abcdef"},
		{"aws_akia", "AKIA1234567890ABCDEF"},
		{"pem_generic", "-----BEGIN PRIVATE KEY-----"},
		{"pem_openssh", "-----BEGIN OPENSSH PRIVATE KEY-----"},
	}

	for _, tc := range blocked {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ctrl.SendPrompt(context.Background(), SendPromptRequest{
				Target:    "w4:p1",
				Message:   tc.msg,
				Confirmed: true,
			})
			if err == nil || res.Code != "secret_blocked" || res.Retryable {
				t.Fatalf("%s: expected secret_blocked non-retryable failure, got res=%+v err=%v", tc.name, res, err)
			}
		})
	}
	if len(exec.calls) != 0 {
		t.Fatalf("blocked secret prompts must never invoke herdr CLI, got calls=%v", exec.calls)
	}

	// Benign prompts mentioning keywords without credential assignments pass.
	for _, msg := range []string{
		"check api_key documentation",
		"review password reset flow",
		"run go test ./pkg/core/agentsession/...",
	} {
		if res, err := ctrl.SendPrompt(context.Background(), SendPromptRequest{
			Target:    "w4:p1",
			Message:   msg,
			Confirmed: true,
		}); err != nil || res.Code != "sent" {
			t.Fatalf("benign prompt %q was wrongly rejected: res=%+v err=%v", msg, res, err)
		}
	}
}

func TestHerdrControlRejectsLeadingDashOptionInjection(t *testing.T) {
	exec := &mockHerdrMatrixExec{
		listOut: `{"result":{"agents":[{"pane_id":"-evil","name":"--help","agent":"codex","agent_status":"idle"}]}}`,
	}
	ctrl := NewHerdrController(exec)

	if res, err := ctrl.FocusPane(context.Background(), "--help"); err == nil || res.Code != "invalid_target" {
		t.Fatalf("FocusPane leading dash: res=%+v err=%v", res, err)
	}
	if res, err := ctrl.FocusPane(context.Background(), " -f "); err == nil || res.Code != "invalid_target" {
		t.Fatalf("FocusPane padded leading dash: res=%+v err=%v", res, err)
	}
	if res, err := ctrl.FocusPane(context.Background(), "w4:p1\n--evil"); err == nil || res.Code != "invalid_target" {
		t.Fatalf("FocusPane newline injection: res=%+v err=%v", res, err)
	}
	if res, err := ctrl.FocusWorkspace(context.Background(), "--all"); err == nil || res.Code != "invalid_workspace" {
		t.Fatalf("FocusWorkspace leading dash: res=%+v err=%v", res, err)
	}
	if res, err := ctrl.RenameAgent(context.Background(), "--target", "safe-name"); err == nil || res.Code != "invalid_target" {
		t.Fatalf("RenameAgent leading dash target: res=%+v err=%v", res, err)
	}
	if res, err := ctrl.RenameAgent(context.Background(), "w4:p1", "--force"); err == nil || res.Code != "invalid_name" {
		t.Fatalf("RenameAgent leading dash name: res=%+v err=%v", res, err)
	}
	if res, err := ctrl.RenameAgent(context.Background(), "w4:p1", "\n--force"); err == nil || res.Code != "invalid_name" {
		t.Fatalf("RenameAgent newline+dash name: res=%+v err=%v", res, err)
	}
	if res, err := ctrl.SendPrompt(context.Background(), SendPromptRequest{Target: "-evil", Message: "hi", Confirmed: true}); err == nil || res.Code != "invalid_target" {
		t.Fatalf("SendPrompt leading dash target: res=%+v err=%v", res, err)
	}
	if res, err := ctrl.StartAgent(context.Background(), "--workspace-evil", "codex", ""); err == nil || res.Code != "invalid_workspace" {
		t.Fatalf("StartAgent leading dash workspace: res=%+v err=%v", res, err)
	}
	if res, err := ctrl.StartAgent(context.Background(), "w4", "codex", "--exec"); err == nil || res.Code != "invalid_name" {
		t.Fatalf("StartAgent leading dash name: res=%+v err=%v", res, err)
	}
	if res, err := ctrl.WaitState(context.Background(), "--timeout", "idle", time.Second); err == nil || res.Code != "invalid_target" {
		t.Fatalf("WaitState leading dash target: res=%+v err=%v", res, err)
	}
	if res, err := ctrl.WaitState(context.Background(), "w4:p1", "--state-evil", time.Second); err == nil || res.Code != "invalid_state" {
		t.Fatalf("WaitState leading dash state: res=%+v err=%v", res, err)
	}
	if len(exec.calls) != 0 {
		t.Fatalf("option injection attempts must never invoke herdr CLI, got calls=%v", exec.calls)
	}
}
