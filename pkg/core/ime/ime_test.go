package ime

import (
	"bytes"
	"encoding/binary"
	"errors"
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
	var exported bytes.Buffer
	if err := ExportRimeDict(&exported, d); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(exported.String(), "你好\tni hao\t100\n") {
		t.Fatalf("CLVD export lost record: %q", exported.String())
	}
}

func TestExportRimeDictCanonicalRoundTrip(t *testing.T) {
	input := "---\nname: source\n...\n乙\tz yi\t1.5\n甲\ta\t2\n丙\tz yi\t3\n"
	first, err := CompileRimeDict(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	var exported bytes.Buffer
	if err := ExportRimeDict(&exported, first); err != nil {
		t.Fatal(err)
	}
	want := "---\nname: clavis\n...\n乙\tz yi\t150\n甲\ta\t2\n丙\tz yi\t3\n"
	if exported.String() != want {
		t.Fatalf("exported dictionary = %q, want %q", exported.String(), want)
	}
	second, err := CompileRimeDict(bytes.NewReader(exported.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	var reexported bytes.Buffer
	if err := ExportRimeDict(&reexported, second); err != nil {
		t.Fatal(err)
	}
	if reexported.String() != exported.String() {
		t.Fatalf("second export = %q, first export = %q", reexported.String(), exported.String())
	}
	if got := second.Lookup("zyi"); len(got) != 2 || got[0].Word != "乙" || got[1].Word != "丙" {
		t.Fatalf("lookup ordering changed: %#v", got)
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestExportRimeDictErrors(t *testing.T) {
	if err := ExportRimeDict(nil, StarterLexicon()); err == nil {
		t.Fatal("nil writer should fail")
	}
	var b bytes.Buffer
	if err := ExportRimeDict(&b, nil); err == nil {
		t.Fatal("nil lexicon should fail")
	}
	if err := ExportRimeDict(errorWriter{}, StarterLexicon()); err == nil || err.Error() != "write failed" {
		t.Fatalf("writer error = %v", err)
	}
}

func TestLoadClvDictRejectsOldVersion(t *testing.T) {
	l, err := CompileRimeDict(strings.NewReader("词\tci\t1\n"))
	if err != nil {
		t.Fatal(err)
	}
	data := append([]byte(nil), l.Bytes()...)
	binary.LittleEndian.PutUint16(data[4:], 1)
	if _, err := LoadClvDict(data); err == nil || err.Error() != "ime: unsupported CLVD version" {
		t.Fatalf("old version error = %v", err)
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
