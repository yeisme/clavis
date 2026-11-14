package bridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

func TestEngineKeyForVK(t *testing.T) {
	cases := []struct {
		vk    uint16
		text  string
		chord bool
		kind  inputengine.KeyKind
		ok    bool
	}{
		{0x41, "a", false, inputengine.KeyLetter, true},
		{0x41, "A", false, inputengine.KeyOther, true}, // Shift/Caps: upper-case is literal
		{0x41, "a", true, inputengine.KeyLetter, true}, // Ctrl+A: letter but Chord set
		{0x31, "1", false, inputengine.KeyDigit, true},
		{0x31, "!", false, inputengine.KeyOther, true},
		{0xDE, "'", false, inputengine.KeyApostrophe, true},
		{0xBD, "-", false, inputengine.KeyPagePrev, true},
		{0xBB, "=", false, inputengine.KeyPageNext, true},
		{0xBC, ",", false, inputengine.KeyOther, true},
		{0x20, " ", false, inputengine.KeySpace, true},
		{0x0D, "\r", false, inputengine.KeyEnter, true},
		{0x08, "\b", false, inputengine.KeyBackspace, true},
		{0x1B, "\x1b", false, inputengine.KeyEscape, true},
		{0x25, "", false, inputengine.KeyNavigation, true}, // Left
		{0x09, "\t", false, inputengine.KeyNavigation, true},
		{0x74, "", false, inputengine.KeyNavigation, true}, // F5
		{0xDC, "", false, 0, false},                        // dead key / no text
	}
	for i, c := range cases {
		k, ok := engineKeyForVK(c.vk, c.text, c.chord)
		if ok != c.ok || (ok && k.Kind != c.kind) || k.Chord != c.chord {
			t.Fatalf("case %d (vk=%#x %q): got %+v ok=%v", i, c.vk, c.text, k, ok)
		}
	}
}

func TestUTF16UnitsAndEligibility(t *testing.T) {
	if got := utf16Units("你a😀"); len(got) != 4 || got[0] != 0x4F60 || got[2] != 0xD83D || got[3] != 0xDE00 {
		t.Fatalf("utf16 = %x", got)
	}
	if !inputEngineEligible(false, false, false) {
		t.Fatal("plain ASCII field must be eligible")
	}
	for _, c := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		if inputEngineEligible(c[0], c[1], c[2]) {
			t.Fatalf("must not take over: native=%v secure=%v self=%v", c[0], c[1], c[2])
		}
	}
}

func TestInputEngineConfigPlumbing(t *testing.T) {
	// Default config has Enabled=false -> nil engine.
	def := config.DefaultInputEngineConfig()
	if def.Enabled {
		t.Fatal("default [input_engine] must stay disabled")
	}
	if eng := newInputEngineFromConfig(def, t.TempDir()); eng != nil {
		t.Fatal("disabled config must not instantiate engine")
	}
	for _, mode := range []string{"native_imk", "off"} {
		cfg := def
		cfg.Enabled = true
		cfg.Mode = mode
		if eng := newInputEngineFromConfig(cfg, t.TempDir()); eng != nil {
			t.Fatalf("mode %q must not instantiate inline hook engine", mode)
		}
	}

	dir := t.TempDir()
	dictPath := filepath.Join(dir, "bridge_custom.dict.yaml")
	if err := os.WriteFile(dictPath, []byte("---\nname: bridge\n...\n桥接热词\tqiao jie re ci\t999999\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := def
	cfg.Enabled = true
	cfg.Mode = "inline_hook"
	cfg.Scheme = "xiaohe"
	cfg.CandidateCount = 1 // clamped to 3
	cfg.FuzzyPinyin = []string{"z=zh"}
	cfg.CustomDictPaths = []string{dictPath}

	eng := newInputEngineFromConfig(cfg, filepath.Join(dir, "cache"))
	if eng == nil {
		t.Fatal("enabled inline_hook config returned nil engine")
	}
	if eng.CandidateCount() != inputengine.MinCandidateCount {
		t.Fatalf("clamped CandidateCount = %d, want %d", eng.CandidateCount(), inputengine.MinCandidateCount)
	}
	// Shuangpin ("nihc" -> 你好) works through the wired engine.
	for _, r := range "nihc" {
		eng.HandleKey(inputengine.Key{Kind: inputengine.KeyLetter, Rune: r})
	}
	if v := eng.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "你好" {
		t.Fatalf("wired xiaohe nihc = %+v", v.Candidates)
	}
	eng.Reset()

	// Custom Rime dict ("qcjpreci" -> qiao'jie're'ci -> 桥接热词) works through the wired engine.
	for _, r := range "qcjpreci" {
		eng.HandleKey(inputengine.Key{Kind: inputengine.KeyLetter, Rune: r})
	}
	if v := eng.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "桥接热词" {
		t.Fatalf("wired custom dict in xiaohe = %+v (preedit=%q)", v.Candidates, v.Preedit)
	}
}
