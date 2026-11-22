package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// TestLeaderSubpageHeightFollowsItsNodes opens the Leader and steps into the
// "a" group (AI rewrite, two actions): the size the app gives the window
// follows the group's node count, and a real render at that size has no
// empty band under the last row.
func TestLeaderSubpageHeightFollowsItsNodes(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader, Context: &engine.CapsuleContext{AppName: "Xcode"}})
	appearance := f.ctrl.Config().Main.Appearance
	_, rootH := PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	if want := leaderHeightFor(len(f.ctrl.State().LeaderNodes)); rootH != want {
		t.Fatalf("root height %d, want leaderHeightFor(%d)=%d", rootH, len(f.ctrl.State().LeaderNodes), want)
	}
	f.ctrl.Dispatch(engine.Event{Type: engine.EventLeaderKeyPress, Key: "a"})
	st := f.ctrl.State()
	if st.Phase != engine.PhaseLeader || len(st.LeaderNodes) != 2 {
		t.Fatalf("'a' did not open the two-action AI group: phase=%s nodes=%d", st.Phase, len(st.LeaderNodes))
	}
	w, h := PreferredCapsuleSizeForState(st, appearance)
	if want := leaderHeightFor(2); h != want || h >= rootH {
		t.Fatalf("subpage height %d, want leaderHeightFor(2)=%d (root %d)", h, want, rootH)
	}
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()
	last, ok := tt.Find("Translate EN/ZH")
	footer, ok2 := tt.Find(i18n.T("leader.hint.close"))
	if !ok || !ok2 {
		t.Fatalf("subpage rows or footer missing; texts %q", tt.Texts())
	}
	if gap := footer.Y - (last.Y + last.H); gap > float32(leaderRowHeight) {
		t.Fatalf("%.0fpx of empty panel between the last row and the footer at %dx%d", gap, w, h)
	}
	t.Logf("root %d → subpage %dx%d; last row to footer %.0fpx", rootH, w, h, footer.Y-(last.Y+last.H))
}

// TestLeaderSubpageHasAWayBack renders the Leader at the top and inside the
// AI group. At the top the title is "Leader" and no "⌫ Back" hint promises a
// level that is not there (⌫ closes the panel there). Inside the group a
// back button names the level above and the group is the title; the footer
// says ⌫ goes back; clicking the button returns to the top, where the
// window is sized for its five rows again.
func TestLeaderSubpageHasAWayBack(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader, Context: &engine.CapsuleContext{AppName: "Xcode"}})
	appearance := f.ctrl.Config().Main.Appearance
	back := i18n.T("leader.hint.back")

	w, h := PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	root := ui.NewTester(f.ctrl.CapsuleView, w, h)
	root.Frame()
	if root.HasText(back) {
		t.Fatalf("top level shows %q although ⌫ closes the panel there; texts %q", back, root.Texts())
	}

	f.ctrl.Dispatch(engine.Event{Type: engine.EventLeaderKeyPress, Key: "a"})
	w, h = PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	sub := ui.NewTester(f.ctrl.CapsuleView, w, h)
	sub.Frame()
	for _, want := range []string{back, i18n.T("leader.root"), "AI Rewrite"} {
		if !sub.HasText(want) {
			t.Fatalf("AI group lacks %q; texts %q", want, sub.Texts())
		}
	}
	if err := sub.Click(i18n.T("leader.root")); err != nil {
		t.Fatalf("click the back button: %v", err)
	}
	st := f.ctrl.State()
	if len(st.LeaderPath) != 0 || len(st.LeaderNodes) != 5 {
		t.Fatalf("back button: path=%v nodes=%d, want the top level's 5", st.LeaderPath, len(st.LeaderNodes))
	}
	if _, h2 := PreferredCapsuleSizeForState(st, appearance); h2 != leaderHeightFor(5) {
		t.Fatalf("back at the top, height %d, want leaderHeightFor(5)=%d", h2, leaderHeightFor(5))
	}

}

func TestLeaderCompactRowSelectionUsesFlatBlueMark(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader, Context: &engine.CapsuleContext{AppName: "Xcode"}})
	appearance := f.ctrl.Config().Main.Appearance
	w, h := PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()
	row, ok := tt.Find("leader-row:a")
	if !ok {
		t.Fatal("leader row not found")
	}
	if row.H != leaderCardHeight {
		t.Fatalf("leader row height=%.1f, want 38", row.H)
	}
	tt.Move(row.X+row.W/2, row.Y+row.H/2)
	bar := tt.Image().At(int(row.X+1), int(row.Y+row.H/2))
	fill := tt.Image().At(int(row.X+10), int(row.Y+row.H/2))
	br, bg, bb, ba := bar.RGBA()
	if uint8(br>>8) != 10 || uint8(bg>>8) != 132 || uint8(bb>>8) != 255 || uint8(ba>>8) != 255 {
		t.Fatalf("leader selection bar=%#v, want #0A84FF", bar)
	}
	expected := leaderSelectionFill(ui.LightTheme()).Over(ui.LightTheme().Background)
	fr, fg, fb, fa := fill.RGBA()
	if uint8(fr>>8) != expected.R || uint8(fg>>8) != expected.G || uint8(fb>>8) != expected.B || uint8(fa>>8) != expected.A {
		t.Fatalf("leader selection fill=%#v, want %#v", fill, expected)
	}
}

func TestLeaderCompactHeightsRenderAtFormulaValues(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	for count, want := range map[int]int{1: 140, 2: 168, 5: 288, 9: 438} {
		nodes := make([]config.LeaderNode, count)
		for i := range nodes {
			nodes[i] = config.LeaderNode{Key: string(rune('a' + i)), Label: "Action"}
		}
		state := engine.State{Phase: engine.PhaseLeader, LeaderNodes: nodes}
		tt := ui.NewTester(func(c *ui.Context) {
			ctrl.renderLeaderView(c, ui.LightTheme(), state)
		}, 680, want)
		tt.Frame()
		view, ok := tt.Find("leader-view")
		if !ok || int(view.H) != want {
			t.Fatalf("count=%d rendered leader height=%.1f ok=%v, want %d", count, view.H, ok, want)
		}
	}
}
