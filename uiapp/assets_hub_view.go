package uiapp

import (
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

type assetsSubTab uint8

const (
	assetsSubMedia assetsSubTab = iota
	assetsSubPinned
	assetsSubVault
)

var assetsSubStore = struct {
	mu    sync.Mutex
	byCtl map[*AppController]assetsSubTab
}{
	byCtl: make(map[*AppController]assetsSubTab),
}

func (a *AppController) assetsSubTab() assetsSubTab {
	assetsSubStore.mu.Lock()
	defer assetsSubStore.mu.Unlock()
	return assetsSubStore.byCtl[a]
}

func (a *AppController) setAssetsSubTab(tab assetsSubTab) {
	assetsSubStore.mu.Lock()
	assetsSubStore.byCtl[a] = tab
	assetsSubStore.mu.Unlock()
}

func (a *AppController) collectMediaAssetItems(state engine.State) []engine.OmniboxItem {
	items := a.MediaSearchItems()
	if len(items) > 0 {
		return items
	}
	var out []engine.OmniboxItem
	for _, entry := range state.ClipboardEntries {
		if !entry.Media.IsMedia() {
			continue
		}
		mCopy := entry.Media
		kh := ""
		if entry.IsPinned {
			kh = engine.KeyHintPinned
		}
		out = append(out, engine.OmniboxItem{
			ID:                 "clip:" + entry.ID,
			Title:              nonEmptyOr(entry.Content, mCopy.Kind),
			Subtitle:           entry.SourceAppName,
			KeyHint:            kh,
			Kind:               engine.ItemClipboardEntry,
			ClipboardID:        entry.ID,
			ClipboardContent:   entry.Content,
			ClipboardMedia:     &mCopy,
			ClipboardSourceApp: entry.SourceAppName,
			ClipboardCopiedAt:  entry.CopiedAt,
		})
	}
	return out
}

func (a *AppController) renderAssetsHubView(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	zh := i18n.IsChinese()
	curTab := a.assetsSubTab()
	state := a.State()

	ui.Column(c).Key("assets-hub-view").Label("assets-hub-view").FillWidth().Grow(1).MinHeight(0).Gap(space8).Children(func() {
		// Top header: Back button + Title + 3 sub-view switcher pills.
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			backLbl := pickLang(zh, "返回", "Back")
			backBtn := ui.ButtonBase(c).Key("assets-back").Label(backLbl).
				Padding(space2, space8).Radius(radiusChip).Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false)
			backBtn.Children(func() {
				ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
					renderSemanticIcon(c, "chevron.left", typeMicro, secondaryInk(theme))
					ui.Text(c, backLbl).FontSize(typeMeta).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
				})
			})
			if backBtn.Clicked() {
				a.CloseHubSurface()
			}

			renderSemanticIcon(c, "photo", IconSize16, secondaryInk(theme))
			ui.Text(c, pickLang(zh, "资产中心", "Assets Hub")).FontSize(typeBody).FontWeight(700).TextColor(theme.Text).SingleLine()
			ui.Spacer(c)

			tabBtn := func(key, label string, target assetsSubTab) {
				active := curTab == target
				b := ui.ButtonBase(c).Key(key).Label(label).Padding(space2, space8).Radius(radiusChip).FocusRing(false)
				if active {
					b.Background(theme.Accent.Alpha(alphaSubtle)).Border(space1, theme.Accent.Alpha(alphaHalf))
				} else {
					b.Background(tileFill(theme)).Border(space1, hairline(theme))
				}
				fg := secondaryInk(theme)
				if active {
					fg = accentInk(theme, surfaceBase(theme))
				}
				b.Children(func() {
					ui.Text(c, label).FontSize(typeMeta).FontWeight(600).TextColor(fg).SingleLine()
				})
				if b.Clicked() {
					a.setAssetsSubTab(target)
				}
			}
			tabBtn("assets-tab-media", pickLang(zh, "媒体", "Media"), assetsSubMedia)
			tabBtn("assets-tab-pinned", pickLang(zh, "置顶与模板", "Pinned"), assetsSubPinned)
			tabBtn("assets-tab-vault", pickLang(zh, "密钥库", "Vault"), assetsSubVault)
		})

		kitDivider(c, theme)

		switch curTab {
		case assetsSubMedia:
			mediaItems := a.collectMediaAssetItems(state)
			ui.Box(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
				a.renderMediaSearchSurface(c, theme, mediaItems)
			})
		case assetsSubPinned:
			a.renderPinnedAndTemplatesView(c, theme, cfg, state)
		case assetsSubVault:
			a.renderVaultAssetsView(c, theme)
		}
	})
}

