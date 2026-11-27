package uiapp

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// aiProfilePanelState stores transient UI state for the AI Profile & cc-switch
// management view (never raw secrets).
type aiProfilePanelState struct {
	mu              sync.Mutex
	previewTargetID string
	presetNote      string
}

func (a *AppController) aiProfilePanel() *aiProfilePanelState {
	return a.aiProfilePanelStateFor()
}

func (a *AppController) aiProfilePreviewTarget() (string, string) {
	st := a.aiProfilePanel()
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.previewTargetID, st.presetNote
}

func (a *AppController) setAIProfilePreviewTarget(id string) {
	st := a.aiProfilePanel()
	st.mu.Lock()
	st.previewTargetID = strings.TrimSpace(id)
	st.mu.Unlock()
}

func (a *AppController) setAIProfilePresetNote(note string) {
	st := a.aiProfilePanel()
	st.mu.Lock()
	st.presetNote = note
	st.mu.Unlock()
}

// aiProfileSecretRef returns the non-secret reference locator for a profile.
// Raw secret values are never read or formatted here.
func aiProfileSecretRef(p config.AIProfile) string {
	acct := strings.TrimSpace(p.KeychainAccount)
	if acct != "" {
		return "keychain://" + acct
	}
	if ai.IsKeylessAllowed(p.Provider, p.BaseURL) {
		return "local://keyless"
	}
	return "keychain://(unset)"
}

// aiProfileFallbackEnv returns the effective environment variable fallback name.
func aiProfileFallbackEnv(p config.AIProfile) string {
	env := strings.TrimSpace(p.APIKeyEnvVar)
	if env != "" {
		return env
	}
	return secrets.DefaultEnvVarForProvider(p.Provider)
}

// aiProfileSecretFallbackRef returns a non-secret display of the env/local fallback.
func aiProfileSecretFallbackRef(p config.AIProfile) string {
	env := aiProfileFallbackEnv(p)
	if env != "" {
		return "env://" + env
	}
	if ai.IsKeylessAllowed(p.Provider, p.BaseURL) {
		return "local://keyless"
	}
	return i18n.T("settings.ai.none_keyless")
}

// aiProfileEffectiveEndpoint resolves the concrete wire URL that the AI client
// will hit for this profile.
func aiProfileEffectiveEndpoint(p config.AIProfile) string {
	switch p.Protocol {
	case config.ProtocolAnthropic:
		if u, err := ai.ResolveAnthropicMessagesURL(p.BaseURL); err == nil {
			return u
		}
	case config.ProtocolGemini:
		if u, err := ai.ResolveGeminiStreamURL(p.BaseURL, p.Model); err == nil {
			return u
		}
	default:
		if u, err := ai.ResolveCompletionsURL(p.BaseURL); err == nil {
			return u
		}
	}
	return strings.TrimSpace(p.BaseURL)
}

// aiKeySourceSummary returns the safe configured/unconfigured label and chip tone
// for the active profile. Never includes secret bytes.
func aiKeySourceSummary(profile config.AIProfile, keySource secrets.APIKeySource) (label string, configured bool, tone chipTone) {
	switch keySource {
	case secrets.KeySourceKeychain:
		return i18n.T("settings.ai.key_in_keychain"), true, chipSuccess
	case secrets.KeySourceFile:
		return pickLang(i18n.IsChinese(), "已存入密钥文件（••••••••）", "Configured in Keys File (••••••••)"), true, chipSuccess
	case secrets.KeySourceEnv:
		envName := aiProfileFallbackEnv(profile)
		if envName == "" {
			envName = "API_KEY"
		}
		return i18n.Tf("settings.ai.key_via_env", envName), true, chipSuccess
	default:
		if ai.IsKeylessAllowed(profile.Provider, profile.BaseURL) {
			return i18n.T("settings.ai.key_local"), true, chipSuccess
		}
		return i18n.T("settings.ai.key_not_configured"), false, chipWarning
	}
}

func aiCredentialOriginLabel(profile config.AIProfile, keySource secrets.APIKeySource) string {
	zh := i18n.IsChinese()
	envName := aiProfileFallbackEnv(profile)
	switch keySource {
	case secrets.KeySourceKeychain:
		return pickLang(zh, "configured · Keychain", "configured · Keychain")
	case secrets.KeySourceFile:
		return pickLang(zh, "configured · 密钥文件", "configured · Keys File")
	case secrets.KeySourceEnv:
		if envName != "" {
			return "configured · env://" + envName
		}
		return "configured · env"
	default:
		if ai.IsKeylessAllowed(profile.Provider, profile.BaseURL) {
			return pickLang(zh, "local keyless（免密钥）", "local keyless")
		}
		if envName != "" {
			return "unconfigured (env://" + envName + ")"
		}
		return "unconfigured"
	}
}

