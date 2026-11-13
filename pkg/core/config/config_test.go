package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

func TestDefaultGeneratorIdempotencyAndValidity(t *testing.T) {
	dir := t.TempDir()

	created1, err := config.GenerateIfMissing(dir)
	if err != nil {
		t.Fatalf("GenerateIfMissing failed: %v", err)
	}
	if len(created1) != 5 {
		t.Fatalf("expected 5 created files, got %d: %v", len(created1), created1)
	}

	// Modify config.toml to verify subsequent generation preserves user edits
	mainPath := filepath.Join(dir, string(config.FileMain))
	raw, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read main: %v", err)
	}
	marker := "\n# user custom comment\n"
	if err := os.WriteFile(mainPath, append(raw, []byte(marker)...), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}

	created2, err := config.GenerateIfMissing(dir)
	if err != nil {
		t.Fatalf("second GenerateIfMissing failed: %v", err)
	}
	if len(created2) != 0 {
		t.Fatalf("expected 0 created files on second call, got %v", created2)
	}

	after, err := os.ReadFile(mainPath)
	if err != nil || !strings.HasSuffix(string(after), marker) {
		t.Fatalf("user modification was overwritten")
	}

	// Verify all default files decode to DefaultConfig and validate cleanly
	m, err := config.DecodeMainConfig(config.DefaultContent(config.FileMain))
	if err != nil {
		t.Fatalf("decode main: %v", err)
	}
	l, err := config.DecodeLeaderConfig(config.DefaultContent(config.FileLeader))
	if err != nil {
		t.Fatalf("decode leader: %v", err)
	}
	s, err := config.DecodeSnippetsConfig(config.DefaultContent(config.FileSnippets))
	if err != nil {
		t.Fatalf("decode snippets: %v", err)
	}
	g, err := config.DecodeGlossaryConfig(config.DefaultContent(config.FileGlossary))
	if err != nil {
		t.Fatalf("decode glossary: %v", err)
	}
	ime, err := config.DecodeIMERulesConfig(config.DefaultContent(config.FileIMERules))
	if err != nil {
		t.Fatalf("decode ime-rules: %v", err)
	}

	parsed := config.ClavisConfig{Main: m, Leader: l, Snippets: s, Glossary: g, IMERules: ime}
	if !reflect.DeepEqual(parsed, config.DefaultConfig()) {
		t.Fatalf("parsed default TOML does not match DefaultConfig()\nparsed: %+v\ndefault: %+v", parsed, config.DefaultConfig())
	}
	if issues := config.Validate(parsed); len(issues) != 0 {
		t.Fatalf("expected 0 validation issues on default config, got %+v", issues)
	}
}

func TestConfigRejectsRawAPIKeys(t *testing.T) {
	for _, key := range []string{"api_key", "apiKey", "secret_key", "secretKey"} {
		tomlStr := "[ai]\n" + key + " = \"sk-12345\"\n"
		_, err := config.DecodeMainConfig(tomlStr)
		if err == nil {
			t.Fatalf("expected error for forbidden key %q", key)
		}
		var de *config.DecodingError
		if !errors.As(err, &de) {
			t.Fatalf("expected DecodingError for %q, got %T: %v", key, err, err)
		}
		if de.KeyPath != "ai."+key {
			t.Fatalf("expected keyPath ai.%s, got %s", key, de.KeyPath)
		}
	}
}

