package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
)

// TestLeaderRailBlocksNeverOverlap renders the Leader root with two recent
// activities (an Agent session awaiting review and a clipboard preview) at the
// size the app gives the panel. The rail used to let "Recent activity" shrink
// without clipping, so its second card was drawn over the quick tools; now the
// blocks stack in order inside the rail and the quick tools keep their row.
func TestLeaderRailBlocksNeverOverlap(t *testing.T) {
	f := newInteractionFixture(t, nil)
	now := time.Now()
	waitUntil(t, 3*time.Second, func() bool {
		r := f.ctrl.agentRecordedOperationResult()
		return !f.ctrl.agentRefreshing() && r.State != "" && r.State != host.StateRefreshing
	}, "startup agent refresh settle")
	f.ctrl.agentMu.Lock()
	f.ctrl.agentSessions = []agentsession.Session{{
		ID: "rail-1", Provider: "dsh", Title: "Five-state AI center review",
		Status: agentsession.StatusWaiting, PendingReview: true,
		LastActivityAt: now.Add(-4 * time.Minute), LastMessage: "snapshots done, waiting for review",
	}}
	setAgentOperationResult(f.ctrl, host.OperationResult{State: host.StateReady, ObservedAt: now})
	f.ctrl.agentMu.Unlock()

	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader, Context: &engine.CapsuleContext{
		AppName: "Visual Studio Code", ClipboardPreview: "go test ./uiapp/... -count=1",
	}})
	w, h := PreferredCapsuleSizeForState(f.ctrl.State(), f.ctrl.Config().Main.Appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()

	find := func(key string) ui.Rect {
		t.Helper()
		r, ok := tt.Find(key)
		if !ok {
			t.Fatalf("missing %q; texts %q", key, tt.Texts())
		}
		return ui.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H}
	}
	rail := find("leader-dashboard-rail")
	order := []string{"leader-workbenches", "leader-system-status", "leader-recent-activity", "leader-quick-tools"}
	prevBottom := rail.Y
	for _, key := range order {
		r := find(key)
		if r.Y+0.5 < prevBottom {
			t.Fatalf("%s starts at y=%.1f, above the end of the block before it (y=%.1f): blocks overlap", key, r.Y, prevBottom)
		}
		if r.Y+r.H > rail.Y+rail.H+0.5 {
			t.Fatalf("%s ends at y=%.1f, below the rail's bottom (y=%.1f)", key, r.Y+r.H, rail.Y+rail.H)
		}
		prevBottom = r.Y + r.H
	}
}

// TestLeaderRailActivityRowsFit pins the fit: the five-row root has room for
// one activity line, a taller panel shows more, and a rail too short for any
// hides the block rather than squeezing it.
func TestLeaderRailActivityRowsFit(t *testing.T) {
	root := leaderRailHeight(float32(leaderHeightFor(5)))
	if got := leaderRailActivityRows(root, 2); got != 1 {
		t.Fatalf("five-row root fits %d activity lines, want 1 (rail %.0fpt)", got, root)
	}
	if got := leaderRailActivityRows(leaderRailHeight(float32(leaderHeightFor(8))), 2); got != 2 {
		t.Fatalf("eight-row panel fits %d activity lines, want 2", got)
	}
	if got := leaderRailActivityRows(leaderRailHeight(float32(leaderHeightFor(3))), 2); got != 0 {
		t.Fatalf("three-row panel fits %d activity lines, want 0", got)
	}
	if got := leaderRailActivityRows(root, 0); got != 0 {
		t.Fatalf("no activities gave %d lines", got)
	}
}

// TestAgentWorkspaceStripFolds keeps a long workspace list to a strip: six
// chips and a "+N" count while collapsed — the active filter always among
// them — and every chip once opened.
func TestAgentWorkspaceStripFolds(t *testing.T) {
	var all []agentsession.HerdrWorkspace
	for i := 0; i < 20; i++ {
		id := "ws-" + string(rune('a'+i))
		all = append(all, agentsession.HerdrWorkspace{WorkspaceID: id, Label: id})
	}
	shown, hidden := visibleAgentWorkspaces(all, "", false)
	if len(shown) != agentWorkspaceChipLimit || hidden != len(all)-agentWorkspaceChipLimit {
		t.Fatalf("collapsed strip shows %d (+%d), want %d (+%d)", len(shown), hidden, agentWorkspaceChipLimit, len(all)-agentWorkspaceChipLimit)
	}
	shown, _ = visibleAgentWorkspaces(all, "ws-q", false)
	found := false
	for _, ws := range shown {
		found = found || ws.WorkspaceID == "ws-q"
	}
	if !found {
		t.Fatal("the active workspace folded behind +N")
	}
	if shown, hidden = visibleAgentWorkspaces(all, "", true); len(shown) != len(all) || hidden != 0 {
		t.Fatalf("open strip shows %d (+%d), want all %d", len(shown), hidden, len(all))
	}
	if shown, hidden = visibleAgentWorkspaces(all[:3], "", false); len(shown) != 3 || hidden != 0 {
		t.Fatalf("short list folded: %d (+%d)", len(shown), hidden)
	}
}
