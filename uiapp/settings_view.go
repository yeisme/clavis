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

// SettingsView renders the native 5-tab Clavis Settings window using mygo/ui.
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

	tabNames := SettingsTabNames()
	if selectedTab < 0 || selectedTab >= len(tabNames) {
		selectedTab = 0
	}

	ui.Column(c).Fill().Background(theme.Background).Padding(14, 18).Gap(10).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "Clavis Settings").FontSize(18).FontWeight(700).TextColor(theme.Text).SingleLine()
				ui.Text(c, "Declarative keyboard launcher, snippets, clipboard, keymap & AI").
					FontSize(12).
					TextColor(theme.TextMuted).
					SingleLine()
			})
			ui.Spacer(c)
			if statusMsg != "" {
				ui.Box(c).
					Padding(4, 10).
					Radius(6).
					Background(withAlpha(theme.Accent, 0.12)).
					Children(func() {
						ui.Text(c, statusMsg).FontSize(12).TextColor(theme.Accent).SingleLine()
					})
			}
		})

		tabs := ui.Tabs(c, &selectedTab, tabNames...)
		if tabs.Changed() {
			a.mu.Lock()
			a.uiState.settingsTabIdx = selectedTab
			a.mu.Unlock()
		}

		ui.Divider(c)

		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			switch SettingsTab(selectedTab) {
			case SettingsTabGeneral:
				a.renderSettingsGeneralTab(c, theme, cfg, statusMsg)
			case SettingsTabAI:
				a.renderSettingsAITab(c, theme, cfg, apiKeySource, aiTestStatus, aiTestMsg)
			case SettingsTabIMEKeymap:
				a.renderSettingsIMEKeymapTab(c, theme, cfg)
			case SettingsTabPermissions:
				a.renderSettingsPermissionsTab(c, theme, diag)
			case SettingsTabConfigs:
				a.renderSettingsConfigsTab(c, theme)
			}
		})
	})
}

func (a *AppController) renderSettingsGeneralTab(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
	statusMsg string,
) {
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		settingsSectionCard(c, theme, "Triggers", func() {
			settingsKeyValueRow(c, theme, "leader_key", cfg.Main.Trigger.LeaderKey)
			settingsKeyValueRow(c, theme, "double_tap_key", cfg.Main.Trigger.DoubleTapKey)
			settingsKeyValueRow(c, theme, "double_tap_interval_ms", fmt.Sprintf("%d ms", cfg.Main.Trigger.DoubleTapIntervalMs))
			settingsKeyValueRow(c, theme, "capsule_shortcut", cfg.Main.Trigger.CapsuleShortcut)
			settingsKeyValueRow(c, theme, "snippet_expansion_enabled", fmt.Sprintf("%v", cfg.Main.Trigger.SnippetExpansionEnabled))
		})

		settingsSectionCard(c, theme, "Appearance", func() {
			settingsKeyValueRow(c, theme, "theme", cfg.Main.Appearance.Theme)
			settingsKeyValueRow(c, theme, "capsule_width", fmt.Sprintf("%.0f px", cfg.Main.Appearance.CapsuleWidth))
			settingsKeyValueRow(c, theme, "max_visible_results", fmt.Sprintf("%d", cfg.Main.Appearance.MaxVisibleResults))
			settingsKeyValueRow(c, theme, "corner_radius", fmt.Sprintf("%.0f px", cfg.Main.Appearance.CornerRadius))
			settingsKeyValueRow(c, theme, "hud_duration_ms", fmt.Sprintf("%d ms", cfg.Main.Appearance.HUDDurationMs))
		})

		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			if ui.PrimaryButton(c, "Reload Configuration").Clicked() {
				a.ReloadConfig()
			}
			if statusMsg != "" {
				ui.Text(c, statusMsg).FontSize(12).TextColor(theme.TextMuted)
			}
		})
	})
}

