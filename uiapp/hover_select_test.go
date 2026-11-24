package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// TestOmniboxHoverSelectsWithoutConfirming is the contract for pointer hover:
// moving the mouse over a clipboard row selects it and moves the preview, but
// never copies, pastes or records a use.
func TestOmniboxHoverSelectsWithoutConfirming(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.record(t, "hover-a", "hover-b", "hover-c")
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	tt := f.omniboxTester(t)

	items := f.ctrl.State().OmniboxItems
	if len(items) < 3 {
		t.Fatalf("expected at least three clipboard rows, got %d", len(items))
	}
	if got := f.ctrl.State().SelectedIndex; got != 0 {
		t.Fatalf("selected index = %d, want 0 before hovering", got)
	}

	rect, ok := tt.Find(items[2].Title)
	if !ok {
		t.Fatalf("row %q not found; texts=%v", items[2].Title, tt.Texts())
	}
	tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.Frame()

	waitUntil(t, 2*time.Second, func() bool {
		return f.ctrl.State().SelectedIndex == 2
	}, "hover selects the third row")

	if got := f.ctrl.State().OmniboxItems[2].ClipboardContent; got != "hover-a" && got != "hover-b" && got != "hover-c" {
		t.Fatalf("unexpected hovered content %q", got)
	}
	if copied := f.copiedTexts(); len(copied) != 0 {
		t.Fatalf("hover copied to the clipboard: %v", copied)
	}
	if injected := f.bridge.getInjected(); len(injected) != 0 {
		t.Fatalf("hover pasted into the app: %+v", injected)
	}
	if used := f.usedItemIDs(); len(used) != 0 {
		t.Fatalf("hover recorded item use: %v", used)
	}
	if got := f.ctrl.State().Phase; got != engine.PhaseOmnibox {
		t.Fatalf("hover changed the phase to %s", got)
	}
}

// TestOmniboxHoverDoesNotUndoArrowKeys keeps keyboard navigation authoritative
// while the pointer rests over a row.
func TestOmniboxHoverDoesNotUndoArrowKeys(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.record(t, "arrow-a", "arrow-b", "arrow-c")
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	tt := f.omniboxTester(t)

	items := f.ctrl.State().OmniboxItems
	if len(items) < 2 {
		t.Fatalf("expected at least two rows, got %d", len(items))
	}
	rect, ok := tt.Find(items[0].Title)
	if !ok {
		t.Fatalf("row %q not found", items[0].Title)
	}
	tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.Frame()
	waitUntil(t, 2*time.Second, func() bool { return f.ctrl.State().SelectedIndex == 0 }, "hover selects row 0")

	tt.Key(0, ui.KeyDown)
	tt.Frame()
	waitUntil(t, 2*time.Second, func() bool { return f.ctrl.State().SelectedIndex == 1 }, "arrow down selects row 1")

	// Additional frames must not pull the selection back to the resting pointer.
	tt.Frame()
	tt.Frame()
	if got := f.ctrl.State().SelectedIndex; got != 1 {
		t.Fatalf("resting pointer undid keyboard selection: index = %d, want 1", got)
	}
}

func TestOmniboxKeyboardNavigationIgnoresRestingPointerAcrossScroll(t *testing.T) {
	f := newInteractionFixture(t, nil)
	for i := 0; i < 16; i++ {
		f.record(t, "scroll-row-"+string(rune('a'+i)))
	}
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	tt := f.omniboxTester(t)
	items := f.ctrl.State().OmniboxItems
	if len(items) < 13 {
		t.Fatalf("expected enough rows to cross the viewport, got %d", len(items))
	}
	first, ok := tt.Find(items[0].Title)
	if !ok {
		t.Fatalf("first row %q not found", items[0].Title)
	}
	tt.Move(first.X+first.W/2, first.Y+first.H/2)
	tt.Frame()
	waitUntil(t, 2*time.Second, func() bool { return f.ctrl.State().SelectedIndex == 0 }, "hover selects first row")

	for want := 1; want <= 12; want++ {
		tt.Key(0, ui.KeyDown)
		tt.Frame()
		if got := f.ctrl.State().SelectedIndex; got != want {
			t.Fatalf("step %d selected index = %d, want %d", want, got, want)
		}
	}
	if got := f.ctrl.State().SelectedIndex; got != 12 {
		t.Fatalf("resting pointer bounced selection to %d, want 12", got)
	}
}

func TestOmniboxHoverRearmsAfterTwoPointPointerMove(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.record(t, "rearm-a", "rearm-b", "rearm-c")
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	f.ctrl.mu.Lock()
	f.ctrl.uiState.lastPointerPos = ui.Point{X: 120, Y: 280}
	f.ctrl.uiState.pointerPosKnown = true
	f.ctrl.mu.Unlock()
	f.ctrl.Dispatch(engine.Event{Type: engine.EventSelectNext})
	if got := f.ctrl.State().SelectedIndex; got != 1 {
		t.Fatalf("keyboard navigation selected index = %d, want 1", got)
	}
	f.ctrl.hoverSelect(0, ui.Point{X: 121, Y: 280})
	if got := f.ctrl.State().SelectedIndex; got != 1 {
		t.Fatalf("one-point pointer move reselected index %d, want 1", got)
	}
	f.ctrl.hoverSelect(2, ui.Point{X: 122, Y: 280})
	if got := f.ctrl.State().SelectedIndex; got != 2 {
		t.Fatalf("two-point pointer move selected index %d, want 2", got)
	}
}

func TestOmniboxRowKeyUsesStableItemID(t *testing.T) {
	first := engine.OmniboxItem{ID: "clipboard:first", Title: "same title"}
	second := engine.OmniboxItem{ID: "clipboard:second", Title: "same title"}
	if got := omniboxRowKey(first); got != "omnibox-row:clipboard:first" {
		t.Fatalf("first row key = %q, want item ID based key", got)
	}
	if got := omniboxRowKey(second); got != "omnibox-row:clipboard:second" {
		t.Fatalf("second row key = %q, want item ID based key", got)
	}
	if omniboxRowKey(first) == omniboxRowKey(second) {
		t.Fatal("rows with distinct item IDs must not share a key")
	}
}

// TestOmniboxClickConfirmsHoveredRow checks that the click path still confirms
// only the row the pointer is on.
func TestOmniboxClickConfirmsHoveredRow(t *testing.T) {
	f := newInteractionFixture(t, nil)
	f.record(t, "click-a", "click-b")
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	tt := f.omniboxTester(t)

	items := f.ctrl.State().OmniboxItems
	if len(items) < 2 {
		t.Fatalf("expected two rows, got %d", len(items))
	}
	want := items[1]
	if err := tt.Click(want.Title); err != nil {
		t.Fatalf("click %q: %v", want.Title, err)
	}
	waitUntil(t, 2*time.Second, func() bool { return len(f.copiedTexts()) == 1 }, "click copies the row")
	if got := f.copiedTexts()[0]; got != want.ClipboardContent {
		t.Fatalf("click copied %q, want %q", got, want.ClipboardContent)
	}
}
