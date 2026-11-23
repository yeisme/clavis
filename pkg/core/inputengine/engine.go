// Package inputengine is Clavis' own pinyin composition engine (docs/design/own-input-engine.md).
// It is pure logic: the platform layer feeds normalized keys and performs commits/rendering.
package inputengine

import (
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/yeisme/clavis/pkg/core/ime"
)

// KeyKind classifies a key for the composition state machine (§4).
type KeyKind int

// NavDirection is the optional direction carried by a KeyNavigation event.
// A zero direction preserves the legacy navigation behavior (reset and pass).
type NavDirection uint8

const (
	NavNone NavDirection = iota
	NavUp
	NavDown
)

const (
	KeyOther      KeyKind = iota // printable char not otherwise listed (Rune set)
	KeyLetter                    // a-z (Rune lower-case); upper-case comes as KeyOther
	KeyDigit                     // 0-9 (Rune)
	KeySpace                     //
	KeyEnter                     //
	KeyBackspace                 //
	KeyEscape                    //
	KeyApostrophe                // ' syllable separator
	KeyPagePrev                  // '-'
	KeyPageNext                  // '='
	KeyNavigation                // arrows, Tab, Home/End, Delete, F-keys...: reset + pass
	KeyShiftDown
	KeyShiftUp
)

// Candidate page-size bounds matching config.InputEngineConfig validation.
const (
	MinCandidateCount     = 3
	MaxCandidateCount     = 9
	DefaultCandidateCount = 7
)

// Key is one key-down (or Shift up/down) event.
type Key struct {
	Kind KeyKind
	Rune rune
	// NavDir optionally requests candidate selection movement for KeyNavigation.
	NavDir NavDirection
	// Chord is true when Ctrl/Alt/Win is held: the engine resets and lets the shortcut through.
	Chord bool
}

// Result tells the platform layer what to do with the key.
type Result struct {
	Consume bool   // swallow the physical key
	Commit  string // text to insert (sent before any later key)
	Changed bool   // view changed → re-render candidate window
}

// Candidate is one selectable item.
type Candidate struct {
	Text string
	// Consumed is how many leading syllables picking this candidate commits. The UI reads
	// it to tell a whole-phrase answer from a single character.
	Consumed int
	AI       bool // from the async AI source (shown with a marker)
}

// View is a render snapshot.
type View struct {
	Composing  bool
	ASCII      bool
	Preedit    string // segmented pinyin, e.g. "ni'hao"
	Candidates []Candidate
	Selected   int // index within Candidates
	Page       int
	// PageCount is the number of candidate pages for this composition. The candidate window
	// shows one page at a time, so without it the UI cannot tell a typist that eight more
	// pages exist ("也数根" used to sit on page 2 of 9 with nothing on screen saying so).
	PageCount int
	// SelectedConsumed is how many leading syllables the highlighted candidate commits.
	// The pinyin line marks those syllables so a typist can see whether Enter takes one
	// syllable or the whole sentence before pressing it.
	SelectedConsumed int
	HasPrev          bool
	HasNext          bool
}

// UserCandidate is a learned user word surfaced by a UserFrequencyProvider.
type UserCandidate = ime.UserCandidate

// UserFrequencyProvider supplies RAM-resident unigram/bigram boosts and learned words.
// Implementations must never block or perform I/O on LookupBoost or CustomWordsForPrefix.
type UserFrequencyProvider interface {
	LookupBoost(pinyinKey, word, prevWord string) int32
	CustomWordsForPrefix(syllablePrefix string) []UserCandidate
	// BigramWordsForPrefix returns RAM-mirror continuations after prevWord whose
	// reading can start with syllablePrefix. Implementations must never perform
	// I/O on the typing hot path.
	BigramWordsForPrefix(prevWord, syllablePrefix string) []UserCandidate
}

