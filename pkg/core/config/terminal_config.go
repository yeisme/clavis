package config

import (
	"strings"

	"github.com/yeisme/clavis/pkg/core/toml"
)

// TerminalProfile describes how to open a terminal at a directory. Command is
// an app name for open -na or an executable; ArgTemplate may contain {dir}.
type TerminalProfile struct {
	Name        string `json:"name" toml:"name"`
	BundleID    string `json:"bundleId" toml:"bundle_id"`
	Command     string `json:"command" toml:"command"`
	ArgTemplate string `json:"argTemplate" toml:"arg_template"`
}

type TerminalConfig struct {
	Default  string            `json:"default"`
	Profiles []TerminalProfile `json:"profiles"`
}

func DefaultTerminalConfig() TerminalConfig {
	return TerminalConfig{Default: "ghostty", Profiles: []TerminalProfile{
		{Name: "ghostty", BundleID: "com.mitchellh.ghostty", Command: "Ghostty", ArgTemplate: "--working-directory={dir}"},
		{Name: "terminal", BundleID: "com.apple.Terminal", Command: "Terminal", ArgTemplate: "{dir}"},
	}}
}

func isAppleTerminalProfile(p TerminalProfile) bool {
	return strings.EqualFold(strings.TrimSpace(p.BundleID), "com.apple.Terminal") ||
		(strings.TrimSpace(p.BundleID) == "" && strings.EqualFold(strings.TrimSpace(p.Command), "Terminal"))
}

func decodeTerminalConfig(tbl *toml.Table, path string) (TerminalConfig, error) {
	cfg := DefaultTerminalConfig()
	if tbl == nil {
		return cfg, nil
	}
	if v, ok, err := optionalString(tbl, "default", path); err != nil {
		return cfg, err
	} else if ok {
		cfg.Default = strings.TrimSpace(v)
	}
	profiles, ok, err := optionalTableArray(tbl, "profiles", path)
	if err != nil {
		return cfg, err
	}
	if !ok {
		return cfg, nil
	}
	cfg.Profiles = nil
	for i, p := range profiles {
		pp := joinPath(path, "profiles") + "[" + itoa(i) + "]"
		profile := TerminalProfile{}
		if profile.Name, _, err = optionalString(p, "name", pp); err != nil {
			return cfg, err
		}
		if profile.BundleID, _, err = optionalString(p, "bundle_id", pp); err != nil {
			return cfg, err
		}
		if profile.Command, _, err = optionalString(p, "command", pp); err != nil {
			return cfg, err
		}
		if profile.ArgTemplate, _, err = optionalString(p, "arg_template", pp); err != nil {
			return cfg, err
		}
		if isAppleTerminalProfile(profile) && strings.TrimSpace(profile.ArgTemplate) == "--working-directory={dir}" {
			profile.ArgTemplate = "{dir}"
		}
		cfg.Profiles = append(cfg.Profiles, profile)
	}
	return cfg, nil
}

func (c TerminalConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("default", toml.StringVal(c.Default))
	vals := make([]toml.Value, len(c.Profiles))
	for i, p := range c.Profiles {
		pt := toml.NewTable()
		pt.Set("name", toml.StringVal(p.Name))
		pt.Set("bundle_id", toml.StringVal(p.BundleID))
		pt.Set("command", toml.StringVal(p.Command))
		pt.Set("arg_template", toml.StringVal(p.ArgTemplate))
		vals[i] = toml.TableVal(pt)
	}
	t.Set("profiles", toml.ArrayVal(vals))
	return t
}

func itoa(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = digits[n%10]
		n /= 10
	}
	return string(b[i:])
}
