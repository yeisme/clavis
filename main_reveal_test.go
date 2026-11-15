package main

import (
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// TestCapsuleRevealDelay pins the which-key rule: only a Leader menu that is
// not yet on screen waits; drilling into a group of a visible menu, the
// Omnibox, AI and the HUD show at once; the setting is clamped.
func TestCapsuleRevealDelay(t *testing.T) {
	trig := config.DefaultTriggerConfig()
	if got := capsuleRevealDelay(engine.PhaseLeader, false, trig); got != time.Duration(config.DefaultLeaderRevealDelayMs)*time.Millisecond {
		t.Fatalf("fresh leader delay = %v", got)
	}
	if got := capsuleRevealDelay(engine.PhaseLeader, true, trig); got != 0 {
		t.Fatalf("visible leader re-presented with delay %v (would flicker)", got)
	}
	for _, phase := range []engine.CapsulePhase{engine.PhaseOmnibox, engine.PhaseAIStreaming, engine.PhaseHUD} {
		if got := capsuleRevealDelay(phase, false, trig); got != 0 {
			t.Fatalf("%s delay = %v, want immediate", phase, got)
		}
	}
	for ms, want := range map[int]time.Duration{
		0:    0,
		-50:  0,
		5000: time.Duration(config.MaxLeaderRevealDelayMs) * time.Millisecond,
		400:  400 * time.Millisecond,
	} {
		trig.LeaderRevealDelayMs = ms
		if got := capsuleRevealDelay(engine.PhaseLeader, false, trig); got != want {
			t.Fatalf("leader_reveal_delay_ms = %d -> %v, want %v", ms, got, want)
		}
	}
}
