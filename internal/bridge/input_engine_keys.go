package bridge

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

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
// preserving the zero-overhead disabled default.
func inputEngineOptionsFromConfig(cfg config.InputEngineConfig, cacheDir string) (inputengine.Options, bool) {
	if !cfg.Enabled || (cfg.Mode != "" && cfg.Mode != "inline_hook" && cfg.Mode != "dual") {
		return inputengine.Options{}, false
	}
	resolvedCache := strings.TrimSpace(cacheDir)
	if resolvedCache == "" && len(cfg.CustomDictPaths) > 0 {
		resolvedCache = defaultInputEngineCacheDir()
	}
	return inputengine.Options{
		CandidateCount:   cfg.CandidateCount,
		ShiftToggleASCII: cfg.ShiftToggleASCII,
		Scheme:           cfg.Scheme,
		FuzzyPinyin:      slices.Clone(cfg.FuzzyPinyin),
		CustomDictPaths:  slices.Clone(cfg.CustomDictPaths),
		DictCacheDir:     resolvedCache,
	}, true
}

func newInputEngineFromConfig(cfg config.InputEngineConfig, cacheDir string) *inputengine.Engine {
	opts, ok := inputEngineOptionsFromConfig(cfg, cacheDir)
	if !ok {
		return nil
	}
	return inputengine.New(opts)
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
	var out []uint16
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
			continue
		}
		out = append(out, uint16(r))
	}
	return out
}

// inputEngineEligible applies design §6 rules 3/5 + password guard: the engine only takes over
// when the system IME is in ASCII state, the field is not a password, the foreground app is
// not Clavis, and no keymap layer owns the key.
func inputEngineEligible(nativeIME, secure, selfApp bool) bool {
	return !nativeIME && !secure && !selfApp
}
