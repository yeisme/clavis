package config

import "testing"

// TestValidateMainSurfacesEverySection guards the ValidateMain wiring itself.
// Section validators can be unit-green while ValidateMain silently forgets to
// call one of them (this happened when two config sections were merged), which
// would let invalid config pass the save gate in store.SaveMain.
func TestValidateMainSurfacesEverySection(t *testing.T) {
	main := DefaultMainConfig()
	main.CLI = CLIConfig{
		Enabled: true,
		TrustedProfiles: []CLITrustedProfile{{
			ID:             "",
			Enabled:        true,
			Commands:       []string{},
			TimeoutSeconds: 0,
		}},
	}
	main.MCP = MCPConfig{
		Enabled: true,
		Servers: []MCPServerConfig{{
			ID:             "",
			Transport:      "wat",
			TimeoutSeconds: 601,
			MaxConcurrency: 0,
		}},
	}

	directCLI := validateCLIConfig(main.CLI)
	directMCP := ValidateMCP(main.MCP)
	if len(directCLI) == 0 || len(directMCP) == 0 {
		t.Fatalf("fixture must produce issues in both sections: cli=%d mcp=%d", len(directCLI), len(directMCP))
	}

	issues := ValidateMain(main)
	cliIssues, mcpIssues := 0, 0
	for _, issue := range issues {
		if len(issue.KeyPath) >= 4 && issue.KeyPath[:4] == "cli." {
			cliIssues++
		}
		if len(issue.KeyPath) >= 4 && issue.KeyPath[:4] == "mcp." {
			mcpIssues++
		}
	}
	if cliIssues == 0 {
		t.Fatalf("ValidateMain dropped cli.* issues; issues = %#v", issues)
	}
	if mcpIssues == 0 {
		t.Fatalf("ValidateMain dropped mcp.* issues; issues = %#v", issues)
	}
}
