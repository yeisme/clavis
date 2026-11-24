package uiapp

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// Settings section indices. The first five mirror SettingsTab so that
// OpenSettings(SettingsTabAI) and friends select the matching section; the
// extended sections continue past them.
const (
	settingsSectionGeneral     = int(SettingsTabGeneral)
	settingsSectionAI          = int(SettingsTabAI)
	settingsSectionIMEKeymap   = int(SettingsTabIMEKeymap)
	settingsSectionPermissions = int(SettingsTabPermissions)
	settingsSectionConfigs     = int(SettingsTabConfigs)
	settingsSectionLeader      = 5
	settingsSectionSnippets    = 6
	settingsSectionGlossary    = 7
	settingsSectionWorkflows   = 8
	settingsSectionAppearance  = 9
	settingsSectionClipboard   = 10
	settingsSectionAbout       = 11
	settingsSectionSecurity    = 12
)

// settingsSectionMeta describes one navigation rail entry.
type settingsSectionMeta struct {
	Index    int
	Key      string
	Label    string
	Icon     string
	Hint     string
	Settings SettingsTab
	Group    string
}

type settingsGroupMeta struct {
	Key   string
	Label string
}

func settingsGroups() []settingsGroupMeta {
	return []settingsGroupMeta{
		{Key: "input", Label: i18n.T("settings.group.input")},
		{Key: "productivity", Label: i18n.T("settings.group.productivity")},
		{Key: "intelligence", Label: i18n.T("settings.group.intelligence")},
		{Key: "system", Label: i18n.T("settings.group.system")},
		{Key: "about", Label: i18n.T("settings.group.about")},
	}
}

// settingsSections returns the ordered navigation rail entries.
func settingsSections() []settingsSectionMeta {
	return []settingsSectionMeta{
		{settingsSectionGeneral, "triggers", i18n.T("settings.section.general"), "bolt", i18n.T("settings.section.general.hint"), SettingsTabGeneral, "input"},
		{settingsSectionIMEKeymap, "ime-keymap", i18n.T("settings.section.ime"), "keyboard", i18n.T("settings.section.ime.hint"), SettingsTabIMEKeymap, "input"},
		{settingsSectionLeader, "leader", i18n.T("settings.section.leader"), "command", i18n.T("settings.section.leader.hint"), SettingsTabGeneral, "input"},
		{settingsSectionSnippets, "snippets", i18n.T("omnibox.tab.snippets"), "text.badge.plus", i18n.T("settings.section.snippets.hint"), SettingsTabGeneral, "productivity"},
		{settingsSectionGlossary, "glossary", i18n.T("settings.section.glossary"), "character.book.closed", i18n.T("settings.section.glossary.hint"), SettingsTabGeneral, "productivity"},
		{settingsSectionWorkflows, "workflows", i18n.T("settings.section.workflows"), "wand.and.stars", i18n.T("settings.section.workflows.hint"), SettingsTabGeneral, "productivity"},
		{settingsSectionAI, "ai", i18n.T("settings.section.ai"), "sparkles", i18n.T("settings.section.ai.hint"), SettingsTabAI, "intelligence"},
		{settingsSectionPermissions, "permissions", i18n.T("settings.section.permissions"), "gearshape", i18n.T("settings.section.permissions.hint"), SettingsTabPermissions, "system"},
		{settingsSectionConfigs, "configs", i18n.T("settings.section.configs"), "folder", i18n.T("settings.section.configs.hint"), SettingsTabConfigs, "system"},
	}
}

func settingsNavigationSections() []settingsSectionMeta {
	sections := append([]settingsSectionMeta(nil), settingsSections()...)
	sections = append(sections[:6], append([]settingsSectionMeta{
		{settingsSectionClipboard, "clipboard", i18n.T("settings.section.clipboard"), "doc.on.clipboard", i18n.T("settings.section.clipboard.hint"), SettingsTabGeneral, "productivity"},
	}, sections[6:]...)...)
	sections = append(sections,
		settingsSectionMeta{settingsSectionAppearance, "appearance", i18n.T("settings.section.appearance"), "paintbrush", i18n.T("settings.section.appearance.hint"), SettingsTabGeneral, "system"},
		settingsSectionMeta{settingsSectionSecurity, "security", pickLang(i18n.IsChinese(), "安全", "Security"), "lock.rotation", pickLang(i18n.IsChinese(), "密码与密钥引用、只读元数据与 User-Only TTL 复制", "SecretRefs, metadata-only index & User-Only TTL copy"), SettingsTabGeneral, "system"},
		settingsSectionMeta{settingsSectionAbout, "about", i18n.T("settings.section.about"), "info.circle", i18n.T("settings.section.about.hint"), SettingsTabGeneral, "about"},
	)
	for i := range sections {
		switch sections[i].Key {
		case "general", "ime-keymap", "leader":
			sections[i].Group = "input"
		case "snippets", "glossary", "clipboard", "workflows":
			sections[i].Group = "productivity"
		case "ai":
			sections[i].Group = "intelligence"
		case "appearance", "permissions", "configs", "security":
			sections[i].Group = "system"
		case "about":
			sections[i].Group = "about"
		}
	}
	return sections
}

// settingsSectionMetaFor resolves the metadata of a section index.
func settingsSectionMetaFor(index int) settingsSectionMeta {
	for _, meta := range settingsNavigationSections() {
		if meta.Index == index {
			return meta
		}
	}
	return settingsNavigationSections()[0]
}

