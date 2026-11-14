package bridge

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

func TestParseHotkeyClipboardDefaultMatchesExactly(t *testing.T) {
	hk, err := ParseHotkey("alt+shift+c")
	if err != nil {
		t.Fatalf("ParseHotkey(alt+shift+c): %v", err)
	}
	if !hk.Matches("c", []string{"option", "shift"}) {
		t.Fatal("expected alt+shift+c to match option+shift+c")
	}
	if !hk.Matches("C", []string{"shift", "alt"}) {
		t.Fatal("expected modifier order / key case to be irrelevant")
	}
	if hk.Matches("c", []string{"option"}) {
		t.Fatal("alt+c must not match alt+shift+c")
	}
	if hk.Matches("c", []string{"control", "shift"}) {
		t.Fatal("ctrl+shift+c must not match alt+shift+c")
	}
	if hk.Matches("c", []string{"option", "shift", "command"}) {
		t.Fatal("extra modifiers must not match")
	}
}

func TestTriggerRouterClipboardToggle(t *testing.T) {
	router := newTriggerRouter(config.DefaultTriggerConfig())

	evs, consume := router.HandleKeyDownWithIME("c", "C", []string{"option", "shift"}, false, false, false, 100)
	if !consume || len(evs) != 1 || evs[0].Kind != host.TriggerClipboardToggle {
		t.Fatalf("expected TriggerClipboardToggle consumed, got consume=%v evs=%+v", consume, evs)
	}

	// Capsule shortcut still wins its own chord.
	evs, consume = router.HandleKeyDownWithIME("space", " ", []string{"option"}, false, false, false, 200)
	if !consume || len(evs) != 1 || evs[0].Kind != host.TriggerCapsuleToggle {
		t.Fatalf("expected TriggerCapsuleToggle, got consume=%v evs=%+v", consume, evs)
	}

	// Near-miss chords fall through to the snippet branch (reset, not consumed).
	for _, mods := range [][]string{{"option"}, {"control", "shift"}} {
		evs, consume = router.HandleKeyDownWithIME("c", "c", mods, false, false, false, 300)
		if consume || len(evs) != 1 || evs[0].Kind != host.TriggerSnippetReset {
			t.Fatalf("expected snippet reset for %v+c, got consume=%v evs=%+v", mods, consume, evs)
		}
	}

	// Plain typing still feeds snippets.
	evs, consume = router.HandleKeyDownWithIME("c", "c", nil, false, false, false, 400)
	if consume || len(evs) != 1 || evs[0].Kind != host.TriggerSnippetKey {
		t.Fatalf("expected snippet key, got consume=%v evs=%+v", consume, evs)
	}

	// Empty clipboard_shortcut disables the hotkey.
	cfg := config.DefaultTriggerConfig()
	cfg.ClipboardShortcut = ""
	router.UpdateConfig(cfg)
	evs, consume = router.HandleKeyDownWithIME("c", "C", []string{"option", "shift"}, false, false, false, 500)
	if consume || (len(evs) == 1 && evs[0].Kind == host.TriggerClipboardToggle) {
		t.Fatalf("disabled clipboard hotkey must not fire, got consume=%v evs=%+v", consume, evs)
	}
}
