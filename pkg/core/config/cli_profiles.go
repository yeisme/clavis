package config

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yeisme/clavis/pkg/core/toml"
)

// CLIConfig controls trusted external command execution.
type CLIConfig struct {
	Enabled         bool                `json:"enabled"`
	TrustedProfiles []CLITrustedProfile `json:"trustedProfiles"`
}

// CLITrustedProfile is an explicit, deny-by-default command execution policy.
type CLITrustedProfile struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	Commands       []string `json:"commands"`
	AllowEnv       []string `json:"allowEnv"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
	DefaultDir     string   `json:"defaultDir"`
	DryRun         bool     `json:"dryRun"`
}

func DefaultCLIConfig() CLIConfig { return CLIConfig{Enabled: false} }

func decodeCLIConfig(tbl *toml.Table, path string) (CLIConfig, error) {
	c := DefaultCLIConfig()
	if tbl == nil {
		return c, nil
	}
	if v, ok, err := optionalBool(tbl, "enabled", path); err != nil {
		return c, err
	} else if ok {
		c.Enabled = v
	}
	profiles, ok, err := optionalTableArray(tbl, "trusted_profiles", path)
	if err != nil {
		return c, err
	}
	if !ok || len(profiles) == 0 {
		return c, nil
	}
	c.TrustedProfiles = make([]CLITrustedProfile, 0, len(profiles))
	for i, p := range profiles {
		pp := joinPath(path, "trusted_profiles") + "[" + strconv.Itoa(i) + "]"
		profile := CLITrustedProfile{TimeoutSeconds: 15}
		var e error
		if profile.ID, _, e = optionalString(p, "id", pp); e != nil {
			return c, e
		}
		if profile.Name, _, e = optionalString(p, "name", pp); e != nil {
			return c, e
		}
		if profile.Enabled, _, e = optionalBool(p, "enabled", pp); e != nil {
			return c, e
		}
		if profile.Commands, _, e = optionalStringArray(p, "commands", pp); e != nil {
			return c, e
		}
		if profile.AllowEnv, _, e = optionalStringArray(p, "allow_env", pp); e != nil {
			return c, e
		}
		if profile.TimeoutSeconds, _, e = optionalInt(p, "timeout_seconds", pp); e != nil {
			return c, e
		}
		if profile.DefaultDir, _, e = optionalString(p, "default_dir", pp); e != nil {
			return c, e
		}
		if profile.DryRun, _, e = optionalBool(p, "dry_run", pp); e != nil {
			return c, e
		}
		if profile.TimeoutSeconds == 0 {
			profile.TimeoutSeconds = 15
		}
		c.TrustedProfiles = append(c.TrustedProfiles, profile)
	}
	return c, nil
}

func (c CLIConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("enabled", toml.BoolVal(c.Enabled))
	vals := make([]toml.Value, len(c.TrustedProfiles))
	for i, p := range c.TrustedProfiles {
		pt := toml.NewTable()
		pt.Set("id", toml.StringVal(p.ID))
		pt.Set("name", toml.StringVal(p.Name))
		pt.Set("enabled", toml.BoolVal(p.Enabled))
		pt.Set("commands", stringsToTOMLArray(p.Commands))
		pt.Set("allow_env", stringsToTOMLArray(p.AllowEnv))
		pt.Set("timeout_seconds", toml.IntVal(int64(p.TimeoutSeconds)))
		pt.Set("default_dir", toml.StringVal(p.DefaultDir))
		pt.Set("dry_run", toml.BoolVal(p.DryRun))
		vals[i] = toml.TableVal(pt)
	}
	if len(vals) > 0 {
		t.Set("trusted_profiles", toml.ArrayVal(vals))
	}
	return t
}

func validateCLIConfig(c CLIConfig) []ValidationIssue {
	var issues []ValidationIssue
	seen := map[string]bool{}
	for i, p := range c.TrustedProfiles {
		path := "cli.trusted_profiles[" + strconv.Itoa(i) + "]"
		if strings.TrimSpace(p.ID) == "" {
			issues = append(issues, ValidationIssue{File: string(FileMain), KeyPath: path + ".id", Severity: SeverityError, Message: "trusted profile id cannot be empty"})
		}
		if seen[p.ID] {
			issues = append(issues, ValidationIssue{File: string(FileMain), KeyPath: path + ".id", Severity: SeverityError, Message: "trusted profile id must be unique"})
		}
		seen[p.ID] = true
		for j, cmd := range p.Commands {
			if strings.TrimSpace(cmd) == "" {
				issues = append(issues, ValidationIssue{File: string(FileMain), KeyPath: path + ".commands[" + strconv.Itoa(j) + "]", Severity: SeverityError, Message: "trusted profile command cannot be empty"})
			}
		}
		if p.TimeoutSeconds < 1 || p.TimeoutSeconds > 300 {
			issues = append(issues, ValidationIssue{File: string(FileMain), KeyPath: path + ".timeout_seconds", Severity: SeverityError, Message: "timeout_seconds must be between 1 and 300"})
		}
		if p.DefaultDir != "" && !filepath.IsAbs(p.DefaultDir) {
			issues = append(issues, ValidationIssue{File: string(FileMain), KeyPath: path + ".default_dir", Severity: SeverityError, Message: "default_dir must be an absolute path"})
		}
	}
	return issues
}

func (c CLIConfig) CLIProfileIDs() map[string]struct{} {
	known := make(map[string]struct{}, len(c.TrustedProfiles))
	for _, p := range c.TrustedProfiles {
		known[p.ID] = struct{}{}
	}
	return known
}

func validateCLIActionProfiles(leader LeaderConfig, cli CLIConfig) []ValidationIssue {
	known := cli.CLIProfileIDs()
	var issues []ValidationIssue
	var walk func([]LeaderNode, string)
	walk = func(nodes []LeaderNode, base string) {
		for i, n := range nodes {
			p := base + "[" + strconv.Itoa(i) + "]"
			if n.Action.Type == ActionCLI && n.Action.Profile != "" {
				if _, ok := known[n.Action.Profile]; !ok {
					issues = append(issues, ValidationIssue{File: string(FileLeader), KeyPath: p + ".profile", Severity: SeverityError, Message: "CLI action references unknown trusted profile " + n.Action.Profile})
				}
			}
			walk(n.Action.Children, p+".children")
		}
	}
	walk(leader.Nodes, "nodes")
	return issues
}
