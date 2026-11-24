package config_test

import (
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

func TestClipboardShortcutLegacyDefaultAndRoundTrip(t *testing.T) {
	legacy := "[trigger]\nleader_key = \"right_option\"\ncapsule_shortcut = \"option+space\"\n"
	decoded, err := config.DecodeMainConfig(legacy)
	if err != nil {
		t.Fatalf("decode legacy trigger failed: %v", err)
	}
	if decoded.Trigger.ClipboardShortcut != "alt+shift+c" {
		t.Fatalf("missing clipboard_shortcut should default to alt+shift+c, got %q", decoded.Trigger.ClipboardShortcut)
	}
	encoded := decoded.Encode()
	if !strings.Contains(encoded, `clipboard_shortcut = "alt+shift+c"`) {
		t.Fatalf("encode must write clipboard_shortcut back:\n%s", encoded)
	}

	decoded.Trigger.ClipboardShortcut = "ctrl+alt+v"
	rt, err := config.DecodeMainConfig(decoded.Encode())
	if err != nil {
		t.Fatalf("round-trip decode failed: %v", err)
	}
	if rt.Trigger.ClipboardShortcut != "ctrl+alt+v" {
		t.Fatalf("round-trip clipboard_shortcut = %q", rt.Trigger.ClipboardShortcut)
	}

	if !strings.Contains(config.DefaultContent(config.FileMain), `clipboard_shortcut = "alt+shift+c"`) {
		t.Fatal("generated default main.toml must include clipboard_shortcut")
	}
}

func TestClipboardShortcutValidation(t *testing.T) {
	cfg := config.DefaultMainConfig()
	if issues := config.ValidateMain(cfg); len(issues) != 0 {
		t.Fatalf("default config must validate clean, got %+v", issues)
	}
	cfg.Trigger.ClipboardShortcut = ""
	for _, is := range config.ValidateMain(cfg) {
		if is.KeyPath == "trigger.clipboard_shortcut" {
			t.Fatalf("empty clipboard_shortcut means disabled and must not error: %+v", is)
		}
	}
	cfg.Trigger.ClipboardShortcut = "alt+shift+nope"
	found := false
	for _, is := range config.ValidateMain(cfg) {
		if is.KeyPath == "trigger.clipboard_shortcut" && is.Severity == config.SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatal("expected error for invalid clipboard_shortcut")
	}
}

func TestClipboardSensitiveTTLSecondsDefaultRoundTripAndClamp(t *testing.T) {
	legacy := "[clipboard]\nenabled = true\n"
	decoded, err := config.DecodeMainConfig(legacy)
	if err != nil {
		t.Fatalf("decode legacy clipboard failed: %v", err)
	}
	if decoded.Clipboard.SensitiveTTLSeconds != config.DefaultSensitiveCopyTTLSeconds {
		t.Fatalf("legacy sensitive_ttl_seconds = %d, want %d", decoded.Clipboard.SensitiveTTLSeconds, config.DefaultSensitiveCopyTTLSeconds)
	}

	decoded.Clipboard.SensitiveTTLSeconds = 90
	encoded := decoded.Encode()
	if !strings.Contains(encoded, "sensitive_ttl_seconds = 90") {
		t.Fatalf("encode missing sensitive_ttl_seconds = 90:\n%s", encoded)
	}
	rt, err := config.DecodeMainConfig(encoded)
	if err != nil {
		t.Fatalf("round-trip decode failed: %v", err)
	}
	if rt.Clipboard.SensitiveTTLSeconds != 90 {
		t.Fatalf("round-trip sensitive_ttl_seconds = %d, want 90", rt.Clipboard.SensitiveTTLSeconds)
	}

	// Out-of-range values clamp cleanly into [5..300] (<=0 defaults to 30).
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"[clipboard]\nsensitive_ttl_seconds = 1\n", config.MinSensitiveCopyTTLSeconds},
		{"[clipboard]\nsensitive_ttl_seconds = 999\n", config.MaxSensitiveCopyTTLSeconds},
		{"[clipboard]\nsensitive_ttl_seconds = 0\n", config.DefaultSensitiveCopyTTLSeconds},
	} {
		got, err := config.DecodeMainConfig(tc.raw)
		if err != nil {
			t.Fatalf("decode %q failed: %v", tc.raw, err)
		}
		if got.Clipboard.SensitiveTTLSeconds != tc.want {
			t.Fatalf("decode %q -> sensitive_ttl_seconds=%d, want %d", tc.raw, got.Clipboard.SensitiveTTLSeconds, tc.want)
		}
	}
}

func TestTerminalConfigDefaultAppleTerminalArgTemplateAndLegacyMigration(t *testing.T) {
	def := config.DefaultTerminalConfig()
	foundTerminal := false
	for _, p := range def.Profiles {
		if p.BundleID == "com.apple.Terminal" {
			foundTerminal = true
			if p.ArgTemplate != "{dir}" {
				t.Fatalf("default Apple Terminal ArgTemplate = %q, want {dir}", p.ArgTemplate)
			}
		}
	}
	if !foundTerminal {
		t.Fatal("default terminal config missing com.apple.Terminal profile")
	}

	legacy := `
[terminal]
default = "terminal"

[[terminal.profiles]]
name = "terminal"
bundle_id = "com.apple.Terminal"
command = "Terminal"
arg_template = "--working-directory={dir}"
`
	decoded, err := config.DecodeMainConfig(legacy)
	if err != nil {
		t.Fatalf("decode legacy terminal config: %v", err)
	}
	if len(decoded.Terminal.Profiles) != 1 || decoded.Terminal.Profiles[0].ArgTemplate != "{dir}" {
		t.Fatalf("legacy Apple Terminal template was not migrated: %+v", decoded.Terminal.Profiles)
	}
}
