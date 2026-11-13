package uiapp

import (
	"math"
	"strings"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
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

// PreferredPillSizeForLabel sizes the floating non-activating pill for a given label.
// Short IME badges ("EN", "中", <= 3 runes) stay compact, while longer status/layer notices
// such as "LAYER • NAV" or "Bound Notes → ASCII" scale the width up (capped at 340) so the
// text renders without truncation and without the panel ever stealing focus.
func PreferredPillSizeForLabel(label string) (width, height int) {
	trimmed := strings.TrimSpace(label)
	runeCount := utf8.RuneCountInString(trimmed)
	if runeCount <= 3 {
		return pillShortWidth, pillShortHeight
	}
	w := pillTextBasePad + runeCount*pillRuneWidthEst
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
