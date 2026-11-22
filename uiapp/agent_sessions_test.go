package uiapp

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
)

func TestAgentItemsConversationAndCommandCompletion(t *testing.T) {
	now := time.Now()
	sessions := []agentsession.Session{{ID: "cx-1", Provider: "codex", Title: "输入法验收", Status: agentsession.StatusRunning, LastActivityAt: now, LastMessage: "请运行 go test", PendingReview: true, Workspace: "clavis"}}
	items := agentItems(sessions, nil, "验收")
	if len(items) != 1 || items[0].Title != "输入法验收" || items[0].ID != "agent-session:codex:cx-1" {
		t.Fatalf("conversation completion = %+v", items)
	}
	commands := agentItems(sessions, nil, "/测")
	if len(commands) != 1 || commands[0].UtilityValue != "agent-command:test" {
		t.Fatalf("command completion = %+v", commands)
	}
}

func TestAgentTabRendersCachedSessionAndDiagnostic(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.agentMu.Lock()
	ctrl.agentRefreshStarted = true
	ctrl.agentSessions = []agentsession.Session{{ID: "dsh-1", Provider: "dsh", Title: "DSH 会话", Status: agentsession.StatusWaiting, LastActivityAt: time.Now(), PendingReview: true}}
	ctrl.agentDiagnostics = []agentsession.Diagnostic{{Provider: "claude", Code: "unavailable", Message: "projects directory was not found"}}
	ctrl.agentMu.Unlock()
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabAgent})
	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	if !tt.HasText("DSH 会话") || !tt.HasText("DSH") || !tt.HasText("待验收") || !tt.HasText("诊断") {
		t.Fatalf("agent tab did not render cached session and diagnostic: %v", tt.Texts())
	}
	if rect, ok := tt.Find("DSH"); !ok || rect.W <= 0 || rect.H <= 0 {
		t.Fatalf("provider badge has no rendered bounds: %v %v", rect, ok)
	} else {
		t.Logf("Agent provider badge DSH bounds: x=%.1f y=%.1f w=%.1f h=%.1f", rect.X, rect.Y, rect.W, rect.H)
	}
}

func TestAgentTabShowsLoadingStateDuringRefresh(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.agentMu.Lock()
	ctrl.agentRefreshStarted = true
	ctrl.agentMu.Unlock()
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabAgent})
	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	if !tt.HasText("正在加载 Agent 会话…") {
		t.Fatalf("loading state missing: %v", tt.Texts())
	}
}

func TestAgentStatusMessageRendersFailureStateWithTester(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	result := host.OperationResult{State: host.StateFailed, Message: "Codex 数据源不可用", Retryable: true}
	setAgentOperationResult(ctrl, result)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Text(c, agentStatusMessage(result)).Key("agent-refresh-status")
	}, 260, 40)
	tt.Frame()
	if !tt.HasText("Codex 数据源不可用") {
		t.Fatalf("status text missing: %v", tt.Texts())
	}
}

func TestAgentOperationResultStatesAndUnknownDoesNotRetry(t *testing.T) {
	for _, tc := range []struct {
		name        string
		sessions    int
		diagnostics int
		hadCache    bool
		want        host.OperationState
	}{
		{"ready", 1, 0, false, host.StateReady},
		{"degraded", 1, 1, true, host.StateDegraded},
		{"failed", 0, 1, false, host.StateFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessions := make([]agentsession.Session, tc.sessions)
			diagnostics := make([]agentsession.Diagnostic, tc.diagnostics)
			got := agentOperationResult(sessions, diagnostics, tc.hadCache)
			if got.State != tc.want {
				t.Fatalf("state=%s want %s", got.State, tc.want)
			}
		})
	}
	unknown := host.OperationResult{State: host.StateUnknown, Retryable: false}
	if unknown.Retryable || agentStatusMessage(unknown) != "状态未知" {
		t.Fatalf("unknown result=%+v", unknown)
	}
}

func TestAgentDiagnosticTextIsRedactedBeforeUI(t *testing.T) {
	items := agentItems(nil, []agentsession.Diagnostic{{Provider: "codex", Code: "read_failed", Message: "token=sk-abcdefghijklmnopqrstuvwxyz /Users/alice/private"}}, "")
	if len(items) < 1 || strings.Contains(items[len(items)-1].Subtitle, "sk-abcdefghijklmnopqrstuvwxyz") || strings.Contains(items[len(items)-1].Subtitle, "/Users/alice") {
		t.Fatalf("diagnostic leaked secret/path: %+v", items)
	}
}
