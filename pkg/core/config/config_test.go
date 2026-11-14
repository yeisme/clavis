package config_test

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func TestDefaultGeneratorIdempotencyAndValidity(t *testing.T) {
	dir := t.TempDir()

	created1, err := config.GenerateIfMissing(dir)
	if err != nil {
		t.Fatalf("GenerateIfMissing failed: %v", err)
	}
	if len(created1) != 7 {
		t.Fatalf("expected 7 created files, got %d: %v", len(created1), created1)
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
	prompts, err := config.DecodePromptsConfig(config.DefaultContent(config.FilePrompts))
	if err != nil {
		t.Fatalf("decode prompts: %v", err)
	}
	components, err := config.DecodeComponentsConfig(config.DefaultContent(config.FileComponents))
	if err != nil {
		t.Fatalf("decode components: %v", err)
	}

	parsed := config.ClavisConfig{Main: m, Leader: l, Snippets: s, Glossary: g, IMERules: ime, Prompts: prompts, Components: components}
	if !reflect.DeepEqual(parsed, config.DefaultConfig()) {
		t.Fatalf("parsed default TOML does not match DefaultConfig()\nparsed: %+v\ndefault: %+v", parsed, config.DefaultConfig())
	}
	if issues := config.Validate(parsed); len(issues) != 0 {
		t.Fatalf("expected 0 validation issues on default config, got %+v", issues)
	}
}

func TestUnifiedInputSchemas(t *testing.T) {
	cfg := config.DefaultMainConfig()
	if cfg.InputEngine.Enabled || cfg.InputEngine.CandidateCount != 7 || cfg.Appearance.VisualPreset != "spatial_3d" || cfg.Daemon.MaxMemoryMB != 64 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	cfg.InputEngine.Mode = "invalid"
	if !hasIssue(config.ValidateMain(cfg), "input_engine.mode", config.SeverityError) {
		t.Fatal("expected invalid input engine mode")
	}
	p := config.DefaultPromptsConfig()
	if len(p.Items) < 5 {
		t.Fatalf("expected rich prompt defaults")
	}
	if _, err := config.DecodePromptsConfig(p.Encode()); err != nil {
		t.Fatalf("prompt roundtrip: %v", err)
	}
	c := config.DefaultComponentsConfig()
	if len(c.Widgets) == 0 || len(c.Sources) == 0 {
		t.Fatalf("expected component defaults")
	}
	if _, err := config.DecodeComponentsConfig(c.Encode()); err != nil {
		t.Fatalf("component roundtrip: %v", err)
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

func TestValidatorRejectsNaNAndInfFloats(t *testing.T) {
	for _, val := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		m := config.DefaultMainConfig()
		m.Appearance.CapsuleWidth = val
		m.Appearance.CornerRadius = val
		m.AI.Temperature = val
		m.AI.TimeoutSeconds = val
		issues := config.ValidateMain(m)
		for _, kp := range []string{
			"appearance.capsule_width",
			"appearance.corner_radius",
			"ai.timeout_seconds",
		} {
			if !hasIssue(issues, kp, config.SeverityError) {
				t.Fatalf("expected error for %s=%v, got %+v", kp, val, issues)
			}
		}
		if !hasIssue(issues, "ai.temperature", config.SeverityWarning) {
			t.Fatalf("expected warning for ai.temperature=%v, got %+v", val, issues)
		}
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

func TestZeroGPTDefaultsAndMultiProfileAI(t *testing.T) {
	mainTOML := config.DefaultContent(config.FileMain)
	lowerTOML := strings.ToLower(mainTOML)
	if strings.Contains(lowerTOML, "gpt") || strings.Contains(lowerTOML, "openai.com") {
		t.Fatalf("DefaultContent(FileMain) must not contain any GPT/OpenAI defaults:\n%s", mainTOML)
	}

	aiCfg := config.DefaultAIConfig()
	if aiCfg.ActiveProfileID != "deepseek" || aiCfg.Provider != "deepseek" || aiCfg.Model != "deepseek-chat" {
		t.Fatalf("expected DeepSeek as default AI profile, got %+v", aiCfg)
	}
	if len(aiCfg.Profiles) != 4 {
		t.Fatalf("expected 4 default AI profiles (deepseek, ollama, claude, gemini), got %d", len(aiCfg.Profiles))
	}

	ollama, ok := aiCfg.ProfileByID("ollama")
	if !ok || ollama.Model != "qwen2.5:7b" || ollama.Protocol != config.ProtocolOpenAICompatible {
		t.Fatalf("unexpected ollama profile: %+v (%v)", ollama, ok)
	}
	claude, ok := aiCfg.ProfileByID("claude")
	if !ok || claude.Protocol != config.ProtocolAnthropic || claude.APIKeyEnvVar != "ANTHROPIC_API_KEY" {
		t.Fatalf("unexpected claude profile: %+v (%v)", claude, ok)
	}
	gemini, ok := aiCfg.ProfileByID("gemini")
	if !ok || gemini.Protocol != config.ProtocolGemini || gemini.APIKeyEnvVar != "GEMINI_API_KEY" {
		t.Fatalf("unexpected gemini profile: %+v (%v)", gemini, ok)
	}

	// Switching ActiveProfileID updates ActiveProfile()
	aiCfg.ActiveProfileID = "claude"
	if got := aiCfg.ActiveProfile(); got.ID != "claude" || got.Protocol != config.ProtocolAnthropic {
		t.Fatalf("expected ActiveProfile claude, got %+v", got)
	}

	// Legacy flat [ai] without [[ai.profiles]] synthesizes a single profile
	legacyTOML := `
[ai]
provider = "custom-llm"
base_url = "https://llm.example.com/v1"
model = "custom-70b"
keychain_account = "clavis.ai.custom"
`
	decodedLegacy, err := config.DecodeMainConfig(legacyTOML)
	if err != nil {
		t.Fatalf("DecodeMainConfig legacy failed: %v", err)
	}
	if len(decodedLegacy.AI.Profiles) != 1 || decodedLegacy.AI.ActiveProfile().Model != "custom-70b" {
		t.Fatalf("expected synthesized single profile for legacy [ai], got %+v", decodedLegacy.AI)
	}

	// Reject raw API keys inside [[ai.profiles]]
	for _, key := range []string{"api_key", "apiKey", "secret_key", "secretKey"} {
		badProfTOML := "[ai]\nactive_profile_id = \"p1\"\n[[ai.profiles]]\nid = \"p1\"\nbase_url = \"https://api.deepseek.com/v1\"\nmodel = \"deepseek-chat\"\n" + key + " = \"sk-secret\"\n"
		_, err := config.DecodeMainConfig(badProfTOML)
		if err == nil {
			t.Fatalf("expected error for forbidden key %q in [[ai.profiles]]", key)
		}
		var de *config.DecodingError
		if !errors.As(err, &de) || de.KeyPath != "ai.profiles[0]."+key {
			t.Fatalf("expected DecodingError at ai.profiles[0].%s, got %v", key, err)
		}
	}
}

func TestKeymapSmartIMEAndSaveStore(t *testing.T) {
	// Keymap & Smart IME TOML decode/encode + validation
	customTOML := `
[keymap]
enabled = true
tap_hold_threshold_ms = 180
caps_lock_tap = "escape"
caps_lock_hold = "nav"
nav_layer_enabled = true

[[keymap.remaps]]
from = "right_command"
tap = "f18"
hold = "right_command"
app_bundle_ids = ["com.apple.Terminal"]

[ime]
enabled = true
default_input_source_id = "com.apple.keylayout.ABC"
show_indicator_hud = true
switch_delay_ms = 40
lock_ascii_on_leader = false
show_floating_pill = false
`
	m, err := config.DecodeMainConfig(customTOML)
	if err != nil {
		t.Fatalf("DecodeMainConfig failed: %v", err)
	}
	if m.Keymap.TapHoldThresholdMs != 180 || len(m.Keymap.Remaps) != 1 || m.Keymap.Remaps[0].Tap != "f18" {
		t.Fatalf("unexpected keymap config: %+v", m.Keymap)
	}
	if m.IME.LockASCIIOnLeader || m.IME.ShowFloatingPill {
		t.Fatalf("expected LockASCIIOnLeader=false and ShowFloatingPill=false, got %+v", m.IME)
	}

	// Keymap threshold out-of-range validation
	badKeymap := m
	badKeymap.Keymap.TapHoldThresholdMs = 50
	if issues := config.ValidateMain(badKeymap); !hasIssue(issues, "keymap.tap_hold_threshold_ms", config.SeverityError) {
		t.Fatalf("expected keymap.tap_hold_threshold_ms validation error, got %+v", issues)
	}

	// IME rules URLPattern & TitlePattern + regex validation
	imeTOML := `
[[rules]]
bundle_id = "com.apple.Safari"
app_name = "Safari"
url_pattern = "*github.com*"
title_pattern = ".*Pull Request.*"
input_source_id = "com.apple.keylayout.ABC"
preferred_mode = "ascii"
restore_on_leave = true
`
	imeCfg, err := config.DecodeIMERulesConfig(imeTOML)
	if err != nil {
		t.Fatalf("DecodeIMERulesConfig failed: %v", err)
	}
	if len(imeCfg.Rules) != 1 || imeCfg.Rules[0].URLPattern != "*github.com*" || imeCfg.Rules[0].TitlePattern != ".*Pull Request.*" {
		t.Fatalf("unexpected decoded IMERule: %+v", imeCfg.Rules)
	}
	if issues := config.ValidateIMERules(imeCfg); len(issues) != 0 {
		t.Fatalf("expected valid IME rule, got %+v", issues)
	}

	badIME := config.IMERulesConfig{
		Rules: append([]config.IMERule(nil), imeCfg.Rules...),
	}
	badIME.Rules[0].TitlePattern = "[unclosed-regex"
	if issues := config.ValidateIMERules(badIME); !hasIssue(issues, "rules[0].title_pattern", config.SeverityError) {
		t.Fatalf("expected rules[0].title_pattern regex error, got %+v", issues)
	}

	// ConfigStore SaveMain and SaveIMERules
	dir := t.TempDir()
	store := config.NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatalf("LoadOrInitialize failed: %v", err)
	}

	if err := store.SaveMain(m); err != nil {
		t.Fatalf("SaveMain failed: %v", err)
	}
	if store.Current().Main.Keymap.TapHoldThresholdMs != 180 {
		t.Fatalf("expected SaveMain to update Current()")
	}
	if err := store.SaveMain(badKeymap); err == nil {
		t.Fatalf("expected SaveMain to reject invalid MainConfig")
	}

	if err := store.SaveIMERules(imeCfg); err != nil {
		t.Fatalf("SaveIMERules failed: %v", err)
	}
	if len(store.Current().IMERules.Rules) != 1 || store.Current().IMERules.Rules[0].URLPattern != "*github.com*" {
		t.Fatalf("expected SaveIMERules to update Current()")
	}
	if err := store.SaveIMERules(badIME); err == nil {
		t.Fatalf("expected SaveIMERules to reject invalid IMERulesConfig")
	}
	if !store.Reload() {
		t.Fatalf("expected Reload after SaveMain/SaveIMERules to succeed, errors: %+v", store.LastErrors())
	}

	// Verify ShowPreviewPane round-trip & default true
	defMain := config.DefaultMainConfig()
	if !defMain.Appearance.ShowPreviewPane {
		t.Fatal("expected ShowPreviewPane=true by default")
	}
	defMain.Appearance.ShowPreviewPane = false
	decodedMain, err := config.DecodeMainConfig(defMain.Encode())
	if err != nil || decodedMain.Appearance.ShowPreviewPane {
		t.Fatalf("expected ShowPreviewPane=false after round-trip, got %+v (%v)", decodedMain.Appearance, err)
	}

	// Verify optional input_source_id when preferred_mode is valid ("ascii", "native", "keep")
	modeOnlyTOML := `
[[rules]]
bundle_id = "com.tencent.xinWeChat"
app_name = "WeChat"
preferred_mode = "native"
restore_on_leave = true

[[rules]]
process_name = "code"
app_name = "VS Code"
preferred_mode = "ascii"
`
	modeOnlyCfg, err := config.DecodeIMERulesConfig(modeOnlyTOML)
	if err != nil {
		t.Fatalf("DecodeIMERulesConfig without input_source_id failed: %v", err)
	}
	if issues := config.ValidateIMERules(modeOnlyCfg); len(issues) != 0 {
		t.Fatalf("expected mode-only IME rules to validate cleanly, got %+v", issues)
	}
	if err := store.SaveIMERules(modeOnlyCfg); err != nil {
		t.Fatalf("SaveIMERules mode-only failed: %v", err)
	}
	redecodedIME, err := config.DecodeIMERulesConfig(modeOnlyCfg.Encode())
	if err != nil || len(redecodedIME.Rules) != 2 || redecodedIME.Rules[0].PreferredMode != config.IMEModeNative {
		t.Fatalf("unexpected round-trip for mode-only IME rules: %+v (%v)", redecodedIME, err)
	}

	// Missing both input_source_id and preferred_mode fails validation
	emptyModeAndSource := config.IMERulesConfig{
		Rules: []config.IMERule{{BundleID: "com.example.App", AppName: "Example"}},
	}
	if issues := config.ValidateIMERules(emptyModeAndSource); !hasIssue(issues, "rules[0].input_source_id", config.SeverityError) {
		t.Fatalf("expected validation error when both input_source_id and preferred_mode are empty, got %+v", issues)
	}

	// Verify SaveSnippets, SaveLeader (cross-file validation), SaveGlossary, and SaveAll
	newSnippets := config.SnippetsConfig{
		Items: []config.SnippetItem{
			{ID: "greet", Trigger: ";gm", Content: "Good morning!", Description: "Greeting", WordBoundary: true},
		},
	}
	if err := store.SaveSnippets(newSnippets); err != nil {
		t.Fatalf("SaveSnippets failed: %v", err)
	}
	if len(store.Current().Snippets.Items) != 1 || store.Current().Snippets.Items[0].ID != "greet" {
		t.Fatalf("SaveSnippets did not update Current().Snippets")
	}

	// Leader referencing non-existent snippet generates cross-file warning, duplicate key fails
	leaderWithMissingSnip := config.LeaderConfig{
		Nodes: []config.LeaderNode{
			{Key: "g", Label: "Greet", Action: config.SnippetAction("missing-snippet")},
		},
	}
	if err := store.SaveLeader(leaderWithMissingSnip); err != nil {
		t.Fatalf("SaveLeader with warning should succeed, got: %v", err)
	}
	if len(store.LastWarnings()) == 0 {
		t.Fatal("expected cross-file missing snippet warning in LastWarnings() after SaveLeader")
	}
	// Saving snippet with matching ID clears the cross-file warning!
	newSnippets.Items = append(newSnippets.Items, config.SnippetItem{
		ID: "missing-snippet", Trigger: ";ms", Content: "Fixed", WordBoundary: true,
	})
	if err := store.SaveSnippets(newSnippets); err != nil {
		t.Fatalf("SaveSnippets second call failed: %v", err)
	}
	if len(store.LastWarnings()) != 0 {
		t.Fatalf("expected cross-file warning to clear when snippet is added, got %+v", store.LastWarnings())
	}

	badLeaderSave := config.LeaderConfig{
		Nodes: []config.LeaderNode{
			{Key: "a", Label: "One", Action: config.ToggleIMEAction()},
			{Key: "a", Label: "Two", Action: config.ToggleIMEAction()},
		},
	}
	if err := store.SaveLeader(badLeaderSave); err == nil {
		t.Fatal("expected SaveLeader to reject duplicate keys")
	}

	newGlossary := config.GlossaryConfig{
		Terms: []config.GlossaryTerm{
			{Term: "MyGo", Aliases: []string{"mygo"}, Hint: "UI framework", Category: "tech"},
		},
	}
	if err := store.SaveGlossary(newGlossary); err != nil {
		t.Fatalf("SaveGlossary failed: %v", err)
	}
	if len(store.Current().Glossary.Terms) != 1 || store.Current().Glossary.Terms[0].Term != "MyGo" {
		t.Fatalf("SaveGlossary did not update Current().Glossary")
	}
	if err := store.SaveGlossary(config.GlossaryConfig{Terms: []config.GlossaryTerm{{Term: "  "}}}); err == nil {
		t.Fatal("expected SaveGlossary to reject empty term")
	}

	fullCfg := store.Current()
	fullCfg.Main.Appearance.CapsuleWidth = 720
	if err := store.SaveAll(fullCfg); err != nil {
		t.Fatalf("SaveAll failed: %v", err)
	}
	if store.Current().Main.Appearance.CapsuleWidth != 720 || len(store.SavedDigests()) != 7 {
		t.Fatalf("unexpected state after SaveAll: width=%v digests=%d", store.Current().Main.Appearance.CapsuleWidth, len(store.SavedDigests()))
	}
	if !store.Reload() {
		t.Fatalf("Reload after all Save* calls failed: %+v", store.LastErrors())
	}
}

// TestAppearanceLanguageLegacyDefault pins the old-config contract: a
// config.toml written before [appearance].language existed decodes to the
// "auto" default, an explicit value round-trips through Encode, and the
// default template and DefaultConfig stay in sync.
func TestAppearanceLanguageLegacyDefault(t *testing.T) {
	decoded, err := config.DecodeMainConfig("[appearance]\ntheme = \"dark\"\n")
	if err != nil {
		t.Fatalf("decode legacy appearance failed: %v", err)
	}
	if decoded.Appearance.Language != "auto" {
		t.Fatalf("legacy config language = %q, want auto", decoded.Appearance.Language)
	}

	decoded.Appearance.Language = "zh-CN"
	encoded := decoded.Encode()
	if !strings.Contains(encoded, "language = \"zh-CN\"") {
		t.Fatalf("encoded config lost the language key:\n%s", encoded)
	}
	rt, err := config.DecodeMainConfig(encoded)
	if err != nil {
		t.Fatalf("decode round-trip failed: %v", err)
	}
	if rt.Appearance.Language != "zh-CN" {
		t.Fatalf("round-trip language = %q, want zh-CN", rt.Appearance.Language)
	}

	if def := config.DefaultAppearanceConfig(); def.Language != "auto" {
		t.Fatalf("DefaultAppearanceConfig().Language = %q, want auto", def.Language)
	}
	issues := config.ValidateMain(config.DefaultMainConfig())
	if len(issues) != 0 {
		t.Fatalf("default config must validate clean, got %+v", issues)
	}
	bad := config.DefaultMainConfig()
	bad.Appearance.Language = "klingon"
	if !hasIssue(config.ValidateMain(bad), "appearance.language", config.SeverityWarning) {
		t.Fatalf("expected warning for unknown language, got %+v", config.ValidateMain(bad))
	}
}

// TestValidatorDiagnosticsLocalizeToChinese verifies the validator speaks
// the configured language, and that the store applies it on reload.
func TestValidatorDiagnosticsLocalizeToChinese(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	i18n.SetLanguage(i18n.Chinese)

	main := config.DefaultMainConfig()
	main.Appearance.Theme = "solarized"
	issues := config.ValidateMain(main)
	found := false
	for _, issue := range issues {
		if issue.KeyPath == "appearance.theme" && issue.Severity == config.SeverityWarning {
			found = true
			if !strings.Contains(issue.Message, "未知的 appearance.theme") {
				t.Fatalf("expected Chinese diagnostic, got %q", issue.Message)
			}
		}
	}
	if !found {
		t.Fatalf("expected appearance.theme warning, got %+v", issues)
	}

	// English restored by the deferred SetLanguage; a store reload of a
	// zh-CN config switches diagnostics without touching the caller.
	i18n.SetLanguage(i18n.English)
	dir := t.TempDir()
	store := config.NewConfigStore(dir)
	loaded, err := store.LoadOrInitialize()
	if err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}
	if loaded.Main.Appearance.Language != "auto" {
		t.Fatalf("fresh template language = %q, want auto", loaded.Main.Appearance.Language)
	}
	main.Appearance.Language = "zh-CN"
	main.Appearance.Theme = "solarized"
	if err := store.SaveMain(main); err != nil {
		t.Fatalf("SaveMain: %v", err)
	}
	if !store.Reload() {
		t.Fatalf("Reload failed: %+v", store.LastErrors())
	}
	warned := false
	for _, w := range store.LastWarnings() {
		if w.KeyPath == "appearance.theme" {
			warned = true
			if !strings.Contains(w.Message, "未知的 appearance.theme") {
				t.Fatalf("expected Chinese warning from store, got %q", w.Message)
			}
		}
	}
	if !warned {
		t.Fatalf("expected theme warning from store, got %+v", store.LastWarnings())
	}
}