func aiSecretBadgeShort(profile config.AIProfile, isActive bool, activeSource secrets.APIKeySource) (string, chipTone) {
	zh := i18n.IsChinese()
	if isActive {
		switch activeSource {
		case secrets.KeySourceKeychain, secrets.KeySourceFile, secrets.KeySourceEnv:
			return pickLang(zh, "已配置", "configured"), chipSuccess
		default:
			if ai.IsKeylessAllowed(profile.Provider, profile.BaseURL) {
				return pickLang(zh, "免密钥", "keyless"), chipSuccess
			}
			return pickLang(zh, "未配置", "unconfigured"), chipWarning
		}
	}
	if ai.IsKeylessAllowed(profile.Provider, profile.BaseURL) {
		return pickLang(zh, "本地免密钥", "local keyless"), chipSuccess
	}
	if strings.TrimSpace(profile.KeychainAccount) != "" || aiProfileFallbackEnv(profile) != "" {
		return pickLang(zh, "引用就绪", "ref bound"), chipNeutral
	}
	return pickLang(zh, "未绑定引用", "unconfigured"), chipWarning
}

func aiProfileCapabilitiesSummary(caps config.ModelCapabilities) []string {
	zh := i18n.IsChinese()
	var out []string
	if caps.Streaming {
		out = append(out, pickLang(zh, "流式", "Streaming"))
	}
	if caps.ToolCalling {
		out = append(out, pickLang(zh, "工具", "Tools"))
	}
	if caps.StructuredOutput {
		out = append(out, pickLang(zh, "结构化", "Structured"))
	}
	if caps.ReasoningEffort {
		out = append(out, pickLang(zh, "推理", "Reasoning"))
	}
	if caps.Vision {
		out = append(out, pickLang(zh, "视觉", "Vision"))
	}
	if caps.ContextWindow > 0 {
		out = append(out, fmt.Sprintf("ctx %dk", caps.ContextWindow/1000))
	}
	return out
}

