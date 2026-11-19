package engine

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

// TestHoverEventSelectsWithoutEffects pins the pointer-hover contract: moving
// the pointer over a row only moves the selection. It must never emit a copy,
// paste, pin or delete effect, and out-of-range indexes are ignored.
func TestHoverEventSelectsWithoutEffects(t *testing.T) {
	m := NewStateMachine(config.DefaultConfig())
	m.Handle(Event{Type: EventOpenOmnibox, Tab: config.TabClipboard})
	m.Handle(Event{
		Type: EventSetClipboardEntries,
		ClipboardEntries: []OmniboxClipboardEntry{
			{ID: "a", Content: "first"},
			{ID: "b", Content: "second"},
			{ID: "c", Content: "third"},
		},
	})
	items := m.State().OmniboxItems
	if len(items) < 3 {
		t.Fatalf("expected three items, got %d", len(items))
	}

	effects := m.Handle(Event{Type: EventHoverItem, Index: 2})
	if len(effects) != 0 {
		t.Fatalf("hover emitted effects: %+v", effects)
	}
	if got := m.State().SelectedIndex; got != 2 {
		t.Fatalf("hover selected index = %d, want 2", got)
	}

	// A repeated hover on the same index is a no-op with no effects.
	if effects := m.Handle(Event{Type: EventHoverItem, Index: 2}); len(effects) != 0 {
		t.Fatalf("repeated hover emitted effects: %+v", effects)
	}

	for _, index := range []int{-1, 3, 99} {
		before := m.State().SelectedIndex
		if effects := m.Handle(Event{Type: EventHoverItem, Index: index}); len(effects) != 0 {
			t.Fatalf("out-of-range hover %d emitted effects: %+v", index, effects)
		}
		if got := m.State().SelectedIndex; got != before {
			t.Fatalf("out-of-range hover %d moved selection to %d", index, got)
		}
	}

	// Hover outside the Omnibox must not touch selection.
	m.Handle(Event{Type: EventDismiss})
	if effects := m.Handle(Event{Type: EventHoverItem, Index: 1}); len(effects) != 0 {
		t.Fatalf("hover outside omnibox emitted effects: %+v", effects)
	}
}
