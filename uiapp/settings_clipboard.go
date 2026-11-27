package uiapp

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

type clipboardTTLSyncState struct {
	initialized   bool
	lastConfigTTL int
}

// syncClipboardSensitiveTTLFromConfig reconciles the runtime sensitive copy TTL
// with the persisted config.ClipboardConfig value (e.g. after restart or config
// reload) without clobbering direct runtime overrides when config stays default.
func (a *AppController) syncClipboardSensitiveTTLFromConfig(clipCfg config.ClipboardConfig) {
	if a == nil {
		return
	}
	cfgTTL := config.NormalizeSensitiveTTLSeconds(clipCfg.SensitiveTTLSeconds)
	shouldApply := false
	a.withClipboardTTLSync(func(st *clipboardTTLSyncState) {
		if !st.initialized {
			st.initialized = true
			st.lastConfigTTL = cfgTTL
			if cfgTTL != config.DefaultSensitiveCopyTTLSeconds {
				shouldApply = true
			}
		} else if st.lastConfigTTL != cfgTTL {
			st.lastConfigTTL = cfgTTL
			shouldApply = true
		}
	})
	if shouldApply {
		a.SetClipboardSensitiveTTLSeconds(cfgTTL)
	}
}

// updateClipboardSensitiveTTL updates the runtime sensitive copy TTL and
// persists the clamped value into config.toml so it survives restarts.
func (a *AppController) updateClipboardSensitiveTTL(sec int) int {
	norm := a.SetClipboardSensitiveTTLSeconds(sec)
	a.withClipboardTTLSync(func(st *clipboardTTLSyncState) {
		st.initialized = true
		st.lastConfigTTL = norm
	})
	if a != nil && a.ConfigStore != nil {
		_ = a.updateMainConfigField(nil, "clipboard", "sensitive_copy_ttl_seconds", i18n.T("settings.clipboard.manager.ttl_label"), func(m *config.MainConfig) {
			m.Clipboard.SensitiveTTLSeconds = norm
		})
	}
	return norm
}

// ClipboardKeyManagerView renders a standalone, ui.NewTester-ready view of
// Clipboard Shortcut, Conflict Detection, Copy Capabilities, Sensitive TTL,
// and User-Only Copy Controls.
func (a *AppController) ClipboardKeyManagerView(c *ui.Context) {
	cfg := a.Config()
	theme := resolveAppearanceTheme(c, cfg.Main.Appearance.Theme)
	c.SetTheme(theme)

	applyTextLocale(ui.Column(c).Fill().Background(theme.Background).Padding(space12, space14).Gap(space10)).Children(func() {
		ui.Scroll(c).Fill().Children(func() {
			ui.Column(c).FillWidth().Gap(space10).Children(func() {
				a.renderClipboardKeyManagerCard(c, theme, cfg)
			})
		})
	})
}

