package inputmigration

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestMergeLearningSumsAndCompressesUnpinned(t *testing.T) {
	left := LearningData{
		Enabled: true,
		Words:   []LearningEntry{{KeyDigest: Digest("nihao"), WordDigest: Digest("你好"), Frequency: 8, LastUsedNS: 10}},
		Bigrams: []BigramEntry{{PreviousDigest: Digest("你"), WordDigest: Digest("好"), Frequency: 3, LastUsedNS: 11}},
	}
	right := LearningData{
		Words:   []LearningEntry{{KeyDigest: Digest("nihao"), WordDigest: Digest("你好"), Frequency: 16, LastUsedNS: 20}},
		Bigrams: []BigramEntry{{PreviousDigest: Digest("你"), WordDigest: Digest("好"), Frequency: 5, LastUsedNS: 9}},
	}
	got := MergeLearning(left, right)
	wantWord := uint64(math.Round(1024 * math.Log1p(24)))
	wantBigram := uint64(math.Round(1024 * math.Log1p(8)))
	if got.Words[0].Frequency != wantWord || got.Words[0].LastUsedNS != 20 {
		t.Fatalf("word merge = %+v, want frequency=%d lastUsedNs=20", got.Words[0], wantWord)
	}
	if got.Bigrams[0].Frequency != wantBigram || got.Bigrams[0].LastUsedNS != 11 {
		t.Fatalf("bigram merge = %+v, want frequency=%d lastUsedNs=11", got.Bigrams[0], wantBigram)
	}
	if !bytesEqualLearning(MergeLearning(left, right), MergeLearning(right, left)) {
		t.Fatal("MergeLearning is not exchangeable")
	}
}

func TestMergeLearningPinnedNeverCompressedOrEvicted(t *testing.T) {
	pinned := LearningEntry{KeyDigest: Digest("pinned-key"), WordDigest: Digest("pinned-word"), Frequency: math.MaxUint64 - 2, Pinned: true}
	other := LearningEntry{KeyDigest: Digest("pinned-key"), WordDigest: Digest("pinned-word"), Frequency: 1, Pinned: false}
	words := make([]LearningEntry, 0, maxMergedEntries+1)
	words = append(words, pinned)
	for i := 0; i < maxMergedEntries; i++ {
		words = append(words, LearningEntry{KeyDigest: Digest("old-key-" + string(rune(i))), WordDigest: Digest("old-word"), Frequency: 1})
	}
	got := MergeLearning(LearningData{Words: words}, LearningData{Words: []LearningEntry{other}})
	var found *LearningEntry
	for i := range got.Words {
		if got.Words[i].KeyDigest == pinned.KeyDigest && got.Words[i].WordDigest == pinned.WordDigest {
			found = &got.Words[i]
			break
		}
	}
	if found == nil {
		t.Fatal("pinned entry was evicted")
	}
	if !found.Pinned || found.Frequency != math.MaxUint64-1 {
		t.Fatalf("pinned entry = %+v, want exact checked sum and pinned=true", *found)
	}
}

func TestMergeLearningUnionHasNoDuplicateKeys(t *testing.T) {
	entry := LearningEntry{KeyDigest: Digest("same-key"), WordDigest: Digest("same-word"), Frequency: 2}
	got := MergeLearning(LearningData{Words: []LearningEntry{entry, entry}}, LearningData{Words: []LearningEntry{entry}})
	if len(got.Words) != 1 {
		t.Fatalf("merged duplicate count = %d, want 1", len(got.Words))
	}
	if got.Words[0].Frequency != uint64(math.Round(1024*math.Log1p(6))) {
		t.Fatalf("merged duplicate frequency = %d", got.Words[0].Frequency)
	}
}

func TestMergeLearningNewWordCanEnterCapacity(t *testing.T) {
	words := make([]LearningEntry, 0, maxMergedEntries)
	for i := 0; i < maxMergedEntries-1; i++ {
		words = append(words, LearningEntry{
			KeyDigest: Digest("old-high-" + string(rune(i))), WordDigest: Digest("word"), Frequency: 100,
		})
	}
	// The existing tail has a lower compressed score. The new word's merged
	// score overtakes that tail and enters the capacity-limited result.
	oldTail := LearningEntry{KeyDigest: Digest("old-tail"), WordDigest: Digest("word"), Frequency: 10}
	newWord := LearningEntry{KeyDigest: Digest("new-word"), WordDigest: Digest("word"), Frequency: 11}
	words = append(words, oldTail)
	got := MergeLearning(LearningData{Words: words}, LearningData{Words: []LearningEntry{newWord}})
	found := false
	for _, entry := range got.Words {
		if entry.KeyDigest == newWord.KeyDigest && entry.WordDigest == newWord.WordDigest {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("new word did not enter top %d after compression", maxMergedEntries)
	}
	if len(got.Words) != maxMergedEntries {
		t.Fatalf("merged capacity = %d, want %d", len(got.Words), maxMergedEntries)
	}
}

func bytesEqualLearning(a, b LearningData) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return strings.EqualFold(string(x), string(y))
}
