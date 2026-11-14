package prompt

import (
	"github.com/yeisme/clavis/pkg/core/config"
	"strings"
	"time"
)

// RenderContext provides automatic values available to templates.
type RenderContext struct {
	Selection, Clipboard, App, URL, WindowTitle, Glossary, Cursor string
	// Common aliases make it convenient to pass an engine CapsuleContext without adapters.
	SelectedText, ClipboardPreview, AppName, ActiveURL string
	Date, Time, DateTime                               string
	Now                                                time.Time
}
type PromptRenderResult struct {
	RenderedText     string
	CursorBackOffset int
	MissingRequired  []string
}

func (c RenderContext) value(n string) string {
	switch n {
	case "selection":
		if c.Selection != "" {
			return c.Selection
		}
		return c.SelectedText
	case "clipboard":
		if c.Clipboard != "" {
			return c.Clipboard
		}
		return c.ClipboardPreview
	case "app":
		if c.App != "" {
			return c.App
		}
		return c.AppName
	case "url":
		if c.URL != "" {
			return c.URL
		}
		return c.ActiveURL
	case "window_title":
		return c.WindowTitle
	case "glossary":
		return c.Glossary
	case "cursor":
		return c.Cursor
	case "date":
		if c.Date != "" {
			return c.Date
		}
	case "time":
		if c.Time != "" {
			return c.Time
		}
	case "datetime":
		if c.DateTime != "" {
			return c.DateTime
		}
	}
	now := c.Now
	if now.IsZero() {
		now = time.Now()
	}
	switch n {
	case "date":
		return now.Format("2006-01-02")
	case "time":
		return now.Format("15:04:05")
	case "datetime":
		return now.Format(time.RFC3339)
	}
	return ""
}

// RenderPrompt renders placeholders and reports missing required slots. Values override defaults.
func RenderPrompt(t config.PromptTemplate, values map[string]string, ctx RenderContext) PromptRenderResult {
	p := ParseTemplate(t.Template)
	slots := p.Slots(t)
	by := make(map[string]config.PromptSlot, len(slots))
	for _, s := range slots {
		by[s.Name] = s
	}
	var b strings.Builder
	b.Grow(len(t.Template))
	cursor := -1
	for i, lit := range p.Parts {
		b.WriteString(lit)
		if i >= len(p.Tokens) {
			continue
		}
		x := p.Tokens[i]
		var v string
		if x.BuiltIn {
			v = ctx.value(x.Name)
		} else if values != nil {
			v = values[x.Name]
		}
		if v == "" {
			if s, ok := by[x.Name]; ok {
				v = s.Default
			}
		}
		if x.Name == "cursor" {
			cursor = b.Len()
			v = ctx.value("cursor")
		}
		b.WriteString(v)
	}
	rendered := b.String()
	off := 0
	if cursor >= 0 {
		off = len([]rune(rendered)) - len([]rune(rendered[:cursor]))
	}
	missing := []string{}
	seen := map[string]bool{}
	for _, s := range slots {
		if !s.Required || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		v := ""
		if values != nil {
			v = values[s.Name]
		}
		if v == "" {
			v = s.Default
		}
		if v == "" {
			for _, x := range p.Tokens {
				if x.Name == s.Name && x.Default != "" {
					v = x.Default
				}
			}
		}
		if v == "" {
			missing = append(missing, s.Name)
		}
	}
	return PromptRenderResult{RenderedText: rendered, CursorBackOffset: off, MissingRequired: missing}
}