// Options configure the engine.
type Options struct {
	CandidateCount   int                   // page size; <=0 defaults to 7, clamped to 3..9
	ShiftToggleASCII bool                  // tap Shift alone to toggle ASCII pass-through
	Scheme           string                // ""|"quanpin"|"xiaohe"|"ziranma"|"microsoft"|"sogou"|"pinyin_plus"
	FuzzyPinyin      []string              // optional fuzzy rules, e.g. ["z=zh", "an=ang"]
	CustomDictPaths  []string              // optional Rime .dict.yaml or .clvdict paths
	DictCacheDir     string                // optional cache dir for compiled .clvdict
	Dictionary       Dictionary            // optional explicit dictionary override
	UserFrequency    UserFrequencyProvider // optional RAM-only unigram/bigram booster (nil-safe)
	BlockedWords     []string              // local user blocklist, removed before candidates are surfaced
}

// seenEntry tracks a deduplicated candidate text tagged with the active rebuild generation.
type seenEntry struct {
	text string
	gen  uint32
}

// Engine holds composition state. All methods are safe for concurrent use (hook thread +
// UI/AI goroutines) and never block on I/O.
type Engine struct {
	mu           sync.Mutex
	opts         Options
	dict         Dictionary
	dicts        []Dictionary
	shuangpin    bool
	raw          string // letters and ' as typed
	segs         []string
	rawSegs      []string
	cands        []Candidate
	ai           []string
	prevCommit   string
	page, sel    int
	ascii        bool
	shiftAlone   bool
	seenSlots    [128]seenEntry
	seenGen      uint32
	emitConsumed int
	emitCap      int
	emitCount    int
	emitLocalFn  func(string) bool
	segAlts      [][]string
	comboBuf     []string
	blocked      map[string]struct{}
}

const (
	maxRawLen     = 64
	maxCandidates = 60
	maxAltCombos  = 24
	// maxComposeSyls bounds the phrase length tried at each position of a sentence
	// candidate. Longer phrases starting at the first syllable still come from the
	// prefix-phrase tier.
	maxComposeSyls = 8
)

func clampCandidateCount(n int) int {
	if n == 0 {
		return DefaultCandidateCount
	}
	return min(max(n, MinCandidateCount), MaxCandidateCount)
}

func normalizeScheme(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "quanpin":
		return "quanpin"
	case "xiaohe", "ziranma", "microsoft", "sogou", "pinyin_plus":
		return s
	default:
		return "quanpin"
	}
}

func New(opts Options) *Engine {
	opts.CandidateCount = clampCandidateCount(opts.CandidateCount)
	opts.Scheme = normalizeScheme(opts.Scheme)
	opts.FuzzyPinyin = cleanFuzzyRules(opts.FuzzyPinyin)
	opts.BlockedWords = cleanBlockedWords(opts.BlockedWords)
	getLexicon() // build tables up front, not on the first keystroke inside the hook
	dict := opts.Dictionary
	if dict == nil {
		dict = LoadDictionary(opts.CustomDictPaths, opts.DictCacheDir)
	}
	if opts.Scheme != "quanpin" {
		_ = ime.ShuangpinSyllables(opts.Scheme, "ni") // warm Shuangpin lookup tables up front
	}
	var dicts []Dictionary
	if cd, ok := dict.(chainedDictionary); ok {
		dicts = []Dictionary(cd)
	} else {
		dicts = []Dictionary{dict}
	}
	e := &Engine{
		opts:      opts,
		dict:      dict,
		dicts:     dicts,
		shuangpin: opts.Scheme != "quanpin",
		segs:      make([]string, 0, 16),
		rawSegs:   make([]string, 0, 16),
		cands:     make([]Candidate, 0, maxCandidates),
		blocked:   blockedWordSet(opts.BlockedWords),
	}
	e.emitLocalFn = e.emitLocalCandidate
	return e
}

func cleanFuzzyRules(rules []string) []string {
	if len(rules) == 0 {
		return nil
	}
	var out []string
	for _, r := range rules {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

func cleanBlockedWords(words []string) []string {
	seen := make(map[string]struct{}, len(words))
	out := make([]string, 0, len(words))
	for _, word := range words {
		word = strings.TrimSpace(word)
		if word == "" {
			continue
		}
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = struct{}{}
		out = append(out, word)
	}
	return out
}

func blockedWordSet(words []string) map[string]struct{} {
	set := make(map[string]struct{}, len(words))
	for _, word := range words {
		set[word] = struct{}{}
	}
	return set
}

// SetBlockedWords replaces the local user blocklist and rebuilds the current view.
func (e *Engine) SetBlockedWords(words []string) {
	e.mu.Lock()
	e.blocked = blockedWordSet(cleanBlockedWords(words))
	if len(e.segs) > 0 {
		e.cands = e.cands[:0]
		e.page, e.sel = 0, 0
		e.buildCandidates()
	}
	e.mu.Unlock()
}

// CandidateCount returns the active clamped page size (3..9).
func (e *Engine) CandidateCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.opts.CandidateCount
}

// Raw returns the current pinyin input ("" when idle).
func (e *Engine) Raw() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.raw
}

