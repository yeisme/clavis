package uiapp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestKitWidgetsRenderInBothThemes(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(map[bool]string{false: "light", true: "dark"}[dark], func(t *testing.T) {
			tt := ui.NewTester(func(c *ui.Context) {
				theme := c.Theme()
				kitHeaderBar(c)
				kitCard(c, theme, cardFlat, func() { ui.Text(c, "Card") })
				kitPanel(c, theme, cardElevated, func() { ui.Text(c, "Panel") })
				kitInsetWell(c, theme, func() { ui.Text(c, "Well") })
				kitDivider(c, theme)
				kitRow(c, theme, interactionState{Selected: true}, motionProfiles[animationOff], func() { ui.Text(c, "Row") })
				kitControlRow(c, theme, "Setting", func() { ui.Text(c, "Value") })
				kitSegmentedPicker(c, theme, []string{"One", "Two"}, "One")
				kitSegmentedTabs(c, theme, []string{"A", "B"}, 0, nil)
				kitChipGroup(c, theme, []string{"One", "Two"})
				kitIconButton(c, theme, "checkmark", true, "Done", nil)
				kitStatusPill(c, theme, "Ready", chipSuccess)
				kitEmptyState(c, theme, "search", "Nothing", "Try another query")
				kitErrorBanner(c, theme, "Something went wrong")
				kitStatusDot(c, theme.Success, true)
			}, int(railWidth+space32), int(headerHeight+space32))
			tt.SetDark(dark)
			tt.Frame()
			if tt.Image() == nil || tt.Image().Bounds().Empty() {
				t.Fatal("kit frame produced an empty image")
			}
		})
	}
}

func TestKitFocusAndErrorContrast(t *testing.T) {
	for _, theme := range []*ui.Theme{ui.LightTheme(), ui.DarkTheme()} {
		if contrastRatio(kitFocusRing(theme), theme.Surface) < minGlyphContrast {
			t.Fatalf("focus ring contrast is too low: %v", contrastRatio(kitFocusRing(theme), theme.Surface))
		}
		background := theme.Danger.Alpha(space2 / space8)
		foreground := readableOn(theme.Danger, theme.Text, background.Over(theme.Surface), minTextContrast)
		if contrastRatio(foreground, background.Over(theme.Surface)) < minTextContrast {
			t.Fatalf("error text contrast is too low: %v", contrastRatio(foreground, background))
		}
	}
}

func TestKitPressDoesNotAnimateWithoutMotion(t *testing.T) {
	profile := motionProfiles[animationReduced]
	tt := ui.NewTester(func(c *ui.Context) {
		kitInteractive(c, c.Theme(), ui.Box(c), interactionState{Pressed: true}, profile)
	}, int(railWidth), int(headerHeight))
	tt.Frame()
	if tt.FramePending() {
		t.Fatal("reduced motion press must not request animation frames")
	}

	tt = ui.NewTester(func(c *ui.Context) {
		kitInteractive(c, c.Theme(), ui.Box(c), interactionState{Pressed: true}, motionProfiles[animationOff])
	}, int(railWidth), int(headerHeight))
	tt.Frame()
	if tt.FramePending() {
		t.Fatal("off motion press must not request animation frames")
	}
}

func TestKitFilesHaveNoRawStyleLiterals(t *testing.T) {
	for _, name := range []string{"kit_surface.go", "kit_controls.go", "kit_header.go", "kit_states.go"} {
		src, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		if debt := countStyleLiterals(string(src)); debt != (styleDebt{}) {
			t.Errorf("%s contains raw style literals: %v", name, debt)
		}
	}
}
