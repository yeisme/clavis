package uiapp

import (
	"image"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
)

func TestSettingsAppearanceStageControlsPersist(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.setSettingsSection(settingsSectionAppearance)
	tt := ui.NewTester(ctrl.SettingsView, 960, 900)
	tt.Frame()
	scrollSettingsTo(t, tt, "Visual Preset")

	if err := tt.Click("Classic"); err != nil {
		t.Fatalf("click Classic: %v", err)
	}
	tt.Frame()
	if got := ctrl.Config().Main.Appearance.VisualPreset; got != "classic" {
		t.Fatalf("live visual preset = %q", got)
	}

	scrollSettingsTo(t, tt, "Oblique Rows")
	if err := tt.Click("Oblique Rows"); err != nil {
		t.Fatalf("click Oblique Rows: %v", err)
	}
	tt.Frame()
	if !ctrl.Config().Main.Appearance.ObliqueRows {
		t.Fatal("oblique rows stayed disabled")
	}

	setNamedSlider(t, tt, "Stage Intensity", 0.001)
	setNamedSlider(t, tt, "Glass Clarity", 1)

	appearance := ctrl.Config().Main.Appearance
	if appearance.StageIntensity != 0 || appearance.GlassClarity != 1 {
		t.Fatalf("live appearance = %+v", appearance)
	}
	body := readConfigFile(t, ctrl, config.FileMain)
	for _, want := range []string{
		`visual_preset = "classic"`,
		"stage_intensity = 0",
		"oblique_rows = true",
		"glass_clarity = 1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("config.toml missing %q:\n%s", want, body)
		}
	}

	plan := resolveStagePlan(stageInputs{
		Preset:      appearance.VisualPreset,
		Animations:  "full",
		Intensity:   appearance.StageIntensity,
		ObliqueRows: appearance.ObliqueRows,
		Accelerated: true,
	})
	if plan.Yaw != 0 {
		t.Fatalf("yaw = %v after stage intensity 0", plan.Yaw)
	}
}

func TestSettingsAppearanceStageClampsPastRange(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.setSettingsSection(settingsSectionAppearance)
	tt := ui.NewTester(ctrl.SettingsView, 960, 900)
	tt.Frame()
	setNamedSlider(t, tt, "Stage Intensity", 2)
	setNamedSlider(t, tt, "Glass Clarity", 2)

	appearance := ctrl.Config().Main.Appearance
	if appearance.StageIntensity != 1 || appearance.GlassClarity != 1 {
		t.Fatalf("clamped appearance = %+v", appearance)
	}
	body := readConfigFile(t, ctrl, config.FileMain)
	for _, want := range []string{"stage_intensity = 1.0", "glass_clarity = 1.0"} {
		if !strings.Contains(body, want) {
			t.Fatalf("config.toml missing %q:\n%s", want, body)
		}
	}
}

func TestSettingsAppearanceStageRendersMinimumWindow(t *testing.T) {
	for _, dark := range []bool{false, true} {
		ctrl, _, _, _ := newTestController(t, nil)
		ctrl.setSettingsSection(settingsSectionAppearance)
		tt := ui.NewTesterWith(ctrl.SettingsView, 760, 500, ui.TesterOptions{Dark: dark})
		tt.Frame()
		img := tt.Image()
		if img == nil || img.Bounds().Empty() || uniformImage(img) {
			t.Fatalf("dark=%v rendered an empty settings window", dark)
		}
	}
}

func scrollSettingsTo(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	for range 8 {
		if info, ok := tt.Access(label); ok && !info.Offscreen {
			return
		}
		tt.Scroll(400, 250, 0, -240)
		tt.Frame()
	}
	t.Fatalf("could not bring %q on screen", label)
}

func setNamedSlider(t *testing.T, tt *ui.Tester, label string, fraction float32) {
	t.Helper()
	scrollSettingsTo(t, tt, label)
	info, ok := tt.AccessByRole(ui.RoleSlider, label)
	if !ok {
		t.Fatalf("slider %q missing", label)
	}
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	// Keep the click inside the padded content track: the extremes of the
	// accessible bounds are outside the area that changes the value.
	x := info.Bounds.X + 8 + (info.Bounds.W-16)*fraction
	tt.ClickAt(x, info.Bounds.Y+info.Bounds.H/2)
	tt.Frame()
}

func uniformImage(img *image.RGBA) bool {
	if len(img.Pix) < 4 {
		return true
	}
	for i := 4; i < len(img.Pix); i++ {
		if img.Pix[i] != img.Pix[i%4] {
			return false
		}
	}
	return true
}