// renderSettingsAIProfilesSection renders the AI Profile & cc-switch management
// view while keeping the primary controls (profile switcher, effective runtime,
// Keychain test, Add Profile, Active Profile Model) within the 960x640 default
// viewport and placing the full cc-switch profile comparison matrix directly
// below.
func (a *AppController) renderSettingsAIProfilesSection(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
	keySource secrets.APIKeySource,
	testStatus AITestStatus,
	testMsg string,
) {
	zh := i18n.IsChinese()
	activeProfile := a.ActiveAIProfile()
	profiles := cfg.Main.AI.Profiles
	if len(profiles) == 0 {
		profiles = config.DefaultAIProfiles()
	}
	previewID, presetNote := a.aiProfilePreviewTarget()
	if strings.EqualFold(previewID, activeProfile.ID) {
		previewID = ""
		a.setAIProfilePreviewTarget("")
	}
	var previewTarget config.AIProfile
	hasPreview := false
	if previewID != "" {
		for _, p := range profiles {
			if strings.EqualFold(p.ID, previewID) {
				previewTarget, hasPreview = p, true
				break
			}
		}
		if !hasPreview {
			a.setAIProfilePreviewTarget("")
		}
	}

	nextCycleID := nextAIProfileID(cfg.Main.AI, activeProfile.ID)
	keyStatusLabel, _, keyStatusTone := aiKeySourceSummary(activeProfile, keySource)

	// Card 1: Compact cc-switch Switcher + Current Effective Runtime & Fallback.
	// Profile ID buttons render FIRST so direct clicks ("ollama", "deepseek") hit
	// the switcher reliably.
	settingsSourceCard(c, theme, i18n.T("settings.card.ai_profiles"), "[ai] · cc-switch", func() {
		ui.Row(c).FillWidth().Wrap().Gap(space6).AlignItems(ui.Center).Children(func() {
			for _, prof := range profiles {
				p := prof
				active := strings.EqualFold(p.ID, activeProfile.ID)
				pill := ui.ButtonBase(c).Key("settings-ai-prof:"+p.ID).Height(space24+space4).
					Padding(space2, space12).Radius(radiusChip).FocusRing(false)
				if active {
					pill.Background(theme.Accent)
				} else {
					pill.Background(theme.Surface).Border(space1, hairline(theme))
				}
				pill.Children(func() {
					txt := ui.Text(c, p.ID).FontSize(typeLabel).SingleLine()
					if active {
						txt.TextColor(theme.AccentText).FontWeight(600)
					} else {
						txt.TextColor(theme.Text)
					}
				})
				if pill.Clicked() {
					a.setAIProfilePreviewTarget("")
					_ = a.SelectAIProfile(p.ID)
				}
			}
			ui.Spacer(c)
			if nextCycleID != "" && !strings.EqualFold(nextCycleID, activeProfile.ID) {
				previewBtn := pickLang(zh, "切换影响: "+nextCycleID, "Impact: "+nextCycleID)
				if hasPreview {
					previewBtn = pickLang(zh, "收起影响", "Hide Impact")
				}
				if ui.Button(c, previewBtn).Key("ai-preview-next:"+nextCycleID).Padding(space2, space8).Shrink(0).Clicked() {
					if hasPreview {
						a.setAIProfilePreviewTarget("")
					} else {
						a.setAIProfilePreviewTarget(nextCycleID)
					}
				}
			}
			kitStatusPill(c, theme, keyStatusLabel, keyStatusTone)
		})

		if hasPreview {
			a.renderAISwitchImpactCard(c, theme, cfg, activeProfile, previewTarget, keySource)
		}

		protoStr := aiProtocolShortLabel(activeProfile.Protocol)
		cycleSummary := pickLang(zh, "仅当前", "single")
		if nextCycleID != "" && !strings.EqualFold(nextCycleID, activeProfile.ID) {
			cycleSummary = fmt.Sprintf("%s (%s⇧M)", nextCycleID, modPrefix())
		}

		ui.Grid(c).Columns(2).GapX(space10).GapY(space2).FillWidth().Children(func() {
			settingsKeyValuePair(c, theme, "active_profile", activeProfile.ID+" · "+activeProfile.Provider+" ("+protoStr+")")
			settingsKeyValuePair(c, theme, "model / reasoning", activeProfile.Model+" · "+aiReasoningDisplay(activeProfile.ReasoningEffort))
			settingsKeyValuePair(c, theme, "endpoint", aiProfileEffectiveEndpoint(activeProfile))
			settingsKeyValuePair(c, theme, "secret_ref", aiProfileSecretRef(activeProfile))
			settingsKeyValuePair(c, theme, "env_source", aiCredentialOriginLabel(activeProfile, keySource))
			settingsKeyValuePair(c, theme, "fallback_chain", aiProfileSecretFallbackRef(activeProfile)+" · "+cycleSummary)
		})

		caps := aiProfileCapabilitiesSummary(activeProfile.Capabilities)
		ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, "checkmark", IconSize16, theme.Accent)
			ui.Text(c, pickLang(zh, "能力:", "Capabilities:")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
			if len(caps) == 0 {
				kitStatusPill(c, theme, pickLang(zh, "保守默认", "Conservative defaults"), chipNeutral)
			} else {
				for _, capLabel := range caps {
					kitStatusPill(c, theme, capLabel, chipNeutral)
				}
			}
			ui.Spacer(c)
			continueLabel := pickLang(zh, "允许续写", "Allow AI continuation")
			cont := cfg.Main.AI.ContinueEnabled
			ui.Text(c, continueLabel).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
			if sw := ui.Switch(c, &cont); sw.Changed() {
				_ = a.updateMainConfigField(c, "ai", "continue_enabled", continueLabel, func(m *config.MainConfig) { m.AI.ContinueEnabled = cont })
			}
			renderFieldSavedBadge(c, theme, a.settingsEditor(), settingsFieldID("ai", "continue_enabled"))
		})
	})

	// Two-column operational workspace (fits within the default 960x640 window):
	// Keychain/SecretRef + Add Profile on left, Active Profile editor on right.
	ui.Grid(c).Columns(settingsTwoColumns(c)).Gap(space10).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(space10).Children(func() {
			a.renderAIKeychainCard(c, theme, activeProfile, keySource, testStatus, testMsg)
			a.renderAIProfileAddCard(c, theme, presetNote)
		})
		a.renderAIProfileConfigCard(c, theme, cfg, activeProfile)
	})

	// Card 3: Full cc-switch Profile Matrix & Switch Impact Overview.
	a.renderAIProfileMatrixCard(c, theme, cfg, profiles, activeProfile, previewTarget, hasPreview, keySource)
}

