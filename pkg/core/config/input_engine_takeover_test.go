package config

import "testing"

// The takeover switches default to: force_takeover off (taking over silently is surprising),
// auto_takeover on (a non-ASCII source means the user wants Chinese), lock_system_source on
// (two candidate windows fighting is the worst failure mode).
func TestInputEngineTakeoverDefaults(t *testing.T) {
	c := DefaultInputEngineConfig()
	if c.ForceTakeover {
		t.Fatal("force_takeover must default to off")
	}
	if !c.AutoTakeover {
		t.Fatal("auto_takeover should default on")
	}
	if !c.LockSystemSource {
		t.Fatal("lock_system_source should default on")
	}
}

// Only deviations are written, so a default config.toml stays readable and does not grow a
// line per new switch.
func TestInputEngineTakeoverOnlyWritesDeviations(t *testing.T) {
	if tbl := DefaultInputEngineConfig().toTOML(); tbl != nil {
		for _, key := range []string{"force_takeover", "auto_takeover", "lock_system_source"} {
			if _, ok := tbl.Get(key); ok {
				t.Fatalf("default config wrote %q", key)
			}
		}
	}

	c := DefaultInputEngineConfig()
	c.ForceTakeover = true
	c.AutoTakeover = false
	c.LockSystemSource = false
	tbl := c.toTOML()
	if v, ok := tbl.Get("force_takeover"); !ok {
		t.Fatal("force_takeover=true not written")
	} else if b, _ := v.AsBool(); !b {
		t.Fatal("force_takeover should be true")
	}
	if v, ok := tbl.Get("auto_takeover"); !ok {
		t.Fatal("auto_takeover=false not written")
	} else if b, _ := v.AsBool(); b {
		t.Fatal("auto_takeover should be false")
	}
	if v, ok := tbl.Get("lock_system_source"); !ok {
		t.Fatal("lock_system_source=false not written")
	} else if b, _ := v.AsBool(); b {
		t.Fatal("lock_system_source should be false")
	}
}