// Reset drops any composition (mouse click, focus change, chord). Reports whether it was composing.
func (e *Engine) Reset() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	was := e.raw != ""
	e.clear()
	e.prevCommit = ""
	return was
}

// SetASCII forces ASCII (pass-through) mode.
func (e *Engine) SetASCII(v bool) {
	e.mu.Lock()
	e.ascii = v
	e.mu.Unlock()
}

// ASCII reports whether the engine is in English pass-through mode.
func (e *Engine) ASCII() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ascii
}

// SetAICandidates appends AI words for raw; stale results (raw changed) are dropped.
func (e *Engine) SetAICandidates(raw string, words []string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if raw == "" || raw != e.raw {
		return false
	}
	// Own the asynchronous result. Providers commonly reuse or append to their
	// response slice after delivering it, and retaining the caller's backing
	// array would race with candidate rebuilds under e.mu.
	e.ai = slices.Clone(words)
	e.rebuild(false)
	return true
}

func (e *Engine) clear() {
	e.raw = ""
	e.segs = e.segs[:0]
	e.rawSegs = e.rawSegs[:0]
	e.cands = e.cands[:0]
	e.ai = nil
	e.page, e.sel = 0, 0
}

// HandleKey runs the state machine (§4 table).
func (e *Engine) HandleKey(k Key) Result {
	e.mu.Lock()
	defer e.mu.Unlock()

	switch k.Kind {
	case KeyShiftDown:
		e.shiftAlone = true
		return Result{}
	case KeyShiftUp:
		alone := e.shiftAlone
		e.shiftAlone = false
		if !alone || !e.opts.ShiftToggleASCII {
			return Result{}
		}
		e.ascii = !e.ascii
		// Toggling mid-composition commits the typed letters as-is (common IME behaviour).
		commit := e.rawLetters()
		e.clear()
		e.prevCommit = ""
		return Result{Commit: commit, Changed: true}
	}
	e.shiftAlone = false

	composing := e.raw != ""
	if k.Kind == KeyNavigation && k.NavDir != NavNone && composing && !k.Chord {
		return e.moveSelection(k.NavDir)
	}
	if k.Chord || k.Kind == KeyNavigation {
		if composing {
			e.clear()
			e.prevCommit = ""
			return Result{Changed: true}
		}
		return Result{}
	}
	if !composing {
		if k.Kind == KeyLetter && !e.ascii {
			e.raw = asciiLetterString(k.Rune)
			e.rebuild(true)
			return Result{Consume: true, Changed: true}
		}
		return Result{}
	}

	switch k.Kind {
	case KeyLetter:
		if len(e.raw) < maxRawLen {
			e.raw += asciiLetterString(k.Rune)
			e.ai = nil
			e.rebuild(true)
		}
		return Result{Consume: true, Changed: true}
	case KeyApostrophe:
		if !strings.HasSuffix(e.raw, "'") && len(e.raw) < maxRawLen {
			e.raw += "'"
			e.rebuild(true)
		}
		return Result{Consume: true, Changed: true}
	case KeyBackspace:
		e.raw = e.raw[:len(e.raw)-1]
		e.ai = nil
		if strings.Trim(e.raw, "'") == "" {
			e.clear()
		} else {
			e.rebuild(true)
		}
		return Result{Consume: true, Changed: true}
	case KeyEscape:
		e.clear()
		e.prevCommit = ""
		return Result{Consume: true, Changed: true}
	case KeyEnter:
		commit := e.rawLetters()
		e.clear()
		e.prevCommit = ""
		return Result{Consume: true, Commit: commit, Changed: true}
	case KeySpace:
		return e.selectIndex(e.sel)
	case KeyDigit:
		n := int(k.Rune - '0')
		if n < 1 || n > e.opts.CandidateCount {
			return Result{}
		}
		return e.selectIndex(n - 1)
	case KeyPagePrev:
		if e.page > 0 {
			e.page--
			e.sel = 0
		}
		return Result{Consume: true, Changed: true}
	case KeyPageNext:
		if (e.page+1)*e.opts.CandidateCount < len(e.cands) {
			e.page++
			e.sel = 0
		}
		return Result{Consume: true, Changed: true}
	default:
		// Other printable (punctuation, upper-case): commit the top dictionary candidate then the char.
		// AI candidates must never be auto-committed without explicit selection.
		var autoCand *Candidate
		for i := range e.cands {
			if e.cands[i].AI {
				continue
			}
			if autoCand == nil && e.cands[i].Consumed >= len(e.segs) {
				autoCand = &e.cands[i]
			}
		}
		top := e.rawLetters()
		committedWord := ""
		if autoCand != nil {
			top = autoCand.Text
			committedWord = top
		}
		e.clear()
		e.prevCommit = committedWord
		if k.Rune != 0 {
			top += string(k.Rune)
		}
		return Result{Consume: true, Commit: top, Changed: true}
	}
}

