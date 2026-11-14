package uiapp

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

var omniboxTabs = []config.CapsuleTab{
	config.TabActions,
	config.TabClipboard,
	config.TabSnippets,
	config.TabAI,
	config.TabGlossary,
}

// CapsuleView renders the native MyGo Capsule UI for the current CapsulePhase.
func (a *AppController) CapsuleView(c *ui.Context) {
	cfg := a.Config()
	state := a.State()
	diag := a.Diagnostics()
	theme := resolveAppearanceTheme(c, cfg.Main.Appearance.Theme)
	c.SetTheme(theme)
	prefs := c.Preferences()
	radius := NormalizeCornerRadius(cfg.Main.Appearance)
	profile := appMotionProfile(c, cfg.Main.Appearance)
	DisableAnimationsFor(c, profile)
	a.uiState.lastExitHideDelay = profile.ExitHideDelay()

	if state.Phase != engine.PhaseAIStreaming {
		a.uiState.followUpQuery = ""
	}

	defer func() {
		a.uiState.lastPhase = state.Phase
		a.uiState.lastTab = state.OmniboxTab
		a.uiState.lastSelectedIdx = state.SelectedIndex
	}()

	if state.Phase == engine.PhaseHidden {
		capsuleUI.reset(a)
		ui.Box(c).Fill()
		return
	}

	root := c.Root()
	if !prefs.ReduceTransparency {
		root.Radius(radius)
	}

	card := applyTextLocale(ui.Column(c).Fill().Radius(radius).Clip())
	if state.Phase == engine.PhaseHUD {
		card = card.Transition(profile.hudEnterTransition())
	} else {
		card = card.Transition(profile.capsuleEnterTransition())
	}
	if !prefs.ReduceTransparency {
		card.Material(glass.Glass{Native: c.Vibrancy()})
		if prefs.HighContrast {
			card.Border(1.5, theme.Border)
		} else {
			card.Border(1, withAlpha(theme.Border, 0.5))
		}
	} else {
		borderW := float32(1)
		if prefs.HighContrast {
			borderW = 1.5
		}
		card.Background(theme.Background).Border(borderW, theme.Border)
	}

	card.Children(func() {
		switch state.Phase {
		case engine.PhaseHUD:
			a.renderHUDView(c, theme, state)
		case engine.PhaseLeader:
			a.renderCapsuleHeader(c, theme, cfg, state, diag)
			ui.Divider(c)
			a.renderLeaderView(c, theme, state)
		case engine.PhaseOmnibox:
			a.renderCapsuleHeader(c, theme, cfg, state, diag)
			ui.Divider(c)
			a.renderOmniboxView(c, theme, cfg, state)
		case engine.PhaseAIStreaming:
			a.renderCapsuleHeader(c, theme, cfg, state, diag)
			ui.Divider(c)
			a.renderAIStreamingView(c, theme, cfg, state)
		}
	})
}

// IMEPillView renders a compact floating micro-capsule ("中" / "EN" / IME name)
// with glass vibrancy and a pill border for the cursor-side IME indicator window.
func IMEPillView(c *ui.Context, label string) {
	trimmed := strings.TrimSpace(label)
	if trimmed == "" {
		trimmed = "EN"
	}
	theme := c.Theme()
	prefs := c.Preferences()

	if !prefs.ReduceTransparency {
		c.Root().Radius(999)
	}

	pill := applyTextLocale(ui.Row(c).
		Fill().
		Padding(4, 10).
		Radius(999).
		Center().
		Gap(5).
		Clip())
	if !prefs.ReduceTransparency {
		pill.Material(glass.Glass{Native: c.Vibrancy()})
		pill.Border(1, withAlpha(theme.Border, 0.55))
	} else {
		pill.Background(theme.Background).Border(1, theme.Border)
	}

	pill.Children(func() {
		renderSemanticIcon(c, "keyboard", 12, theme.Accent)
		ui.Text(c, trimmed).FontSize(12).FontWeight(700).TextColor(theme.Text).SingleLine()
	})
}

func resolveAppearanceTheme(c *ui.Context, mode string) *ui.Theme {
	prefs := c.Preferences()
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "dark":
		return ui.DarkTheme().WithPreferences(prefs)
	case "light":
		return ui.LightTheme().WithPreferences(prefs)
	default:
		return c.Theme()
	}
}

