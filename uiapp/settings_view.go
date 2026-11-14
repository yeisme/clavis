package uiapp

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/ai"
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
)

// settingsSectionMeta describes one navigation rail entry.
type settingsSectionMeta struct {
	Index    int
	Key      string
	Label    string
	Icon     string
	Hint     string
	Settings SettingsTab
}

// settingsSections returns the ordered navigation rail entries.
func settingsSections() []settingsSectionMeta {
	return []settingsSectionMeta{
		{settingsSectionGeneral, "general", i18n.T("settings.section.general"), "bolt", i18n.T("settings.section.general.hint"), SettingsTabGeneral},
		{settingsSectionIMEKeymap, "ime-keymap", i18n.T("settings.section.ime"), "keyboard", i18n.T("settings.section.ime.hint"), SettingsTabIMEKeymap},
		{settingsSectionLeader, "leader", i18n.T("settings.section.leader"), "command", i18n.T("settings.section.leader.hint"), SettingsTabGeneral},
		{settingsSectionSnippets, "snippets", i18n.T("omnibox.tab.snippets"), "text.badge.plus", i18n.T("settings.section.snippets.hint"), SettingsTabGeneral},
		{settingsSectionGlossary, "glossary", i18n.T("settings.section.glossary"), "character.book.closed", i18n.T("settings.section.glossary.hint"), SettingsTabGeneral},
		{settingsSectionAI, "ai", i18n.T("settings.section.ai"), "sparkles", i18n.T("settings.section.ai.hint"), SettingsTabAI},
		{settingsSectionPermissions, "permissions", i18n.T("settings.section.permissions"), "gearshape", i18n.T("settings.section.permissions.hint"), SettingsTabPermissions},
		{settingsSectionConfigs, "configs", i18n.T("settings.section.configs"), "folder", i18n.T("settings.section.configs.hint"), SettingsTabConfigs},
	}
}

// settingsSectionMetaFor resolves the metadata of a section index.
func settingsSectionMetaFor(index int) settingsSectionMeta {
	for _, meta := range settingsSections() {
		if meta.Index == index {
			return meta
		}
	}
	return settingsSections()[0]
}

