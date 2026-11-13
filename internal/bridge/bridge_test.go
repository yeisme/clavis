package bridge

import (
	"context"
	"errors"
	"testing"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

func TestNewHostService_InterfaceAndPermissions(t *testing.T) {
	var svc HostService = NewHostService(
		WithTriggerConfig(config.DefaultTriggerConfig()),
		WithKeymapConfig(config.DefaultKeymapConfig()),
		WithIMEConfig(config.DefaultIMEConfig(), config.DefaultIMERulesConfig()),
	)
	if svc == nil {
		t.Fatal("expected non-nil HostService")
	}
	caps := svc.Capabilities()
	if !caps.Keychain {
		t.Fatal("expected Keychain capability to be true on desktop platforms")
	}
	st, err := svc.RefreshPermissions(context.Background())
	if err != nil {
		t.Fatalf("RefreshPermissions failed: %v", err)
	}
	_ = st

	// Verify runtime config updaters and SetIMEMode(keep)
	kmCfg := config.DefaultKeymapConfig()
	kmCfg.CapsLockHold = "hyper"
	svc.UpdateKeymapConfig(kmCfg)
	svc.UpdateIMEConfig(config.DefaultIMEConfig(), config.DefaultIMERulesConfig())
	if err := svc.SetIMEMode(context.Background(), config.IMEModeKeep); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("SetIMEMode(keep) failed: %v", err)
	}
}

func TestTriggerRouter_LeaderDoubleTapHotkeyAndSnippets(t *testing.T) {
	cfg := config.DefaultTriggerConfig()
	cfg.LeaderKey = "right_option"
	cfg.DoubleTapKey = "right_command"
	cfg.CapsuleShortcut = "option+space"
	cfg.SnippetExpansionEnabled = true

	router := newTriggerRouter(cfg)

	// Leader double tap
	router.HandleModifier("right_option", true, false, 100)
	router.HandleModifier("right_option", false, false, 140)
	router.HandleModifier("right_option", true, false, 200)
	evs := router.HandleModifier("right_option", false, false, 240)
	if len(evs) != 1 || evs[0].Kind != host.TriggerLeaderKey {
		t.Fatalf("expected TriggerLeaderKey, got %+v", evs)
	}

	// Secondary double tap key
	router.HandleModifier("right_command", true, false, 500)
	router.HandleModifier("right_command", false, false, 540)
	router.HandleModifier("right_command", true, false, 600)
	evs = router.HandleModifier("right_command", false, false, 640)
	if len(evs) != 1 || evs[0].Kind != host.TriggerDoubleTap {
		t.Fatalf("expected TriggerDoubleTap, got %+v", evs)
	}

	// Capsule shortcut consumes key
	evs, consume := router.HandleKeyDown("space", " ", []string{"option"}, false, false, 700)
	if !consume || len(evs) != 1 || evs[0].Kind != host.TriggerCapsuleToggle {
		t.Fatalf("expected TriggerCapsuleToggle consumed, got consume=%v evs=%+v", consume, evs)
	}

	// Inline snippet suppressed when Secure Input is active
	evs, consume = router.HandleKeyDown("a", "a", nil, false, true, 800)
	if consume || len(evs) != 0 {
		t.Fatalf("expected no snippet event during Secure Input, got consume=%v evs=%+v", consume, evs)
	}

	// Normal inline snippet key & backspace
	evs, consume = router.HandleKeyDown("a", "a", nil, false, false, 900)
	if consume || len(evs) != 1 || evs[0].Kind != host.TriggerSnippetKey || evs[0].Rune != "a" {
		t.Fatalf("expected TriggerSnippetKey 'a', got consume=%v evs=%+v", consume, evs)
	}
	evs, consume = router.HandleKeyDown("backspace", "", nil, true, false, 950)
	if consume || len(evs) != 1 || evs[0].Kind != host.TriggerSnippetBack {
		t.Fatalf("expected TriggerSnippetBack, got %+v", evs)
	}
}

func TestSelfPasteboardTracker(t *testing.T) {
	tr := newSelfPasteboardTracker()

	// Initial poll initializes baseline without emitting a candidate
	if tr.ShouldProcess(5) {
		t.Fatal("expected initial baseline not to emit")
	}
	// Same count does not emit
	if tr.ShouldProcess(5) {
		t.Fatal("expected unchanged count not to emit")
	}
	// Self-ignored changeCount does not emit
	tr.Ignore(6)
	if tr.ShouldProcess(6) {
		t.Fatal("expected self-ignored changeCount 6 not to emit")
	}
	// External changeCount emits
	if !tr.ShouldProcess(7) {
		t.Fatal("expected external changeCount 7 to emit")
	}
}

