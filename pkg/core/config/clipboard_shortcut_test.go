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