func (a *AppController) renderCapsuleHeader(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
	state engine.State,
	diag DiagnosticsSnapshot,
) {
	ui.Row(c).FillWidth().Padding(10, 14).Gap(8).AlignItems(ui.Center).Children(func() {
		// Clavis brand badge with command icon
		ui.Row(c).
			Padding(3, 8).
			Radius(6).
			Gap(5).
			AlignItems(ui.Center).
			Background(withAlpha(theme.Accent, 0.16)).
			Children(func() {
				renderSemanticIcon(c, "command", 13, theme.Accent)
				ui.Text(c, "Clavis").FontSize(12).FontWeight(700).TextColor(theme.Accent).SingleLine()
			})

		switch state.Phase {
		case engine.PhaseLeader:
			crumbs := make([]string, 0, 1+len(state.LeaderBreadcrumbs))
			crumbs = append(crumbs, i18n.T("leader.root"))
			crumbs = append(crumbs, state.LeaderBreadcrumbs...)
			ui.Text(c, strings.Join(crumbs, " › ")).
				FontSize(13).
				FontWeight(600).
				TextColor(theme.Text).
				SingleLine()

		case engine.PhaseOmnibox:
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				for _, tab := range omniboxTabs {
					active := state.OmniboxTab == tab
					pill := ui.ButtonBase(c).
						Padding(4, 9).
						Radius(6).
						FocusRing(false)
					if active {
						pill.Background(theme.Accent)
					} else {
						pill.Background(theme.Surface).Border(1, withAlpha(theme.Border, 0.7))
					}
					pill.Children(func() {
						txt := ui.Text(c, omniboxTabLabel(tab)).FontSize(12).SingleLine()
						if active {
							txt.TextColor(theme.AccentText).FontWeight(600)
						} else {
							txt.TextColor(theme.TextMuted)
						}
					})
					if pill.Clicked() {
						a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: tab})
						a.uiState.searchHandle.RequestFocus(c)
					}
				}
			})

		case engine.PhaseAIStreaming:
			modeLabel := state.AIMode
			if modeLabel == "" {
				modeLabel = i18n.T("ai.mode.ask")
			}
			ui.Text(c, i18n.Tf("ai.header", modeLabel)).FontSize(13).FontWeight(600).TextColor(theme.Text).SingleLine()

			// Active AI Profile pill (clickable to cycle profiles)
			activeProf := a.ActiveAIProfile()
			profID := activeProf.ID
			if profID == "" {
				profID = activeProf.Provider
			}
			profLabel := fmt.Sprintf("%s / %s", profID, activeProf.Model)
			profPill := ui.ButtonBase(c).
				Padding(2, 8).
				Radius(999).
				Background(theme.Surface).
				Border(1, withAlpha(theme.Border, 0.7)).
				FocusRing(false)
			profPill.Children(func() {
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					renderSemanticIcon(c, "sparkles", 11, theme.Accent)
					ui.Text(c, profLabel).FontSize(11).FontWeight(600).TextColor(theme.Text).SingleLine()
				})
			})
			if profPill.Clicked() {
				nextID := nextAIProfileID(cfg.Main.AI, activeProf.ID)
				if nextID != "" {
					_ = a.SelectAIProfile(nextID)
				}
			}

			statusText := i18n.T("ai.status.streaming")
			statusBg := withAlpha(theme.Accent, 0.15)
			statusFg := theme.Accent
			if state.AIError != "" {
				statusText = i18n.T("common.error")
				statusBg = withAlpha(theme.Danger, 0.16)
				statusFg = theme.Danger
			} else if state.AIComplete {
				statusText = i18n.T("common.done")
				statusBg = withAlpha(theme.Success, 0.16)
				statusFg = theme.Success
			}
			ui.Box(c).Padding(2, 8).Radius(999).Background(statusBg).Children(func() {
				ui.Text(c, statusText).FontSize(11).FontWeight(600).TextColor(statusFg).SingleLine()
			})
		}

		ui.Spacer(c)

		if state.Context.AppName != "" {
			ui.Box(c).
				Padding(2, 7).
				Radius(5).
				Background(theme.Surface).
				Border(1, withAlpha(theme.Border, 0.6)).
				Children(func() {
					ui.Text(c, state.Context.AppName).FontSize(11).TextColor(theme.TextMuted).SingleLine()
				})
		}

		imeLabel := diag.ActiveInputSource.Name
		if imeLabel == "" {
			imeLabel = diag.ActiveInputSource.ID
		}
		if imeLabel != "" {
			ui.Box(c).
				Padding(2, 7).
				Radius(5).
				Background(theme.Surface).
				Border(1, withAlpha(theme.Border, 0.6)).
				Children(func() {
					ui.Text(c, imeLabel).FontSize(11).TextColor(theme.TextMuted).SingleLine()
				})
		}
	})
}

func nextAIProfileID(aiCfg config.AIConfig, currentID string) string {
	profiles := aiCfg.Profiles
	if len(profiles) == 0 {
		profiles = config.DefaultAIProfiles()
	}
	if len(profiles) == 0 {
		return ""
	}
	idx := 0
	for i, p := range profiles {
		if strings.EqualFold(p.ID, currentID) {
			idx = (i + 1) % len(profiles)
			return profiles[idx].ID
		}
	}
	return profiles[0].ID
}

func (a *AppController) renderLeaderView(c *ui.Context, theme *ui.Theme, state engine.State) {
	container := ui.Column(c).
		Bind(&a.uiState.leaderHandle).
		Focusable().
		AutoFocus().
		FocusRing(false).
		FillWidth().
		Grow(1).
		Padding(12, 14).
		Gap(10)

	if a.uiState.lastPhase != engine.PhaseLeader || !a.uiState.leaderHandle.Focused(c) {
		a.uiState.leaderHandle.RequestFocus(c)
	}

	container.HandleInput(func(ev ui.InputEvent) bool {
		return a.handleLeaderInput(ev)
	})

	container.Children(func() {
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			if len(state.LeaderNodes) == 0 {
				ui.Box(c).FillWidth().Padding(24).Center().Children(func() {
					ui.Text(c, i18n.T("leader.empty")).TextColor(theme.TextMuted)
				})
				return
			}
			ui.Grid(c).Columns(2).Gap(8).FillWidth().Children(func() {
				for idx, node := range state.LeaderNodes {
					n := node
					card := ui.ButtonBase(c).
						Key(fmt.Sprintf("leader:%d:%s", idx, n.Key)).
						FillWidth().
						Padding(10, 12).
						Radius(10).
						Background(theme.Surface).
						Border(1, theme.Border)

					card.Children(func() {
						ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
							ui.Box(c).
								MinWidth(26).
								Padding(3, 7).
								Radius(6).
								Background(withAlpha(theme.Accent, 0.14)).
								Border(1, withAlpha(theme.Accent, 0.35)).
								Center().
								Children(func() {
									ui.Text(c, n.Key).FontSize(12).FontWeight(700).TextColor(theme.Accent).SingleLine()
								})

							renderSemanticIcon(c, n.Icon, 16, theme.Accent)

							ui.Text(c, n.Label).FontSize(13).FontWeight(500).TextColor(theme.Text).SingleLine().Grow(1)

							if n.Action.Type == config.ActionGroup {
								ui.Text(c, "›").FontSize(15).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
							}
						})
					})

					if card.Clicked() {
						a.Dispatch(engine.Event{Type: engine.EventLeaderKeyPress, Key: n.Key})
						a.uiState.leaderHandle.RequestFocus(c)
					}
				}
			})
		})

		ui.Divider(c)
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("leader.footer")).
				FontSize(11).
				TextColor(theme.TextMuted).
				SingleLine()
		})
	})
}

