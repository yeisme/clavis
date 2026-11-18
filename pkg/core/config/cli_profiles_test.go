package config

import (
	"strings"
	"testing"
)

func TestCLIConfigRoundTrip(t *testing.T) {
	cfg := DefaultMainConfig()
	cfg.CLI = CLIConfig{
		Enabled: true,
		TrustedProfiles: []CLITrustedProfile{{
			ID: "safe-tools", Name: "Safe tools", Enabled: true,
			Commands: []string{"/bin/echo"}, AllowEnv: []string{"CUSTOM_MODE"},
			TimeoutSeconds: 20, DefaultDir: "/tmp", DryRun: false,
		}},
	}
	encoded := cfg.Encode()
	decoded, err := DecodeMainConfig(encoded)
	if err != nil {
		t.Fatalf("decode encoded config: %v", err)
	}
	if len(decoded.CLI.TrustedProfiles) != 1 || decoded.CLI.TrustedProfiles[0].ID != "safe-tools" {
		t.Fatalf("CLI profile did not round-trip: %#v", decoded.CLI)
	}
	if decoded.CLI.TrustedProfiles[0].TimeoutSeconds != 20 || decoded.CLI.TrustedProfiles[0].DefaultDir != "/tmp" {
		t.Fatalf("CLI profile fields did not round-trip: %#v", decoded.CLI.TrustedProfiles[0])
	}
}

func TestCLISectionIsOptionalForLegacyConfig(t *testing.T) {
	cfg, err := DecodeMainConfig("")
	if err != nil {
		t.Fatalf("legacy config without cli section should decode: %v", err)
	}
	if issues := ValidateMain(cfg); len(issues) != 0 {
		t.Fatalf("legacy config without cli section should validate: %#v", issues)
	}
	encoded := cfg.Encode()
	if !strings.Contains(encoded, "[cli]") || !strings.Contains(encoded, "enabled = false") {
		t.Fatalf("encoded config should document safe CLI defaults: %s", encoded)
	}
}

func TestCLIConfigValidation(t *testing.T) {
	cfg := DefaultMainConfig()
	cfg.CLI.TrustedProfiles = []CLITrustedProfile{
		{ID: "dup", TimeoutSeconds: 0, Commands: []string{""}},
		{ID: "dup", TimeoutSeconds: 301, DefaultDir: "relative"},
	}
	issues := validateCLIConfig(cfg.CLI)
	if len(issues) < 4 {
		t.Fatalf("expected profile validation issues, got %#v", issues)
	}
}

func TestCLIActionUnknownProfileValidation(t *testing.T) {
	leader := LeaderConfig{Nodes: []LeaderNode{{
		Key: "x", Label: "CLI", Action: CLIProfileAction("missing", "/bin/echo"),
	}}}
	issues := validateCLIActionProfiles(leader, DefaultCLIConfig())
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "missing") {
		t.Fatalf("expected diagnosable missing profile issue, got %#v", issues)
	}
}
