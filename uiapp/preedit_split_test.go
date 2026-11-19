package uiapp

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/inputengine"
)

func TestSplitPreedit(t *testing.T) {
	cases := []struct {
		name, preedit string
		n             int
		head, tail    string
	}{
		{"no split at 0", "ye'shu'gen", 0, "", "ye'shu'gen"},
		{"first syllable", "ye'shu'gen", 1, "ye", "shu'gen"},
		{"two syllables", "wo'shi'ye'shu'gen", 2, "wo'shi", "ye'shu'gen"},
		{"whole input", "ye'shu'gen", 3, "", "ye'shu'gen"},
		{"beyond length", "ni'hao", 9, "", "ni'hao"},
		{"single syllable", "ni", 1, "", "ni"},
		{"empty", "", 1, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, tl := splitPreedit(tc.preedit, tc.n)
			if h != tc.head || tl != tc.tail {
				t.Fatalf("splitPreedit(%q,%d) = (%q,%q), want (%q,%q)", tc.preedit, tc.n, h, tl, tc.head, tc.tail)
			}
		})
	}
}

// The highlighted candidate's consumed count must reach the UI: without it the pinyin line
// cannot show which syllables Enter would take.
func TestViewExposesSelectedConsumed(t *testing.T) {
	e := inputengine.New(inputengine.Options{CandidateCount: 7})
	for _, r := range "woshizhongguoren" {
		e.HandleKey(inputengine.Key{Kind: inputengine.KeyLetter, Rune: r})
	}
	v := e.View()
	if len(v.Candidates) == 0 {
		t.Fatal("no candidates")
	}
	if v.SelectedConsumed < 1 || v.SelectedConsumed > len(v.Candidates)+1 {
		t.Fatalf("SelectedConsumed = %d, out of range", v.SelectedConsumed)
	}
	// PageCount backs the "1/9" indicator; it must be consistent with HasNext.
	if v.PageCount > 1 && !v.HasNext && !v.HasPrev {
		t.Fatalf("PageCount=%d but neither arrow shows", v.PageCount)
	}
}
