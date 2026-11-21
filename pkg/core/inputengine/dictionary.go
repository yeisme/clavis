package inputengine

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
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

// DictionarySource identifies the origin of a candidate. Smaller values have
// higher priority; callers should use the named constants rather than relying
// on their numeric values.
type DictionarySource uint8

const (
	SourceCustom DictionarySource = iota
	SourceLearned
	SourceStarter
	SourceBuiltin
)

// CandidateRecord is the canonical candidate before presentation formatting.
type CandidateRecord struct {
	Pinyin          string
	Word            string
	Source          DictionarySource
	Frequency       uint64
	Pinned          bool
	OriginalOrdinal uint32
}

// ResolveCandidate applies source priority, then the deterministic same-source
// tie-breaks from the dictionary contract. It emits one record per (Pinyin, Word)
// pair and performs no I/O.
func ResolveCandidate(records []CandidateRecord) []CandidateRecord {
	winners := make(map[string]CandidateRecord, len(records))
	for _, record := range records {
		key := record.Pinyin + "\x00" + record.Word
		current, ok := winners[key]
		if !ok || betterCandidate(record, current) {
			winners[key] = record
		}
	}
	out := make([]CandidateRecord, 0, len(winners))
	for _, record := range winners {
		out = append(out, record)
	}
	sort.SliceStable(out, func(i, j int) bool { return betterCandidate(out[i], out[j]) })
	return out
}

func betterCandidate(a, b CandidateRecord) bool {
	if sourcePriority(a.Source) != sourcePriority(b.Source) {
		return sourcePriority(a.Source) < sourcePriority(b.Source)
	}
	if a.Pinned != b.Pinned {
		return a.Pinned
	}
	if a.Frequency != b.Frequency {
		return a.Frequency > b.Frequency
	}
	if a.OriginalOrdinal != b.OriginalOrdinal {
		return a.OriginalOrdinal < b.OriginalOrdinal
	}
	if a.Pinyin != b.Pinyin {
		return bytes.Compare([]byte(a.Pinyin), []byte(b.Pinyin)) < 0
	}
	return bytes.Compare([]byte(a.Word), []byte(b.Word)) < 0
}

func sourcePriority(source DictionarySource) int {
	switch source {
	case SourceCustom:
		return 0
	case SourceLearned:
		return 1
	case SourceStarter:
		return 2
	case SourceBuiltin:
		return 3
	default:
		return 4
	}
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

// DictionaryStatus describes the dictionary layer selected by the user resolver.
// Source is "user" when at least one user .clvdict loaded successfully, and
// "embedded" when the built-in chain is active. Diagnostics are informational and
// never make dictionary resolution fail.
type DictionaryStatus struct {
	Source      string
	Path        string
	Count       int
	Diagnostics []string
}

// DictionaryDiagnostic explains why one custom source was not used. It contains
// no dictionary contents, so callers can safely surface it in settings/logs.
type DictionaryDiagnostic struct {
	Path     string
	Kind     string
	Message  string
	Fallback string
}

// LoadUserDictionary resolves ~/.config/clavis/dicts/*.clvdict. Every file is
// loaded and indexed before returning, so Dictionary lookups never perform disk
// I/O. Files are sorted by name and earlier layers have higher priority.
func LoadUserDictionary() (Dictionary, DictionaryStatus) {
	dir := userDictionaryDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		// An absent directory is the normal first-run state, not a load failure.
		return defaultDictionary(), DictionaryStatus{Source: "embedded", Path: "embedded", Count: embeddedDictionaryCount()}
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".clvdict") {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return defaultDictionary(), DictionaryStatus{Source: "embedded", Path: "embedded", Count: embeddedDictionaryCount()}
	}
	layers := make([]Dictionary, 0, len(paths))
	loadedPaths := make([]string, 0, len(paths))
	status := DictionaryStatus{Source: "embedded", Path: "embedded", Count: 0}
	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("user dictionary %s: %v", path, readErr))
			continue
		}
		lex, loadErr := ime.LoadClvDict(data)
		if loadErr != nil || lex == nil || lex.Len() == 0 {
			if loadErr == nil {
				loadErr = fmt.Errorf("empty dictionary")
			}
			status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("user dictionary %s: %v", path, loadErr))
			continue
		}
		layers = append(layers, NewIMEDictionary(lex))
		loadedPaths = append(loadedPaths, path)
		status.Count += lex.Len()
	}
	if len(layers) == 0 {
		status.Count = embeddedDictionaryCount()
		status.Diagnostics = append(status.Diagnostics, "all user dictionaries failed; using embedded dictionaries")
		return defaultDictionary(), status
	}
	status.Source = "user"
	status.Path = strings.Join(loadedPaths, ", ")
	return ChainDictionaries(append(layers, defaultDictionary())...), status
}

