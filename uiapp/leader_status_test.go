package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// TestLeaderASCIILockChipIsNeutral renders the Leader while it holds the
// temporary ASCII lock: the "ASCII locked" chip in the footer is a status,
// not an action, so no pixel of it may be painted in the accent.
func TestLeaderASCIILockChipIsNeutral(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader, Context: &engine.CapsuleContext{AppName: "Xcode"}})
	appearance := f.ctrl.Config().Main.Appearance
	f.ctrl.mu.Lock()
	f.ctrl.imeLockedForCapsule = true
	f.ctrl.mu.Unlock()
	w, h := PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	locked := ui.NewTester(f.ctrl.CapsuleView, w, h)
	locked.Frame()
	chipText, ok := locked.Find(i18n.T("leader.ascii_locked"))
	if !ok {
		t.Fatalf("ASCII-lock chip missing; texts %q", locked.Texts())
	}
	if n := accentPixels(locked, int(chipText.X)-int(space24), int(chipText.Y)-int(space4), int(chipText.X+chipText.W)+int(space8), int(chipText.Y+chipText.H)+int(space4), ui.LightTheme().Accent); n != 0 {
		t.Fatalf("ASCII-lock chip paints %d accent pixels; it is a status, not an action", n)
	}
}