func TestTruncateUTF8_PreservesRuneBoundaries(t *testing.T) {
	s := "a😀b中c" // 'a'(1) + '😀'(4) + 'b'(1) + '中'(3) + 'c'(1) = 10 bytes
	cases := []struct {
		maxBytes int
		want     string
	}{
		{0, ""},
		{1, "a"},
		{2, "a"},
		{3, "a"},
		{4, "a"},
		{5, "a😀"},
		{6, "a😀b"},
		{7, "a😀b"},
		{8, "a😀b"},
		{9, "a😀b中"},
		{10, "a😀b中c"},
		{100, "a😀b中c"},
	}
	for _, tc := range cases {
		got := truncateUTF8(s, tc.maxBytes)
		if got != tc.want {
			t.Errorf("truncateUTF8(%q, %d) = %q, want %q", s, tc.maxBytes, got, tc.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("truncateUTF8(%q, %d) produced invalid UTF-8: %x", s, tc.maxBytes, got)
		}
	}
}

func TestTriggerRouter_HandleKeyDownWithIMECompositionAndContextResets(t *testing.T) {
	cfg := config.DefaultTriggerConfig()
	cfg.CapsuleShortcut = "option+space"
	cfg.SnippetExpansionEnabled = true
	router := newTriggerRouter(cfg)

	// 1. Plain key while idle -> snippet key, not consumed.
	evs, consume := router.HandleKeyDownWithIME("s", "s", nil, false, false, false, 100)
	if consume || len(evs) != 1 || evs[0].Kind != host.TriggerSnippetKey || evs[0].Rune != "s" {
		t.Fatalf("expected TriggerSnippetKey 's', got consume=%v evs=%+v", consume, evs)
	}

	// 2. IME composition active -> the keystroke belongs to the candidate window, so the
	//    snippet buffer is reset instead of being fed raw preedit text.
	evs, consume = router.HandleKeyDownWithIME("n", "n", nil, false, false, true, 200)
	if consume || len(evs) != 1 || evs[0].Kind != host.TriggerSnippetReset || evs[0].Key != "n" {
		t.Fatalf("expected TriggerSnippetReset during IME composition, got consume=%v evs=%+v", consume, evs)
	}

	// 3. Cursor-moving / context-breaking keys reset the buffer.
	for i, key := range []string{"left", "right", "up", "down", "home", "end", "pageup", "pagedown", "return", "escape", "tab", "delete"} {
		evs, consume = router.HandleKeyDownWithIME(key, "", nil, false, false, false, int64(300+i))
		if consume || len(evs) != 1 || evs[0].Kind != host.TriggerSnippetReset {
			t.Fatalf("expected TriggerSnippetReset for %q, got consume=%v evs=%+v", key, consume, evs)
		}
	}

	// 4. Non-shift shortcut chords (paste/cut/select-all) also invalidate the buffer.
	for i, chord := range [][]string{{"command"}, {"control"}, {"option"}, {"fn"}} {
		evs, consume = router.HandleKeyDownWithIME("v", "v", chord, false, false, false, int64(500+i))
		if consume || len(evs) != 1 || evs[0].Kind != host.TriggerSnippetReset {
			t.Fatalf("expected TriggerSnippetReset for %v+v, got consume=%v evs=%+v", chord, consume, evs)
		}
	}

	// 5. Shift alone is text-typing: it must still feed the snippet buffer.
	evs, consume = router.HandleKeyDownWithIME("s", "S", []string{"shift"}, false, false, false, 600)
	if consume || len(evs) != 1 || evs[0].Kind != host.TriggerSnippetKey || evs[0].Rune != "S" {
		t.Fatalf("expected TriggerSnippetKey 'S' with shift held, got consume=%v evs=%+v", consume, evs)
	}

	// 6. The capsule shortcut wins over IME composition (shortcuts are not IME text).
	evs, consume = router.HandleKeyDownWithIME("space", " ", []string{"option"}, false, false, true, 700)
	if !consume || len(evs) != 1 || evs[0].Kind != host.TriggerCapsuleToggle {
		t.Fatalf("expected TriggerCapsuleToggle consumed during IME composition, got consume=%v evs=%+v", consume, evs)
	}

	// 7. Backspace during composition resets instead of rewinding the buffer.
	evs, consume = router.HandleKeyDownWithIME("backspace", "", nil, true, false, true, 800)
	if consume || len(evs) != 1 || evs[0].Kind != host.TriggerSnippetReset {
		t.Fatalf("expected TriggerSnippetReset for backspace during composition, got consume=%v evs=%+v", consume, evs)
	}
	// ... and without composition it is a snippet backspace.
	evs, consume = router.HandleKeyDownWithIME("backspace", "", nil, true, false, false, 900)
	if consume || len(evs) != 1 || evs[0].Kind != host.TriggerSnippetBack {
		t.Fatalf("expected TriggerSnippetBack, got consume=%v evs=%+v", consume, evs)
	}

	// 8. Secure Input and disabled snippet expansion suppress everything.
	if evs, consume = router.HandleKeyDownWithIME("a", "a", nil, false, true, false, 1000); consume || len(evs) != 0 {
		t.Fatalf("expected no events during Secure Input, got consume=%v evs=%+v", consume, evs)
	}
	offCfg := config.DefaultTriggerConfig()
	offCfg.SnippetExpansionEnabled = false
	offRouter := newTriggerRouter(offCfg)
	if evs, consume = offRouter.HandleKeyDownWithIME("a", "a", nil, false, false, false, 1100); consume || len(evs) != 0 {
		t.Fatalf("expected no events when snippet expansion is disabled, got consume=%v evs=%+v", consume, evs)
	}
}

func TestTriggerRouter_HandleKeyDownWithIMEResetsPendingDoubleTap(t *testing.T) {
	cfg := config.DefaultTriggerConfig()
	cfg.LeaderKey = "right_option"
	cfg.CapsuleShortcut = "option+space"
	router := newTriggerRouter(cfg)

	// Start a leader double tap, then let an intervening key (e.g. mid-composition) break it.
	router.HandleModifier("right_option", true, false, 100)
	router.HandleModifier("right_option", false, false, 140)
	router.HandleKeyDownWithIME("n", "n", nil, false, false, true, 180)
	router.HandleModifier("right_option", true, false, 220)
	if evs := router.HandleModifier("right_option", false, false, 260); len(evs) != 0 {
		t.Fatalf("expected intervening key to reset leader double tap, got %+v", evs)
	}
}
