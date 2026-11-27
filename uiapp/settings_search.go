package uiapp

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

type settingsSearchKind uint8

const (
	searchKindPage settingsSearchKind = iota
	searchKindField
	searchKindValue
)

type settingsSearchResult struct {
	Kind      settingsSearchKind
	PageKey   string
	FieldKey  string
	ItemID    string
	ItemGroup string
	Title     string
	Path      string
	MatchText string
}

type settingsSearchFieldDesc struct {
	PageKey  string
	FieldKey string
	Card     string
	TitleKey string
	Title    string
	Keywords string
}

func normalizeSettingsSearchText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\u3000':
			b.WriteRune(' ')
		case r >= '\uFF01' && r <= '\uFF5E':
			b.WriteRune(unicode.ToLower(r - 0xFEE0))
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func compactSettingsSearchText(s string) string {
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(normalizeSettingsSearchText(s))
}

func settingsSearchMatchesTokens(haystack, query string) bool {
	qNorm := strings.TrimSpace(normalizeSettingsSearchText(query))
	if qNorm == "" {
		return true
	}
	hNorm := normalizeSettingsSearchText(haystack)
	hCompact := compactSettingsSearchText(haystack)
	for _, tok := range strings.Fields(qNorm) {
		tokCompact := compactSettingsSearchText(tok)
		if !strings.Contains(hNorm, tok) && (tokCompact == "" || !strings.Contains(hCompact, tokCompact)) {
			return false
		}
	}
	return true
}

func settingsPageKeywords(pageKey string) string {
	switch pageKey {
	case "triggers":
		return i18n.Bilingual("settings.search.kw.triggers") + " " + i18n.Bilingual("settings.section.general")
	case "ime-keymap":
		return i18n.Bilingual("settings.search.kw.ime_keymap") + " " + i18n.Bilingual("settings.section.ime")
	case "leader":
		return i18n.Bilingual("settings.search.kw.leader") + " " + i18n.Bilingual("settings.section.leader")
	case "snippets":
		return i18n.Bilingual("settings.search.kw.snippets") + " " + i18n.Bilingual("omnibox.tab.snippets")
	case "glossary":
		return i18n.Bilingual("settings.search.kw.glossary") + " " + i18n.Bilingual("settings.section.glossary")
	case "clipboard":
		return i18n.Bilingual("settings.search.kw.clipboard") + " " + i18n.Bilingual("settings.section.clipboard")
	case "workflows":
		return i18n.Bilingual("settings.search.kw.workflows") + " " + i18n.Bilingual("settings.section.workflows")
	case "templates":
		return i18n.Bilingual("settings.section.templates.keywords") + " " + i18n.Bilingual("settings.search.kw.templates") + " " + i18n.Bilingual("settings.section.templates")
	case "ai":
		return i18n.Bilingual("settings.search.kw.ai") + " " + i18n.Bilingual("settings.section.ai")
	case "appearance":
		return i18n.Bilingual("settings.search.kw.appearance") + " " + i18n.Bilingual("settings.section.appearance")
	case "permissions":
		return i18n.Bilingual("settings.search.kw.permissions") + " " + i18n.Bilingual("settings.section.permissions")
	case "configs":
		return i18n.Bilingual("settings.search.kw.configs") + " " + i18n.Bilingual("settings.section.configs")
	case "security":
		return i18n.Bilingual("settings.search.kw.security") + " " + i18n.Bilingual("settings.section.security")
	case "about":
		return i18n.Bilingual("settings.search.kw.about") + " " + i18n.Bilingual("settings.section.about")
	default:
		return ""
	}
}

