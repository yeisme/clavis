package bridge

import (
	"testing"

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