func (a *AppController) renderAIProfileMatrixCard(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
	profiles []config.AIProfile,
	activeProfile config.AIProfile,
	previewTarget config.AIProfile,
	hasPreview bool,
	keySource secrets.APIKeySource,
) {
	zh := i18n.IsChinese()
	settingsSourceCard(c, theme,
		pickLang(zh, "cc-switch 配置总览与切换影响", "cc-switch Profile Matrix & Switch Impact"),
		fmt.Sprintf("%d profiles", len(profiles)),
		func() {
			cols := settingsTwoColumns(c)
			ui.Grid(c).Columns(cols).Gap(space8).FillWidth().Children(func() {
				for _, prof := range profiles {
					p := prof
					isActive := strings.EqualFold(p.ID, activeProfile.ID)
					isPreview := hasPreview && strings.EqualFold(p.ID, previewTarget.ID)
					a.renderAIProfileMatrixTile(c, theme, p, isActive, isPreview, keySource)
				}
			})

			if hasPreview {
				previewNotice := pickLang(zh,
					fmt.Sprintf("已展开切换影响预览：%s → %s（见上方配置切换栏）。", activeProfile.ID, previewTarget.ID),
					fmt.Sprintf("Switch impact preview open above: %s → %s.", activeProfile.ID, previewTarget.ID))
				ui.Text(c, previewNotice).FontSize(typeMicro).TextColor(theme.TextMuted).Wrap()
			} else {
				mirrorMsg := pickLang(zh,
					"点击任意候选卡片的「影响」可对比协议、模型、推理强度、SecretRef 与生效面；密钥按 Keychain → 环境变量 → 本地免密钥解析，永不写入 TOML。",
					"Click Impact on any standby profile to diff protocol, model, reasoning, SecretRef, and affected surfaces; secrets resolve Keychain → Env → Local keyless and never touch TOML.")
				ui.Text(c, mirrorMsg).FontSize(typeMicro).TextColor(theme.TextMuted).Wrap()
			}
		})
}

func (a *AppController) renderAIProfileMatrixTile(
	c *ui.Context,
	theme *ui.Theme,
	p config.AIProfile,
	isActive bool,
	isPreview bool,
	activeKeySource secrets.APIKeySource,
) {
	zh := i18n.IsChinese()
	border := hairline(theme)
	bg := theme.Surface
	if isActive {
		border = selectionEdge(theme)
		bg = selectionFill(theme)
	} else if isPreview {
		border = theme.Accent.Alpha(alphaStrong)
		bg = theme.Accent.Alpha(alphaFaint)
	}

	ui.Box(c).Key("ai-switch-tile:"+p.ID).FillWidth().Padding(space8, space10).Radius(radiusRow).
		Background(bg).Border(space1, border).Children(func() {
		ui.Column(c).FillWidth().Gap(space4).Children(func() {
			ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, p.Name).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
				ui.Text(c, aiProtocolShortLabel(p.Protocol)).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine().Grow(1).MinWidth(0)
				if isActive {
					kitStatusPill(c, theme, pickLang(zh, "生效中", "Active"), chipAccent)
				} else {
					impactLbl := pickLang(zh, "影响", "Impact")
					if isPreview {
						impactLbl = pickLang(zh, "收起", "Hide")
					}
					if ui.Button(c, impactLbl).Key("ai-impact:"+p.ID).Padding(space2, space6).Shrink(0).Clicked() {
						if isPreview {
							a.setAIProfilePreviewTarget("")
						} else {
							a.setAIProfilePreviewTarget(p.ID)
						}
					}
					if ui.Button(c, pickLang(zh, "切换", "Use")).Key("ai-use:"+p.ID).Padding(space2, space6).Shrink(0).Clicked() {
						a.setAIProfilePreviewTarget("")
						_ = a.SelectAIProfile(p.ID)
					}
				}
			})

			modelLine := strings.TrimSpace(p.Provider) + " / " + strings.TrimSpace(p.Model)
			ui.Text(c, modelLine).FontSize(typeMeta).FontWeight(500).TextColor(theme.Text).SingleLine()

			badgeText, badgeTone := aiSecretBadgeShort(p, isActive, activeKeySource)
			ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "reasoning: "+aiReasoningDisplay(p.ReasoningEffort)).
					FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine().Grow(1).MinWidth(0)
				kitStatusPill(c, theme, badgeText, badgeTone)
			})
			ui.Text(c, aiProfileSecretRef(p)+" → "+aiProfileSecretFallbackRef(p)).
				FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		})
	})
}

