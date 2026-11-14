package uiapp

import (
	"math"
	"strings"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

const (
	defaultCapsuleWidth  = 680
	minCapsuleWidth      = 420
	maxCapsuleWidth      = 960
	defaultCornerRadius  = 16.0
	defaultHUDDurationMs = 1400

	// Floating IME/status pill sizing.
	pillShortWidth   = 68
	pillShortHeight  = 30
	pillMaxWidth     = 340
	pillLongHeight   = 32
	pillTextBasePad  = 36
	pillRuneWidthEst = 9
)

// PreferredCapsuleSize computes clean compact window/surface dimensions for each CapsulePhase.
func PreferredCapsuleSize(phase engine.CapsulePhase, appearance config.AppearanceConfig) (width, height int) {
	w := int(math.Round(appearance.CapsuleWidth))
	if w <= 0 {
		w = defaultCapsuleWidth
	}
	if w < minCapsuleWidth {
		w = minCapsuleWidth
	}
	if w > maxCapsuleWidth {
		w = maxCapsuleWidth
	}
	// Chinese UI text runs wider per character: unless the user asked for
	// something even larger, keep a wider floor so hint lines and rows
	// don't crowd (the user's own larger setting always wins).
	if i18n.IsChinese() && w < defaultCapsuleWidth+40 {
		w = defaultCapsuleWidth + 40
	}

	switch phase {
	case engine.PhaseHidden:
		return 0, 0
	case engine.PhaseHUD:
		hudW := w
		if hudW > 360 {
			hudW = 360
		}
		if hudW < 240 {
			hudW = 240
		}
		return hudW, 56
	case engine.PhaseLeader:
		return w, 340
	case engine.PhaseOmnibox:
		maxVisible := appearance.MaxVisibleResults
		if maxVisible <= 0 {
			maxVisible = 8
		}
		if maxVisible < 3 {
			maxVisible = 3
		}
		if maxVisible > 12 {
			maxVisible = 12
		}
		// Header (44) + Search row (52) + padding/dividers/footer (52) + rows (44px each)
		h := 148 + maxVisible*44
		if h < 320 {
			h = 320
		}
		if h > 640 {
			h = 640
		}
		return w, h
	case engine.PhaseAIStreaming:
		return w, 480
	default:
		return w, 420
	}
}

// PreferredCapsuleSizeForState computes Capsule dimensions taking multi-turn AI history into account.
func PreferredCapsuleSizeForState(state engine.State, appearance config.AppearanceConfig) (width, height int) {
	w, h := PreferredCapsuleSize(state.Phase, appearance)
	if state.Phase == engine.PhaseAIStreaming && len(state.AIHistory) > 0 && h < 540 {
		h = 540
	}
	return w, h
}

// PreferredIMEPillSize returns compact dimensions for the floating IME micro-pill window.
func PreferredIMEPillSize() (width, height int) {
	return pillShortWidth, pillShortHeight
}

// runeWidthUnits returns how many "Latin widths" a rune occupies: CJK,
// fullwidth forms and CJK punctuation are double-width, everything else
// (including combining marks) single. It is the estimator behind the pill
// and capsule sizing so Chinese text gets the room it actually needs.
func runeWidthUnits(r rune) int {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK radicals, Kangxi, CJK symbols
		r >= 0x3041 && r <= 0x33FF, // kana, CJK compatibility
		r >= 0x3400 && r <= 0x4DBF, // CJK ext A
		r >= 0x4E00 && r <= 0x9FFF, // CJK unified
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE4F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r == 0x3000: // ideographic space
		return 2
	default:
		return 1
	}
}

// textWidthUnits estimates the rendered width of s in "Latin widths".
func textWidthUnits(s string) int {
	units := 0
	for _, r := range s {
		units += runeWidthUnits(r)
	}
	return units
}

// PreferredPillSizeForLabel sizes the floating non-activating pill for a given label.
// Short IME badges ("EN", "中", <= 3 runes) stay compact, while longer status/layer notices
// such as "LAYER • NAV" or "Bound Notes → ASCII" scale the width up (capped at 340) so the
// text renders without truncation and without the panel ever stealing focus. Width is
// estimated in CJK-aware units: a Chinese label needs roughly twice the room of Latin.
func PreferredPillSizeForLabel(label string) (width, height int) {
	trimmed := strings.TrimSpace(label)
	runeCount := utf8.RuneCountInString(trimmed)
	if runeCount <= 3 {
		return pillShortWidth, pillShortHeight
	}
	w := pillTextBasePad + textWidthUnits(trimmed)*pillRuneWidthEst
	if w < pillShortWidth {
		w = pillShortWidth
	}
	if w > pillMaxWidth {
		w = pillMaxWidth
	}
	return w, pillLongHeight
}

// NormalizeCornerRadius returns a safe positive corner radius from AppearanceConfig.
func NormalizeCornerRadius(appearance config.AppearanceConfig) float32 {
	r := appearance.CornerRadius
	if math.IsNaN(r) || math.IsInf(r, 0) || r <= 0 {
		return defaultCornerRadius
	}
	if r > 40 {
		return 40
	}
	return float32(r)
}
