package template

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const SchemaVersion = "promptrepo.catalog.v0.1"

// Declaration order is part of the canonical digest protocol. Do not reorder.
type Catalog struct {
	SchemaVersion string      `json:"schema_version"`
	Repository    Repository  `json:"repository"`
	GeneratedAt   time.Time   `json:"generated_at"`
	Digest        string      `json:"digest"`
	Discovery     []Discovery `json:"discovery,omitempty"`
	Solutions     []Solution  `json:"solutions"`
}

type Repository struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	DefaultLocale   string `json:"default_locale"`
	TaxonomyVersion string `json:"taxonomy_version"`
}

type Discovery struct {
	PackageID  string   `json:"package_id"`
	SolutionID string   `json:"solution_id"`
	Role       string   `json:"role"`
	Locale     string   `json:"locale"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Media      []string `json:"media"`
}

type Solution struct {
	PackageID    string            `json:"package_id"`
	ID           string            `json:"id"`
	Version      string            `json:"version"`
	Digest       string            `json:"digest"`
	Category     string            `json:"category"`
	Tags         []string          `json:"tags,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Rights       string            `json:"rights"`
	Maturity     string            `json:"maturity"`
	Locales      map[string]Locale `json:"locales"`
	Templates    []Template        `json:"templates,omitempty"`
	RepositoryID string            `json:"repository_id,omitempty"`
}

// Locale mirrors promptrepo.LocalizedText exactly: usage and aliases are optional, and
// the real catalog relies on it (15 locales omit usage, 40 carry aliases). Dropping the
// omitempty tags or the Aliases field silently breaks every digest in the catalog.
type Locale struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Usage   string   `json:"usage,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
}

type Template struct {
	Role   string `json:"role"`
	Locale string `json:"locale"`
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

func (s Solution) Key() string { return s.PackageID + "/" + s.ID + "@" + s.Version }

// ParseCatalog verifies catalog and solution metadata. Any failure returns nil;
// use LoadCatalog for the body and sidecar existence checks before making it usable.
func ParseCatalog(data []byte) (*Catalog, []Diagnostic) {
	return parseCatalog(data, false)
}

// Snapshot refresh accepts a verified empty catalog as an upstream revocation.
// Keep the public T1 parser's non-empty distribution contract unchanged.
func parseCatalog(data []byte, allowEmpty bool) (*Catalog, []Diagnostic) {
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, []Diagnostic{diagnostic(InvalidJSON, "catalog.json", "invalid catalog JSON")}
	}
	if c.SchemaVersion != SchemaVersion {
		return nil, []Diagnostic{diagnostic(UnsupportedSchema, "schema_version", "unsupported catalog schema")}
	}
	if c.Repository.ID == "" || (!allowEmpty && len(c.Solutions) == 0) {
		return nil, []Diagnostic{diagnostic(InvalidCatalog, "catalog.json", "repository ID and solutions are required")}
	}
	var diagnostics []Diagnostic
	if err := VerifyCatalogDigest(c); err != nil {
		diagnostics = append(diagnostics, digestDiagnostic(err, "digest"))
	}
	seen := make(map[string]bool)
	for _, s := range c.Solutions {
		key := s.Key()
		if s.PackageID == "" || s.ID == "" || s.Version == "" || seen[key] {
			diagnostics = append(diagnostics, diagnostic(InvalidCatalog, key, "missing or duplicate solution identity"))
		}
		seen[key] = true
		if err := VerifySolutionDigest(s); err != nil {
			diagnostics = append(diagnostics, digestDiagnostic(err, key+"/digest"))
		}
		if len(s.Templates) == 0 {
			diagnostics = append(diagnostics, diagnostic(MissingTemplates, key, "solution has no templates"))
		}
		for _, t := range s.Templates {
			if !safePath(t.Path) || !safeToken(t.Role) || !safeToken(t.Locale) {
				diagnostics = append(diagnostics, diagnostic(UnsafePath, key, "unsafe template path, role or locale"))
			}
			if err := ValidateDigest(t.Digest); err != nil {
				diagnostics = append(diagnostics, digestDiagnostic(err, t.Path))
			}
		}
	}
	if len(diagnostics) > 0 {
		return nil, diagnostics
	}
	return &c, nil
}

func safeToken(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "/\\:\x00")
}

func safePath(value string) bool {
	return value != "." && fs.ValidPath(value) && !strings.ContainsAny(value, "\\:\x00") && !strings.Contains(value, "../")
}

// ContractPath implements the companion sidecar convention; it does not parse inputs.
func ContractPath(t Template) (string, error) {
	if !safePath(t.Path) || !safeToken(t.Role) || !safeToken(t.Locale) {
		return "", diagnostic(UnsafePath, t.Path, "unsafe template reference")
	}
	prefix, _, found := strings.Cut(t.Path, "/prompts/")
	if !found || prefix == "" {
		return "", diagnostic(UnsafePath, t.Path, "template must be below a solution prompts directory")
	}
	return path.Join(prefix, "contracts", t.Role+"."+t.Locale+".json"), nil
}

// LoadCatalog validates all references through os.Root, which confines filesystem
// resolution (including symlinks) to root. No fallback or network access is used.
func LoadCatalog(root string) (*Catalog, []Diagnostic) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, []Diagnostic{diagnostic(IOError, "catalog.json", "cannot open catalog root")}
	}
	defer r.Close()
	data, err := r.ReadFile("catalog.json")
	if err != nil {
		return nil, []Diagnostic{diagnostic(DanglingReference, "catalog.json", "catalog is missing or unreadable")}
	}
	c, diagnostics := ParseCatalog(data)
	if c == nil {
		return nil, diagnostics
	}
	for _, s := range c.Solutions {
		for _, t := range s.Templates {
			body, err := r.ReadFile(filepath.FromSlash(t.Path))
			if err != nil {
				diagnostics = append(diagnostics, diagnostic(DanglingReference, t.Path, "template is missing or inaccessible inside root"))
			} else if err := VerifyDigest(t.Digest, body); err != nil {
				diagnostics = append(diagnostics, digestDiagnostic(err, t.Path))
			}
			sidecar, err := ContractPath(t)
			if err != nil {
				diagnostics = append(diagnostics, err.(Diagnostic))
				continue
			}
			info, err := r.Stat(filepath.FromSlash(sidecar))
			if err != nil || !info.Mode().IsRegular() {
				diagnostics = append(diagnostics, diagnostic(DanglingReference, sidecar, "contract sidecar is missing or not a regular file"))
			}
		}
	}
	if len(diagnostics) > 0 {
		return nil, diagnostics
	}
	return c, nil
}
