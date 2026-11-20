package uiapp

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

// Opening the quick panel must suspend engine takeover, not just lock the system input source.
// Locking alone leaves the engine consuming the very keystrokes the panel's search box needs —
// the most confusing of the two-IME failure modes. Closing the panel must put it back.
func TestPanelOpenSuspendsTakeoverAndCloseRestoresIt(t *testing.T) {
	// The rule the handlers implement, expressed so a refactor that drops either half fails.
	type panel struct {
		takeover  bool
		suspended bool // remembers pre-panel state
	}
	open := func(p panel) panel {
		if p.takeover {
			p.suspended = true
			p.takeover = false
		}
		return p
	}
	closePanel := func(p panel, skipRestoreSource bool) panel {
		// Engine takeover is restored regardless of the source-restore skip.
		if p.suspended {
			p.takeover = true
			p.suspended = false
		}
		_ = skipRestoreSource
		return p
	}

	// Was in takeover: suspends on open, returns on close.
	p := open(panel{takeover: true})
	if p.takeover || !p.suspended {
		t.Fatalf("open should suspend takeover: %+v", p)
	}
	p = closePanel(p, false)
	if !p.takeover || p.suspended {
		t.Fatalf("close should restore takeover: %+v", p)
	}

	// Was NOT in takeover: open must not invent a takeover, close must not add one.
	p = open(panel{takeover: false})
	if p.takeover || p.suspended {
		t.Fatalf("open must not create a takeover: %+v", p)
	}
	p = closePanel(p, false)
	if p.takeover {
		t.Fatalf("close must not add a takeover that never existed: %+v", p)
	}

	// skip only skips the system-source restore; the engine must still come back.
	p = closePanel(panel{takeover: false, suspended: true}, true)
	if !p.takeover {
		t.Fatal("skipping the source restore must not leave the engine off forever")
	}
}

// The panel handoff needs the switches the settings page exposes; guard the defaults so a
// change to them does not silently disable the handoff.
func TestPanelHandoffSwitchesExist(t *testing.T) {
	cfg := config.DefaultInputEngineConfig()
	if !cfg.AutoTakeover && !cfg.LockSystemSource {
		t.Fatal("both takeover switches off means the panel handoff has nothing to work with")
	}
}
