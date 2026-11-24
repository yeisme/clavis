package template

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

var bcp47LocaleRE = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{1,8})*$`)

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
	SchemaVersion    string            `json:"schema_version"`
	PackageID        string            `json:"package_id"`
	SolutionID       string            `json:"solution_id"`
	Version          string            `json:"version"`
	Role             string            `json:"role"`
	Locale           string            `json:"locale"`
	TemplatePath     string            `json:"template_path"`
	TemplateDigest   string            `json:"template_digest"`
	Digest           string            `json:"digest"`
	License          string            `json:"license"`
	Permissions      []string          `json:"permissions,omitempty"`
	Inputs           []InputDefinition `json:"inputs,omitempty"`
	SupportedLocales []string          `json:"supported_locales,omitempty"`
}

// NormalizeLocale validates a BCP-47-like non-empty locale tag (e.g. "en",
// "zh", "zh-CN", "zh-Hans-CN", "ja-JP") and returns its canonical casing.
func NormalizeLocale(locale string) (string, bool) {
	trimmed := strings.TrimSpace(locale)
	if trimmed == "" || !bcp47LocaleRE.MatchString(trimmed) {
		return "", false
	}
	parts := strings.Split(trimmed, "-")
	parts[0] = strings.ToLower(parts[0])
	for i := 1; i < len(parts); i++ {
		sub := parts[i]
		if len(sub) == 4 && isASCIIAlpha(sub) {
			parts[i] = strings.ToUpper(sub[:1]) + strings.ToLower(sub[1:])
		} else if len(sub) == 2 && isASCIIAlpha(sub) {
			parts[i] = strings.ToUpper(sub)
		} else {
			parts[i] = strings.ToLower(sub)
		}
	}
	return strings.Join(parts, "-"), true
}

func isASCIIAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return len(s) > 0
}

// ValidLocale reports whether locale is a valid BCP-47-like tag.
func ValidLocale(locale string) bool {
	_, ok := NormalizeLocale(locale)
	return ok
}

// LocalesMatch reports whether a and b are valid BCP-47 locales that match
// case-insensitively after normalization.
func LocalesMatch(a, b string) bool {
	na, okA := NormalizeLocale(a)
	nb, okB := NormalizeLocale(b)
	return okA && okB && strings.EqualFold(na, nb)
}

// LocaleRenderable reports whether the contract's locale is valid and allowed
// by its declared SupportedLocales (if any).
func (c Contract) LocaleRenderable() bool {
	norm, ok := NormalizeLocale(c.Locale)
	if !ok {
		return false
	}
	if len(c.SupportedLocales) == 0 {
		return true
	}
	for _, allowed := range c.SupportedLocales {
		if LocalesMatch(allowed, norm) {
			return true
		}
	}
	return false
}

// SupportsLocale reports whether this contract can render for requestedLocale.
// An empty requestedLocale checks only the contract's own locale validity.
func (c Contract) SupportsLocale(requestedLocale string) bool {
	if !c.LocaleRenderable() {
		return false
	}
	if strings.TrimSpace(requestedLocale) == "" {
		return true
	}
	normReq, ok := NormalizeLocale(requestedLocale)
	if !ok {
		return false
	}
	if len(c.SupportedLocales) > 0 {
		for _, allowed := range c.SupportedLocales {
			if LocalesMatch(allowed, normReq) {
				return true
			}
		}
		return false
	}
	return LocalesMatch(c.Locale, normReq)
}

// LocalizedLabel returns the best localized label for locale from Labels,
// falling back to primary language subtag, "en", and finally in.Name.
func (in InputDefinition) LocalizedLabel(locale string) string {
	if v := lookupLocalizedMap(in.Labels, locale); v != "" {
		return v
	}
	return in.Name
}

// LocalizedDescription returns the best localized description for locale from
// Descriptions, falling back to primary language subtag, "en", or empty string.
func (in InputDefinition) LocalizedDescription(locale string) string {
	return lookupLocalizedMap(in.Descriptions, locale)
}

func lookupLocalizedMap(m map[string]string, locale string) string {
	if len(m) == 0 {
		return ""
	}
	if v := strings.TrimSpace(m[locale]); v != "" {
		return v
	}
	norm, ok := NormalizeLocale(locale)
	if ok {
		if v := strings.TrimSpace(m[norm]); v != "" {
			return v
		}
		for k, v := range m {
			if LocalesMatch(k, norm) && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		if base, _, hasDash := strings.Cut(norm, "-"); hasDash {
			for k, v := range m {
				if LocalesMatch(k, base) && strings.TrimSpace(v) != "" {
					return strings.TrimSpace(v)
				}
			}
		}
	}
	if v := strings.TrimSpace(m["en"]); v != "" {
		return v
	}
	return ""
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
	if !c.LocaleRenderable() {
		return nil, []Diagnostic{diagnostic(LocaleUnsupported, "locale", "contract locale is not renderable")}
	}
	seenInputs := make(map[string]bool, len(c.Inputs))
	for _, in := range c.Inputs {
		if in.Name == "" || !ValidInputType(in.Type) {
			return nil, []Diagnostic{diagnostic(InvalidCatalog, "inputs", "contract contains an unsupported input definition")}
		}
		if seenInputs[in.Name] {
			return nil, []Diagnostic{diagnostic(DuplicateInput, in.Name, "contract input is declared more than once")}
		}
		seenInputs[in.Name] = true
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
	if c.SupportedLocales != nil {
		c.SupportedLocales = append([]string(nil), c.SupportedLocales...)
		sort.Strings(c.SupportedLocales)
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
