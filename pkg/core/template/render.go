package template

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// RenderResult is the body-free result envelope for contract rendering. A
// fatal diagnostic always returns an empty RenderedText; callers must not send
// a result with diagnostics to an executor.
type RenderResult struct {
	RenderedText string       `json:"-"`
	Missing      []string     `json:"missing,omitempty"`
	Diagnostics  []Diagnostic `json:"diagnostics,omitempty"`
}

type renderToken struct {
	raw, name, valueDefault string
	options                 []string
	builtin                 bool
}

var renderPlaceholderRE = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// Render renders a validated English contract without ambient built-ins.
// Contract inputs are the only values that can be supplied to the body.
func Render(c Contract, body string, values map[string]string) RenderResult {
	return RenderContract(c, body, values, nil)
}

// RenderContract renders a contract body with explicit values and optional
// built-in values. It never falls back to permissive placeholder behavior:
// unknown placeholders, invalid inputs and missing required inputs fail closed
// with body-free structured diagnostics.
func RenderContract(c Contract, body any, values, builtins map[string]string) RenderResult {
	result := RenderResult{}
	bodyText, ok := renderBodyText(body)
	if !ok {
		result.Diagnostics = append(result.Diagnostics, diagnostic(InvalidBody, "body", "template body must be text"))
		return result
	}
	if c.Locale != "en" {
		result.Diagnostics = append(result.Diagnostics, diagnostic(LocaleUnsupported, "locale", "contract locale is not renderable"))
		return result
	}

	declared := make(map[string]InputDefinition, len(c.Inputs))
	fatal := false
	for _, in := range c.Inputs {
		if in.Name == "" || !ValidInputType(in.Type) {
			result.Diagnostics = append(result.Diagnostics, diagnostic(InputInvalid, "inputs", "contract contains an unsupported input definition"))
			fatal = true
			continue
		}
		if _, exists := declared[in.Name]; exists {
			result.Diagnostics = append(result.Diagnostics, diagnostic(DuplicateInput, in.Name, "contract input is declared more than once"))
			fatal = true
			continue
		}
		declared[in.Name] = in
	}

	tokens, parts, syntaxOK := parseRenderTemplate(bodyText)
	if !syntaxOK {
		result.Diagnostics = append(result.Diagnostics, diagnostic(InvalidBody, "body", "template contains an unterminated placeholder"))
		return result
	}
	seen := make(map[string]bool)
	for _, token := range tokens {
		if token.name == "" {
			continue
		}
		if in, exists := declared[token.name]; exists {
			if token.builtin && !seen[token.name] {
				// Contract inputs take precedence over same-name ambient values.
				result.Diagnostics = append(result.Diagnostics, diagnostic(BuiltinConflict, token.name, "contract input takes precedence over built-in placeholder"))
			}
			_ = in
			seen[token.name] = true
			continue
		}
		if !token.builtin {
			result.Diagnostics = append(result.Diagnostics, diagnostic(UndeclaredPlaceholder, token.name, "placeholder is not declared by the contract"))
			fatal = true
		}
	}

	resolved := make(map[string]string, len(declared))
	for _, in := range c.Inputs {
		if _, duplicate := resolved[in.Name]; duplicate {
			continue
		}
		value, supplied := values[in.Name]
		if !supplied || value == "" {
			value = in.Default
		}
		resolved[in.Name] = value
		if value == "" {
			if in.Required {
				result.Diagnostics = append(result.Diagnostics, diagnostic(MissingRequired, in.Name, "required input is missing"))
				fatal = true
			}
			continue
		}
		if rule := renderInputError(in, value); rule != "" {
			result.Diagnostics = append(result.Diagnostics, diagnostic(InputInvalid, in.Name, "input does not satisfy "+rule))
			fatal = true
		}
	}
	if fatal {
		return result
	}

	var rendered strings.Builder
	rendered.Grow(len(bodyText))
	for i, literal := range parts {
		rendered.WriteString(literal)
		if i >= len(tokens) {
			continue
		}
		token := tokens[i]
		if in, exists := declared[token.name]; exists {
			value := resolved[in.Name]
			if value == "" && !in.Required && !containsString(result.Missing, in.Name) {
				result.Missing = append(result.Missing, in.Name)
			}
			rendered.WriteString(value)
			continue
		}
		if token.builtin {
			rendered.WriteString(builtins[token.name])
			continue
		}
		rendered.WriteString(token.valueDefault)
	}
	result.RenderedText = rendered.String()
	return result
}

