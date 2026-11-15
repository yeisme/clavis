package config

import (
	"strings"
	"testing"
)

// legacyMainKeys are config.toml keys that did nothing and were removed:
// show_bento_shelf had no consumer; the input engine never read
// mixed_english or auto_pair_punctuation.
var legacyMainKeys = []string{"show_bento_shelf", "mixed_english", "auto_pair_punctuation"}

// TestRemovedKeysStillLoad: a config.toml written by an older Clavis keeps
// loading, and the next save drops the removed keys.
func TestRemovedKeysStillLoad(t *testing.T) {
	old := defaultMainTOML
	old = strings.Replace(old, "[appearance]\n", "[appearance]\nshow_bento_shelf = false\n", 1)
	old = strings.Replace(old, "[input_engine]\n", "[input_engine]\nmixed_english = false\nauto_pair_punctuation = false\n", 1)
	for _, key := range legacyMainKeys {
		if !strings.Contains(old, key) {
			t.Fatalf("test setup: %s not in the old config", key)
		}
	}

	cfg, err := DecodeMainConfig(old)
	if err != nil {
		t.Fatalf("an old config.toml no longer loads: %v", err)
	}
	if want := DefaultMainConfig(); cfg.InputEngine.Scheme != want.InputEngine.Scheme || cfg.Appearance.Theme != want.Appearance.Theme {
		t.Fatalf("decoded config drifted from the defaults: %+v", cfg)
	}
	saved := cfg.Encode()
	for _, key := range legacyMainKeys {
		if strings.Contains(saved, key) {
			t.Errorf("Encode still writes %s", key)
		}
		if strings.Contains(defaultMainTOML, key) {
			t.Errorf("the default config.toml still has %s", key)
		}
	}
	if _, err := DecodeMainConfig(saved); err != nil {
		t.Fatalf("the re-saved config does not load: %v", err)
	}
}
