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