func (a *AppController) handleLeaderInput(ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputKeyDown:
		if ev.Key == ui.KeyEscape && ev.Mods == 0 {
			a.uiState.suppressNextLeaderText = ""
			a.Dispatch(engine.Event{Type: engine.EventEscape})
			return true
		}
		if ev.Key == ui.KeyBackspace && ev.Mods == 0 {
			a.uiState.suppressNextLeaderText = ""
			a.Dispatch(engine.Event{Type: engine.EventBackspace})
			return true
		}
		if keyStr, ok := leaderKeyString(ev.Key, ev.Mods); ok {
			a.uiState.suppressNextLeaderText = keyStr
			a.Dispatch(engine.Event{Type: engine.EventLeaderKeyPress, Key: keyStr})
			return true
		}
	case ui.InputKeyUp:
		a.uiState.suppressNextLeaderText = ""
	case ui.InputText:
		if ev.Text == "" {
			return false
		}
		if a.uiState.suppressNextLeaderText != "" && strings.EqualFold(ev.Text, a.uiState.suppressNextLeaderText) {
			a.uiState.suppressNextLeaderText = ""
			return true
		}
		a.uiState.suppressNextLeaderText = ""
		r, _ := utf8.DecodeRuneInString(ev.Text)
		if r != utf8.RuneError {
			a.Dispatch(engine.Event{Type: engine.EventLeaderKeyPress, Key: string(r)})
			return true
		}
	}
	return false
}

