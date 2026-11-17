// Package skills provides the filesystem and manifest foundation for local
// declarative skills. It only describes and discovers skills; it does not
// execute actions, workflows, or grant permissions.
package skills

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yeisme/clavis/pkg/core/toml"
)

const (
	// ManifestFileName is the manifest file found in each skill directory.
	ManifestFileName = "skill.toml"
	// DefaultAPIVersion is used when api_version is omitted from a manifest.
	DefaultAPIVersion = "1"
)

var skillIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// State describes whether a skill is eligible for use by a future host. The
// state is declarative; changing it does not start or stop anything.
type State string

const (
	StateEnabled  State = "enabled"
	StateDisabled State = "disabled"
)

// Dependency declares another skill and an optional version constraint. The
// constraint is intentionally opaque at this layer: resolving versions is a
// host/registry concern, not a filesystem concern.
type Dependency struct {
	ID       string `toml:"id"`
	Version  string `toml:"version"`
	Optional bool   `toml:"optional"`
}

// FileReferences names declarative files belonging to a skill. Paths are
// relative to the directory containing the manifest and are never interpreted
// as commands or shell expressions.
type FileReferences struct {
	Actions   []string `toml:"actions"`
	Workflows []string `toml:"workflows"`
}

// Manifest is the contents of a skill.toml file. Action and workflow files
// are references only; this package deliberately does not load or execute
// them.
type Manifest struct {
	APIVersion   string       `toml:"api_version"`
	ID           string       `toml:"id"`
	Name         string       `toml:"name"`
	Version      string       `toml:"version"`
	Description  string       `toml:"description"`
	Enabled      bool         `toml:"enabled"`
	Dependencies []Dependency `toml:"dependencies"`
	Actions      []string     `toml:"actions"`
	Workflows    []string     `toml:"workflows"`

	// Files accepts the grouped [files] spelling. Actions and Workflows are
	// the canonical fields after loading a manifest.
	Files FileReferences `toml:"-"`
	// Dir and ManifestPath are populated by LoadManifest and Scan. They are
	// not serialized into skill.toml.
	Dir          string `toml:"-"`
	ManifestPath string `toml:"-"`
}

// Skill is an alias for Manifest, retained to make discovery-oriented call
// sites read naturally without introducing a second source of truth.
type Skill = Manifest

// IsEnabled reports whether the manifest has not disabled this skill.
func (m Manifest) IsEnabled() bool { return m.Enabled }

// State returns the declarative enabled/disabled state.
func (m Manifest) State() State {
	if m.Enabled {
		return StateEnabled
	}
	return StateDisabled
}

// Enable and Disable change only the in-memory manifest state. Persisting a
// changed state is intentionally outside this package.
func (m *Manifest) Enable() {
	if m != nil {
		m.Enabled = true
	}
}

func (m *Manifest) Disable() {
	if m != nil {
		m.Enabled = false
	}
}

// ActionReferences returns a copy of the action references.
func (m Manifest) ActionReferences() []string { return append([]string(nil), m.Actions...) }

// WorkflowReferences returns a copy of the workflow references.
func (m Manifest) WorkflowReferences() []string { return append([]string(nil), m.Workflows...) }

// ActionPaths returns action reference paths resolved against the manifest
// directory. It does not check existence or open the files.
func (m Manifest) ActionPaths() []string {
	references := m.Actions
	if len(references) == 0 {
		references = m.Files.Actions
	}
	return resolveReferences(m.Dir, references)
}

// WorkflowPaths returns workflow reference paths resolved against the manifest
// directory. It does not check existence or open the files.
func (m Manifest) WorkflowPaths() []string {
	references := m.Workflows
	if len(references) == 0 {
		references = m.Files.Workflows
	}
	return resolveReferences(m.Dir, references)
}