func (a *AppController) renderAISwitchImpactCard(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
	current config.AIProfile,
	target config.AIProfile,
	currentKeySource secrets.APIKeySource,
) {
	zh := i18n.IsChinese()
	ui.Box(c).Key("ai-switch-impact:"+target.ID).FillWidth().Padding(space8, space10).Radius(radiusRow).
		Background(theme.Accent.Alpha(alphaFaint)).Border(space1, theme.Accent.Alpha(alphaHalf)).Children(func() {
		ui.Column(c).FillWidth().Gap(space4).Children(func() {
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				renderSemanticIcon(c, "arrow.triangle.branch", IconSize16, theme.Accent)
				ui.Text(c, pickLang(zh,
					fmt.Sprintf("切换影响预览：%s → %s", current.ID, target.ID),
					fmt.Sprintf("Switch Impact Preview: %s → %s", current.ID, target.ID))).
					FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine().Grow(1)
				if ui.PrimaryButton(c, pickLang(zh, "确认切换", "Confirm Switch")).
					Key("ai-confirm-switch:"+target.ID).Padding(space2, space10).Shrink(0).Clicked() {
					a.setAIProfilePreviewTarget("")
					_ = a.SelectAIProfile(target.ID)
				}
				if ui.Button(c, pickLang(zh, "取消", "Dismiss")).
					Key("ai-dismiss-switch").Padding(space2, space8).Shrink(0).Clicked() {
					a.setAIProfilePreviewTarget("")
				}
			})

			renderSwitchDeltaRow(c, theme, "Provider / Protocol",
				current.Provider+" ("+aiProtocolShortLabel(current.Protocol)+")",
				target.Provider+" ("+aiProtocolShortLabel(target.Protocol)+")")
			renderSwitchDeltaRow(c, theme, "Model", current.Model, target.Model)
			renderSwitchDeltaRow(c, theme, "Reasoning", aiReasoningDisplay(current.ReasoningEffort), aiReasoningDisplay(target.ReasoningEffort))
			renderSwitchDeltaRow(c, theme, "Endpoint", aiProfileEffectiveEndpoint(current), aiProfileEffectiveEndpoint(target))
			renderSwitchDeltaRow(c, theme, "SecretRef",
				aiProfileSecretRef(current)+" ("+aiProfileSecretFallbackRef(current)+")",
				aiProfileSecretRef(target)+" ("+aiProfileSecretFallbackRef(target)+")")

			if aiProfileSecretRef(current) != aiProfileSecretRef(target) {
				warn := pickLang(zh,
					"密钥引用将切换至目标账户（已有密钥不会被覆盖或复制）；若目标非本地免密钥，请确保对应 Keychain 或环境变量已就绪。",
					"Credential lookup will switch to the target SecretRef (stored keys are never overwritten or copied); verify target Keychain or env var unless local keyless.")
				if ai.IsKeylessAllowed(target.Provider, target.BaseURL) {
					warn = pickLang(zh,
						"目标为本地免密钥端点，切换后无需远程 API Key；原配置密钥保持安全留存。",
						"Target is a local keyless endpoint; no remote API key is required and existing secrets stay untouched.")
				} else if currentKeySource != secrets.KeySourceNone {
					warn = pickLang(zh,
						"当前配置已就绪，切换后将改读 "+aiProfileSecretRef(target)+"（回退 "+aiProfileSecretFallbackRef(target)+"），不会泄露或迁移旧密钥。",
						"Active credentials will switch to "+aiProfileSecretRef(target)+" (fallback "+aiProfileSecretFallbackRef(target)+"); previous keys are never copied or leaked.")
				}
				kitWarningBanner(c, theme, warn)
			}

			continueState := pickLang(zh, "续写关闭", "Continue off")
			if cfg.Main.AI.ContinueEnabled {
				continueState = pickLang(zh, "续写开启", "Continue on")
			}
			ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
				ui.Text(c, pickLang(zh, "即时生效面:", "Affected surfaces:")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
				for _, surface := range []string{
					"config.toml [ai]",
					pickLang(zh, "Omnibox AI 中心", "Omnibox AI Center"),
					pickLang(zh, "划词改写/翻译", "Selection Rewrite"),
					pickLang(zh, "Leader AI 动作", "Leader AI Actions"),
					continueState,
				} {
					kitStatusPill(c, theme, surface, chipNeutral)
				}
			})
		})
	})
}

func renderSwitchDeltaRow(c *ui.Context, theme *ui.Theme, field, before, after string) {
	changed := strings.TrimSpace(before) != strings.TrimSpace(after)
	ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, field).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).Width(settingsFieldLabelWidth + space24).SingleLine()
		if !changed {
			ui.Text(c, after).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine().Grow(1).MinWidth(0)
			return
		}
		ui.Text(c, before+"  →  "+after).FontSize(typeMeta).FontWeight(600).TextColor(theme.Text).SingleLine().Grow(1).MinWidth(0)
	})
}