func (a *AppController) renderOmniboxView(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
	state engine.State,
) {
	profile := appMotionProfile(c, cfg.Main.Appearance)
	if a.uiState.lastPhase != engine.PhaseOmnibox || a.uiState.lastTab != state.OmniboxTab {
		a.uiState.searchHandle.RequestFocus(c)
	}

	rootCol := ui.Column(c).FillWidth().Grow(1).Padding(10, 14).Gap(8)
	rootCol.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind != ui.InputKeyDown || a.uiState.isComposing {
			return false
		}
		return a.handleOmniboxKey(ev.Key, ev.Mods, state)
	})

	// While the contextual action menu is open, Escape closes only the menu,
	// wherever the keyboard focus is.
	if a.omniboxActionMenuOpen(state) {
		c.OnWindowShortcut(0, ui.KeyEscape, func() {
			capsuleUI.setActionMenuShown(a, false)
		})
	}

	rootCol.Children(func() {
		query := state.OmniboxQuery
		in := ui.TextInput(c, &query).
			Bind(&a.uiState.searchHandle).
			AutoFocus().
			Search().
			Placeholder(omniboxPlaceholder(state.OmniboxTab)).
			Label(i18n.T("omnibox.search_label")).
			FillWidth()

		if query != "" {
			in.TextRanges(ui.TextRange{
				Start:  0,
				End:    utf8.RuneCountInString(query),
				Color:  theme.Text,
				Weight: 500,
			})
		}

		in.ObserveInput(func(ev ui.InputEvent) {
			switch ev.Kind {
			case ui.InputCompose:
				a.uiState.isComposing = ev.Text != ""
			case ui.InputText, ui.InputCommand:
				a.uiState.isComposing = false
			}
		})

		if !in.Focused() {
			a.uiState.isComposing = false
		} else {
			a.uiState.isComposing = in.Composing()
		}

		// Quick chords (⌥1..⌥9 / Alt+1..9) must be taken before the text
		// editor types the Option+digit character.
		in.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind != ui.InputKeyDown || a.uiState.isComposing {
				return false
			}
			return a.handleOmniboxKey(ev.Key, ev.Mods, state)
		})

		in.OnTextKey(func(k ui.TextKey) bool {
			if a.uiState.isComposing {
				switch k.Key {
				case ui.KeyEnter, ui.KeyUp, ui.KeyDown, ui.KeyTab, ui.KeyEscape:
					return true
				default:
					return false
				}
			}
			return a.handleOmniboxKey(k.Key, k.Mods, state)
		})

		if in.Changed() && query != state.OmniboxQuery {
			a.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: query})
		}

		if a.omniboxActionMenuOpen(state) {
			a.renderOmniboxActionMenu(c, theme, state)
		} else if capsuleUI.actionMenuShown(a) {
			capsuleUI.setActionMenuShown(a, false)
		}

		items := state.OmniboxItems
		if len(items) == 0 {
			// Empty-state illustration: the tab's icon above its caption.
			ui.Box(c).FillWidth().Grow(1).Center().Padding(24).Children(func() {
				ui.Column(c).Gap(10).Center().Children(func() {
					renderSemanticIcon(c, omniboxTabIcon(state.OmniboxTab), 24, withAlpha(theme.TextMuted, 0.55))
					ui.Text(c, omniboxEmptyText(state.OmniboxTab)).TextColor(theme.TextMuted)
				})
			})
		} else {
			selectedIdx := state.SelectedIndex
			if selectedIdx < 0 {
				selectedIdx = 0
			}
			if selectedIdx >= len(items) {
				selectedIdx = len(items) - 1
			}
			if a.uiState.lastSelectedIdx != selectedIdx || a.uiState.lastTab != state.OmniboxTab {
				a.uiState.listState.ScrollIntoView(selectedIdx)
			}
			a.uiState.listState.Selected = &selectedIdx
			a.uiState.listState.Label = func(row int) string {
				if row >= 0 && row < len(items) {
					return items[row].Title
				}
				return ""
			}

			renderList := func() {
				list := ui.List(c, &a.uiState.listState, len(items), func(i int) {
					item := items[i]
					isSelected := i == state.SelectedIndex
					row := ui.Row(c).
						FillWidth().
						Padding(7, 10).
						Radius(8).
						Gap(10).
						AlignItems(ui.Center).
						Transition(ui.ElementTransition{
							Animation: profile.rowEnterAnimation(i),
							Enter:     profile.rowEnterMotion(),
							ExitDelay: -1, // rows leave at once; only entering staggers
						})
					selAnim := profile.selectionAnimation()
					if selAnim.Duration > 0 {
						if isSelected {
							row.Background(row.AnimateColor("row-sel", withAlpha(theme.Accent, 0.16), selAnim)).
								Border(1, withAlpha(theme.Accent, 0.45))
						} else {
							row.Background(row.AnimateColor("row-sel", ui.Transparent, selAnim))
						}
					} else if isSelected {
						row.Background(withAlpha(theme.Accent, 0.16)).Border(1, withAlpha(theme.Accent, 0.45))
					}

					iconColor := theme.TextMuted
					if isSelected || item.Kind == engine.ItemAIMode || item.Kind == engine.ItemUtility {
						iconColor = theme.Accent
					}

					row.Children(func() {
						a.renderOmniboxRowLeadingIcon(c, item, iconColor)

						if item.Kind == engine.ItemClipboardEntry && item.KeyHint == engine.KeyHintPinned {
							ui.Box(c).
								Padding(2, 6).
								Radius(4).
								Background(withAlpha(theme.Accent, 0.16)).
								Children(func() {
									ui.Text(c, i18n.T("omnibox.pin_badge")).FontSize(10).FontWeight(700).TextColor(theme.Accent).SingleLine()
								})
						}

						ui.Column(c).Grow(1).Gap(2).Children(func() {
							spans := buildHighlightedSpans(item.Title, item.MatchedIndices, theme.Accent, theme.Text)
							ui.RichText(c, spans...).FontSize(13).SingleLine()
							if item.Subtitle != "" {
								ui.Text(c, item.Subtitle).FontSize(11).TextColor(theme.TextMuted).SingleLine()
							}
						})

						// Direct quick-select badge (⌥1..⌥9 / Alt+1..9).
						if cfg.Main.Appearance.ShowKeyHints && i < 9 {
							ui.Box(c).
								Padding(2, 6).
								Radius(5).
								Background(theme.Surface).
								Border(1, withAlpha(theme.Border, 0.8)).
								Children(func() {
									ui.Text(c, quickIndexLabel(i)).FontSize(10).TextColor(theme.TextMuted).SingleLine()
								})
						}

						if cfg.Main.Appearance.ShowKeyHints && item.KeyHint != "" && item.KeyHint != engine.KeyHintPinned {
							ui.Box(c).
								Padding(2, 7).
								Radius(5).
								Background(theme.Surface).
								Border(1, theme.Border).
								Children(func() {
									ui.Text(c, item.KeyHint).FontSize(11).TextColor(theme.TextMuted).SingleLine()
								})
						}
					})

					if row.RowDoubleClicked() {
						a.SelectIndex(i)
						a.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
					} else if row.RowClicked() {
						a.SelectIndex(i)
						a.uiState.searchHandle.RequestFocus(c)
					}
				}).FillWidth().Grow(1)

				if list.Changed() && selectedIdx >= 0 && selectedIdx < len(items) && selectedIdx != state.SelectedIndex {
					a.SelectIndex(selectedIdx)
				}
				if list.Submitted() {
					a.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
				}
			}

			// Side-by-side split: results list on the left, rich preview on the right.
			previewItem, hasPreview := omniboxPreviewItem(state)
			if hasPreview && a.previewPaneVisible(cfg) {
				ui.Row(c).FillWidth().Grow(1).Gap(10).AlignItems(ui.Stretch).Children(func() {
					ui.Column(c).Grow(3).Children(renderList)
					ui.Column(c).Grow(2).Children(func() {
						a.renderOmniboxPreviewCard(c, theme, state, previewItem)
					})
				})
			} else {
				renderList()
			}
		}

		ui.Divider(c)
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			prefix := modPrefix()
			hint := i18n.Tf("omnibox.footer.hint", prefix, prefix)
			if selected, ok := selectedOmniboxItem(state); ok && selected.Kind == engine.ItemClipboardEntry {
				hint += i18n.Tf("omnibox.footer.hint_clipboard", prefix, prefix)
			}
			ui.Text(c, hint).FontSize(11).TextColor(theme.TextMuted).SingleLine().Grow(1)

			a.renderOmniboxFooterFrameToggle(c, theme, state, prefix+i18n.T("omnibox.footer.preview"),
				a.previewPaneVisible(cfg), func() {
					capsuleUI.togglePreview(a, cfg.Main.Appearance.ShowPreviewPane)
					a.uiState.searchHandle.RequestFocus(c)
				})
			a.renderOmniboxFooterFrameToggle(c, theme, state, prefix+i18n.T("omnibox.footer.actions"),
				a.omniboxActionMenuOpen(state), func() {
					a.toggleOmniboxActionMenu()
					a.uiState.searchHandle.RequestFocus(c)
				})
		})
	})
}

