package config

import "github.com/yeisme/clavis/pkg/core/template"

// TemplateView adapts the already merged last-known-good config. In particular,
// Current().Prompts already includes Markdown-over-TOML precedence. This method
// neither re-reads nor rewrites legacy prompt files or their configuration shape.
func (s *ConfigStore) TemplateView(library *template.Library) ([]template.Item, []template.Diagnostic) {
	prompts := s.Current().Prompts
	local := make([]template.Item, 0, len(prompts.Items))
	for _, p := range prompts.Items {
		item := template.Item{ID: p.ID, Trigger: p.Trigger, Title: p.Title, Category: p.Category, Description: p.Description, Body: p.Template, AppBundleIDs: append([]string(nil), p.AppBundleIDs...), Enabled: true, SourceKind: template.SourceLocal}
		if len(p.Slots) > 0 {
			item.SlotDefaults = make(map[string]template.SlotDefault, len(p.Slots))
			for _, slot := range p.Slots {
				d := slot.Default
				item.SlotDefaults[slot.Name] = template.SlotDefault{Default: &d}
			}
		}
		local = append(local, item)
	}
	if library == nil {
		return template.MergeView(local, nil, nil)
	}
	return library.View(local)
}
