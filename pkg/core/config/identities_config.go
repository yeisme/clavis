package config

import (
	"strings"

	"github.com/yeisme/clavis/pkg/core/toml"
)

// IdentitiesConfig configures identities.toml (Personas, Env Profiles, and
// Sync Identity). Zero value equals DefaultIdentitiesConfig().
type IdentitiesConfig struct {
	ActivePersonaID    string `json:"activePersonaId,omitempty"`
	ActiveEnvProfileID string `json:"activeEnvProfileId,omitempty"`
}

// DefaultIdentitiesConfig returns the zero-noise default IdentitiesConfig.
func DefaultIdentitiesConfig() IdentitiesConfig {
	return IdentitiesConfig{}
}

// DecodeIdentitiesConfig parses identities.toml content.
func DecodeIdentitiesConfig(input string) (IdentitiesConfig, error) {
	tbl, err := toml.Parse(input)
	if err != nil {
		return IdentitiesConfig{}, err
	}
	return IdentitiesConfigFromTOML(tbl)
}

// IdentitiesConfigFromTOML decodes an already-parsed TOML table into IdentitiesConfig.
func IdentitiesConfigFromTOML(tbl *toml.Table) (IdentitiesConfig, error) {
	return decodeIdentitiesConfig(tbl, "")
}

func decodeIdentitiesConfig(tbl *toml.Table, path string) (IdentitiesConfig, error) {
	cfg := DefaultIdentitiesConfig()
	if tbl == nil {
		return cfg, nil
	}
	if s, ok, err := optionalString(tbl, "active_persona_id", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.ActivePersonaID = s
	}
	if s, ok, err := optionalString(tbl, "active_env_profile_id", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.ActiveEnvProfileID = s
	}
	return cfg, nil
}

func (c IdentitiesConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	if c.ActivePersonaID != "" {
		t.Set("active_persona_id", toml.StringVal(c.ActivePersonaID))
	}
	if c.ActiveEnvProfileID != "" {
		t.Set("active_env_profile_id", toml.StringVal(c.ActiveEnvProfileID))
	}
	return t
}

// Encode serializes IdentitiesConfig to TOML.
func (c IdentitiesConfig) Encode() string {
	encoded := toml.Encode(c.toTOML())
	if strings.TrimSpace(encoded) == "" {
		return "# Clavis identities configuration (personas, env profiles, sync identity)\n"
	}
	return encoded
}

// ValidateIdentities validates IdentitiesConfig.
func ValidateIdentities(_ IdentitiesConfig) []ValidationIssue {
	return nil
}
