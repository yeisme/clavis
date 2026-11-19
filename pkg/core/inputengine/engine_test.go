package inputengine

import (
	"reflect"
	"slices"
	"testing"
)

func typeLetters(e *Engine, s string) {
	for _, r := range s {
		if r == '\'' {
			e.HandleKey(Key{Kind: KeyApostrophe, Rune: r})
			continue
		}
		e.HandleKey(Key{Kind: KeyLetter, Rune: r})
	}
}

func texts(v View) []string {
	out := make([]string, len(v.Candidates))
	for i, c := range v.Candidates {
		out[i] = c.Text
	}
	return out
}

func TestSegment(t *testing.T) {
	cases := map[string][]string{
		"nihao":   {"ni", "hao"},
		"xian":    {"xian"},
		"xi'an":   {"xi", "an"},
		"zhongg":  {"zhong", "g"},
		"zg":      {"z", "g"},
		"womende": {"wo", "men", "de"},
	}
	for raw, want := range cases {
		if got := Segment(raw); !reflect.DeepEqual(got, want) {
			t.Fatalf("Segment(%q) = %v want %v", raw, got, want)
		}
	}
}

func TestCandidatesWordsJianpinAndChars(t *testing.T) {
	e := New(Options{CandidateCount: 9})
	typeLetters(e, "nihao")
	if v := e.View(); len(v.Candidates) == 0 || v.Candidates[0].Text != "你好" {
		t.Fatalf("nihao → %v", texts(v))
	}
	e.Reset()
	typeLetters(e, "zg")
	if v := e.View(); !slices.Contains(texts(v), "中国") {
		t.Fatalf("zg jianpin → %v", texts(v))
	}
	e.Reset()
	typeLetters(e, "de")
	if v := e.View(); v.Candidates[0].Text != "的" {
		t.Fatalf("de → %v (frequency order)", texts(v))
	}
	e.Reset()
	typeLetters(e, "juede")
	if !slices.Contains(texts(e.View()), "觉得") {
		t.Fatalf("polyphone word missing: %v", texts(e.View()))
	}
}

func TestStateMachineCommitKeys(t *testing.T) {
	e := New(Options{CandidateCount: 5})
	if r := e.HandleKey(Key{Kind: KeyDigit, Rune: '1'}); r.Consume {
		t.Fatal("digit consumed while idle")
	}
	typeLetters(e, "nihao")
	if r := e.HandleKey(Key{Kind: KeySpace}); !r.Consume || r.Commit != "你好" || e.Raw() != "" {
		t.Fatalf("space: %+v raw=%q", r, e.Raw())
	}
	typeLetters(e, "nihao")
	if r := e.HandleKey(Key{Kind: KeyEnter}); r.Commit != "nihao" {
		t.Fatalf("enter commits raw: %+v", r)
	}
	typeLetters(e, "ni")
	if r := e.HandleKey(Key{Kind: KeyEscape}); !r.Consume || r.Commit != "" || e.Raw() != "" {
		t.Fatalf("esc: %+v", r)
	}
	typeLetters(e, "a")
	if r := e.HandleKey(Key{Kind: KeyBackspace}); !r.Consume || e.View().Composing {
		t.Fatalf("backspace to empty: %+v", r)
	}
	// Space while idle passes through.
	if r := e.HandleKey(Key{Kind: KeySpace}); r.Consume {
		t.Fatal("idle space consumed")
	}
	// Out-of-range digit passes through, with no commit.
	typeLetters(e, "ni")
	if r := e.HandleKey(Key{Kind: KeyDigit, Rune: '9'}); r.Consume || r.Commit != "" || e.Raw() != "ni" {
		t.Fatalf("digit 9 > page size: %+v raw=%q", r, e.Raw())
	}
	// Digit picks candidate n.
	want := e.View().Candidates[1].Text
	if r := e.HandleKey(Key{Kind: KeyDigit, Rune: '2'}); r.Commit != want {
		t.Fatalf("digit 2: %+v want %q", r, want)
	}
}

func TestPartialCommitKeepsRemainder(t *testing.T) {
	e := New(Options{CandidateCount: 9})
	typeLetters(e, "nihaoa") // no 3-syllable word: char for "ni" must leave "hao'a"
	v := e.View()
	idx := slices.IndexFunc(v.Candidates, func(c Candidate) bool { return c.Text == "你好" })
	if idx < 0 {
		t.Fatalf("prefix word missing: %v", texts(v))
	}
	r := e.HandleKey(Key{Kind: KeyDigit, Rune: rune('1' + idx)})
	if r.Commit != "你好" || e.Raw() != "a" || !e.View().Composing {
		t.Fatalf("partial commit: %+v raw=%q", r, e.Raw())
	}
}