// normalizeSettingsSection clamps an unknown section index to the first one.
func normalizeSettingsSection(index int) int {
	for _, meta := range settingsNavigationSections() {
		if meta.Index == index {
			return index
		}
	}
	return settingsSectionGeneral
}

// settingsSectionKey returns the navigation rail key of a section index.
func settingsSectionKey(index int) string {
	return settingsSectionMetaFor(index).Key
}

// settingsSectionFromKey resolves a navigation rail key to its section index.
func settingsSectionFromKey(key string) (int, bool) {
	if key == "general" {
		return settingsSectionGeneral, true
	}
	for _, meta := range settingsNavigationSections() {
		if meta.Key == key {
			return meta.Index, true
		}
	}
	return 0, false
}

// setSettingsSection records the section chosen in the navigation rail.
func (a *AppController) setSettingsSection(index int) {
	a.mu.Lock()
	a.uiState.settingsTabIdx = index
	a.mu.Unlock()
}

func (a *AppController) requestSettingsSection(index int) {
	se := a.settingsEditor()
	if seGet(se, func(s *settingsEditorState) bool { return s.leavePrompt }) {
		return
	}
	if a.settingsDraftDirty() {
		seSet(se, func(s *settingsEditorState) { s.pendingSection, s.leavePrompt = index, true })
		return
	}
	a.setSettingsSection(index)
}

func (a *AppController) settingsDraftDirty() bool {
	state := seGet(a.settingsEditor(), func(s *settingsEditorState) struct {
		snippetIsNew, glossaryIsNew, workflowLoaded, workflowIsNew              bool
		snippetSelected, glossarySelected, workflowOriginalID, workflowSelected string
		snippetDraft                                                            config.SnippetItem
		glossaryDraft                                                           config.GlossaryTerm
		workflowDraft                                                           config.Workflow
		leaderNewKey, leaderNewLabel                                            string
	} {
		return struct {
			snippetIsNew, glossaryIsNew, workflowLoaded, workflowIsNew              bool
			snippetSelected, glossarySelected, workflowOriginalID, workflowSelected string
			snippetDraft                                                            config.SnippetItem
			glossaryDraft                                                           config.GlossaryTerm
			workflowDraft                                                           config.Workflow
			leaderNewKey, leaderNewLabel                                            string
		}{s.snippetIsNew, s.glossaryIsNew, s.workflowLoaded, s.workflowIsNew, s.snippetSelected, s.glossarySelected, s.workflowOriginalID, s.workflowSelected, s.snippetDraft, s.glossaryDraft, s.workflowDraft, s.leaderNewKey, s.leaderNewLabel}
	})
	cfg := a.ConfigStore.Current()
	if state.snippetIsNew || state.snippetSelected != "" {
		var saved config.SnippetItem
		found := false
		for _, item := range cfg.Snippets.Items {
			if item.ID == state.snippetSelected {
				saved, found = item, true
				break
			}
		}
		if state.snippetIsNew || !found || !reflect.DeepEqual(saved, state.snippetDraft) {
			return true
		}
	}
	if state.glossaryIsNew || state.glossarySelected != "" {
		var saved config.GlossaryTerm
		found := false
		for _, item := range cfg.Glossary.Terms {
			if item.Term == state.glossarySelected {
				saved, found = item, true
				break
			}
		}
		if state.glossaryIsNew || !found || !reflect.DeepEqual(saved, state.glossaryDraft) {
			return true
		}
	}
	if state.workflowLoaded || state.workflowIsNew {
		var saved config.Workflow
		found := false
		for _, item := range cfg.Workflows.Workflows {
			if item.ID == state.workflowOriginalID || item.ID == state.workflowSelected {
				saved, found = item, true
				break
			}
		}
		if state.workflowIsNew || !found || !reflect.DeepEqual(cloneWorkflow(saved), normalizeWorkflow(state.workflowDraft)) {
			return true
		}
	}
	return state.leaderNewKey != "" || state.leaderNewLabel != ""
}

// SettingsView renders the native Clavis Settings visual configuration center.
func (a *AppController) SettingsView(c *ui.Context) {
	cfg := a.Config()
	theme := resolveAppearanceTheme(c, cfg.Main.Appearance.Theme)
	c.SetTheme(theme)

	// The key source is kept current where it can change (profile switch,
	// key saved or cleared, config saved or reloaded, Settings opened):
	// reading the keychain here, every frame and under a.mu, hung Clavis at
	// launch (see refreshAPIKeySource).
	a.mu.Lock()
	selectedTab := a.uiState.settingsTabIdx
	statusMsg := a.settingsStatus
	apiKeySource := a.apiKeySource
	aiTestStatus := a.aiTestStatus
	aiTestMsg := a.aiTestMessage
	a.mu.Unlock()
	query := seGet(a.settingsEditor(), func(s *settingsEditorState) string { return s.railQuery })
	diag := a.Diagnostics()

	section := normalizeSettingsSection(selectedTab)

	applyTextLocale(ui.Row(c).Fill().Background(theme.Background)).Children(func() {
		a.renderSettingsSidebar(c, section, cfg, query)
		ui.Box(c).Width(space1).FillHeight().Background(hairline(theme))
		ui.Column(c).FillHeight().Grow(1).Padding(space6, space10).Gap(space6).Children(func() {
			a.renderSettingsPaneHeader(c, theme, section, statusMsg)
			ui.Scroll(c).FillWidth().Grow(1).Children(func() {
				ui.Column(c).FillWidth().Gap(space6).Children(func() {
					a.renderSettingsSection(c, theme, cfg, section, apiKeySource, aiTestStatus, aiTestMsg, diag)
				})
			})
		})
	})
}