// renderClipboardKeyManagerCard renders the Clipboard Shortcut & Copy
// Capability Manager card: global hotkey + live conflict detection, copy/paste
// capability matrix (content / ID / path / metadata / paste / user-only
// sensitive copy), password history isolation, and sensitive TTL auto-clear.
func (a *AppController) renderClipboardKeyManagerCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	a.syncClipboardSensitiveTTLFromConfig(cfg.Main.Clipboard)
	policy := a.ClipboardKeyManagerPolicy()
	conflicts := config.DetectClipboardShortcutConflicts(cfg.Main, policy.Bindings)
	sensitiveState := a.SensitiveClipboardState()
	statusMsg, errMsg := a.clipboardActionStatus()

	title := i18n.T("settings.clipboard.manager.title")
	settingsSourceCard(c, theme, title, "[trigger] · [clipboard]", func() {
		// 1. Global Clipboard Shortcut & Live Conflict Detection.
		normShortcut := config.NormalizeClipboardShortcut(cfg.Main.Trigger.ClipboardShortcut)
		shortcutDisplay := normShortcut
		if shortcutDisplay == "" {
			shortcutDisplay = i18n.T("settings.clipboard.manager.disabled")
		}
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("settings.clipboard.manager.global_shortcut")).
				FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
			keycap(c, theme, shortcutDisplay, keyToneAccent)
			ui.Spacer(c)
			if len(conflicts) == 0 {
				kitStatusPill(c, theme, i18n.T("settings.clipboard.manager.no_conflicts"), chipSuccess)
			} else {
				kitStatusPill(c, theme, i18n.Tf("settings.clipboard.manager.conflicts", len(conflicts)), chipWarning)
			}
		})

		ui.Row(c).FillWidth().Wrap().Gap(space6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("settings.clipboard.manager.quick_presets")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
			for _, preset := range []struct {
				label string
				value string
			}{
				{"alt+shift+c", "alt+shift+c"},
				{"ctrl+alt+v", "ctrl+alt+v"},
				{"cmd+shift+v", "cmd+shift+v"},
				{i18n.T("settings.clipboard.manager.disable"), ""},
			} {
				p := preset
				active := normShortcut == config.NormalizeClipboardShortcut(p.value)
				btn := ui.Button(c, p.label).Key("clip-shortcut-preset:"+p.label).Padding(space2, space8)
				if active {
					btn.Border(space1, selectionEdge(theme))
				}
				if btn.Clicked() {
					_ = a.UpdateMainConfig(func(m *config.MainConfig) {
						m.Trigger.ClipboardShortcut = p.value
					})
				}
			}
		})

		for i, conflict := range conflicts {
			if conflict.Severity == config.SeverityError {
				kitErrorBanner(c, theme, fmt.Sprintf("[%d] %s", i+1, conflict.Message))
			} else {
				kitWarningBanner(c, theme, fmt.Sprintf("[%d] %s", i+1, conflict.Message))
			}
		}

		settingsCardDivider(c, theme)

		// 2. Sensitive TTL Auto-Clear & Password History Guard.
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("settings.clipboard.manager.sensitive_section")).
				FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine().Grow(1)
			kitStatusPill(c, theme, i18n.T("settings.clipboard.manager.user_only_badge"), chipAccent)
		})

		if v, changed := settingNumberRow(
			c, theme,
			i18n.T("settings.clipboard.manager.ttl_label"),
			"sensitive_copy_ttl_seconds",
			i18n.T("settings.clipboard.manager.ttl_help"),
			float64(policy.SensitiveTTLSeconds),
			float64(config.MinSensitiveCopyTTLSeconds),
			float64(config.MaxSensitiveCopyTTLSeconds),
			5,
			"s",
		); changed {
			a.updateClipboardSensitiveTTL(int(v))
		}

		ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("settings.clipboard.manager.ttl_presets")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
			for _, sec := range []int{10, 30, 60, 120} {
				s := sec
				lbl := fmt.Sprintf("%ds", s)
				btn := ui.Button(c, lbl).Key("clip-ttl-preset:"+lbl).Padding(space2, space8)
				if policy.SensitiveTTLSeconds == s {
					btn.Border(space1, selectionEdge(theme))
				}
				if btn.Clicked() {
					a.updateClipboardSensitiveTTL(s)
				}
			}
			ui.Spacer(c)
			if cfg.Main.Clipboard.IgnoreConcealedTypes {
				kitStatusPill(c, theme, i18n.T("settings.clipboard.manager.password_excluded"), chipSuccess)
			} else {
				kitStatusPill(c, theme, i18n.T("settings.clipboard.manager.concealed_recommended"), chipWarning)
			}
		})

		if sensitiveState.Active {
			remSec := int(sensitiveState.Remaining.Round(time.Second) / time.Second)
			if remSec < 0 {
				remSec = 0
			}
			ui.Box(c).Key("clip-sensitive-ttl-banner").FillWidth().Padding(space8, space10).Radius(radiusRow).
				Background(theme.Warning.Alpha(alphaSubtle)).Border(space1, theme.Warning.Alpha(alphaHalf)).Children(func() {
				ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
					renderSemanticIcon(c, "exclamationmark.triangle", IconSize16, theme.Warning)
					ui.Text(c, i18n.Tf("settings.clipboard.manager.sensitive_banner", sensitiveState.Label, remSec)).
						FontSize(typeMeta).FontWeight(500).TextColor(theme.Text).Wrap().Grow(1)
					if ui.Button(c, i18n.T("settings.clipboard.manager.clear_now")).Key("clip-sensitive-clear-now").Padding(space2, space8).Shrink(0).Clicked() {
						_ = a.ClearSensitiveClipboardNow()
					}
				})
			})
		}

		settingsCardDivider(c, theme)

		// 3. Copy & Paste Capabilities Matrix + User-Only Action Bar.
		ui.Text(c, i18n.T("settings.clipboard.manager.capability_matrix")).
			FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()

		targetItem, secretLabel, secretVal := a.resolveClipboardKeyManagerTarget()

		ui.Grid(c).Columns(settingsTwoColumns(c)).Gap(space8).FillWidth().Children(func() {
			for _, binding := range policy.Bindings {
				b := binding
				a.renderClipboardCapabilityTile(c, theme, b, targetItem, secretLabel, secretVal)
			}
		})

		if errMsg != "" {
			kitErrorBanner(c, theme, errMsg)
		} else if statusMsg != "" {
			kitStatusPill(c, theme, statusMsg, chipSuccess)
		}
	})

	a.renderClipboardWorkbenchCard(c, theme, cfg)
}

func (a *AppController) renderClipboardWorkbenchCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	clip := cfg.Main.Clipboard
	settingsSourceCard(c, theme, i18n.T("settings.clipboard.wb.card"), "[clipboard]", func() {
		renderChoiceGroup := func(fieldKey, title, caption, current string, opts []settingOption, onPick func(string)) {
			a.settingRowWithFeedback(c, theme, "clipboard", fieldKey, title, caption, "", func() ui.Element {
				return ui.Row(c).Gap(space4).AlignItems(ui.Center).Shrink(0).Children(func() {
					for _, opt := range opts {
						o := opt
						active := strings.EqualFold(current, o.value)
						btn := ui.Button(c, o.label).Key("clip-wb:"+caption+":"+o.value).Padding(space2, space8)
						if active {
							btn.Border(space1, selectionEdge(theme)).Background(selectionFill(theme))
						}
						if btn.Clicked() {
							onPick(o.value)
						}
					}
				})
			})
		}

		renderChoiceGroup(
			"enter_action",
			i18n.T("settings.clipboard.wb.enter_action"),
			"clipboard.enter_action",
			config.NormalizeClipboardEnterAction(clip.EnterAction),
			[]settingOption{
				{value: "paste", label: i18n.T("settings.clipboard.wb.enter_paste")},
				{value: "copy", label: i18n.T("settings.clipboard.wb.enter_copy")},
			},
			func(v string) {
				_ = a.updateMainConfigField(c, "clipboard", "enter_action", i18n.T("settings.clipboard.wb.enter_action"), func(m *config.MainConfig) { m.Clipboard.EnterAction = v })
			},
		)
		renderChoiceGroup(
			"row_density",
			i18n.T("settings.clipboard.wb.row_density"),
			"clipboard.row_density",
			config.NormalizeClipboardRowDensity(clip.RowDensity),
			[]settingOption{
				{value: "compact", label: i18n.T("settings.clipboard.wb.density_compact")},
				{value: "comfortable", label: i18n.T("settings.clipboard.wb.density_comfortable")},
			},
			func(v string) {
				_ = a.updateMainConfigField(c, "clipboard", "row_density", i18n.T("settings.clipboard.wb.row_density"), func(m *config.MainConfig) { m.Clipboard.RowDensity = v })
			},
		)
		renderChoiceGroup(
			"pin_placement",
			i18n.T("settings.clipboard.wb.pin_placement"),
			"clipboard.pin_placement",
			config.NormalizeClipboardPinPlacement(clip.PinPlacement),
			[]settingOption{
				{value: "top", label: i18n.T("settings.clipboard.wb.pin_top")},
				{value: "bottom", label: i18n.T("settings.clipboard.wb.pin_bottom")},
				{value: "inline", label: i18n.T("settings.clipboard.wb.pin_inline")},
			},
			func(v string) {
				_ = a.updateMainConfigField(c, "clipboard", "pin_placement", i18n.T("settings.clipboard.wb.pin_placement"), func(m *config.MainConfig) { m.Clipboard.PinPlacement = v })
			},
		)
		renderChoiceGroup(
			"selection_style",
			i18n.T("settings.clipboard.wb.selection_style"),
			"clipboard.selection_style",
			config.NormalizeClipboardSelectionStyle(clip.SelectionStyle),
			[]settingOption{
				{value: "accent_bar", label: i18n.T("settings.clipboard.wb.style_accent_bar")},
				{value: "maccy_solid", label: i18n.T("settings.clipboard.wb.style_maccy_solid")},
			},
			func(v string) {
				_ = a.updateMainConfigField(c, "clipboard", "selection_style", i18n.T("settings.clipboard.wb.selection_style"), func(m *config.MainConfig) { m.Clipboard.SelectionStyle = v })
			},
		)

		settingsCardDivider(c, theme)
		ui.Text(c, i18n.T("settings.clipboard.wb.excluded_apps")).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
		if len(clip.ExcludedBundleIDs) == 0 {
			ui.Text(c, i18n.T("settings.clipboard.wb.excluded_empty")).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
			return
		}
		ui.Row(c).FillWidth().Wrap().Gap(space6).AlignItems(ui.Center).Children(func() {
			for _, bid := range clip.ExcludedBundleIDs {
				id := bid
				ui.Row(c).Gap(space4).Padding(space2, space6).Radius(radiusPill).Background(tileFill(theme)).Border(space1, hairline(theme)).AlignItems(ui.Center).Children(func() {
					ui.Text(c, id).Font("monospace").FontSize(typeMicro).TextColor(theme.Text).SingleLine()
					if ui.Button(c, i18n.T("settings.clipboard.wb.excluded_remove")).Key("clip-exclude-del:"+id).Padding(space1, space4).Clicked() {
						_ = a.UpdateMainConfig(func(m *config.MainConfig) {
							filtered := make([]string, 0, len(m.Clipboard.ExcludedBundleIDs))
							for _, cur := range m.Clipboard.ExcludedBundleIDs {
								if cur != id {
									filtered = append(filtered, cur)
								}
							}
							m.Clipboard.ExcludedBundleIDs = filtered
						})
					}
				})
			}
		})
	})
}

