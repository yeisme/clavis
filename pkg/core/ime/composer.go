package ime

import (
	"strings"
	"unicode"

	"github.com/yeisme/clavis/pkg/core/config"
)

type KeyKind uint8

const (
	KeyRune KeyKind = iota
	KeyBackspace
	KeyEnter
	KeySpace
	KeyShift
	KeyUp
	KeyDown
	KeyPageUp
	KeyPageDown
	KeyDigit
	KeyPunctuation
)

type KeyEvent struct {
	Kind  KeyKind
	Rune  rune
	Shift bool
	Text  string
}
type QuickRouteKind uint8

const (
	RouteNone QuickRouteKind = iota
	RoutePromptTemplate
	RoutePromptOptimize
	RouteSnippet
	RouteEmoji
	RouteCalculator
)

type QuickRoute struct {
	Kind   QuickRouteKind
	Prefix string
	Text   string
}
type ComposerState struct {
	Preedit string
	Route   QuickRoute
}
type KeyOutcome struct {
	Consumed                        bool
	CommitText                      string
	CursorBackOffset                int
	PreeditDisplay                  string
	Candidates                      []Candidate
	Page, TotalPages, SelectedIndex int
	ASCIIMode                       bool
	QuickRoute                      QuickRoute
}

type Composer struct {
	cfg            Config
	lex            *LexiconStack
	keys           []byte
	preedit        strings.Builder
	candidates     []Candidate
	page, selected int
	ascii          bool
	shiftPending   bool
	route          QuickRoute
}
type Config struct {
	Scheme                                              string
	CandidateCount                                      int
	MixedEnglish, AutoPairPunctuation, ShiftToggleASCII bool
	FuzzyPinyin                                         []string
	User                                                UserFrequencyProvider
	RoutePromptTemplate                                 string
}