// moveSelection changes the highlight within the current candidate page. Directional
// navigation deliberately never changes the page or composition; clamping still reports a
// change so the platform can consume the requested navigation event consistently.
func (e *Engine) moveSelection(dir NavDirection) Result {
	start := e.page * e.opts.CandidateCount
	end := min(start+e.opts.CandidateCount, len(e.cands))
	pageLen := end - start
	if pageLen > 0 {
		switch dir {
		case NavUp:
			if e.sel > 0 {
				e.sel--
			}
		case NavDown:
			if e.sel+1 < pageLen {
				e.sel++
			}
		}
	}
	return Result{Consume: true, Changed: true}
}

// selectIndex commits the idx-th candidate on the current page. A candidate covering only a
// prefix of the syllables commits immediately and leaves the rest composing.
func (e *Engine) selectIndex(idx int) Result {
	abs := e.page*e.opts.CandidateCount + idx
	if abs < 0 || abs >= len(e.cands) {
		if len(e.cands) == 0 {
			commit := e.rawLetters()
			e.clear()
			e.prevCommit = ""
			return Result{Consume: true, Commit: commit, Changed: true}
		}
		return Result{Consume: true}
	}
	c := e.cands[abs]
	e.prevCommit = c.Text
	if c.Consumed >= len(e.segs) {
		e.clear()
		return Result{Consume: true, Commit: c.Text, Changed: true}
	}
	rem := e.segs[c.Consumed:]
	if e.shuangpin && c.Consumed < len(e.rawSegs) {
		rem = e.rawSegs[c.Consumed:]
	}
	e.raw = strings.Join(rem, "'")
	e.ai = nil
	e.rebuild(true)
	return Result{Consume: true, Commit: c.Text, Changed: true}
}

func (e *Engine) rawLetters() string {
	if !strings.ContainsRune(e.raw, '\'') {
		return e.raw
	}
	return strings.ReplaceAll(e.raw, "'", "")
}

// View returns the render snapshot for the current page.
func (e *Engine) View() View {
	e.mu.Lock()
	defer e.mu.Unlock()
	v := View{Composing: e.raw != "", ASCII: e.ascii, Preedit: strings.Join(e.segs, "'"), Page: e.page, Selected: e.sel}
	start := e.page * e.opts.CandidateCount
	end := min(start+e.opts.CandidateCount, len(e.cands))
	if start < end {
		v.Candidates = append([]Candidate(nil), e.cands[start:end]...)
	}
	v.HasPrev = e.page > 0
	v.HasNext = end < len(e.cands)
	if abs := e.page*e.opts.CandidateCount + e.sel; abs >= 0 && abs < len(e.cands) {
		v.SelectedConsumed = e.cands[abs].Consumed
	}
	if n := len(e.cands); n > 0 {
		v.PageCount = (n + e.opts.CandidateCount - 1) / e.opts.CandidateCount
	}
	return v
}