// renderOmniboxFooterFrameToggle renders a small footer toggle button.
func (a *AppController) renderOmniboxFooterFrameToggle(
	c *ui.Context,
	theme *ui.Theme,
	_ engine.State,
	label string,
	active bool,
	onClick func(),
) {
	btn := ui.ButtonBase(c).
		Padding(2, 8).
		Radius(6).
		FocusRing(false).
		Background(theme.Surface).
		Border(1, withAlpha(theme.Border, 0.8))
	if active {
		btn.Background(withAlpha(theme.Accent, 0.16)).Border(1, withAlpha(theme.Accent, 0.45))
	}
	btn.Children(func() {
		ui.Text(c, label).FontSize(10).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
	})
	if btn.Clicked() && onClick != nil {
		onClick()
	}
}

func (a *AppController) renderOmniboxRowLeadingIcon(c *ui.Context, item engine.OmniboxItem, iconColor ui.Color) {
	switch item.Kind {
	case engine.ItemInstalledApp:
		if bmp := a.AppIconBitmap(item.AppPath); bmp != nil {
			ui.Image(c, bmp).Size(22, 22).Radius(5)
			return
		}
		iconName := item.Icon
		if iconName == "" {
			iconName = "app.badge"
		}
		renderSemanticIcon(c, iconName, 16, iconColor)

	case engine.ItemEmoji:
		glyph := strings.TrimSpace(item.UtilityValue)
		if glyph != "" {
			ui.Box(c).Size(22, 22).Center().Children(func() {
				ui.Text(c, glyph).FontSize(18).SingleLine()
			})
			return
		}
		renderSemanticIcon(c, "face.smiling", 16, iconColor)

	default:
		iconName := item.Icon
		if iconName == "" {
			iconName = "bolt"
		}
		renderSemanticIcon(c, iconName, 16, iconColor)
	}
}

func (a *AppController) handleOmniboxKey(key ui.Key, mods ui.Modifiers, state engine.State) bool {
	// The contextual action menu takes navigation keys while it is open.
	if a.handleActionMenuKey(key, mods, state) {
		return true
	}

	// Direct row quick-select: ⌥1..⌥9 inserts, ⌥⌘1..⌥⌘9 runs the secondary action.
	if mods == ui.Alt {
		if idx, ok := rowQuickKeyIndex(key); ok && idx < len(state.OmniboxItems) {
			a.Dispatch(engine.Event{Type: engine.EventSelectAndConfirmIndex, Index: idx})
			return true
		}
	}
	if mods == (ui.Alt|primaryMod()) || mods == (ui.Alt|ui.Cmd) {
		if idx, ok := rowQuickKeyIndex(key); ok && idx < len(state.OmniboxItems) {
			a.Dispatch(engine.Event{
				Type:      engine.EventSelectAndConfirmIndex,
				Index:     idx,
				Secondary: true,
			})
			return true
		}
	}

	// Clipboard pin/delete works wherever a clipboard entry is selected
	// (the Clipboard tab, or '@' prefix routing in the Actions tab).
	if selected, ok := selectedOmniboxItem(state); ok && selected.Kind == engine.ItemClipboardEntry {
		if isPrimaryMod(mods) && key == ui.KeyP {
			a.Dispatch(engine.Event{Type: engine.EventTogglePinSelectedClipboardItem})
			return true
		}
		if isPrimaryMod(mods) && (key == ui.KeyBackspace || key == ui.KeyDelete) {
			a.Dispatch(engine.Event{Type: engine.EventDeleteSelectedClipboardItem})
			return true
		}
	}

	if isPrimaryMod(mods) {
		switch {
		case key == ui.KeyEnter:
			a.Dispatch(engine.Event{Type: engine.EventConfirmSecondarySelection})
			return true
		case key == ui.KeyK:
			if len(state.OmniboxItems) > 0 {
				a.toggleOmniboxActionMenu()
			}
			return true
		case key == ui.KeyI:
			cfg := a.Config()
			capsuleUI.togglePreview(a, cfg.Main.Appearance.ShowPreviewPane)
			return true
		}
		if idx, ok := numberKeyIndex(key); ok && idx < len(omniboxTabs) {
			capsuleUI.setActionMenuShown(a, false)
			a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: omniboxTabs[idx]})
			return true
		}
	}

	switch {
	case key == ui.KeyDown && mods == 0, key == ui.KeyN && mods == ui.Ctrl:
		a.Dispatch(engine.Event{Type: engine.EventSelectNext})
		return true
	case key == ui.KeyUp && mods == 0, key == ui.KeyP && mods == ui.Ctrl:
		a.Dispatch(engine.Event{Type: engine.EventSelectPrevious})
		return true
	case key == ui.KeyEnter && mods == 0:
		a.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
		return true
	case key == ui.KeyTab && (mods == 0 || mods == ui.Shift):
		nextTab := cycleOmniboxTab(state.OmniboxTab, mods == ui.Shift)
		capsuleUI.setActionMenuShown(a, false)
		a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: nextTab})
		return true
	case key == ui.KeyEscape && mods == 0:
		a.Dispatch(engine.Event{Type: engine.EventEscape})
		return true
	}
	return false
}

