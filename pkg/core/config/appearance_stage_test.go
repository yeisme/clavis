package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageIntensityDefaultsWhenAppearanceAbsent(t *testing.T) {
	cfg, err := DecodeMainConfig("")
	if err != nil {
		t.Fatalf("decode empty config: %v", err)
	}
	if cfg.Appearance.StageIntensity != 0.7 {
		t.Fatalf("StageIntensity = %v, want 0.7", cfg.Appearance.StageIntensity)
	}
	if !cfg.Appearance.ObliqueRows {
		t.Fatal("ObliqueRows = false, want true by default")
	}
}

func TestStageIntensityExplicitValues(t *testing.T) {
	input := `
[appearance]
stage_intensity = 0.25
oblique_rows = false
`
	cfg, err := DecodeMainConfig(input)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Appearance.StageIntensity != 0.25 {
		t.Fatalf("StageIntensity = %v, want 0.25", cfg.Appearance.StageIntensity)
	}
	if cfg.Appearance.ObliqueRows {
		t.Fatal("ObliqueRows = true, want false")
	}
	// A key that is present must not disturb the other appearance defaults.
	if cfg.Appearance.Theme != "system" || cfg.Appearance.CapsuleWidth != 680.0 {
		t.Fatalf("explicit stage keys disturbed other fields: %+v", cfg.Appearance)
	}
}

// TestStageIntensityLegacyConfigStillLoads checks that a config.toml written
// before stage_intensity and oblique_rows existed loads unchanged: every other
// field keeps its value and the two new knobs quietly take their defaults.
func TestStageIntensityLegacyConfigStillLoads(t *testing.T) {
	legacy := `[trigger]
leader_key = "right_alt"
double_tap_key = "right_win"
double_tap_interval_ms = 450
capsule_shortcut = "ctrl+space"
clipboard_shortcut = "ctrl+shift+v"
snippet_expansion_enabled = false
snippet_backspace_undo = false
leader_reveal_delay_ms = 100

[keymap]
enabled = false
tap_hold_threshold_ms = 120
caps_lock_tap = "escape"
caps_lock_hold = "nav"
nav_layer_enabled = false

[appearance]
theme = "dark"
language = "zh-CN"
animations = "reduced"
capsule_width = 540.0
max_visible_results = 5
corner_radius = 8.0
show_key_hints = false
show_preview_pane = false
hud_duration_ms = 900
visual_preset = "classic"
enable_3d_effects = false
enable_shaders = false
glass_clarity = 0.5
candidate_bar_orientation = "vertical"

[ai]
active_profile_id = "deepseek"
provider = "deepseek"
protocol = "openai_compatible"
base_url = "https://api.deepseek.com/v1"
model = "deepseek-chat"
temperature = 0.4
timeout_seconds = 15.0
system_prompt = "be brief"
keychain_account = "clavis.ai.deepseek"
api_key_env_var = "DEEPSEEK_API_KEY"

[clipboard]
enabled = false
max_history_items = 50
retention_days = 7
ignore_concealed_types = false
media_capture = false
media_quota_mb = 64

[ime]
enabled = false
default_input_source_id = "com.apple.keylayout.ABC"
show_indicator_hud = false
switch_delay_ms = 10
lock_ascii_on_leader = false
show_floating_pill = false

[input_engine]
enabled = false
mode = "off"
scheme = "quanpin"
candidate_count = 5
shift_toggle_ascii = false
user_learning = false
imk_socket_path = ""

[selection_bar]
enabled = false
trigger_mode = "disabled"

[daemon]
idle_compact_seconds = 120
max_memory_mb = 32
watchdog_interval_ms = 1000
in_place_optimize_shortcut = "option+tab"
`
	cfg, err := DecodeMainConfig(legacy)
	if err != nil {
		t.Fatalf("a pre-existing config.toml no longer loads: %v", err)
	}
	if cfg.Appearance.StageIntensity != 0.7 || !cfg.Appearance.ObliqueRows {
		t.Fatalf("missing keys should take defaults, got intensity=%v oblique=%v",
			cfg.Appearance.StageIntensity, cfg.Appearance.ObliqueRows)
	}
	a := cfg.Appearance
	if a.Theme != "dark" || a.Language != "zh-CN" || a.Animations != "reduced" ||
		a.CapsuleWidth != 540.0 || a.MaxVisibleResults != 5 || a.CornerRadius != 8.0 ||
		a.ShowKeyHints || a.ShowPreviewPane || a.HUDDurationMs != 900 ||
		a.VisualPreset != "classic" || a.Enable3DEffects || a.EnableShaders ||
		a.GlassClarity != 0.5 || a.CandidateBarOrientation != "vertical" {
		t.Fatalf("legacy appearance fields were touched: %+v", a)
	}
	if cfg.Trigger.LeaderKey != "right_alt" || cfg.Trigger.DoubleTapIntervalMs != 450 ||
		cfg.Keymap.TapHoldThresholdMs != 120 || cfg.AI.Temperature != 0.4 ||
		cfg.Clipboard.MaxHistoryItems != 50 || cfg.IME.SwitchDelayMs != 10 ||
		cfg.InputEngine.Mode != "off" || cfg.InputEngine.CandidateCount != 5 ||
		cfg.SelectionBar.TriggerMode != "disabled" || cfg.Daemon.IdleCompactSeconds != 120 {
		t.Fatalf("legacy non-appearance fields were touched: %+v", cfg)
	}
	if issues := ValidateMain(cfg); len(issues) != 0 {
		t.Fatalf("a legacy config with the new defaults must validate clean, got %+v", issues)
	}
}