func (a *AppController) renderSettingsAITab(
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

	ui.Column(c).FillWidth().Gap(10).Children(func() {
		settingsSectionCard(c, theme, "AI Profiles & Provider Configuration", func() {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Active Profile").FontSize(12).TextColor(theme.TextMuted).SingleLine()
				ui.Spacer(c)
				for _, prof := range profiles {
					p := prof
					isActive := strings.EqualFold(p.ID, activeProfile.ID)
					pill := ui.ButtonBase(c).
						Key("ai-prof:"+p.ID).
						Padding(4, 10).
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

			settingsKeyValueRow(c, theme, "provider", activeProfile.Provider)
			settingsKeyValueRow(c, theme, "protocol", protoStr)
			settingsKeyValueRow(c, theme, "base_url", activeProfile.BaseURL)
			settingsKeyValueRow(c, theme, "model", activeProfile.Model)
			settingsKeyValueRow(c, theme, "keychain_account", activeProfile.KeychainAccount)
			settingsKeyValueRow(c, theme, "api_key_env_var", envVarStr)
		})

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
				ui.Text(c, "Key Status").FontSize(13).TextColor(theme.TextMuted).SingleLine()
				ui.Spacer(c)
				ui.Text(c, keyStatusLabel).FontSize(13).FontWeight(600).TextColor(keyStatusColor).SingleLine()
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

				ui.Spacer(c)

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
					Padding(6, 10).
					Radius(6).
					Background(bannerBg).
					Children(func() {
						ui.Text(c, testMsg).FontSize(12).TextColor(bannerColor)
					})
			}
		})
	})
}