func (a *AppController) resolveClipboardKeyManagerTarget() (clipboard.ClipboardItem, string, string) {
	st := a.clipboardKeyManager()
	st.mu.Lock()
	var demo *clipboard.ClipboardItem
	if st.demoItem != nil {
		cp := *st.demoItem
		demo = &cp
	}
	secretLabel := st.demoSecretLabel
	secretVal := st.demoSecretValue
	st.mu.Unlock()
	if demo != nil {
		return *demo, secretLabel, secretVal
	}
	// Fall back to the first real clipboard item in State if present (no per-frame SQL).
	state := a.State()
	for _, it := range state.OmniboxItems {
		if strings.TrimSpace(it.ClipboardID) == "" {
			continue
		}
		item := clipboard.ClipboardItem{
			ID:             it.ClipboardID,
			Content:        it.ClipboardContent,
			SourceAppName:  it.Subtitle,
			SourceBundleID: it.AppBundleID,
		}
		if it.ClipboardMedia != nil {
			item.Media = clipboard.Media{
				Kind:      clipboard.Kind(it.ClipboardMedia.Kind),
				MediaPath: it.ClipboardMedia.Path,
				MediaMime: it.ClipboardMedia.Mime,
				Width:     it.ClipboardMedia.Width,
				Height:    it.ClipboardMedia.Height,
				ByteSize:  it.ClipboardMedia.ByteSize,
			}
		}
		return item, secretLabel, secretVal
	}
	return clipboard.ClipboardItem{}, secretLabel, secretVal
}

