package ime

import "testing"

func BenchmarkComposerKeystroke(b *testing.B) {
	c := NewComposer(Config{Scheme: "quanpin", CandidateCount: 7, AutoPairPunctuation: true}, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.FeedKey(KeyEvent{Kind: KeyRune, Rune: 'a'})
		c.FeedKey(KeyEvent{Kind: KeyBackspace})
	}
}
func BenchmarkRimeDictLookup(b *testing.B) {
	l := StarterLexicon()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = l.Lookup("nihao")
	}
}