func TestValidatorRulesAndCrossPlatformModifiers(t *testing.T) {
	if issues := config.Validate(config.DefaultConfig()); len(issues) != 0 {
		t.Fatalf("expected default config valid, got %+v", issues)
	}

	// Boundaries 100 and 800 valid; 99 and 801 invalid
	cfg := config.DefaultConfig()
	cfg.Main.Trigger.DoubleTapIntervalMs = 100
	if issues := config.Validate(cfg); len(issues) != 0 {
		t.Fatalf("100ms should be valid, got %+v", issues)
	}
	cfg.Main.Trigger.DoubleTapIntervalMs = 800
	if issues := config.Validate(cfg); len(issues) != 0 {
		t.Fatalf("800ms should be valid, got %+v", issues)
	}
	for _, bad := range []int{99, 801} {
		cfg.Main.Trigger.DoubleTapIntervalMs = bad
		issues := config.Validate(cfg)
		if !hasIssue(issues, "trigger.double_tap_interval_ms", config.SeverityError) {
			t.Fatalf("expected error for double_tap_interval_ms=%d", bad)
		}
	}

	// Cross-platform modifiers & shortcuts
	cross := config.DefaultMainConfig()
	cross.Trigger.LeaderKey = "right_alt"
	cross.Trigger.DoubleTapKey = "right_win"
	cross.Trigger.CapsuleShortcut = "ctrl+alt+space"
	cross.Appearance.CornerRadius = 0
	cross.Appearance.Theme = "light"
	if issues := config.ValidateMain(cross); len(issues) != 0 {
		t.Fatalf("expected cross-platform modifiers valid, got %+v", issues)
	}

	// Empty triggers, bad URL, negative corner radius, unsupported modifier
	badMain := config.DefaultMainConfig()
	badMain.Trigger.LeaderKey = "invalid_key"
	badMain.Trigger.DoubleTapKey = ""
	badMain.Trigger.CapsuleShortcut = "option+"
	badMain.AI.BaseURL = "ftp://example.com"
	badMain.AI.KeychainAccount = "  "
	badMain.Appearance.CornerRadius = -1
	badMain.Appearance.Theme = "neon"
	mainIssues := config.ValidateMain(badMain)
	for _, kp := range []string{
		"trigger.leader_key",
		"trigger.double_tap_key",
		"trigger.capsule_shortcut",
		"ai.base_url",
		"ai.keychain_account",
		"appearance.corner_radius",
	} {
		if !hasIssue(mainIssues, kp, config.SeverityError) {
			t.Fatalf("expected error for %s, got %+v", kp, mainIssues)
		}
	}
	if !hasIssue(mainIssues, "appearance.theme", config.SeverityWarning) {
		t.Fatalf("expected warning for unknown theme")
	}

	// Leader duplicate key at same level vs child level
	badLeader := config.LeaderConfig{
		Nodes: []config.LeaderNode{
			{
				Key:   "w",
				Label: "Window",
				Action: config.GroupAction(
					config.LeaderNode{Key: "w", Label: "Child w ok", Action: config.ToggleIMEAction()},
					config.LeaderNode{Key: "x", Label: "Bad CLI", Action: config.CLIAction("   ")},
				),
			},
			{Key: "w", Label: "Dup w", Action: config.OpenAppAction("com.apple.Finder")},
		},
	}
	leaderIssues := config.ValidateLeader(badLeader, map[string]struct{}{})
	if !hasIssue(leaderIssues, "nodes[1].key", config.SeverityError) {
		t.Fatalf("expected duplicate leader key error, got %+v", leaderIssues)
	}
	if !hasIssue(leaderIssues, "nodes[0].children[1].command", config.SeverityError) {
		t.Fatalf("expected empty cli command error, got %+v", leaderIssues)
	}
	if hasIssue(leaderIssues, "nodes[0].children[0].key", config.SeverityError) {
		t.Fatalf("did not expect error for same key in child level")
	}

	// Duplicate snippets
	badSnippets := config.SnippetsConfig{
		Items: []config.SnippetItem{
			{ID: "s1", Trigger: ";hi", Content: "Hello"},
			{ID: "s1", Trigger: ";hi", Content: "World"},
		},
	}
	snipIssues := config.ValidateSnippets(badSnippets)
	if !hasIssue(snipIssues, "snippets[1].id", config.SeverityError) || !hasIssue(snipIssues, "snippets[1].trigger", config.SeverityError) {
		t.Fatalf("expected duplicate snippet errors, got %+v", snipIssues)
	}

	// Cross-platform IME rule using process_name + duplicate checks
	imeTOML := `
[[rules]]
process_name = "WindowsTerminal.exe"
app_name = "Windows Terminal"
input_source_id = "en-US"
preferred_mode = "ascii"
restore_on_leave = true

[[rules]]
process_name = "windowsterminal.exe"
app_name = "Windows Terminal"
input_source_id = "en-US"
preferred_mode = "ascii"
`
	imeCfg, err := config.DecodeIMERulesConfig(imeTOML)
	if err != nil {
		t.Fatalf("decode ime rules with process_name failed: %v", err)
	}
	imeIssues := config.ValidateIMERules(imeCfg)
	if !hasIssue(imeIssues, "rules[1].process_name", config.SeverityError) {
		t.Fatalf("expected duplicate process_name error, got %+v", imeIssues)
	}
}

