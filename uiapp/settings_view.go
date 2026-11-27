package uiapp

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	internaldiag "github.com/yeisme/clavis/internal/diag"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func (a *AppController) settingsDraftDirty() bool {
	return a.anySettingsPageDirty()
}

// SettingsView renders the native Clavis Settings visual configuration center.
func (a *AppController) SettingsView(c *ui.Context) {
	a.syncDocumentEdited()
	cfg := a.Config()
	theme := resolveAppearanceTheme(c, cfg.Main.Appearance.Theme)
	c.SetTheme(theme)

	// The key source is kept current where it can change (profile switch,
	// key saved or cleared, config saved or reloaded, Settings opened):
	// reading the keychain here, every frame and under a.mu, hung Clavis at
	// launch (see refreshAPIKeySource).
	a.mu.Lock()
	selectedPage := a.uiState.settingsPage
	apiKeySource := a.apiKeySource
	aiTestStatus := a.aiTestStatus
	aiTestMsg := a.aiTestMessage
	a.mu.Unlock()
	se := a.settingsEditor()
	diag := a.Diagnostics()

	pageKey := normalizeSettingsPageKey(selectedPage)

	if c.Shortcut(primaryMod(), ui.KeyF) {
		seSet(se, func(s *settingsEditorState) { s.searchFocusRequested = true })
	}
	groups := settingsGroups()
	groupKeys := []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5}
	for i, gk := range groupKeys {
		if i < len(groups) && c.Shortcut(primaryMod(), gk) {
			if firstPage, ok := settingsFirstPageInGroup(groups[i].Key); ok {
				a.setSettingsPage(firstPage)
				pageKey = firstPage
			}
			break
		}
	}

	query := seGet(se, func(s *settingsEditorState) string { return s.railQuery })

	root := applyTextLocale(ui.Row(c).Fill().Background(theme.Background))
	var searchFocused bool
	root.Children(func() {
		// Non-tab-stop focus sink so empty Esc in Search can unfocus without making
		// the root container itself a focus ancestor or Tab stop.
		ui.Box(c).Key("settings-focus-sink").Width(0).Height(0).Bind(&se.blurHandle).TabStop(false).FocusRing(false)
		searchFocused = a.renderSettingsSidebar(c, pageKey, cfg, query)
		ui.Box(c).Width(space1).FillHeight().Background(hairline(theme))
		ui.Column(c).FillHeight().Grow(1).Padding(space6, space10).Gap(space4).Children(func() {
			a.renderSettingsPaneHeader(c, theme, pageKey)
			a.renderSettingsFailureBanners(c, theme, pageKey)
			ui.Scroll(c).Key("settings-scroll:" + pageKey).FillWidth().Grow(1).Children(func() {
				ui.Column(c).FillWidth().Children(func() {
					topAnchor := ui.Box(c).Key("settings-scroll-top:" + pageKey).Height(0).FillWidth()
					if seGet(se, func(s *settingsEditorState) bool {
						if !s.pendingScrollTop {
							return false
						}
						s.pendingScrollTop = false
						return true
					}) {
						topAnchor.ScrollIntoView(ui.ScrollIntoViewOptions{Block: ui.ScrollStart})
					}
					ui.Column(c).FillWidth().Gap(space6).Children(func() {
						a.renderSettingsSection(c, theme, cfg, pageKey, apiKeySource, aiTestStatus, aiTestMsg, diag)
					})
				})
			})
		})
	})
	recording := seGet(se, func(s *settingsEditorState) bool { return s.recordingShortcut != "" })
	if !searchFocused && !recording && c.Shortcut(0, ui.KeyEscape) {
		a.handleSettingsRootEscape()
	}
}