func TestCandidateHighlightMovement(t *testing.T) {
	e := New(Options{CandidateCount: 3})
	typeLetters(e, "shi")
	if got := e.View().Selected; got != 0 {
		t.Fatalf("initial selection = %d, want 0", got)
	}
	last := len(e.View().Candidates) - 1
	for want := 1; want <= last; want++ {
		if r := e.HandleKey(Key{Kind: KeyNavigation, NavDir: NavDown}); !r.Consume || !r.Changed {
			t.Fatalf("down %d: %+v", want, r)
		}
		if got := e.View().Selected; got != want {
			t.Fatalf("down %d selected = %d, want %d", want, got, want)
		}
	}
	if r := e.HandleKey(Key{Kind: KeyNavigation, NavDir: NavDown}); !r.Consume || !r.Changed || e.View().Selected != last {
		t.Fatalf("down at end: %+v selected=%d want=%d", r, e.View().Selected, last)
	}
	for want := last - 1; want >= 0; want-- {
		if r := e.HandleKey(Key{Kind: KeyNavigation, NavDir: NavUp}); !r.Consume || !r.Changed {
			t.Fatalf("up %d: %+v", want, r)
		}
		if got := e.View().Selected; got != want {
			t.Fatalf("up %d selected = %d, want %d", want, got, want)
		}
	}
	if r := e.HandleKey(Key{Kind: KeyNavigation, NavDir: NavUp}); !r.Consume || !r.Changed || e.View().Selected != 0 {
		t.Fatalf("up at start: %+v selected=%d", r, e.View().Selected)
	}
	if e.Raw() != "shi" {
		t.Fatalf("directional navigation changed raw to %q", e.Raw())
	}

	e.HandleKey(Key{Kind: KeyPageNext})
	if v := e.View(); v.Page != 1 || v.Selected != 0 {
		t.Fatalf("page next view = page %d selected %d, want page 1 selected 0", v.Page, v.Selected)
	}
	e.HandleKey(Key{Kind: KeyNavigation, NavDir: NavDown})
	if got := e.View().Selected; got != 1 {
		t.Fatalf("second-page down selected = %d, want 1", got)
	}
	e.HandleKey(Key{Kind: KeyPagePrev})
	if got := e.View().Selected; got != 0 {
		t.Fatalf("page prev selected = %d, want 0", got)
	}
}

func TestPagingAndPunctuation(t *testing.T) {
	e := New(Options{CandidateCount: 3})
	typeLetters(e, "shi")
	first := texts(e.View())
	e.HandleKey(Key{Kind: KeyPageNext})
	v := e.View()
	if v.Page != 1 || !v.HasPrev || slices.Equal(texts(v), first) {
		t.Fatalf("page next: %+v", v)
	}
	e.HandleKey(Key{Kind: KeyPagePrev})
	if !slices.Equal(texts(e.View()), first) {
		t.Fatal("page prev")
	}
	r := e.HandleKey(Key{Kind: KeyOther, Rune: ','})
	if !r.Consume || r.Commit != first[0]+"," || e.Raw() != "" {
		t.Fatalf("punctuation commits top + char: %+v", r)
	}
}

// Mutual-exclusion rules (design §6): chords/navigation reset and pass through; modifiers
// never enter composition; ASCII mode passes letters.
func TestMutualExclusion(t *testing.T) {
	e := New(Options{CandidateCount: 5, ShiftToggleASCII: true})
	typeLetters(e, "ni")
	if r := e.HandleKey(Key{Kind: KeyLetter, Rune: 'c', Chord: true}); r.Consume || e.Raw() != "" {
		t.Fatalf("ctrl+c while composing: %+v", r)
	}
	if r := e.HandleKey(Key{Kind: KeyLetter, Rune: 'c', Chord: true}); r.Consume {
		t.Fatal("idle chord consumed")
	}
	typeLetters(e, "ni")
	if r := e.HandleKey(Key{Kind: KeyNavigation}); r.Consume || e.Raw() != "" {
		t.Fatalf("arrow: %+v", r)
	}
	// Shift tap toggles ASCII; mid-composition it commits raw letters.
	typeLetters(e, "hao")
	e.HandleKey(Key{Kind: KeyShiftDown})
	if r := e.HandleKey(Key{Kind: KeyShiftUp}); r.Commit != "hao" || !e.ASCII() {
		t.Fatalf("shift toggle: %+v ascii=%v", r, e.ASCII())
	}
	if r := e.HandleKey(Key{Kind: KeyLetter, Rune: 'a'}); r.Consume {
		t.Fatal("ASCII mode consumed letter")
	}
	// Shift used as a modifier (Shift+A) does not toggle.
	e.HandleKey(Key{Kind: KeyShiftDown})
	e.HandleKey(Key{Kind: KeyOther, Rune: 'A'})
	e.HandleKey(Key{Kind: KeyShiftUp})
	if !e.ASCII() {
		t.Fatal("shift chord toggled mode")
	}
	// Mouse reset.
	e.SetASCII(false)
	typeLetters(e, "ni")
	if !e.Reset() || e.Raw() != "" {
		t.Fatal("reset")
	}
}

