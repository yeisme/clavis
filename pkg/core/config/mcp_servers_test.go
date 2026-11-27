package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPConfigRoundTripAndDefaults(t *testing.T) {
	input := `[mcp]
enabled = true
[[mcp.servers]]
id = "local"
name = "Local"
enabled = true
transport = "stdio"
command = "/bin/tool"
args = ["--stdio"]
environment = ["MODE=test"]
working_dir = "/tmp"
timeout_seconds = 30
max_concurrency = 2
allowed_tools = ["echo"]
auto_start = true
`
	cfg, err := DecodeMainConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCP.Servers) != 1 || cfg.MCP.Servers[0].TimeoutSeconds != 30 {
		t.Fatalf("MCP = %#v", cfg.MCP)
	}
	encoded := cfg.Encode()
	again, err := DecodeMainConfig(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.MCP.Servers[0]; got.ID != "local" || got.AllowedTools[0] != "echo" || got.WorkingDir != "/tmp" {
		t.Fatalf("round trip = %#v", got)
	}

	without, err := DecodeMainConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if len(without.MCP.Servers) != 0 {
		t.Fatalf("unexpected servers: %#v", without.MCP)
	}
	if _, err := DecodeMainConfig(without.Encode()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateMCP(t *testing.T) {
	workDir := t.TempDir()
	toolPath := filepath.Join(workDir, "tool")
	valid := MCPConfig{Servers: []MCPServerConfig{
		{ID: "stdio", Transport: "stdio", Command: toolPath, WorkingDir: workDir, TimeoutSeconds: 60, MaxConcurrency: 4},
		{ID: "http", Transport: "http", URL: "http://127.0.0.1:8080", OptIn: true, TimeoutSeconds: 60, MaxConcurrency: 4},
	}}
	if issues := ValidateMCP(valid); len(issues) != 0 {
		t.Fatalf("valid issues = %#v", issues)
	}
	bad := MCPConfig{Servers: []MCPServerConfig{
		{ID: "same", Transport: "wat", TimeoutSeconds: 601, MaxConcurrency: 0, WorkingDir: "relative"},
		{ID: "same", Transport: "http", URL: "http://example.com", TimeoutSeconds: 1, MaxConcurrency: 1},
	}}
	issues := ValidateMCP(bad)
	if len(issues) < 6 {
		t.Fatalf("issues = %#v", issues)
	}
	joined := ""
	for _, issue := range issues {
		joined += issue.KeyPath + " " + issue.Message + "\n"
	}
	for _, want := range []string{"duplicate", "local", "opt_in", "absolute"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
}

func TestDefaultMainTemplateDocumentsMCP(t *testing.T) {
	content := DefaultContent(FileMain)
	for _, want := range []string{"[mcp]", "allowed_tools", "timeout_seconds"} {
		if !strings.Contains(content, want) {
			t.Errorf("template missing %q", want)
		}
	}
}
