package uiapp

import (
	"fmt"
	"strings"
	"sync"
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

var clipboardTTLSyncStore = struct {
	mu    sync.Mutex
	byCtl map[*AppController]*clipboardTTLSyncState
}{
	byCtl: make(map[*AppController]*clipboardTTLSyncState),
}

// syncClipboardSensitiveTTLFromConfig reconciles the runtime sensitive copy TTL
// with the persisted config.ClipboardConfig value (e.g. after restart or config
// reload) without clobbering direct runtime overrides when config stays default.
func (a *AppController) syncClipboardSensitiveTTLFromConfig(clipCfg config.ClipboardConfig) {
	if a == nil {
		return
	}
	cfgTTL := config.NormalizeSensitiveTTLSeconds(clipCfg.SensitiveTTLSeconds)
	clipboardTTLSyncStore.mu.Lock()
	st, ok := clipboardTTLSyncStore.byCtl[a]
	if !ok {
		st = &clipboardTTLSyncState{}
		clipboardTTLSyncStore.byCtl[a] = st
	}
	shouldApply := false
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
	clipboardTTLSyncStore.mu.Unlock()
	if shouldApply {
		a.SetClipboardSensitiveTTLSeconds(cfgTTL)
	}
}

// updateClipboardSensitiveTTL updates the runtime sensitive copy TTL and
// persists the clamped value into config.toml so it survives restarts.
func (a *AppController) updateClipboardSensitiveTTL(sec int) int {
	norm := a.SetClipboardSensitiveTTLSeconds(sec)
	clipboardTTLSyncStore.mu.Lock()
	st, ok := clipboardTTLSyncStore.byCtl[a]
	if !ok {
		st = &clipboardTTLSyncState{}
		clipboardTTLSyncStore.byCtl[a] = st
	}
	st.initialized = true
	st.lastConfigTTL = norm
	clipboardTTLSyncStore.mu.Unlock()
	if a != nil && a.ConfigStore != nil {
		_ = a.UpdateMainConfig(func(m *config.MainConfig) {
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
	zh := i18n.IsChinese()
	a.syncClipboardSensitiveTTLFromConfig(cfg.Main.Clipboard)
	policy := a.ClipboardKeyManagerPolicy()
	conflicts := config.DetectClipboardShortcutConflicts(cfg.Main, policy.Bindings)
	sensitiveState := a.SensitiveClipboardState()
	statusMsg, errMsg := a.clipboardActionStatus()

	title := pickLang(zh, "剪贴板快捷键与复制能力管理", "Clipboard Shortcuts & Copy Capabilities")
	settingsSourceCard(c, theme, title, "[trigger] · [clipboard]", func() {
		// 1. Global Clipboard Shortcut & Live Conflict Detection.
		normShortcut := config.NormalizeClipboardShortcut(cfg.Main.Trigger.ClipboardShortcut)
		shortcutDisplay := normShortcut
		if shortcutDisplay == "" {
			shortcutDisplay = pickLang(zh, "已禁用", "Disabled")
		}
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, pickLang(zh, "全局剪贴板快捷键", "Global Clipboard Shortcut")).
				FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
			keycap(c, theme, shortcutDisplay, keyToneAccent)
			ui.Spacer(c)
			if len(conflicts) == 0 {
				kitStatusPill(c, theme, pickLang(zh, "无快捷键冲突", "No conflicts"), chipSuccess)
			} else {
				kitStatusPill(c, theme,
					pickLang(zh, fmt.Sprintf("%d 项冲突", len(conflicts)), fmt.Sprintf("%d conflict(s)", len(conflicts))),
					chipWarning)
			}
		})

		ui.Row(c).FillWidth().Wrap().Gap(space6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, pickLang(zh, "快速切换:", "Quick presets:")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
			for _, preset := range []struct {
				label string
				value string
			}{
				{"alt+shift+c", "alt+shift+c"},
				{"ctrl+alt+v", "ctrl+alt+v"},
				{"cmd+shift+v", "cmd+shift+v"},
				{pickLang(zh, "禁用", "Disable"), ""},
			} {
				p := preset
				active := normShortcut == config.NormalizeClipboardShortcut(p.value)
				btn := ui.Button(c, p.label).Key("clip-shortcut-preset:" + p.label).Padding(space2, space8)
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
			ui.Text(c, pickLang(zh, "敏感内容自动清除与历史隔离", "Sensitive Copy TTL & Password History Guard")).
				FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine().Grow(1)
			kitStatusPill(c, theme, pickLang(zh, "仅用户触发 · 禁止 Agent 工具", "User-Only · No Agent Tools"), chipAccent)
		})

		if v, changed := settingNumberRow(
			c, theme,
			pickLang(zh, "敏感内容自动清除时间 (TTL)", "Sensitive Copy Auto-Clear TTL"),
			"sensitive_copy_ttl_seconds",
			pickLang(zh, "用户手动复制敏感内容后，到期自动清空系统剪贴板。", "Automatically wipes the system clipboard after a user-only sensitive copy."),
			float64(policy.SensitiveTTLSeconds),
			float64(config.MinSensitiveCopyTTLSeconds),
			float64(config.MaxSensitiveCopyTTLSeconds),
			5,
			"s",
		); changed {
			a.updateClipboardSensitiveTTL(int(v))
		}

		ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, pickLang(zh, "常用 TTL:", "TTL presets:")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
			for _, sec := range []int{10, 30, 60, 120} {
				s := sec
				lbl := fmt.Sprintf("%ds", s)
				btn := ui.Button(c, lbl).Key("clip-ttl-preset:" + lbl).Padding(space2, space8)
				if policy.SensitiveTTLSeconds == s {
					btn.Border(space1, selectionEdge(theme))
				}
				if btn.Clicked() {
					a.updateClipboardSensitiveTTL(s)
				}
			}
			ui.Spacer(c)
			if cfg.Main.Clipboard.IgnoreConcealedTypes {
				kitStatusPill(c, theme, pickLang(zh, "密码类条目不进入普通历史", "Password entries excluded from history"), chipSuccess)
			} else {
				kitStatusPill(c, theme, pickLang(zh, "建议开启隐藏类型过滤", "Enable concealed filter recommended"), chipWarning)
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
					ui.Text(c, pickLang(zh,
						fmt.Sprintf("敏感内容（%s）已在剪贴板中，约 %ds 后自动清除，未写入普通历史", sensitiveState.Label, remSec),
						fmt.Sprintf("Sensitive item (%s) on clipboard — auto-clearing in %ds, excluded from history", sensitiveState.Label, remSec))).
						FontSize(typeMeta).FontWeight(500).TextColor(theme.Text).Wrap().Grow(1)
					if ui.Button(c, pickLang(zh, "立即清除", "Clear Now")).Key("clip-sensitive-clear-now").Padding(space2, space8).Shrink(0).Clicked() {
						_ = a.ClearSensitiveClipboardNow()
					}
				})
			})
		}

		settingsCardDivider(c, theme)

		// 3. Copy & Paste Capabilities Matrix + User-Only Action Bar.
		ui.Text(c, pickLang(zh, "复制与粘贴能力清单", "Copy & Paste Capability Matrix")).
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
	zh := i18n.IsChinese()
	name, desc, btnLabel := clipboardCapabilityCopyText(binding.Action, zh)

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
					kitStatusPill(c, theme, pickLang(zh, "仅用户", "User-Only"), chipAccent)
				} else {
					kitStatusPill(c, theme, pickLang(zh, "Capsule 动作", "Capsule"), chipNeutral)
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

func clipboardCapabilityCopyText(action config.ClipboardActionKind, zh bool) (name, desc, btn string) {
	switch action {
	case config.ClipboardActionCopyContent:
		return pickLang(zh, "正文与媒体内容", "Entry Content"),
			pickLang(zh, "复制正文或媒体内容到系统剪贴板", "Copy text or media payload to clipboard"),
			pickLang(zh, "复制内容", "Copy Content")
	case config.ClipboardActionPaste:
		return pickLang(zh, "直接粘贴", "Direct Paste"),
			pickLang(zh, "写入剪贴板并向当前应用发送粘贴", "Write to clipboard and paste into frontmost app"),
			pickLang(zh, "粘贴", "Paste")
	case config.ClipboardActionCopyID:
		return pickLang(zh, "条目唯一标识", "Entry Identifier"),
			pickLang(zh, "复制剪贴板条目唯一标识，便于引用与排查", "Copy the unique entry ID for references"),
			pickLang(zh, "复制 ID", "Copy ID")
	case config.ClipboardActionCopyPath:
		return pickLang(zh, "本地文件路径", "Local File Path"),
			pickLang(zh, "复制文件/图片/视频条目关联的本地路径", "Copy local file/image/video paths"),
			pickLang(zh, "复制路径", "Copy Path")
	case config.ClipboardActionCopyMetadata:
		return pickLang(zh, "脱敏元信息", "Redacted Metadata"),
			pickLang(zh, "复制来源应用、时间、尺寸等脱敏元信息", "Copy redacted source app, time, and size metadata"),
			pickLang(zh, "复制元数据", "Copy Metadata")
	case config.ClipboardActionCopySensitive:
		return pickLang(zh, "敏感内容（自动清除）", "Sensitive Payload (TTL)"),
			pickLang(zh, "仅允许用户触发；不进普通历史，到期自动清空", "User-only copy; excluded from history, auto-cleared by TTL"),
			pickLang(zh, "安全复制 (TTL)", "Copy Sensitive (TTL)")
	default:
		return string(action), "", string(action)
	}
}