func (e *Engine) rebuild(resetPage bool) {
	if e.shuangpin {
		e.segs, e.rawSegs = segmentShuangpinInto(e.segs[:0], e.rawSegs[:0], e.opts.Scheme, e.raw, e.opts.FuzzyPinyin)
	} else {
		e.segs = segmentInto(e.segs[:0], getLexicon(), e.raw)
		e.rawSegs = append(e.rawSegs[:0], e.segs...)
	}
	e.cands = e.cands[:0]
	e.buildCandidates()
	if resetPage {
		e.page, e.sel = 0, 0
	}
	if e.page*e.opts.CandidateCount >= len(e.cands) {
		e.page, e.sel = 0, 0
	}
}

// Segment splits raw pinyin into syllables: minimum-cost DP where a full syllable costs 1, a
// trailing syllable prefix 2 and a lone initial 3; "'" forces a boundary.
func Segment(raw string) []string {
	if raw == "" {
		return nil
	}
	return segmentInto(make([]string, 0, min(8, len(raw))), getLexicon(), raw)
}

func segmentInto(dst []string, l *lexicon, raw string) []string {
	start := 0
	n := len(raw)
	for i := 0; i <= n; i++ {
		if i < n && raw[i] != '\'' {
			continue
		}
		part := raw[start:i]
		start = i + 1
		if part == "" {
			continue
		}
		last := i == n
		dst = segmentChunkInto(dst, l, part, last)
	}
	return dst
}

func segmentChunkInto(dst []string, l *lexicon, s string, allowTrailingPrefix bool) []string {
	n := len(s)
	if n == 0 {
		return dst
	}
	const inf = 1 << 30
	var costStack [maxRawLen + 1]int
	var prevStack [maxRawLen + 1]int
	var cutsStack [maxRawLen + 1]int
	var cost, prev, cuts []int
	if n <= maxRawLen {
		cost = costStack[:n+1]
		prev = prevStack[:n+1]
		cuts = cutsStack[:0]
	} else {
		cost = make([]int, n+1)
		prev = make([]int, n+1)
		cuts = make([]int, 0, n)
	}
	for i := 1; i <= n; i++ {
		cost[i] = inf
	}
	for i := 0; i < n; i++ {
		if cost[i] == inf {
			continue
		}
		cur := uint16(0)
		limit := min(n, i+l.maxSylLen)
		for j := i + 1; j <= limit; j++ {
			ch := s[j-1]
			if ch >= 'a' && ch <= 'z' && (j == i+1 || cur != 0) {
				cur = l.trie[cur].next[ch-'a']
			} else {
				cur = 0
			}
			c := inf
			switch {
			case cur != 0 && l.trie[cur].isSyl:
				c = 1
			case j == n && allowTrailingPrefix && cur != 0:
				c = 2
			case j == i+1:
				c = 3
			}
			if c < inf && cost[i]+c < cost[j] {
				cost[j], prev[j] = cost[i]+c, i
			}
			if cur == 0 {
				break
			}
		}
	}
	for j := n; j > 0; j = prev[j] {
		cuts = append(cuts, j)
	}
	for k := len(cuts) - 1; k >= 0; k-- {
		end := cuts[k]
		dst = append(dst, s[prev[end]:end])
	}
	return dst
}

func segmentShuangpinInto(segsDst, rawDst []string, scheme, raw string, _ []string) ([]string, []string) {
	// Delegate decoding to ime.ShuangpinSyllables / ime.SplitShuangpin while tracking raw pairs
	// 1-to-1 for partial commit preservation.
	start := 0
	n := len(raw)
	for i := 0; i <= n; i++ {
		if i < n && raw[i] != '\'' {
			continue
		}
		chunk := raw[start:i]
		start = i + 1
		for len(chunk) >= 2 {
			pair := chunk[:2]
			chunk = chunk[2:]
			rawDst = append(rawDst, pair)
			if syls := ime.ShuangpinSyllables(scheme, pair); len(syls) > 0 {
				segsDst = append(segsDst, syls[0])
			} else {
				segsDst = append(segsDst, pair)
			}
		}
		if len(chunk) == 1 {
			rawDst = append(rawDst, chunk)
			if syls := ime.ShuangpinSyllables(scheme, chunk); len(syls) > 0 {
				segsDst = append(segsDst, syls[0])
			} else {
				segsDst = append(segsDst, chunk)
			}
		}
	}
	return segsDst, rawDst
}

