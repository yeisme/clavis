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

func settleAgentTabForTest(t *testing.T, ctrl *AppController, sessions []agentsession.Session, diagnostics []agentsession.Diagnostic, res host.OperationResult) {
	t.Helper()
	waitUntil(t, 3*time.Second, func() bool {
		r := ctrl.agentRecordedOperationResult()
		return !ctrl.agentRefreshing() && r.State != "" && r.State != host.StateRefreshing
	}, "startup agent refresh should settle")
	before := ctrl.agentRecordedOperationResult().ObservedAt
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabAgent})
	waitUntil(t, 3*time.Second, func() bool {
		r := ctrl.agentRecordedOperationResult()
		return !ctrl.agentRefreshing() && r.ObservedAt.After(before)
	}, "open agent tab refresh should settle")
	ctrl.agentMu.Lock()
	ctrl.agentSessions = append([]agentsession.Session(nil), sessions...)
	ctrl.agentDiagnostics = append([]agentsession.Diagnostic(nil), diagnostics...)
	setAgentOperationResult(ctrl, res)
	ctrl.agentMu.Unlock()
	ctrl.mu.Lock()
	ctrl.uiState.agentSelected = 0
	ctrl.mu.Unlock()
}

func TestAgentWorkbenchShowsTracksConclusionApprovalAndCollapsibleLogDrawer(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	now := time.Now()
	sessions := []agentsession.Session{
		{
			ID:             "cx-1",
			Provider:       "codex",
			Title:          "Build UI",
			Status:         agentsession.StatusRunning,
			LastActivityAt: now,
			LastMessage:    "Running tests with token=sk-secret99999999",
			Workspace:      "/Users/alice/private/clavis",
		},
		{
			ID:             "dsh-2",
			Provider:       "dsh",
			Title:          "Review PR",
			Status:         agentsession.StatusWaiting,
			PendingReview:  true,
			LastActivityAt: now.Add(-5 * time.Minute),
			LastMessage:    "Ready for human sign-off",
			Workspace:      "/Users/alice/private/clavis",
		},
		{
			ID:             "cl-3",
			Provider:       "claude",
			Title:          "Audit Docs",
			Status:         agentsession.StatusStopped,
			LastActivityAt: now.Add(-20 * time.Minute),
			LastMessage:    "All checks green",
			Workspace:      "/Users/alice/private/clavis",
		},
	}

	settleAgentTabForTest(t, ctrl, sessions, nil, host.OperationResult{State: host.StateReady, ObservedAt: now})

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()

	// Parallel track counts visible immediately without opening the drawer.
	if _, ok := tt.Find("agent-workbench-band"); !ok {
		t.Fatalf("workbench track band missing: %v", tt.Texts())
	}
	if !tt.HasText("All 3") || !tt.HasText("Running 1") || !tt.HasText("Approval 1") || !tt.HasText("Done 1") {
		t.Fatalf("lane summary counts missing: %v", tt.Texts())
	}
	// Secret must be redacted in visible conclusion; log drawer stays closed by default.
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "sk-secret99999999") || strings.Contains(txt, "/Users/alice") {
			t.Fatalf("leaked sensitive text before drawer open: %q", txt)
		}
	}
	if _, ok := tt.Find("agent-log-drawer-body"); ok {
		t.Fatal("log drawer should be collapsed by default")
	}

	// Open the log drawer and verify workspace/path remains redacted inside it too.
	if err := tt.Click("Log drawer · 2"); err != nil {
		t.Fatalf("click log drawer toggle: %v (texts=%v)", err, tt.Texts())
	}
	tt.Frame()
	if _, ok := tt.Find("agent-log-drawer-body"); !ok {
		t.Fatalf("log drawer body did not open: %v", tt.Texts())
	}
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "/Users/alice") || strings.Contains(txt, "sk-secret99999999") {
			t.Fatalf("log drawer leaked raw path or secret: %q", txt)
		}
	}

	// Filter to Approval lane and confirm blocking approval gate renders.
	if err := tt.Click("Approval 1"); err != nil {
		t.Fatalf("click Approval lane chip: %v", err)
	}
	tt.Frame()
	if _, ok := tt.Find("agent-approval-gate"); !ok {
		t.Fatalf("approval blocking gate missing on Approval lane: %v", tt.Texts())
	}
	if !tt.HasText("Ready for human sign-off") {
		t.Fatalf("approval conclusion missing: %v", tt.Texts())
	}

	// Filter to Done lane and confirm completed card renders.
	if err := tt.Click("Done 1"); err != nil {
		t.Fatalf("click Done lane chip: %v", err)
	}
	tt.Frame()
	if _, ok := tt.Find("agent-done-card"); !ok {
		t.Fatalf("done state card missing on Done lane: %v", tt.Texts())
	}
	if !tt.HasText("All checks green") {
		t.Fatalf("done conclusion missing: %v", tt.Texts())
	}
}

func TestAgentWorkbenchErrorStateRedactsAndExposesRetry(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	now := time.Now()
	settleAgentTabForTest(t, ctrl, nil, []agentsession.Diagnostic{
		{Provider: "codex", Code: "read_failed", Message: "bearer supersecret999 /Users/bob/.codex/state.db"},
	}, host.OperationResult{
		State:      host.StateFailed,
		Code:       "read_failed",
		Message:    "Codex 数据源不可用",
		Retryable:  true,
		ObservedAt: now,
	})

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()

	if !tt.HasText("Retry Refresh") {
		t.Fatalf("error state missing Retry Refresh button: %v", tt.Texts())
	}
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "supersecret999") || strings.Contains(txt, "/Users/bob") {
			t.Fatalf("error state leaked secret/path: %q", txt)
		}
	}
}
