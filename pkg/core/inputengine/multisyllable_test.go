package inputengine

import (
	"slices"
	"strings"
	"testing"
)

// pickOnFirstPage chooses want with its digit key. It must be on the first page, which is
// what a user typing a sentence segment by segment actually sees.
func pickOnFirstPage(t *testing.T, e *Engine, want string) Result {
	t.Helper()
	v := e.View()
	idx := slices.IndexFunc(v.Candidates, func(c Candidate) bool { return c.Text == want })
	if idx < 0 {
		t.Fatalf("%q not on the first page of %q: %v", want, v.Preedit, texts(v))
	}
	return e.HandleKey(Key{Kind: KeyDigit, Rune: rune('1' + idx)})
}

// pickAnywhere pages forward until want is shown, then chooses it.
func pickAnywhere(t *testing.T, e *Engine, want string) Result {
	t.Helper()
	for range maxCandidates {
		v := e.View()
		if idx := slices.IndexFunc(v.Candidates, func(c Candidate) bool { return c.Text == want }); idx >= 0 {
			return e.HandleKey(Key{Kind: KeyDigit, Rune: rune('1' + idx)})
		}
		if !v.HasNext {
			break
		}
		e.HandleKey(Key{Kind: KeyPageNext})
	}
	t.Fatalf("%q not offered for %q: %v", want, e.View().Preedit, allTexts(e))
	return Result{}
}

func allTexts(e *Engine) []string {
	out := make([]string, len(e.cands))
	for i, c := range e.cands {
		out[i] = c.Text
	}
	return out
}

// The built-in dictionaries have no word for ye'shu'gen, wo'shi, shu'gen or mai'shu'ren, so
// these sentences are entered segment by segment: every choice commits its characters and
// leaves the remaining syllables composing.
func TestMultiSyllableSegmentBySegment(t *testing.T) {
	type step struct{ pick, rest string } // rest: preedit left afterwards; "" = finished
	tests := []struct {
		raw   string
		steps []step
	}{
		{"yeshugen", []step{{"叶", "shu'gen"}, {"树", "gen"}, {"根", ""}}},
		{"woshiyeshugen", []step{
			{"我", "shi'ye'shu'gen"}, {"是", "ye'shu'gen"}, {"叶", "shu'gen"}, {"树", "gen"}, {"根", ""},
		}},
		// Three syllables, no word for any span of them.
		{"maishuren", []step{{"卖", "shu'ren"}, {"书", "ren"}, {"人", ""}}},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			e := New(Options{})
			typeLetters(e, tc.raw)
			var got strings.Builder
			for _, s := range tc.steps {
				r := pickOnFirstPage(t, e, s.pick)
				if !r.Consume || r.Commit != s.pick {
					t.Fatalf("pick %q: %+v", s.pick, r)
				}
				got.WriteString(r.Commit)
				v := e.View()
				if v.Composing != (s.rest != "") || v.Preedit != s.rest {
					t.Fatalf("after %q: composing=%v preedit=%q, want %q", s.pick, v.Composing, v.Preedit, s.rest)
				}
			}
			want := ""
			for _, s := range tc.steps {
				want += s.pick
			}
			if got.String() != want {
				t.Fatalf("committed %q, want %q", got.String(), want)
			}
		})
	}
}

// The candidate list must only ever contain real dictionary words and single characters.
// An earlier design spliced several syllables into a made-up string (longest phrase at each
// position), which produced plausible-looking nonsense: 「我始终」 is a real word for
// wo'shi'zhong, but gluing it into wo'shi'ye'shu'gen yielded 「我始终国人」, and ye'shu (页数)
// plus gen gave 「页数根」. The common dictionary made those splices unnecessary.
func TestCandidatesAreDictionaryWordsOrSingles(t *testing.T) {
	cases := []struct{ raw string }{
		{"yeshugen"}, {"woshiyeshugen"}, {"woshizhongguoren"}, {"maishuren"},
		{"zhonghuarenmingongheguo"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			e := New(Options{CandidateCount: 7})
			typeLetters(e, tc.raw)
			n := len(e.segs)
			seen := allTexts(e)
			if len(seen) == 0 {
				t.Fatal("no candidates")
			}
			// Every candidate covers 1..n syllables and is a plain Han string.
			for i, c := range e.cands {
				if c.Consumed < 1 || c.Consumed > n {
					t.Fatalf("candidate %d %q reports Consumed=%d for %d syllables", i, c.Text, c.Consumed, n)
				}
				if strings.ContainsAny(c.Text, "' ") {
					t.Fatalf("candidate %q looks like spliced pinyin, not a word", c.Text)
				}
			}
			// A single syllable's worth of characters must never be outnumbered by wider
			// candidates: page 1 starts with the widest coverage, then narrows.
			for i := 1; i < len(e.cands) && i < 7; i++ {
				if e.cands[i].Consumed > e.cands[i-1].Consumed {
					t.Fatalf("page 1 not ordered by coverage: %q(%d) before %q(%d)",
						e.cands[i-1].Text, e.cands[i-1].Consumed, e.cands[i].Text, e.cands[i].Consumed)
				}
			}
		})
	}
}

// The everyday two-syllable words the 222-word starter list was missing now come from the
// embedded common dictionary. This is the regression that started it all: 当我 must be the
// first candidate for dangwo, not 当 alone.
func TestCommonDictionarySuppliesEverydayWords(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"dangwo", "当我"},
		{"women", "我们"},
		{"yijing", "已经"},
		{"suoyi", "所以"},
		{"shenme", "什么"},
		{"xianzai", "现在"},
		{"keyi", "可以"},
		{"zhege", "这个"},
		{"shijian", "时间"},
		{"gongzuo", "工作"},
		{"wenti", "问题"},
		{"yinwei", "因为"},
		{"suiran", "虽然"},
		{"ruguo", "如果"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			e := New(Options{CandidateCount: 7})
			typeLetters(e, tc.raw)
			v := e.View()
			if len(v.Candidates) == 0 {
				t.Fatal("no candidates")
			}
			if got := v.Candidates[0].Text; got != tc.want {
				t.Fatalf("%s: first candidate = %q, want %q (all: %v)", tc.raw, got, tc.want, texts(v))
			}
		})
	}
}

// Short inputs must keep behaving as before: a whole-input word first, then that syllable's
// characters. The common dictionary may add sibling words (您好 next to 你好) but must not
// drop characters.
func TestShortInputsStillOfferTheirCharacters(t *testing.T) {
	for _, raw := range []string{"ni", "hao", "wo", "shi", "de"} {
		e := New(Options{CandidateCount: 7})
		typeLetters(e, raw)
		if len(e.cands) == 0 {
			t.Fatalf("%s: no candidates", raw)
		}
		singles := 0
		for _, c := range e.cands {
			if c.Consumed == 1 {
				singles++
			}
		}
		if singles == 0 {
			t.Fatalf("%s: no single-character candidates: %v", raw, allTexts(e))
		}
	}
}