func (a *AppController) renderSettingsIMEKeymapTab(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
) {
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		settingsSectionCard(c, theme, "Keymap & IME Behavior", func() {
			keymapEnabled := cfg.Main.Keymap.Enabled
			navLayerEnabled := cfg.Main.Keymap.NavLayerEnabled
			lockASCII := cfg.Main.IME.LockASCIIOnLeader
			showPill := cfg.Main.IME.ShowFloatingPill

			ui.Grid(c).Columns(2).Gap(8).FillWidth().Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Keymap Enabled (keymap.enabled)").FontSize(12).TextColor(theme.Text).SingleLine().Grow(1)
					sw := ui.Switch(c, &keymapEnabled).Label("Keymap Enabled")
					if sw.Changed() {
						_ = a.UpdateMainConfig(func(m *config.MainConfig) {
							m.Keymap.Enabled = keymapEnabled
						})
					}
				})

				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Nav Layer (nav_layer_enabled)").FontSize(12).TextColor(theme.Text).SingleLine().Grow(1)
					sw := ui.Switch(c, &navLayerEnabled).Label("Nav Layer")
					if sw.Changed() {
						_ = a.UpdateMainConfig(func(m *config.MainConfig) {
							m.Keymap.NavLayerEnabled = navLayerEnabled
						})
					}
				})

				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Lock ASCII on Leader (lock_ascii_on_leader)").FontSize(12).TextColor(theme.Text).SingleLine().Grow(1)
					sw := ui.Switch(c, &lockASCII).Label("Lock ASCII on Leader")
					if sw.Changed() {
						_ = a.UpdateMainConfig(func(m *config.MainConfig) {
							m.IME.LockASCIIOnLeader = lockASCII
						})
					}
				})

				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Show IME Floating Pill (show_floating_pill)").FontSize(12).TextColor(theme.Text).SingleLine().Grow(1)
					sw := ui.Switch(c, &showPill).Label("Show IME Floating Pill")
					if sw.Changed() {
						_ = a.UpdateMainConfig(func(m *config.MainConfig) {
							m.IME.ShowFloatingPill = showPill
						})
					}
				})
			})

			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("tap_hold_threshold_ms: %d ms", cfg.Main.Keymap.TapHoldThresholdMs)).
					FontSize(12).
					TextColor(theme.TextMuted).
					SingleLine()
				ui.Text(c, fmt.Sprintf("caps_lock_tap: %s", cfg.Main.Keymap.CapsLockTap)).
					FontSize(12).
					TextColor(theme.TextMuted).
					SingleLine()
				ui.Text(c, fmt.Sprintf("caps_lock_hold: %s", cfg.Main.Keymap.CapsLockHold)).
					FontSize(12).
					TextColor(theme.TextMuted).
					SingleLine()
			})

			if len(cfg.Main.Keymap.Remaps) == 0 {
				ui.Text(c, "Custom remaps (keymap.remaps): None configured").
					FontSize(11).
					TextColor(theme.TextMuted).
					SingleLine()
			} else {
				for idx, r := range cfg.Main.Keymap.Remaps {
					summary := fmt.Sprintf("remap[%d]: %s → tap=%s hold=%s", idx, r.From, r.Tap, r.Hold)
					if len(r.AppBundleIDs) > 0 {
						summary += fmt.Sprintf(" (apps: %s)", strings.Join(r.AppBundleIDs, ", "))
					}
					ui.Text(c, summary).FontSize(11).TextColor(theme.TextMuted).SingleLine()
				}
			}
		})

		settingsSectionCard(c, theme, "IME Auto-Switch Rules (ime-rules.toml)", func() {
			a.mu.Lock()
			targetDraft := a.uiState.imeRuleTargetDraft
			urlDraft := a.uiState.imeRuleURLDraft
			titleDraft := a.uiState.imeRuleTitleDraft
			sourceDraft := a.uiState.imeRuleSourceDraft
			modeDraft := a.uiState.imeRuleModeDraft
			a.mu.Unlock()
			if modeDraft == "" {
				modeDraft = config.IMEModeASCII
			}

			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				tIn := ui.TextInput(c, &targetDraft).
					Placeholder("Bundle ID or Process Name (e.g. com.apple.Safari)...").
					Label("Rule Target").
					Grow(2)
				if tIn.Changed() {
					a.mu.Lock()
					a.uiState.imeRuleTargetDraft = targetDraft
					a.mu.Unlock()
				}

				uIn := ui.TextInput(c, &urlDraft).
					Placeholder("URL Pattern (optional)...").
					Label("URL Pattern").
					Grow(1)
				if uIn.Changed() {
					a.mu.Lock()
					a.uiState.imeRuleURLDraft = urlDraft
					a.mu.Unlock()
				}

				tiIn := ui.TextInput(c, &titleDraft).
					Placeholder("Title Pattern (optional)...").
					Label("Title Pattern").
					Grow(1)
				if tiIn.Changed() {
					a.mu.Lock()
					a.uiState.imeRuleTitleDraft = titleDraft
					a.mu.Unlock()
				}
			})

			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				sIn := ui.TextInput(c, &sourceDraft).
					Placeholder("Input Source ID (default: com.apple.keylayout.ABC)...").
					Label("Input Source ID").
					Grow(1)
				if sIn.Changed() {
					a.mu.Lock()
					a.uiState.imeRuleSourceDraft = sourceDraft
					a.mu.Unlock()
				}

				for _, m := range config.AllIMEModes() {
					modeVal := m
					activeMode := modeDraft == modeVal
					mBtn := ui.ButtonBase(c).
						Key("ime-mode:"+string(modeVal)).
						Padding(4, 8).
						Radius(5).
						FocusRing(false)
					if activeMode {
						mBtn.Background(theme.Accent)
					} else {
						mBtn.Background(theme.Background).Border(1, theme.Border)
					}
					mBtn.Children(func() {
						txt := ui.Text(c, string(modeVal)).FontSize(11).SingleLine()
						if activeMode {
							txt.TextColor(theme.AccentText).FontWeight(600)
						} else {
							txt.TextColor(theme.TextMuted)
						}
					})
					if mBtn.Clicked() {
						a.mu.Lock()
						a.uiState.imeRuleModeDraft = modeVal
						a.mu.Unlock()
					}
				}

				a.mu.Lock()
				curTarget := strings.TrimSpace(a.uiState.imeRuleTargetDraft)
				curURL := strings.TrimSpace(a.uiState.imeRuleURLDraft)
				curTitle := strings.TrimSpace(a.uiState.imeRuleTitleDraft)
				curSource := strings.TrimSpace(a.uiState.imeRuleSourceDraft)
				curMode := a.uiState.imeRuleModeDraft
				a.mu.Unlock()
				if curMode == "" {
					curMode = config.IMEModeASCII
				}

				if ui.PrimaryButton(c, "Add Rule").Disabled(curTarget == "").Clicked() {
					newRule := config.IMERule{
						AppName:        curTarget,
						URLPattern:     curURL,
						TitlePattern:   curTitle,
						InputSourceID:  curSource,
						PreferredMode:  curMode,
						RestoreOnLeave: true,
					}
					if strings.Contains(curTarget, ".") {
						newRule.BundleID = curTarget
					} else {
						newRule.ProcessName = curTarget
					}
					_ = a.AddIMERule(newRule)
				}
			})

			ui.Divider(c)

			rules := cfg.IMERules.Rules
			if len(rules) == 0 {
				ui.Text(c, "No IME rules configured.").FontSize(12).TextColor(theme.TextMuted)
			} else {
				for i := len(rules) - 1; i >= 0; i-- {
					idx := i
					rule := rules[idx]
					targetLabel := rule.BundleID
					if targetLabel == "" {
						targetLabel = rule.ProcessName
					}
					if rule.AppName != "" && !strings.EqualFold(rule.AppName, targetLabel) {
						targetLabel = fmt.Sprintf("%s (%s)", rule.AppName, targetLabel)
					}

					var details []string
					details = append(details, fmt.Sprintf("mode=%s", rule.PreferredMode))
					details = append(details, fmt.Sprintf("source=%s", rule.InputSourceID))
					if rule.URLPattern != "" {
						details = append(details, fmt.Sprintf("url=%s", rule.URLPattern))
					}
					if rule.TitlePattern != "" {
						details = append(details, fmt.Sprintf("title=%s", rule.TitlePattern))
					}
					details = append(details, fmt.Sprintf("restore=%v", rule.RestoreOnLeave))

					ui.Row(c).
						Key(fmt.Sprintf("ime-rule:%d:%s", idx, targetLabel)).
						FillWidth().
						Padding(2, 4).
						Gap(8).
						AlignItems(ui.Center).
						Children(func() {
							ui.Text(c, targetLabel).FontSize(12).FontWeight(600).TextColor(theme.Text).SingleLine()
							ui.Text(c, strings.Join(details, " • ")).
								FontSize(11).
								TextColor(theme.TextMuted).
								SingleLine().
								Grow(1)
							if ui.Button(c, "Delete").Key(fmt.Sprintf("del-ime-rule:%d", idx)).Clicked() {
								_ = a.DeleteIMERule(idx)
							}
						})
				}
			}
		})
	})
}