// matchKey reports whether segs[:len(key)] matches a word key: each typed segment is a prefix
// of the corresponding syllable (so full pinyin, jianpin and mixes all match).
func matchKey(key []string, segs []string) bool {
	if len(key) > len(segs) {
		return false
	}
	for i, syl := range key {
		if !strings.HasPrefix(syl, segs[i]) {
			return false
		}
	}
	return true
}

var asciiLetters = [26]string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z"}

func asciiLetterString(r rune) string {
	if r >= 'a' && r <= 'z' {
		return asciiLetters[r-'a']
	}
	return string(r)
}

func (e *Engine) resetSeen() {
	e.seenGen++
	if e.seenGen == 0 {
		clear(e.seenSlots[:])
		e.seenGen = 1
	}
}

func (e *Engine) markSeen(w string) bool {
	var h uint32 = 2166136261
	for i := 0; i < len(w); i++ {
		h ^= uint32(w[i])
		h *= 16777619
	}
	idx := h & 127
	gen := e.seenGen
	for {
		cur := &e.seenSlots[idx]
		if cur.gen != gen {
			cur.text = w
			cur.gen = gen
			return true
		}
		if cur.text == w {
			return false
		}
		idx = (idx + 1) & 127
	}
}

func (e *Engine) addCandidate(text string, consumed int, isAI bool) bool {
	if text == "" {
		return true
	}
	if _, blocked := e.blocked[text]; blocked {
		return true
	}
	if len(e.cands) >= maxCandidates {
		return false
	}
	if !e.markSeen(text) {
		return true
	}
	e.cands = append(e.cands, Candidate{Text: text, Consumed: consumed, AI: isAI})
	return len(e.cands) < maxCandidates
}

func (e *Engine) emitLocalCandidate(w string) bool {
	if e.emitCap > 0 && e.emitCount >= e.emitCap {
		return false
	}
	if !e.addCandidate(w, e.emitConsumed, false) {
		return false
	}
	if e.emitCap > 0 {
		e.emitCount++
		return e.emitCount < e.emitCap
	}
	return true
}

func (e *Engine) lookupPhrases(segs []string) {
	for _, d := range e.dicts {
		if len(e.cands) >= maxCandidates {
			return
		}
		d.LookupPhrases(segs, e.emitLocalFn)
	}
}

func (e *Engine) lookupSingle(seg string) {
	for _, d := range e.dicts {
		if len(e.cands) >= maxCandidates || (e.emitCap > 0 && e.emitCount >= e.emitCap) {
			return
		}
		d.LookupSingle(seg, e.emitLocalFn)
	}
}