func TestStageIntensityRoundTrip(t *testing.T) {
	cases := []struct {
		intensity float64
		oblique   bool
	}{
		{0, false},
		{1, true},
		{0.25, false},
		{0.7, true},
	}
	for _, tc := range cases {
		cfg := DefaultMainConfig()
		cfg.Appearance.StageIntensity = tc.intensity
		cfg.Appearance.ObliqueRows = tc.oblique
		decoded, err := DecodeMainConfig(cfg.Encode())
		if err != nil {
			t.Fatalf("round trip intensity=%v oblique=%v: %v", tc.intensity, tc.oblique, err)
		}
		if decoded.Appearance.StageIntensity != tc.intensity || decoded.Appearance.ObliqueRows != tc.oblique {
			t.Fatalf("round trip intensity=%v oblique=%v came back as %v / %v",
				tc.intensity, tc.oblique, decoded.Appearance.StageIntensity, decoded.Appearance.ObliqueRows)
		}
	}
}

// TestStageIntensityClampAndWarn drives the real load pipeline: decode keeps
// the raw value so ValidateMain can warn about it, and ConfigStore.Reload
// clamps the stored config afterwards. Testing the two halves separately once
// hid a bug where decode clamped early and the warning never fired in
// production.
func TestStageIntensityClampAndWarn(t *testing.T) {
	cases := []struct {
		literal string
		want    float64
	}{
		{"1.8", 1.0},
		{"-0.2", 0.0},
		{"nan", 0.7},
		{"inf", 1.0},
		{"-inf", 0.0},
	}
	for _, tc := range cases {
		input := "[appearance]\nstage_intensity = " + tc.literal + "\n"
		cfg, err := DecodeMainConfig(input)
		if err != nil {
			t.Fatalf("decode stage_intensity = %s: %v", tc.literal, err)
		}
		// Decode must not clamp: the validator has to see the offending value.
		if cfg.Appearance.StageIntensity == tc.want && tc.literal != "nan" &&
			NormalizeStageIntensity(cfg.Appearance.StageIntensity) == cfg.Appearance.StageIntensity &&
			outOfRangeLiteral(tc.literal) {
			t.Fatalf("stage_intensity = %s was clamped during decode, so no warning can fire", tc.literal)
		}

		issues := ValidateMain(cfg)
		warned := false
		for _, issue := range issues {
			if issue.KeyPath != "appearance.stage_intensity" {
				continue
			}
			if issue.Severity != SeverityWarning {
				t.Fatalf("stage_intensity = %s severity = %s, want warning", tc.literal, issue.Severity)
			}
			if !strings.Contains(issue.Message, "stage_intensity") || !strings.Contains(issue.Message, "clamped to") {
				t.Fatalf("warning for %s does not name the key and the clamp: %q", tc.literal, issue.Message)
			}
			warned = true
		}
		if !warned {
			t.Fatalf("stage_intensity = %s produced no warning, got %+v", tc.literal, issues)
		}
		for _, issue := range issues {
			if issue.Severity == SeverityError {
				t.Fatalf("stage_intensity = %s must not error, got %+v", tc.literal, issues)
			}
		}

		// The running config is clamped even though the file keeps its value.
		if got := NormalizeStageIntensity(cfg.Appearance.StageIntensity); got != tc.want {
			t.Fatalf("stage_intensity = %s normalizes to %v, want %v", tc.literal, got, tc.want)
		}
	}
}

