package bridge

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

// With double-shift selection armed, a bare Shift tap must be deferred so one
// gesture cannot both flip 中/英 and open the selection bar.
func TestShiftDeferralWhenSelectionDoubleShiftArmed(t *testing.T) {
	r := newTriggerRuntime()

	r.setSelection(config.SelectionBarConfig{Enabled: true, TriggerMode: "double_shift"})
	if armed, win := r.selectionDoubleShiftArmed(); !armed || win <= 0 {
		t.Fatalf("double_shift should arm: armed=%v window=%d", armed, win)
	}

	// auto_select does not consume a bare Shift tap.
	r.setSelection(config.SelectionBarConfig{Enabled: true, TriggerMode: "auto_select"})
	if armed, _ := r.selectionDoubleShiftArmed(); armed {
		t.Fatal("auto_select must not arm shift deferral")
	}

	// disabled / enabled=false must not arm.
	r.setSelection(config.SelectionBarConfig{Enabled: false, TriggerMode: "double_shift"})
	if armed, _ := r.selectionDoubleShiftArmed(); armed {
		t.Fatal("enabled=false must not arm")
	}
	r.setSelection(config.SelectionBarConfig{Enabled: true, TriggerMode: "disabled"})
	if armed, _ := r.selectionDoubleShiftArmed(); armed {
		t.Fatal("TriggerMode=disabled must not arm")
	}

	// nil runtime must be safe (platforms without triggers).
	var nilR *triggerRuntime
	if armed, _ := nilR.selectionDoubleShiftArmed(); armed {
		t.Fatal("nil runtime must not arm")
	}
}

// The deferral window must match the detector's window so a completed double
// tap always beats the deferred toggle.
func TestShiftDeferralWindowMatchesDetector(t *testing.T) {
	r := newTriggerRuntime()
	r.setSelection(config.SelectionBarConfig{Enabled: true, TriggerMode: "double_shift"})
	_, win := r.selectionDoubleShiftArmed()
	if win != r.shift.IntervalMs() {
		t.Fatalf("deferral window %d != detector interval %d", win, r.shift.IntervalMs())
	}
	if win <= 0 {
		t.Fatal("window must be positive")
	}
}
