package config

import (
	"strings"
	"testing"
)

// TestTriggerPolishFieldsRoundTrip covers the [trigger] keys added for the
// interaction polish: snippet_backspace_undo and leader_reveal_delay_ms keep
// their defaults when absent, decode when present and survive Encode.
func TestTriggerPolishFieldsRoundTrip(t *testing.T) {
	def := DefaultTriggerConfig()
	if !def.SnippetBackspaceUndo || def.LeaderRevealDelayMs != DefaultLeaderRevealDelayMs {
		t.Fatalf("defaults = %+v", def)
	}

	cfg, err := DecodeMainConfig("[trigger]\nsnippet_backspace_undo = false\nleader_reveal_delay_ms = 300\n")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Trigger.SnippetBackspaceUndo || cfg.Trigger.LeaderRevealDelayMs != 300 {
		t.Fatalf("decoded = %+v", cfg.Trigger)
	}
	out := cfg.Encode()
	for _, want := range []string{"snippet_backspace_undo = false", "leader_reveal_delay_ms = 300"} {
		if !strings.Contains(out, want) {
			t.Fatalf("encoded config lacks %q:\n%s", want, out)
		}
	}
	again, err := DecodeMainConfig(out)
	if err != nil || again.Trigger != cfg.Trigger {
		t.Fatalf("round trip = %+v, %v; want %+v", again.Trigger, err, cfg.Trigger)
	}

	legacy, err := DecodeMainConfig("[trigger]\nleader_key = \"right_option\"\n")
	if err != nil || !legacy.Trigger.SnippetBackspaceUndo || legacy.Trigger.LeaderRevealDelayMs != DefaultLeaderRevealDelayMs {
		t.Fatalf("older config without the keys = %+v, %v", legacy.Trigger, err)
	}
}

func TestLeaderRevealDelayValidation(t *testing.T) {
	for _, tc := range []struct {
		ms   int
		warn bool
	}{{0, false}, {220, false}, {MaxLeaderRevealDelayMs, false}, {-1, true}, {MaxLeaderRevealDelayMs + 1, true}} {
		m := DefaultMainConfig()
		m.Trigger.LeaderRevealDelayMs = tc.ms
		found := false
		for _, is := range ValidateMain(m) {
			if is.KeyPath == "trigger.leader_reveal_delay_ms" {
				found = true
				if is.Severity != SeverityWarning {
					t.Fatalf("severity = %s, want warning (the value is clamped, not fatal)", is.Severity)
				}
			}
		}
		if found != tc.warn {
			t.Fatalf("leader_reveal_delay_ms = %d: warning = %v, want %v", tc.ms, found, tc.warn)
		}
	}
}
