package uiapp

import (
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

func typeSnippetKeys(ctrl *AppController, s string) {
	for _, r := range s {
		ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerSnippetKey, Rune: string(r)})
	}
}

// TestSnippetBackspaceUndoRestoresTypedTrigger: Backspace right after an
// expansion deletes what is left of it (the real Backspace took one rune)
// and types back exactly what the user typed, full-width prefix included.
func TestSnippetBackspaceUndoRestoresTypedTrigger(t *testing.T) {
	for _, typed := range []string{";sig", "；sig"} {
		ctrl, br, _, _ := newTestController(t, nil)
		var pills []string
		ctrl.OnShowIMEPill = func(label string, _ *host.CursorAnchor) { pills = append(pills, label) }

		typeSnippetKeys(ctrl, typed)
		waitUntil(t, time.Second, func() bool { return len(br.getInjected()) == 1 }, "expansion")
		exp := br.getInjected()[0]
		if exp.text != "Best regards," || exp.delCount != utf8.RuneCountInString(typed) {
			t.Fatalf("%q expansion = %+v", typed, exp)
		}
		if len(pills) != 1 || pills[0] == "" {
			t.Fatalf("expected one undo-hint pill, got %q", pills)
		}

		ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerSnippetBack})
		waitUntil(t, time.Second, func() bool { return len(br.getInjected()) == 2 }, "undo injection")
		undo := br.getInjected()[1]
		if undo.text != typed || undo.delCount != utf8.RuneCountInString("Best regards,")-1 || undo.backOffset != 0 {
			t.Fatalf("%q undo = %+v, want %q after deleting %d", typed, undo, typed, utf8.RuneCountInString("Best regards,")-1)
		}

		// One undo only: a second Backspace is an ordinary Backspace.
		ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerSnippetBack})
		time.Sleep(3 * snippetUndoDelay)
		if n := len(br.getInjected()); n != 2 {
			t.Fatalf("a second Backspace injected again (%d injections)", n)
		}
	}
}

func TestSnippetUndoDisarmedByTypingOrReset(t *testing.T) {
	for _, interrupt := range []host.TriggerEvent{
		{Kind: host.TriggerSnippetKey, Rune: "x"},
		{Kind: host.TriggerSnippetReset},
	} {
		ctrl, br, _, _ := newTestController(t, nil)
		typeSnippetKeys(ctrl, ";sig")
		waitUntil(t, time.Second, func() bool { return len(br.getInjected()) == 1 }, "expansion")

		ctrl.HandleTriggerEvent(interrupt)
		ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerSnippetBack})
		time.Sleep(3 * snippetUndoDelay)
		if n := len(br.getInjected()); n != 1 {
			t.Fatalf("after %s, Backspace still undid the expansion (%d injections)", interrupt.Kind, n)
		}
	}
}

func TestSnippetUndoHonorsConfigAndCursorSnippets(t *testing.T) {
	ctrl, br, _, _ := newTestController(t, nil)
	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) { m.Trigger.SnippetBackspaceUndo = false }); err != nil {
		t.Fatalf("UpdateMainConfig: %v", err)
	}
	typeSnippetKeys(ctrl, ";sig")
	waitUntil(t, time.Second, func() bool { return len(br.getInjected()) == 1 }, "expansion")
	ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerSnippetBack})
	time.Sleep(3 * snippetUndoDelay)
	if n := len(br.getInjected()); n != 1 {
		t.Fatalf("undo ran with snippet_backspace_undo = false (%d injections)", n)
	}

	// A {{cursor}} snippet leaves the caret inside the expansion: no undo.
	ctrl2, br2, _, _ := newTestController(t, nil)
	if err := ctrl2.SaveSnippetsConfig(config.SnippetsConfig{Items: []config.SnippetItem{
		{ID: "fn", Trigger: ";fn", Content: "func {{cursor}}() {}"},
	}}); err != nil {
		t.Fatalf("SaveSnippetsConfig: %v", err)
	}
	typeSnippetKeys(ctrl2, ";fn")
	waitUntil(t, time.Second, func() bool { return len(br2.getInjected()) == 1 }, "cursor expansion")
	if br2.getInjected()[0].backOffset == 0 {
		t.Fatalf("expected a cursor back-offset, got %+v", br2.getInjected()[0])
	}
	ctrl2.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerSnippetBack})
	time.Sleep(3 * snippetUndoDelay)
	if n := len(br2.getInjected()); n != 1 {
		t.Fatalf("undo ran for a {{cursor}} expansion (%d injections)", n)
	}
}

func TestUndoSafeExpansion(t *testing.T) {
	for _, ok := range []string{"Best regards,", "¯\\_(ツ)_/¯", "多行\n签名", "👍"} {
		if !undoSafeExpansion(ok) {
			t.Errorf("undoSafeExpansion(%q) = false", ok)
		}
	}
	for _, bad := range []string{"👍🏽", "👨‍💻", "❤️", "🇨🇳", "e\u0301", "a\r\nb"} {
		if undoSafeExpansion(bad) {
			t.Errorf("undoSafeExpansion(%q) = true; one Backspace can span several runes", bad)
		}
	}
}
