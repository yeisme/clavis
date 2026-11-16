package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReasoningEffortRoundTrip: a profile's reasoning_effort survives a save
// and a load; profiles without one get no reasoning_effort line.
func TestReasoningEffortRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewConfigStore(dir)
	cfg, err := store.LoadOrInitialize()
	if err != nil {
		t.Fatal(err)
	}
	main := cfg.Main
	main.AI.Profiles = append(main.AI.Profiles, AIProfile{
		ID: "magpie", Name: "Magpie", Provider: "magpie", Protocol: ProtocolOpenAICompatible,
		BaseURL: "http://10.0.0.1:3425/v1", Model: "huoshan/deepseek-v4-1-flash-261017",
		Temperature: 0.2, TimeoutSeconds: 60, SystemPrompt: "s", KeychainAccount: "clavis.ai.magpie",
		ReasoningEffort: "low",
	})
	main.AI.ActiveProfileID = "magpie"
	if err := store.SaveMain(main); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "reasoning_effort"); n != 1 {
		t.Fatalf("config.toml has %d reasoning_effort lines, want 1 (only the profile that sets it)", n)
	}

	reloaded := NewConfigStore(dir)
	if !reloaded.Reload() {
		t.Fatalf("reload: %v", reloaded.LastErrors())
	}
	if got := reloaded.Current().Main.AI.ActiveProfile().ReasoningEffort; got != "low" {
		t.Fatalf("reasoning_effort after reload = %q, want low", got)
	}
}