func (e *Engine) buildCandidates() {
	segs := e.segs
	if len(segs) == 0 {
		return
	}
	if len(e.dicts) == 0 {
		if e.dict != nil {
			e.dicts = []Dictionary{e.dict}
		} else {
			e.dicts = []Dictionary{defaultDictionary()}
		}
	}
	if e.emitLocalFn == nil {
		e.emitLocalFn = e.emitLocalCandidate
	}
	uf := e.opts.UserFrequency
	e.resetSeen()
	e.emitCap = 0

	hasAlts := e.shuangpin || len(e.opts.FuzzyPinyin) > 0
	var segAlts [][]string
	if hasAlts {
		segAlts = e.expandSegmentAlts(segs)
	}

	// 1. Words covering all syllables, then 2. words covering a prefix (longest first).
	fullCovered := false
	for n := len(segs); n >= 2; n-- {
		tierStart := len(e.cands)
		var customScores map[string]int32
		var pyKey string
		if uf != nil {
			pyKey = joinCompact(segs[:n])
			if n == len(segs) {
				if custom := uf.CustomWordsForPrefix(pyKey); len(custom) > 0 {
					customScores = make(map[string]int32, len(custom))
					for _, u := range custom {
						if u.Word != "" {
							customScores[u.Word] = u.Frequency
							e.addCandidate(u.Word, n, false)
						}
					}
				}
			}
		}
		prefix := segs[:n]
		e.emitConsumed = n
		e.lookupPhrases(prefix)
		if hasAlts && len(e.cands) < maxCandidates {
			e.emitAltPhrases(segAlts[:n], prefix)
		}
		if uf != nil && len(e.cands)-tierStart > 1 {
			rankTierByUserBoost(e.cands[tierStart:], uf, pyKey, e.prevCommit, customScores)
		}
		if n == len(segs) && len(e.ai) > 0 {
			for _, w := range e.ai {
				e.addCandidate(w, len(segs), true)
			}
		}
		if n == len(segs) {
			fullCovered = len(e.cands) > 0
		}
	}

	if len(segs) == 1 && len(e.ai) > 0 {
		for _, w := range e.ai {
			e.addCandidate(w, 1, true)
		}
	}

	// Learned bigrams are a generation tier, not merely a score for dictionary
	// entries. They must sit after dictionary phrases and before single-character
	// fallback so a learned continuation can surface even when it is absent from
	// the current phrase slice. The optional interface keeps the no-data path
	// byte-for-byte equivalent to the cold-start candidate list.
	if uf != nil && e.prevCommit != "" && len(e.cands) < maxCandidates {
		for _, u := range uf.BigramWordsForPrefix(e.prevCommit, joinCompact(segs)) {
			if consumed := bigramConsumed(u.PinyinKey, segs); consumed > 0 {
				e.addCandidate(u.Word, consumed, false)
			}
			if len(e.cands) >= maxCandidates {
				break
			}
		}
	}

	// 3. Single-syllable words and characters for the first segment.
	singlesStart := -1
	if len(e.cands) < maxCandidates {
		tierStart := len(e.cands)
		singlesStart = tierStart
		var customScores map[string]int32
		if uf != nil && len(segs) == 1 {
			if custom := uf.CustomWordsForPrefix(segs[0]); len(custom) > 0 {
				customScores = make(map[string]int32, len(custom))
				for _, u := range custom {
					if u.Word != "" {
						customScores[u.Word] = u.Frequency
						e.addCandidate(u.Word, 1, false)
					}
				}
			}
		}
		e.emitConsumed = 1
		if !hasAlts || len(segAlts[0]) <= 1 {
			e.emitCap = 0
			e.lookupSingle(segs[0])
		} else {
			// Emit top 3 of the primary syllable first, then top 2 of each Shuangpin/fuzzy
			// variant so every alternative reading stays reachable on page 1, then the rest.
			e.emitCap = 3
			e.emitCount = 0
			e.lookupSingle(segAlts[0][0])
			for _, alt := range segAlts[0][1:] {
				if len(e.cands) >= maxCandidates {
					break
				}
				e.emitCap = 2
				e.emitCount = 0
				e.lookupSingle(alt)
			}
			e.emitCap = 0
			for _, alt := range segAlts[0] {
				if len(e.cands) >= maxCandidates {
					break
				}
				e.lookupSingle(alt)
			}
		}
		if uf != nil && len(e.cands)-tierStart > 1 {
			rankTierByUserBoost(e.cands[tierStart:], uf, segs[0], e.prevCommit, customScores)
		}
	}

	// Sentence candidates (splicing several syllables into one made-up string) were removed
	// once the common dictionary landed. They were a stopgap for a 222-word starter list:
	// with only 213 two-character words the engine had nothing to offer for 3+ syllables, so
	// it glued together the longest phrase at each position. That produced plausible-looking
	// but wrong text — 「我始终」 is a real word for wo'shi'zhong, yet gluing it into
	// wo'shi'ye'shu'gen yielded 「我始终国人」, and ye'shu (页数) plus gen gave 「页数根」.
	// With ~46k real words the phrases a typist wants are dictionary entries, so the
	// candidate list is now simply: full-input words, prefix words (longest first), singles.
	if !fullCovered && singlesStart >= 0 && singlesStart < len(e.cands) {
		e.orderFirstPageByCoverage()
	}
}

// orderFirstPageByCoverage stable-sorts the first page by how many syllables each candidate
// commits, widest first. Candidates beyond the page are untouched: they are reached by
// paging, where weight order inside a coverage class still wins.
func (e *Engine) orderFirstPageByCoverage() {
	end := min(e.opts.CandidateCount, len(e.cands))
	page := e.cands[:end]
	slices.SortStableFunc(page, func(a, b Candidate) int {
		if a.Consumed != b.Consumed {
			return b.Consumed - a.Consumed // wider coverage first
		}
		return 0 // keep weight order within a class
	})
}

