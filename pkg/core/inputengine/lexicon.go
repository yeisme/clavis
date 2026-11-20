package inputengine

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/yeisme/clavis/pkg/core/engine"
)

// Lexicon: syllable → characters (frequency ordered) and word list, built once from the
// embedded Unihan readings (pkg/core/engine/pinyin_table.go) plus the frequency/word lists
// below. Offline and deterministic (docs/design/own-input-engine.md §5).

// The starter lexicon lives in data/starter.json, embedded below, so it is a versioned,
// digest-checked artifact instead of Go source: bumping a word no longer means editing a
// .go file, and the shipped dictionary can be diffed and verified on its own.
//
// hanzi is a frequency-ordered run of characters (no per-character reading); words is the
// phrase list. Rime .dict.yaml cannot express "order only, readings irrelevant" without
// inventing a fake pinyin per character, so user-supplied dictionaries use Rime (see
// pkg/core/ime/rime_compiler.go) while this internal starter keeps its own shape.
//
//go:embed data/starter.json
var starterJSON []byte

const starterDigest = "sha256:e6060e3f8cbc421bbc90a7d19e3db7422428301987a0d9d60ad1a68389110346"

type starterData struct {
	Version int      `json:"version"`
	Hanzi   string   `json:"hanzi"`
	Words   []string `json:"words"`
}

// loadStarter verifies the embedded starter data against starterDigest and returns it.
// A mismatch is a build-integrity failure, so it panics: a quietly wrong dictionary would
// break every lookup, which is worse than refusing to start.
func loadStarter() starterData {
	sum := sha256.Sum256(starterJSON)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != starterDigest {
		panic("inputengine: embedded starter digest mismatch: " + got)
	}
	var d starterData
	if err := json.Unmarshal(starterJSON, &d); err != nil {
		panic("inputengine: embedded starter data is invalid: " + err.Error())
	}
	return d
}

const maxPhraseBucketSyls = 16

type sylTrieNode struct {
	next   [26]uint16
	isSyl  bool
	prefix string
	chars  []string
}

type phraseRecord struct {
	syls  []string
	words []string
}

type lexicon struct {
	bySyllable map[string][]rune   // syllable → chars, frequency ordered
	words      map[string][]string // "ni hao" → words (first in list order)
	byInitials map[string][]string // "nh" → words
	syllables  map[string]bool
	prefixes   map[string]bool // every prefix of a valid syllable (for the trailing segment)
	maxSylLen  int
	rank       map[rune]int

	trie          []sylTrieNode
	phraseBuckets [maxPhraseBucketSyls][26][26][]phraseRecord
	longPhrases   []phraseRecord
}

// rankOf orders characters by frequency; unlisted characters sort after all listed ones.
func (l *lexicon) rankOf(r rune) int {
	if n, ok := l.rank[r]; ok {
		return n
	}
	return 1<<20 + int(r)
}

func (l *lexicon) LookupPhrases(segs []string, emit func(word string) bool) {
	n := len(segs)
	if n < 2 || segs[0] == "" || segs[1] == "" {
		return
	}
	var records []phraseRecord
	c0, c1 := segs[0][0], segs[1][0]
	if n < maxPhraseBucketSyls && c0 >= 'a' && c0 <= 'z' && c1 >= 'a' && c1 <= 'z' {
		records = l.phraseBuckets[n][c0-'a'][c1-'a']
	} else if n >= maxPhraseBucketSyls {
		records = l.longPhrases
	} else {
		return
	}
	for i := range records {
		rec := &records[i]
		if len(rec.syls) == n && matchKey(rec.syls, segs) {
			for _, w := range rec.words {
				if !emit(w) {
					return
				}
			}
		}
	}
}

func (l *lexicon) LookupSingle(seg string, emit func(word string) bool) {
	if seg == "" {
		return
	}
	cur := uint16(0)
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if c < 'a' || c > 'z' {
			return
		}
		cur = l.trie[cur].next[c-'a']
		if cur == 0 {
			return
		}
	}
	for _, ch := range l.trie[cur].chars {
		if !emit(ch) {
			return
		}
	}
}

var (
	lexOnce sync.Once
	lex     *lexicon
)

func getLexicon() *lexicon {
	lexOnce.Do(func() { lex = buildLexicon() })
	return lex
}

