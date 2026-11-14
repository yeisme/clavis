// Package prompt provides fast, deterministic prompt templates and composition.
package prompt

import (
	"regexp"
	"strings"

	"github.com/yeisme/clavis/pkg/core/config"
)

type placeholder struct {
	raw, name, kind, def string
	options              []string
}

var placeholderRE = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// ParsedTemplate is the compiled representation of a prompt template.
type ParsedTemplate struct {
	Parts  []string
	Tokens []Placeholder
}

// Placeholder is a token found in a template.
type Placeholder struct {
	Name, Kind, Default string
	Options             []string
	BuiltIn             bool
}

// Compile is an alias for ParseTemplate for callers that cache templates.
func Compile(text string) ParsedTemplate { return ParseTemplate(text) }

// ParseTemplate compiles a template in one linear regexp pass.
func ParseTemplate(text string) ParsedTemplate {
	ms := placeholderRE.FindAllStringSubmatchIndex(text, -1)
	p := ParsedTemplate{Parts: make([]string, 0, len(ms)+1), Tokens: make([]Placeholder, 0, len(ms))}
	last := 0
	for _, m := range ms {
		p.Parts = append(p.Parts, text[last:m[0]])
		raw := strings.TrimSpace(text[m[2]:m[3]])
		x := parseToken(raw)
		p.Tokens = append(p.Tokens, x)
		last = m[1]
	}
	p.Parts = append(p.Parts, text[last:])
	return p
}
func parseToken(raw string) Placeholder {
	if i := strings.Index(raw, "="); i > 0 && strings.TrimSpace(raw[i+1:]) != "" {
		return Placeholder{Name: strings.TrimSpace(raw[:i]), Kind: "text", Default: strings.Trim(strings.TrimSpace(raw[i+1:]), "\"")}
	}
	if i := strings.IndexByte(raw, ':'); i > 0 {
		opts := strings.Split(raw[i+1:], "|")
		for i := range opts {
			opts[i] = strings.TrimSpace(opts[i])
		}
		if len(opts) > 0 && opts[0] != "" {
			return Placeholder{Name: strings.TrimSpace(raw[:i]), Kind: "select", Options: opts, Default: opts[0]}
		}
	}
	return Placeholder{Name: raw, BuiltIn: isBuiltIn(raw)}
}
func isBuiltIn(n string) bool {
	switch n {
	case "selection", "clipboard", "app", "url", "window_title", "date", "time", "datetime", "glossary", "cursor":
		return true
	}
	return false
}

// Slots returns explicit slots merged with shorthand slots in first-use order.
func (p ParsedTemplate) Slots(t config.PromptTemplate) []config.PromptSlot {
	out := make([]config.PromptSlot, 0, len(t.Slots)+len(p.Tokens))
	seen := map[string]bool{}
	for _, s := range t.Slots {
		if s.Name == "" || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		if s.Kind == "" {
			s.Kind = "text"
		}
		out = append(out, s)
	}
	for _, x := range p.Tokens {
		if x.BuiltIn || x.Name == "" || seen[x.Name] {
			continue
		}
		seen[x.Name] = true
		kind := x.Kind
		if kind == "" {
			kind = "text"
		}
		required := kind == "text"
		out = append(out, config.PromptSlot{Name: x.Name, Label: x.Name, Kind: kind, Default: x.Default, Options: x.Options, Required: required})
	}
	return out
}