// normalizeSettingsSection clamps an unknown section index to the first one.
func normalizeSettingsSection(index int) int {
	for _, meta := range settingsSections() {
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
	for _, meta := range settingsSections() {
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

// SettingsView renders the native Clavis Settings visual configuration center.
func (a *AppController) SettingsView(c *ui.Context) {
	cfg := a.Config()
	theme := resolveAppearanceTheme(c, cfg.Main.Appearance.Theme)
	c.SetTheme(theme)

	a.mu.Lock()
	selectedTab := a.uiState.settingsTabIdx
	statusMsg := a.settingsStatus
	a.refreshAPIKeySourceLocked()
	apiKeySource := a.apiKeySource
	aiTestStatus := a.aiTestStatus
	aiTestMsg := a.aiTestMessage
	a.mu.Unlock()
	diag := a.Diagnostics()

	section := normalizeSettingsSection(selectedTab)

	applyTextLocale(ui.Row(c).Fill().Background(theme.Background)).Children(func() {
		a.renderSettingsSidebar(c, section, cfg)
		ui.Box(c).Width(1).FillHeight().Background(theme.Border)
		ui.Column(c).FillHeight().Grow(1).Padding(10, 14).Gap(8).Children(func() {
			a.renderSettingsPaneHeader(c, theme, section, statusMsg)
			ui.Scroll(c).FillWidth().Grow(1).Children(func() {
				ui.Column(c).FillWidth().Gap(10).Children(func() {
					a.renderSettingsSection(c, theme, cfg, section, apiKeySource, aiTestStatus, aiTestMsg, diag)
				})
			})
		})
	})
}

// renderSettingsSidebar draws the navigation rail of sections.
func (a *AppController) renderSettingsSidebar(c *ui.Context, section int, cfg config.ClavisConfig) {
	selected := settingsSectionKey(section)
	sb := ui.Sidebar(c, &selected, func() {
		ui.SidebarSection(c, i18n.T("settings.center"), nil, func() {
			for _, meta := range settingsSections() {
				item := meta
				ui.SidebarItem(c, item.Key, SemanticIconSVG(item.Icon), item.Label).Children(func() {
					if badge := settingsSectionBadge(item.Index, cfg); badge != "" {
						ui.Badge(c, badge)
					}
				})
			}
		})
	}).Width(195).FillHeight()
	if sb.Changed() {
		if index, ok := settingsSectionFromKey(selected); ok {
			a.setSettingsSection(index)
		}
	}
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
	case settingsSectionAI:
		return fmt.Sprintf("%d", len(cfg.Main.AI.Profiles))
	case settingsSectionConfigs:
		return fmt.Sprintf("%d", len(config.AllConfigFileKinds()))
	}
	return ""
}

// renderSettingsPaneHeader draws the section caption and the status pill.
func (a *AppController) renderSettingsPaneHeader(c *ui.Context, theme *ui.Theme, section int, statusMsg string) {
	meta := settingsSectionMetaFor(section)
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		renderSemanticIcon(c, meta.Icon, 16, theme.Accent)
		ui.Column(c).Gap(1).Grow(1).Children(func() {
			ui.Text(c, meta.Label+" — "+meta.Hint).FontSize(13).FontWeight(600).TextColor(theme.Text).SingleLine()
		})
		if statusMsg != "" {
			ui.Box(c).
				Padding(3, 9).
				Radius(999).
				Background(withAlpha(theme.Accent, 0.14)).
				Children(func() {
					ui.Text(c, statusMsg).FontSize(11).TextColor(theme.Accent).SingleLine()
				})
		}
	})
	ui.Divider(c)
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

// renderSettingsGeneralSection draws the interactive trigger and appearance editors.
func (a *AppController) renderSettingsGeneralSection(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	se := a.settingsEditor()
	trigger := cfg.Main.Trigger
	appearance := cfg.Main.Appearance

	settingsSectionCard(c, theme, i18n.T("settings.card.trigger"), func() {
		settingsInlineRow(c, theme, "leader_key", 110, func() {
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				if v, changed := settingsPillGrid(c, theme, "leader-key", 3, triggerPresets(), trigger.LeaderKey); changed {
					_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Trigger.LeaderKey = v })
				}
			})
		})
		settingsInlineRow(c, theme, "double_tap_key", 110, func() {
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				if v, changed := settingsPillGrid(c, theme, "double-tap-key", 3, triggerPresets(), trigger.DoubleTapKey); changed {
					_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Trigger.DoubleTapKey = v })
				}
			})
		})
		if v, changed := settingsStepper(
			c, theme, "double_tap_interval_ms", float64(trigger.DoubleTapIntervalMs), 100, 800, 10, "ms",
		); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Trigger.DoubleTapIntervalMs = int(v) })
		}

		a.renderShortcutEditor(c, theme, se, "capsule_shortcut", "option+space",
			i18n.Tf("settings.trigger.shortcut_caption", trigger.CapsuleShortcut), trigger.CapsuleShortcut,
			func(m *config.MainConfig, v string) { m.Trigger.CapsuleShortcut = v })
		a.renderShortcutEditor(c, theme, se, "clipboard_shortcut", "alt+shift+c",
			i18n.Tf("settings.trigger.clipboard_shortcut_caption", trigger.ClipboardShortcut), trigger.ClipboardShortcut,
			func(m *config.MainConfig, v string) { m.Trigger.ClipboardShortcut = v })

		if v, changed := settingsSwitch(
			c, theme, "snippet_expansion_enabled", i18n.T("settings.trigger.snippet_expansion"), trigger.SnippetExpansionEnabled,
		); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Trigger.SnippetExpansionEnabled = v })
		}
	})

	settingsSectionCard(c, theme, i18n.T("settings.card.appearance"), func() {
		settingsInlineRow(c, theme, "theme", 110, func() {
			if v, changed := settingsPills(c, theme, "theme", []string{"system", "light", "dark"}, appearance.Theme); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.Theme = v })
			}
		})
		settingsInlineRow(c, theme, "language", 110, func() {
			if v, changed := settingsPills(c, theme, "language", i18n.AllLanguages(), appearance.Language); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.Language = v })
			}
		})
		settingsInlineRow(c, theme, "animations", 110, func() {
			if v, changed := settingsPills(c, theme, "animations", AllAnimationModes(), appearance.Animations); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.Animations = v })
			}
		})
		ui.Grid(c).Columns(2).GapX(10).GapY(4).FillWidth().Children(func() {
			if v, changed := settingsStepper(c, theme, "capsule_width", appearance.CapsuleWidth, 400, 1200, 20, "px"); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.CapsuleWidth = v })
			}
			if v, changed := settingsStepper(c, theme, "max_visible_results", float64(appearance.MaxVisibleResults), 3, 20, 1, ""); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.MaxVisibleResults = int(v) })
			}
			if v, changed := settingsStepper(c, theme, "corner_radius", appearance.CornerRadius, 0, 32, 2, "px"); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.CornerRadius = v })
			}
			if v, changed := settingsStepper(c, theme, "hud_duration_ms", float64(appearance.HUDDurationMs), 200, 5000, 100, "ms"); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.HUDDurationMs = int(v) })
			}
		})
		ui.Grid(c).Columns(2).GapX(10).GapY(4).FillWidth().Children(func() {
			if v, changed := settingsSwitch(c, theme, "show_key_hints", i18n.T("settings.appearance.key_hints"), appearance.ShowKeyHints); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.ShowKeyHints = v })
			}
			if v, changed := settingsSwitch(c, theme, "show_preview_pane", i18n.T("settings.appearance.preview_pane"), appearance.ShowPreviewPane); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.ShowPreviewPane = v })
			}
		})
	})

	a.mu.Lock()
	statusMsg := a.settingsStatus
	a.mu.Unlock()
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		if ui.PrimaryButton(c, i18n.T("common.reload")).Clicked() {
			a.ReloadConfig()
		}
		if statusMsg != "" {
			ui.Text(c, statusMsg).FontSize(12).TextColor(theme.TextMuted)
		}
	})
}

