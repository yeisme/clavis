package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
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