// renderSettingsSidebar draws the fixed search box and scrollable navigation rail,
// reporting whether the search field holds keyboard focus.
func (a *AppController) renderSettingsSidebar(c *ui.Context, pageKey string, cfg config.ClavisConfig, query string) bool {
	theme := resolveAppearanceTheme(c, cfg.Main.Appearance.Theme)
	se := a.settingsEditor()
	selected := normalizeSettingsPageKey(pageKey)
	searchFocused := false
	// The sidebar is the elevated gray layer beside the white (or #1C1C1E)
	// content pane — opaque, like every reading surface; glass is for
	// floating panels only.
	ui.Column(c).Width(railWidth).FillHeight().Padding(space8, space8).Gap(space4).Background(theme.Surface).Children(func() {
		ui.Text(c, i18n.T("settings.center")).FontSize(typeLabel).FontWeight(600).TextColor(theme.TextMuted).Padding(space4, space4).SingleLine()
		search := query
		input := ui.TextInput(c.Key("settings-search"), &search).
			Bind(&se.searchHandle).
			Placeholder(i18n.T("settings.search.placeholder")).
			Label(i18n.T("settings.search.placeholder")).
			Search().
			FillWidth()
		if seGet(se, func(s *settingsEditorState) bool {
			if !s.searchFocusRequested {
				return false
			}
			s.searchFocusRequested = false
			return true
		}) {
			se.searchHandle.RequestFocus(c)
		}
		searchFocused = input.Focused()
		input.OnTextKey(func(k ui.TextKey) bool {
			qTrim := strings.TrimSpace(k.Text())
			switch {
			case k.Key == ui.KeyEscape && k.Mods == 0:
				if k.Text() != "" {
					k.SetText("")
					seSet(se, func(s *settingsEditorState) {
						s.railQuery = ""
						s.searchSelectedIdx = 0
					})
				} else {
					se.blurHandle.RequestFocus(c)
				}
				return true
			case k.Key == ui.KeyDown && k.Mods == 0 && qTrim != "":
				results := settingsSearchQuery(k.Text(), cfg)
				if len(results) > 0 {
					limit := min(len(results), 50)
					seSet(se, func(s *settingsEditorState) {
						s.searchSelectedIdx = (s.searchSelectedIdx + 1) % limit
					})
				}
				return true
			case k.Key == ui.KeyUp && k.Mods == 0 && qTrim != "":
				results := settingsSearchQuery(k.Text(), cfg)
				if len(results) > 0 {
					limit := min(len(results), 50)
					seSet(se, func(s *settingsEditorState) {
						s.searchSelectedIdx = (s.searchSelectedIdx - 1 + limit) % limit
					})
				}
				return true
			case k.Key == ui.KeyEnter && k.Mods == 0 && qTrim != "":
				results := settingsSearchQuery(k.Text(), cfg)
				if len(results) > 0 {
					limit := min(len(results), 50)
					idx := seGet(se, func(s *settingsEditorState) int { return s.searchSelectedIdx })
					if idx < 0 || idx >= limit {
						idx = 0
					}
					a.jumpToSettingsSearchResult(c, cfg, results[idx])
				}
				return true
			}
			return false
		})
		if input.Changed() {
			seSet(se, func(s *settingsEditorState) {
				s.railQuery = search
				s.searchSelectedIdx = 0
			})
		}

		ui.Scroll(c).Key("settings-rail-scroll").FillWidth().Grow(1).MinHeight(space0).Children(func() {
			ui.Column(c).FillWidth().Gap(space2).Children(func() {
				if strings.TrimSpace(search) != "" {
					a.renderSettingsSearchResults(c, theme, cfg, search)
					return
				}
				lastGroup := ""
				for _, meta := range settingsPages() {
					item := meta
					if item.Group != lastGroup {
						ui.Text(c.Key("settings-rail-group:"+item.Group), settingsGroupLabel(item.Group)).
							FontSize(typeMeta).FontWeight(600).TextColor(theme.TextMuted).Padding(space4, space4, space2, space4).SingleLine()
						lastGroup = item.Group
					}
					active := item.Key == selected
					row := ui.ButtonBase(c).Key("settings-rail:"+item.Key).FillWidth().Height(28).
						Padding(space2, space8).Radius(radiusRow).Gap(space8).AlignItems(ui.Center).FocusRing(false)
					if active {
						row = kitInteractive(c, theme, row, interactionState{Selected: true}, motionProfiles[animationOff])
					} else if row.Hovered() {
						row.Background(tileFill(theme))
					}
					row.Children(func() {
						kitIconTile(c, theme, item.Icon, active)
						label := ui.Text(c, item.Label).FontSize(typeLabel).TextColor(theme.Text).SingleLine().Grow(1).MinWidth(0)
						if active {
							label.FontWeight(600)
						}
						if badge := settingsPageBadge(item.Key, cfg); badge != "" {
							ui.Text(c, badge).FontSize(typeMeta).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
						}
						if a.hasFailureForPage(item.Key) {
							ui.Box(c).Key("settings-rail-error-dot:" + item.Key).Label("settings-rail-error-dot:" + item.Key).Shrink(0).Children(func() {
								kitStatusDot(c, theme.Danger, false)
							})
						} else if a.settingsPageDirty(item.Key) {
							ui.Box(c).Key("settings-rail-dirty-dot:" + item.Key).Label("settings-rail-dirty-dot:" + item.Key).Shrink(0).Children(func() {
								kitStatusDot(c, theme.Accent, false)
							})
						}
					})
					if row.Clicked() {
						a.setSettingsPage(item.Key)
					}
				}
			})
		})
	})
	return searchFocused
}

