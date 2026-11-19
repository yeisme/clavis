//go:build darwin

package bridge

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/host"
)

// Forced mode must beat the system-input-source gate: a Mac whose system IME is Chinese
// would otherwise never reach the own engine at all.
func TestInlineIMEForcedOverridesGate(t *testing.T) {
	b := &darwinBridge{}
	if b.InlineIMEForced() {
		t.Fatal("forced must default to off")
	}
	// Without forcing, a non-ASCII source keeps the engine out.
	if inlineIMEGateFor(host.InputSourceInfo{ID: "com.apple.inputmethod.SCIM.ITABC", IsASCII: false}) {
		t.Fatal("german: non-ASCII source must block the engine when not forced")
	}

	b.SetInlineIMEForced(true)
	if !b.InlineIMEForced() {
		t.Fatal("SetInlineIMEForced(true) must stick")
	}
	// The forced flag is what overrides the gate, independent of the source.
	if !b.inlineForced.Load() {
		t.Fatal("forced flag must be observable to the hot path")
	}

	b.SetInlineIMEForced(false)
	if b.InlineIMEForced() {
		t.Fatal("SetInlineIMEForced(false) must clear")
	}
}
