package inputmigration

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestMergeLearningSumsAndCompressesUnpinned(t *testing.T) {
	left := LearningData{
		Enabled:     true,
		SummaryOnly: true,
		Words:       []LearningEntry{{KeyDigest: Digest("nihao"), WordDigest: Digest("你好"), Frequency: 8, LastUsedNS: 10}},
		Bigrams:     []BigramEntry{{PreviousDigest: Digest("你"), WordDigest: Digest("好"), Frequency: 3, LastUsedNS: 11}},
	}
	right := LearningData{
		SummaryOnly: true,
		Words:       []LearningEntry{{KeyDigest: Digest("nihao"), WordDigest: Digest("你好"), Frequency: 16, LastUsedNS: 20}},
		Bigrams:     []BigramEntry{{PreviousDigest: Digest("你"), WordDigest: Digest("好"), Frequency: 5, LastUsedNS: 9}},
	}
	got := MergeLearning(left, right)
	if got.Words[0].Frequency != 24 || got.Words[0].LastUsedNS != 20 {
		t.Fatalf("word merge = %+v, want frequency=24 lastUsedNs=20", got.Words[0])
	}
	if got.Bigrams[0].Frequency != 8 || got.Bigrams[0].LastUsedNS != 11 {
		t.Fatalf("bigram merge = %+v, want frequency=8 lastUsedNs=11", got.Bigrams[0])
	}
	if !got.SummaryOnly {
		t.Fatal("merged learning with digest entries must declare SummaryOnly=true")
	}
	if !bytesEqualLearning(MergeLearning(left, right), MergeLearning(right, left)) {
		t.Fatal("MergeLearning is not commutative")
	}

	// Saturation cap: unpinned sum beyond maxMergedFrequency caps cleanly at maxMergedFrequency.
	heavyA := LearningData{
		Enabled:     true,
		SummaryOnly: true,
		Words:       []LearningEntry{{KeyDigest: Digest("cap"), WordDigest: Digest("上限"), Frequency: 3000}},
		Bigrams:     []BigramEntry{{PreviousDigest: Digest("上"), WordDigest: Digest("限"), Frequency: 2500}},
	}
	heavyB := LearningData{
		SummaryOnly: true,
		Words:       []LearningEntry{{KeyDigest: Digest("cap"), WordDigest: Digest("上限"), Frequency: 2500}},
		Bigrams:     []BigramEntry{{PreviousDigest: Digest("上"), WordDigest: Digest("限"), Frequency: 3000}},
	}
	capped := MergeLearning(heavyA, heavyB)
	if capped.Words[0].Frequency != maxMergedFrequency || capped.Bigrams[0].Frequency != maxMergedFrequency {
		t.Fatalf("expected capped frequency=%d, got word=%d bigram=%d", maxMergedFrequency, capped.Words[0].Frequency, capped.Bigrams[0].Frequency)
	}
}