func TestAICandidatesStaleAndAppended(t *testing.T) {
	e := New(Options{CandidateCount: 9})
	typeLetters(e, "nihao")
	if e.SetAICandidates("niha", []string{"X"}) {
		t.Fatal("stale AI result accepted")
	}
	if !e.SetAICandidates("nihao", []string{"你好呀", "你好"}) {
		t.Fatal("fresh AI result rejected")
	}
	v := e.View()
	if v.Candidates[0].Text != "你好" || v.Candidates[0].AI {
		t.Fatalf("local word must stay first: %+v", v.Candidates[:2])
	}
	idx := slices.IndexFunc(v.Candidates, func(c Candidate) bool { return c.Text == "你好呀" })
	if idx < 0 || !v.Candidates[idx].AI {
		t.Fatalf("AI candidate missing/unmarked: %v", texts(v))
	}
	// Typing clears AI results.
	typeLetters(e, "a")
	if slices.Contains(texts(e.View()), "你好呀") {
		t.Fatal("AI candidates survived raw change")
	}
}

func TestRawLengthBounded(t *testing.T) {
	e := New(Options{CandidateCount: 5})
	for i := 0; i < 200; i++ {
		e.HandleKey(Key{Kind: KeyLetter, Rune: 'a'})
	}
	if len(e.Raw()) > maxRawLen {
		t.Fatalf("raw grew to %d", len(e.Raw()))
	}
}

type stubOnlyDict struct {
	singles map[string][]string
}

func (d stubOnlyDict) LookupPhrases([]string, func(string) bool) {}

func (d stubOnlyDict) LookupSingle(seg string, emit func(string) bool) {
	for _, w := range d.singles[seg] {
		if !emit(w) {
			return
		}
	}
}

func TestPunctuationNeverAutoCommitsAICandidate(t *testing.T) {
	// Case 1: only AI candidate(s) exist -> punctuation commits raw letters plus itself.

	e := New(Options{CandidateCount: 5, Dictionary: stubOnlyDict{}})
	typeLetters(e, "nihao")
	if !e.SetAICandidates("nihao", []string{"你好AI"}) {
		t.Fatal("expected AI candidates to be accepted")
	}
	if v := e.View(); len(v.Candidates) != 1 || !v.Candidates[0].AI {
		t.Fatalf("expected only AI candidate, got %+v", v.Candidates)
	}
	if r := e.HandleKey(Key{Kind: KeyOther, Rune: ','}); !r.Consume || r.Commit != "nihao," || e.View().Composing || e.Raw() != "" {
		t.Fatalf("punctuation with only AI candidate must commit raw plus rune, got res=%+v raw=%q", r, e.Raw())
	}

	// Case 2: AI candidate is cands[0] (full-length) while dict only has a partial single-char fallback ->
	// punctuation must never commit the AI candidate.
	e2 := New(Options{CandidateCount: 5, Dictionary: stubOnlyDict{singles: map[string][]string{"ni": {"你"}}}})
	typeLetters(e2, "nizz")
	if !e2.SetAICandidates("nizz", []string{"你走"}) {
		t.Fatal("expected AI candidates to be accepted")
	}
	if v := e2.View(); len(v.Candidates) < 2 || !v.Candidates[0].AI || v.Candidates[1].AI {
		t.Fatalf("expected AI at cands[0] and dict at cands[1], got %+v", v.Candidates)
	}
	if r := e2.HandleKey(Key{Kind: KeyOther, Rune: '.'}); !r.Consume || r.Commit != "nizz." {
		t.Fatalf("punctuation must not auto-commit AI candidate, got res=%+v", r)
	}

	// Case 3: dictionary-backed full-length match still commits on punctuation even when AI is present.
	e3 := New(Options{CandidateCount: 9})
	typeLetters(e3, "nihao")
	_ = e3.SetAICandidates("nihao", []string{"你好呀"})
	if r := e3.HandleKey(Key{Kind: KeyOther, Rune: ','}); !r.Consume || r.Commit != "你好," {
		t.Fatalf("expected dictionary full-length commit, got %+v", r)
	}
}