// NewComposer accepts both the public config shape and the extended test/config shape.
func NewComposer(value interface{}, lex *LexiconStack) *Composer {
	cfg := Config{Scheme: "quanpin", CandidateCount: 7, MixedEnglish: true, AutoPairPunctuation: true, ShiftToggleASCII: true}
	switch v := value.(type) {
	case Config:
		cfg = v
	case config.InputEngineConfig:
		cfg.Scheme, cfg.CandidateCount, cfg.MixedEnglish = v.Scheme, v.CandidateCount, v.MixedEnglish
		cfg.AutoPairPunctuation, cfg.ShiftToggleASCII, cfg.FuzzyPinyin = v.AutoPairPunctuation, v.ShiftToggleASCII, v.FuzzyPinyin
	}
	if cfg.CandidateCount < 1 {
		cfg.CandidateCount = 7
	}
	if cfg.CandidateCount > 9 {
		cfg.CandidateCount = 9
	}
	if lex == nil {
		lex = NewLexiconStack(StarterLexicon())
	}
	return &Composer{cfg: cfg, lex: lex, keys: make([]byte, 0, 128)}
}
func (c *Composer) State() ComposerState {
	return ComposerState{Preedit: c.preedit.String(), Route: c.route}
}
func (c *Composer) reset() {
	c.preedit.Reset()
	c.keys = c.keys[:0]
	c.candidates = c.candidates[:0]
	c.page = 0
	c.selected = 0
	c.route = QuickRoute{}
}
func (c *Composer) result(consumed bool, commit string, back int) KeyOutcome {
	display := c.preedit.String()
	return KeyOutcome{Consumed: consumed, CommitText: commit, CursorBackOffset: back, PreeditDisplay: display, Candidates: c.candidates, Page: c.page, TotalPages: (len(c.candidates) + c.cfg.CandidateCount - 1) / c.cfg.CandidateCount, SelectedIndex: c.selected, ASCIIMode: c.ascii, QuickRoute: c.route}
}
func (c *Composer) refresh() {
	raw := c.preedit.String()
	if raw == "" {
		c.candidates = nil
		c.route = QuickRoute{}
		return
	}
	c.route = routeFor(raw)
	parts := syllableCodes(c.cfg.Scheme, raw, c.cfg.FuzzyPinyin)
	key := strings.Join(parts, " ")
	c.candidates = c.lex.LookupWithPrev(key, "")
	if len(c.candidates) == 0 {
		c.candidates = c.lex.Lookup(strings.Join(parts, ""))
	}
	if len(c.candidates) == 0 {
		c.candidates = c.lex.Lookup(strings.ReplaceAll(raw, "'", ""))
	}
	if len(c.candidates) == 0 {
		c.candidates = c.lex.compose(parts)
	}
	// Fuzzy rules are alternatives, not an additional syllable in the key.
	if len(c.candidates) == 0 {
		for _, variant := range expandFuzzy(strings.ReplaceAll(strings.ToLower(raw), "'", ""), c.cfg.FuzzyPinyin) {
			if variant == strings.ReplaceAll(strings.ToLower(raw), "'", "") {
				continue
			}
			c.candidates = c.lex.Lookup(strings.Join(splitQuanpin(variant), " "))
			if len(c.candidates) > 0 {
				break
			}
		}
	}
	c.page = 0
	c.selected = 0
}
func routeFor(s string) QuickRoute {
	if len(s) < 2 {
		return QuickRoute{}
	}
	kind := RouteNone
	prefix := s[:1]
	switch {
	case strings.HasPrefix(s, "//"):
		kind = RoutePromptOptimize
		prefix = "//"
	case s[0] == '/':
		kind = RoutePromptTemplate
	case s[0] == ';':
		kind = RouteSnippet
	case s[0] == ':':
		kind = RouteEmoji
	case s[0] == '=':
		kind = RouteCalculator
	}
	if kind == RouteNone {
		return QuickRoute{}
	}
	return QuickRoute{Kind: kind, Prefix: prefix, Text: s[len(prefix):]}
}
func (c *Composer) FeedKey(ev KeyEvent) KeyOutcome {
	if ev.Kind == KeyShift {
		if c.cfg.ShiftToggleASCII {
			c.ascii = !c.ascii
			c.shiftPending = false
			return c.result(true, "", 0)
		}
		return c.result(false, "", 0)
	}
	if ev.Shift && ev.Rune != 0 {
		return c.result(true, string(unicode.ToUpper(ev.Rune)), 0)
	}
	if c.ascii {
		if ev.Kind == KeyEnter || ev.Kind == KeySpace {
			s := c.preedit.String()
			c.reset()
			return c.result(true, s, 0)
		}
		if ev.Kind == KeyBackspace {
			s := c.preedit.String()
			if s != "" {
				r := []rune(s)
				c.preedit.Reset()
				c.preedit.WriteString(string(r[:len(r)-1]))
			}
			return c.result(true, "", 0)
		}
		if ev.Rune != 0 {
			c.preedit.WriteRune(ev.Rune)
			return c.result(true, "", 0)
		}
		return c.result(false, "", 0)
	}
	if ev.Kind == KeyBackspace {
		s := c.preedit.String()
		if s != "" {
			r := []rune(s)
			c.preedit.Reset()
			c.preedit.WriteString(string(r[:len(r)-1]))
			c.refresh()
			return c.result(true, "", 0)
		}
		return c.result(false, "", 0)
	}
	if ev.Kind == KeyEnter {
		if c.preedit.Len() > 0 {
			s := c.preedit.String()
			c.reset()
			return c.result(true, s, 0)
		}
		return c.result(false, "", 0)
	}
	if ev.Kind == KeySpace {
		if len(c.candidates) > 0 {
			return c.commitSelected(false)
		}
		if c.preedit.Len() > 0 {
			s := c.preedit.String()
			c.reset()
			return c.result(true, s, 0)
		}
		return c.punctuation(' ')
	}
	if ev.Kind == KeyUp || ev.Kind == KeyDown {
		if len(c.candidates) > 0 {
			if ev.Kind == KeyDown {
				c.selected++
				if c.selected >= len(c.candidates) {
					c.selected = 0
				}
			} else {
				c.selected--
				if c.selected < 0 {
					c.selected = len(c.candidates) - 1
				}
			}
			return c.result(true, "", 0)
		}
		return c.result(false, "", 0)
	}
	if ev.Kind == KeyPageUp || ev.Kind == KeyPageDown || ev.Rune == '-' || ev.Rune == '=' {
		if len(c.candidates) > 0 {
			pages := (len(c.candidates) + c.cfg.CandidateCount - 1) / c.cfg.CandidateCount
			if ev.Kind == KeyPageDown || ev.Rune == '=' {
				c.page++
				if c.page >= pages {
					c.page = 0
				}
			} else {
				c.page--
				if c.page < 0 {
					c.page = pages - 1
				}
			}
			return c.result(true, "", 0)
		}
	}
	if ev.Rune >= '1' && ev.Rune <= '9' && len(c.candidates) > 0 {
		n := int(ev.Rune-'1') + c.page*c.cfg.CandidateCount
		if n < len(c.candidates) {
			c.selected = n
			return c.commitSelected(false)
		}
	}
	if ev.Rune == '[' || ev.Rune == ']' {
		if len(c.candidates) > 0 && len([]rune(c.candidates[c.selected].Word)) > 1 {
			r := []rune(c.candidates[c.selected].Word)
			if ev.Rune == ']' {
				return c.commitText(string(r[len(r)-1]))
			}
			return c.commitText(string(r[0]))
		}
		if c.preedit.Len() == 0 {
			return c.punctuation(ev.Rune)
		}
	}
	if ev.Rune != 0 {
		if ev.Rune == '.' {
			return c.punctuation(ev.Rune)
		}
		if (ev.Rune >= 'a' && ev.Rune <= 'z') || (ev.Rune >= 'A' && ev.Rune <= 'Z') || ev.Rune == '\'' || ev.Rune >= '0' && ev.Rune <= '9' || ev.Rune == '/' || ev.Rune == ';' || ev.Rune == ':' || ev.Rune == '=' {
			c.preedit.WriteRune(unicode.ToLower(ev.Rune))
			c.keys = append(c.keys, byte(ev.Rune))
			c.refresh()
			return c.result(true, "", 0)
		}
		if c.preedit.Len() == 0 {
			return c.punctuation(ev.Rune)
		}
	}
	return c.result(false, "", 0)
}
func (c *Composer) commitText(s string) KeyOutcome { c.reset(); return c.result(true, s, 0) }
func (c *Composer) commitSelected(_ bool) KeyOutcome {
	if len(c.candidates) == 0 {
		return c.result(false, "", 0)
	}
	return c.commitText(c.candidates[c.selected].Word)
}
func (c *Composer) punctuation(r rune) KeyOutcome {
	if r >= '0' && r <= '9' {
		return c.commitText(string(r))
	}
	if r == '.' && c.preedit.Len() > 0 {
		s := c.preedit.String()
		decimal := true
		for _, x := range s {
			if (x < '0' || x > '9') && x != '.' {
				decimal = false
				break
			}
		}
		if decimal {
			c.preedit.WriteRune(r)
			return c.result(true, "", 0)
		}
		return c.result(false, "", 0)
	}
	if !c.cfg.AutoPairPunctuation {
		return c.commitText(string(r))
	}
	m := map[rune]string{',': "，", '.': "。", '?': "？", '!': "！", ':': "：", ';': "；", '\\': "、", '"': "“”", '(': "（）", '[': "【】"}
	if s, ok := m[r]; ok {
		if len([]rune(s)) == 2 {
			c.reset()
			return c.result(true, s, 1)
		}
		return c.commitText(s)
	}
	return c.commitText(string(r))
}
