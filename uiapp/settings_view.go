package uiapp

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/config"
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
		{settingsSectionGeneral, "general", "General & Triggers", "bolt", "Triggers, appearance and shortcuts", SettingsTabGeneral},
		{settingsSectionIMEKeymap, "ime-keymap", "IME & Keymap", "keyboard", "Key remaps, tap/hold and per-app input sources", SettingsTabIMEKeymap},
		{settingsSectionLeader, "leader", "Leader Tree", "command", "Recursive leader.toml key tree", SettingsTabGeneral},
		{settingsSectionSnippets, "snippets", "Snippets", "text.badge.plus", "snippets.toml master-detail editor", SettingsTabGeneral},
		{settingsSectionGlossary, "glossary", "Glossary & Clipboard", "character.book.closed", "glossary.toml and clipboard history", SettingsTabGeneral},
		{settingsSectionAI, "ai", "AI & Keychain", "sparkles", "Profiles, providers, keys and connection tests", SettingsTabAI},
		{settingsSectionPermissions, "permissions", "Permissions & Diagnostics", "gearshape", "Permissions, runtime diagnostics and validation", SettingsTabPermissions},
		{settingsSectionConfigs, "configs", "Configs", "folder", "TOML files and the configuration directory", SettingsTabConfigs},
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

	ui.Row(c).Fill().Background(theme.Background).Children(func() {
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
		ui.SidebarSection(c, "Configuration Center", nil, func() {
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

	settingsSectionCard(c, theme, "Trigger Controls (trigger.*)", func() {
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

		recording := seGet(se, func(s *settingsEditorState) bool { return s.recordingShortcut })
		ui.Text(c, fmt.Sprintf("capsule_shortcut: %s", trigger.CapsuleShortcut)).
			FontSize(11).
			TextColor(theme.TextMuted).
			SingleLine()
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.End).Children(func() {
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				shortcut := trigger.CapsuleShortcut
				in := ui.TextInput(c, &shortcut).
					Label("capsule_shortcut").
					Placeholder("option+space").
					FillWidth()
				if in.Changed() {
					normalized := normalizeShortcutText(shortcut)
					if normalized != "" && normalized != trigger.CapsuleShortcut {
						_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Trigger.CapsuleShortcut = normalized })
					}
				}
			})
			if recording {
				box := ui.Box(c).
					Key("shortcut-recorder").
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
							seSet(se, func(s *settingsEditorState) { s.recordingShortcut = false })
							return true
						}
						combo, ok := shortcutFromKey(ev.Key, ev.Mods)
						if !ok {
							return true
						}
						seSet(se, func(s *settingsEditorState) { s.recordingShortcut = false })
						_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Trigger.CapsuleShortcut = combo })
						return true
					})
				box.Children(func() {
					ui.Text(c, "Press shortcut... (Esc cancels)").
						FontSize(11).
						TextColor(theme.Accent).
						SingleLine()
				})
			} else {
				if ui.PrimaryButton(c, "Record Shortcut").Clicked() {
					seSet(se, func(s *settingsEditorState) { s.recordingShortcut = true })
				}
			}
		})

		if v, changed := settingsSwitch(
			c, theme, "snippet_expansion_enabled", "Snippet Expansion", trigger.SnippetExpansionEnabled,
		); changed {
			_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Trigger.SnippetExpansionEnabled = v })
		}
	})

	settingsSectionCard(c, theme, "Appearance (appearance.*)", func() {
		settingsInlineRow(c, theme, "theme", 110, func() {
			if v, changed := settingsPills(c, theme, "theme", []string{"system", "light", "dark"}, appearance.Theme); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.Theme = v })
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
			if v, changed := settingsSwitch(c, theme, "show_key_hints", "Show Key Hints", appearance.ShowKeyHints); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.ShowKeyHints = v })
			}
			if v, changed := settingsSwitch(c, theme, "show_preview_pane", "Show Preview Pane", appearance.ShowPreviewPane); changed {
				_ = a.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.ShowPreviewPane = v })
			}
		})
	})

	a.mu.Lock()
	statusMsg := a.settingsStatus
	a.mu.Unlock()
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		if ui.PrimaryButton(c, "Reload Configuration").Clicked() {
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

	settingsSectionCard(c, theme, "AI Profiles & Provider Configuration", func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Active Profile").FontSize(11).TextColor(theme.TextMuted).SingleLine()
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
			envVarStr = "(none / keyless)"
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
	settingsSectionCard(c, theme, "API Key & Keychain", func() {
		keyStatusLabel := "Not configured"
		keyStatusColor := theme.Warning
		switch keySource {
		case secrets.KeySourceKeychain:
			keyStatusLabel = "Configured in Keychain (••••••••)"
			keyStatusColor = theme.Success
		case secrets.KeySourceEnv:
			envName := strings.TrimSpace(activeProfile.APIKeyEnvVar)
			if envName == "" {
				envName = secrets.DefaultEnvVarForProvider(activeProfile.Provider)
			}
			if envName == "" {
				envName = "API_KEY"
			}
			keyStatusLabel = fmt.Sprintf("Configured via %s env (••••••••)", envName)
			keyStatusColor = theme.Success
		default:
			if ai.IsKeylessAllowed(activeProfile.Provider, activeProfile.BaseURL) {
				keyStatusLabel = "Local / Keyless (optional)"
				keyStatusColor = theme.Success
			}
		}

		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Key Status").FontSize(11).TextColor(theme.TextMuted).SingleLine()
			ui.Spacer(c)
			ui.Text(c, keyStatusLabel).FontSize(11).FontWeight(600).TextColor(keyStatusColor).SingleLine()
		})

		a.mu.Lock()
		draft := a.uiState.apiKeyDraft
		a.mu.Unlock()
		keyInput := ui.TextInput(c, &draft).
			Password().
			Placeholder("Enter API key (stored securely in Keychain)...").
			Label("API Key").
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
			if ui.PrimaryButton(c, "Save Key").Disabled(strings.TrimSpace(currentDraft) == "").Clicked() {
				_ = a.SaveAPIKey(currentDraft)
			}
			if ui.Button(c, "Clear Key").Clicked() {
				_ = a.DeleteAPIKey()
			}
		})

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			svc := c.Services()
			testLabel := "Test Connection"
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
	settingsSectionCard(c, theme, "Permissions", func() {
		svc := c.Services()
		renderPermRow(c, theme, "Accessibility", diag.Permissions.Accessibility, func() {
			go func() {
				_, _ = a.PromptPermission("accessibility")
				svc.Invalidate()
			}()
		}, func() {
			_ = a.OpenPermissionSettings("accessibility")
		})

		renderPermRow(c, theme, "Input Monitoring", diag.Permissions.InputMonitoring, func() {
			go func() {
				_, _ = a.PromptPermission("input_monitoring")
				svc.Invalidate()
			}()
		}, func() {
			_ = a.OpenPermissionSettings("input_monitoring")
		})
	})

	ui.Grid(c).Columns(2).Gap(10).FillWidth().Children(func() {
		settingsSectionCard(c, theme, "Runtime Diagnostics", func() {
			secureInput := "false"
			if v, ok := diag.Permissions.Details["secureInput"]; ok && v != "" {
				secureInput = v
			}
			activeIME := diag.ActiveInputSource.Name
			if activeIME == "" {
				activeIME = diag.ActiveInputSource.ID
			}
			if activeIME == "" {
				activeIME = "Unknown"
			}
			layer := strings.TrimSpace(diag.ActiveKeymapLayer)
			if layer == "" {
				layer = "base"
			}

			settingsKeyValueRow(c, theme, "Active Keymap Layer", layer)
			settingsKeyValueRow(c, theme, "Secure Input", secureInput)
			settingsKeyValueRow(c, theme, "Monitor Running", fmt.Sprintf("%v", diag.MonitorRunning))
			settingsKeyValueRow(c, theme, "Recovery Count", fmt.Sprintf("%d", diag.RecoveryCount))
			settingsKeyValueRow(c, theme, "Active Input Source", activeIME)

			ui.Row(c).FillWidth().Gap(8).Children(func() {
				svc := c.Services()
				if ui.Button(c, "Refresh").Clicked() {
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
	settingsSectionCard(c, theme, "Configuration Validation", func() {
		if len(errs) == 0 && len(warns) == 0 {
			ui.Text(c, "All configuration files are valid.").FontSize(12).TextColor(theme.Success)
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
					ui.Text(c, "ERROR: "+err.Error()).FontSize(11).TextColor(theme.Danger)
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
					ui.Text(c, fmt.Sprintf("WARNING [%s %s]: %s", w.File, w.KeyPath, w.Message)).
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

	settingsSectionCard(c, theme, "Configuration Directory", func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, "folder", 14, theme.Accent)
			ui.Text(c, dir).FontSize(12).TextColor(theme.Text).Grow(1)
			if ui.PrimaryButton(c, "Open Config Folder").Clicked() {
				_ = a.OpenConfigDirectory()
			}
		})
	})

	settingsSectionCard(c, theme, "TOML Configuration Files", func() {
		for _, kind := range config.AllConfigFileKinds() {
			k := kind
			ui.Row(c).Key("cfgfile:"+string(k)).FillWidth().Padding(3, 0).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(1).Children(func() {
					ui.Text(c, string(k)).FontSize(12).FontWeight(600).TextColor(theme.Text).SingleLine()
					ui.Text(c, configFileDescription(k)).FontSize(11).TextColor(theme.TextMuted).SingleLine()
				})
				if ui.Button(c, "Open").Key("open:" + string(k)).Clicked() {
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
	statusText := "Not Granted"
	statusColor := theme.Warning
	if granted {
		statusText = "Granted"
		statusColor = theme.Success
	}
	ui.Row(c).Key("perm:" + label).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(13).TextColor(theme.Text).SingleLine().Grow(1)
		ui.Text(c, statusText).FontSize(12).FontWeight(600).TextColor(statusColor).SingleLine()
		if ui.Button(c, "Prompt").Key("prompt:"+label).Clicked() && onPrompt != nil {
			onPrompt()
		}
		if ui.Button(c, "Open Settings").Key("settings:"+label).Clicked() && onOpenSettings != nil {
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
		return "Triggers, keymap, appearance, AI profiles, clipboard, and IME settings"
	case config.FileLeader:
		return "Leader key tree, submenus, and actions"
	case config.FileSnippets:
		return "Inline text expansion snippets"
	case config.FileGlossary:
		return "Canonical domain glossary terms and aliases"
	case config.FileIMERules:
		return "Per-application IME auto-switch rules"
	default:
		return ""
	}
}