// renderSettingsSidebar draws the navigation rail of sections.
func (a *AppController) renderSettingsSidebar(c *ui.Context, section int, cfg config.ClavisConfig, query string) {
	theme := resolveAppearanceTheme(c, cfg.Main.Appearance.Theme)
	selected := settingsSectionKey(section)
	ui.Column(c).Width(railWidth).FillHeight().Padding(cardPadY, space8).Gap(space4).Children(func() {
		ui.Text(c, i18n.T("settings.center")).FontSize(typeLabel).FontWeight(600).TextColor(theme.TextMuted).Padding(space8, space4).SingleLine()
		search := query
		input := ui.TextInput(c.Key("settings-search"), &search).Placeholder(i18n.T("settings.search.placeholder")).FillWidth()
		if input.Changed() {
			seSet(a.settingsEditor(), func(s *settingsEditorState) { s.railQuery = search })
			q := strings.ToLower(strings.TrimSpace(search))
			for _, candidate := range settingsNavigationSections() {
				if settingsSectionMatches(candidate, q, cfg) {
					a.setSettingsSection(candidate.Index)
					break
				}
			}
		}
		q := strings.ToLower(strings.TrimSpace(search))
		lastGroup := ""
		for _, meta := range settingsNavigationSections() {
			item := meta
			if q != "" && !settingsSectionMatches(item, q, cfg) {
				continue
			}
			if item.Group != lastGroup {
				for _, group := range settingsGroups() {
					if group.Key == item.Group {
						ui.Text(c.Key("settings-rail-group:"+group.Key), group.Label).FontSize(typeMeta).FontWeight(600).TextColor(theme.TextMuted).Padding(space8, space4).SingleLine()
						break
					}
				}
				lastGroup = item.Group
			}
			active := item.Key == selected
			row := ui.ButtonBase(c).Key("settings-rail:"+item.Key).FillWidth().Height(controlHeight).
				Padding(space4, space8).Radius(radiusRow).Gap(space8).AlignItems(ui.Center).FocusRing(false)
			if active {
				row = kitInteractive(c, theme, row, interactionState{Selected: true}, motionProfiles[animationOff])
				x, y, blur, spread, shadow := elevationShadow(theme, elev1)
				row.Shadow(x, y, blur, spread, shadow)
			}
			row.Children(func() {
				kitIconTile(c, theme, item.Icon, active)
				ui.Text(c, item.Label).FontSize(typeLabel).TextColor(theme.Text).SingleLine().Grow(1).MinWidth(0)
				if badge := settingsSectionBadge(item.Index, cfg); badge != "" {
					ui.Text(c, badge).FontSize(typeMeta).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
				}
			})
			if row.Clicked() {
				if index, ok := settingsSectionFromKey(item.Key); ok {
					a.requestSettingsSection(index)
				}
			}
		}
		se := a.settingsEditor()
		if seGet(se, func(s *settingsEditorState) bool { return s.leavePrompt }) {
			ui.Row(c).Key("settings-draft-warning").FillWidth().Gap(space4).Children(func() {
				ui.Text(c, i18n.T("settings.draft.unsaved")).FontSize(typeMeta).TextColor(theme.TextMuted).Grow(1)
				if ui.Button(c, i18n.T("settings.draft.keep_editing")).Clicked() {
					seSet(se, func(s *settingsEditorState) { s.leavePrompt = false })
				}
				if ui.PrimaryButton(c, i18n.T("settings.draft.discard")).Clicked() {
					pending := seGet(se, func(s *settingsEditorState) int { return s.pendingSection })
					seSet(se, func(s *settingsEditorState) { *s = settingsEditorState{} })
					a.setSettingsSection(pending)
				}
			})
		}
	})
}

func settingsSectionMatches(meta settingsSectionMeta, query string, cfg config.ClavisConfig) bool {
	compact := func(value string) string {
		value = strings.ToLower(value)
		return strings.NewReplacer("_", "", "-", "", " ", "").Replace(value)
	}
	extra := ""
	if meta.Key == "security" {
		extra = "security vault password secret keychain ttl 安全 密码 密钥"
	}
	haystack := compact(strings.Join([]string{meta.Key, meta.Label, meta.Hint, extra, fmt.Sprintf("%+v", cfg)}, " "))
	return strings.Contains(haystack, compact(query))
}

func settingsStatusIsFailure(status string) bool {
	status = strings.ToLower(status)
	return strings.Contains(status, "failed") || strings.Contains(status, "error") || strings.Contains(status, "失败") || strings.Contains(status, "错误")
}