// RenderTemplate is an explicit alias for callers using template terminology.
func RenderTemplate(c Contract, body any, values map[string]string) RenderResult {
	return RenderContract(c, body, values, nil)
}

func renderBodyText(body any) (string, bool) {
	switch value := body.(type) {
	case string:
		return value, true
	case []byte:
		return string(value), true
	default:
		return "", false
	}
}

func parseRenderTemplate(body string) ([]renderToken, []string, bool) {
	matches := renderPlaceholderRE.FindAllStringSubmatchIndex(body, -1)
	tokens := make([]renderToken, 0, len(matches))
	parts := make([]string, 0, len(matches)+1)
	last := 0
	for _, match := range matches {
		parts = append(parts, body[last:match[0]])
		raw := strings.TrimSpace(body[match[2]:match[3]])
		tokens = append(tokens, parseRenderToken(raw))
		last = match[1]
	}
	parts = append(parts, body[last:])
	if strings.Count(body, "{{") != len(matches) || strings.Count(body, "}}") != len(matches) {
		return nil, nil, false
	}
	return tokens, parts, true
}

func parseRenderToken(raw string) renderToken {
	if i := strings.Index(raw, "="); i > 0 && strings.TrimSpace(raw[i+1:]) != "" {
		return renderToken{raw: raw, name: strings.TrimSpace(raw[:i]), valueDefault: strings.Trim(strings.TrimSpace(raw[i+1:]), "\"")}
	}
	if i := strings.IndexByte(raw, ':'); i > 0 {
		options := strings.Split(raw[i+1:], "|")
		for j := range options {
			options[j] = strings.TrimSpace(options[j])
		}
		if len(options) > 0 && options[0] != "" {
			return renderToken{raw: raw, name: strings.TrimSpace(raw[:i]), options: options, valueDefault: options[0]}
		}
	}
	name := strings.TrimSpace(raw)
	return renderToken{raw: raw, name: name, builtin: isRenderBuiltin(name)}
}

func isRenderBuiltin(name string) bool {
	switch name {
	case "selection", "clipboard", "app", "url", "window_title", "date", "time", "datetime", "glossary", "cursor":
		return true
	default:
		return false
	}
}

func renderInputError(in InputDefinition, value string) string {
	if len(in.Enum) != 0 && !containsString(in.Enum, value) {
		return "enum"
	}
	switch strings.ToLower(in.Type) {
	case "string", "enum":
	case "integer":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return "integer"
		}
		if !renderNumberBounds(float64(n), in) {
			return "range"
		}
	case "number":
		n, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return "number"
		}
		if !renderNumberBounds(n, in) {
			return "range"
		}
	case "boolean":
		if value != "true" && value != "false" {
			return "boolean"
		}
	default:
		return "type"
	}
	length := utf8.RuneCountInString(value)
	if in.MinLength != nil && length < *in.MinLength {
		return "min_length"
	}
	if in.MaxLength != nil && length > *in.MaxLength {
		return "max_length"
	}
	if in.Regex != "" {
		re, err := regexp.Compile(in.Regex)
		if err != nil || !re.MatchString(value) {
			return "regex"
		}
	}
	return ""
}

func renderNumberBounds(value float64, in InputDefinition) bool {
	return (in.Min == nil || value >= *in.Min) && (in.Max == nil || value <= *in.Max)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// RenderDiagnosticString is a stable human-readable representation for a
// structured diagnostic. It intentionally contains no rendered body or input.
func RenderDiagnosticString(d Diagnostic) string {
	if d.Path == "" {
		return fmt.Sprintf("%s: %s", d.Code, d.Message)
	}
	return fmt.Sprintf("%s: %s: %s", d.Code, d.Path, d.Message)
}
