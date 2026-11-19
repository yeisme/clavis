package uiapp

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// The panel distinguishes a whole-phrase answer from a single character by font weight, and
// it can only do that because Candidate.Consumed is exported. Pin both halves of that
// contract: phrase candidates report >1, single characters report exactly 1.
func TestCandidateConsumedDistinguishesPhraseFromChar(t *testing.T) {
	e := inputengine.New(inputengine.Options{CandidateCount: 7})
	for _, r := range "yeshugen" {
		e.HandleKey(inputengine.Key{Kind: inputengine.KeyLetter, Rune: r})
	}
	v := e.View()
	if len(v.Candidates) == 0 {
		t.Fatal("no candidates")
	}
	var phrases, singles int
	for _, c := range v.Candidates {
		switch {
		case c.Consumed > 1:
			phrases++
		case c.Consumed == 1:
			singles++
		default:
			t.Fatalf("candidate %q reports Consumed=%d", c.Text, c.Consumed)
		}
	}
	if phrases == 0 {
		t.Fatalf("no multi-syllable candidate on page 1; candidates=%v", texts(v))
	}
	if singles == 0 {
		t.Fatalf("no single-character candidate on page 1; candidates=%v", texts(v))
	}
}

func texts(v inputengine.View) []string {
	out := make([]string, 0, len(v.Candidates))
	for _, c := range v.Candidates {
		out = append(out, c.Text)
	}
	return out
}
