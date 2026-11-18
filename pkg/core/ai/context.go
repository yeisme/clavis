package ai

import (
	"regexp"
	"strings"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// ContextPolicy is the single allowlist for ambient context sent to a model.
// An all-zero policy means legacy callers keep the old behavior; an explicit
// non-zero policy owns every ambient field it permits.
type ContextPolicy struct {
	IncludeGlossary     bool `json:"includeGlossary"`
	IncludeApp          bool `json:"includeApp"`
	IncludeURL          bool `json:"includeURL"`
	IncludeSelection    bool `json:"includeSelection"`
	IncludeClipboard    bool `json:"includeClipboard"`
	IncludeMCPResources bool `json:"includeMCPResources"`
	MaxSelectionRunes   int  `json:"maxSelectionRunes,omitempty"`
	MaxClipboardRunes   int  `json:"maxClipboardRunes,omitempty"`
	MaxMCPResourceRunes int  `json:"maxMCPResourceRunes,omitempty"`
}

type ContextBundle struct {
	Glossary     []config.GlossaryTerm `json:"glossary,omitempty"`
	AppName      string                `json:"appName,omitempty"`
	URL          string                `json:"url,omitempty"`
	Selection    string                `json:"selection,omitempty"`
	Clipboard    string                `json:"clipboard,omitempty"`
	MCPResources []string              `json:"mcpResources,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type MessageSet struct {
	System    []Message `json:"system,omitempty"`
	Developer []Message `json:"developer,omitempty"`
	User      []Message `json:"user,omitempty"`
}

func trimRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

var (
	mcpPrivateKeyPattern = regexp.MustCompile(`(?is)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`)
	mcpBearerPattern     = regexp.MustCompile(`(?i)(\b(?:authorization\s*:\s*bearer|bearer)\s+)[^\s,;]+`)
	mcpJSONBearerPattern = regexp.MustCompile(`(?i)("authorization"\s*:\s*")bearer\s+[^"]*(")`)
	mcpSecretPattern     = regexp.MustCompile(`(?i)((?:"|')?(?:api[_-]?key|access[_-]?token|token|secret|password|passwd)(?:"|')?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;&]+)`)
)

func sanitizeMCPResource(raw string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = 2048
	}
	clean := mcpPrivateKeyPattern.ReplaceAllString(raw, "[REDACTED PRIVATE KEY]")
	clean = mcpJSONBearerPattern.ReplaceAllString(clean, "${1}[REDACTED]${2}")
	clean = mcpBearerPattern.ReplaceAllString(clean, "${1}[REDACTED]")
	clean = mcpSecretPattern.ReplaceAllString(clean, "${1}[REDACTED]")
	return strings.TrimSpace(trimRunes(clean, maxRunes))
}

func sanitizeMCPResources(resources []string, maxRunes int) []string {
	const maxResources = 32
	const maxTotalRunes = 16384
	out := make([]string, 0, len(resources))
	total := 0
	for _, raw := range resources {
		if len(out) >= maxResources || total >= maxTotalRunes {
			break
		}
		clean := sanitizeMCPResource(raw, maxRunes)
		if clean == "" {
			continue
		}
		clean = trimRunes(clean, maxTotalRunes-total)
		out = append(out, clean)
		total += len([]rune(clean))
	}
	return out
}

func ApplyContextPolicy(policy ContextPolicy, bundle ContextBundle, system string, developer string, user string) MessageSet {
	var context []string
	if policy.IncludeApp && bundle.AppName != "" {
		context = append(context, "App: "+bundle.AppName)
	}
	if policy.IncludeURL && bundle.URL != "" {
		context = append(context, "URL: "+bundle.URL)
	}
	if policy.IncludeSelection && bundle.Selection != "" {
		context = append(context, "Selection:\n"+trimRunes(bundle.Selection, policy.MaxSelectionRunes))
	}
	if policy.IncludeClipboard && bundle.Clipboard != "" {
		context = append(context, "Clipboard:\n"+trimRunes(bundle.Clipboard, policy.MaxClipboardRunes))
	}
	if policy.IncludeMCPResources && len(bundle.MCPResources) > 0 {
		if resources := sanitizeMCPResources(bundle.MCPResources, policy.MaxMCPResourceRunes); len(resources) > 0 {
			context = append(context, "MCP resources:\n"+strings.Join(resources, "\n"))
		}
	}
	if policy.IncludeGlossary && len(bundle.Glossary) > 0 {
		var terms []string
		for _, term := range bundle.Glossary {
			if term.Term != "" {
				terms = append(terms, term.Term+": "+term.Hint)
			}
		}
		if len(terms) > 0 {
			context = append(context, "Glossary:\n"+strings.Join(terms, "\n"))
		}
	}
	if len(context) > 0 {
		system = strings.TrimSpace(system + "\n\n<context>\n" + strings.Join(context, "\n") + "\n</context>")
	}
	out := MessageSet{}
	if strings.TrimSpace(system) != "" {
		out.System = []Message{{Role: "system", Content: system}}
	}
	if strings.TrimSpace(developer) != "" {
		out.Developer = []Message{{Role: "developer", Content: developer}}
	}
	if strings.TrimSpace(user) != "" {
		out.User = []Message{{Role: "user", Content: user}}
	}
	return out
}

func ContextBundleFromCapsule(c *engine.CapsuleContext) ContextBundle {
	if c == nil {
		return ContextBundle{}
	}
	return ContextBundle{AppName: c.AppName, URL: c.ActiveURL, Selection: c.SelectedText, Clipboard: c.ClipboardPreview}
}
