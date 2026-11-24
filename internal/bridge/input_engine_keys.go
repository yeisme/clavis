package bridge

import (
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

const maxPendingIMECommits = 256

// inputEngineConfigEqual reports whether two [input_engine] configs are
// equivalent. Callers use it to skip an engine rebuild on config reload: the
// rebuild loads the lexicon (possibly compiling a Rime dictionary) and resets
// any in-flight composition, so an unrelated edit (theme, language, …) must
// not trigger it.
func inputEngineConfigEqual(a, b config.InputEngineConfig) bool {
	return a.Enabled == b.Enabled &&
		a.ForceTakeover == b.ForceTakeover &&
		a.Mode == b.Mode &&
		a.Scheme == b.Scheme &&
		a.CandidateCount == b.CandidateCount &&
		a.ShiftToggleASCII == b.ShiftToggleASCII &&
		a.UserLearning == b.UserLearning &&
		a.IMKSocketPath == b.IMKSocketPath &&
		slices.Equal(a.FuzzyPinyin, b.FuzzyPinyin) &&
		slices.Equal(a.CustomDictPaths, b.CustomDictPaths) &&
		slices.Equal(a.UserWords, b.UserWords) &&
		a.AutoSeedFullName == b.AutoSeedFullName &&
		a.SeededFullName == b.SeededFullName
}

// InputEngineBlocklistHost accepts the local blocklist without coupling the bridge to storage.
type InputEngineBlocklistHost interface {
	UpdateInputEngineBlockedWords(words []string)
}

// InputEngineCommit describes one committed candidate from the own input engine.
type InputEngineCommit struct {
	PinyinKey string
	Word      string
	PrevWord  string
	Pinned    bool
	// FirstChoice is true when the user committed the top-ranked candidate
	// (page 0, index 0) rather than paging or picking a lower-ranked option.
	FirstChoice bool
	// LatencyNs is the hook-side latency from key arrival to commit decision,
	// measured inside the hook thread (no I/O, no allocation).
	LatencyNs int64
}

// InputEngineLearningHost is optionally implemented by bridges that host the own input
// engine, allowing runtime injection of the RAM-only user frequency provider and an
// off-hook commit callback without importing internal/storage.
type InputEngineLearningHost interface {
	UpdateInputEngineLearning(userFreq inputengine.UserFrequencyProvider, onCommit func(InputEngineCommit))
}

// Virtual-key codes used by the input engine mapping (duplicated untagged for testability).
const (
	ieVKBack   = 0x08
	ieVKTab    = 0x09
	ieVKReturn = 0x0D
	ieVKEscape = 0x1B
	ieVKSpace  = 0x20
	ieVKPrior  = 0x21 // PageUp ... Down = 0x22..0x28
	ieVKDown   = 0x28
	ieVKInsert = 0x2D
	ieVKDelete = 0x2E
	ieVKF1     = 0x70
	ieVKF24    = 0x87
)

// inputEngineOptionsFromConfig converts [input_engine] config into inputengine.Options.
// Returns ok=false when the own input engine is disabled or configured for a non-hook mode,
// preserving the zero-overhead disabled default. When user_learning is false, UserFrequency
// is forced to nil so learning stays completely inert.
func inputEngineOptionsFromConfig(cfg config.InputEngineConfig, cacheDir string, userFreq inputengine.UserFrequencyProvider) (inputengine.Options, bool) {
	if !cfg.Enabled || (cfg.Mode != "" && cfg.Mode != "inline_hook") {
		return inputengine.Options{}, false
	}
	resolvedCache := strings.TrimSpace(cacheDir)
	if resolvedCache == "" && len(cfg.CustomDictPaths) > 0 {
		resolvedCache = defaultInputEngineCacheDir()
	}
	if !cfg.UserLearning {
		userFreq = nil
	}
	// Resolve and index all dictionaries during ConfigureInputEngine, not in the
	// hook's key path. The resulting Dictionary is immutable and lookup-only.
	dict, _ := inputengine.LoadDictionaryWithDiagnostics(cfg.CustomDictPaths, resolvedCache, func(d inputengine.DictionaryDiagnostic) {
		log.Printf("inputengine: dictionary diagnostic path=%q kind=%s message=%s", d.Path, d.Kind, d.Message)
	})
	return inputengine.Options{
		CandidateCount:   cfg.CandidateCount,
		ShiftToggleASCII: cfg.ShiftToggleASCII,
		Scheme:           cfg.Scheme,
		FuzzyPinyin:      slices.Clone(cfg.FuzzyPinyin),
		Dictionary:       dict,
		UserFrequency:    userFreq,
	}, true
}

func newInputEngineFromConfig(cfg config.InputEngineConfig, cacheDir string, userFreq inputengine.UserFrequencyProvider) *inputengine.Engine {
	opts, ok := inputEngineOptionsFromConfig(cfg, cacheDir, userFreq)
	if !ok {
		return nil
	}
	return inputengine.New(opts)
}

// inputEngineCommitTracker extracts committed candidates on the hook thread without doing any
// SQL or I/O, buffering commits for the off-hook view/worker goroutine to persist.
type inputEngineCommitTracker struct {
	mu       sync.Mutex
	prevWord string
	pending  []InputEngineCommit
}

func (t *inputEngineCommitTracker) resetPrev() {
	t.mu.Lock()
	t.prevWord = ""
	t.mu.Unlock()
}

func (t *inputEngineCommitTracker) pushCommit(pinyinKey, word string, firstChoice bool, latencyNs int64) {
	t.mu.Lock()
	if len(t.pending) < maxPendingIMECommits {
		t.pending = append(t.pending, InputEngineCommit{
			PinyinKey:   pinyinKey,
			Word:        word,
			PrevWord:    t.prevWord,
			FirstChoice: firstChoice,
			LatencyNs:   latencyNs,
		})
	}
	t.prevWord = word
	t.mu.Unlock()
}

func (t *inputEngineCommitTracker) hasPending() bool {
	t.mu.Lock()
	n := len(t.pending)
	t.mu.Unlock()
	return n > 0
}

func (t *inputEngineCommitTracker) drain(dst []InputEngineCommit) []InputEngineCommit {
	t.mu.Lock()
	if len(t.pending) > 0 {
		dst = append(dst, t.pending...)
		t.pending = t.pending[:0]
	}
	t.mu.Unlock()
	return dst
}

func (t *inputEngineCommitTracker) handleKey(eng *inputengine.Engine, k inputengine.Key, learn bool) inputengine.Result {
	// start measures key-arrival → commit-decision latency for the resident
	// telemetry (P50/P99). time.Now is ~20ns and allocates nothing, so this is
	// safe on the hook thread.
	start := time.Now()
	switch k.Kind {
	case inputengine.KeyShiftDown:
		return eng.HandleKey(k)
	case inputengine.KeyShiftUp:
		res := eng.HandleKey(k)
		if res.Changed || res.Commit != "" {
			t.resetPrev()
		}
		return res
	case inputengine.KeyEscape, inputengine.KeyEnter:
		res := eng.HandleKey(k)
		t.resetPrev()
		return res
	case inputengine.KeyLetter, inputengine.KeyBackspace, inputengine.KeyApostrophe,
		inputengine.KeyPagePrev, inputengine.KeyPageNext:
		if k.Chord {
			res := eng.HandleKey(k)
			if eng.Raw() == "" {
				t.resetPrev()
			}
			return res
		}
		return eng.HandleKey(k)
	}

	if k.Chord || k.Kind == inputengine.KeyNavigation {
		res := eng.HandleKey(k)
		// Preserve bigram context while navigating within an active composition
		// (e.g., Up/Down candidate highlight movement or candidate paging). Only reset
		// prevWord when navigation happens outside composition or aborts composition.
		if eng.Raw() == "" {
			t.resetPrev()
		}
		return res
	}

	if !learn || eng.Raw() == "" {
		res := eng.HandleKey(k)
		if res.Commit != "" {
			t.resetPrev()
		}
		return res
	}

	preView := eng.View()
	res := eng.HandleKey(k)
	if res.Commit == "" || !preView.Composing {
		return res
	}

	word, firstChoice, ok := committedCandidateWord(preView, k, res.Commit, eng.Raw() == "")
	if !ok || word == "" {
		t.resetPrev()
		return res
	}

	var pinyinKey string
	if eng.Raw() == "" {
		pinyinKey = strings.ReplaceAll(preView.Preedit, "'", "")
	} else {
		postPreedit := eng.View().Preedit
		if postPreedit != "" && strings.HasSuffix(preView.Preedit, "'"+postPreedit) {
			consumed := strings.TrimSuffix(preView.Preedit, "'"+postPreedit)
			pinyinKey = strings.ReplaceAll(consumed, "'", "")
		}
	}

	if pinyinKey != "" {
		t.pushCommit(pinyinKey, word, firstChoice, time.Since(start).Nanoseconds())
	} else {
		t.resetPrev()
	}
	return res
}

func committedCandidateWord(preView inputengine.View, k inputengine.Key, commit string, fullCommit bool) (string, bool, bool) {
	switch k.Kind {
	case inputengine.KeySpace:
		idx := preView.Selected
		if idx >= 0 && idx < len(preView.Candidates) && commit == preView.Candidates[idx].Text {
			return preView.Candidates[idx].Text, preView.Page == 0 && idx == 0, true
		}
	case inputengine.KeyDigit:
		idx := int(k.Rune-'0') - 1
		if idx >= 0 && idx < len(preView.Candidates) && commit == preView.Candidates[idx].Text {
			return preView.Candidates[idx].Text, preView.Page == 0 && idx == 0, true
		}
	default:
		// Align with inputengine.Engine.HandleKey punctuation auto-commit: pick the first
		// non-AI candidate covering all segments (not blindly preView.Candidates[0]).
		if fullCommit {
			segCount := preeditSegmentCount(preView.Preedit)
			for i, c := range preView.Candidates {
				if c.AI {
					continue
				}
				if segCount > 0 && c.Consumed < segCount {
					continue
				}
				expected := c.Text
				if k.Rune != 0 {
					expected += string(k.Rune)
				}
				if commit == expected {
					return c.Text, preView.Page == 0 && i == 0, true
				}
				break
			}
		}
	}
	return "", false, false
}

func preeditSegmentCount(preedit string) int {
	if preedit == "" {
		return 0
	}
	return strings.Count(preedit, "'") + 1
}

func defaultInputEngineCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "clavis", "dicts")
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "clavis", "dicts")
	}
	return ""
}