func buildLexicon() *lexicon {
	l := &lexicon{
		bySyllable: map[string][]rune{},
		words:      map[string][]string{},
		byInitials: map[string][]string{},
		syllables:  map[string]bool{},
		prefixes:   map[string]bool{},
		trie:       []sylTrieNode{{}},
	}
	rank := map[rune]int{}
	starter := loadStarter()
	for _, r := range starter.Hanzi {
		if _, seen := rank[r]; !seen && engine.IsHanzi(r) {
			rank[r] = len(rank)
		}
	}
	l.rank = rank
	for r := rune(0x4E00); r <= 0x9FA5; r++ {
		for _, s := range engine.HanziSyllables(r) {
			if s == "" {
				continue
			}
			l.bySyllable[s] = append(l.bySyllable[s], r)
		}
	}
	allSyls := make([]string, 0, len(l.bySyllable))
	for s, chars := range l.bySyllable {
		sort.SliceStable(chars, func(i, j int) bool {
			ri, oki := rank[chars[i]]
			rj, okj := rank[chars[j]]
			switch {
			case oki && okj:
				return ri < rj
			case oki != okj:
				return oki
			}
			return false // keep code-point order for rare characters
		})
		l.syllables[s] = true
		l.maxSylLen = max(l.maxSylLen, len(s))
		for i := 1; i <= len(s); i++ {
			l.prefixes[s[:i]] = true
		}
		allSyls = append(allSyls, s)
	}
	sort.Strings(allSyls)

	// Populate the flat ASCII trie for fast segmentation and single-char lookups.
	for _, s := range allSyls {
		cur := uint16(0)
		validASCII := true
		for i := 0; i < len(s); i++ {
			c := s[i]
			if c < 'a' || c > 'z' {
				validASCII = false
				break
			}
			nxt := l.trie[cur].next[c-'a']
			if nxt == 0 {
				nxt = uint16(len(l.trie))
				l.trie = append(l.trie, sylTrieNode{prefix: s[:i+1]})
				l.trie[cur].next[c-'a'] = nxt
			}
			cur = nxt
		}
		if validASCII && cur != 0 {
			l.trie[cur].isSyl = true
			runes := l.bySyllable[s]
			strs := make([]string, len(runes))
			for i, r := range runes {
				strs[i] = string(r)
			}
			l.trie[cur].chars = strs
		}
	}
	for id := 1; id < len(l.trie); id++ {
		if l.trie[id].isSyl {
			continue
		}
		p := l.trie[id].prefix
		var all []rune
		for _, s := range allSyls {
			if strings.HasPrefix(s, p) {
				all = append(all, l.bySyllable[s][:min(8, len(l.bySyllable[s]))]...)
			}
		}
		sort.SliceStable(all, func(i, j int) bool { return l.rankOf(all[i]) < l.rankOf(all[j]) })
		strs := make([]string, len(all))
		for i, r := range all {
			strs[i] = string(r)
		}
		l.trie[id].chars = strs
	}

	seen := map[string]bool{}
	for _, w := range starter.Words {
		if seen[w] {
			continue
		}
		seen[w] = true
		for _, key := range wordReadings(w) {
			l.words[key] = append(l.words[key], w)
			l.byInitials[initialsOf(key)] = append(l.byInitials[initialsOf(key)], w)
		}
	}
	keys := make([]string, 0, len(l.words))
	for k := range l.words {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		syls := strings.Fields(k)
		n := len(syls)
		if n < 2 || syls[0] == "" || syls[1] == "" {
			continue
		}
		rec := phraseRecord{syls: syls, words: l.words[k]}
		c0, c1 := syls[0][0], syls[1][0]
		if n < maxPhraseBucketSyls && c0 >= 'a' && c0 <= 'z' && c1 >= 'a' && c1 <= 'z' {
			l.phraseBuckets[n][c0-'a'][c1-'a'] = append(l.phraseBuckets[n][c0-'a'][c1-'a'], rec)
		} else {
			l.longPhrases = append(l.longPhrases, rec)
		}
	}
	return l
}

// wordReadings returns the space-joined readings of w (first reading per char, plus the
// second for polyphones such as 觉/得/行 so "juede" and "jiaode" both hit).
// ReadingsFor exposes the syllable readings of a word to callers outside this package (the
// settings UI shows them so a polyphone is visible before it surprises anyone, and storage needs
// the same list to pin every reading). Returns nil when a character has no known reading, which
// also means the word cannot be pinned usefully.
func ReadingsFor(w string) []string {
	return wordReadings(w)
}

func wordReadings(w string) []string {
	var opts [][]string
	for _, r := range w {
		s := engine.HanziSyllables(r)
		if len(s) == 0 {
			return nil
		}
		opts = append(opts, s[:min(2, len(s))])
	}
	out := []string{""}
	for _, o := range opts {
		var next []string
		for _, prefix := range out {
			for _, s := range o {
				if prefix == "" {
					next = append(next, s)
				} else {
					next = append(next, prefix+" "+s)
				}
			}
		}
		if len(next) > 16 {
			next = next[:16]
		}
		out = next
	}
	return out
}

func initialsOf(key string) string {
	var b strings.Builder
	for _, s := range strings.Fields(key) {
		b.WriteString(syllableInitial(s))
	}
	return b.String()
}

// syllableInitial is the shengmu used for jianpin ("zh"/"ch"/"sh" collapse to one letter so
// "zg" matches 中国, as in common IMEs).
func syllableInitial(s string) string {
	if s == "" {
		return ""
	}
	return s[:1]
}
