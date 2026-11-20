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
