package ai

import (
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"strings"
)

// ContextPolicy is the single allowlist for ambient context sent to a model.
// Empty fields are omitted; callers can therefore apply least privilege per request.
type ContextPolicy struct {
	IncludeGlossary     bool `json:"includeGlossary"`
	IncludeApp          bool `json:"includeApp"`
	IncludeURL          bool `json:"includeURL"`
	IncludeSelection    bool `json:"includeSelection"`
	IncludeClipboard    bool `json:"includeClipboard"`
	IncludeMCPResources bool `json:"includeMCPResources"`
	MaxSelectionRunes   int  `json:"maxSelectionRunes,omitempty"`
	MaxClipboardRunes   int  `json:"maxClipboardRunes,omitempty"`
}

// ContextBundle is sanitized request context. MCPResources are caller-provided summaries,
// never transport payloads or authorization headers.
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

// ApplyContextPolicy turns all ambient values into a deterministic message set.
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
		context = append(context, "MCP resources:\n"+strings.Join(bundle.MCPResources, "\n"))
	}
	if policy.IncludeGlossary && len(bundle.Glossary) > 0 {
		var terms []string
		for _, t := range bundle.Glossary {
			if t.Term != "" {
				terms = append(terms, t.Term+": "+t.Hint)
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