// engineKeyForVK maps a key-down to an inputengine.Key. text is the ToUnicodeEx output for
// the current layout/shift state; chord is true when Ctrl/Alt/Win is held. ok=false means the
// key is irrelevant to the engine (e.g. bare modifiers) and must bypass it entirely.
func engineKeyForVK(vk uint16, text string, chord bool) (inputengine.Key, bool) {
	r, _ := utf8.DecodeRuneInString(text)
	if text == "" {
		r = 0
	}
	k := inputengine.Key{Rune: r, Chord: chord}
	switch {
	case vk == ieVKBack:
		k.Kind = inputengine.KeyBackspace
	case vk == ieVKReturn:
		k.Kind = inputengine.KeyEnter
	case vk == ieVKEscape:
		k.Kind = inputengine.KeyEscape
	case vk == ieVKSpace:
		k.Kind = inputengine.KeySpace
	case vk == ieVKTab, vk >= ieVKPrior && vk <= ieVKDown, vk == ieVKInsert, vk == ieVKDelete,
		vk >= ieVKF1 && vk <= ieVKF24:
		k.Kind = inputengine.KeyNavigation
	case r >= 'a' && r <= 'z':
		k.Kind = inputengine.KeyLetter
	case r >= '0' && r <= '9':
		k.Kind = inputengine.KeyDigit
	case r == '\'':
		k.Kind = inputengine.KeyApostrophe
	case r == '-':
		k.Kind = inputengine.KeyPagePrev
	case r == '=':
		k.Kind = inputengine.KeyPageNext
	case r != 0 && r >= 0x20:
		k.Kind = inputengine.KeyOther
	default:
		if !chord {
			return k, false // dead keys / unmapped: leave to the OS
		}
		k.Kind = inputengine.KeyNavigation
	}
	return k, true
}