// outOfRangeLiteral reports whether a test literal names a value outside 0..1.
func outOfRangeLiteral(literal string) bool {
	return literal == "1.8" || literal == "-0.2" || literal == "inf" || literal == "-inf"
}

func TestStageIntensityBoundariesAccepted(t *testing.T) {
	for _, literal := range []string{"0", "0.0", "1", "1.0"} {
		input := "[appearance]\nstage_intensity = " + literal + "\n"
		cfg, err := DecodeMainConfig(input)
		if err != nil {
			t.Fatalf("decode stage_intensity = %s: %v", literal, err)
		}
		if cfg.Appearance.StageIntensity != 0 && cfg.Appearance.StageIntensity != 1 {
			t.Fatalf("stage_intensity = %s decoded to %v", literal, cfg.Appearance.StageIntensity)
		}
		if issues := ValidateMain(cfg); len(issues) != 0 {
			t.Fatalf("stage_intensity = %s must validate clean, got %+v", literal, issues)
		}
	}
}

func TestNormalizeStageIntensity(t *testing.T) {
	if got := NormalizeStageIntensity(math.NaN()); got != 0.7 {
		t.Fatalf("NaN -> %v, want 0.7", got)
	}
	if got := NormalizeStageIntensity(math.Inf(1)); got != 1 {
		t.Fatalf("+Inf -> %v, want 1", got)
	}
	if got := NormalizeStageIntensity(math.Inf(-1)); got != 0 {
		t.Fatalf("-Inf -> %v, want 0", got)
	}
	if got := NormalizeStageIntensity(1.8); got != 1 {
		t.Fatalf("1.8 -> %v, want 1", got)
	}
	if got := NormalizeStageIntensity(-0.2); got != 0 {
		t.Fatalf("-0.2 -> %v, want 0", got)
	}
	if got := NormalizeStageIntensity(0); got != 0 {
		t.Fatalf("0 -> %v, want 0", got)
	}
	if got := NormalizeStageIntensity(1); got != 1 {
		t.Fatalf("1 -> %v, want 1", got)
	}
}

// TestStageIntensityWarningSurvivesTheStorePath is the end-to-end proof that an
// out-of-range stage_intensity in a real config.toml both warns and is clamped
// in the running config. Before the fix, decode clamped first, so the warning
// existed only in a unit test that re-injected the raw value by hand.
func TestStageIntensityWarningSurvivesTheStorePath(t *testing.T) {
	dir := t.TempDir()
	store := NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	// Overwrite the generated config with an out-of-range intensity, then reload.
	path := filepath.Join(dir, string(FileMain))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	patched := strings.Replace(string(data),
		"stage_intensity = 0.7", "stage_intensity = 1.8", 1)
	if patched == string(data) {
		t.Fatal("generated config has no stage_intensity line to patch")
	}
	if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if ok := store.Reload(); !ok {
		t.Fatalf("reload failed: %+v", store.LastErrors())
	}

	if got := store.Current().Main.Appearance.StageIntensity; got != 1 {
		t.Fatalf("running stage_intensity = %v, want clamped 1", got)
	}
	found := false
	for _, w := range store.LastWarnings() {
		if w.KeyPath == "appearance.stage_intensity" && strings.Contains(w.Message, "clamped to") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no stage_intensity warning surfaced, got %+v", store.LastWarnings())
	}
}
