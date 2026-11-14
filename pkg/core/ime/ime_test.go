package ime

import (
	"strings"
	"testing"
)

func feed(c *Composer, s string) KeyOutcome {
	var o KeyOutcome
	for _, r := range s {
		o = c.FeedKey(KeyEvent{Kind: KeyRune, Rune: r})
	}
	return o
}
func TestStarterAndSchemes(t *testing.T) {
	if StarterLexicon().Len() < 1500 {
		t.Fatalf("starter too small: %d", StarterLexicon().Len())
	}
	for _, scheme := range []string{"xiaohe", "ziranma", "microsoft", "sogou", "pinyin_plus"} {
		c := NewComposer(Config{Scheme: scheme, CandidateCount: 7, AutoPairPunctuation: true}, nil)
		o := feed(c, "nihc")
		if len(o.Candidates) == 0 || o.Candidates[0].Word != "你好" {
			t.Fatalf("%s nihc: %#v", scheme, o.Candidates)
		}
	}
	c := NewComposer(Config{Scheme: "quanpin", CandidateCount: 7, AutoPairPunctuation: true, FuzzyPinyin: []string{"z=zh"}}, nil)
	if o := feed(c, "nihao"); len(o.Candidates) == 0 || o.Candidates[0].Word != "你好" {
		t.Fatalf("quanpin: %#v", o.Candidates)
	}
	c = NewComposer(Config{Scheme: "quanpin", CandidateCount: 7, AutoPairPunctuation: true}, nil)
	if o := feed(c, "nh"); len(o.Candidates) == 0 || o.Candidates[0].Word != "你好" {
		t.Fatalf("initialism: %#v", o.Candidates)
	}
}
func TestComposerErgonomics(t *testing.T) {
	c := NewComposer(Config{Scheme: "quanpin", CandidateCount: 7, AutoPairPunctuation: true, ShiftToggleASCII: true}, nil)
	if o := c.FeedKey(KeyEvent{Kind: KeyShift}); !o.ASCIIMode {
		t.Fatal("shift did not toggle ASCII")
	}
	o := feed(c, "hello")
	o = c.FeedKey(KeyEvent{Kind: KeyEnter})
	c.FeedKey(KeyEvent{Kind: KeyShift})
	if o.CommitText != "hello" {
		t.Fatalf("ascii commit %q", o.CommitText)
	}
	for _, r := range "3.14" {
		c.FeedKey(KeyEvent{Kind: KeyRune, Rune: r})
	}
	o = c.FeedKey(KeyEvent{Kind: KeyEnter})
	if o.CommitText != "3.14" {
		t.Fatalf("decimal %q", o.CommitText)
	}
	o = c.FeedKey(KeyEvent{Kind: KeyRune, Rune: '['})
	if o.CommitText != "【】" || o.CursorBackOffset != 1 {
		t.Fatalf("pair %#v", o)
	}
	c = NewComposer(Config{Scheme: "quanpin", CandidateCount: 7, AutoPairPunctuation: true}, nil)
	feed(c, "nihao")
	o = c.FeedKey(KeyEvent{Kind: KeyRune, Rune: '['})
	if o.CommitText != "你" {
		t.Fatalf("left char %q", o.CommitText)
	}
	c = NewComposer(Config{Scheme: "quanpin", CandidateCount: 7}, nil)
	o = feed(c, "/cr")
	if o.QuickRoute.Kind != RoutePromptTemplate || o.QuickRoute.Text != "cr" {
		t.Fatalf("route %#v", o.QuickRoute)
	}
	o = feed(c, "aft")
	if o.QuickRoute.Text != "craft" {
		t.Fatalf("route continuation %#v", o.QuickRoute)
	}
}
func TestRimeRoundTrip(t *testing.T) {
	l, e := CompileRimeDict(strings.NewReader("---\nname: test\n...\n你好\tni hao\t100\n中国\tzhong guo\t90\n双\tshuang\t80\n"))
	if e != nil {
		t.Fatal(e)
	}
	d, e := LoadClvDict(l.Bytes())
	if e != nil {
		t.Fatal(e)
	}
	if d.Len() != 3 || d.Lookup("nihao")[0].Word != "你好" {
		t.Fatalf("roundtrip %d %#v", d.Len(), d.Lookup("nihao"))
	}
}

type testUser struct{}

func (testUser) LookupBoost(_, word, _ string) int32 {
	if word == "中国" {
		return 1000
	}
	return 0
}
func (testUser) CustomWordsForPrefix(string) []UserCandidate { return nil }
func TestUserBoost(t *testing.T) {
	s := NewLexiconStack(StarterLexicon())
	s.User = testUser{}
	got := s.LookupWithPrev("zhongguo", "")
	if len(got) == 0 || got[0].Word != "中国" {
		t.Fatalf("boost %#v", got)
	}
}