func (a *AppController) renderAIStreamingView(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig, state engine.State) {
	profile := appMotionProfile(c, cfg.Main.Appearance)
	c.OnWindowShortcut(0, ui.KeyEscape, func() {
		if a.followUpComposing() {
			return
		}
		a.Dispatch(engine.Event{Type: engine.EventEscape})
	})
	// Cycle AI profiles with ⌘⇧M / Ctrl+Shift+M.
	c.OnWindowShortcut(primaryMod()|ui.Shift, ui.KeyM, func() {
		activeProf := a.ActiveAIProfile()
		if nextID := nextAIProfileID(a.Config().Main.AI, activeProf.ID); nextID != "" {
			_ = a.SelectAIProfile(nextID)
		}
	})
	if state.AIComplete && state.AIError == "" {
		c.OnWindowShortcut(0, ui.KeyTab, func() {
			a.uiState.followUpHandle.RequestFocus(c)
		})
	}
	if state.AIError == "" && state.AIOutput != "" {
		c.OnWindowShortcut(0, ui.KeyEnter, func() {
			if a.followUpComposing() {
				return
			}
			trimmed := strings.TrimSpace(a.uiState.followUpQuery)
			if trimmed != "" {
				a.uiState.followUpQuery = ""
				a.Dispatch(engine.Event{Type: engine.EventFollowUpAI, Prompt: trimmed})
				return
			}
			a.Dispatch(engine.Event{Type: engine.EventAcceptAIOutput})
		})
	}
	if state.AIOutput != "" {
		c.OnWindowShortcut(primaryMod(), ui.KeyC, func() {
			a.Dispatch(engine.Event{Type: engine.EventCopyAIOutput})
		})
	}

	ui.Column(c).FillWidth().Grow(1).Padding(12, 14).Gap(8).Children(func() {
		if state.AISourceText != "" {
			srcRunes := utf8.RuneCountInString(state.AISourceText)
			headerLabel := i18n.Tf("ai.source_text", srcRunes)
			toggle := ui.ButtonBase(c).
				FillWidth().
				Padding(6, 10).
				Radius(8).
				Background(theme.Surface).
				Border(1, theme.Border)
			toggle.Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, headerLabel).FontSize(12).FontWeight(500).TextColor(theme.TextMuted).SingleLine()
					ui.Spacer(c)
					chevron := "▸"
					if a.uiState.sourceExpanded {
						chevron = "▾"
					}
					ui.Text(c, chevron).FontSize(12).TextColor(theme.TextMuted).SingleLine()
				})
			})
			if toggle.Clicked() {
				a.uiState.sourceExpanded = !a.uiState.sourceExpanded
			}
			if a.uiState.sourceExpanded {
				ui.Box(c).
					FillWidth().
					MaxHeight(84).
					Padding(8, 10).
					Radius(8).
					Background(theme.Surface).
					Border(1, withAlpha(theme.Border, 0.6)).
					Children(func() {
						ui.Scroll(c).FillWidth().Children(func() {
							ui.Text(c, state.AISourceText).FontSize(12).TextColor(theme.TextMuted)
						})
					})
			}
		}

		// Render previous multi-turn history pills when present
		if len(state.AIHistory) > 0 {
			ui.Box(c).
				FillWidth().
				MaxHeight(96).
				Padding(6, 8).
				Radius(8).
				Background(withAlpha(theme.Surface, 0.7)).
				Border(1, withAlpha(theme.Border, 0.6)).
				Children(func() {
					ui.Scroll(c).FillWidth().Children(func() {
						ui.Column(c).FillWidth().Gap(4).Children(func() {
							for idx, turn := range state.AIHistory {
								roleLabel := i18n.T("ai.role_you")
								roleColor := theme.Accent
								if strings.EqualFold(turn.Role, "assistant") {
									roleLabel = i18n.T("omnibox.tab.ai")
									roleColor = theme.TextMuted
								}
								ui.Row(c).
									Key(fmt.Sprintf("ai-turn:%d", idx)).
									FillWidth().
									Gap(6).
									AlignItems(ui.Center).
									Children(func() {
										ui.Box(c).
											Padding(1, 6).
											Radius(4).
											Background(withAlpha(roleColor, 0.14)).
											Children(func() {
												ui.Text(c, roleLabel).FontSize(10).FontWeight(700).TextColor(roleColor).SingleLine()
											})
										ui.Text(c, turn.Content).FontSize(11).TextColor(theme.TextMuted).SingleLine().Grow(1)
									})
							}
						})
					})
				})
		}

		applyProseLineHeight(ui.Scroll(c).
			FillWidth().
			Grow(1).
			Padding(10, 12).
			Radius(10).
			Background(theme.Surface).
			Border(1, theme.Border)).
			Children(func() {
				switch {
				case state.AIError != "":
					// Two quick jolts (2 × 60ms) whenever a new error lands.
					if state.AIError != a.uiState.lastAIError {
						a.uiState.lastAIError = state.AIError
						a.uiState.aiShakeRun++
					}
					errBox := ui.Box(c).
						FillWidth().
						Padding(10, 12).
						Radius(8).
						Background(withAlpha(theme.Danger, 0.12)).
						Border(1, withAlpha(theme.Danger, 0.4)).
						Children(func() {
							ui.Text(c, state.AIError).FontSize(13).TextColor(theme.Danger)
						})
					if frames := shakeKeyframes(profile); frames != nil {
						jolt := errBox.Keyframes("ai-error-shake", frames, ui.KeyframeOptions{Run: a.uiState.aiShakeRun})
						errBox.Translate(jolt*3, 0)
					}
				case state.AIOutput == "":
					ui.Text(c, i18n.T("ai.generating")).FontSize(13).TextColor(theme.TextMuted)
				default:
					markdown.View(c, state.AIOutput, &a.uiState.mdOpts)
					if !state.AIComplete {
						// Pulsing caret while the answer streams in (900ms loop).
						caret := ui.Box(c).Width(7).Height(15).Radius(2)
						caret.Background(theme.Accent.Alpha(cursorOpacity(caret, profile)))
					}
				}
			})

		// Follow-up input shown when AI completion finishes cleanly
		if state.AIComplete && state.AIError == "" {
			followUp := a.uiState.followUpQuery
			in := ui.TextInput(c, &followUp).
				Bind(&a.uiState.followUpHandle).
				Placeholder(i18n.T("ai.followup_placeholder")).
				Label(i18n.T("ai.followup_label")).
				FillWidth()

			// Track IME composition so candidate selection never sends or closes.
			in.ObserveInput(func(ev ui.InputEvent) {
				switch ev.Kind {
				case ui.InputCompose:
					capsuleUI.setFollowUpComposing(a, ev.Text != "")
				case ui.InputText, ui.InputCommand:
					capsuleUI.setFollowUpComposing(a, false)
				}
			})
			if !in.Focused() {
				capsuleUI.setFollowUpComposing(a, false)
			} else {
				capsuleUI.setFollowUpComposing(a, in.Composing())
			}

			// Quick chords (⌥1..⌥9 / Alt+1..9) must be taken before the text
			// editor types the Option+digit character.
			in.HandleInput(func(ev ui.InputEvent) bool {
				if ev.Kind != ui.InputKeyDown || a.uiState.isComposing {
					return false
				}
				return a.handleOmniboxKey(ev.Key, ev.Mods, state)
			})

			in.OnTextKey(func(k ui.TextKey) bool {
				if a.followUpComposing() {
					switch k.Key {
					case ui.KeyEnter, ui.KeyEscape, ui.KeyUp, ui.KeyDown, ui.KeyTab:
						// IME candidate selection keys belong to the input method.
						return true
					default:
						return false
					}
				}
				switch {
				case k.Key == ui.KeyTab && k.Mods == 0:
					return true
				case k.Key == ui.KeyEscape && k.Mods == 0:
					a.Dispatch(engine.Event{Type: engine.EventEscape})
					return true
				case k.Key == ui.KeyEnter && k.Mods == 0:
					trimmed := strings.TrimSpace(k.Text())
					if trimmed == "" {
						trimmed = strings.TrimSpace(a.uiState.followUpQuery)
					}
					if trimmed != "" {
						k.SetText("")
						a.uiState.followUpQuery = ""
						a.Dispatch(engine.Event{
							Type:   engine.EventFollowUpAI,
							Prompt: trimmed,
						})
						return true
					}
					a.Dispatch(engine.Event{Type: engine.EventAcceptAIOutput})
					return true
				}
				return false
			})

			if in.Changed() {
				a.uiState.followUpQuery = followUp
			}
		}

		ui.Divider(c)
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			canAccept := state.AIError == "" && state.AIOutput != ""
			canCopy := state.AIOutput != ""

			replaceBtn := ui.PrimaryButton(c, i18n.T("ai.replace")).Disabled(!canAccept)
			if replaceBtn.Clicked() && canAccept {
				a.Dispatch(engine.Event{Type: engine.EventAcceptAIOutput})
			}

			copyLabel := modPrefix() + "C " + i18n.T("common.copy")
			copyBtn := ui.Button(c, copyLabel).Disabled(!canCopy)
			if copyBtn.Clicked() && canCopy {
				a.Dispatch(engine.Event{Type: engine.EventCopyAIOutput})
			}

			ui.Spacer(c)

			cancelLabel := i18n.T("ai.cancel")
			if state.AIComplete {
				cancelLabel = i18n.T("ai.close")
			}
			if ui.Button(c, cancelLabel).Clicked() {
				a.Dispatch(engine.Event{Type: engine.EventEscape})
			}
		})
	})
}

