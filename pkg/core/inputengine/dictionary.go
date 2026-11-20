package inputengine

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/yeisme/clavis/pkg/core/ime"
)

// Dictionary supplies phrase and single-syllable candidates for segmented pinyin.
// Implementations must be safe for concurrent read-only use and perform no I/O during lookup.
type Dictionary interface {
	// LookupPhrases emits multi-syllable words matching segs (len >= 2), where each typed
	// segment is an exact match or prefix of the corresponding word syllable. Iteration stops
	// early if emit returns false.
	LookupPhrases(segs []string, emit func(word string) bool)
	// LookupSingle emits single-syllable words/characters matching seg (exact syllable or
	// prefix). Iteration stops early if emit returns false.
	LookupSingle(seg string, emit func(word string) bool)
}

// UnihanDictionary returns the embedded Unihan character + built-in phrase dictionary.
func UnihanDictionary() Dictionary {
	return getLexicon()
}

var (
	starterDictOnce sync.Once
	starterDict     Dictionary
	defaultDictOnce sync.Once
	defaultDict     Dictionary
)

// StarterDictionary returns a Dictionary backed by ime.StarterLexicon().
func StarterDictionary() Dictionary {
	starterDictOnce.Do(func() {
		starterDict = newIMEDictionary(ime.StarterLexicon(), true)
	})
	return starterDict
}

// The common dictionary is a 45k-word Rime build compiled from rime-ice's base dictionary
// (Apache-2.0, see data/common.meta.json for the exact selection rule) and embedded as the
// already-compiled .clvdict form. It exists because the hand-written starter list is only
// 222 words: everyday two-syllable words like 当我 were simply absent, so the engine fell
// back to first-syllable characters and a typist saw 当/党/荡 with no way to get 我.
//
// Embedded compiled rather than parsed: .clvdict loads without rebuilding indexes, keeping
// startup flat. rime-ice carries easter-egg weights (19260817 and similar) on joke entries,
// so the export keeps only 20000 <= weight < 600000 and applies data/blocklist.txt.
//
//go:embed data/common.clvdict
var commonClvDict []byte

const commonClvDigest = "sha256:86d7962f4bd037be31795f5390fec21a503db470361fba2c3105c28458a7f60c"

var (
	commonDictOnce sync.Once
	commonDict     Dictionary
)

// commonDictionary returns the embedded common-word layer, or nil when the artifact is
// unusable. A digest mismatch is a build-integrity failure: panic rather than silently ship
// a wrong dictionary.
func commonDictionary() Dictionary {
	commonDictOnce.Do(func() {
		sum := sha256.Sum256(commonClvDict)
		if got := "sha256:" + hex.EncodeToString(sum[:]); got != commonClvDigest {
			panic("inputengine: embedded common dictionary digest mismatch: " + got)
		}
		l, err := ime.LoadClvDict(commonClvDict)
		if err != nil || l == nil || l.Len() == 0 {
			return
		}
		commonDict = newIMEDictionary(l, false)
	})
	return commonDict
}

func defaultDictionary() Dictionary {
	defaultDictOnce.Do(func() {
		if sl := ime.StarterLexicon(); sl != nil && sl.Len() > 0 {
			// Order matters: the curated starter list first (it holds the highest-frequency
			// pairs), then the broad common layer, then Unihan single characters as fallback.
			if cd := commonDictionary(); cd != nil {
				defaultDict = ChainDictionaries(StarterDictionary(), cd, UnihanDictionary())
			} else {
				defaultDict = ChainDictionaries(StarterDictionary(), UnihanDictionary())
			}
		} else {
			defaultDict = UnihanDictionary()
		}
	})
	return defaultDict
}