// renderSettingsAISection draws AI profiles, the Keychain editor and the
// active profile configuration.
func (a *AppController) renderSettingsAISection(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
	keySource secrets.APIKeySource,
	testStatus AITestStatus,
	testMsg string,
) {
	activeProfile := a.ActiveAIProfile()
	profiles := cfg.Main.AI.Profiles
	if len(profiles) == 0 {
		profiles = config.DefaultAIProfiles()
	}

	settingsSectionCard(c, theme, i18n.T("settings.card.ai_profiles"), func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("settings.ai.active_profile")).FontSize(11).TextColor(theme.TextMuted).SingleLine()
			for _, prof := range profiles {
				p := prof
				isActive := strings.EqualFold(p.ID, activeProfile.ID)
				pill := ui.ButtonBase(c).
					Key("ai-prof:"+p.ID).
					Padding(3, 10).
					Radius(6).
					FocusRing(false)
				if isActive {
					pill.Background(theme.Accent)
				} else {
					pill.Background(theme.Background).Border(1, theme.Border)
				}
				pill.Children(func() {
					txt := ui.Text(c, p.ID).FontSize(12).SingleLine()
					if isActive {
						txt.TextColor(theme.AccentText).FontWeight(600)
					} else {
						txt.TextColor(theme.Text)
					}
				})
				if pill.Clicked() {
					_ = a.SelectAIProfile(p.ID)
				}
			}
		})

		protoStr := string(activeProfile.Protocol)
		if protoStr == "" {
			protoStr = string(config.ProtocolOpenAICompatible)
		}
		envVarStr := strings.TrimSpace(activeProfile.APIKeyEnvVar)
		if envVarStr == "" {
			envVarStr = secrets.DefaultEnvVarForProvider(activeProfile.Provider)
		}
		if envVarStr == "" {
			envVarStr = i18n.T("settings.ai.none_keyless")
		}

		ui.Grid(c).Columns(2).GapX(10).GapY(2).FillWidth().Children(func() {
			settingsKeyValuePair(c, theme, "provider", activeProfile.Provider)
			settingsKeyValuePair(c, theme, "protocol", protoStr)
			settingsKeyValuePair(c, theme, "base_url", activeProfile.BaseURL)
			settingsKeyValuePair(c, theme, "model", activeProfile.Model)
			settingsKeyValuePair(c, theme, "keychain_account", activeProfile.KeychainAccount)
			settingsKeyValuePair(c, theme, "api_key_env_var", envVarStr)
		})
	})

	ui.Grid(c).Columns(2).Gap(10).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			a.renderAIKeychainCard(c, theme, activeProfile, keySource, testStatus, testMsg)
			a.renderAIAddProfileCard(c, theme)
		})
		a.renderAIProfileEditor(c, theme, cfg)
	})
}

