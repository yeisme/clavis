package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQuickJumpConfigRoundTripAndValidation(t *testing.T) {
	raw := `
[[bookmarks]]
id = "clavis-repo"
name = "Clavis Repo"
kind = "dir"
target = "~/workspace/clavis"
tags = ["go", "clavis"]

[[bookmarks]]
id = "gh-search"
name = "GitHub Search"
kind = "url"
target = "https://github.com/search?q={query}"
tags = ["web", "code"]
`
	cfg, err := DecodeQuickJumpConfig(raw)
	if err != nil {
		t.Fatalf("DecodeQuickJumpConfig: %v", err)
	}
	if len(cfg.Bookmarks) != 2 || cfg.Bookmarks[1].Kind != QuickJumpURL {
		t.Fatalf("unexpected decoded bookmarks: %+v", cfg.Bookmarks)
	}
	if issues := ValidateQuickJump(cfg); len(issues) != 0 {
		t.Fatalf("expected valid quickjump config, got %+v", issues)
	}

	// Store reload integration
	dir := t.TempDir()
	store := NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, string(FileQuickJump)), []byte(cfg.Encode()), 0o644); err != nil {
		t.Fatal(err)
	}
	if !store.Reload() {
		t.Fatalf("Reload failed: %+v", store.LastErrors())
	}
	if len(store.Current().QuickJump.Bookmarks) != 2 {
		t.Fatalf("expected 2 bookmarks after reload, got %+v", store.Current().QuickJump.Bookmarks)
	}

	// Reject javascript: URL schemes
	bad := QuickJumpConfig{
		Bookmarks: []QuickJumpBookmark{{ID: "xss", Name: "Bad", Kind: QuickJumpURL, Target: "javascript:alert(1)"}},
	}
	if issues := ValidateQuickJump(bad); len(issues) == 0 {
		t.Fatal("expected error for javascript: URL bookmark")
	}
}