func (a *AppController) renderClipboardCapabilityTile(
	c *ui.Context,
	theme *ui.Theme,
	binding config.ClipboardActionBinding,
	target clipboard.ClipboardItem,
	secretLabel string,
	secretVal string,
) {
	name, desc, btnLabel := clipboardCapabilityCopyText(binding.Action)

	ui.Box(c).Key("clip-cap:"+string(binding.Action)).FillWidth().Padding(space8, space10).Radius(radiusRow).
		Background(theme.Surface).Border(space1, hairline(theme)).Children(func() {
		ui.Column(c).FillWidth().Gap(space4).Children(func() {
			ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, name).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine().Grow(1)
				if chord := config.NormalizeClipboardShortcut(binding.Shortcut); chord != "" {
					keycap(c, theme, chord, keyToneNeutral)
				}
			})
			ui.Text(c, desc).FontSize(typeMicro).TextColor(theme.TextMuted).Wrap()
			ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
				if binding.UserOnly {
					kitStatusPill(c, theme, i18n.T("settings.clipboard.manager.badge.user_only"), chipAccent)
				} else {
					kitStatusPill(c, theme, i18n.T("settings.clipboard.manager.badge.capsule"), chipNeutral)
				}
				ui.Spacer(c)
				disabled := false
				switch binding.Action {
				case config.ClipboardActionCopySensitive:
					disabled = strings.TrimSpace(secretVal) == "" && strings.TrimSpace(target.Content) == ""
				case config.ClipboardActionCopyPath:
					disabled = strings.TrimSpace(target.MediaPath) == ""
				case config.ClipboardActionCopyID:
					disabled = strings.TrimSpace(target.ID) == ""
				default:
					disabled = strings.TrimSpace(target.ID) == "" && strings.TrimSpace(target.Content) == "" && !target.IsMedia()
				}
				if ui.Button(c, btnLabel).Key("clip-act:"+string(binding.Action)).Disabled(disabled).Padding(space2, space8).Shrink(0).Clicked() {
					req := ClipboardCopyRequest{
						Origin: ClipboardCopyOriginUser,
						Action: binding.Action,
						Item:   target,
					}
					if binding.Action == config.ClipboardActionCopySensitive {
						req.Sensitive = true
						req.PasswordLike = true
						req.SecretLabel = secretLabel
						if secretVal != "" {
							req.Item = clipboard.ClipboardItem{
								ID:      "sensitive-ephemeral",
								Content: secretVal,
							}
						}
					}
					_, _ = a.ExecuteClipboardAction(req)
				}
			})
		})
	})
}

func clipboardCapabilityCopyText(action config.ClipboardActionKind) (name, desc, btn string) {
	switch action {
	case config.ClipboardActionCopyContent:
		return i18n.T("settings.clipboard.cap.content.name"),
			i18n.T("settings.clipboard.cap.content.desc"),
			i18n.T("settings.clipboard.cap.content.btn")
	case config.ClipboardActionPaste:
		return i18n.T("settings.clipboard.cap.paste.name"),
			i18n.T("settings.clipboard.cap.paste.desc"),
			i18n.T("settings.clipboard.cap.paste.btn")
	case config.ClipboardActionCopyID:
		return i18n.T("settings.clipboard.cap.id.name"),
			i18n.T("settings.clipboard.cap.id.desc"),
			i18n.T("settings.clipboard.cap.id.btn")
	case config.ClipboardActionCopyPath:
		return i18n.T("settings.clipboard.cap.path.name"),
			i18n.T("settings.clipboard.cap.path.desc"),
			i18n.T("settings.clipboard.cap.path.btn")
	case config.ClipboardActionCopyMetadata:
		return i18n.T("settings.clipboard.cap.metadata.name"),
			i18n.T("settings.clipboard.cap.metadata.desc"),
			i18n.T("settings.clipboard.cap.metadata.btn")
	case config.ClipboardActionCopySensitive:
		return i18n.T("settings.clipboard.cap.sensitive.name"),
			i18n.T("settings.clipboard.cap.sensitive.desc"),
			i18n.T("settings.clipboard.cap.sensitive.btn")
	default:
		return string(action), "", string(action)
	}
}
