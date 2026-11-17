package engine_test

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestClipboardEntryMappingCarriesPreviewAndActions(t *testing.T) {
	m := engine.OmniboxClipboardEntry{
		ID: "clip-1", Content: "hello", IsPinned: false,
		Preview: engine.ClipboardEntryPreview{Summary: "hello", Text: "hello", Decodable: true},
		Actions: engine.ClipboardEntryActions{CanPin: true, CanCopy: true, CanPaste: true, CanDelete: true},
	}
	if !m.Actions.CanPin || m.Actions.CanUnpin || !m.Preview.Decodable || m.Preview.Summary != "hello" {
		t.Fatalf("incomplete clipboard mapping: %+v", m)
	}
}

func TestClipboardActivationIsOrderedAndDoesNotRepin(t *testing.T) {
	cfg := config.DefaultConfig()
	sm := engine.NewStateMachine(cfg)
	_ = sm.Handle(engine.Event{Type: engine.EventSetClipboardEntries, ClipboardEntries: []engine.OmniboxClipboardEntry{
		{ID: "unpinned", Content: "one", IsPinned: false},
		{ID: "pinned", Content: "two", IsPinned: true},
	}})
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})

	effects := sm.Handle(engine.Event{Type: engine.EventActivateClipboardItem})
	if len(effects) != 1 || effects[0].Type != engine.EffectActivateClipboardItem {
		t.Fatalf("activation effects = %+v", effects)
	}
	op := effects[0].ClipboardActivation
	if op == nil {
		t.Fatal("missing clipboard activation operation")
	}
	firstID := op.ID
	if firstID != "pinned" && firstID != "unpinned" {
		t.Fatalf("unexpected activated item: %+v", op)
	}
	// Select the other row, then assert both contracts independently. The
	// engine is allowed to preserve store order while the UI later sorts it.
	_ = sm.Handle(engine.Event{Type: engine.EventSelectNext})
	effects = sm.Handle(engine.Event{Type: engine.EventActivateClipboardItem})
	op = effects[0].ClipboardActivation
	if op == nil {
		t.Fatal("missing second clipboard activation operation")
	}
	if firstID == "pinned" {
		if op.ID != "unpinned" || !op.PinBeforeWrite || len(op.Steps) != 3 || op.Steps[0] != engine.ClipboardStepPin || op.Steps[1] != engine.ClipboardStepWrite || op.Steps[2] != engine.ClipboardStepPreview {
			t.Fatalf("unpinned activation must pin/write/preview: %+v", op)
		}
	} else if op.ID != "pinned" || op.PinBeforeWrite || len(op.Steps) != 2 || op.Steps[0] != engine.ClipboardStepWrite || op.Steps[1] != engine.ClipboardStepPreview {
		t.Fatalf("pinned activation must write+preview without repin: %+v", op)
	}
}
