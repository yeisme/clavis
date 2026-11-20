package template

import (
	"encoding/json"
	"sort"
	"strings"
)

// InputDefinition describes one value accepted by a template contract. The
// declaration order is part of the contract digest protocol; do not reorder
// fields without changing the schema version.
type InputDefinition struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Required     bool              `json:"required,omitempty"`
	Default      string            `json:"default,omitempty"`
	Examples     []string          `json:"examples,omitempty"`
	Enum         []string          `json:"enum,omitempty"`
	Regex        string            `json:"regex,omitempty"`
	Min          *float64          `json:"min,omitempty"`
	Max          *float64          `json:"max,omitempty"`
	MinLength    *int              `json:"min_length,omitempty"`
	MaxLength    *int              `json:"max_length,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Descriptions map[string]string `json:"descriptions,omitempty"`
	Sensitivity  string            `json:"sensitivity,omitempty"`
}

// Contract is the sidecar execution contract for one catalog template. The
// declaration order mirrors the canonical JSON representation.
type Contract struct {
	SchemaVersion  string            `json:"schema_version"`
	PackageID      string            `json:"package_id"`
	SolutionID     string            `json:"solution_id"`
	Version        string            `json:"version"`
	Role           string            `json:"role"`
	Locale         string            `json:"locale"`
	TemplatePath   string            `json:"template_path"`
	TemplateDigest string            `json:"template_digest"`
	Digest         string            `json:"digest"`
	License        string            `json:"license"`
	Permissions    []string          `json:"permissions,omitempty"`
	Inputs         []InputDefinition `json:"inputs,omitempty"`
}

// ParseContract parses and verifies a complete contract sidecar.
func ParseContract(data []byte) (*Contract, []Diagnostic) {
	var c Contract
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, []Diagnostic{diagnostic(InvalidJSON, "contract", "invalid contract JSON")}
	}
	if c.SchemaVersion != ContractSchema {
		return nil, []Diagnostic{diagnostic(UnknownSchema, "schema_version", "unsupported contract schema")}
	}
	for _, in := range c.Inputs {
		if in.Name == "" || !ValidInputType(in.Type) {
			return nil, []Diagnostic{diagnostic(InvalidCatalog, "inputs", "contract contains an unsupported input definition")}
		}
	}
	if err := VerifyContractDigest(c); err != nil {
		return nil, []Diagnostic{digestDiagnostic(err, "digest")}
	}
	return &c, nil
}

// CanonicalContractDigest computes the digest over the canonical contract
// representation. Sorting is done on copies, so this function never mutates c.
func CanonicalContractDigest(c Contract) (string, error) {
	c.Digest = ""
	if c.Permissions != nil {
		c.Permissions = append([]string(nil), c.Permissions...)
		sort.Strings(c.Permissions)
	}
	if c.Inputs != nil {
		c.Inputs = append([]InputDefinition(nil), c.Inputs...)
		sort.SliceStable(c.Inputs, func(i, j int) bool {
			return c.Inputs[i].Name < c.Inputs[j].Name
		})
	}
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return Digest(data), nil
}

// VerifyContractDigest verifies the declared canonical contract digest.
func VerifyContractDigest(c Contract) error {
	if err := ValidateDigest(c.Digest); err != nil {
		return err
	}
	actual, err := CanonicalContractDigest(c)
	if err != nil || actual != c.Digest {
		return diagnostic(DigestMismatch, "", "contract does not match declared digest")
	}
	return nil
}

// ValidateContractBinding verifies the identity and body binding between a
// contract and its catalog records. A solution digest is deliberately not
// compared: solution, template, and contract digests are independent layers.
func ValidateContractBinding(c Contract, s Solution, t Template, body []byte) []Diagnostic {
	var out []Diagnostic
	if c.SchemaVersion != ContractSchema {
		out = append(out, diagnostic(UnknownSchema, "schema_version", "unsupported contract schema"))
	}
	if err := VerifyContractDigest(c); err != nil {
		out = append(out, digestDiagnostic(err, "digest"))
	}
	if c.TemplateDigest != t.Digest {
		out = append(out, diagnostic(DigestMismatch, "template_digest", "contract does not bind catalog template digest"))
	}
	if err := VerifyDigest(t.Digest, body); err != nil {
		out = append(out, digestDiagnostic(err, "template_digest"))
	}
	pairs := [][3]string{
		{"package_id", c.PackageID, s.PackageID},
		{"solution_id", c.SolutionID, s.ID},
		{"version", c.Version, s.Version},
		{"role", c.Role, t.Role},
		{"locale", c.Locale, t.Locale},
		{"template_path", c.TemplatePath, t.Path},
	}
	for _, pair := range pairs {
		if pair[1] != pair[2] {
			out = append(out, diagnostic(UpstreamMismatch, pair[0], "contract identity differs from catalog template"))
		}
	}
	return out
}

// ContractInput returns an input by name. It is useful to consumers which
// need to inspect a contract without depending on its JSON representation.
func (c Contract) ContractInput(name string) (InputDefinition, bool) {
	for _, in := range c.Inputs {
		if in.Name == name {
			return in, true
		}
	}
	return InputDefinition{}, false
}

// ValidInputType reports whether a contract input can be rendered by Clavis.
func ValidInputType(kind string) bool {
	switch strings.ToLower(kind) {
	case "string", "enum", "integer", "number", "boolean":
		return true
	default:
		return false
	}
}
