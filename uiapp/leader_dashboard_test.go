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

func TestLeaderDashboardRendersRealDataAndSupportsNumericTabEscSearch(t *testing.T) {
	f := newInteractionFixture(t, nil)
	now := time.Now()
	// Let startup refresh settle, then seed deterministic real Agent session.
	waitUntil(t, 3*time.Second, func() bool {
		r := f.ctrl.agentRecordedOperationResult()
		return !f.ctrl.agentRefreshing() && r.State != "" && r.State != host.StateRefreshing
	}, "startup agent refresh settle")
	f.ctrl.agentMu.Lock()
	f.ctrl.agentSessions = []agentsession.Session{{
		ID:             "cx-dash-1",
		Provider:       "codex",
		Title:          "Leader Dashboard Review",
		Status:         agentsession.StatusWaiting,
		PendingReview:  true,
		LastActivityAt: now.Add(-2 * time.Minute),
		LastMessage:    "token=sk-secretvalue999999 ready for review",
	}}
	f.ctrl.agentDiagnostics = nil
	setAgentOperationResult(f.ctrl, host.OperationResult{State: host.StateReady, ObservedAt: now})
	f.ctrl.agentMu.Unlock()

	ctx := &engine.CapsuleContext{
		AppName:          "Visual Studio Code",
		SelectedText:     "func demo() {}",
		ClipboardPreview: "git status --short",
	}
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader, Context: ctx})
	w, h := PreferredCapsuleSizeForState(f.ctrl.State(), f.ctrl.Config().Main.Appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()

	for _, key := range []string{"leader-dashboard-rail", "leader-workbenches", "leader-system-status", "leader-recent-activity"} {
		if _, ok := tt.Find(key); !ok {
			t.Fatalf("missing dashboard region %q: %v", key, tt.Texts())
		}
	}
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "sk-secretvalue999999") {
			t.Fatalf("leader recent activity leaked secret: %q", txt)
		}
	}

	// Digit '6' jumps directly to Agent workbench.
	tt.Key(0, ui.Key6)
	tt.Frame()
	if st := f.ctrl.State(); st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabAgent {
		t.Fatalf("digit 6 did not jump to Agent tab: phase=%s tab=%s", st.Phase, st.OmniboxTab)
	}

	// Re-open Leader; Tab jumps to Omnibox search.
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader, Context: ctx})
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if st := f.ctrl.State(); st.Phase != engine.PhaseOmnibox || st.OmniboxTab != config.TabActions {
		t.Fatalf("Tab did not jump to Omnibox Actions: phase=%s tab=%s", st.Phase, st.OmniboxTab)
	}

	// Re-open Leader; '/' downgrades to Omnibox search.
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader, Context: ctx})
	tt.Frame()
	tt.Key(0, ui.KeySlash)
	tt.Frame()
	if st := f.ctrl.State(); st.Phase != engine.PhaseOmnibox {
		t.Fatalf("'/' did not open Omnibox search: phase=%s", st.Phase)
	}

	// Re-open Leader; Esc closes cleanly.
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader, Context: ctx})
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if st := f.ctrl.State(); st.Phase != engine.PhaseHidden {
		t.Fatalf("Esc at Leader root did not close: phase=%s", st.Phase)
	}
}

func TestLeaderDashboardSubpageCollapsesRail(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader, Context: &engine.CapsuleContext{AppName: "Xcode"}})
	f.ctrl.Dispatch(engine.Event{Type: engine.EventLeaderKeyPress, Key: "a"})
	w, h := PreferredCapsuleSizeForState(f.ctrl.State(), f.ctrl.Config().Main.Appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()
	if _, ok := tt.Find("leader-dashboard-rail"); ok {
		t.Fatal("subpage should collapse the right dashboard rail to keep compact subpage geometry")
	}
}
