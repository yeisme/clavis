package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func inputEngineIssue(issues []ValidationIssue, severity ValidationSeverity) (ValidationIssue, bool) {
	for _, issue := range issues {
		if issue.KeyPath == "input_engine.mode" && issue.Severity == severity {
			return issue, true
		}
	}
	return ValidationIssue{}, false
}

func TestDecodeLegacyInputEngineModeMigratesToInlineHook(t *testing.T) {
	for _, legacy := range []string{"native_imk", "dual"} {
		cfg, err := DecodeMainConfig("[input_engine]\nmode = \"" + legacy + "\"\n")
		if err != nil {
			t.Fatalf("decode %s: %v", legacy, err)
		}
		if cfg.InputEngine.Mode != "inline_hook" {
			t.Fatalf("decode %s mode = %q, want inline_hook", legacy, cfg.InputEngine.Mode)
		}
		issue, ok := inputEngineIssue(ValidateMain(cfg), SeverityWarning)
		if !ok || !strings.Contains(issue.Message, legacy) || !strings.Contains(issue.Message, "migrated") {
			t.Fatalf("decode %s warning = %#v, want stable legacy migration warning", legacy, issue)
		}
		if strings.Contains(cfg.Encode(), "native_imk") || strings.Contains(cfg.Encode(), "mode = \"dual\"") {
			t.Fatalf("encoded migrated config retained legacy mode:\n%s", cfg.Encode())
		}
	}
}

func TestInputEngineLegacyFieldsRemainParseCompatible(t *testing.T) {
	cfg, err := DecodeMainConfig("[input_engine]\nmixed_english = false\nauto_pair_punctuation = false\n")
	if err != nil {
		t.Fatalf("legacy input engine fields must remain parse-compatible: %v", err)
	}
	if strings.Contains(cfg.Encode(), "mixed_english") || strings.Contains(cfg.Encode(), "auto_pair_punctuation") {
		t.Fatalf("legacy fields must not be persisted:\n%s", cfg.Encode())
	}
}

func TestInputEngineModeValidationDoesNotAcceptLegacyDirectValues(t *testing.T) {
	for _, mode := range []string{"native_imk", "dual", "tsf", "other"} {
		cfg := DefaultMainConfig()
		cfg.InputEngine.Mode = mode
		if _, ok := inputEngineIssue(ValidateMain(cfg), SeverityError); !ok {
			t.Fatalf("direct mode %q must be a validation error", mode)
		}
	}
}

func TestInputEngineSocketWarning(t *testing.T) {
	cfg, err := DecodeMainConfig("[input_engine]\nimk_socket_path = \"/tmp/clavis-imk.sock\"\n")
	if err != nil {
		t.Fatalf("decode socket path: %v", err)
	}
	var found bool
	for _, issue := range ValidateMain(cfg) {
		if issue.KeyPath == "input_engine.imk_socket_path" {
			found = true
			if issue.Severity != SeverityWarning || !strings.Contains(issue.Message, "not wired") || !strings.Contains(issue.Message, "inline_hook") {
				t.Fatalf("socket warning = %#v", issue)
			}
		}
	}
	if !found {
		t.Fatal("expected warning for nonempty input_engine.imk_socket_path")
	}
}

func TestConfigStoreReloadReportsLegacyInputEngineWarning(t *testing.T) {
	dir := t.TempDir()
	store := NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}
	mainPath := filepath.Join(dir, string(FileMain))
	main, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	updated := strings.Replace(string(main), `mode = "inline_hook"`, `mode = "native_imk"`, 1)
	if updated == string(main) {
		t.Fatal("default config did not contain input_engine.mode")
	}
	if err := os.WriteFile(mainPath, []byte(updated), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if !store.Reload() {
		t.Fatalf("Reload failed: %v", store.LastErrors())
	}
	warnings := store.LastWarnings()
	count := 0
	for _, warning := range warnings {
		if warning.KeyPath == "input_engine.mode" {
			count++
			if warning.Severity != SeverityWarning || !strings.Contains(warning.Message, "native_imk") {
				t.Fatalf("legacy mode warning = %#v", warning)
			}
		}
	}
	if count != 1 {
		t.Fatalf("input_engine.mode warning count = %d, want 1; warnings=%#v", count, warnings)
	}
	if got := store.Current().Main.InputEngine.Mode; got != "inline_hook" {
		t.Fatalf("reloaded mode = %q, want inline_hook", got)
	}
	if err := store.SaveMain(store.Current().Main); err != nil {
		t.Fatalf("SaveMain migrated config: %v", err)
	}
	saved, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if strings.Contains(string(saved), "native_imk") || strings.Contains(string(saved), `mode = "dual"`) {
		t.Fatalf("SaveMain persisted a legacy mode:\n%s", saved)
	}
}

func TestInputEngineUnknownModeReloadFails(t *testing.T) {
	dir := t.TempDir()
	store := NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}
	mainPath := filepath.Join(dir, string(FileMain))
	main, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	updated := strings.Replace(string(main), `mode = "inline_hook"`, `mode = "tsf"`, 1)
	if err := os.WriteFile(mainPath, []byte(updated), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if store.Reload() {
		t.Fatal("Reload accepted unknown input engine mode")
	}
	if len(store.LastErrors()) == 0 || store.LastErrors()[0].KeyPath != "input_engine.mode" {
		t.Fatalf("LastErrors = %#v, want input_engine.mode", store.LastErrors())
	}
}