// ChainDictionaries combines dictionaries in priority order; earlier dictionaries emit first.
func ChainDictionaries(dicts ...Dictionary) Dictionary {
	filtered := make([]Dictionary, 0, len(dicts))
	for _, d := range dicts {
		if d != nil {
			filtered = append(filtered, d)
		}
	}
	if len(filtered) == 0 {
		return UnihanDictionary()
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return chainedDictionary(filtered)
}

type chainedDictionary []Dictionary

func (c chainedDictionary) LookupPhrases(segs []string, emit func(word string) bool) {
	stopped := false
	wrap := func(w string) bool {
		if !emit(w) {
			stopped = true
			return false
		}
		return true
	}
	for _, d := range c {
		d.LookupPhrases(segs, wrap)
		if stopped {
			return
		}
	}
}

func (c chainedDictionary) LookupSingle(seg string, emit func(word string) bool) {
	stopped := false
	wrap := func(w string) bool {
		if !emit(w) {
			stopped = true
			return false
		}
		return true
	}
	for _, d := range c {
		d.LookupSingle(seg, wrap)
		if stopped {
			return
		}
	}
}

// LoadDictionary resolves the active Dictionary with priority:
//  1. customPaths (compiled/cached .clvdict via ime.LoadOrCompileDicts)
//  2. built-in ime.StarterLexicon()
//  3. embedded Unihan lexicon (final fallback)
//
// Missing or invalid customPaths degrade gracefully to the built-in dictionaries.
func LoadDictionary(customPaths []string, cacheDir string) Dictionary {
	var clean []string
	for _, p := range customPaths {
		if p = strings.TrimSpace(p); p != "" {
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		return defaultDictionary()
	}
	resolvedCache := strings.TrimSpace(cacheDir)
	if resolvedCache == "" {
		resolvedCache = defaultDictCacheDir()
	}
	customLex, err := ime.LoadOrCompileDicts(clean, resolvedCache)
	if err != nil || customLex == nil || customLex.Len() == 0 {
		return defaultDictionary()
	}
	return ChainDictionaries(NewIMEDictionary(customLex), defaultDictionary())
}

func defaultDictCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "clavis", "dicts")
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "clavis", "dicts")
	}
	return ""
}

type imePhraseItem struct {
	syls []string
	word string
	freq int32
}

type imeSingleItem struct {
	syl  string
	word string
	freq int32
}

type imeDictionary struct {
	phraseBuckets [maxPhraseBucketSyls][26][26][]imePhraseItem
	longPhrases   []imePhraseItem
	singleExact   map[string][]imeSingleItem
	singleByFirst [26][]imeSingleItem
}

// NewIMEDictionary adapts an *ime.Lexicon (from .clvdict bytes or compiled Rime YAML) to Dictionary.
func NewIMEDictionary(lex *ime.Lexicon) Dictionary {
	return newIMEDictionary(lex, false)
}

func newIMEDictionary(lex *ime.Lexicon, starterFilter bool) Dictionary {
	d := &imeDictionary{
		singleExact: make(map[string][]imeSingleItem),
	}
	if lex == nil || lex.Len() == 0 {
		return d
	}
	lex.ForEachWord(func(pinyin, word string, freq int32) bool {
		if word == "" || pinyin == "" || !isASCIIPinyin(pinyin) {
			return true
		}
		if starterFilter && !hasHanRune(word) {
			return true
		}
		syls := splitIMEPinyin(pinyin)
		if len(syls) == 0 {
			return true
		}
		if starterFilter && len(syls) < 2 {
			return true
		}
		for _, variant := range expandUSyllableVariants(syls) {
			d.insert(variant, word, freq)
		}
		return true
	})
	for n := 2; n < maxPhraseBucketSyls; n++ {
		for c0 := 0; c0 < 26; c0++ {
			for c1 := 0; c1 < 26; c1++ {
				items := d.phraseBuckets[n][c0][c1]
				if len(items) > 1 {
					sort.SliceStable(items, func(i, j int) bool { return items[i].freq > items[j].freq })
				}
			}
		}
	}
	if len(d.longPhrases) > 1 {
		sort.SliceStable(d.longPhrases, func(i, j int) bool { return d.longPhrases[i].freq > d.longPhrases[j].freq })
	}
	for k, items := range d.singleExact {
		if len(items) > 1 {
			sort.SliceStable(items, func(i, j int) bool { return items[i].freq > items[j].freq })
			d.singleExact[k] = items
		}
	}
	for c0 := 0; c0 < 26; c0++ {
		items := d.singleByFirst[c0]
		if len(items) > 1 {
			sort.SliceStable(items, func(i, j int) bool { return items[i].freq > items[j].freq })
		}
	}
	return d
}

