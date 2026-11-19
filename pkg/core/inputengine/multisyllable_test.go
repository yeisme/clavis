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

// With three or more syllables and no word for the whole input, the first syllable's
// characters fill exactly one page, the sentence candidates (longest first) come next, and
// the rest of those characters follow. Before, all 60 slots went to first-syllable
// characters and nothing reached the later syllables.
func TestSentenceCandidatesFollowFirstPage(t *testing.T) {
	tests := []struct {
		raw, first string
		page       int
		sentences  []string
	}{
		{"yeshugen", "ye", 7, []string{"也数根", "也数"}},
		{"yeshugen", "ye", 5, []string{"也数根", "也数"}},
		{"woshiyeshugen", "wo", 7, []string{"我是也数根", "我是也数", "我是也", "我是"}},
		{"maishuren", "mai", 7, []string{"买数人", "买数"}},
		// A word in the middle of the input (zhong'guo) is used whole.
		{"woshizhongguoren", "wo", 7, []string{"我是中国人", "我是中国", "我是中", "我是"}},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			alone := New(Options{CandidateCount: tc.page})
			typeLetters(alone, tc.first)
			singles := allTexts(alone)

			e := New(Options{CandidateCount: tc.page})
			typeLetters(e, tc.raw)
			if len(e.cands) > maxCandidates {
				t.Fatalf("%d candidates, cap is %d", len(e.cands), maxCandidates)
			}
			// Page 1 keeps the top single in slot 1 (the number-key habit), promotes the
			// best whole-input sentence into slot 2, then continues with the remaining
			// singles — the one pushed off the page end stays reachable behind them.
			wantPage := make([]string, 0, tc.page)
			wantPage = append(wantPage, singles[0], tc.sentences[0])
			wantPage = append(wantPage, singles[1:tc.page-1]...)
			if got := texts(e.View()); !slices.Equal(got, wantPage) {
				t.Fatalf("first page = %v, want [top single, best sentence, rest of pages singles]: %v", got, wantPage)
			}
			// The remaining sentence candidates (shorter ones) follow the page.
			for i, want := range tc.sentences[1:] {
				c := e.cands[tc.page+i]
				if c.Text != want || c.consumed != len(e.segs)-(i+1) {
					t.Fatalf("slot %d = %q/%d, want %q/%d; all: %v", tc.page+i, c.Text, c.consumed, want, len(e.segs)-(i+1), allTexts(e))
				}
			}
			// The first syllable's characters past the first page are still all there, in order.
			var rest []string
			for _, c := range e.cands {
				if c.consumed == 1 {
					rest = append(rest, c.Text)
				}
			}
			if len(rest) <= tc.page || !slices.Equal(rest, singles[:len(rest)]) {
				t.Fatalf("single characters = %v, want a prefix of %v", rest, singles)
			}
		})
	}
}

// Choosing a sentence candidate commits the syllables it covers and leaves the rest composing.
func TestSentenceCandidateLeavesRestComposing(t *testing.T) {
	e := New(Options{})
	typeLetters(e, "woshizhongguoren")
	if r := pickAnywhere(t, e, "我是"); r.Commit != "我是" || e.View().Preedit != "zhong'guo'ren" {
		t.Fatalf("pick 我是: %+v preedit=%q", r, e.View().Preedit)
	}
	if r := pickOnFirstPage(t, e, "中国"); r.Commit != "中国" || e.View().Preedit != "ren" {
		t.Fatalf("pick 中国: %+v preedit=%q", r, e.View().Preedit)
	}
	if r := pickOnFirstPage(t, e, "人"); r.Commit != "人" || e.View().Composing {
		t.Fatalf("pick 人: %+v view=%+v", r, e.View())
	}

	e = New(Options{})
	typeLetters(e, "woshizhongguoren")
	if r := pickAnywhere(t, e, "我是中国人"); r.Commit != "我是中国人" || e.View().Composing {
		t.Fatalf("pick whole sentence: %+v view=%+v", r, e.View())
	}
}

// One- and two-syllable lists are exactly what they were: words first, then the first
// syllable's characters, nothing inserted.
func TestShortInputsKeepTheirCandidates(t *testing.T) {
	tests := []struct {
		raw       string
		firstPage []string
	}{
		{"nihao", []string{"你好", "你", "尼", "泥", "拟", "逆", "妮"}},
		{"zhongguo", []string{"中国", "中", "种", "重", "众", "终", "钟"}},
		{"shugen", []string{"数", "书", "术", "属", "述", "树", "输"}},
		{"gen", []string{"根", "跟", "亘", "亙", "哏", "揯", "搄"}},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			e := New(Options{})
			typeLetters(e, tc.raw)
			if got := texts(e.View()); !slices.Equal(got, tc.firstPage) {
				t.Fatalf("first page = %v, want %v", got, tc.firstPage)
			}
			singlesStarted := false
			for i, c := range e.cands {
				if c.consumed == 1 {
					singlesStarted = true
				} else if singlesStarted {
					t.Fatalf("candidate %d %q/%d after the single characters: %v", i, c.Text, c.consumed, allTexts(e))
				}
			}
		})
	}
}

// A word for the whole input needs no sentence candidates.
func TestWholeInputWordSkipsSentenceCandidates(t *testing.T) {
	e := New(Options{})
	typeLetters(e, "zhonghuarenmingongheguo")
	if e.cands[0].Text != "中华人民共和国" || e.cands[0].consumed != len(e.segs) {
		t.Fatalf("first = %q/%d", e.cands[0].Text, e.cands[0].consumed)
	}
	for _, c := range e.cands[1:] {
		if c.consumed != 1 {
			t.Fatalf("unexpected %q/%d: %v", c.Text, c.consumed, allTexts(e))
		}
	}
}