// renderSettingsPaneHeader draws the section caption and optional per-page status pill.
func (a *AppController) renderSettingsPaneHeader(c *ui.Context, theme *ui.Theme, pageKey string) {
	meta, ok := settingsPageByKey(pageKey)
	if !ok {
		meta = settingsPages()[0]
	}
	groupLabel := settingsGroupLabel(meta.Group)
	notice, hasNotice := a.pageNotice(meta.Key)
	ui.Row(c).FillWidth().MinHeight(controlHeight).Gap(space8).AlignItems(ui.Center).Children(func() {
		renderSemanticIcon(c, meta.Icon, IconSize16, secondaryInk(theme))
		ui.Column(c).Gap(0).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, groupLabel+" / "+meta.Label).FontSize(typeTitle).FontWeight(600).TextColor(theme.Text).Wrap()
			ui.Text(c, meta.Hint).FontSize(typeMeta).TextColor(theme.TextMuted).Wrap()
		})
		if hasNotice && notice.Text != "" {
			kitStatusPill(c, theme, notice.Text, notice.Tone)
		}
	})
	kitDivider(c, theme)
}

// renderSettingsSection dispatches to the renderer of the active settings page.
func (a *AppController) renderSettingsSection(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
	pageKey string,
	apiKeySource secrets.APIKeySource,
	aiTestStatus AITestStatus,
	aiTestMsg string,
	diag DiagnosticsSnapshot,
) {
	switch normalizeSettingsPageKey(pageKey) {
	case "ime-keymap":
		a.renderIMEKeymapSection(c, theme, cfg)
	case "leader":
		a.renderLeaderSection(c, theme, cfg)
	case "snippets":
		a.renderSnippetsSection(c, theme, cfg)
	case "glossary":
		a.renderGlossarySection(c, theme, cfg)
	case "workflows":
		a.renderWorkflowsSection(c, theme, cfg)
	case "templates":
		a.renderSettingsTemplatesSection(c, theme, cfg)
	case "clipboard":
		a.renderSettingsClipboardSection(c, theme, cfg)
	case "appearance":
		a.renderSettingsAppearanceSection(c, theme, cfg)
	case "security":
		a.renderSettingsSecuritySection(c, theme)
	case "about":
		a.renderSettingsAboutSection(c, theme)
	case "ai":
		a.renderSettingsAISection(c, theme, cfg, apiKeySource, aiTestStatus, aiTestMsg)
	case "permissions":
		a.renderSettingsPermissionsSection(c, theme, diag)
	case "configs":
		a.renderSettingsConfigsSection(c, theme)
	default:
		a.renderSettingsGeneralSection(c, theme, cfg)
	}
}