// Validate checks identity, metadata, state, dependency, and reference
// syntax. It does not require referenced files to exist, allowing a host to
// validate a manifest before its declarative files are installed.
func (m Manifest) Validate() error {
	if m.APIVersion != "" && m.APIVersion != DefaultAPIVersion {
		return fmt.Errorf("unsupported api_version %q", m.APIVersion)
	}
	if !skillIDPattern.MatchString(m.ID) {
		return errors.New("id must contain only letters, digits, '.', '_' or '-' and start with a letter or digit")
	}
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(m.Version) == "" {
		return errors.New("version is required")
	}
	seenDependencies := make(map[string]struct{}, len(m.Dependencies))
	for i, dependency := range m.Dependencies {
		dependencyID := dependency.ID
		if !skillIDPattern.MatchString(dependencyID) {
			return fmt.Errorf("dependencies[%d].id is invalid", i)
		}
		if _, duplicate := seenDependencies[dependencyID]; duplicate {
			return fmt.Errorf("dependencies[%d].id %q is duplicated", i, dependencyID)
		}
		seenDependencies[dependencyID] = struct{}{}
		if dependency.Version != "" && strings.TrimSpace(dependency.Version) == "" {
			return fmt.Errorf("dependencies[%d].version cannot be blank", i)
		}
	}
	actions := m.Actions
	if len(actions) == 0 {
		actions = m.Files.Actions
	}
	workflows := m.Workflows
	if len(workflows) == 0 {
		workflows = m.Files.Workflows
	}
	refs := append([]string{}, actions...)
	refs = append(refs, workflows...)
	for _, ref := range refs {
		if err := validateReference(ref); err != nil {
			return fmt.Errorf("file reference %q: %w", ref, err)
		}
	}
	return nil
}

func validateReference(ref string) error {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return errors.New("path cannot be blank")
	}
	// Manifest paths use slash separators on every platform. Rejecting a
	// backslash also prevents a Windows path from acquiring another meaning
	// when a manifest is moved between platforms.
	if strings.Contains(trimmed, "\\") || strings.ContainsRune(trimmed, '\x00') || path.IsAbs(trimmed) || filepath.IsAbs(trimmed) || filepath.VolumeName(trimmed) != "" {
		return errors.New("path must be relative and use slash separators")
	}
	if first := strings.IndexByte(trimmed, '/'); first >= 0 && strings.Contains(trimmed[:first], ":") {
		return errors.New("path must be relative and use slash separators")
	}
	clean := path.Clean(trimmed)
	if clean == "." || clean != trimmed || clean == ".." || strings.HasPrefix(clean, "../") {
		return errors.New("path must stay within the skill directory")
	}
	return nil
}

func resolveReferences(dir string, refs []string) []string {
	out := make([]string, len(refs))
	for i, ref := range refs {
		out[i] = filepath.Join(dir, filepath.FromSlash(ref))
	}
	return out
}

// LoadManifest reads and validates one skill.toml file using the repository's
// TOML parser. It never executes, downloads, or otherwise follows a manifest
// reference.
func LoadManifest(manifestPath string) (Manifest, error) {
	if strings.TrimSpace(manifestPath) == "" {
		return Manifest{}, errors.New("manifest path is required")
	}
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("read skill manifest %q: %w", manifestPath, err)
	}
	manifest, err := DecodeManifest(string(contents))
	if err != nil {
		return Manifest{}, fmt.Errorf("decode skill manifest %q: %w", manifestPath, err)
	}
	manifest.ManifestPath = filepath.Clean(manifestPath)
	manifest.Dir = filepath.Dir(manifest.ManifestPath)
	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("skill manifest %q: %w", manifestPath, err)
	}
	return manifest, nil
}

// DecodeManifest parses and validates skill.toml text. The returned manifest
// has no filesystem location because the text has no source path.
func DecodeManifest(input string) (Manifest, error) {
	root, err := toml.Parse(input)
	if err != nil {
		return Manifest{}, err
	}
	return ManifestFromTOML(root)
}

