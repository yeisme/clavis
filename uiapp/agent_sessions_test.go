package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
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
	if !tt.HasText("DSH 会话") || !tt.HasText("待验收") || !tt.HasText("诊断") {
		t.Fatalf("agent tab did not render cached session and diagnostic: %v", tt.Texts())
	}
}