// hudIconForMessage picks the semantic icon for a HUD notice: a checkmark
// for copy/success notices, a keyboard for IME switches, an xmark for
// failures, and the plain accent dot for everything else.
func hudIconForMessage(msg string) string {
	switch {
	case strings.Contains(msg, i18n.T("hud.copied")),
		strings.Contains(msg, i18n.T("hud.normalized_glossary")),
		strings.Contains(msg, i18n.T("hud.normalized_clipboard")),
		strings.Contains(msg, i18n.T("common.done")):
		return "checkmark"
	case strings.Contains(msg, i18n.T("hud.ime_switched")),
		strings.Contains(msg, i18n.T("hud.ime_bound")),
		strings.Contains(msg, i18n.T("hud.ime_unbound")):
		return "keyboard"
	case strings.Contains(msg, i18n.T("common.error")),
		strings.Contains(msg, i18n.T("hud.config_reload_failed")),
		strings.Contains(msg, i18n.T("hud.command_failed")):
		return "xmark"
	default:
		return ""
	}
}

func (a *AppController) renderHUDView(c *ui.Context, theme *ui.Theme, state engine.State) {
	ui.Row(c).Fill().Padding(10, 16).Center().Gap(8).Children(func() {
		if icon := hudIconForMessage(state.HUDMessage); icon != "" {
			color := theme.Accent
			if icon == "xmark" {
				color = theme.Danger
			}
			renderSemanticIcon(c, icon, 14, color)
		} else {
			ui.Box(c).Size(8, 8).Radius(4).Background(theme.Accent)
		}
		ui.Text(c, state.HUDMessage).FontSize(13).FontWeight(600).TextColor(theme.Text).SingleLine()
	})
}

func omniboxTabLabel(tab config.CapsuleTab) string {
	switch tab {
	case config.TabActions:
		return i18n.T("omnibox.tab.actions")
	case config.TabClipboard:
		return i18n.T("omnibox.tab.clipboard")
	case config.TabSnippets:
		return i18n.T("omnibox.tab.snippets")
	case config.TabAI:
		return i18n.T("omnibox.tab.ai")
	case config.TabGlossary:
		return i18n.T("omnibox.tab.glossary")
	case config.TabSettings:
		return i18n.T("common.settings")
	default:
		return string(tab)
	}
}