// renderAIProfileConfigCard renders the full editor for the active AI profile,
// covering provider, protocol, model, reasoning_effort, fallback env var,
// SecretRef keychain_account, capabilities, and system prompt.
func (a *AppController) renderAIProfileConfigCard(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
	active config.AIProfile,
) {
	zh := i18n.IsChinese()
	settingsEditorCard(c, theme, i18n.T("settings.card.profile_config"), func() {
		settingsHint(c, theme, i18n.Tf("settings.ai.editing_profile", active.ID))
		ui.Column(c).FillWidth().Gap(space4).Children(func() {
			settingsInlineRow(c, theme, i18n.T("settings.field.name"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.ai.profile_name"), active.Name, active.Name); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Name = v })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.provider"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.ai.profile_provider"), "deepseek", active.Provider); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Provider = strings.TrimSpace(v) })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.protocol"), settingsFieldLabelWidth, func() {
				opts := make([]string, 0, 3)
				for _, p := range config.AllAIProtocols() {
					opts = append(opts, string(p))
				}
				current := string(active.Protocol)
				if current == "" {
					current = string(config.ProtocolOpenAICompatible)
				}
				if v, changed := settingsSelect(c, opts, current); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Protocol = config.AIProtocol(v) })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.model"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.ai.profile_model"), "model-name", active.Model); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Model = strings.TrimSpace(v) })
				}
			})
			settingsInlineRow(c, theme, pickLang(zh, "推理强度", "Reasoning"), settingsFieldLabelWidth, func() {
				opts := aiReasoningEffortOptions()
				labels := make([]string, len(opts))
				curLabel := opts[0].label
				for i, o := range opts {
					labels[i] = o.label
					if strings.EqualFold(o.value, strings.TrimSpace(active.ReasoningEffort)) {
						curLabel = o.label
					}
				}
				if chosenLabel, changed := settingsSelect(c, labels, curLabel); changed {
					for _, o := range opts {
						if o.label == chosenLabel {
							val := o.value
							a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.ReasoningEffort = val })
							break
						}
					}
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.temperature"), settingsFieldLabelWidth, func() {
				if v, changed := settingsInlineNumber(c, theme, i18n.T("settings.field.temperature"), active.Temperature, 0, 2, 0.1, ""); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Temperature = v })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.timeout_s"), settingsFieldLabelWidth, func() {
				if v, changed := settingsInlineNumber(c, theme, i18n.T("settings.field.timeout_s"), active.TimeoutSeconds, 5, 300, 5, "s"); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.TimeoutSeconds = v })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.base_url"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.ai.profile_base_url"), "https://api.example.com/v1", active.BaseURL); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.BaseURL = strings.TrimSpace(v) })
				}
			})
			settingsInlineRow(c, theme, i18n.T("settings.field.keychain"), settingsFieldLabelWidth, func() {
				if v, changed := settingsTextInput(c, i18n.T("settings.ai.profile_keychain"), "clavis.ai.profile", active.KeychainAccount); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.KeychainAccount = strings.TrimSpace(v) })
				}
			})
			settingsInlineRow(c, theme, pickLang(zh, "回退 Env", "Fallback Env"), settingsFieldLabelWidth, func() {
				ph := secrets.DefaultEnvVarForProvider(active.Provider)
				if ph == "" {
					ph = secrets.FallbackEnvVar
				}
				if v, changed := settingsTextInput(c, pickLang(zh, "回退环境变量", "Profile Fallback Env"), ph, active.APIKeyEnvVar); changed {
					a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.APIKeyEnvVar = strings.TrimSpace(v) })
				}
			})
		})

		ui.Text(c, "SecretRef: "+aiProfileSecretRef(active)+" → "+aiProfileSecretFallbackRef(active)).
			FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()

		settingsCardDivider(c, theme)
		ui.Text(c, pickLang(zh, "模型能力声明（保守关闭，按需开启）", "Model Capabilities (conservative off by default)")).
			FontSize(typeMeta).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
		ui.Grid(c).Columns(2).GapX(space10).GapY(space2).FillWidth().Children(func() {
			if v, changed := settingSwitchRow(c, theme, pickLang(zh, "流式输出", "Streaming"), "streaming", "", active.Capabilities.Streaming); changed {
				a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Capabilities.Streaming = v })
			}
			if v, changed := settingSwitchRow(c, theme, pickLang(zh, "工具调用", "Tool Calling"), "tool_calling", "", active.Capabilities.ToolCalling); changed {
				a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Capabilities.ToolCalling = v })
			}
			if v, changed := settingSwitchRow(c, theme, pickLang(zh, "结构化输出", "Structured"), "structured_output", "", active.Capabilities.StructuredOutput); changed {
				a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Capabilities.StructuredOutput = v })
			}
			if v, changed := settingSwitchRow(c, theme, pickLang(zh, "推理参数", "Reasoning Cap"), "reasoning_effort", "", active.Capabilities.ReasoningEffort); changed {
				a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Capabilities.ReasoningEffort = v })
			}
			if v, changed := settingSwitchRow(c, theme, pickLang(zh, "视觉理解", "Vision"), "vision", "", active.Capabilities.Vision); changed {
				a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.Capabilities.Vision = v })
			}
		})

		settingsCardDivider(c, theme)
		settingsLabelled(c, theme, i18n.T("settings.field.system_prompt"), func() {
			if v, changed := settingsTextArea(c, i18n.T("settings.ai.system_prompt"), i18n.T("settings.ai.system_prompt_ph"), active.SystemPrompt, 2); changed {
				a.updateAIProfile(active.ID, func(p *config.AIProfile) { p.SystemPrompt = v })
			}
		})

		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			se := a.settingsEditor()
			confirmDeleteID := seGet(se, func(s *settingsEditorState) string { return s.aiProfileConfirmDelete })
			if confirmDeleteID == active.ID {
				kitDeleteConfirmRow(c, theme, "ai-profile-delete-confirm",
					settingsConfirmPromptForFile(active.ID, config.FileMain),
					i18n.T("settings.delete.confirm_action"),
					i18n.T("settings.delete.cancel"),
					func() { seSet(se, func(s *settingsEditorState) { s.aiProfileConfirmDelete = "" }) },
					func() {
						seSet(se, func(s *settingsEditorState) { s.aiProfileConfirmDelete = "" })
						a.deleteAIProfile(active.ID)
					},
				)
				return
			}
			if ui.Button(c, pickLang(zh, "复制当前配置", "Duplicate")).Key("ai-duplicate:" + active.ID).Clicked() {
				a.duplicateAIProfile(active)
			}
			if ui.Button(c, i18n.T("settings.ai.delete_profile")).Disabled(len(cfg.Main.AI.Profiles) <= 1).Clicked() {
				seSet(se, func(s *settingsEditorState) { s.aiProfileConfirmDelete = active.ID })
			}
			ui.Spacer(c)
			settingsHint(c, theme, i18n.Tf("settings.ai.profile_count", len(cfg.Main.AI.Profiles)))
		})
	})
}