func TestMergeLearningNoInflationAndThreeDeviceAssociativity(t *testing.T) {
	// Single-frequency entry must NEVER inflate (compress(1) == 1).
	single := LearningData{
		Enabled: true,
		Words:   []LearningEntry{NewLearningEntry("yi", "一", 1, false)},
		Bigrams: []BigramEntry{NewBigramEntry("第", "一", 1)},
	}
	mergedSingle := MergeLearning(single, LearningData{})
	if mergedSingle.Words[0].Frequency != 1 || mergedSingle.Bigrams[0].Frequency != 1 {
		t.Fatalf("single frequency inflated: word=%d bigram=%d, want 1 and 1",
			mergedSingle.Words[0].Frequency, mergedSingle.Bigrams[0].Frequency)
	}
	if !mergedSingle.SummaryOnly {
		t.Fatal("merging non-empty summary with empty zero-value LearningData must keep SummaryOnly=true")
	}

	// Three-device associativity & commutativity across all groupings and permutations:
	// Merge(Merge(A, B), C) == Merge(A, Merge(B, C)) == Merge(Merge(C, A), B) ...
	devA := LearningData{
		Enabled:     true,
		SummaryOnly: true,
		Words: []LearningEntry{
			{KeyDigest: Digest("gong"), WordDigest: Digest("共"), Frequency: 1, LastUsedNS: 100},
			{KeyDigest: Digest("bao"), WordDigest: Digest("饱"), Frequency: 2000, LastUsedNS: 50},
			{KeyDigest: Digest("pin"), WordDigest: Digest("顶"), Frequency: 3, Pinned: true, LastUsedNS: 10},
			{KeyDigest: Digest("onlyA"), WordDigest: Digest("甲"), Frequency: 4, LastUsedNS: 11},
		},
		Bigrams: []BigramEntry{
			{PreviousDigest: Digest("你"), WordDigest: Digest("好"), Frequency: 1, LastUsedNS: 10},
			{PreviousDigest: Digest("超"), WordDigest: Digest("限"), Frequency: 3000, LastUsedNS: 10},
		},
	}
	devB := LearningData{
		Enabled:     true,
		SummaryOnly: false, // Even if a caller forgot SummaryOnly on one device, result stays valid.
		Words: []LearningEntry{
			{KeyDigest: Digest("gong"), WordDigest: Digest("共"), Frequency: 2, LastUsedNS: 300},
			{KeyDigest: Digest("bao"), WordDigest: Digest("饱"), Frequency: 1500, LastUsedNS: 70},
			{KeyDigest: Digest("pin"), WordDigest: Digest("顶"), Frequency: 5, Pinned: false, LastUsedNS: 20},
		},
		Bigrams: []BigramEntry{
			{PreviousDigest: Digest("你"), WordDigest: Digest("好"), Frequency: 2, LastUsedNS: 25},
			{PreviousDigest: Digest("超"), WordDigest: Digest("限"), Frequency: 2000, LastUsedNS: 30},
		},
	}
	devC := LearningData{
		Enabled:     true,
		SummaryOnly: true,
		Words: []LearningEntry{
			{KeyDigest: Digest("gong"), WordDigest: Digest("共"), Frequency: 5, LastUsedNS: 200},
			{KeyDigest: Digest("bao"), WordDigest: Digest("饱"), Frequency: 1200, LastUsedNS: 60},
			{KeyDigest: Digest("onlyC"), WordDigest: Digest("丙"), Frequency: 1, LastUsedNS: 40},
		},
		Bigrams: []BigramEntry{
			{PreviousDigest: Digest("你"), WordDigest: Digest("好"), Frequency: 4, LastUsedNS: 15},
			{PreviousDigest: Digest("超"), WordDigest: Digest("限"), Frequency: 900, LastUsedNS: 20},
		},
	}

	leftAssoc := MergeLearning(MergeLearning(devA, devB), devC)
	rightAssoc := MergeLearning(devA, MergeLearning(devB, devC))
	if !bytesEqualLearning(leftAssoc, rightAssoc) {
		t.Fatalf("MergeLearning is not associative:\n(A+B)+C = %+v\nA+(B+C) = %+v", leftAssoc, rightAssoc)
	}
	for _, perm := range [][3]LearningData{
		{devA, devC, devB},
		{devB, devA, devC},
		{devB, devC, devA},
		{devC, devA, devB},
		{devC, devB, devA},
	} {
		got := MergeLearning(MergeLearning(perm[0], perm[1]), perm[2])
		if !bytesEqualLearning(leftAssoc, got) {
			t.Fatalf("MergeLearning order-dependent across 3 devices:\nwant = %+v\n got = %+v", leftAssoc, got)
		}
	}

	byKey := make(map[string]LearningEntry, len(leftAssoc.Words))
	for _, w := range leftAssoc.Words {
		byKey[w.KeyDigest] = w
	}
	if w := byKey[Digest("gong")]; w.Frequency != 8 || w.LastUsedNS != 300 {
		t.Fatalf("gong merged = %+v, want freq=8 lastUsed=300", w)
	}
	if w := byKey[Digest("bao")]; w.Frequency != maxMergedFrequency || w.LastUsedNS != 70 {
		t.Fatalf("bao merged = %+v, want freq=%d lastUsed=70", w, maxMergedFrequency)
	}
	if w := byKey[Digest("pin")]; !w.Pinned || w.Frequency != 8 || w.LastUsedNS != 20 {
		t.Fatalf("pin merged = %+v, want pinned=true freq=8 lastUsed=20", w)
	}
	if w := byKey[Digest("onlyC")]; w.Frequency != 1 {
		t.Fatalf("onlyC inflated = %+v, want freq=1", w)
	}

	// Result must pass PersonalBaseline.Validate() without SummaryOnly contradiction.
	b := baseline()
	b.Learning = leftAssoc
	if err := b.Validate(); err != nil {
		t.Fatalf("merged 3-device learning failed PersonalBaseline.Validate: %v", err)
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
	if got.Words[0].Frequency != 6 {
		t.Fatalf("merged duplicate frequency = %d, want 6", got.Words[0].Frequency)
	}
}

func TestMergeLearningNewWordCanEnterCapacity(t *testing.T) {
	words := make([]LearningEntry, 0, maxMergedEntries)
	for i := 0; i < maxMergedEntries-1; i++ {
		words = append(words, LearningEntry{
			KeyDigest: Digest("old-high-" + string(rune(i))), WordDigest: Digest("word"), Frequency: 100,
		})
	}
	// The existing tail has a lower score. The new word's merged score overtakes
	// that tail and enters the capacity-limited result.
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
		t.Fatalf("new word did not enter top %d after merge", maxMergedEntries)
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
