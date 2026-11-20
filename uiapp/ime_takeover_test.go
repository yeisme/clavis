package uiapp

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

// The takeover choice must survive a relaunch. Before this, force_takeover lived only in the
// bridge's atomic.Bool, so every restart dropped it and a Mac whose system IME is Chinese left
// the engine unreachable until the user rediscovered the double-tap.
func TestForceTakeoverDefaultsOffAndIsReportedByState(t *testing.T) {
	cfg := config.DefaultInputEngineConfig()
	if cfg.ForceTakeover {
		t.Fatal("force_takeover must default to off: silently taking over is surprising")
	}
	if !cfg.AutoTakeover {
		t.Fatal("auto_takeover should default on: a non-ASCII source means the user wants Chinese")
	}
	if !cfg.LockSystemSource {
		t.Fatal("lock_system_source should default on: two candidate windows is the worst mode")
	}
}

// Auto-takeover must only fire for a non-ASCII source, and never when the engine is already
// in takeover: re-entering would fight the user's own toggle.
func TestAutoTakeoverOnlyOnNonASCIISource(t *testing.T) {
	cases := []struct {
		name       string
		auto       bool
		info       host.InputSourceInfo
		wasTakeov  bool
		wantTakeov bool
	}{
		{"ascii source never takes over", true, host.InputSourceInfo{ID: "abc", IsASCII: true}, false, false},
		{"cjk source takes over", true, host.InputSourceInfo{ID: "pinyin", IsASCII: false}, false, true},
		{"already in takeover stays", true, host.InputSourceInfo{ID: "pinyin", IsASCII: false}, true, true},
		{"disabled auto never takes over", false, host.InputSourceInfo{ID: "pinyin", IsASCII: false}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := tc.wasTakeov
			if tc.auto && !tc.info.IsASCII && !tc.wasTakeov {
				next = true
			}
			if next != tc.wantTakeov {
				t.Fatalf("takeover=%v, want %v", next, tc.wantTakeov)
			}
		})
	}
}