// ManifestFromTOML decodes and validates a parsed skill.toml table.
func ManifestFromTOML(root *toml.Table) (Manifest, error) {
	if root == nil {
		return Manifest{}, errors.New("manifest is empty")
	}
	base := root
	if value, ok := root.Get("skill"); ok {
		var tableOK bool
		base, tableOK = value.AsTable()
		if !tableOK {
			return Manifest{}, errors.New("skill must be a table")
		}
	}
	m := Manifest{APIVersion: DefaultAPIVersion, Enabled: true}
	var err error
	if m.APIVersion, err = optionalString(base, "api_version", DefaultAPIVersion); err != nil {
		return Manifest{}, err
	}
	if m.ID, err = requiredString(base, "id"); err != nil {
		return Manifest{}, err
	}
	if m.Name, err = requiredString(base, "name"); err != nil {
		return Manifest{}, err
	}
	if m.Version, err = requiredString(base, "version"); err != nil {
		return Manifest{}, err
	}
	if m.Description, err = optionalString(base, "description", ""); err != nil {
		return Manifest{}, err
	}
	if m.Enabled, err = optionalBool(base, "enabled", true); err != nil {
		return Manifest{}, err
	}

	depsValue, depsOK := base.Get("dependencies")
	if !depsOK && base != root {
		depsValue, depsOK = root.Get("dependencies")
	}
	if depsOK {
		m.Dependencies, err = decodeDependencies(depsValue)
		if err != nil {
			return Manifest{}, err
		}
	}
	m.Actions, m.Workflows, err = decodeReferences(base, root)
	if err != nil {
		return Manifest{}, err
	}
	m.Files = FileReferences{Actions: append([]string(nil), m.Actions...), Workflows: append([]string(nil), m.Workflows...)}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func requiredString(t *toml.Table, key string) (string, error) {
	value, ok := t.Get(key)
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	str, ok := value.AsString()
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return str, nil
}

func optionalString(t *toml.Table, key, fallback string) (string, error) {
	value, ok := t.Get(key)
	if !ok {
		return fallback, nil
	}
	str, ok := value.AsString()
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return str, nil
}

func optionalBool(t *toml.Table, key string, fallback bool) (bool, error) {
	value, ok := t.Get(key)
	if !ok {
		return fallback, nil
	}
	result, ok := value.AsBool()
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return result, nil
}

func decodeDependencies(value toml.Value) ([]Dependency, error) {
	if table, ok := value.AsTable(); ok {
		keys := table.SortedKeys()
		deps := make([]Dependency, 0, len(keys))
		for _, id := range keys {
			entry := table.Entries[id]
			if version, ok := entry.AsString(); ok {
				deps = append(deps, Dependency{ID: id, Version: version})
				continue
			}
			metadata, ok := entry.AsTable()
			if !ok {
				return nil, fmt.Errorf("dependency %q must be a version string or table", id)
			}
			version, err := optionalString(metadata, "version", "")
			if err != nil {
				return nil, fmt.Errorf("dependency %q: %w", id, err)
			}
			optional, err := optionalBool(metadata, "optional", false)
			if err != nil {
				return nil, fmt.Errorf("dependency %q: %w", id, err)
			}
			deps = append(deps, Dependency{ID: id, Version: version, Optional: optional})
		}
		return deps, nil
	}
	array, ok := value.AsArray()
	if !ok {
		return nil, errors.New("dependencies must be an array or table")
	}
	deps := make([]Dependency, 0, len(array))
	for i, item := range array {
		if id, ok := item.AsString(); ok {
			deps = append(deps, Dependency{ID: id})
			continue
		}
		table, ok := item.AsTable()
		if !ok {
			return nil, fmt.Errorf("dependencies[%d] must be a string or table", i)
		}
		id, err := requiredString(table, "id")
		if err != nil {
			return nil, fmt.Errorf("dependencies[%d]: %w", i, err)
		}
		version, err := optionalString(table, "version", "")
		if err != nil {
			return nil, fmt.Errorf("dependencies[%d]: %w", i, err)
		}
		optional, err := optionalBool(table, "optional", false)
		if err != nil {
			return nil, fmt.Errorf("dependencies[%d]: %w", i, err)
		}
		deps = append(deps, Dependency{ID: id, Version: version, Optional: optional})
	}
	return deps, nil
}

func decodeReferences(base, root *toml.Table) ([]string, []string, error) {
	actions, err := stringArray(base, "actions")
	if err != nil {
		return nil, nil, err
	}
	workflows, err := stringArray(base, "workflows")
	if err != nil {
		return nil, nil, err
	}
	if base != root {
		if len(actions) == 0 {
			actions, err = stringArray(root, "actions")
		}
		if err != nil {
			return nil, nil, err
		}
		if len(workflows) == 0 {
			workflows, err = stringArray(root, "workflows")
		}
		if err != nil {
			return nil, nil, err
		}
	}
	for _, key := range []string{"files", "references"} {
		value, ok := root.Get(key)
		if !ok {
			continue
		}
		table, ok := value.AsTable()
		if !ok {
			return nil, nil, fmt.Errorf("%s must be a table", key)
		}
		if len(actions) == 0 {
			actions, err = stringArray(table, "actions")
		}
		if err != nil {
			return nil, nil, err
		}
		if len(workflows) == 0 {
			workflows, err = stringArray(table, "workflows")
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return actions, workflows, nil
}

func stringArray(t *toml.Table, key string) ([]string, error) {
	value, ok := t.Get(key)
	if !ok {
		return nil, nil
	}
	array, ok := value.AsArray()
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
	out := make([]string, len(array))
	for i, item := range array {
		out[i], ok = item.AsString()
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be a string", key, i)
		}
	}
	return out, nil
}

// Encode serializes a manifest deterministically with the repository TOML
// encoder. Runtime fields (Dir and ManifestPath) are omitted.
func (m Manifest) Encode() string {
	root := toml.NewTable()
	root.Set("api_version", toml.StringVal(m.APIVersion))
	root.Set("description", toml.StringVal(m.Description))
	root.Set("enabled", toml.BoolVal(m.Enabled))
	root.Set("id", toml.StringVal(m.ID))
	root.Set("name", toml.StringVal(m.Name))
	root.Set("version", toml.StringVal(m.Version))
	deps := make([]toml.Value, len(m.Dependencies))
	for i, dep := range m.Dependencies {
		t := toml.NewTable()
		t.Set("id", toml.StringVal(dep.ID))
		t.Set("optional", toml.BoolVal(dep.Optional))
		t.Set("version", toml.StringVal(dep.Version))
		deps[i] = toml.TableVal(t)
	}
	root.Set("dependencies", toml.ArrayVal(deps))
	actions, workflows := m.Actions, m.Workflows
	if len(actions) == 0 {
		actions = m.Files.Actions
	}
	if len(workflows) == 0 {
		workflows = m.Files.Workflows
	}
	root.Set("actions", stringsToArray(actions))
	root.Set("workflows", stringsToArray(workflows))
	return toml.Encode(root)
}

func stringsToArray(items []string) toml.Value {
	values := make([]toml.Value, len(items))
	for i, item := range items {
		values[i] = toml.StringVal(item)
	}
	return toml.ArrayVal(values)
}

// Scan discovers immediate child directories containing skill.toml. Entries
// are processed in lexical path order, symlinked directories/manifests are
// ignored, and duplicate skill IDs are rejected. A missing root is an empty
// result, which makes optional skill directories safe to configure.
func Scan(root string) ([]Manifest, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("skills directory is required")
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat skills directory %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("skills path %q is not a directory", root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read skills directory %q: %w", root, err)
	}
	// os.ReadDir returns sorted entries, but explicitly retaining this sort
	// documents and protects the deterministic contract if the source changes.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	out := make([]Manifest, 0, len(entries))
	seen := make(map[string]string)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		child := filepath.Join(root, entry.Name())
		manifestPath := filepath.Join(child, ManifestFileName)
		manifestInfo, err := os.Lstat(manifestPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat skill manifest %q: %w", manifestPath, err)
		}
		if manifestInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}
		manifest, err := LoadManifest(manifestPath)
		if err != nil {
			return nil, err
		}
		if previous, exists := seen[manifest.ID]; exists {
			return nil, fmt.Errorf("duplicate skill id %q in %q and %q", manifest.ID, previous, manifest.ManifestPath)
		}
		seen[manifest.ID] = manifest.ManifestPath
		out = append(out, manifest)
	}
	return out, nil
}

// ScanDirectory is the descriptive alias for Scan.
func ScanDirectory(root string) ([]Skill, error) { return Scan(root) }

// ScanManifests is an explicit alias for callers that prefer the manifest
// terminology used by LoadManifest.
func ScanManifests(root string) ([]Manifest, error) { return Scan(root) }