// renderAIKeychainCard draws key status, the key input and the connection test.
func (a *AppController) renderAIKeychainCard(
	c *ui.Context,
	theme *ui.Theme,
	activeProfile config.AIProfile,
	keySource secrets.APIKeySource,
	testStatus AITestStatus,
	testMsg string,
) {
	settingsSectionCard(c, theme, i18n.T("settings.card.api_key"), func() {
		keyStatusLabel := i18n.T("settings.ai.key_not_configured")
		keyStatusColor := theme.Warning
		switch keySource {
		case secrets.KeySourceKeychain:
			keyStatusLabel = i18n.T("settings.ai.key_in_keychain")
			keyStatusColor = theme.Success
		case secrets.KeySourceEnv:
			envName := strings.TrimSpace(activeProfile.APIKeyEnvVar)
			if envName == "" {
				envName = secrets.DefaultEnvVarForProvider(activeProfile.Provider)
			}
			if envName == "" {
				envName = "API_KEY"
			}
			keyStatusLabel = i18n.Tf("settings.ai.key_via_env", envName)
			keyStatusColor = theme.Success
		default:
			if ai.IsKeylessAllowed(activeProfile.Provider, activeProfile.BaseURL) {
				keyStatusLabel = i18n.T("settings.ai.key_local")
				keyStatusColor = theme.Success
			}
		}

		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("settings.ai.key_status")).FontSize(11).TextColor(theme.TextMuted).SingleLine()
			ui.Spacer(c)
			ui.Text(c, keyStatusLabel).FontSize(11).FontWeight(600).TextColor(keyStatusColor).SingleLine()
		})

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

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.PrimaryButton(c, i18n.T("settings.ai.save_key")).Disabled(strings.TrimSpace(currentDraft) == "").Clicked() {
				_ = a.SaveAPIKey(currentDraft)
			}
			if ui.Button(c, i18n.T("settings.ai.clear_key")).Clicked() {
				_ = a.DeleteAPIKey()
			}
		})

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			svc := c.Services()
			testLabel := i18n.T("settings.ai.test_connection")
			if testStatus == AITestLoading {
				testLabel = "Testing..."
			}
			profID := activeProfile.ID
			if ui.Button(c, testLabel).Disabled(testStatus == AITestLoading).Clicked() {
				go func() {
					_ = a.TestAIConnectionForProfile(context.Background(), profID)
					svc.Invalidate()
				}()
			}
		})

		if testMsg != "" {
			bannerColor := theme.TextMuted
			bannerBg := theme.Surface
			switch testStatus {
			case AITestSuccess:
				bannerColor = theme.Success
				bannerBg = withAlpha(theme.Success, 0.12)
			case AITestError:
				bannerColor = theme.Danger
				bannerBg = withAlpha(theme.Danger, 0.12)
			case AITestLoading:
				bannerColor = theme.Accent
				bannerBg = withAlpha(theme.Accent, 0.12)
			}
			ui.Box(c).
				FillWidth().
				Padding(5, 9).
				Radius(6).
				Background(bannerBg).
				Children(func() {
					ui.Text(c, testMsg).FontSize(11).TextColor(bannerColor)
				})
		}
	})
}

