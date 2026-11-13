package engine

import (
	"sort"
	"strings"
	"unicode"

	"github.com/yeisme/clavis/pkg/core/config"
)

// GlossaryEngine resolves relevant domain glossary terms and normalizes aliases.
type GlossaryEngine struct {
	Terms []config.GlossaryTerm
}

// NewGlossaryEngine constructs a GlossaryEngine from terms.
func NewGlossaryEngine(terms []config.GlossaryTerm) GlossaryEngine {
	copied := make([]config.GlossaryTerm, len(terms))
	copy(copied, terms)
	return GlossaryEngine{Terms: copied}
}

// NewGlossaryEngineFromConfig constructs a GlossaryEngine from GlossaryConfig.
func NewGlossaryEngineFromConfig(cfg config.GlossaryConfig) GlossaryEngine {
	return NewGlossaryEngine(cfg.Terms)
}

// RelevantTerms returns terms whose canonical name or alias appears as a whole word in text/context.
// Pass maxTerms <= 0 for no limit.
func (g GlossaryEngine) RelevantTerms(text, context string, maxTerms int) []config.GlossaryTerm {
	combined := text
	if context != "" {
		combined = text + "\n" + context
	}
	if strings.TrimSpace(combined) == "" {
		return nil
	}

	var matched []config.GlossaryTerm
	for _, entry := range g.Terms {
		candidates := make([]string, 0, 1+len(entry.Aliases))
		candidates = append(candidates, entry.Term)
		candidates = append(candidates, entry.Aliases...)

		hit := false
		for _, cand := range candidates {
			trimmed := strings.TrimSpace(cand)
			if trimmed == "" {
				continue
			}
			if containsTokenRunes(trimmed, combined, true, true) {
				hit = true
				break
			}
		}
		if hit {
			matched = append(matched, entry)
			if maxTerms > 0 && len(matched) >= maxTerms {
				break
			}
		}
	}
	return matched
}

// FormatPromptBlock formats glossary terms into a deterministic <glossary> XML block.
func (GlossaryEngine) FormatPromptBlock(terms []config.GlossaryTerm) string {
	if len(terms) == 0 {
		return ""
	}
	lines := make([]string, 0, len(terms)+2)
	lines = append(lines, "<glossary>")
	for _, entry := range terms {
		var sb strings.Builder
		sb.WriteString("- ")
		sb.WriteString(entry.Term)
		if cat := strings.TrimSpace(entry.Category); cat != "" {
			sb.WriteString(" [")
			sb.WriteString(cat)
			sb.WriteString("]")
		}
		var aliases []string
		for _, a := range entry.Aliases {
			if ta := strings.TrimSpace(a); ta != "" {
				aliases = append(aliases, ta)
			}
		}
		if len(aliases) > 0 {
			sb.WriteString(" (aliases: ")
			sb.WriteString(strings.Join(aliases, ", "))
			sb.WriteString(")")
		}
		if hint := strings.TrimSpace(entry.Hint); hint != "" {
			sb.WriteString(": ")
			sb.WriteString(hint)
		}
		lines = append(lines, sb.String())
	}
	lines = append(lines, "</glossary>")
	return strings.Join(lines, "\n")
}

// InjectIntoSystemPrompt appends a <glossary> block for relevant terms to systemPrompt.
func (g GlossaryEngine) InjectIntoSystemPrompt(systemPrompt, text, context string, maxTerms int) string {
	selected := g.RelevantTerms(text, context, maxTerms)
	block := g.FormatPromptBlock(selected)
	if block == "" {
		return systemPrompt
	}
	if strings.TrimSpace(systemPrompt) == "" {
		return block
	}
	return systemPrompt + "\n\n" + block
}

// NormalizeAliases replaces glossary aliases in text with canonical terms using longest-first matching.
func (g GlossaryEngine) NormalizeAliases(text string, caseInsensitive, wholeWord bool) string {
	if text == "" || len(g.Terms) == 0 {
		return text
	}

	type aliasMapping struct {
		aliasRunes []rune
		canonical  string
		origIndex  int
	}

	var mappings []aliasMapping
	idxCounter := 0
	for _, entry := range g.Terms {
		if entry.Term == "" {
			continue
		}
		for _, alias := range entry.Aliases {
			if alias == "" {
				continue
			}
			mappings = append(mappings, aliasMapping{
				aliasRunes: []rune(alias),
				canonical:  entry.Term,
				origIndex:  idxCounter,
			})
			idxCounter++
		}
	}
	if len(mappings) == 0 {
		return text
	}

	sort.SliceStable(mappings, func(i, j int) bool {
		if len(mappings[i].aliasRunes) != len(mappings[j].aliasRunes) {
			return len(mappings[i].aliasRunes) > len(mappings[j].aliasRunes)
		}
		return mappings[i].origIndex < mappings[j].origIndex
	})

	textRunes := []rune(text)
	var out strings.Builder
	out.Grow(len(text))

	i := 0
	for i < len(textRunes) {
		matched := false
		for _, m := range mappings {
			alen := len(m.aliasRunes)
			if i+alen > len(textRunes) {
				continue
			}
			slice := textRunes[i : i+alen]
			if !runesEqual(slice, m.aliasRunes, caseInsensitive) {
				continue
			}
			if wholeWord && !satisfiesWordBoundaries(textRunes, i, i+alen) {
				continue
			}
			out.WriteString(m.canonical)
			i += alen
			matched = true
			break
		}
		if !matched {
			out.WriteRune(textRunes[i])
			i++
		}
	}
	return out.String()
}

func containsTokenRunes(token, text string, caseInsensitive, wholeWord bool) bool {
	tokRunes := []rune(token)
	txtRunes := []rune(text)
	if len(tokRunes) == 0 || len(tokRunes) > len(txtRunes) {
		return false
	}
	limit := len(txtRunes) - len(tokRunes)
	for i := 0; i <= limit; i++ {
		if runesEqual(txtRunes[i:i+len(tokRunes)], tokRunes, caseInsensitive) {
			if !wholeWord || satisfiesWordBoundaries(txtRunes, i, i+len(tokRunes)) {
				return true
			}
		}
	}
	return false
}

func runesEqual(a, b []rune, caseInsensitive bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] == b[i] {
			continue
		}
		if caseInsensitive && unicode.ToLower(a[i]) == unicode.ToLower(b[i]) {
			continue
		}
		return false
	}
	return true
}

func satisfiesWordBoundaries(text []rune, start, end int) bool {
	if start >= end {
		return false
	}
	first := text[start]
	last := text[end-1]
	if isASCIIWordRune(first) && start > 0 && isASCIIWordRune(text[start-1]) {
		return false
	}
	if isASCIIWordRune(last) && end < len(text) && isASCIIWordRune(text[end]) {
		return false
	}
	return true
}

func isASCIIWordRune(r rune) bool {
	return (r >= '0' && r <= '9') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= 'a' && r <= 'z') ||
		r == '_'
}