// omniboxTabIcon maps a tab to its semantic icon for tab bars, empty
// states and switch items.
func omniboxTabIcon(tab config.CapsuleTab) string {
	switch tab {
	case config.TabActions:
		return "bolt"
	case config.TabClipboard:
		return "doc.on.clipboard"
	case config.TabSnippets:
		return "text.badge.plus"
	case config.TabAI:
		return "sparkles"
	case config.TabGlossary:
		return "character.book.closed"
	case config.TabSettings:
		return "gearshape"
	default:
		return "bolt"
	}
}

func omniboxPlaceholder(tab config.CapsuleTab) string {
	switch tab {
	case config.TabClipboard:
		return i18n.T("omnibox.placeholder.clipboard")
	case config.TabSnippets:
		return i18n.T("omnibox.placeholder.snippets")
	case config.TabAI:
		return i18n.T("omnibox.placeholder.ai")
	case config.TabGlossary:
		return i18n.T("omnibox.placeholder.glossary")
	default:
		return i18n.T("omnibox.placeholder.default")
	}
}

func omniboxEmptyText(tab config.CapsuleTab) string {
	switch tab {
	case config.TabClipboard:
		return i18n.T("omnibox.empty.clipboard")
	case config.TabSnippets:
		return i18n.T("omnibox.empty.snippets")
	case config.TabAI:
		return i18n.T("omnibox.empty.ai")
	case config.TabGlossary:
		return i18n.T("omnibox.empty.glossary")
	default:
		return i18n.T("omnibox.empty.default")
	}
}

func cycleOmniboxTab(current config.CapsuleTab, backward bool) config.CapsuleTab {
	idx := 0
	for i, t := range omniboxTabs {
		if t == current {
			idx = i
			break
		}
	}
	if backward {
		idx = (idx - 1 + len(omniboxTabs)) % len(omniboxTabs)
	} else {
		idx = (idx + 1) % len(omniboxTabs)
	}
	return omniboxTabs[idx]
}

func numberKeyIndex(key ui.Key) (int, bool) {
	switch key {
	case ui.Key1:
		return 0, true
	case ui.Key2:
		return 1, true
	case ui.Key3:
		return 2, true
	case ui.Key4:
		return 3, true
	case ui.Key5:
		return 4, true
	default:
		return 0, false
	}
}

func leaderKeyString(key ui.Key, mods ui.Modifiers) (string, bool) {
	if mods&^ui.Shift != 0 {
		return "", false
	}
	if key >= ui.KeyA && key <= ui.KeyZ {
		base := 'a' + rune(key-ui.KeyA)
		if mods&ui.Shift != 0 {
			base = 'A' + rune(key-ui.KeyA)
		}
		return string(base), true
	}
	if key >= ui.Key0 && key <= ui.Key9 && mods == 0 {
		return string(rune('0' + (key - ui.Key0))), true
	}
	// '/' jumps straight to a clean Actions Omnibox search bar.
	if key == ui.KeySlash && mods == 0 {
		return "/", true
	}
	return "", false
}

// primaryMod returns the platform's primary shortcut modifier.
func primaryMod() ui.Modifiers {
	if runtime.GOOS == "darwin" {
		return ui.Cmd
	}
	return ui.Ctrl
}

// isPrimaryMod reports whether mods holds the platform primary modifier.
// ui.Cmd is always accepted so headless tests work on any OS.
func isPrimaryMod(mods ui.Modifiers) bool {
	return mods == primaryMod() || mods == ui.Cmd
}

// modPrefix is the printable label of the primary modifier.
func modPrefix() string {
	if runtime.GOOS == "darwin" {
		return "\u2318"
	}
	return "Ctrl+"
}

// altPrefix is the printable label of the option/alt modifier.
func altPrefix() string {
	if runtime.GOOS == "darwin" {
		return "\u2325"
	}
	return "Alt+"
}

// rowQuickKeyIndex maps digits 1..9 to Omnibox row indices 0..8.
func rowQuickKeyIndex(key ui.Key) (int, bool) {
	if key < ui.Key1 || key > ui.Key9 {
		return 0, false
	}
	return int(key - ui.Key1), true
}

// quickIndexLabel renders the direct quick-select badge for row i.
func quickIndexLabel(i int) string {
	return altPrefix() + strconv.Itoa(i+1)
}

func buildHighlightedSpans(title string, matchedIndices []int, accent, normal ui.Color) []ui.Span {
	if title == "" {
		return nil
	}
	if len(matchedIndices) == 0 {
		return []ui.Span{{Text: title, Color: normal}}
	}
	hit := make(map[int]bool, len(matchedIndices))
	for _, idx := range matchedIndices {
		hit[idx] = true
	}

	runes := []rune(title)
	var spans []ui.Span
	var buf strings.Builder
	curMatched := hit[0]

	flush := func() {
		if buf.Len() == 0 {
			return
		}
		if curMatched {
			spans = append(spans, ui.Span{
				Text:   buf.String(),
				Color:  accent,
				Weight: 700,
			})
		} else {
			spans = append(spans, ui.Span{
				Text:  buf.String(),
				Color: normal,
			})
		}
		buf.Reset()
	}

	for i, r := range runes {
		m := hit[i]
		if i > 0 && m != curMatched {
			flush()
			curMatched = m
		}
		buf.WriteRune(r)
	}
	flush()
	return spans
}

func withAlpha(c ui.Color, alpha float32) ui.Color {
	return c.Alpha(alpha)
}