func settingsSearchFieldEntries() []settingsSearchFieldDesc {
	return []settingsSearchFieldDesc{
		// Triggers
		{PageKey: "triggers", FieldKey: "leader_key", Card: "[trigger]", TitleKey: "settings.trigger.leader_key", Title: i18n.T("settings.trigger.leader_key"), Keywords: "leader_key hotkey"},
		{PageKey: "triggers", FieldKey: "double_tap_key", Card: "[trigger]", TitleKey: "settings.trigger.double_tap_key", Title: i18n.T("settings.trigger.double_tap_key"), Keywords: "double_tap_key hotkey"},
		{PageKey: "triggers", FieldKey: "double_tap_interval_ms", Card: "[trigger]", TitleKey: "settings.trigger.double_tap_interval", Title: i18n.T("settings.trigger.double_tap_interval"), Keywords: "double_tap_interval_ms"},
		{PageKey: "triggers", FieldKey: "leader_reveal_delay_ms", Card: "[trigger]", TitleKey: "settings.trigger.leader_reveal_delay", Title: i18n.T("settings.trigger.leader_reveal_delay"), Keywords: "leader_reveal_delay_ms"},
		{PageKey: "triggers", FieldKey: "capsule_shortcut", Card: "[trigger]", TitleKey: "settings.trigger.capsule_shortcut", Title: i18n.T("settings.trigger.capsule_shortcut"), Keywords: "capsule_shortcut shortcut"},
		{PageKey: "triggers", FieldKey: "clipboard_shortcut", Card: "[trigger]", TitleKey: "settings.trigger.clipboard_shortcut", Title: i18n.T("settings.trigger.clipboard_shortcut"), Keywords: "clipboard_shortcut clipboard"},
		{PageKey: "triggers", FieldKey: "screenshot_shortcut", Card: "[trigger]", TitleKey: "settings.section.triggers.screenshot_shortcut", Title: i18n.T("settings.section.triggers.screenshot_shortcut"), Keywords: "screenshot_shortcut capture"},
		{PageKey: "triggers", FieldKey: "snippet_expansion_enabled", Card: "[trigger]", TitleKey: "settings.trigger.snippet_expansion", Title: i18n.T("settings.trigger.snippet_expansion"), Keywords: "snippet_expansion_enabled"},
		{PageKey: "triggers", FieldKey: "snippet_backspace_undo", Card: "[trigger]", TitleKey: "settings.trigger.snippet_backspace_undo", Title: i18n.T("settings.trigger.snippet_backspace_undo"), Keywords: "snippet_backspace_undo"},
		// Appearance
		{PageKey: "appearance", FieldKey: "theme", Card: "[appearance]", TitleKey: "settings.appearance.theme", Title: i18n.T("settings.appearance.theme"), Keywords: "theme dark light system"},
		{PageKey: "appearance", FieldKey: "language", Card: "[appearance]", TitleKey: "settings.appearance.language", Title: i18n.T("settings.appearance.language"), Keywords: "language english chinese"},
		{PageKey: "appearance", FieldKey: "animations", Card: "[appearance]", TitleKey: "settings.appearance.animations", Title: i18n.T("settings.appearance.animations"), Keywords: "animations motion reduced"},
		{PageKey: "appearance", FieldKey: "show_key_hints", Card: "[appearance]", TitleKey: "settings.appearance.key_hints", Title: i18n.T("settings.appearance.key_hints"), Keywords: "show_key_hints"},
		{PageKey: "appearance", FieldKey: "show_preview_pane", Card: "[appearance]", TitleKey: "settings.appearance.preview_pane", Title: i18n.T("settings.appearance.preview_pane"), Keywords: "show_preview_pane"},
		{PageKey: "appearance", FieldKey: "capsule_width", Card: "[appearance]", TitleKey: "settings.appearance.capsule_width", Title: i18n.T("settings.appearance.capsule_width"), Keywords: "capsule_width"},
		{PageKey: "appearance", FieldKey: "max_visible_results", Card: "[appearance]", TitleKey: "settings.appearance.max_visible_results", Title: i18n.T("settings.appearance.max_visible_results"), Keywords: "max_visible_results"},
		{PageKey: "appearance", FieldKey: "corner_radius", Card: "[appearance]", TitleKey: "settings.appearance.corner_radius", Title: i18n.T("settings.appearance.corner_radius"), Keywords: "corner_radius"},
		{PageKey: "appearance", FieldKey: "hud_duration_ms", Card: "[appearance]", TitleKey: "settings.appearance.hud_duration", Title: i18n.T("settings.appearance.hud_duration"), Keywords: "hud_duration_ms"},
		{PageKey: "appearance", FieldKey: "visual_preset", Card: "[appearance]", TitleKey: "settings.appearance.visual_preset", Title: i18n.T("settings.appearance.visual_preset"), Keywords: "visual_preset liquid_glass spatial_3d"},
		{PageKey: "appearance", FieldKey: "stage_intensity", Card: "[appearance]", TitleKey: "settings.appearance.stage_intensity", Title: i18n.T("settings.appearance.stage_intensity"), Keywords: "stage_intensity"},
		{PageKey: "appearance", FieldKey: "oblique_rows", Card: "[appearance]", TitleKey: "settings.appearance.oblique_rows", Title: i18n.T("settings.appearance.oblique_rows"), Keywords: "oblique_rows"},
		{PageKey: "appearance", FieldKey: "glass_clarity", Card: "[appearance]", TitleKey: "settings.appearance.glass_clarity", Title: i18n.T("settings.appearance.glass_clarity"), Keywords: "glass_clarity"},
		// IME & Keymap
		{PageKey: "ime-keymap", FieldKey: "keymap.enabled", Card: "[keymap]", TitleKey: "settings.keymap.enabled", Title: i18n.T("settings.keymap.enabled"), Keywords: "keymap enabled"},
		{PageKey: "ime-keymap", FieldKey: "nav_layer_enabled", Card: "[keymap]", TitleKey: "settings.keymap.nav_layer", Title: i18n.T("settings.keymap.nav_layer"), Keywords: "nav_layer_enabled"},
		{PageKey: "ime-keymap", FieldKey: "tap_hold_threshold_ms", Card: "[keymap]", TitleKey: "settings.keymap.tap_hold_threshold", Title: i18n.T("settings.keymap.tap_hold_threshold"), Keywords: "tap_hold_threshold_ms"},
		{PageKey: "ime-keymap", FieldKey: "caps_lock_tap", Card: "[keymap]", TitleKey: "settings.keymap.caps_lock_tap", Title: i18n.T("settings.keymap.caps_lock_tap"), Keywords: "caps_lock_tap"},
		{PageKey: "ime-keymap", FieldKey: "caps_lock_hold", Card: "[keymap]", TitleKey: "settings.keymap.caps_lock_hold", Title: i18n.T("settings.keymap.caps_lock_hold"), Keywords: "caps_lock_hold"},
		{PageKey: "ime-keymap", FieldKey: "ime.enabled", Card: "[ime]", TitleKey: "settings.ime.enabled", Title: i18n.T("settings.ime.enabled"), Keywords: "ime enabled"},
		{PageKey: "ime-keymap", FieldKey: "lock_ascii_on_leader", Card: "[ime]", TitleKey: "settings.ime.lock_ascii", Title: i18n.T("settings.ime.lock_ascii"), Keywords: "lock_ascii_on_leader"},
		{PageKey: "ime-keymap", FieldKey: "show_floating_pill", Card: "[ime]", TitleKey: "settings.ime.floating_pill", Title: i18n.T("settings.ime.floating_pill"), Keywords: "show_floating_pill"},
		{PageKey: "ime-keymap", FieldKey: "show_indicator_hud", Card: "[ime]", TitleKey: "settings.ime.indicator_hud", Title: i18n.T("settings.ime.indicator_hud"), Keywords: "show_indicator_hud"},
		{PageKey: "ime-keymap", FieldKey: "switch_delay_ms", Card: "[ime]", TitleKey: "settings.ime.switch_delay", Title: i18n.T("settings.ime.switch_delay"), Keywords: "switch_delay_ms"},
		{PageKey: "ime-keymap", FieldKey: "default_input_source_id", Card: "[ime]", TitleKey: "settings.ime.default_source", Title: i18n.T("settings.ime.default_source"), Keywords: "default_input_source_id"},
		{PageKey: "ime-keymap", FieldKey: "input_engine.enabled", Card: "[input_engine]", TitleKey: "settings.input_engine.enabled", Title: i18n.T("settings.input_engine.enabled"), Keywords: "input_engine enabled"},
		{PageKey: "ime-keymap", FieldKey: "input_engine.scheme", Card: "[input_engine]", TitleKey: "settings.input_engine.scheme", Title: i18n.T("settings.input_engine.scheme"), Keywords: "input_engine scheme"},
		{PageKey: "ime-keymap", FieldKey: "input_engine.candidate_count", Card: "[input_engine]", TitleKey: "settings.input_engine.candidate_count", Title: i18n.T("settings.input_engine.candidate_count"), Keywords: "candidate_count"},
		{PageKey: "ime-keymap", FieldKey: "appearance.candidate_bar_orientation", Card: "[input_engine]", TitleKey: "settings.input_engine.orientation", Title: i18n.T("settings.input_engine.orientation"), Keywords: "candidate_bar_orientation"},
		{PageKey: "ime-keymap", FieldKey: "input_engine.shift_toggle_ascii", Card: "[input_engine]", TitleKey: "settings.input_engine.shift_toggle", Title: i18n.T("settings.input_engine.shift_toggle"), Keywords: "shift_toggle_ascii"},
		{PageKey: "ime-keymap", FieldKey: "input_engine.user_learning", Card: "[input_engine]", TitleKey: "settings.input_engine.user_learning", Title: i18n.T("settings.input_engine.user_learning"), Keywords: "user_learning"},
		// Clipboard
		{PageKey: "clipboard", FieldKey: "enabled", Card: "[clipboard]", TitleKey: "settings.clipboard.enabled", Title: i18n.T("settings.clipboard.enabled"), Keywords: "clipboard enabled"},
		{PageKey: "clipboard", FieldKey: "ignore_concealed_types", Card: "[clipboard]", TitleKey: "settings.clipboard.ignore_concealed", Title: i18n.T("settings.clipboard.ignore_concealed"), Keywords: "ignore_concealed_types privacy"},
		{PageKey: "clipboard", FieldKey: "max_history_items", Card: "[clipboard]", TitleKey: "settings.clipboard.max_items", Title: i18n.T("settings.clipboard.max_items"), Keywords: "max_history_items"},
		{PageKey: "clipboard", FieldKey: "retention_days", Card: "[clipboard]", TitleKey: "settings.clipboard.retention", Title: i18n.T("settings.clipboard.retention"), Keywords: "retention_days"},
		{PageKey: "clipboard", FieldKey: "media_capture", Card: "[clipboard]", TitleKey: "settings.clipboard.media_capture", Title: i18n.T("settings.clipboard.media_capture"), Keywords: "media_capture"},
		{PageKey: "clipboard", FieldKey: "media_quota_mb", Card: "[clipboard]", TitleKey: "settings.clipboard.media_quota", Title: i18n.T("settings.clipboard.media_quota"), Keywords: "media_quota_mb"},
		{PageKey: "clipboard", FieldKey: "sensitive_copy_ttl_seconds", Card: "[clipboard]", TitleKey: "settings.clipboard.manager.ttl_label", Title: i18n.T("settings.clipboard.manager.ttl_label"), Keywords: "sensitive_copy_ttl_seconds ttl"},
		{PageKey: "clipboard", FieldKey: "enter_action", Card: "[clipboard]", TitleKey: "settings.clipboard.wb.enter_action", Title: i18n.T("settings.clipboard.wb.enter_action"), Keywords: "clipboard enter_action"},
		{PageKey: "clipboard", FieldKey: "row_density", Card: "[clipboard]", TitleKey: "settings.clipboard.wb.row_density", Title: i18n.T("settings.clipboard.wb.row_density"), Keywords: "clipboard row_density"},
		{PageKey: "clipboard", FieldKey: "pin_placement", Card: "[clipboard]", TitleKey: "settings.clipboard.wb.pin_placement", Title: i18n.T("settings.clipboard.wb.pin_placement"), Keywords: "clipboard pin_placement"},
		{PageKey: "clipboard", FieldKey: "selection_style", Card: "[clipboard]", TitleKey: "settings.clipboard.wb.selection_style", Title: i18n.T("settings.clipboard.wb.selection_style"), Keywords: "clipboard selection_style"},
		// AI (field names ONLY, NEVER secret values)
		{PageKey: "ai", FieldKey: "api_key", Card: "Keychain", TitleKey: "settings.ai.key_label", Title: i18n.T("settings.ai.key_label"), Keywords: "api key api_key keychain"},
		{PageKey: "ai", FieldKey: "continue_enabled", Card: "[ai]", TitleKey: "settings.ai.active_profile", Title: i18n.T("settings.ai.active_profile"), Keywords: "ai profile continue_enabled"},
	}
}