func (a *AppController) renderSettingsPermissionsTab(
	c *ui.Context,
	theme *ui.Theme,
	diag DiagnosticsSnapshot,
) {
	ui.Column(c).FillWidth().Gap(12).Children(func() {
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

		errs := a.ConfigStore.LastErrors()
		warns := a.ConfigStore.LastWarnings()
		settingsSectionCard(c, theme, "Configuration Validation", func() {
			if len(errs) == 0 && len(warns) == 0 {
				ui.Text(c, "All configuration files are valid.").FontSize(13).TextColor(theme.Success)
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
						ui.Text(c, "ERROR: "+err.Error()).FontSize(12).TextColor(theme.Danger)
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
							FontSize(12).
							TextColor(theme.Warning)
					})
			}
		})
	})
}

func (a *AppController) renderSettingsConfigsTab(c *ui.Context, theme *ui.Theme) {
	dir := a.ConfigStore.Directory()
	if dir == "" {
		dir = "~/.config/clavis"
	}

	ui.Column(c).FillWidth().Gap(12).Children(func() {
		settingsSectionCard(c, theme, "Configuration Directory", func() {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, dir).FontSize(13).TextColor(theme.Text).Grow(1)
				if ui.PrimaryButton(c, "Open Config Folder").Clicked() {
					_ = a.OpenConfigDirectory()
				}
			})
		})

		settingsSectionCard(c, theme, "TOML Configuration Files", func() {
			for _, kind := range config.AllConfigFileKinds() {
				k := kind
				ui.Row(c).Key("cfgfile:"+string(k)).FillWidth().Padding(4, 0).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(2).Children(func() {
						ui.Text(c, string(k)).FontSize(13).FontWeight(600).TextColor(theme.Text).SingleLine()
						ui.Text(c, configFileDescription(k)).FontSize(11).TextColor(theme.TextMuted).SingleLine()
					})
					if ui.Button(c, "Open").Key("open:" + string(k)).Clicked() {
						_ = a.OpenConfigFile(k)
					}
				})
			}
		})
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
		Padding(10, 12).
		Radius(10).
		Background(theme.Surface).
		Border(1, theme.Border).
		Gap(6).
		Children(func() {
			ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(theme.Text).SingleLine()
			ui.Divider(c)
			body()
		})
}

func settingsKeyValueRow(c *ui.Context, theme *ui.Theme, key, val string) {
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, key).FontSize(12).TextColor(theme.TextMuted).SingleLine()
		ui.Spacer(c)
		ui.Text(c, val).FontSize(12).FontWeight(500).TextColor(theme.Text).SingleLine()
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