// settingsSectionBadge returns the short count badge of a section.
func settingsSectionBadge(index int, cfg config.ClavisConfig) string {
	switch index {
	case settingsSectionIMEKeymap:
		return fmt.Sprintf("%d", len(cfg.IMERules.Rules))
	case settingsSectionLeader:
		return fmt.Sprintf("%d", len(cfg.Leader.Nodes))
	case settingsSectionSnippets:
		return fmt.Sprintf("%d", len(cfg.Snippets.Items))
	case settingsSectionGlossary:
		return fmt.Sprintf("%d", len(cfg.Glossary.Terms))
	case settingsSectionWorkflows:
		return fmt.Sprintf("%d", len(cfg.Workflows.Workflows))
	case settingsSectionAI:
		return fmt.Sprintf("%d", len(cfg.Main.AI.Profiles))
	case settingsSectionClipboard:
		return fmt.Sprintf("%d", len(cfg.Main.Clipboard.ExcludedBundleIDs))
	case settingsSectionConfigs:
		return fmt.Sprintf("%d", len(config.AllConfigFileKinds()))
	}
	return ""
}

// renderSettingsPaneHeader draws the section caption and the status pill.
func (a *AppController) renderSettingsPaneHeader(c *ui.Context, theme *ui.Theme, section int, statusMsg string) {
	meta := settingsSectionMetaFor(section)
	groupLabel := meta.Group
	for _, group := range settingsGroups() {
		if group.Key == meta.Group {
			groupLabel = group.Label
			break
		}
	}
	// A plain row rather than kitHeaderBar: the pane header sits directly under
	// the window's title bar and must not add the bar's own padding. The height
	// stays deliberately tight so the first card of every section is reachable
	// without scrolling at the window's default size.
	ui.Row(c).FillWidth().Height(controlHeight).Gap(space8).AlignItems(ui.Center).Children(func() {
		renderSemanticIcon(c, meta.Icon, IconSize16, secondaryInk(theme))
		ui.Column(c).Gap(space1).Grow(1).Children(func() {
			ui.Text(c, groupLabel+" / "+meta.Label).FontSize(typeTitle).FontWeight(600).TextColor(theme.Text).SingleLine()
			ui.Text(c, meta.Hint).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		})
		if statusMsg != "" {
			if settingsStatusIsFailure(statusMsg) {
				settingsBanner(c, theme.Danger, statusMsg)
			} else {
				kitStatusPill(c, theme, statusMsg, chipNeutral)
			}
		}
	})
	kitDivider(c, theme)
}

// renderSettingsSection dispatches to the renderer of the section selected.
func (a *AppController) renderSettingsSection(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
	section int,
	apiKeySource secrets.APIKeySource,
	aiTestStatus AITestStatus,
	aiTestMsg string,
	diag DiagnosticsSnapshot,
) {
	switch section {
	case settingsSectionIMEKeymap:
		a.renderIMEKeymapSection(c, theme, cfg)
	case settingsSectionLeader:
		a.renderLeaderSection(c, theme, cfg)
	case settingsSectionSnippets:
		a.renderSnippetsSection(c, theme, cfg)
	case settingsSectionGlossary:
		a.renderGlossarySection(c, theme, cfg)
	case settingsSectionWorkflows:
		a.renderWorkflowsSection(c, theme, cfg)
	case settingsSectionClipboard:
		a.renderSettingsClipboardSection(c, theme, cfg)
	case settingsSectionAppearance:
		a.renderSettingsAppearanceSection(c, theme, cfg)
	case settingsSectionSecurity:
		a.renderSettingsSecuritySection(c, theme)
	case settingsSectionAbout:
		a.renderSettingsAboutSection(c, theme)
	case settingsSectionAI:
		a.renderSettingsAISection(c, theme, cfg, apiKeySource, aiTestStatus, aiTestMsg)
	case settingsSectionPermissions:
		a.renderSettingsPermissionsSection(c, theme, diag)
	case settingsSectionConfigs:
		a.renderSettingsConfigsSection(c, theme)
	default:
		a.renderSettingsGeneralSection(c, theme, cfg)
	}
}

// These wrappers expose the grouped navigation's dedicated secondary pages.
func (a *AppController) renderSettingsClipboardSection(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	a.renderClipboardCard(c, theme, cfg)
}

func (a *AppController) renderSettingsAppearanceSection(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	a.renderSettingsAppearanceCard(c, theme, cfg)
}

func (a *AppController) renderSettingsAboutSection(c *ui.Context, theme *ui.Theme) {
	settingsSourceCard(c, theme, i18n.T("settings.section.about"), "[about]", func() {
		settingsHint(c, theme, i18n.T("settings.section.about.hint"))
	})
	a.renderSyncStatusCard(c, theme)
}

