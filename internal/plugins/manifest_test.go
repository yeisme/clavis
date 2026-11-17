package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestValidateRequiresIdentityAndEntrypoint(t *testing.T) {
	cases := []Manifest{
		{Name: "x", Version: "1", Entrypoint: "x"},
		{ID: "x", Version: "1", Entrypoint: "x"},
		{ID: "x", Name: "x", Entrypoint: "x"},
		{ID: "x", Name: "x", Version: "1"},
		{ID: "not valid", Name: "x", Version: "1", Entrypoint: "x"},
	}
	for i, manifest := range cases {
		if err := manifest.Validate(); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

func TestLoadManifestDefaultsAPIVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.toml")
	if err := os.WriteFile(path, []byte("id = \"hello\"\nname = \"Hello\"\nversion = \"1\"\ncommand = \"hello\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.APIVersion != "1" || manifest.Dir != dir {
		t.Fatalf("manifest defaults = %#v", manifest)
	}
}