func TestConfigStoreReloadAndRollback(t *testing.T) {
	dir := t.TempDir()
	store := config.NewConfigStore(dir)

	initCfg, err := store.LoadOrInitialize()
	if err != nil {
		t.Fatalf("LoadOrInitialize failed: %v", err)
	}
	if !reflect.DeepEqual(initCfg, config.DefaultConfig()) {
		t.Fatalf("expected default config on init")
	}
	if len(store.LastErrors()) != 0 {
		t.Fatalf("expected no initial errors, got %+v", store.LastErrors())
	}

	// Valid edit & reload
	updated := initCfg.Main
	updated.Trigger.DoubleTapIntervalMs = 450
	updated.Appearance.Theme = "dark"
	mainPath := filepath.Join(dir, string(config.FileMain))
	if err := os.WriteFile(mainPath, []byte(updated.Encode()), 0o644); err != nil {
		t.Fatalf("write updated main: %v", err)
	}
	if !store.Reload() {
		t.Fatalf("expected reload to succeed, errors: %+v", store.LastErrors())
	}
	lastGood := store.Current()
	if lastGood.Main.Trigger.DoubleTapIntervalMs != 450 || lastGood.Main.Appearance.Theme != "dark" {
		t.Fatalf("reload did not update current config")
	}

	// Corrupt leader.toml -> rollback to lastGood
	leaderPath := filepath.Join(dir, string(config.FileLeader))
	validLeader, _ := os.ReadFile(leaderPath)
	if err := os.WriteFile(leaderPath, []byte("[[nodes]\nkey = \"unterminated"), 0o644); err != nil {
		t.Fatalf("corrupt leader: %v", err)
	}
	if store.Reload() {
		t.Fatalf("expected reload to fail on syntax error")
	}
	if !reflect.DeepEqual(store.Current(), lastGood) {
		t.Fatalf("expected rollback to last-known-good config")
	}
	errs := store.LastErrors()
	if len(errs) != 1 || errs[0].File != string(config.FileLeader) || errs[0].Line == 0 {
		t.Fatalf("expected leader.toml parse error with line, got %+v", errs)
	}

	// Restore leader, inject semantic validation error into config.toml
	if err := os.WriteFile(leaderPath, validLeader, 0o644); err != nil {
		t.Fatalf("restore leader: %v", err)
	}
	invalidMain := updated
	invalidMain.Trigger.DoubleTapIntervalMs = 50
	invalidMain.AI.BaseURL = "invalid-url"
	if err := os.WriteFile(mainPath, []byte(invalidMain.Encode()), 0o644); err != nil {
		t.Fatalf("write invalid main: %v", err)
	}
	if store.Reload() {
		t.Fatalf("expected reload to fail on validation errors")
	}
	if !reflect.DeepEqual(store.Current(), lastGood) {
		t.Fatalf("expected rollback to last-known-good on validation failure")
	}
	valErrs := store.LastErrors()
	if len(valErrs) < 2 {
		t.Fatalf("expected at least 2 validation errors, got %+v", valErrs)
	}

	// Recover
	fixedMain := updated
	fixedMain.Trigger.DoubleTapIntervalMs = 550
	if err := os.WriteFile(mainPath, []byte(fixedMain.Encode()), 0o644); err != nil {
		t.Fatalf("write fixed main: %v", err)
	}
	if !store.Reload() {
		t.Fatalf("expected recovery reload to succeed, got %+v", store.LastErrors())
	}
	if store.Current().Main.Trigger.DoubleTapIntervalMs != 550 {
		t.Fatalf("expected updated interval 550")
	}
}

func hasIssue(issues []config.ValidationIssue, keyPath string, sev config.ValidationSeverity) bool {
	for _, issue := range issues {
		if issue.KeyPath == keyPath && issue.Severity == sev {
			return true
		}
	}
	return false
}
