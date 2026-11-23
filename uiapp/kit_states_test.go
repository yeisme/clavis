package uiapp

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestKitStateSemanticColorsFollowVisualSpec(t *testing.T) {
	light, dark := ui.LightTheme(), ui.DarkTheme()
	wantLightDanger := ui.RGBA(255, 59, 48, 1)
	wantDarkDanger := ui.RGBA(255, 69, 58, 1)
	if kitStateEmptyColor(light) != ui.RGBA(142, 142, 147, 0.75) || kitStateEmptyColor(dark) != ui.RGBA(122, 122, 128, 0.75) {
		t.Fatal("empty state color must use neutral gray in both themes")
	}
	if kitStateLoadingColor(light) != ui.RGBA(10, 132, 255, 1) || kitStateLoadingColor(dark) != ui.RGBA(10, 132, 255, 1) {
		t.Fatal("loading state color must use system blue in both themes")
	}
	if kitStateErrorColor(light) != wantLightDanger || kitStateErrorColor(dark) != wantDarkDanger {
		t.Fatal("error state color must use the theme-specific system red")
	}
}

func TestKitStateViewRendersRetryInBothThemes(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(map[bool]string{false: "light", true: "dark"}[dark], func(t *testing.T) {
			tt := ui.NewTester(func(c *ui.Context) {
				kitStateView(c, c.Theme(), kitStateError, "exclamationmark.circle", "加载失败", "网络暂不可用", func() {})
			}, 320, 220)
			tt.SetDark(dark)
			tt.Frame()
			if !tt.HasText("重试") || !tt.HasText("网络暂不可用") {
				t.Fatalf("error state should expose reason and retry action, got %v", tt.Texts())
			}
		})
	}
}

func TestKitWarningBannerAndInlineErrorRetry(t *testing.T) {
	retried := false
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Gap(space8).Children(func() {
			kitWarningBanner(c, c.Theme(), "Config warning: deprecated key")
			kitInlineLoading(c, c.Theme(), "test-loading", "Loading stats…")
			kitInlineError(c, c.Theme(), "test-err", "Stats unavailable", func() { retried = true })
		})
	}, 420, 180)
	tt.Frame()
	if !tt.HasText("Config warning: deprecated key") || !tt.HasText("Loading stats…") || !tt.HasText("Stats unavailable") {
		t.Fatalf("missing expected state texts: %v", tt.Texts())
	}
	rect, ok := tt.Find("Retry")
	if !ok {
		t.Fatalf("retry button missing: %v", tt.Texts())
	}
	tt.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	if !retried {
		t.Fatal("clicking inline error retry did not invoke callback")
	}
}

func TestOmniboxSearchEmptyClearFilterButton(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	ctrl.clipboardStatsMu.Lock()
	ctrl.clipboardStats.loaded = true
	ctrl.clipboardStats.filter = "image"
	ctrl.clipboardStatsMu.Unlock()
	ctrl.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: "nonexistent-needle-xyz"})

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	btn, ok := tt.Find("Clear filter")
	if !ok {
		t.Fatalf("clear filter button missing on empty search: %v", tt.Texts())
	}
	tt.ClickAt(btn.X+btn.W/2, btn.Y+btn.H/2)
	if ctrl.State().OmniboxQuery != "" || ctrl.clipboardStatsFilter() != "" {
		t.Fatalf("clear filter failed: query=%q filter=%q", ctrl.State().OmniboxQuery, ctrl.clipboardStatsFilter())
	}
}

func TestStatsBandErrorExposesRetryAction(t *testing.T) {
	retried := false
	tt := ui.NewTester(func(c *ui.Context) {
		renderLexiconStatsBandWithRetry(c, ui.LightTheme(), nil, true, errors.New("db closed"), 10, func() { retried = true })
	}, 360, 72)
	tt.Frame()
	btn, ok := tt.Find("Retry")
	if !ok {
		t.Fatalf("lexicon stats error retry button missing: %v", tt.Texts())
	}
	tt.ClickAt(btn.X+btn.W/2, btn.Y+btn.H/2)
	if !retried {
		t.Fatal("lexicon stats retry callback not invoked")
	}
}
