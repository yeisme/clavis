// Package plugins provides a small, provider-neutral runtime for local plugins.
//
// Plugin manifests only describe a plugin. Permissions are intentionally
// declarative; this package does not grant filesystem or network access.
package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

var pluginIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Permissions are declarations consumed by a host or UI. The runtime never
// uses them to grant access to a process.
type Permissions struct {
	Filesystem []string `toml:"filesystem"`
	Network    []string `toml:"network"`
}

// Manifest is the contents of a plugin.toml file. Entrypoint is preferred;
// Command is retained as a convenient spelling for manifests produced by
// older tooling. Args are passed to the entrypoint without shell expansion.
type Manifest struct {
	APIVersion   string   `toml:"api_version"`
	ID           string   `toml:"id"`
	Name         string   `toml:"name"`
	Version      string   `toml:"version"`
	Description  string   `toml:"description"`
	Entrypoint   string   `toml:"entrypoint"`
	Command      string   `toml:"command"`
	Args         []string `toml:"args"`
	Capabilities []string `toml:"capabilities"`
	// StyleFields is the allow-list returned by style.render. A style response
	// can never expose fields that are absent from this declaration.
	StyleFields []string    `toml:"style_fields"`
	Permissions Permissions `toml:"permissions"`
	Dir         string      `toml:"-"`
}

// Metadata is the provider-neutral registration record.
type Metadata struct {
	ID           string
	Name         string
	Version      string
	Description  string
	Capabilities []string
	StyleFields  []string
	Permissions  Permissions
	ManifestPath string
}

// Capability describes an operation exposed by a plugin.
type Capability struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}

// Registry receives metadata and capabilities without imposing a UI or
// provider implementation. Registration is performed during Scan.
type Registry interface {
	RegisterPlugin(Metadata) error
	RegisterCapability(pluginID string, capability Capability) error
}

// Provider is an alias kept for callers that call their registry a provider.
type Provider = Registry

// LoadManifest reads and validates a plugin.toml file.
func LoadManifest(path string) (Manifest, error) {
	var manifest Manifest
	if _, err := toml.DecodeFile(path, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode plugin manifest %q: %w", path, err)
	}
	manifest.Dir = filepath.Dir(path)
	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("plugin manifest %q: %w", path, err)
	}
	if manifest.APIVersion == "" {
		manifest.APIVersion = "1"
	}
	return manifest, nil
}

// Validate checks required identity and executable fields. It does not check
// whether the executable exists until the plugin is started lazily.
func (m Manifest) Validate() error {
	if m.APIVersion == "" {
		m.APIVersion = "1"
	}
	if m.APIVersion != "1" {
		return fmt.Errorf("unsupported api_version %q", m.APIVersion)
	}
	if !pluginIDPattern.MatchString(m.ID) {
		return errors.New("id must contain only letters, digits, '.', '_' or '-' and start with a letter or digit")
	}
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(m.Version) == "" {
		return errors.New("version is required")
	}
	if strings.TrimSpace(m.Entrypoint) == "" && strings.TrimSpace(m.Command) == "" {
		return errors.New("entrypoint is required")
	}
	if strings.TrimSpace(m.Entrypoint) != "" && strings.TrimSpace(m.Command) != "" {
		return errors.New("entrypoint and command are mutually exclusive")
	}
	for _, c := range m.Capabilities {
		if strings.TrimSpace(c) == "" {
			return errors.New("capability names cannot be empty")
		}
	}
	for _, p := range append(append([]string{}, m.Permissions.Filesystem...), m.Permissions.Network...) {
		if strings.TrimSpace(p) == "" {
			return errors.New("permission declarations cannot be empty")
		}
	}
	return nil
}

func (m Manifest) executable() (string, []string) {
	command := m.Entrypoint
	if command == "" {
		command = m.Command
	}
	return command, append([]string(nil), m.Args...)
}

func scanManifests(root string) ([]Manifest, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	manifests := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "plugin.toml")
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		manifest, err := LoadManifest(path)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, manifest)
	}
	return manifests, nil
}