func settingsSearchQuery(query string, cfg config.ClavisConfig) []settingsSearchResult {
	qTrim := strings.TrimSpace(query)
	if qTrim == "" {
		return nil
	}
	var out []settingsSearchResult

	// 1. Pages
	for _, p := range settingsPages() {
		gLabel := settingsGroupLabel(p.Group)
		hay := strings.Join([]string{gLabel, i18n.Bilingual("settings.group." + p.Group), p.Label, p.Hint, p.Key, settingsPageKeywords(p.Key)}, " ")
		if settingsSearchMatchesTokens(hay, qTrim) {
			out = append(out, settingsSearchResult{
				Kind:      searchKindPage,
				PageKey:   p.Key,
				Title:     p.Label,
				Path:      gLabel + " › " + p.Label,
				MatchText: p.Label,
			})
		}
	}

	// 2. Fields
	for _, f := range settingsSearchFieldEntries() {
		p, ok := settingsPageByKey(f.PageKey)
		if !ok {
			continue
		}
		gLabel := settingsGroupLabel(p.Group)
		hay := strings.Join([]string{gLabel, p.Label, f.Card, f.Title, i18n.Bilingual(f.TitleKey), f.FieldKey, f.Keywords}, " ")
		if settingsSearchMatchesTokens(hay, qTrim) {
			out = append(out, settingsSearchResult{
				Kind:      searchKindField,
				PageKey:   f.PageKey,
				FieldKey:  f.FieldKey,
				Title:     f.Title,
				Path:      gLabel + " › " + p.Label + " › " + f.Card,
				MatchText: f.Title,
			})
		}
	}

	// 3. Saved config items ONLY (no drafts, no snippet bodies, NO API key / Vault secret values)
	var walkLeader func(nodes []config.LeaderNode, prefix []string)
	walkLeader = func(nodes []config.LeaderNode, prefix []string) {
		p, _ := settingsPageByKey("leader")
		gLabel := settingsGroupLabel(p.Group)
		for _, n := range nodes {
			path := append(append([]string(nil), prefix...), n.Key)
			pathID := strings.Join(path, "/")
			hay := n.Key + " " + n.Label + " " + string(n.Action.Type)
			if settingsSearchMatchesTokens(hay, qTrim) {
				out = append(out, settingsSearchResult{
					Kind:      searchKindValue,
					PageKey:   "leader",
					FieldKey:  "item/" + pathID,
					ItemID:    pathID,
					ItemGroup: "leader",
					Title:     n.Label,
					Path:      gLabel + " › " + p.Label + " › " + n.Key,
					MatchText: n.Label,
				})
			}
			if len(n.Action.Children) > 0 {
				walkLeader(n.Action.Children, path)
			}
		}
	}
	walkLeader(cfg.Leader.Nodes, nil)

	if p, ok := settingsPageByKey("snippets"); ok {
		gLabel := settingsGroupLabel(p.Group)
		for _, item := range cfg.Snippets.Items {
			hay := item.ID + " " + item.Trigger + " " + item.Description
			if settingsSearchMatchesTokens(hay, qTrim) {
				title := nonEmptyOr(item.Trigger, item.ID)
				out = append(out, settingsSearchResult{
					Kind:      searchKindValue,
					PageKey:   "snippets",
					FieldKey:  "item/" + item.ID,
					ItemID:    item.ID,
					ItemGroup: "snippets",
					Title:     title,
					Path:      gLabel + " › " + p.Label + " › " + nonEmptyOr(item.Description, item.ID),
					MatchText: title,
				})
			}
		}
	}

	if p, ok := settingsPageByKey("glossary"); ok {
		gLabel := settingsGroupLabel(p.Group)
		for _, term := range cfg.Glossary.Terms {
			hay := term.Term + " " + strings.Join(term.Aliases, " ") + " " + term.Category
			if settingsSearchMatchesTokens(hay, qTrim) {
				out = append(out, settingsSearchResult{
					Kind:      searchKindValue,
					PageKey:   "glossary",
					FieldKey:  "item/" + term.Term,
					ItemID:    term.Term,
					ItemGroup: "glossary",
					Title:     term.Term,
					Path:      gLabel + " › " + p.Label + " › " + term.Term,
					MatchText: term.Term,
				})
			}
		}
	}

	if p, ok := settingsPageByKey("workflows"); ok {
		gLabel := settingsGroupLabel(p.Group)
		for _, wf := range cfg.Workflows.Workflows {
			hay := wf.ID + " " + wf.Name + " " + wf.Description
			if settingsSearchMatchesTokens(hay, qTrim) {
				title := nonEmptyOr(wf.Name, wf.ID)
				out = append(out, settingsSearchResult{
					Kind:      searchKindValue,
					PageKey:   "workflows",
					FieldKey:  "item/" + wf.ID,
					ItemID:    wf.ID,
					ItemGroup: "workflows",
					Title:     title,
					Path:      gLabel + " › " + p.Label + " › " + title,
					MatchText: title,
				})
			}
		}
	}

	if p, ok := settingsPageByKey("templates"); ok {
		gLabel := settingsGroupLabel(p.Group)
		for _, pt := range cfg.Prompts.Items {
			hay := pt.ID + " " + pt.Title + " " + pt.Trigger + " " + pt.Category
			if settingsSearchMatchesTokens(hay, qTrim) {
				title := nonEmptyOr(pt.Title, pt.ID)
				out = append(out, settingsSearchResult{
					Kind:      searchKindValue,
					PageKey:   "templates",
					FieldKey:  "item/" + pt.ID,
					ItemID:    pt.ID,
					ItemGroup: "templates",
					Title:     title,
					Path:      gLabel + " › " + p.Label + " › " + nonEmptyOr(pt.Trigger, pt.ID),
					MatchText: title,
				})
			}
		}
	}

	if p, ok := settingsPageByKey("ai"); ok {
		gLabel := settingsGroupLabel(p.Group)
		for _, prof := range cfg.Main.AI.Profiles {
			hay := prof.ID + " " + prof.Name + " " + prof.Provider + " " + prof.Model
			if settingsSearchMatchesTokens(hay, qTrim) {
				title := nonEmptyOr(prof.Name, prof.ID)
				out = append(out, settingsSearchResult{
					Kind:      searchKindValue,
					PageKey:   "ai",
					FieldKey:  "item/" + prof.ID,
					ItemID:    prof.ID,
					ItemGroup: "ai",
					Title:     title,
					Path:      gLabel + " › " + p.Label + " › " + prof.Provider,
					MatchText: title,
				})
			}
		}
	}

	if p, ok := settingsPageByKey("ime-keymap"); ok {
		gLabel := settingsGroupLabel(p.Group)
		for _, rule := range cfg.IMERules.Rules {
			hay := rule.AppName + " " + rule.BundleID + " " + rule.ProcessName
			if settingsSearchMatchesTokens(hay, qTrim) {
				title := nonEmptyOr(rule.AppName, nonEmptyOr(rule.BundleID, rule.ProcessName))
				out = append(out, settingsSearchResult{
					Kind:      searchKindValue,
					PageKey:   "ime-keymap",
					FieldKey:  "item/" + title,
					Title:     title,
					Path:      gLabel + " › " + p.Label + " › " + title,
					MatchText: title,
				})
			}
		}
	}

	if p, ok := settingsPageByKey("clipboard"); ok {
		gLabel := settingsGroupLabel(p.Group)
		for _, bid := range cfg.Main.Clipboard.ExcludedBundleIDs {
			if settingsSearchMatchesTokens(bid, qTrim) {
				out = append(out, settingsSearchResult{
					Kind:      searchKindValue,
					PageKey:   "clipboard",
					FieldKey:  "item/" + bid,
					Title:     bid,
					Path:      gLabel + " › " + p.Label + " › " + bid,
					MatchText: bid,
				})
			}
		}
	}

	return out
}