func (d *imeDictionary) insert(syls []string, word string, freq int32) {
	n := len(syls)
	if n == 1 {
		s := syls[0]
		if s == "" {
			return
		}
		it := imeSingleItem{syl: s, word: word, freq: freq}
		d.singleExact[s] = append(d.singleExact[s], it)
		if s[0] >= 'a' && s[0] <= 'z' {
			d.singleByFirst[s[0]-'a'] = append(d.singleByFirst[s[0]-'a'], it)
		}
		return
	}
	if syls[0] == "" || syls[1] == "" {
		return
	}
	it := imePhraseItem{syls: syls, word: word, freq: freq}
	c0, c1 := syls[0][0], syls[1][0]
	if n < maxPhraseBucketSyls && c0 >= 'a' && c0 <= 'z' && c1 >= 'a' && c1 <= 'z' {
		d.phraseBuckets[n][c0-'a'][c1-'a'] = append(d.phraseBuckets[n][c0-'a'][c1-'a'], it)
	} else {
		d.longPhrases = append(d.longPhrases, it)
	}
}

func (d *imeDictionary) LookupPhrases(segs []string, emit func(word string) bool) {
	n := len(segs)
	if n < 2 || segs[0] == "" || segs[1] == "" {
		return
	}
	var items []imePhraseItem
	c0, c1 := segs[0][0], segs[1][0]
	if n < maxPhraseBucketSyls && c0 >= 'a' && c0 <= 'z' && c1 >= 'a' && c1 <= 'z' {
		items = d.phraseBuckets[n][c0-'a'][c1-'a']
	} else if n >= maxPhraseBucketSyls {
		items = d.longPhrases
	} else {
		return
	}
	if len(items) == 0 {
		return
	}
	// Pass 1: exact full-syllable matches in frequency order.
	for i := range items {
		it := &items[i]
		if len(it.syls) == n && exactKey(it.syls, segs) {
			if !emit(it.word) {
				return
			}
		}
	}
	// Pass 2: prefix / jianpin matches in frequency order.
	for i := range items {
		it := &items[i]
		if len(it.syls) == n && !exactKey(it.syls, segs) && matchKey(it.syls, segs) {
			if !emit(it.word) {
				return
			}
		}
	}
}

func (d *imeDictionary) LookupSingle(seg string, emit func(word string) bool) {
	if seg == "" {
		return
	}
	for _, it := range d.singleExact[seg] {
		if !emit(it.word) {
			return
		}
	}
	c0 := seg[0]
	if c0 < 'a' || c0 > 'z' {
		return
	}
	for _, it := range d.singleByFirst[c0-'a'] {
		if it.syl != seg && strings.HasPrefix(it.syl, seg) {
			if !emit(it.word) {
				return
			}
		}
	}
}

func exactKey(key, segs []string) bool {
	if len(key) != len(segs) {
		return false
	}
	for i := range key {
		if key[i] != segs[i] {
			return false
		}
	}
	return true
}

func isASCIIPinyin(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= unicode.MaxASCII {
			return false
		}
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != ' ' && c != '\'' {
			return false
		}
	}
	return true
}

func hasHanRune(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func splitIMEPinyin(pinyin string) []string {
	fields := strings.Fields(strings.ToLower(strings.ReplaceAll(pinyin, "'", " ")))
	if len(fields) == 1 && len(fields[0]) > 1 {
		l := getLexicon()
		if !l.syllables[fields[0]] {
			return Segment(fields[0])
		}
	}
	return fields
}

func expandUSyllableVariants(syls []string) [][]string {
	hasV := false
	for _, s := range syls {
		if s == "nv" || s == "lv" || s == "nve" || s == "lve" {
			hasV = true
			break
		}
	}
	if !hasV {
		return [][]string{syls}
	}
	alt := make([]string, len(syls))
	for i, s := range syls {
		alt[i] = strings.ReplaceAll(s, "v", "u")
	}
	return [][]string{syls, alt}
}
