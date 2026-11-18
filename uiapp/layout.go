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

	// Omnibox result rows the panel sizes for; Settings offers the same
	// range, so every value the stepper allows changes the panel.
	minVisibleResults = 3
	maxVisibleResults = 12

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
		return hudW, int(headerHeight + space16)
	case engine.PhaseLeader:
		return w, 340
	case engine.PhaseOmnibox:
		maxVisible := appearance.MaxVisibleResults
		if maxVisible <= 0 {
			maxVisible = 8
		}
		maxVisible = min(max(maxVisible, minVisibleResults), maxVisibleResults)
		// Header, search row, footer, and the 16 DIP breathing room around the
		// list are chrome; each result row also gets a 4 DIP grid gap.
		h := int(headerHeight + searchRowHeight + footerHeight + omniboxChrome +
			float32(maxVisible)*(rowHeightOmnibox+rowGap))
		if h < 320 {
			h = 320
		}
		if h > 720 {
			h = 720
		}
		return w, h
	case engine.PhaseAIStreaming:
		return w, 520
	default:
		return w, 420
	}
}

// PreferredCapsuleSizeForState computes Capsule dimensions taking multi-turn AI history into account.
func PreferredCapsuleSizeForState(state engine.State, appearance config.AppearanceConfig) (width, height int) {
	w, h := PreferredCapsuleSize(state.Phase, appearance)
	if state.Phase == engine.PhaseOmnibox && state.SelectedIndex >= 0 && state.SelectedIndex < len(state.OmniboxItems) {
		item := state.OmniboxItems[state.SelectedIndex]
		if item.ClipboardMedia != nil && item.ClipboardMedia.IsMedia() && (item.ClipboardMedia.Kind == "image" || item.ClipboardMedia.Kind == "video") {
			height += int(mediaCanvasHeight - rowHeightOmnibox)
			if height > 860 {
				height = 860
			}
		}
	}
	if state.Phase == engine.PhaseAIStreaming && len(state.AIHistory) > 0 && h < 580 {
		h = 580
	}
	if state.Phase == engine.PhaseLeader {
		h = leaderHeightFor(len(state.LeaderNodes))
	}
	return w, h
}

// Omnibox row metrics: the list's small grid gap is kept here because it is
// specific to the sizing pitch rather than a shared view token.
const (
	rowGap        float32 = 4  // breathing room between adjacent Omnibox result rows
	omniboxChrome float32 = 16 // list padding and dividers not covered by row tokens
)

// Leader menu metrics: the chrome surrounds a two-column grid of cards. Keep
// each term named so a card or footer density change has an obvious derivation.
const (
	leaderCardHeight   = 52           // card content and vertical padding in the Leader grid
	leaderGridGap      = int(space8)  // grid gap between the two-column Leader cards
	leaderDivider      = 1            // each divider consumes one DIP in the menu chrome
	leaderBodyPadding  = int(space12) // padding between the header and card grid
	leaderFooterGap    = int(space16) // breathing room before the footer row
	leaderFooterHeight = int(space20) // compact footer row containing Leader key hints
	leaderChromeHeight = int(headerHeight) + leaderDivider + leaderBodyPadding +
		leaderDivider + leaderFooterGap + leaderFooterHeight + int(space16) // header, dividers, padding and footer spacing
	leaderRowHeight = leaderCardHeight + leaderGridGap // one card plus its grid gap
	leaderMinHeight = 172                              // minimum that keeps the header, one card row and footer usable
	leaderMaxHeight = 480                              // maximum before a long Leader menu relies on scrolling
)

// leaderHeightFor fits the Leader menu to its cards (two per row) instead of
// a fixed height, so a short menu doesn't trail an empty half panel and a
// long one grows until it scrolls.
func leaderHeightFor(nodes int) int {
	rows := (nodes + 1) / 2
	if rows < 1 {
		rows = 1
	}
	h := leaderChromeHeight + rows*leaderRowHeight
	return min(max(h, leaderMinHeight), leaderMaxHeight)
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

// PreferredCandidateBarSize returns bounded dimensions for the candidate
// strip. Counts are clamped so malformed engine state cannot create an
// off-screen surface.
func PreferredCandidateBarSize(candidateCount int, orientation string, hasQuickRoute bool) (width, height int) {
	if candidateCount < 0 {
		candidateCount = 0
	}
	if candidateCount > 9 {
		candidateCount = 9
	}
	quick := 0
	if hasQuickRoute {
		quick = 1
	}
	if strings.EqualFold(orientation, "vertical") {
		height = 48 + candidateCount*38 + quick*38
		if height < 56 {
			height = 56
		}
		if height > 460 {
			height = 460
		}
		return 92, height
	}
	width = 24 + (candidateCount+quick)*54
	if width < 92 {
		width = 92
	}
	if width > 620 {
		width = 620
	}
	return width, 54
}

// PreferredPromptComposerSize sizes the prompt editor and its optional AI
// optimization preview while keeping it usable on compact displays.
func PreferredPromptComposerSize(slotCount int, showOptimizePreview bool) (width, height int) {
	if slotCount < 1 {
		slotCount = 1
	}
	if slotCount > 8 {
		slotCount = 8
	}
	width = 420 + slotCount*34
	if showOptimizePreview {
		width += 180
	}
	if width > 960 {
		width = 960
	}
	height = 106 + slotCount*10
	if showOptimizePreview {
		height += 72
	}
	if height > 360 {
		height = 360
	}
	return width, height
}

// PreferredSelectionBarSize returns a compact, screen-clamped action bar.
func PreferredSelectionBarSize(actionCount int) (width, height int) {
	if actionCount < 0 {
		actionCount = 0
	}
	if actionCount > 8 {
		actionCount = 8
	}
	width = 32 + actionCount*72
	if width < 120 {
		width = 120
	}
	if width > 680 {
		width = 680
	}
	return width, 48
}