func (a *AppController) renderPinnedAndTemplatesView(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig, state engine.State) {
	zh := i18n.IsChinese()
	var pinned []engine.OmniboxClipboardEntry
	for _, entry := range state.ClipboardEntries {
		if entry.IsPinned {
			pinned = append(pinned, entry)
		}
	}
	snippets := cfg.Snippets.Items
	prompts := cfg.Prompts.Items

	ui.Scroll(c).Key("assets-pinned-scroll").FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Gap(space8).Children(func() {
			// Section 1: Pinned Clipboard Items.
			ui.Text(c, pickLang(zh, fmt.Sprintf("置顶剪贴板 (%d)", len(pinned)), fmt.Sprintf("Pinned Clipboard (%d)", len(pinned)))).
				FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
			if len(pinned) == 0 {
				ui.Text(c, pickLang(zh, "暂无置顶剪贴板条目（在剪贴板列表中按 ⌘P 置顶）", "No pinned clipboard entries yet (press ⌘P in Clipboard tab)")).
					FontSize(typeMeta).TextColor(theme.TextMuted).Wrap()
			}
			for _, entry := range pinned {
				e := entry
				kitCard(c, theme, cardFlat, func() {
					ui.Row(c).FillWidth().Padding(space8, space10).Gap(space8).AlignItems(ui.Center).Children(func() {
						renderSemanticIcon(c, "pin.fill", typeMeta, secondaryInk(theme))
						ui.Column(c).Grow(1).MinWidth(0).Gap(space2).Children(func() {
							ui.Text(c, previewSingleLine(nonEmptyOr(e.Content, e.ID), 80)).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
							if e.SourceAppName != "" {
								ui.Text(c, e.SourceAppName).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
							}
						})
						if ui.Button(c, pickLang(zh, "复制", "Copy")).Key("assets-pin-copy-" + e.ID).Padding(space2, space8).Clicked() {
							_ = a.writeClipboardText(e.Content)
							a.ShowToastOrHUD(i18n.T("hud.copied"))
						}
						if ui.Button(c, "MD").Key("assets-pin-md-" + e.ID).Padding(space2, space6).Clicked() {
							_ = a.writeClipboardText("```\n" + strings.TrimRight(e.Content, "\r\n") + "\n```")
							a.ShowToastOrHUD(i18n.T("hud.copied"))
						}
					})
				})
			}

			kitDivider(c, theme)

			// Section 2: Snippets & Prompt Templates.
			ui.Text(c, pickLang(zh,
				fmt.Sprintf("片段与提示词模板 (%d + %d)", len(snippets), len(prompts)),
				fmt.Sprintf("Snippets & Templates (%d + %d)", len(snippets), len(prompts)),
			)).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()

			for _, snip := range snippets {
				s := snip
				kitCard(c, theme, cardFlat, func() {
					ui.Row(c).FillWidth().Padding(space6, space10).Gap(space8).AlignItems(ui.Center).Children(func() {
						previewBadge(c, theme, "", s.Trigger, false)
						ui.Column(c).Grow(1).MinWidth(0).Gap(space2).Children(func() {
							ui.Text(c, nonEmptyOr(s.Description, s.Trigger)).FontSize(typeMeta).FontWeight(600).TextColor(theme.Text).SingleLine()
							ui.Text(c, previewSingleLine(s.Content, 72)).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
						})
						if ui.Button(c, pickLang(zh, "复制", "Copy")).Key("assets-snip-copy-" + s.ID).Padding(space2, space8).Clicked() {
							_ = a.writeClipboardText(s.Content)
							a.ShowToastOrHUD(i18n.T("hud.copied"))
						}
					})
				})
			}

			for _, tpl := range prompts {
				pt := tpl
				kitCard(c, theme, cardFlat, func() {
					ui.Row(c).FillWidth().Padding(space6, space10).Gap(space8).AlignItems(ui.Center).Children(func() {
						previewBadge(c, theme, "sparkles", nonEmptyOr(pt.Trigger, pt.ID), false)
						ui.Column(c).Grow(1).MinWidth(0).Gap(space2).Children(func() {
							ui.Text(c, nonEmptyOr(pt.Title, pt.ID)).FontSize(typeMeta).FontWeight(600).TextColor(theme.Text).SingleLine()
							ui.Text(c, previewSingleLine(pt.Description, 72)).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
						})
						if ui.Button(c, pickLang(zh, "复制模板", "Copy")).Key("assets-tpl-copy-" + pt.ID).Padding(space2, space8).Clicked() {
							_ = a.writeClipboardText(pt.Template)
							a.ShowToastOrHUD(i18n.T("hud.copied"))
						}
					})
				})
			}
		})
	})
}

func previewSingleLine(s string, maxRunes int) string {
	one := strings.Join(strings.Fields(s), " ")
	r := []rune(one)
	if len(r) <= maxRunes {
		return one
	}
	return string(r[:maxRunes]) + "…"
}