// renderSettingsGeneralSection draws the interactive trigger and appearance editors.
func (a *AppController) renderSettingsGeneralSection(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	se := a.settingsEditor()
	trigger := cfg.Main.Trigger
	keys := triggerKeyOptions(runtime.GOOS)

	settingsSourceCard(c, theme, i18n.T("settings.card.trigger"), "[trigger]", func() {
		if v, changed := settingSelectRow(c, theme, i18n.T("settings.trigger.leader_key"), "leader_key",
			i18n.T("settings.trigger.leader_key_help"), keys, trigger.LeaderKey, 180); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Trigger.LeaderKey = v })
		}
		if v, changed := settingSelectRow(c, theme, i18n.T("settings.trigger.double_tap_key"), "double_tap_key",
			i18n.T("settings.trigger.double_tap_key_help"), keys, trigger.DoubleTapKey, 180); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Trigger.DoubleTapKey = v })
		}
		ui.Grid(c).Columns(2).GapX(space16).GapY(space4).FillWidth().Children(func() {
			if v, changed := settingNumberRow(c, theme, i18n.T("settings.trigger.double_tap_interval"), "double_tap_interval_ms", "",
				float64(trigger.DoubleTapIntervalMs), 100, 800, 10, "ms"); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Trigger.DoubleTapIntervalMs = int(v) })
			}
			// Which-key pause: the Leader menu shows only if no key follows
			// within this delay (0 shows it at once).
			if v, changed := settingNumberRow(c, theme, i18n.T("settings.trigger.leader_reveal_delay"), "leader_reveal_delay_ms",
				i18n.T("settings.trigger.leader_reveal_delay_help"),
				float64(trigger.LeaderRevealDelayMs), 0, config.MaxLeaderRevealDelayMs, 20, "ms"); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Trigger.LeaderRevealDelayMs = int(v) })
			}
		})

		settingsCardDivider(c, theme)
		a.renderShortcutEditor(c, theme, se, "capsule_shortcut", "option+space",
			i18n.T("settings.trigger.capsule_shortcut"), trigger.CapsuleShortcut,
			func(m *config.MainConfig, v string) { m.Trigger.CapsuleShortcut = v })
		a.renderShortcutEditor(c, theme, se, "clipboard_shortcut", "alt+shift+c",
			i18n.T("settings.trigger.clipboard_shortcut"), trigger.ClipboardShortcut,
			func(m *config.MainConfig, v string) { m.Trigger.ClipboardShortcut = v })

		settingsCardDivider(c, theme)
		if v, changed := settingSwitchRow(c, theme, i18n.T("settings.trigger.snippet_expansion"),
			"snippet_expansion_enabled", "", trigger.SnippetExpansionEnabled); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Trigger.SnippetExpansionEnabled = v })
		}
		if v, changed := settingSwitchRow(c, theme, i18n.T("settings.trigger.snippet_backspace_undo"),
			"snippet_backspace_undo", "", trigger.SnippetBackspaceUndo); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Trigger.SnippetBackspaceUndo = v })
		}
	})

	a.renderSettingsAppearanceCard(c, theme, cfg)

	a.mu.Lock()
	statusMsg := a.settingsStatus
	a.mu.Unlock()
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		if ui.PrimaryButton(c, i18n.T("common.reload")).Clicked() {
			a.ReloadConfig()
		}
		if statusMsg != "" {
			ui.Text(c, statusMsg).FontSize(typeLabel).TextColor(theme.TextMuted)
		}
	})
}

func (a *AppController) renderSettingsAppearanceCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	appearance := cfg.Main.Appearance
	settingsSourceCard(c, theme, i18n.T("settings.card.appearance"), "[appearance]", func() {
		if v, changed := settingChoiceRow(c, theme, i18n.T("settings.appearance.theme"), "theme", "",
			themeOptions(), appearance.Theme); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.Theme = v })
		}
		if v, changed := settingChoiceRow(c, theme, i18n.T("settings.appearance.language"), "language", "",
			languageOptions(), appearance.Language); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.Language = v })
		}
		if v, changed := settingChoiceRow(c, theme, i18n.T("settings.appearance.animations"), "animations", "",
			animationOptions(), appearance.Animations); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.Animations = v })
		}

		settingsCardDivider(c, theme)
		ui.Grid(c).Columns(2).GapX(space16).GapY(space4).FillWidth().Children(func() {
			if v, changed := settingSwitchRow(c, theme, i18n.T("settings.appearance.key_hints"), "show_key_hints", "",
				appearance.ShowKeyHints); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.ShowKeyHints = v })
			}
			if v, changed := settingSwitchRow(c, theme, i18n.T("settings.appearance.preview_pane"), "show_preview_pane", "",
				appearance.ShowPreviewPane); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.ShowPreviewPane = v })
			}
			if v, changed := settingNumberRow(c, theme, i18n.T("settings.appearance.capsule_width"), "capsule_width", "",
				appearance.CapsuleWidth, 400, 1200, 20, "px"); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.CapsuleWidth = v })
			}
			if v, changed := settingNumberRow(c, theme, i18n.T("settings.appearance.max_visible_results"), "max_visible_results", "",
				float64(appearance.MaxVisibleResults), minVisibleResults, maxVisibleResults, 1, ""); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.MaxVisibleResults = int(v) })
			}
			if v, changed := settingNumberRow(c, theme, i18n.T("settings.appearance.corner_radius"), "corner_radius", "",
				appearance.CornerRadius, 0, 32, 2, "px"); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.CornerRadius = v })
			}
			if v, changed := settingNumberRow(c, theme, i18n.T("settings.appearance.hud_duration"), "hud_duration_ms", "",
				float64(appearance.HUDDurationMs), 200, 5000, 100, "ms"); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.HUDDurationMs = int(v) })
			}
		})

		settingsCardDivider(c, theme)
		ui.Text(c, i18n.T("settings.appearance.spatial_look")).FontSize(typeMeta).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
		if v, changed := settingChoiceRow(c, theme, i18n.T("settings.appearance.visual_preset"), "visual_preset", "",
			visualPresetOptions(), appearance.VisualPreset); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.VisualPreset = v })
		}
		if v, changed := settingSliderRow(c, theme, i18n.T("settings.appearance.stage_intensity"), "stage_intensity", "",
			appearance.StageIntensity, 0, 1, 0.05); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.StageIntensity = config.NormalizeStageIntensity(v) })
		}
		if v, changed := settingSwitchRow(c, theme, i18n.T("settings.appearance.oblique_rows"), "oblique_rows", "",
			appearance.ObliqueRows); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.ObliqueRows = v })
		}
		if v, changed := settingSliderRow(c, theme, i18n.T("settings.appearance.glass_clarity"), "glass_clarity", "",
			appearance.GlassClarity, 0, 1, 0.05); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.GlassClarity = v })
		}
	})
}

