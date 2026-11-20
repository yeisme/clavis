package prompt

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/template"
)

// ContractDiagnostic is a safe, user-facing contract validation diagnostic.
// It intentionally contains a name and rule, but never the supplied value.
type ContractDiagnostic struct {
	Code    string `json:"code"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
}

func (d *ContractDiagnostic) Error() string {
	if d == nil {
		return ""
	}
	if d.Name == "" {
		return d.Code + ": " + d.Message
	}
	return d.Code + ":" + d.Name + ": " + d.Message
}

// ContractRenderResult is the result of strict contract rendering. A non-empty
// Diagnostics means rendering was rejected and RenderedText is empty.
type ContractRenderResult struct {
	RenderedText     string
	CursorBackOffset int
	Missing          []string
	MissingRequired  []string
	Diagnostics      []ContractDiagnostic
}

// PromptSlotsFromContract maps contract input declarations to UI slots. The
// order is contract order (which is normally canonicalized by name).
func PromptSlotsFromContract(c template.Contract) []config.PromptSlot {
	out := make([]config.PromptSlot, 0, len(c.Inputs))
	for _, in := range c.Inputs {
		s := config.PromptSlot{Name: in.Name, Label: in.Name, Default: in.Default, Required: in.Required}
		switch strings.ToLower(in.Type) {
		case "string", "enum":
			s.Kind = "text"
			if len(in.Enum) != 0 {
				s.Kind, s.Options = "select", append([]string(nil), in.Enum...)
			}
		case "integer", "number":
			s.Kind = "number"
		case "boolean":
			s.Kind = "select"
			s.Options = []string{"true", "false"}
		default:
			s.Kind = "text"
		}
		out = append(out, s)
	}
	return out
}

// ContractToPromptSlots is a descriptive alias for PromptSlotsFromContract.
func ContractToPromptSlots(c template.Contract) []config.PromptSlot {
	return PromptSlotsFromContract(c)
}

// ValidateContractInputs validates values without rendering a body. Diagnostics
// are safe to log and never contain any user-provided value.
func ValidateContractInputs(c template.Contract, values map[string]string) []ContractDiagnostic {
	_, diagnostics := validateContractValues(c, values)
	return diagnostics
}

// RenderContractPrompt strictly renders a body against c. Unlike RenderPrompt,
// this API rejects undeclared placeholders and invalid input values.
func RenderContractPrompt(c template.Contract, body any, values map[string]string, ctx RenderContext) ContractRenderResult {
	result := ContractRenderResult{}
	bodyText, ok := contractBodyText(body)
	if !ok {
		result.Diagnostics = append(result.Diagnostics, ContractDiagnostic{Code: "invalid_body", Message: "template body must be text"})
		return result
	}
	if c.Locale != "en" {
		result.Diagnostics = append(result.Diagnostics, ContractDiagnostic{Code: "locale_unsupported", Message: "contract locale is not renderable"})
		return result
	}
	p := ParseTemplate(bodyText)
	declared := make(map[string]template.InputDefinition, len(c.Inputs))
	for _, in := range c.Inputs {
		if _, exists := declared[in.Name]; exists {
			result.Diagnostics = append(result.Diagnostics, ContractDiagnostic{Code: "duplicate_input", Name: in.Name, Message: "contract input is declared more than once"})
			continue
		}
		declared[in.Name] = in
	}
	seen := map[string]bool{}
	for _, x := range p.Tokens {
		if x.Name == "" {
			continue
		}
		if in, ok := declared[x.Name]; ok {
			if x.BuiltIn && !seen[x.Name] {
				result.Diagnostics = append(result.Diagnostics, ContractDiagnostic{Code: "builtin_conflict", Name: x.Name, Message: "contract input takes precedence over built-in placeholder"})
			}
			_ = in
			seen[x.Name] = true
			continue
		}
		if !x.BuiltIn {
			result.Diagnostics = append(result.Diagnostics, ContractDiagnostic{Code: "undeclared_placeholder", Name: x.Name, Message: "placeholder is not declared by the contract"})
		}
	}
	if _, ds := validateContractValues(c, values); len(ds) != 0 {
		result.Diagnostics = append(result.Diagnostics, ds...)
	}
	fatal := false
	for _, d := range result.Diagnostics {
		if d.Code != "builtin_conflict" {
			fatal = true
			break
		}
	}
	if fatal {
		return result
	}

	var b strings.Builder
	b.Grow(len(bodyText))
	cursor := -1
	for i, lit := range p.Parts {
		b.WriteString(lit)
		if i >= len(p.Tokens) {
			continue
		}
		x := p.Tokens[i]
		v, provided := values[x.Name]
		if in, ok := declared[x.Name]; ok {
			if !provided || v == "" {
				v = in.Default
			}
			if v == "" && !in.Required && !seenMissing(result.Missing, in.Name) {
				result.Missing = append(result.Missing, in.Name)
			}
		} else if x.BuiltIn {
			v = ctx.value(x.Name)
		} else if !provided {
			v = x.Default
		}
		if x.Name == "cursor" && declared[x.Name].Name == "" {
			cursor = b.Len()
			v = ctx.value("cursor")
		}
		b.WriteString(v)
	}
	result.RenderedText = b.String()
	if cursor >= 0 {
		result.CursorBackOffset = utf8.RuneCountInString(result.RenderedText) - utf8.RuneCountInString(result.RenderedText[:cursor])
	}
	return result
}

// RenderPromptWithContract is an alternate name for callers migrating from
// the permissive RenderPrompt API.
func RenderPromptWithContract(c template.Contract, body any, values map[string]string, ctx RenderContext) ContractRenderResult {
	return RenderContractPrompt(c, body, values, ctx)
}

// RenderContractTemplate is an explicit alias for callers that prefer the
// template-oriented name. body may be a string, []byte, or PromptTemplate.
func RenderContractTemplate(c template.Contract, body any, values map[string]string, ctx RenderContext) ContractRenderResult {
	return RenderContractPrompt(c, body, values, ctx)
}

func contractBodyText(body any) (string, bool) {
	switch value := body.(type) {
	case string:
		return value, true
	case []byte:
		return string(value), true
	case config.PromptTemplate:
		return value.Template, true
	case *config.PromptTemplate:
		if value == nil {
			return "", false
		}
		return value.Template, true
	default:
		return "", false
	}
}

func seenMissing(values []string, name string) bool {
	for _, value := range values {
		if value == name {
			return true
		}
	}
	return false
}

func validateContractValues(c template.Contract, values map[string]string) (map[string]string, []ContractDiagnostic) {
	resolved := make(map[string]string, len(c.Inputs))
	var out []ContractDiagnostic
	seen := map[string]bool{}
	for _, in := range c.Inputs {
		if in.Name == "" {
			out = append(out, ContractDiagnostic{Code: "input_invalid", Message: "contract input name cannot be empty"})
			continue
		}
		if seen[in.Name] {
			continue
		}
		seen[in.Name] = true
		v, supplied := values[in.Name]
		if !supplied || v == "" {
			v = in.Default
		}
		resolved[in.Name] = v
		if v == "" {
			if in.Required {
				out = append(out, ContractDiagnostic{Code: "missing_required", Name: in.Name, Message: "required input is missing"})
			}
			continue
		}
		if d := validateInput(in, v); d != nil {
			out = append(out, *d)
		}
	}
	return resolved, out
}

func validateInput(in template.InputDefinition, value string) *ContractDiagnostic {
	invalid := func(rule string) *ContractDiagnostic {
		return &ContractDiagnostic{Code: "input_invalid", Name: in.Name, Message: fmt.Sprintf("input does not satisfy %s", rule)}
	}
	if len(in.Enum) != 0 {
		found := false
		for _, option := range in.Enum {
			if value == option {
				found = true
				break
			}
		}
		if !found {
			return invalid("enum")
		}
	}
	switch strings.ToLower(in.Type) {
	case "string", "enum":
		// String constraints below are intentionally rune based for user text.
	case "integer":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return invalid("integer")
		}
		if !numberBounds(float64(n), in) {
			return invalid("range")
		}
	case "number":
		n, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return invalid("number")
		}
		if !numberBounds(n, in) {
			return invalid("range")
		}
	case "boolean":
		if value != "true" && value != "false" {
			return invalid("boolean")
		}
	default:
		return invalid("type")
	}
	length := utf8.RuneCountInString(value)
	if in.MinLength != nil && length < *in.MinLength {
		return invalid("min_length")
	}
	if in.MaxLength != nil && length > *in.MaxLength {
		return invalid("max_length")
	}
	if in.Regex != "" {
		re, err := regexp.Compile(in.Regex)
		if err != nil || !re.MatchString(value) {
			return invalid("regex")
		}
	}
	return nil
}

func numberBounds(value float64, in template.InputDefinition) bool {
	return (in.Min == nil || value >= *in.Min) && (in.Max == nil || value <= *in.Max)
}