func settingsMatchIndices(text, query string) []int {
	if text == "" || strings.TrimSpace(query) == "" {
		return nil
	}
	textRunes := []rune(text)
	lowerRunes := make([]rune, len(textRunes))
	for i, r := range textRunes {
		switch {
		case r == '\u3000':
			lowerRunes[i] = ' '
		case r >= '\uFF01' && r <= '\uFF5E':
			lowerRunes[i] = unicode.ToLower(r - 0xFEE0)
		default:
			lowerRunes[i] = unicode.ToLower(r)
		}
	}
	lowerStr := string(lowerRunes)
	var idxs []int
	for _, tok := range strings.Fields(normalizeSettingsSearchText(query)) {
		if tok == "" {
			continue
		}
		tokRunes := []rune(tok)
		if pos := strings.Index(lowerStr, tok); pos >= 0 {
			runeStart := len([]rune(lowerStr[:pos]))
			for k := 0; k < len(tokRunes); k++ {
				idxs = append(idxs, runeStart+k)
			}
		}
	}
	slices.Sort(idxs)
	return slices.Compact(idxs)
}

func (a *AppController) jumpToSettingsSearchResult(c *ui.Context, cfg config.ClavisConfig, res settingsSearchResult) {
	a.setSettingsPage(res.PageKey)
	se := a.settingsEditor()
	if res.Kind == searchKindValue && res.ItemID != "" {
		switch res.ItemGroup {
		case "snippets":
			for _, item := range cfg.Snippets.Items {
				if item.ID == res.ItemID {
					seSet(se, func(s *settingsEditorState) {
						s.snippetIsNew = false
						s.snippetSelected = item.ID
						s.snippetDraft = item
						s.snippetConfirmDelete = false
					})
					break
				}
			}
		case "glossary":
			for _, term := range cfg.Glossary.Terms {
				if term.Term == res.ItemID {
					seSet(se, func(s *settingsEditorState) {
						s.glossaryIsNew = false
						s.glossarySelected = term.Term
						s.glossaryDraft = term
						s.glossaryConfirmDelete = false
					})
					break
				}
			}
		case "workflows":
			for _, wf := range cfg.Workflows.Workflows {
				if wf.ID == res.ItemID {
					a.selectWorkflowDraft(wf)
					break
				}
			}
		case "leader":
			path := strings.Split(res.ItemID, "/")
			if node, ok := findLeaderNode(cfg.Leader.Nodes, path); ok {
				seSet(se, func(s *settingsEditorState) {
					s.leaderSelected = path
					s.leaderDraft = node
					s.leaderDraft.Action = cloneLeaderAction(node.Action)
					s.leaderLoaded = true
					s.leaderConfirmDelete = false
				})
			}
		}
	}
	if res.FieldKey != "" && res.Kind == searchKindField {
		anchor := res.PageKey + ":" + res.FieldKey
		seSet(se, func(s *settingsEditorState) {
			s.pendingScrollTop = false
			s.pendingScrollField = anchor
			s.highlightField = anchor
			s.highlightUntil = time.Time{}
		})
	}
}