// renderSettingsAISection draws AI profiles, cc-switch impact preview, the
// Keychain / SecretRef editor and the active profile configuration.
func (a *AppController) renderSettingsAISection(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
	keySource secrets.APIKeySource,
	testStatus AITestStatus,
	testMsg string,
) {
	a.renderSettingsAIProfilesSection(c, theme, cfg, keySource, testStatus, testMsg)
}

// renderAIKeychainCard draws key status, the SecretRef resolution chain,
// the password-masked key input and the connection test.
func (a *AppController) renderAIKeychainCard(
	c *ui.Context,
	theme *ui.Theme,
	activeProfile config.AIProfile,
	keySource secrets.APIKeySource,
	testStatus AITestStatus,
	testMsg string,
) {
	settingsEditorCard(c, theme, i18n.T("settings.card.api_key"), func() {
		keyStatusLabel, configured, _ := aiKeySourceSummary(activeProfile, keySource)
		keyStatusColor := theme.Warning
		if configured {
			keyStatusColor = theme.Success
		}

		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("settings.ai.key_status")).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
			ui.Spacer(c)
			ui.Text(c, keyStatusLabel).FontSize(typeMeta).FontWeight(600).TextColor(keyStatusColor).SingleLine()
		})

		ui.Text(c, "SecretRef: "+aiProfileSecretRef(activeProfile)+" → "+aiProfileSecretFallbackRef(activeProfile)).
			FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()

		a.mu.Lock()
		draft := a.uiState.apiKeyDraft
		a.mu.Unlock()
		keyInput := ui.TextInput(c, &draft).
			Password().
			Placeholder(i18n.T("settings.ai.key_placeholder")).
			Label(i18n.T("settings.ai.key_label")).
			FillWidth()
		if keyInput.Changed() {
			a.mu.Lock()
			a.uiState.apiKeyDraft = draft
			a.mu.Unlock()
		}

		a.mu.Lock()
		currentDraft := a.uiState.apiKeyDraft
		a.mu.Unlock()

		ui.Row(c).FillWidth().Wrap().Gap(space8).AlignItems(ui.Center).Children(func() {
			if ui.PrimaryButton(c, i18n.T("settings.ai.save_key")).Disabled(strings.TrimSpace(currentDraft) == "").Clicked() {
				_ = a.SaveAPIKey(currentDraft)
			}
			if ui.Button(c, i18n.T("settings.ai.clear_key")).Clicked() {
				_ = a.DeleteAPIKey()
			}
			svc := c.Services()
			testLabel := i18n.T("settings.ai.test_connection")
			if testStatus == AITestLoading {
				if i18n.IsChinese() {
					testLabel = "测试中…"
				} else {
					testLabel = "Testing…"
				}
			}
			profID := activeProfile.ID
			if ui.Button(c, testLabel).Disabled(testStatus == AITestLoading).Clicked() {
				go func() {
					_ = a.TestAIConnectionForProfile(context.Background(), profID)
					svc.Invalidate()
				}()
			}
			if testStatus == AITestLoading {
				kitStatusDot(c, kitStateLoadingColor(theme), true)
			}
		})

		if testMsg != "" {
			if testStatus == AITestError {
				kitErrorBanner(c, theme, testMsg)
			} else {
				tone := chipNeutral
				if testStatus == AITestSuccess {
					tone = chipSuccess
				}
				if testStatus == AITestLoading {
					tone = chipAccent
				}
				kitStatusPill(c, theme, testMsg, tone)
			}
		}
	})
}

