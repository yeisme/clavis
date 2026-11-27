package uiapp

import (
	"fmt"
	"strings"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

type settingsGroupMeta struct {
	Key   string
	Label string
}

type settingsPageMeta struct {
	Key   string
	Group string
	Label string
	Icon  string
	Hint  string
}

// settingsGroups returns the 5 Settings navigation groups in canonical order.
func settingsGroups() []settingsGroupMeta {
	return []settingsGroupMeta{
		{Key: "input", Label: i18n.T("settings.group.input")},
		{Key: "productivity", Label: i18n.T("settings.group.productivity")},
		{Key: "intelligence", Label: i18n.T("settings.group.intelligence")},
		{Key: "system", Label: i18n.T("settings.group.system")},
		{Key: "about", Label: i18n.T("settings.group.about")},
	}
}

// settingsPages returns the single canonical ordered list of Settings pages.
func settingsPages() []settingsPageMeta {
	return []settingsPageMeta{
		{Key: "triggers", Group: "input", Label: i18n.T("settings.section.general"), Icon: "bolt", Hint: i18n.T("settings.section.general.hint")},
		{Key: "ime-keymap", Group: "input", Label: i18n.T("settings.section.ime"), Icon: "keyboard", Hint: i18n.T("settings.section.ime.hint")},
		{Key: "leader", Group: "input", Label: i18n.T("settings.section.leader"), Icon: "command", Hint: i18n.T("settings.section.leader.hint")},
		{Key: "snippets", Group: "productivity", Label: i18n.T("omnibox.tab.snippets"), Icon: "text.badge.plus", Hint: i18n.T("settings.section.snippets.hint")},
		{Key: "glossary", Group: "productivity", Label: i18n.T("settings.section.glossary"), Icon: "character.book.closed", Hint: i18n.T("settings.section.glossary.hint")},
		{Key: "templates", Group: "productivity", Label: i18n.T("settings.section.templates"), Icon: "doc.text", Hint: i18n.T("settings.section.templates.hint")},
		{Key: "clipboard", Group: "productivity", Label: i18n.T("settings.section.clipboard"), Icon: "doc.on.clipboard", Hint: i18n.T("settings.section.clipboard.hint")},
		{Key: "workflows", Group: "productivity", Label: i18n.T("settings.section.workflows"), Icon: "wand.and.stars", Hint: i18n.T("settings.section.workflows.hint")},
		{Key: "ai", Group: "intelligence", Label: i18n.T("settings.section.ai"), Icon: "sparkles", Hint: i18n.T("settings.section.ai.hint")},
		{Key: "appearance", Group: "system", Label: i18n.T("settings.section.appearance"), Icon: "paintbrush", Hint: i18n.T("settings.section.appearance.hint")},
		{Key: "permissions", Group: "system", Label: i18n.T("settings.section.permissions"), Icon: "gearshape", Hint: i18n.T("settings.section.permissions.hint")},
		{Key: "configs", Group: "system", Label: i18n.T("settings.section.configs"), Icon: "folder", Hint: i18n.T("settings.section.configs.hint")},
		{Key: "security", Group: "system", Label: i18n.T("settings.section.security"), Icon: "lock.rotation", Hint: i18n.T("settings.section.security.hint")},
		{Key: "about", Group: "about", Label: i18n.T("settings.section.about"), Icon: "info.circle", Hint: i18n.T("settings.section.about.hint")},
	}
}

func settingsPageByKey(key string) (settingsPageMeta, bool) {
	resolved, ok := resolveSettingsPageKey(key)
	if !ok {
		return settingsPageMeta{}, false
	}
	for _, p := range settingsPages() {
		if p.Key == resolved {
			return p, true
		}
	}
	return settingsPageMeta{}, false
}

func settingsGroupLabel(groupKey string) string {
	for _, g := range settingsGroups() {
		if g.Key == groupKey {
			return g.Label
		}
	}
	return groupKey
}

func settingsFirstPageInGroup(groupKey string) (string, bool) {
	for _, p := range settingsPages() {
		if p.Group == groupKey {
			return p.Key, true
		}
	}
	return "", false
}

func resolveSettingsPageKey(raw string) (string, bool) {
	k := strings.ToLower(strings.TrimSpace(raw))
	switch k {
	case "general":
		return "triggers", true
	case "ime", "keymap":
		return "ime-keymap", true
	case "prompts":
		return "templates", true
	case "vault":
		return "security", true
	}
	for _, p := range settingsPages() {
		if p.Key == k {
			return p.Key, true
		}
	}
	return "", false
}

func normalizeSettingsPageKey(key string) string {
	if resolved, ok := resolveSettingsPageKey(key); ok {
		return resolved
	}
	return "triggers"
}

func settingsPageForTab(tab SettingsTab, lastVisited string) string {
	switch tab {
	case SettingsTabAI:
		return "ai"
	case SettingsTabIMEKeymap:
		return "ime-keymap"
	case SettingsTabPermissions:
		return "permissions"
	case SettingsTabConfigs:
		return "configs"
	case SettingsTabGeneral:
		if p, ok := settingsPageByKey(lastVisited); ok {
			return p.Key
		}
		return "triggers"
	default:
		if p, ok := settingsPageByKey(lastVisited); ok {
			return p.Key
		}
		return "triggers"
	}
}

func settingsTabForPage(pageKey string) SettingsTab {
	switch normalizeSettingsPageKey(pageKey) {
	case "ai":
		return SettingsTabAI
	case "ime-keymap":
		return SettingsTabIMEKeymap
	case "permissions":
		return SettingsTabPermissions
	case "configs":
		return SettingsTabConfigs
	default:
		return SettingsTabGeneral
	}
}

func settingsPageBadge(pageKey string, cfg config.ClavisConfig) string {
	switch pageKey {
	case "ime-keymap":
		return fmt.Sprintf("%d", len(cfg.IMERules.Rules))
	case "leader":
		return fmt.Sprintf("%d", len(cfg.Leader.Nodes))
	case "snippets":
		return fmt.Sprintf("%d", len(cfg.Snippets.Items))
	case "glossary":
		return fmt.Sprintf("%d", len(cfg.Glossary.Terms))
	case "workflows":
		return fmt.Sprintf("%d", len(cfg.Workflows.Workflows))
	case "templates":
		return fmt.Sprintf("%d", len(cfg.Prompts.Items))
	case "ai":
		return fmt.Sprintf("%d", len(cfg.Main.AI.Profiles))
	case "clipboard":
		return fmt.Sprintf("%d", len(cfg.Main.Clipboard.ExcludedBundleIDs))
	case "configs":
		return fmt.Sprintf("%d", len(config.AllConfigFileKinds()))
	}
	return ""
}