func (a *AppController) applyFieldAnchor(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
	row ui.Element,
	pageKey string,
	fieldKey string,
) {
	if c == nil || a == nil {
		return
	}
	anchor := pageKey + ":" + fieldKey
	se := a.settingsEditor()
	now := c.Now()

	se.mu.Lock()
	shouldScroll := se.pendingScrollField == anchor
	if shouldScroll {
		se.pendingScrollField = ""
	}
	highlighted := se.highlightField == anchor
	if highlighted && se.highlightUntil.IsZero() {
		se.highlightUntil = now.Add(1200 * time.Millisecond)
	}
	until := se.highlightUntil
	if highlighted && !now.Before(until) {
		se.highlightField = ""
		se.highlightUntil = time.Time{}
		highlighted = false
	}
	se.mu.Unlock()

	if shouldScroll {
		reduceMotion := c.Preferences().ReduceMotion ||
			strings.EqualFold(cfg.Main.Appearance.Animations, "off") ||
			strings.EqualFold(cfg.Main.Appearance.Animations, "reduced")
		seSet(se, func(s *settingsEditorState) { s.lastScrollAnimated = !reduceMotion })
		opts := ui.ScrollIntoViewOptions{Block: ui.ScrollCenter}
		if !reduceMotion {
			anim := ui.Smooth
			opts.Animation = &anim
		}
		row.ScrollIntoView(opts)
	}
	if highlighted {
		row.Background(theme.Accent.Alpha(alphaSubtle)).Radius(radiusRow)
		c.After(until.Sub(now))
	}
}