// renderSettingsPermissionsSection draws permissions, diagnostics and the
// configuration validation banners.
func (a *AppController) renderSettingsPermissionsSection(c *ui.Context, theme *ui.Theme, diag DiagnosticsSnapshot) {
	settingsEditorCard(c, theme, i18n.T("settings.card.permissions"), func() {
		svc := c.Services()
		renderPermRow(c, theme, i18n.T("settings.perm.accessibility"), diag.Permissions.Accessibility, func() {
			go func() {
				_, _ = a.PromptPermission("accessibility")
				svc.Invalidate()
			}()
		}, func() {
			_ = a.OpenPermissionSettings("accessibility")
		})

		renderPermRow(c, theme, i18n.T("settings.perm.input_monitoring"), diag.Permissions.InputMonitoring, func() {
			go func() {
				_, _ = a.PromptPermission("input_monitoring")
				svc.Invalidate()
			}()
		}, func() {
			_ = a.OpenPermissionSettings("input_monitoring")
		})
	})

	ui.Grid(c).Columns(settingsTwoColumns(c)).Gap(10).FillWidth().Children(func() {
		settingsEditorCard(c, theme, i18n.T("settings.card.diagnostics"), func() {
			secureInput := "false"
			if v, ok := diag.Permissions.Details["secureInput"]; ok && v != "" {
				secureInput = v
			}
			activeIME := FormatInputSourceStatus(diag.ActiveInputSource)
			layer := strings.TrimSpace(diag.ActiveKeymapLayer)
			if layer == "" {
				layer = "base"
			}

			settingsKeyValueRow(c, theme, i18n.T("settings.diag.layer"), layer)
			settingsKeyValueRow(c, theme, i18n.T("settings.diag.secure_input"), yesNo(secureInput))
			settingsKeyValueRow(c, theme, i18n.T("settings.diag.monitor"), yesNo(strconv.FormatBool(diag.MonitorRunning)))
			settingsKeyValueRow(c, theme, i18n.T("settings.diag.recovery"), fmt.Sprintf("%d", diag.RecoveryCount))
			settingsKeyValueRow(c, theme, i18n.T("settings.diag.input_source"), activeIME)
			if diag.LastIMEError != "" {
				settingsKeyValueRow(c, theme, i18n.T("settings.diag.ime_error"), diag.LastIMEError)
			}
			if v := diag.Permissions.Details["mouseHook"]; v != "" {
				settingsKeyValueRow(c, theme, i18n.T("settings.diag.mouse_hook"), v)
			}
			if v := diag.Permissions.Details["hookError"]; v != "" {
				settingsKeyValueRow(c, theme, i18n.T("settings.diag.hook_error"), v)
			}

			ui.Row(c).FillWidth().Gap(8).Children(func() {
				svc := c.Services()
				if ui.Button(c, i18n.T("settings.diag.refresh")).Clicked() {
					go func() {
						_, _ = a.RefreshPermissions(context.Background())
						svc.Invalidate()
					}()
				}
			})
		})

		renderConfigValidationCard(c, a, theme)
	})
}

// renderConfigValidationCard draws the configuration validation banners.
func renderConfigValidationCard(c *ui.Context, a *AppController, theme *ui.Theme) {
	errs := a.ConfigStore.LastErrors()
	warns := a.ConfigStore.LastWarnings()
	settingsEditorCard(c, theme, i18n.T("settings.card.validation"), func() {
		if len(errs) == 0 && len(warns) == 0 {
			ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
				renderSemanticIcon(c, "checkmark", typeLabel, theme.Success)
				ui.Text(c, i18n.T("settings.validation.ok")).FontSize(typeLabel).TextColor(theme.Success).Wrap().Grow(1)
			})
			return
		}
		for _, err := range errs {
			kitErrorBanner(c, theme, i18n.Tf("settings.validation.error", err.Error()))
		}
		for _, w := range warns {
			kitWarningBanner(c, theme, i18n.Tf("settings.validation.warning", w.File, w.KeyPath, w.Message))
		}
	})
}

// renderSettingsConfigsSection draws the TOML file quick-openers.
func (a *AppController) renderSettingsConfigsSection(c *ui.Context, theme *ui.Theme) {
	dir := a.ConfigStore.Directory()
	if dir == "" {
		dir = "~/.config/clavis"
	}

	settingsEditorCard(c, theme, i18n.T("settings.card.config_dir"), func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, "folder", 14, secondaryInk(theme))
			ui.Text(c, dir).FontSize(typeLabel).TextColor(theme.Text).Grow(1)
			if ui.PrimaryButton(c, i18n.T("settings.configs.open_dir")).Clicked() {
				_ = a.OpenConfigDirectory()
			}
		})
	})

	settingsEditorCard(c, theme, i18n.T("settings.card.toml_files"), func() {
		for _, kind := range config.AllConfigFileKinds() {
			k := kind
			ui.Row(c).Key("cfgfile:"+string(k)).FillWidth().Padding(space2, 0).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(1).Children(func() {
					ui.Text(c, string(k)).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
					ui.Text(c, configFileDescription(k)).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
				})
				if ui.Button(c, i18n.T("common.open")).Key("open:" + string(k)).Clicked() {
					_ = a.OpenConfigFile(k)
				}
			})
		}
	})
}

func renderPermRow(
	c *ui.Context,
	theme *ui.Theme,
	label string,
	granted bool,
	onPrompt func(),
	onOpenSettings func(),
) {
	statusText := i18n.T("settings.perm.not_granted")
	statusColor := theme.Warning
	if granted {
		statusText = i18n.T("settings.perm.granted")
		statusColor = theme.Success
	}
	ui.Row(c).Key("perm:" + label).FillWidth().Height(controlHeight).Gap(space8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(typeBody).TextColor(theme.Text).SingleLine().Grow(1)
		kitStatusDot(c, statusColor, false)
		ui.Text(c, statusText).FontSize(typeLabel).FontWeight(600).TextColor(statusColor).SingleLine()
		if ui.Button(c, i18n.T("settings.perm.prompt")).Key("prompt:"+label).Clicked() && onPrompt != nil {
			onPrompt()
		}
		if ui.Button(c, i18n.T("settings.perm.open_settings")).Key("settings:"+label).Clicked() && onOpenSettings != nil {
			onOpenSettings()
		}
	})
}

// yesNo shows a "true"/"false" diagnostic as Yes or No; other values (a
// platform detail) show as they are.
func boolGauge(v bool) float32 {
	if v {
		return 1
	}
	return 0
}

