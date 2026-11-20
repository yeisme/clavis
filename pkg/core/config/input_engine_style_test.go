package config

import (
	"math"
	"strings"
	"testing"
)

func styleIssues(s InputEngineStyleConfig) map[string]ValidationSeverity {
	main := DefaultMainConfig()
	main.InputEngine.Style = s
	out := map[string]ValidationSeverity{}
	for _, issue := range ValidateMain(main) {
		if strings.HasPrefix(issue.KeyPath, "input_engine.style.") {
			out[issue.KeyPath] = issue.Severity
		}
	}
	return out
}

func TestInputEngineStyleDefaults(t *testing.T) {
	got := DefaultMainConfig().InputEngine.Style
	if got != (InputEngineStyleConfig{Preset: CandidateStyleNative, ShowPageIndicator: true}) {
		t.Fatalf("default style = %+v", got)
	}
	// No [input_engine.style] table at all: the defaults, not a zero struct.
	cfg, err := DecodeMainConfig("[input_engine]\nenabled = true\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InputEngine.Style != got {
		t.Fatalf("missing table decoded to %+v", cfg.InputEngine.Style)
	}
	// The generated main.toml documents the section and decodes to the same defaults.
	content := DefaultContent(FileMain)
	if !strings.Contains(content, "[input_engine.style]") {
		t.Fatal("generated main.toml has no [input_engine.style]")
	}
	fromTemplate, err := DecodeMainConfig(content)
	if err != nil {
		t.Fatal(err)
	}
	if fromTemplate.InputEngine.Style != got || len(styleIssues(fromTemplate.InputEngine.Style)) != 0 {
		t.Fatalf("template style = %+v", fromTemplate.InputEngine.Style)
	}
}

func TestInputEngineStyleDecodeAndRoundTrip(t *testing.T) {
	cfg, err := DecodeMainConfig(`[input_engine]
enabled = true

[input_engine.style]
preset = "custom"
radius = 14
padding = 8.5
font_size = 17
opacity = 0.85
accent = "#FF9F0A"
selected_style = "underline"
show_page_indicator = false
`)
	if err != nil {
		t.Fatal(err)
	}
	want := InputEngineStyleConfig{Preset: "custom", Radius: 14, Padding: 8.5, FontSize: 17, Opacity: 0.85, Accent: "#FF9F0A", SelectedStyle: "underline", ShowPageIndicator: false}
	if cfg.InputEngine.Style != want {
		t.Fatalf("decoded %+v, want %+v", cfg.InputEngine.Style, want)
	}
	if issues := styleIssues(want); len(issues) != 0 {
		t.Fatalf("valid style reported %v", issues)
	}
	again, err := DecodeMainConfig(cfg.Encode())
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if again.InputEngine.Style != want {
		t.Fatalf("round trip %+v, want %+v", again.InputEngine.Style, want)
	}
	if _, err := DecodeMainConfig("[input_engine.style]\nradius = \"big\"\n"); err == nil {
		t.Fatal("a non-number radius must not decode")
	}
}

func TestInputEngineStyleValidation(t *testing.T) {
	base := DefaultInputEngineStyleConfig()
	for _, tc := range []struct {
		name   string
		mutate func(*InputEngineStyleConfig)
		key    string
		want   ValidationSeverity
	}{
		{"unknown preset", func(s *InputEngineStyleConfig) { s.Preset = "neon" }, "input_engine.style.preset", SeverityError},
		{"unknown selection", func(s *InputEngineStyleConfig) { s.SelectedStyle = "glow" }, "input_engine.style.selected_style", SeverityError},
		{"bad accent", func(s *InputEngineStyleConfig) { s.Accent = "blue" }, "input_engine.style.accent", SeverityError},
		{"radius too large", func(s *InputEngineStyleConfig) { s.Radius = 40 }, "input_engine.style.radius", SeverityWarning},
		{"padding too small", func(s *InputEngineStyleConfig) { s.Padding = 1 }, "input_engine.style.padding", SeverityWarning},
		{"font too large", func(s *InputEngineStyleConfig) { s.FontSize = 30 }, "input_engine.style.font_size", SeverityWarning},
		{"opacity above 1", func(s *InputEngineStyleConfig) { s.Opacity = 1.5 }, "input_engine.style.opacity", SeverityWarning},
		{"NaN font", func(s *InputEngineStyleConfig) { s.FontSize = math.NaN() }, "input_engine.style.font_size", SeverityError},
	} {
		s := base
		tc.mutate(&s)
		if got, ok := styleIssues(s)[tc.key]; !ok || got != tc.want {
			t.Errorf("%s: %s severity = %v (reported %v), want %v", tc.name, tc.key, got, ok, tc.want)
		}
	}
	for _, accent := range []string{"", "#0A84FF", "#fa0", "#30d158"} {
		s := base
		s.Accent = accent
		if issues := styleIssues(s); len(issues) != 0 {
			t.Errorf("accent %q reported %v", accent, issues)
		}
	}
}
