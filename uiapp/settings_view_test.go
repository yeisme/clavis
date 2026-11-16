package uiapp

import (
	"image"
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
)

// These two tests cover the guarantees the Settings rework promised but did not
// initially test: that the rail still selects every section, and that all eight
// sections render at the window's minimum size in both themes.

// settingsSectionIndex returns the section the controller currently shows.
func settingsSectionIndex(ctrl *AppController) int {
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	return normalizeSettingsSection(ctrl.uiState.settingsTabIdx)
}

// TestSettingsRailSelectsEverySection clicks each rail entry in turn and checks
// that the controller follows and the pane header names the new section. The
// rail was rewritten from the stock ui.Sidebar onto kit primitives, so this is
// the compatibility contract for every caller that drives sections by key.
func TestSettingsRailSelectsEverySection(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()

	sections := settingsSections()
	if len(sections) != 8 {
		t.Fatalf("settings exposes %d sections, want the documented 8", len(sections))
	}
	// Guard the assertion below: if HasText matched anything, every check would
	// pass vacuously.
	if tt.HasText("§ no such settings section §") {
		t.Fatal("HasText matched nonsense, so the header assertions would be vacuous")
	}
	// Start somewhere that is not the first entry so every click is a change.
	ctrl.setSettingsSection(sections[len(sections)-1].Index)
	tt.Frame()

	for _, meta := range sections {
		openSettingsSection(t, tt, meta.Label)

		if got := settingsSectionIndex(ctrl); got != meta.Index {
			t.Fatalf("clicking %q selected section %d, want %d", meta.Label, got, meta.Index)
		}
		// The header renders "<label> — <hint>"; assert the pane really swapped
		// rather than just the stored index changing.
		if !tt.HasText(meta.Hint) {
			t.Fatalf("pane header for %q does not show its hint %q", meta.Label, meta.Hint)
		}
	}
}

// TestSettingsSectionsRenderAtMinimumSize renders every section at the window's
// minimum size (760x500) in both themes. A card-based layout that overflows or
// collapses shows up as a uniform or empty image, so this catches the two
// failure modes the rework could plausibly introduce without asserting pixels.
func TestSettingsSectionsRenderAtMinimumSize(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)

	for _, dark := range []bool{false, true} {
		mode := "light"
		if dark {
			mode = "dark"
		}
		t.Run(mode, func(t *testing.T) {
			for _, meta := range settingsSections() {
				ctrl.setSettingsSection(meta.Index)
				tt := ui.NewTester(ctrl.SettingsView, 760, 500)
				tt.SetDark(dark)
				tt.Frame()
				tt.Frame()

				img := tt.Image()
				if img == nil || img.Bounds().Empty() {
					t.Fatalf("%s: section %q rendered no image", mode, meta.Label)
				}
				// A real pane draws a rail, cards, dividers and text; a collapsed
				// or clipped-to-nothing pane draws one flat colour.
				if shades := distinctShades(img); shades < 8 {
					t.Fatalf("%s: section %q rendered only %d distinct colours — pane looks empty",
						mode, meta.Label, shades)
				}
				if !tt.HasText(meta.Hint) {
					t.Fatalf("%s: section %q lost its pane header at minimum size", mode, meta.Label)
				}
			}
		})
	}
}

// distinctShades counts the distinct RGB values in an image, sampled on a grid
// so a large surface stays cheap to inspect.
func distinctShades(img *image.RGBA) int {
	seen := map[color.RGBA]struct{}{}
	b := img.Bounds()
	for y := 0; y < b.Max.Y; y += 4 {
		for x := 0; x < b.Max.X; x += 4 {
			seen[img.RGBAAt(x, y)] = struct{}{}
		}
	}
	return len(seen)
}
