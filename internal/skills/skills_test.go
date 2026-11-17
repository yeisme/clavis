package skills

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadManifestUsesRepositoryTOMLAndDefaults(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, ManifestFileName)
	contents := `id = "demo"
name = "Demo"
version = "1.2.3"
description = "A declarative skill"

[files]
actions = ["actions.toml"]
workflows = ["workflows.toml"]

[[dependencies]]
id = "shared"
version = ">=1.0.0"
optional = true
`
	if err := os.WriteFile(manifestPath, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.APIVersion != DefaultAPIVersion || !manifest.Enabled {
		t.Fatalf("defaults = %#v", manifest)
	}
	if manifest.ID != "demo" || manifest.Version != "1.2.3" || manifest.Dir != dir {
		t.Fatalf("identity = %#v", manifest)
	}
	if !reflect.DeepEqual(manifest.Actions, []string{"actions.toml"}) || !reflect.DeepEqual(manifest.Workflows, []string{"workflows.toml"}) {
		t.Fatalf("references = %#v", manifest)
	}
	wantDeps := []Dependency{{ID: "shared", Version: ">=1.0.0", Optional: true}}
	if !reflect.DeepEqual(manifest.Dependencies, wantDeps) {
		t.Fatalf("dependencies = %#v, want %#v", manifest.Dependencies, wantDeps)
	}
	if got := manifest.ActionPaths(); !reflect.DeepEqual(got, []string{filepath.Join(dir, "actions.toml")}) {
		t.Fatalf("action paths = %#v", got)
	}
}

func TestManifestValidateRejectsUnsafeReferencesAndInvalidMetadata(t *testing.T) {
	cases := []Manifest{
		{ID: "bad id", Name: "Name", Version: "1"},
		{ID: "id", Name: "Name", Version: "1", Actions: []string{"../outside.toml"}},
		{ID: "id", Name: "Name", Version: "1", Workflows: []string{"/absolute.toml"}},
		{ID: "id", Name: "Name", Version: "1", Actions: []string{"actions\\run.toml"}},
		{ID: "id", Name: "Name", Version: "1", Dependencies: []Dependency{{ID: "bad id"}}},
	}
	for i, manifest := range cases {
		if err := manifest.Validate(); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

func TestScanIsSortedAndIgnoresNonSkillsAndSymlinks(t *testing.T) {
	root := t.TempDir()
	writeSkill := func(name, id string) {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		contents := "id = \"" + id + "\"\nname = \"" + id + "\"\nversion = \"1\"\n"
		if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill("z-last", "z-last")
	writeSkill("a-first", "a-first")
	if err := os.Mkdir(filepath.Join(root, "not-a-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	writeOutside := filepath.Join(outside, ManifestFileName)
	if err := os.WriteFile(writeOutside, []byte("id = \"link\"\nname = \"Link\"\nversion = \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}

	manifests, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{manifests[0].ID, manifests[1].ID}; !reflect.DeepEqual(got, []string{"a-first", "z-last"}) {
		t.Fatalf("scan order = %#v", got)
	}
}

func TestScanRejectsDuplicateIDsAndMissingRootIsEmpty(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one", "two"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		contents := "id = \"same\"\nname = \"" + name + "\"\nversion = \"1\"\n"
		if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Scan(root); err == nil || !strings.Contains(err.Error(), `duplicate skill id "same"`) {
		t.Fatalf("duplicate error = %v", err)
	}
	missing, err := Scan(filepath.Join(root, "missing"))
	if err != nil || missing != nil {
		t.Fatalf("missing scan = %#v, %v", missing, err)
	}
}

func TestManifestStateIsDeclarativeAndCopiesReferences(t *testing.T) {
	manifest := Manifest{ID: "demo", Name: "Demo", Version: "1", Enabled: true, Actions: []string{"a.toml"}, Workflows: []string{"w.toml"}}
	if manifest.State() != StateEnabled || !manifest.IsEnabled() {
		t.Fatalf("initial state = %q", manifest.State())
	}
	manifest.Disable()
	if manifest.State() != StateDisabled || manifest.IsEnabled() {
		t.Fatalf("disabled state = %q", manifest.State())
	}
	manifest.Enable()
	actions := manifest.ActionReferences()
	actions[0] = "changed.toml"
	if manifest.Actions[0] != "a.toml" {
		t.Fatal("action references were not copied")
	}
}

func TestManifestEncodeIsDeterministicAndRoundTrips(t *testing.T) {
	manifest := Manifest{
		APIVersion:   DefaultAPIVersion,
		ID:           "demo",
		Name:         "Demo",
		Version:      "1",
		Enabled:      false,
		Dependencies: []Dependency{{ID: "other", Version: "^2", Optional: false}},
		Actions:      []string{"actions.toml"},
		Workflows:    []string{"workflows.toml"},
	}
	encoded := manifest.Encode()
	if encoded != manifest.Encode() || !strings.Contains(encoded, "enabled = false") {
		t.Fatalf("encoded manifest is not deterministic: %q", encoded)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ManifestFileName)
	if err := os.WriteFile(path, []byte(encoded), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != manifest.ID || loaded.Enabled != manifest.Enabled || !reflect.DeepEqual(loaded.Actions, manifest.Actions) {
		t.Fatalf("round trip = %#v", loaded)
	}
}