func (a *AppController) renderSettingsSearchResults(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
	query string,
) {
	se := a.settingsEditor()
	results := settingsSearchQuery(query, cfg)
	if len(results) == 0 {
		ui.Column(c).Key("settings-search-empty").FillWidth().Padding(space12, space8).Gap(space4).Children(func() {
			ui.Text(c, i18n.Tf("settings.search.no_results", strings.TrimSpace(query))).
				FontSize(typeBody).TextColor(theme.TextMuted).Wrap()
			ui.Text(c, i18n.T("settings.search.no_results_hint")).
				FontSize(typeMeta).TextColor(theme.TextMuted).Wrap()
		})
		return
	}
	const maxSearchResults = 50
	visible := results
	extra := 0
	if len(visible) > maxSearchResults {
		extra = len(visible) - maxSearchResults
		visible = visible[:maxSearchResults]
	}
	selIdx := seGet(se, func(s *settingsEditorState) int { return s.searchSelectedIdx })
	if selIdx < 0 || selIdx >= len(visible) {
		selIdx = 0
		seSet(se, func(s *settingsEditorState) { s.searchSelectedIdx = 0 })
	}

	mark := matchMark(theme)
	for i, res := range visible {
		item := res
		selected := i == selIdx
		row := ui.ButtonBase(c).Key(fmt.Sprintf("settings-search-result:%d:%s:%s", i, item.PageKey, item.FieldKey)).
			Label(item.Title).FillWidth().Padding(space6, space8).Radius(radiusRow).FocusRing(false)
		if selected {
			row = kitInteractive(c, theme, row, interactionState{Selected: true}, motionProfiles[animationOff])
		} else if row.Hovered() {
			row.Background(tileFill(theme))
		}
		row.Children(func() {
			ui.Column(c).FillWidth().Gap(space1).Children(func() {
				indices := settingsMatchIndices(item.Title, query)
				if len(indices) > 0 {
					spans := buildHighlightedSpans(item.Title, indices, mark, theme.Text)
					ui.RichText(c, spans...).FontSize(typeLabel).FontWeight(600).SingleLine()
				} else {
					ui.Text(c, item.Title).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
				}
				ui.Text(c, item.Path).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
			})
		})
		if row.Clicked() {
			seSet(se, func(s *settingsEditorState) { s.searchSelectedIdx = i })
			a.jumpToSettingsSearchResult(c, cfg, item)
		}
	}
	if extra > 0 {
		ui.Text(c, i18n.Tf("settings.search.more", extra)).FontSize(typeMeta).TextColor(theme.TextMuted).Padding(space6, space8).Wrap()
	}
}