// LoadDictionaryWithDiagnostics resolves every explicit custom source independently. A
// bad file is skipped as a whole and reported; another valid file still loads.
// The returned dictionary is fully indexed before this function returns, so its
// lookup path performs no disk I/O.
func LoadDictionaryWithDiagnostics(customPaths []string, cacheDir string, report func(DictionaryDiagnostic)) (Dictionary, error) {
	user, status := LoadUserDictionary()
	clean := cleanDictionaryPaths(customPaths)
	if len(clean) == 0 {
		return user, nil
	}
	resolvedCache := strings.TrimSpace(cacheDir)
	if resolvedCache == "" {
		resolvedCache = defaultDictCacheDir()
	}
	layers := make([]Dictionary, 0, len(clean))
	for _, path := range clean {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			diagnostic := DictionaryDiagnostic{Path: path, Kind: "unreadable", Message: readErr.Error(), Fallback: "starter+builtin"}
			recordDictionaryDiagnostic(&status, diagnostic, report)
			continue
		}
		if kind, valid := validateDictionaryData(data); !valid {
			message := "dictionary contains invalid or missing fields"
			diagnostic := DictionaryDiagnostic{Path: path, Kind: kind, Message: message, Fallback: "starter+builtin"}
			status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("%s: %s", path, message))
			if report != nil {
				report(diagnostic)
			}
			continue
		}
		lex, loadErr := ime.LoadOrCompileDicts([]string{path}, resolvedCache)
		if loadErr != nil || lex == nil || lex.Len() == 0 {
			kind := classifyDictionaryFailure(data, loadErr)
			message := "empty dictionary"
			if loadErr != nil {
				message = loadErr.Error()
			}
			diagnostic := DictionaryDiagnostic{Path: path, Kind: kind, Message: message, Fallback: "starter+builtin"}
			status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("%s: %s", path, message))
			if report != nil {
				report(diagnostic)
			}
			continue
		}
		layers = append(layers, NewIMEDictionary(lex))
	}
	if len(layers) == 0 {
		return user, nil
	}
	// Explicit custom layers outrank user, learned, starter, and builtin layers.
	return ChainDictionaries(append(layers, user)...), nil
}

func recordDictionaryDiagnostic(status *DictionaryStatus, diagnostic DictionaryDiagnostic, report func(DictionaryDiagnostic)) {
	status.Diagnostics = append(status.Diagnostics, fmt.Sprintf("%s: %s", diagnostic.Path, diagnostic.Message))
	if report != nil {
		report(diagnostic)
		return
	}
	// Keep a nil callback observable without persisting user dictionary contents.
	fmt.Fprintf(os.Stderr, "inputengine: dictionary diagnostic path=%q kind=%s message=%s\n", diagnostic.Path, diagnostic.Kind, diagnostic.Message)
}

func validateDictionaryData(data []byte) (string, bool) {
	if bytes.HasPrefix(data, []byte{'C', 'L', 'V', 'D'}) {
		return "", true
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return "empty", false
	}
	inHeader := false
	decidedHeader := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.Trim(raw, " \r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !decidedHeader {
			decidedHeader = true
			if line == "---" || (!strings.ContainsRune(line, '\t') && strings.ContainsRune(line, ':')) {
				inHeader = true
				continue
			}
		}
		if inHeader {
			if line == "..." {
				inHeader = false
			}
			continue
		}
		if line == "..." {
			continue
		}
		if !strings.ContainsRune(line, '\t') {
			return "malformed", false
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || strings.TrimSpace(fields[0]) == "" || strings.TrimSpace(fields[1]) == "" {
			return "missing_field", false
		}
	}
	return "", true
}

func classifyDictionaryFailure(data []byte, loadErr error) string {
	if len(bytes.TrimSpace(data)) == 0 {
		return "empty"
	}
	if bytes.HasPrefix(data, []byte{'C', 'L', 'V', 'D'}) {
		return "malformed"
	}
	inHeader := false
	decidedHeader := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !decidedHeader {
			decidedHeader = true
			if line == "---" || (!strings.ContainsRune(line, '\t') && strings.ContainsRune(line, ':')) {
				inHeader = true
				continue
			}
		}
		if inHeader {
			if line == "..." {
				inHeader = false
			}
			continue
		}
		if line == "..." {
			continue
		}
		if !strings.ContainsRune(line, '\t') {
			return "malformed"
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || strings.TrimSpace(fields[0]) == "" || strings.TrimSpace(fields[1]) == "" {
			return "missing_field"
		}
	}
	if loadErr != nil {
		return "compiler_error"
	}
	return "empty"
}

// LoadDictionary resolves the active Dictionary with priority:
//  1. customPaths (compiled/cached .clvdict via ime.LoadOrCompileDicts)
//  2. built-in ime.StarterLexicon()
//  3. embedded Unihan lexicon (final fallback)
//
// Missing or invalid customPaths degrade gracefully to the built-in dictionaries.
func LoadDictionary(customPaths []string, cacheDir string) Dictionary {
	dict, _ := LoadDictionaryWithDiagnostics(customPaths, cacheDir, nil)
	return dict
}

func cleanDictionaryPaths(paths []string) []string {
	clean := make([]string, 0, len(paths))
	for _, p := range paths {
		if p = strings.TrimSpace(p); p != "" {
			clean = append(clean, p)
		}
	}
	return clean
}

func userDictionaryDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".config", "clavis", "dicts")
	}
	return filepath.Join(home, ".config", "clavis", "dicts")
}

func embeddedDictionaryCount() int {
	count := 0
	if lex := ime.StarterLexicon(); lex != nil {
		count += lex.Len()
	}
	if len(commonClvDict) > 0 {
		if lex, err := ime.LoadClvDict(commonClvDict); err == nil && lex != nil {
			count += lex.Len()
		}
	}
	return count
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
