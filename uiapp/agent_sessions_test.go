package uiapp

import (
	"context"
	"strings"
	"sync"
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

type fakeAgentFocuser struct {
	available      bool
	result         host.OperationResult
	mu             sync.Mutex
	availableCalls int
	focusCalls     int
	focusedID      string
}

func (f *fakeAgentFocuser) Name() string { return "fake-herdr" }
func (f *fakeAgentFocuser) Available(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.availableCalls++
	return f.available, nil
}
func (f *fakeAgentFocuser) Focus(_ context.Context, sessionID string) (host.OperationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.focusCalls++
	f.focusedID = sessionID
	return f.result, nil
}

func renderAgentToast(t *testing.T, message string) *ui.Tester {
	t.Helper()
	tester := ui.NewTester(func(c *ui.Context) { ui.Text(c, message).Key("agent-focus-toast") }, 420, 48)
	tester.Frame()
	return tester
}

func TestAgentFocusUnavailableCopiesWithHonestToast(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	var copied, toast string
	ctrl.mu.Lock()
	ctrl.ClipboardWriter = func(value string) { copied = value }
	ctrl.OnShowIMEPill = func(message string, _ *host.CursorAnchor) { toast = message }
	ctrl.mu.Unlock()
	restore := setAgentFocuserForTest(&fakeAgentFocuser{available: false})
	defer restore()
	if !ctrl.agentItemAction(engine.OmniboxItem{UtilityValue: "agent-session:codex:sess-1"}) {
		t.Fatal("session item was not handled")
	}
	// The action needs a matching in-memory session before it can focus/copy.
	ctrl.agentMu.Lock()
	ctrl.agentSessions = []agentsession.Session{{ID: "sess-1", Provider: "codex"}}
	ctrl.agentMu.Unlock()
	ctrl.agentItemAction(engine.OmniboxItem{UtilityValue: "agent-session:codex:sess-1"})
	waitUntil(t, time.Second, func() bool { return strings.Contains(toast, "无法直接跳转") }, "honest unavailable focus toast")
	if copied != "sess-1" {
		t.Fatalf("copied=%q", copied)
	}
	if !renderAgentToast(t, toast).HasText("无法直接跳转") {
		t.Fatalf("toast=%q", toast)
	}
}

func TestAgentFocusSuccessAndUnknownUseThreeHonestBranches(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result host.OperationResult
		want   string
	}{
		{name: "success", result: host.OperationResult{State: host.StateReady}, want: "已跳转到该会话"},
		{name: "unknown", result: host.OperationResult{State: host.StateUnknown, Retryable: false}, want: "未找到该会话的面板"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl, _, _, _ := newTestController(t, nil)
			var toast string
			ctrl.mu.Lock()
			ctrl.OnShowIMEPill = func(message string, _ *host.CursorAnchor) { toast = message }
			ctrl.mu.Unlock()
			fake := &fakeAgentFocuser{available: true, result: tc.result}
			restore := setAgentFocuserForTest(fake)
			defer restore()
			ctrl.agentMu.Lock()
			ctrl.agentSessions = []agentsession.Session{{ID: "sess-2", Provider: "codex"}}
			ctrl.agentMu.Unlock()
			ctrl.ClipboardWriter = func(string) {}
			ctrl.agentItemAction(engine.OmniboxItem{UtilityValue: "agent-session:codex:sess-2"})
			waitUntil(t, time.Second, func() bool { return strings.Contains(toast, tc.want) }, "focus branch toast")
			if !renderAgentToast(t, toast).HasText(tc.want) {
				t.Fatalf("toast=%q", toast)
			}
			fake.mu.Lock()
			if fake.focusCalls != 1 || fake.focusedID != "sess-2" {
				t.Fatalf("focusCalls=%d focusedID=%q", fake.focusCalls, fake.focusedID)
			}
			fake.mu.Unlock()
		})
	}
}

func TestAgentDiagnosticTextIsRedactedBeforeUI(t *testing.T) {
	items := agentItems(nil, []agentsession.Diagnostic{{Provider: "codex", Code: "read_failed", Message: "token=sk-abcdefghijklmnopqrstuvwxyz /Users/alice/private"}}, "")
	if len(items) < 1 || strings.Contains(items[len(items)-1].Subtitle, "sk-abcdefghijklmnopqrstuvwxyz") || strings.Contains(items[len(items)-1].Subtitle, "/Users/alice") {
		t.Fatalf("diagnostic leaked secret/path: %+v", items)
	}
}

func TestAgentSwitchTabTriggersRefreshAndRendersPreview(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	// Let NewAppController's startup refresh settle first so we prove
	// EventSwitchTab itself triggers a fresh refresh.
	waitUntil(t, 3*time.Second, func() bool {
		res := ctrl.agentRecordedOperationResult()
		return !ctrl.agentRefreshing() && res.State != "" && res.State != host.StateRefreshing
	}, "startup agent refresh should settle")
	initialObserved := ctrl.agentRecordedOperationResult().ObservedAt

	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	ctrl.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: config.TabAgent})
	waitUntil(t, 3*time.Second, func() bool {
		res := ctrl.agentRecordedOperationResult()
		return !ctrl.agentRefreshing() && res.ObservedAt.After(initialObserved)
	}, "switching to Agent tab should trigger and finish a fresh session refresh")

	ctrl.agentMu.Lock()
	ctrl.agentSessions = []agentsession.Session{{
		ID:             "cx-preview-1",
		Provider:       "codex",
		Title:          "Frontend Polish",
		Status:         agentsession.StatusRunning,
		LastActivityAt: time.Now(),
		Workspace:      "/workspace/clavis",
		LastMessage:    "Preview pane renders session details",
		PendingReview:  true,
	}}
	ctrl.agentDiagnostics = nil
	setAgentOperationResult(ctrl, host.OperationResult{State: host.StateReady, ObservedAt: time.Now()})
	ctrl.agentMu.Unlock()

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	if !tt.HasText("Preview pane renders session details") {
		t.Fatalf("agent preview body missing last message: %v", tt.Texts())
	}
	if !tt.HasText("ID: cx-preview-1") {
		t.Fatalf("agent preview body missing session ID: %v", tt.Texts())
	}
	if !tt.HasText("R") {
		t.Fatalf("agent footer missing refresh shortcut hint: %v", tt.Texts())
	}
}

func TestAgentVisibleItemsIsolatesControllerResults(t *testing.T) {
	ctrlA := &AppController{}
	ctrlB := &AppController{}
	setAgentOperationResult(ctrlA, host.OperationResult{State: host.StateReady, ObservedAt: time.Now()})
	setAgentOperationResult(ctrlB, host.OperationResult{State: host.StateFailed, Message: "Codex 数据源不可用", Retryable: true})
	itemsA := ctrlA.agentVisibleItems("")
	if len(itemsA) != 0 {
		t.Fatalf("ctrlA leaked ctrlB failure result: %+v", itemsA)
	}
	itemsB := ctrlB.agentVisibleItems("")
	if len(itemsB) != 1 || itemsB[0].UtilityValue != "agent-refresh-status" {
		t.Fatalf("ctrlB missing its own failure status row: %+v", itemsB)
	}
}