// renderSettingsPermissionsSection draws permissions, diagnostics and the
// configuration validation banners.
func (a *AppController) renderSettingsPermissionsSection(c *ui.Context, theme *ui.Theme, diag DiagnosticsSnapshot) {
	settingsSectionCard(c, theme, i18n.T("settings.card.permissions"), func() {
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

	ui.Grid(c).Columns(2).Gap(10).FillWidth().Children(func() {
		settingsSectionCard(c, theme, i18n.T("settings.card.diagnostics"), func() {
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
			settingsKeyValueRow(c, theme, i18n.T("settings.diag.secure_input"), secureInput)
			settingsKeyValueRow(c, theme, i18n.T("settings.diag.monitor"), fmt.Sprintf("%v", diag.MonitorRunning))
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
	settingsSectionCard(c, theme, i18n.T("settings.card.validation"), func() {
		if len(errs) == 0 && len(warns) == 0 {
			ui.Text(c, i18n.T("settings.validation.ok")).FontSize(12).TextColor(theme.Success)
			return
		}
		for i, err := range errs {
			ui.Box(c).
				Key(fmt.Sprintf("cfg-err:%d", i)).
				FillWidth().
				Padding(6, 10).
				Radius(6).
				Background(withAlpha(theme.Danger, 0.12)).
				Children(func() {
					applyProseLineHeight(ui.Text(c, i18n.Tf("settings.validation.error", err.Error()))).FontSize(11).TextColor(theme.Danger)
				})
		}
		for i, w := range warns {
			ui.Box(c).
				Key(fmt.Sprintf("cfg-warn:%d", i)).
				FillWidth().
				Padding(6, 10).
				Radius(6).
				Background(withAlpha(theme.Warning, 0.12)).
				Children(func() {
					applyProseLineHeight(ui.Text(c, i18n.Tf("settings.validation.warning", w.File, w.KeyPath, w.Message))).
						FontSize(11).
						TextColor(theme.Warning)
				})
		}
	})
}

// renderSettingsConfigsSection draws the TOML file quick-openers.
func (a *AppController) renderSettingsConfigsSection(c *ui.Context, theme *ui.Theme) {
	dir := a.ConfigStore.Directory()
	if dir == "" {
		dir = "~/.config/clavis"
	}

	settingsSectionCard(c, theme, i18n.T("settings.card.config_dir"), func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, "folder", 14, theme.Accent)
			ui.Text(c, dir).FontSize(12).TextColor(theme.Text).Grow(1)
			if ui.PrimaryButton(c, i18n.T("settings.configs.open_dir")).Clicked() {
				_ = a.OpenConfigDirectory()
			}
		})
	})

	settingsSectionCard(c, theme, i18n.T("settings.card.toml_files"), func() {
		for _, kind := range config.AllConfigFileKinds() {
			k := kind
			ui.Row(c).Key("cfgfile:"+string(k)).FillWidth().Padding(3, 0).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(1).Children(func() {
					ui.Text(c, string(k)).FontSize(12).FontWeight(600).TextColor(theme.Text).SingleLine()
					ui.Text(c, configFileDescription(k)).FontSize(11).TextColor(theme.TextMuted).SingleLine()
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
	ui.Row(c).Key("perm:" + label).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(13).TextColor(theme.Text).SingleLine().Grow(1)
		ui.Text(c, statusText).FontSize(12).FontWeight(600).TextColor(statusColor).SingleLine()
		if ui.Button(c, i18n.T("settings.perm.prompt")).Key("prompt:"+label).Clicked() && onPrompt != nil {
			onPrompt()
		}
		if ui.Button(c, i18n.T("settings.perm.open_settings")).Key("settings:"+label).Clicked() && onOpenSettings != nil {
			onOpenSettings()
		}
	})
}

func settingsSectionCard(c *ui.Context, theme *ui.Theme, title string, body func()) {
	ui.Column(c).
		FillWidth().
		Padding(8, 10).
		Radius(10).
		Background(theme.Surface).
		Border(1, theme.Border).
		Gap(5).
		Children(func() {
			ui.Text(c, title).FontSize(12).FontWeight(600).TextColor(theme.Text).SingleLine()
			ui.Divider(c)
			body()
		})
}

func settingsKeyValuePair(c *ui.Context, theme *ui.Theme, key, val string) {
	ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, key).FontSize(10).TextColor(theme.TextMuted).SingleLine()
		ui.Text(c, val).FontSize(11).FontWeight(500).TextColor(theme.Text).SingleLine().Grow(1)
	})
}

func settingsKeyValueRow(c *ui.Context, theme *ui.Theme, key, val string) {
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, key).FontSize(11).TextColor(theme.TextMuted).SingleLine()
		ui.Spacer(c)
		ui.Text(c, val).FontSize(11).FontWeight(500).TextColor(theme.Text).SingleLine()
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
	default:
		return ""
	}
}

// renderShortcutEditor draws a shortcut text input plus a chord recorder for one trigger field.
func (a *AppController) renderShortcutEditor(
	c *ui.Context,
	theme *ui.Theme,
	se *settingsEditorState,
	field string,
	placeholder string,
	caption string,
	current string,
	apply func(m *config.MainConfig, v string),
) {
	recording := seGet(se, func(s *settingsEditorState) bool { return s.recordingShortcut == field })
	ui.Text(c, caption).
		FontSize(11).
		TextColor(theme.TextMuted).
		SingleLine()
	ui.Row(c).Key("shortcut-row:" + field).FillWidth().Gap(8).AlignItems(ui.End).Children(func() {
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			shortcut := current
			in := ui.TextInput(c.Key("shortcut-input:"+field), &shortcut).
				Label(field).
				Placeholder(placeholder).
				FillWidth()
			if in.Changed() {
				normalized := normalizeShortcutText(shortcut)
				if normalized != "" && normalized != current {
					_ = a.UpdateMainConfig(func(m *config.MainConfig) { apply(m, normalized) })
				}
			}
		})
		if recording {
			box := ui.Box(c).
				Key("shortcut-recorder:"+field).
				Padding(4, 10).
				Radius(6).
				Background(withAlpha(theme.Accent, 0.14)).
				Border(1, theme.Accent).
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
					FontSize(11).
					TextColor(theme.Accent).
					SingleLine()
			})
		} else {
			if ui.PrimaryButton(c.Key("shortcut-record:"+field), i18n.T("settings.trigger.record")).Clicked() {
				seSet(se, func(s *settingsEditorState) { s.recordingShortcut = field })
			}
		}
	})
}
