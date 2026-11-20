package uiapp

import (
	"math"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func TestPreferredCapsuleSizeOmniboxMonotonic(t *testing.T) {
	appearance := config.DefaultAppearanceConfig()
	previous := 0
	for visible := minVisibleResults; visible <= maxVisibleResults; visible++ {
		appearance.MaxVisibleResults = visible
		_, height := PreferredCapsuleSize(engine.PhaseOmnibox, appearance)
		if height <= previous {
			t.Fatalf("visible=%d height=%d did not increase after %d", visible, height, previous)
		}
		previous = height
	}
}

func TestPreferredCapsuleSizeBounds(t *testing.T) {
	appearance := config.DefaultAppearanceConfig()
	for _, visible := range []int{0, -5, 99} {
		appearance.MaxVisibleResults = visible
		_, height := PreferredCapsuleSize(engine.PhaseOmnibox, appearance)
		if height < 320 || height > 720 {
			t.Fatalf("omnibox visible=%d height=%d outside [320,720]", visible, height)
		}
	}
	for _, phase := range []engine.CapsulePhase{
		engine.PhaseHUD, engine.PhaseLeader, engine.PhaseOmnibox,
		engine.PhaseAIStreaming,
	} {
		_, height := PreferredCapsuleSize(phase, appearance)
		switch phase {
		case engine.PhaseHUD:
			if height != 56 {
				t.Fatalf("HUD height=%d, want 56", height)
			}
		case engine.PhaseLeader:
			if height < leaderMinHeight || height > leaderMaxHeight {
				t.Fatalf("Leader height=%d outside [%d,%d]", height, leaderMinHeight, leaderMaxHeight)
			}
		case engine.PhaseOmnibox:
			if height < 320 || height > 720 {
				t.Fatalf("Omnibox height=%d outside [320,720]", height)
			}
		case engine.PhaseAIStreaming:
			if height < 520 {
				t.Fatalf("AI height=%d below 520", height)
			}
		}
	}
}

func TestPreferredCapsuleSizeOmniboxDerivation(t *testing.T) {
	appearance := config.DefaultAppearanceConfig()
	for _, visible := range []int{3, 8, 12} {
		appearance.MaxVisibleResults = visible
		_, got := PreferredCapsuleSize(engine.PhaseOmnibox, appearance)
		want := int(headerHeight + searchRowHeight + footerHeight + omniboxChrome +
			float32(visible)*(rowHeightOmnibox+rowGap))
		want = min(max(want, 320), 720)
		if got != want {
			t.Fatalf("visible=%d height=%d, independently derived %d", visible, got, want)
		}
	}
}

// The Leader menu lists one action per row (see docs/design/leader-panel-visual-research.md),
// so height grows one row at a time rather than two cards at a time.
func TestLeaderHeightFitsRowsForAllCounts(t *testing.T) {
	for nodes := 1; nodes <= 24; nodes++ {
		want := min(max(leaderChromeHeight+nodes*leaderRowHeight, leaderMinHeight), leaderMaxHeight)
		if got := leaderHeightFor(nodes); got != want {
			t.Fatalf("nodes=%d height=%d, want %d", nodes, got, want)
		}
	}
	// Each extra action must add a row's worth of height until the cap is reached.
	if leaderHeightFor(2) <= leaderHeightFor(1) {
		t.Fatal("a second action must make the menu taller")
	}
}

func TestPreferredCapsuleSizeChineseWidthFloor(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	i18n.SetLanguage(i18n.Chinese)
	appearance := config.DefaultAppearanceConfig()
	appearance.CapsuleWidth = 420
	width, _ := PreferredCapsuleSize(engine.PhaseOmnibox, appearance)
	if width != defaultCapsuleWidth+40 {
		t.Fatalf("Chinese width=%d, want %d", width, defaultCapsuleWidth+40)
	}
}

func TestNormalizeCornerRadiusStillClampsNonFinite(t *testing.T) {
	appearance := config.DefaultAppearanceConfig()
	appearance.CornerRadius = math.NaN()
	if got := NormalizeCornerRadius(appearance); got != defaultCornerRadius {
		t.Fatalf("NaN corner radius=%v, want %v", got, defaultCornerRadius)
	}
}
