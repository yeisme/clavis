// Package inputengine is Clavis' own pinyin composition engine (docs/design/own-input-engine.md).
// It is pure logic: the platform layer feeds normalized keys and performs commits/rendering.
package inputengine

import (
	"sort"
	"strings"
	"sync"
	"unicode"
)

// KeyKind classifies a key for the composition state machine (§4).
type KeyKind int

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

// Key is one key-down (or Shift up/down) event.
type Key struct {
	Kind KeyKind
	Rune rune
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
	Text     string
	consumed int  // number of leading segments this candidate covers
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
	HasPrev    bool
	HasNext    bool
}

// Options configure the engine.
type Options struct {
	CandidateCount   int // page size, clamped to 1..9
	ShiftToggleASCII bool
}

// Engine holds composition state. All methods are safe for concurrent use (hook thread +
// UI/AI goroutines) and never block on I/O.
type Engine struct {
	mu         sync.Mutex
	opts       Options
	raw        string // letters and ' as typed
	segs       []string
	cands      []Candidate
	ai         []string
	page, sel  int
	ascii      bool
	shiftAlone bool
}

const maxRawLen = 64

func New(opts Options) *Engine {
	opts.CandidateCount = min(max(opts.CandidateCount, 1), 9)
	getLexicon() // build tables up front, not on the first keystroke inside the hook
	return &Engine{opts: opts}
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
	e.ai = words
	e.rebuild(false)
	return true
}

func (e *Engine) clear() {
	e.raw, e.segs, e.cands, e.ai = "", nil, nil, nil
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
		return Result{Commit: commit, Changed: true}
	}
	e.shiftAlone = false

	composing := e.raw != ""
	if k.Chord || k.Kind == KeyNavigation {
		if composing {
			e.clear()
			return Result{Changed: true}
		}
		return Result{}
	}
	if !composing {
		if k.Kind == KeyLetter && !e.ascii {
			e.raw = string(k.Rune)
			e.rebuild(true)
			return Result{Consume: true, Changed: true}
		}
		return Result{}
	}

	switch k.Kind {
	case KeyLetter:
		if len(e.raw) < maxRawLen {
			e.raw += string(k.Rune)
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
		return Result{Consume: true, Changed: true}
	case KeyEnter:
		commit := e.rawLetters()
		e.clear()
		return Result{Consume: true, Commit: commit, Changed: true}
	case KeySpace:
		return e.selectIndex(e.sel)
	case KeyDigit:
		n := int(k.Rune - '0')
		if n < 1 || n > e.opts.CandidateCount {
			return Result{Consume: true}
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
		// Other printable (punctuation, upper-case): commit the top candidate then the char.
		top := e.rawLetters()
		if len(e.cands) > 0 && e.cands[0].consumed >= len(e.segs) {
			top = e.cands[0].Text
		}
		e.clear()
		if k.Rune != 0 {
			top += string(k.Rune)
		}
		return Result{Consume: true, Commit: top, Changed: true}
	}
}

// selectIndex commits the idx-th candidate on the current page. A candidate covering only a
// prefix of the syllables commits immediately and leaves the rest composing.
func (e *Engine) selectIndex(idx int) Result {
	abs := e.page*e.opts.CandidateCount + idx
	if abs < 0 || abs >= len(e.cands) {
		if len(e.cands) == 0 {
			commit := e.rawLetters()
			e.clear()
			return Result{Consume: true, Commit: commit, Changed: true}
		}
		return Result{Consume: true}
	}
	c := e.cands[abs]
	if c.consumed >= len(e.segs) {
		e.clear()
		return Result{Consume: true, Commit: c.Text, Changed: true}
	}
	e.raw = strings.Join(e.segs[c.consumed:], "'")
	e.ai = nil
	e.rebuild(true)
	return Result{Consume: true, Commit: c.Text, Changed: true}
}

func (e *Engine) rawLetters() string { return strings.ReplaceAll(e.raw, "'", "") }

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
	return v
}

func (e *Engine) rebuild(resetPage bool) {
	e.segs = Segment(e.raw)
	e.cands = candidatesFor(e.segs, e.ai)
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
	l := getLexicon()
	var out []string
	parts := strings.Split(raw, "'")
	for pi, part := range parts {
		if part == "" {
			continue
		}
		last := pi == len(parts)-1
		out = append(out, segmentChunk(l, part, last)...)
	}
	return out
}

func segmentChunk(l *lexicon, s string, allowTrailingPrefix bool) []string {
	n := len(s)
	const inf = 1 << 30
	cost := make([]int, n+1)
	prev := make([]int, n+1)
	for i := 1; i <= n; i++ {
		cost[i] = inf
	}
	for i := 0; i < n; i++ {
		if cost[i] == inf {
			continue
		}
		for j := i + 1; j <= min(n, i+l.maxSylLen); j++ {
			w := s[i:j]
			c := inf
			switch {
			case l.syllables[w]:
				c = 1
			case j == n && allowTrailingPrefix && l.prefixes[w]:
				c = 2
			case j == i+1:
				c = 3
			}
			if c < inf && cost[i]+c < cost[j] {
				cost[j], prev[j] = cost[i]+c, i
			}
		}
	}
	var segs []string
	for j := n; j > 0; j = prev[j] {
		segs = append(segs, s[prev[j]:j])
	}
	for i, k := 0, len(segs)-1; i < k; i, k = i+1, k-1 {
		segs[i], segs[k] = segs[k], segs[i]
	}
	return segs
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

const maxCandidates = 60

func candidatesFor(segs []string, ai []string) []Candidate {
	if len(segs) == 0 {
		return nil
	}
	l := getLexicon()
	seen := map[string]bool{}
	var out []Candidate
	add := func(text string, consumed int, isAI bool) {
		if text == "" || seen[text] || len(out) >= maxCandidates {
			return
		}
		seen[text] = true
		out = append(out, Candidate{Text: text, consumed: consumed, AI: isAI})
	}
	// 1. Words covering all syllables, then 2. words covering a prefix (longest first).
	keys := make([]string, 0, len(l.words))
	for k := range l.words {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic order
	for n := len(segs); n >= 2; n-- {
		for _, k := range keys {
			key := strings.Fields(k)
			if len(key) == n && matchKey(key, segs) {
				for _, w := range l.words[k] {
					add(w, n, false)
				}
			}
		}
		if n == len(segs) {
			for _, w := range ai {
				add(w, len(segs), true)
			}
		}
	}
	if len(segs) == 1 {
		for _, w := range ai {
			add(w, 1, true)
		}
	}
	// 3. Single characters for the first syllable (or every syllable starting with the prefix).
	for _, r := range charsFor(l, segs[0]) {
		add(string(r), 1, false)
	}
	return out
}

func charsFor(l *lexicon, seg string) []rune {
	if l.syllables[seg] {
		return l.bySyllable[seg]
	}
	var syls []string
	for s := range l.bySyllable {
		if strings.HasPrefix(s, seg) {
			syls = append(syls, s)
		}
	}
	sort.Strings(syls)
	var all []rune
	for _, s := range syls {
		all = append(all, l.bySyllable[s][:min(8, len(l.bySyllable[s]))]...)
	}
	sort.SliceStable(all, func(i, j int) bool { return l.rankOf(all[i]) < l.rankOf(all[j]) })
	return all
}

// IsLetterKey reports whether r is an ASCII a-z letter (helper for platform mappers).
func IsLetterKey(r rune) bool { return r < unicode.MaxASCII && r >= 'a' && r <= 'z' }
