package inputengine

import (
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/ime"
)

func BenchmarkEngineKeystrokeQuanpin(b *testing.B) {
	e := New(Options{CandidateCount: 7})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.HandleKey(Key{Kind: KeyLetter, Rune: 'a'})
		e.HandleKey(Key{Kind: KeyBackspace})
	}
}

func BenchmarkEngineTypeNihaoQuanpin(b *testing.B) {
	e := New(Options{CandidateCount: 7})
	keys := []Key{
		{Kind: KeyLetter, Rune: 'n'},
		{Kind: KeyLetter, Rune: 'i'},
		{Kind: KeyLetter, Rune: 'h'},
		{Kind: KeyLetter, Rune: 'a'},
		{Kind: KeyLetter, Rune: 'o'},
		{Kind: KeySpace},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.HandleKey(keys[i%len(keys)])
	}
}

func BenchmarkEngineTypeNihcXiaohe(b *testing.B) {
	e := New(Options{CandidateCount: 7, Scheme: "xiaohe", FuzzyPinyin: []string{"z=zh"}})
	keys := []Key{
		{Kind: KeyLetter, Rune: 'n'},
		{Kind: KeyLetter, Rune: 'i'},
		{Kind: KeyLetter, Rune: 'h'},
		{Kind: KeyLetter, Rune: 'c'},
		{Kind: KeySpace},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.HandleKey(keys[i%len(keys)])
	}
}

func BenchmarkEngineKeystrokeCustomRimeDict(b *testing.B) {
	lex, err := ime.CompileRimeDict(strings.NewReader("---\nname: bench\n...\n克莱维斯\tke lai wei si\t999999\n你好世界\tni hao shi jie\t800000\n"))
	if err != nil {
		b.Fatal(err)
	}
	e := New(Options{
		CandidateCount: 7,
		Dictionary:     ChainDictionaries(NewIMEDictionary(lex), StarterDictionary(), UnihanDictionary()),
	})
	keys := []Key{
		{Kind: KeyLetter, Rune: 'n'},
		{Kind: KeyLetter, Rune: 'i'},
		{Kind: KeyLetter, Rune: 'h'},
		{Kind: KeyLetter, Rune: 'a'},
		{Kind: KeyLetter, Rune: 'o'},
		{Kind: KeySpace},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.HandleKey(keys[i%len(keys)])
	}
}