// utf16Units encodes s for KEYEVENTF_UNICODE SendInput (surrogate pairs for non-BMP).
func utf16Units(s string) []uint16 {
	return utf16.Encode([]rune(s))
}

// inputEngineKeyForInline is the platform-neutral mapping used by macOS' CGEventTap and
// unit-tested on every platform. Rune carries the layout-decoded printable character.
func inputEngineKeysForInline(ev host.InlineKeyEvent) ([]inputengine.Key, bool) {
	if strings.EqualFold(ev.KeyName, "shift_tap") {
		return []inputengine.Key{{Kind: inputengine.KeyShiftDown}, {Kind: inputengine.KeyShiftUp}}, true
	}
	chord := false
	for _, mod := range ev.Modifiers {
		switch strings.ToLower(mod) {
		case "ctrl", "control", "alt", "option", "cmd", "command", "meta":
			chord = true
		}
	}
	name := strings.ToLower(ev.KeyName)
	r, _ := utf8.DecodeRuneInString(ev.Rune)
	if ev.Rune == "" {
		r = 0
	}
	k := inputengine.Key{Rune: r, Chord: chord}
	switch name {
	case "up":
		k.NavDir = inputengine.NavUp
	case "down":
		k.NavDir = inputengine.NavDown
	}
	// Prefer the layout-decoded Rune for character classification. KeyName is
	// only a fallback for non-printable named keys; this keeps macOS aligned
	// with the Windows ToUnicodeEx path when shifted symbols share a keycode.
	switch {
	case r == ' ':
		k.Kind = inputengine.KeySpace
	case r == '\r' || r == '\n':
		k.Kind = inputengine.KeyEnter
	case r == '\b' || r == 0x7f:
		k.Kind = inputengine.KeyBackspace
	case r == 0x1b:
		k.Kind = inputengine.KeyEscape
	case r >= 'a' && r <= 'z':
		k.Kind = inputengine.KeyLetter
	case r >= '0' && r <= '9':
		k.Kind = inputengine.KeyDigit
	case r == '\'':
		k.Kind = inputengine.KeyApostrophe
	case r == '-':
		k.Kind = inputengine.KeyPagePrev
	case r == '=':
		k.Kind = inputengine.KeyPageNext
	case r >= 0x20:
		k.Kind = inputengine.KeyOther
	case name == "space":
		k.Kind = inputengine.KeySpace
	case name == "return" || name == "enter":
		k.Kind = inputengine.KeyEnter
	case name == "backspace" || name == "delete":
		k.Kind = inputengine.KeyBackspace
	case name == "escape":
		k.Kind = inputengine.KeyEscape
	case name == "tab" || name == "left" || name == "right" || name == "up" || name == "down" || name == "home" || name == "end" || name == "pageup" || name == "pagedown" || (len(name) > 1 && name[0] == 'f'):
		k.Kind = inputengine.KeyNavigation
	default:
		if chord {
			k.Kind = inputengine.KeyNavigation
		} else {
			k.Kind = inputengine.KeyOther
		}
	}
	return []inputengine.Key{k}, true
}

func inputEngineKeyForInline(ev host.InlineKeyEvent) (inputengine.Key, bool) {
	keys, ok := inputEngineKeysForInline(ev)
	if !ok || len(keys) != 1 {
		return inputengine.Key{}, false
	}
	return keys[0], true
}

func inputEngineCommitUnits(s string) []uint16 { return utf16.Encode([]rune(s)) }

// emitInputEngineCommit posts UTF-16 chunks synchronously. The poster is injectable for tests.
func emitInputEngineCommit(s string, post func([]uint16)) {
	units := inputEngineCommitUnits(s)
	for len(units) > 0 {
		n := len(units)
		if n > 20 {
			n = 20
		}
		post(units[:n])
		units = units[n:]
	}
}

// inputEngineEligible applies design §6 rules 3/5 + password guard: the engine only takes over
// when the system IME is in ASCII state, the field is not a password, the foreground app is
// not Clavis, and no keymap layer owns the key.
func inputEngineEligible(nativeIME, secure, selfApp bool) bool {
	return !nativeIME && !secure && !selfApp
}
