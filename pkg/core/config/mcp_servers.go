package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/yeisme/clavis/pkg/core/toml"
)

const (
	defaultMCPTimeoutSeconds = 60
	defaultMCPMaxConcurrency = 4
)

// MCPConfig declares optional, permission-gated MCP servers in config.toml.
type MCPConfig struct {
	Enabled bool              `json:"enabled"`
	Servers []MCPServerConfig `json:"servers,omitempty"`
}

type MCPServerConfig struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	Transport      string   `json:"transport"`
	Command        string   `json:"command,omitempty"`
	Args           []string `json:"args,omitempty"`
	Environment    []string `json:"environment,omitempty"`
	WorkingDir     string   `json:"workingDir,omitempty"`
	URL            string   `json:"url,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
	MaxConcurrency int      `json:"maxConcurrency"`
	AllowedTools   []string `json:"allowedTools,omitempty"`
	OptIn          bool     `json:"optIn"`
	AutoStart      bool     `json:"autoStart"`
}

func DefaultMCPConfig() MCPConfig { return MCPConfig{Servers: nil} }

// EnabledServers returns enabled server declarations, preserving order.
func (c MCPConfig) EnabledServers() []MCPServerConfig {
	out := make([]MCPServerConfig, 0, len(c.Servers))
	for _, s := range c.Servers {
		if s.Enabled {
			out = append(out, s)
		}
	}
	return out
}

// MCPConfig intentionally stays in pkg/core/config and does not import
// internal/mcp. Callers bridge it through internal/mcp.ConfigFromSection using
// the neutral field values, avoiding an inward dependency from config.

func decodeMCPConfig(tbl *toml.Table, path string) (MCPConfig, error) {
	c := DefaultMCPConfig()
	if tbl == nil {
		return c, nil
	}
	if v, ok, err := optionalBool(tbl, "enabled", path); err != nil {
		return c, err
	} else if ok {
		c.Enabled = v
	}
	tables, _, err := optionalTableArray(tbl, "servers", path)
	if err != nil {
		return c, err
	}
	for i, st := range tables {
		p := fmt.Sprintf("%s.servers[%d]", path, i)
		s := MCPServerConfig{TimeoutSeconds: defaultMCPTimeoutSeconds, MaxConcurrency: defaultMCPMaxConcurrency}
		if s.ID, _, err = optionalString(st, "id", p); err != nil {
			return c, err
		}
		if s.Name, _, err = optionalString(st, "name", p); err != nil {
			return c, err
		}
		if s.Transport, _, err = optionalString(st, "transport", p); err != nil {
			return c, err
		}
		if s.Command, _, err = optionalString(st, "command", p); err != nil {
			return c, err
		}
		if s.WorkingDir, _, err = optionalString(st, "working_dir", p); err != nil {
			return c, err
		}
		if s.URL, _, err = optionalString(st, "url", p); err != nil {
			return c, err
		}
		if n, ok, e := optionalInt(st, "timeout_seconds", p); e != nil {
			return c, e
		} else if ok {
			s.TimeoutSeconds = n
		}
		if n, ok, e := optionalInt(st, "max_concurrency", p); e != nil {
			return c, e
		} else if ok {
			s.MaxConcurrency = n
		}
		if s.Args, _, err = optionalStringArray(st, "args", p); err != nil {
			return c, err
		}
		if s.Environment, _, err = optionalStringArray(st, "environment", p); err != nil {
			return c, err
		}
		if s.AllowedTools, _, err = optionalStringArray(st, "allowed_tools", p); err != nil {
			return c, err
		}
		if s.Enabled, _, err = optionalBool(st, "enabled", p); err != nil {
			return c, err
		}
		if s.OptIn, _, err = optionalBool(st, "opt_in", p); err != nil {
			return c, err
		}
		if s.AutoStart, _, err = optionalBool(st, "auto_start", p); err != nil {
			return c, err
		}
		c.Servers = append(c.Servers, s)
	}
	return c, nil
}
func (c MCPConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("enabled", toml.BoolVal(c.Enabled))
	arr := make([]toml.Value, len(c.Servers))
	for i, s := range c.Servers {
		arr[i] = toml.TableVal(s.toTOML())
	}
	t.Set("servers", toml.ArrayVal(arr))
	return t
}
func (s MCPServerConfig) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("id", toml.StringVal(s.ID))
	t.Set("name", toml.StringVal(s.Name))
	t.Set("enabled", toml.BoolVal(s.Enabled))
	t.Set("transport", toml.StringVal(s.Transport))
	if s.Command != "" {
		t.Set("command", toml.StringVal(s.Command))
	}
	if len(s.Args) > 0 {
		t.Set("args", stringsToTOMLArray(s.Args))
	}
	if len(s.Environment) > 0 {
		t.Set("environment", stringsToTOMLArray(s.Environment))
	}
	if s.WorkingDir != "" {
		t.Set("working_dir", toml.StringVal(s.WorkingDir))
	}
	if s.URL != "" {
		t.Set("url", toml.StringVal(s.URL))
	}
	t.Set("timeout_seconds", toml.IntVal(int64(s.TimeoutSeconds)))
	t.Set("max_concurrency", toml.IntVal(int64(s.MaxConcurrency)))
	t.Set("allowed_tools", stringsToTOMLArray(s.AllowedTools))
	t.Set("opt_in", toml.BoolVal(s.OptIn))
	t.Set("auto_start", toml.BoolVal(s.AutoStart))
	return t
}

func ValidateMCP(c MCPConfig) []ValidationIssue {
	issues := []ValidationIssue{}
	seen := map[string]bool{}
	for i, s := range c.Servers {
		p := fmt.Sprintf("mcp.servers[%d]", i)
		id := strings.TrimSpace(s.ID)
		if id == "" {
			issues = append(issues, mcpIssue(p+".id", "server id is required"))
		}
		if seen[id] {
			issues = append(issues, mcpIssue(p+".id", "duplicate server id"))
		}
		seen[id] = true
		if s.Transport != "stdio" && s.Transport != "http" {
			issues = append(issues, mcpIssue(p+".transport", "transport must be stdio or http"))
		}
		if s.TimeoutSeconds <= 0 || s.TimeoutSeconds > 600 {
			issues = append(issues, mcpIssue(p+".timeout_seconds", "timeout_seconds must be between 1 and 600"))
		}
		if s.MaxConcurrency <= 0 {
			issues = append(issues, mcpIssue(p+".max_concurrency", "max_concurrency must be positive"))
		}
		if s.WorkingDir != "" && !filepath.IsAbs(s.WorkingDir) {
			issues = append(issues, mcpIssue(p+".working_dir", "working_dir must be absolute"))
		}
		if s.Transport == "stdio" && strings.TrimSpace(s.Command) == "" {
			issues = append(issues, mcpIssue(p+".command", "command is required for stdio transport"))
		}
		for j, entry := range s.Environment {
			if parts := strings.SplitN(entry, "=", 2); len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
				issues = append(issues, mcpIssue(fmt.Sprintf("%s.environment[%d]", p, j), "environment entries must be KEY=VALUE"))
			}
		}
		if s.Transport == "http" {
			if !s.OptIn {
				issues = append(issues, mcpIssue(p+".opt_in", "mcp: HTTP transport requires explicit opt-in"))
			}
			u, e := url.Parse(s.URL)
			if e != nil || u.Scheme != "http" || u.Host == "" || !isMCPHostnameLocal(u.Hostname()) {
				issues = append(issues, mcpIssue(p+".url", "mcp: HTTP transport URL must be local"))
			}
		}
	}
	return issues
}
func mcpIssue(path, msg string) ValidationIssue {
	return ValidationIssue{File: string(FileMain), KeyPath: path, Severity: SeverityError, Message: msg}
}
func isMCPHostnameLocal(h string) bool { return h == "localhost" || h == "127.0.0.1" || h == "::1" }