func (e *Engine) expandSegmentAlts(segs []string) [][]string {
	if cap(e.segAlts) < len(segs) {
		e.segAlts = make([][]string, len(segs))
	} else {
		e.segAlts = e.segAlts[:len(segs)]
	}
	for i, seg := range segs {
		alts := e.segAlts[i][:0]
		alts = append(alts, seg)
		appendAlt := func(v string) {
			if v != "" && !slices.Contains(alts, v) {
				alts = append(alts, v)
			}
		}
		if e.shuangpin && i < len(e.rawSegs) {
			for _, sy := range ime.ShuangpinSyllables(e.opts.Scheme, e.rawSegs[i]) {
				appendAlt(sy)
			}
		}
		if len(e.opts.FuzzyPinyin) > 0 {
			baseLen := len(alts)
			for bi := 0; bi < baseLen; bi++ {
				for _, fz := range ime.ExpandFuzzy(alts[bi], e.opts.FuzzyPinyin) {
					appendAlt(fz)
				}
			}
		}
		e.segAlts[i] = alts
	}
	return e.segAlts
}

func (e *Engine) emitAltPhrases(segAlts [][]string, primary []string) {
	n := len(segAlts)
	multi := false
	for _, a := range segAlts {
		if len(a) > 1 {
			multi = true
			break
		}
	}
	if !multi {
		return
	}
	if cap(e.comboBuf) < n {
		e.comboBuf = make([]string, n)
	} else {
		e.comboBuf = e.comboBuf[:n]
	}
	combo := e.comboBuf
	emittedCombos := 0
	var walk func(idx int, diff bool)
	walk = func(idx int, diff bool) {
		if len(e.cands) >= maxCandidates || emittedCombos >= maxAltCombos {
			return
		}
		if idx == n {
			if !diff {
				return
			}
			emittedCombos++
			e.lookupPhrases(combo)
			return
		}
		for _, candSyl := range segAlts[idx] {
			combo[idx] = candSyl
			walk(idx+1, diff || candSyl != primary[idx])
			if len(e.cands) >= maxCandidates || emittedCombos >= maxAltCombos {
				return
			}
		}
	}
	walk(0, false)
}

func bigramConsumed(reading string, segs []string) int {
	if reading == "" || len(segs) == 0 {
		return 0
	}
	full := splitIMEPinyin(reading)
	for start := range full {
		if len(full)-start < len(segs) {
			continue
		}
		if matchKey(full[start:start+len(segs)], segs) {
			return len(segs)
		}
	}
	return 0
}

func joinCompact(segs []string) string {
	if len(segs) == 1 {
		return segs[0]
	}
	var b strings.Builder
	total := 0
	for _, s := range segs {
		total += len(s)
	}
	b.Grow(total)
	for _, s := range segs {
		b.WriteString(s)
	}
	return b.String()
}

// CompactReading exposes the engine's pinyin-key representation to storage and UI.
func CompactReading(segs []string) string { return joinCompact(segs) }

func rankTierByUserBoost(tier []Candidate, uf UserFrequencyProvider, pyKey, prevWord string, customFreq map[string]int32) {
	type scored struct {
		cand  Candidate
		score int32
	}
	anyNonZero := false
	items := make([]scored, len(tier))
	for i, c := range tier {
		s := uf.LookupBoost(pyKey, c.Text, prevWord)
		if customFreq != nil {
			s += customFreq[c.Text]
		}
		if s != 0 {
			anyNonZero = true
		}
		items[i] = scored{cand: c, score: s}
	}
	if !anyNonZero {
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].score > items[j].score
	})
	for i := range items {
		tier[i] = items[i].cand
	}
}

func candidatesFor(segs []string, ai []string) []Candidate {
	e := &Engine{
		dict:  defaultDictionary(),
		segs:  segs,
		ai:    ai,
		cands: make([]Candidate, 0, maxCandidates),
	}
	e.emitLocalFn = e.emitLocalCandidate
	e.buildCandidates()
	return e.cands
}

// IsLetterKey reports whether r is an ASCII a-z letter (helper for platform mappers).
func IsLetterKey(r rune) bool { return r < unicode.MaxASCII && r >= 'a' && r <= 'z' }