// renderAIProfileAddCard draws the custom profile form plus quick provider
// templates so users can add OpenAI-compatible, Claude, Gemini, or local
// Ollama profiles with sane SecretRef defaults.
func (a *AppController) renderAIProfileAddCard(c *ui.Context, theme *ui.Theme, presetNote string) {
	zh := i18n.IsChinese()
	se := a.settingsEditor()
	settingsEditorCard(c, theme, i18n.T("settings.card.add_profile"), func() {
		draft := seGet(se, func(s *settingsEditorState) config.AIProfile { return s.aiDraftProfile })
		if draft.ID == "" && draft.Provider == "" && draft.BaseURL == "" && draft.Model == "" {
			draft = config.AIProfile{Protocol: config.ProtocolOpenAICompatible, Temperature: 0.2, TimeoutSeconds: 30}
		}

		ui.Grid(c).Columns(2).GapX(space8).GapY(space2).FillWidth().Children(func() {
			if v, changed := settingsTextInput(c, i18n.T("settings.ai.new_profile_id"), "my-profile", draft.ID); changed {
				seSet(se, func(s *settingsEditorState) { s.aiDraftProfile.ID = v })
			}
			if v, changed := settingsTextInput(c, i18n.T("settings.ai.new_profile_provider"), "openai", draft.Provider); changed {
				seSet(se, func(s *settingsEditorState) { s.aiDraftProfile.Provider = v })
			}
			if v, changed := settingsTextInput(c, i18n.T("settings.ai.new_profile_base_url"), "https://api.example.com/v1", draft.BaseURL); changed {
				seSet(se, func(s *settingsEditorState) { s.aiDraftProfile.BaseURL = v })
			}
			if v, changed := settingsTextInput(c, i18n.T("settings.ai.new_profile_model"), "model-name", draft.Model); changed {
				seSet(se, func(s *settingsEditorState) { s.aiDraftProfile.Model = v })
			}
		})

		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			if ui.PrimaryButton(c, i18n.T("settings.ai.add_profile")).Clicked() {
				a.addAIProfileDraft()
				a.setAIProfilePresetNote("")
			}
			settingsHint(c, theme, i18n.T("settings.ai.new_profile_hint"))
		})

		ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, pickLang(zh, "快速模板:", "Presets:")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
			for _, tpl := range aiProfileStarterPresets() {
				t := tpl
				if ui.Button(c, t.label).Key("ai-preset:"+t.id).Padding(space2, space6).Clicked() {
					seSet(se, func(s *settingsEditorState) {
						s.aiDraftProfile = t.profile
					})
					a.setAIProfilePresetNote(pickLang(zh, "已填充 "+t.label+" 模板，可修改 ID 后添加", "Filled "+t.label+" preset; edit ID if needed and click Add Profile"))
				}
			}
		})

		if presetNote != "" {
			ui.Text(c, presetNote).FontSize(typeMicro).TextColor(theme.TextMuted).Wrap()
		}
	})
}