// These wrappers expose the grouped navigation's dedicated secondary pages.
func (a *AppController) renderSettingsClipboardSection(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	a.renderClipboardCard(c, theme, cfg)
	a.renderClipboardKeyManagerCard(c, theme, cfg)
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

// renderSettingsGeneralSection draws the interactive trigger editors.
func (a *AppController) renderSettingsGeneralSection(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	se := a.settingsEditor()
	trigger := cfg.Main.Trigger
	keys := triggerKeyOptions(runtime.GOOS)

	settingsSourceCard(c, theme, i18n.T("settings.card.trigger"), "[trigger]", func() {
		a.immediateSelectRow(c, theme, "triggers", i18n.T("settings.trigger.leader_key"), "leader_key",
			i18n.T("settings.trigger.leader_key_help"), keys, trigger.LeaderKey, 180,
			func(m *config.MainConfig, v string) { m.Trigger.LeaderKey = v })
		a.immediateSelectRow(c, theme, "triggers", i18n.T("settings.trigger.double_tap_key"), "double_tap_key",
			i18n.T("settings.trigger.double_tap_key_help"), keys, trigger.DoubleTapKey, 180,
			func(m *config.MainConfig, v string) { m.Trigger.DoubleTapKey = v })
		ui.Grid(c).Columns(2).GapX(space16).GapY(space4).FillWidth().Children(func() {
			a.immediateNumberRow(c, theme, "triggers", i18n.T("settings.trigger.double_tap_interval"), "double_tap_interval_ms", "",
				float64(trigger.DoubleTapIntervalMs), 100, 800, 10, "ms",
				func(m *config.MainConfig, v float64) { m.Trigger.DoubleTapIntervalMs = int(v) })
			// Which-key pause: the Leader menu shows only if no key follows
			// within this delay (0 shows it at once).
			a.immediateNumberRow(c, theme, "triggers", i18n.T("settings.trigger.leader_reveal_delay"), "leader_reveal_delay_ms",
				i18n.T("settings.trigger.leader_reveal_delay_help"),
				float64(trigger.LeaderRevealDelayMs), 0, config.MaxLeaderRevealDelayMs, 20, "ms",
				func(m *config.MainConfig, v float64) { m.Trigger.LeaderRevealDelayMs = int(v) })
		})

		settingsCardDivider(c, theme)
		a.renderShortcutEditor(c, theme, se, "capsule_shortcut", "option+space",
			i18n.T("settings.trigger.capsule_shortcut"), trigger.CapsuleShortcut,
			func(m *config.MainConfig, v string) { m.Trigger.CapsuleShortcut = v })
		a.renderShortcutEditor(c, theme, se, "clipboard_shortcut", "alt+shift+c",
			i18n.T("settings.trigger.clipboard_shortcut"), trigger.ClipboardShortcut,
			func(m *config.MainConfig, v string) { m.Trigger.ClipboardShortcut = v })
		a.renderShortcutEditor(c, theme, se, "screenshot_shortcut", "alt+shift+a",
			i18n.T("settings.section.triggers.screenshot_shortcut"), trigger.ScreenshotShortcut,
			func(m *config.MainConfig, v string) { m.Trigger.ScreenshotShortcut = v })

		settingsCardDivider(c, theme)
		a.immediateSwitchRow(c, theme, "triggers", i18n.T("settings.trigger.snippet_expansion"),
			"snippet_expansion_enabled", "", trigger.SnippetExpansionEnabled,
			func(m *config.MainConfig, v bool) { m.Trigger.SnippetExpansionEnabled = v })
		a.immediateSwitchRow(c, theme, "triggers", i18n.T("settings.trigger.snippet_backspace_undo"),
			"snippet_backspace_undo", "", trigger.SnippetBackspaceUndo,
			func(m *config.MainConfig, v bool) { m.Trigger.SnippetBackspaceUndo = v })
	})

	notice, hasNotice := a.pageNotice("triggers")
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		if ui.PrimaryButton(c, i18n.T("common.reload")).Clicked() {
			a.ReloadConfig()
		}
		if hasNotice && notice.Text != "" {
			ui.Text(c, notice.Text).FontSize(typeLabel).TextColor(theme.TextMuted)
		}
	})
}

func (a *AppController) renderSettingsAppearanceCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	appearance := cfg.Main.Appearance
	settingsSourceCard(c, theme, i18n.T("settings.card.appearance"), "[appearance]", func() {
		a.immediateChoiceRow(c, theme, "appearance", i18n.T("settings.appearance.theme"), "theme", "",
			themeOptions(), appearance.Theme, func(m *config.MainConfig, v string) { m.Appearance.Theme = v })
		a.immediateChoiceRow(c, theme, "appearance", i18n.T("settings.appearance.language"), "language", "",
			languageOptions(), appearance.Language, func(m *config.MainConfig, v string) { m.Appearance.Language = v })
		a.immediateChoiceRow(c, theme, "appearance", i18n.T("settings.appearance.animations"), "animations", "",
			animationOptions(), appearance.Animations, func(m *config.MainConfig, v string) { m.Appearance.Animations = v })

		settingsCardDivider(c, theme)
		ui.Grid(c).Columns(2).GapX(space16).GapY(space4).FillWidth().Children(func() {
			a.immediateSwitchRow(c, theme, "appearance", i18n.T("settings.appearance.key_hints"), "show_key_hints", "",
				appearance.ShowKeyHints, func(m *config.MainConfig, v bool) { m.Appearance.ShowKeyHints = v })
			a.immediateSwitchRow(c, theme, "appearance", i18n.T("settings.appearance.preview_pane"), "show_preview_pane", "",
				appearance.ShowPreviewPane, func(m *config.MainConfig, v bool) { m.Appearance.ShowPreviewPane = v })
			a.immediateNumberRow(c, theme, "appearance", i18n.T("settings.appearance.capsule_width"), "capsule_width", "",
				appearance.CapsuleWidth, 400, 1200, 20, "px",
				func(m *config.MainConfig, v float64) { m.Appearance.CapsuleWidth = v })
			a.immediateNumberRow(c, theme, "appearance", i18n.T("settings.appearance.max_visible_results"), "max_visible_results", "",
				float64(appearance.MaxVisibleResults), minVisibleResults, maxVisibleResults, 1, "",
				func(m *config.MainConfig, v float64) { m.Appearance.MaxVisibleResults = int(v) })
			se := a.settingsEditor()
			ui.Column(c).FillWidth().Gap(space2).Children(func() {
				if a.immediateNumberRow(c, theme, "appearance", i18n.T("settings.appearance.corner_radius"), "corner_radius", "",
					appearance.CornerRadius, 0, 32, 2, "px",
					func(m *config.MainConfig, v float64) { m.Appearance.CornerRadius = v }) {
					seSet(se, func(s *settingsEditorState) { s.cornerRadiusChanged = true })
				}
				if seGet(se, func(s *settingsEditorState) bool { return s.cornerRadiusChanged }) {
					settingsHint(c, theme, i18n.T("settings.appearance.corner_radius_restart"))
				}
			})
			a.immediateNumberRow(c, theme, "appearance", i18n.T("settings.appearance.hud_duration"), "hud_duration_ms", "",
				float64(appearance.HUDDurationMs), 200, 5000, 100, "ms",
				func(m *config.MainConfig, v float64) { m.Appearance.HUDDurationMs = int(v) })
		})

		settingsCardDivider(c, theme)
		ui.Text(c, i18n.T("settings.appearance.spatial_look")).FontSize(typeMeta).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
		a.immediateChoiceRow(c, theme, "appearance", i18n.T("settings.appearance.visual_preset"), "visual_preset", "",
			visualPresetOptions(), appearance.VisualPreset, func(m *config.MainConfig, v string) { m.Appearance.VisualPreset = v })
		a.immediateSliderRow(c, theme, "appearance", i18n.T("settings.appearance.stage_intensity"), "stage_intensity", "",
			appearance.StageIntensity, 0, 1, 0.05,
			func(m *config.MainConfig, v float64) { m.Appearance.StageIntensity = config.NormalizeStageIntensity(v) })
		a.immediateSwitchRow(c, theme, "appearance", i18n.T("settings.appearance.oblique_rows"), "oblique_rows", "",
			appearance.ObliqueRows, func(m *config.MainConfig, v bool) { m.Appearance.ObliqueRows = v })
		a.immediateSliderRow(c, theme, "appearance", i18n.T("settings.appearance.glass_clarity"), "glass_clarity", "",
			appearance.GlassClarity, 0, 1, 0.05, func(m *config.MainConfig, v float64) { m.Appearance.GlassClarity = v })
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
		keyRow := ui.Column(c).Key("settings-field:ai:api_key").FillWidth().Gap(space4)
		keyRow.Children(func() {
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
		})
		a.applyFieldAnchor(c, theme, a.Config(), keyRow, "ai", "api_key")

		a.mu.Lock()
		currentDraft := a.uiState.apiKeyDraft
		a.mu.Unlock()

		se := a.settingsEditor()
		confirmClear := seGet(se, func(s *settingsEditorState) bool { return s.apiKeyConfirmDelete })
		if confirmClear {
			kitDeleteConfirmRow(c, theme, "api-key-delete-confirm",
				i18n.Tf("settings.delete.confirm_api_key", activeProfile.ID),
				i18n.T("settings.delete.confirm_action"),
				i18n.T("settings.delete.cancel"),
				func() { seSet(se, func(s *settingsEditorState) { s.apiKeyConfirmDelete = false }) },
				func() {
					seSet(se, func(s *settingsEditorState) { s.apiKeyConfirmDelete = false })
					_ = a.DeleteAPIKey()
				},
			)
		} else {
			ui.Row(c).FillWidth().Wrap().Gap(space8).AlignItems(ui.Center).Children(func() {
				if ui.PrimaryButton(c, i18n.T("settings.ai.save_key")).Disabled(strings.TrimSpace(currentDraft) == "").Clicked() {
					_ = a.SaveAPIKey(currentDraft)
				}
				if ui.Button(c, i18n.T("settings.ai.clear_key")).Clicked() {
					seSet(se, func(s *settingsEditorState) { s.apiKeyConfirmDelete = true })
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
		}

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

		renderPermStateRow(c, theme, i18n.T("settings.perm.screen_recording"), diag.Permissions.ScreenRecording, diag.Permissions.Details["screenRecording"], func() {
			go func() {
				_, _ = a.PromptPermission("screen_recording")
				svc.Invalidate()
			}()
		}, func() {
			_ = a.OpenPermissionSettings("screen_recording")
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
			if screenState := strings.TrimSpace(diag.Permissions.Details["screenRecording"]); screenState != "" {
				settingsKeyValueRow(c, theme, i18n.T("settings.perm.screen_recording"), localizedPermissionDetail(screenState))
			}
			settingsKeyValueRow(c, theme, i18n.T("settings.diag.recovery"), fmt.Sprintf("%d", diag.RecoveryCount))
			settingsKeyValueRow(c, theme, i18n.T("settings.diag.input_source"), activeIME)
			if diag.LastIMEError != "" {
				settingsKeyValueRow(c, theme, i18n.T("settings.diag.ime_error"), diag.LastIMEError)
			}
			if v := diag.Permissions.Details["mouseHook"]; v != "" {
				settingsKeyValueRow(c, theme, i18n.T("settings.diag.mouse_hook"), yesNo(v))
			}
			if v := diag.Permissions.Details["hookError"]; v != "" {
				settingsKeyValueRow(c, theme, i18n.T("settings.diag.hook_error"), v)
			}
			if crashPath, modAt, ok := internaldiag.HasRecentCrash(a.effectiveLogDir()); ok {
				settingsKeyValueRow(c, theme, i18n.T("settings.diag.recent_crash"), filepath.Base(crashPath)+" · "+modAt.Local().Format("2006-01-02 15:04"))
			}

			ui.Row(c).FillWidth().Wrap().Gap(space8).Children(func() {
				svc := c.Services()
				if ui.Button(c, i18n.T("settings.diag.refresh")).Clicked() {
					go func() {
						_, _ = a.RefreshPermissions(context.Background())
						svc.Invalidate()
					}()
				}
				if ui.Button(c, i18n.T("settings.diag.export_bundle")).Key("diag-export-bundle").Clicked() {
					go func() {
						_, _ = a.ExportDiagnosticBundle()
						svc.Invalidate()
					}()
				}
				if ui.Button(c, i18n.T("settings.diag.open_logs")).Key("diag-open-logs").Clicked() {
					_ = a.OpenLogsDirectory()
				}
				if ui.Button(c, i18n.T("settings.diag.backup_now")).Key("diag-backup-now").Clicked() {
					go func() {
						_, _ = a.BackupConfigAndData()
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
				ui.Text(c, i18n.T("settings.validation.ok")).FontSize(typeLabel).TextColor(statusInk(theme, theme.Success)).Wrap().Grow(1)
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
	renderPermStateRow(c, theme, label, granted, "", onPrompt, onOpenSettings)
}

func renderPermStateRow(
	c *ui.Context,
	theme *ui.Theme,
	label string,
	granted bool,
	state string,
	onPrompt func(),
	onOpenSettings func(),
) {
	statusText := i18n.T("settings.perm.not_granted")
	statusColor := theme.Warning
	state = strings.ToLower(strings.TrimSpace(state))
	unknown := !granted && (state == "unknown" || state == "unsupported")
	notRequired := state == "not_required"
	if granted {
		statusText = i18n.T("settings.perm.granted")
		statusColor = theme.Success
	} else if notRequired {
		statusText = i18n.T("settings.perm.not_required")
		statusColor = theme.TextMuted
	} else if unknown {
		statusText = i18n.T("settings.perm.unknown")
		statusColor = theme.Warning
	}
	ui.Row(c).Key("perm:" + label).FillWidth().Height(controlHeight).Gap(space8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(typeBody).TextColor(theme.Text).SingleLine().Grow(1)
		kitStatusDot(c, statusColor, false)
		ui.Text(c, statusText).FontSize(typeLabel).FontWeight(600).TextColor(statusColor).SingleLine()
		if ui.Button(c, i18n.T("settings.perm.prompt")).Key("prompt:"+label).Clicked() && onPrompt != nil {
			onPrompt()
		}
		if !granted && !notRequired {
			if ui.Button(c, i18n.T("settings.perm.open_settings")).Key("settings:"+label).Clicked() && onOpenSettings != nil {
				onOpenSettings()
			}
		}
	})
}

func localizedPermissionDetail(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "granted", "allowed":
		return i18n.T("settings.perm.granted")
	case "denied", "not_granted":
		return i18n.T("settings.perm.not_granted")
	case "not_required":
		return i18n.T("settings.perm.not_required")
	case "unknown", "unsupported":
		return i18n.T("settings.perm.unknown")
	default:
		return yesNo(state)
	}
}

func yesNo(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
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
	kitCard(c, theme, cardFlat, func() {
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
	kitCard(c, theme, cardFlat, func() {
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
	case config.FileQuickJump:
		return pickLang(i18n.IsChinese(), "统一快跳书签（URL、目录与脚本）", "Unified Quick Jump bookmarks (URLs, paths, and scripts)")
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
	row := ui.Row(c).Key("settings-field:triggers:"+field).FillWidth().Gap(space12).AlignItems(ui.Center).Padding(space2, 0)
	row.Children(func() {
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
				_ = a.updateMainConfigField(c, "triggers", field, title, func(m *config.MainConfig) { apply(m, normalized) })
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
					_ = a.updateMainConfigField(c, "triggers", field, title, func(m *config.MainConfig) { apply(m, combo) })
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
		renderFieldSavedBadge(c, theme, se, settingsFieldID("triggers", field))
	})
	a.applyFieldAnchor(c, theme, a.Config(), row, "triggers", field)
}