func yesNo(v string) string {
	switch v {
	case "true":
		return i18n.T("common.yes")
	case "false":
		return i18n.T("common.no")
	}
	return v
}

// settingsSourceCard is a settings card naming, on the right of its title,
// the config table or file its rows write ("[trigger]", "ime-rules.toml").
func settingsSourceCard(c *ui.Context, theme *ui.Theme, title, source string, body func()) {
	kitCard(c, theme, cardElevated, func() {
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine().Grow(1)
			if source != "" {
				ui.Text(c, source).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
			}
		})
		kitDivider(c, theme)
		body()
	})
}

// settingsCardDivider separates groups of rows inside a card.
func settingsCardDivider(c *ui.Context, theme *ui.Theme) {
	ui.Box(c).FillWidth().Height(space1).Margin(space2, 0).Background(hairline(theme))
}

func settingsEditorCard(c *ui.Context, theme *ui.Theme, title string, body func()) {
	kitCard(c, theme, cardElevated, func() {
		ui.Text(c, title).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
		kitDivider(c, theme)
		body()
	})
}

// settingsInsetGroup is the flatter tier for nested groups within an editor card.
func settingsInsetGroup(c *ui.Context, theme *ui.Theme, body func()) {
	kitCard(c, theme, cardInset, body)
}

func settingsKeyValuePair(c *ui.Context, theme *ui.Theme, key, val string) {
	ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, key).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		ui.Text(c, val).FontSize(typeMeta).FontWeight(500).TextColor(theme.Text).SingleLine().Grow(1)
	})
}

func settingsKeyValueRow(c *ui.Context, theme *ui.Theme, key, val string) {
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, key).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		ui.Spacer(c)
		ui.Text(c, val).FontSize(typeMeta).FontWeight(500).TextColor(theme.Text).SingleLine()
	})
}

func configFileDescription(kind config.ConfigFileKind) string {
	switch kind {
	case config.FileMain:
		return i18n.T("settings.configs.desc.main")
	case config.FileLeader:
		return i18n.T("settings.configs.desc.leader")
	case config.FileSnippets:
		return i18n.T("settings.configs.desc.snippets")
	case config.FileGlossary:
		return i18n.T("settings.configs.desc.glossary")
	case config.FileIMERules:
		return i18n.T("settings.configs.desc.ime_rules")
	case config.FilePrompts:
		if i18n.IsChinese() {
			return "带参数槽位的 AI 提示词模板"
		}
		return "AI prompt templates with interactive slots"
	case config.FileComponents:
		if i18n.IsChinese() {
			return "扩展脚本与自定义面板组件"
		}
		return "Custom scripts and panel components"
	case config.FileWorkflows:
		return i18n.T("settings.configs.desc.workflows")
	default:
		return ""
	}
}

// renderShortcutEditor draws one shortcut setting: its name and config line
// on the left; on the right, the chord as text (editable) and a recorder.
func (a *AppController) renderShortcutEditor(
	c *ui.Context,
	theme *ui.Theme,
	se *settingsEditorState,
	field string,
	placeholder string,
	title string,
	current string,
	apply func(m *config.MainConfig, v string),
) {
	recording := seGet(se, func(s *settingsEditorState) bool { return s.recordingShortcut == field })
	ui.Row(c).Key("shortcut-row:"+field).FillWidth().Gap(space12).AlignItems(ui.Center).Padding(space2, 0).Children(func() {
		name := settingRowName(c, theme, title, settingCaption(field, current), "")
		shortcut := current
		in := ui.TextInput(c.Key("shortcut-input:"+field), &shortcut).
			Label(field).
			Placeholder(placeholder).
			Width(170).
			Shrink(0)
		in.LabelledBy(name)
		if in.Changed() {
			normalized := normalizeShortcutText(shortcut)
			if normalized != "" && normalized != current {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { apply(m, normalized) })
			}
		}
		if recording {
			box := ui.Box(c).
				Key("shortcut-recorder:"+field).
				Padding(space4, space10).
				Radius(radiusChip).
				Background(selectionFill(theme)).
				Border(1, selectionEdge(theme)).
				Shrink(0).
				Focusable().
				AutoFocus().
				HandleInput(func(ev ui.InputEvent) bool {
					if ev.Kind != ui.InputKeyDown {
						return false
					}
					if ev.Key == ui.KeyEscape && ev.Mods == 0 {
						seSet(se, func(s *settingsEditorState) { s.recordingShortcut = "" })
						return true
					}
					combo, ok := shortcutFromKey(ev.Key, ev.Mods)
					if !ok {
						return true
					}
					seSet(se, func(s *settingsEditorState) { s.recordingShortcut = "" })
					_ = a.UpdateMainConfig(func(m *config.MainConfig) { apply(m, combo) })
					return true
				})
			box.Children(func() {
				ui.Text(c, i18n.T("settings.trigger.recording")).
					FontSize(typeMeta).
					TextColor(accentInk(theme, theme.Surface)).
					SingleLine()
			})
		} else if ui.Button(c.Key("shortcut-record:"+field), i18n.T("settings.trigger.record")).Shrink(0).Clicked() {
			seSet(se, func(s *settingsEditorState) { s.recordingShortcut = field })
		}
	})
}