type aiProfilePreset struct {
	id      string
	label   string
	profile config.AIProfile
}

func aiProfileStarterPresets() []aiProfilePreset {
	return []aiProfilePreset{
		{
			id:    "openai-reasoning",
			label: "OpenAI",
			profile: config.AIProfile{
				ID:              "openai-fast",
				Name:            "OpenAI",
				Provider:        "openai",
				Protocol:        config.ProtocolOpenAICompatible,
				BaseURL:         "https://api.openai.com/v1",
				Model:           "gpt-4.1-mini",
				Temperature:     0.2,
				TimeoutSeconds:  30,
				KeychainAccount: "clavis.ai.openai-fast",
				APIKeyEnvVar:    "OPENAI_API_KEY",
				ReasoningEffort: "low",
				Capabilities:    config.ModelCapabilities{Streaming: true, ToolCalling: true, StructuredOutput: true, ReasoningEffort: true},
			},
		},
		{
			id:    "anthropic-claude",
			label: "Claude",
			profile: config.AIProfile{
				ID:              "claude-work",
				Name:            "Claude Work",
				Provider:        "anthropic",
				Protocol:        config.ProtocolAnthropic,
				BaseURL:         "https://api.anthropic.com/v1",
				Model:           "claude-sonnet-4-0",
				Temperature:     0.2,
				TimeoutSeconds:  45,
				KeychainAccount: "clavis.ai.claude-work",
				APIKeyEnvVar:    "ANTHROPIC_API_KEY",
				Capabilities:    config.ModelCapabilities{Streaming: true, ToolCalling: true, Vision: true},
			},
		},
		{
			id:    "gemini-flash",
			label: "Gemini",
			profile: config.AIProfile{
				ID:              "gemini-pro",
				Name:            "Gemini Pro",
				Provider:        "gemini",
				Protocol:        config.ProtocolGemini,
				BaseURL:         "https://generativelanguage.googleapis.com/v1beta",
				Model:           "gemini-2.5-pro",
				Temperature:     0.2,
				TimeoutSeconds:  45,
				KeychainAccount: "clavis.ai.gemini-pro",
				APIKeyEnvVar:    "GEMINI_API_KEY",
				Capabilities:    config.ModelCapabilities{Streaming: true, ToolCalling: true, StructuredOutput: true, Vision: true},
			},
		},
		{
			id:    "ollama-local",
			label: "Ollama",
			profile: config.AIProfile{
				ID:              "ollama-code",
				Name:            "Ollama Local",
				Provider:        "ollama",
				Protocol:        config.ProtocolOpenAICompatible,
				BaseURL:         "http://127.0.0.1:11434/v1",
				Model:           "qwen2.5-coder:7b",
				Temperature:     0.2,
				TimeoutSeconds:  60,
				KeychainAccount: "clavis.ai.ollama-code",
				Capabilities:    config.ModelCapabilities{Streaming: true},
			},
		},
	}
}

// duplicateAIProfile clones src with a unique ID and isolated SecretRef account.
func (a *AppController) duplicateAIProfile(src config.AIProfile) {
	_ = a.UpdateMainConfig(func(m *config.MainConfig) {
		base := strings.TrimSpace(src.ID)
		if base == "" {
			base = "profile"
		}
		candidate := base + "-copy"
		for n := 2; ; n++ {
			if _, exists := m.AI.ProfileByID(candidate); !exists {
				break
			}
			candidate = fmt.Sprintf("%s-copy-%d", base, n)
		}
		clone := src
		clone.ID = candidate
		clone.Name = strings.TrimSpace(src.Name) + " Copy"
		clone.KeychainAccount = "clavis.ai." + candidate
		m.AI.Profiles = append(append([]config.AIProfile(nil), m.AI.Profiles...), clone)
		m.AI.ActiveProfileID = clone.ID
		syncActiveAIFlatFields(&m.AI, clone)
	})
}
