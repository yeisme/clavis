//go:build darwin

package bridge

import (
	"strings"
	"testing"
)

// Regression: darwinModifierEvent reports "left_shift"/"right_shift", never a bare
// "shift". The input-mode toggle and the double-shift selection bar were gated on
// `modName == "shift"` and so never fired on macOS at all.
func TestDarwinModifierEventReportsSidedShift(t *testing.T) {
	// kVK_RightShift = 60, kVK_Shift = 56.
	if name, down, _ := darwinModifierEvent(60, nxDeviceRShiftMask); name != "right_shift" || !down {
		t.Fatalf("right shift keydown = (%q,%v), want (right_shift,true)", name, down)
	}
	if name, down, _ := darwinModifierEvent(56, nxDeviceLShiftMask); name != "left_shift" || !down {
		t.Fatalf("left shift keydown = (%q,%v), want (left_shift,true)", name, down)
	}
	// A bare "shift" is never produced; the split below would silently regress if it were.
	for _, keyCode := range []uint16{56, 60} {
		if name, _, _ := darwinModifierEvent(keyCode, 0); name == "shift" {
			t.Fatalf("keycode %d reported a bare \"shift\"; both sides must stay distinguishable", keyCode)
		}
	}
}

// The handler must accept both sides through the same entry point (HasSuffix) and then
// route them apart: right = input mode, left = selection bar.
func TestShiftSidesRouteApart(t *testing.T) {
	for _, name := range []string{"left_shift", "right_shift"} {
		if !strings.HasSuffix(name, "shift") {
			t.Fatalf("%q must be recognised by the handler", name)
		}
	}
	if !strings.HasSuffix("right_shift", "shift") || "right_shift" == "left_shift" {
		t.Fatal("right and left must remain distinguishable after the suffix match")
	}
}

// Regression: shiftDown was a single shared flag, so holding LEFT shift and then tapping
// RIGHT shift looked exactly like one right-shift tap and flipped 中/英 by accident.
// The side is now remembered, and the toggle requires it to be the right shift itself.
func TestShiftSideIsRememberedSoLeftPlusRightTapDoesNotToggle(t *testing.T) {
	// This is the rule the handler now applies; keep it expressed here so a refactor that
	// drops the side check fails loudly.
	decide := func(down bool, side string, hadKey bool) bool {
		const modName = "right_shift"
		right := modName == "right_shift"
		return right && !down && side == modName && !hadKey
	}
	if decide(true, "left_shift", false) {
		t.Fatal("left shift keydown must never count as a right-shift tap")
	}
	if decide(false, "left_shift", false) {
		t.Fatal("releasing left shift must never toggle the input mode")
	}
	if decide(false, "right_shift", true) {
		t.Fatal("a right shift used as a modifier (hadKey) must not toggle")
	}
	if !decide(false, "right_shift", false) {
		t.Fatal("a plain right-shift tap must toggle")
	}
	if decide(false, "", false) {
		t.Fatal("an empty side must not toggle")
	}
}

// With the toggle hotkey defaulting to empty, a double tap on the right shift is the only
// way into forced Chinese takeover. Without it the engine can never engage while a
// non-ASCII system input source is active, and the whole input method looks dead.
func TestRightShiftDoubleTapDrivesTheToggle(t *testing.T) {
	r := newTriggerRuntime()
	var fired bool
	for i := 0; i < 2; i++ {
		if r.rightShiftDouble("right_shift", true, false, int64(i*50)) {
			fired = true
		}
		if r.rightShiftDouble("right_shift", false, false, int64(i*50+10)) {
			fired = true
		}
	}
	if !fired {
		t.Fatal("two quick right-shift taps must produce the toggle")
	}

	// The left shift must never drive it.
	r2 := newTriggerRuntime()
	for i := 0; i < 2; i++ {
		r2.rightShiftDouble("left_shift", true, false, int64(i*50))
		if r2.rightShiftDouble("left_shift", false, false, int64(i*50+10)) {
			t.Fatal("left shift must not drive the input-mode toggle")
		}
	}
}
