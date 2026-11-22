package uiapp

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/yeisme/clavis/pkg/core/agentsession"
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
	config.TabAgent,
}

// CapsuleView renders the native MyGo Capsule UI for the current CapsulePhase.
func (a *AppController) CapsuleView(c *ui.Context) {
	cfg := a.Config()
	appearance := cfg.Main.Appearance
	SetCandidateAppearance(appearance)
	state := a.State()
	diag := a.Diagnostics()
	theme := resolveAppearanceTheme(c, appearance.Theme)
	c.SetTheme(theme)
	prefs := c.Preferences()
	radius := NormalizeCornerRadius(appearance)
	profile := appMotionProfile(c, appearance)
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
	card = applyCapsuleCardSurface(c, card, theme, appearance)

	renderInteractivePhase := func() {
		a.renderCapsuleHeader(c, theme, cfg, state, diag)
		ui.Divider(c)
		switch state.Phase {
		case engine.PhaseLeader:
			a.renderLeaderView(c, theme, state)
		case engine.PhaseOmnibox:
			a.renderOmniboxView(c, theme, cfg, state)
		case engine.PhaseAIStreaming:
			a.renderAIStreamingView(c, theme, cfg, state)
		}
	}

	card.Children(func() {
		if state.Phase == engine.PhaseHUD {
			// HUD keeps the same compact context header as the interactive
			// phases, so transient notices do not change the window's identity.
			a.renderCapsuleHeader(c, theme, cfg, state, diag)
			ui.Divider(c)
			a.renderHUDView(c, theme, state)
			return
		}
		if isClassicPreset(appearance) {
			renderInteractivePhase()
			return
		}
		stageIdx, _ := capsuleStageIndex(state.Phase)
		stageCfg := effectiveSpatialAppearance(c, appearance)
		StageContainer3D(c, "capsule", stageIdx, stageCfg, func() {
			col := ui.Column(c).Fill()
			if Spatial3DEnabled(c, stageCfg) && stageIdx != 0 && appearance.CapsuleWidth > 720 {
				col.Padding(space0, space6)
			}
			col.Children(renderInteractivePhase)
		})
	})
}

// IMEPillView renders the floating, non-activating pill beside the caret: a
// two-segment 中|EN mode switch for input-mode changes, a layer badge for
// keymap hold layers, or an icon + sentence for other notices (see
// renderIMEPill in pill_view.go).
func IMEPillView(c *ui.Context, label string) {
	renderIMEPill(c, label)
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

func capsuleSegmentedTabs(c *ui.Context, theme *ui.Theme, labels []string, active int, onPick func(int)) {
	ui.Row(c).Gap(space2).AlignItems(ui.Center).Children(func() {
		for i, label := range labels {
			selected := i == active
			segment := ui.ButtonBase(c).Padding(space4, space8).Radius(radiusSegment).FocusRing(false)
			if selected {
				segment.Background(theme.Accent)
			} else {
				segment.Background(tileFill(theme))
			}
			segment.Children(func() {
				text := ui.Text(c, label).FontSize(typeLabel).SingleLine()
				if selected {
					text.TextColor(theme.AccentText).FontWeight(600)
				} else {
					text.TextColor(theme.TextMuted)
				}
			})
			if segment.Clicked() && onPick != nil {
				onPick(i)
			}
		}
	})
}

func (a *AppController) renderCapsuleHeader(
	c *ui.Context,
	theme *ui.Theme,
	cfg config.ClavisConfig,
	state engine.State,
	diag DiagnosticsSnapshot,
) {
	header := kitHeaderBar(c)
	if state.Phase == engine.PhaseAIStreaming && !isClassicPreset(cfg.Main.Appearance) {
		header.Draw(func(p *ui.Painter, r ui.Rect) {
			drawCapsuleAIHeaderGlow(c, p, r, theme, cfg.Main.Appearance, state)
		})
	}
	header.Children(func() {
		switch state.Phase {
		case engine.PhaseLeader:
			// Keep the brand in Leader only: Omnibox's query and tabs need the
			// horizontal room, while Leader benefits from the orientation cue.
			// Neutral, like every dialog title bar: the accent is reserved for
			// the chosen row, focus and feedback.
			ui.Row(c).Padding(space2, space8).Radius(radiusChip).Gap(space4).
				AlignItems(ui.Center).Background(tileFill(theme)).Children(func() {
				renderSemanticIcon(c, "command", typeBody, secondaryInk(theme))
				ui.Text(c, "Clavis").FontSize(typeLabel).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
			})
			a.renderLeaderTitle(c, theme, state)

		case engine.PhaseOmnibox:
			labels := make([]string, len(omniboxTabs))
			active := 0
			for i, tab := range omniboxTabs {
				labels[i] = omniboxTabLabel(tab)
				if tab == state.OmniboxTab {
					active = i
				}
			}
			// The capsule owns the tab hit targets so a dispatch rebuild cannot
			// retain a glass-group pane from the previous immediate-mode pass.
			capsuleSegmentedTabs(c, theme, labels, active, func(i int) {
				a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: omniboxTabs[i]})
				a.uiState.searchHandle.RequestFocus(c)
			})

		case engine.PhaseAIStreaming:
			activeOrb := !state.AIComplete && state.AIError == ""
			RenderLiquidOrb(c, 24, activeOrb, effectiveSpatialAppearance(c, cfg.Main.Appearance))
			modeLabel := aiModeLabel(state.AIMode)
			ui.Text(c, i18n.Tf("ai.header", modeLabel)).FontSize(typeBody).FontWeight(600).
				TextColor(theme.Text).SingleLine()
			activeProf := a.ActiveAIProfile()
			profID := activeProf.ID
			if profID == "" {
				profID = activeProf.Provider
			}
			profLabel := fmt.Sprintf("%s / %s", profID, activeProf.Model)
			profile := ui.ButtonBase(c).Padding(space2, space8).Radius(radiusPill).
				Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false)
			profile.Children(func() {
				ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
					renderSemanticIcon(c, "sparkles", typeMeta, theme.Accent)
					ui.Text(c, profLabel).FontSize(typeMeta).FontWeight(600).TextColor(theme.Text).SingleLine()
				})
			})
			if profile.Clicked() {
				if nextID := nextAIProfileID(cfg.Main.AI, activeProf.ID); nextID != "" {
					_ = a.SelectAIProfile(nextID)
				}
			}
		}

		ui.Spacer(c)
		items := make([]string, 0, 2)
		if state.Context.AppName != "" {
			items = append(items, state.Context.AppName)
		}
		imeLabel := diag.ActiveInputSource.Name
		if imeLabel == "" {
			imeLabel = diag.ActiveInputSource.ID
		}
		if imeLabel != "" {
			items = append(items, imeLabel)
		}
		if len(items) > 0 {
			kitChipGroup(c, theme, items)
		}
		if state.Phase == engine.PhaseAIStreaming {
			statusText := i18n.T("ai.status.streaming")
			tone := chipAccent
			if state.AIError != "" {
				statusText, tone = i18n.T("common.error"), chipDanger
			} else if state.AIComplete {
				statusText, tone = i18n.T("common.done"), chipSuccess
			}
			kitStatusPill(c, theme, statusText, tone)
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

// renderLeaderTitle names where the Leader is. At the top level that is
// "Leader"; inside a group it is a back button naming the level above —
// the way a macOS navigation bar does — and the group's own name. The button
// does what ⌫ does.
func (a *AppController) renderLeaderTitle(c *ui.Context, theme *ui.Theme, state engine.State) {
	crumbs := state.LeaderBreadcrumbs
	if len(crumbs) == 0 || len(state.LeaderPath) == 0 {
		ui.Text(c, i18n.T("leader.root")).FontSize(typeBody).FontWeight(600).TextColor(theme.Text).SingleLine()
		return
	}
	parent := i18n.T("leader.root")
	if len(crumbs) > 1 {
		parent = i18n.DisplayLabel(crumbs[len(crumbs)-2])
	}
	// The pair is labelled with the whole path ("Leader › Window"), so a
	// screen reader still hears where it is, not just the last level.
	path := append([]string{i18n.T("leader.root")}, crumbs...)
	ui.Row(c).Label(strings.Join(path, " › ")).Gap(space4).AlignItems(ui.Center).Shrink(1).Children(func() {
		back := ui.ButtonBase(c).Key("leader-back").Padding(space2, space6).Radius(radiusChip).FocusRing(false).
			Label(i18n.T("leader.hint.back") + " " + parent)
		if back.Hovered() {
			back.Background(tileFill(theme))
		}
		back.Children(func() {
			ui.Row(c).Gap(space2).AlignItems(ui.Center).Children(func() {
				renderSemanticIcon(c, "chevron.left", typeMeta, secondaryInk(theme))
				ui.Text(c, parent).FontSize(typeLabel).TextColor(secondaryInk(theme)).SingleLine()
			})
		})
		if back.Clicked() {
			a.Dispatch(engine.Event{Type: engine.EventBackspace})
			a.uiState.leaderHandle.RequestFocus(c)
		}
		ui.Text(c, i18n.DisplayLabel(crumbs[len(crumbs)-1])).FontSize(typeBody).FontWeight(600).TextColor(theme.Text).SingleLine().Shrink(1)
	})
}

func (a *AppController) renderLeaderView(c *ui.Context, theme *ui.Theme, state engine.State) {
	container := ui.Column(c).
		Key("leader-view").
		Label("leader-view").
		Bind(&a.uiState.leaderHandle).
		Focusable().
		AutoFocus().
		FocusRing(false).
		FillWidth().
		Grow(1).
		Padding(space8, space14).
		Gap(space0)

	if a.uiState.lastPhase != engine.PhaseLeader || !a.uiState.leaderHandle.Focused(c) {
		a.uiState.leaderHandle.RequestFocus(c)
	}

	container.HandleInput(func(ev ui.InputEvent) bool {
		return a.handleLeaderInput(ev)
	})

	container.Children(func() {
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			if len(state.LeaderNodes) == 0 {
				ui.Box(c).FillWidth().Padding(space24).Center().Children(func() {
					ui.Text(c, i18n.T("leader.empty")).TextColor(theme.TextMuted)
				})
				return
			}
			// One action per row, not a 2x2 card grid. Raycast, Linear, Notion and VS Code all
			// present command entries as a scannable vertical list, and VS Code's own UX guide
			// requires clear names with the shortcut shown as supporting information. A grid also
			// forced four visual weights (keycap, icon, label, group count) into one row of a
			// 180pt card. See docs/design/leader-panel-visual-research.md §最小改动清单 (1).
			ui.Column(c).FillWidth().Gap(float32(leaderGridGap)).Children(func() {
				for _, node := range state.LeaderNodes {
					n := node
					isGroup := n.Action.Type == config.ActionGroup
					clicked := false
					button := ui.ButtonBase(c).
						Key("leader-row:"+n.Key).
						Label("leader-row:"+n.Key).
						FillWidth().
						Height(float32(leaderCardHeight)).
						Padding(space0, space12).
						Radius(leaderRowRadius).
						FocusRing(false)
					if button.Hovered() {
						button.Background(leaderSelectionFill(theme))
					}
					button.Draw(func(p *ui.Painter, r ui.Rect) {
						if button.Hovered() {
							p.Fill(ui.Rect{X: r.X, Y: r.Y, W: rowSelectionBarWidth, H: r.H}, leaderSelectionBar(), 0)
						}
					})
					button.Children(func() {
						ui.Row(c).FillWidth().Gap(space10).AlignItems(ui.Center).Children(func() {
							leaderKeycap(c, theme, n.Key)
							renderSemanticIcon(c, n.Icon, IconSize16, theme.TextMuted)
							ui.Text(c, i18n.DisplayLabel(n.Label)).FontSize(typeBody).FontWeight(500).TextColor(theme.Text).SingleLine().Grow(1).Shrink(1)
							if isGroup {
								ui.Text(c, strconv.Itoa(len(n.Action.Children))).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
								renderSemanticIcon(c, "chevron.right", 12, theme.TextMuted)
							}
						})
					})
					clicked = button.Clicked()

					if clicked {
						a.Dispatch(engine.Event{Type: engine.EventLeaderKeyPress, Key: n.Key})
						a.uiState.leaderHandle.RequestFocus(c)
					}
				}
			})
		})

		ui.Divider(c)
		ui.Row(c).FillWidth().Height(float32(leaderFooterHeight)).Gap(space12).AlignItems(ui.Center).Children(func() {
			keyHint(c, theme, "a–z", i18n.T("leader.hint.trigger"), keyToneNeutral)
			keyHint(c, theme, "/", i18n.T("leader.hint.search"), keyToneNeutral)
			// ⌫ goes up a level only inside a group; at the top it closes the
			// panel like esc, so "Back" there promised something it did not do.
			if len(state.LeaderPath) > 0 {
				keyHint(c, theme, "⌫", i18n.T("leader.hint.back"), keyToneNeutral)
			}
			keyHint(c, theme, "esc", i18n.T("leader.hint.close"), keyToneNeutral)
			ui.Spacer(c)
			// While the Leader holds the temporary ASCII lock, say so: it is
			// why the keys type letters even though a Chinese IME was active.
			// A status, not an action: neutral, like the IME pill's layer state.
			if a.asciiLockedForCapsule() {
				chip(c, theme, "lock.fill", i18n.T("leader.ascii_locked"), chipNeutral)
			}
		})
	})
}

// leaderKeycap is the Leader menu's mnemonic key: a larger accent keycap, the
// one thing to press on each card.
// leaderKeycap renders the letter that triggers a leader node. Neutral on purpose: a 24px
// square carrying an accent fill, an accent border, an accent shadow and accent text stacked
// four layers of the same hue, which read as "washed out blue" and out-shouted the label it was
// annotating. The accent belongs to the selected row, not to every keycap in the grid. This now
// matches kitKeycap's quiet tone, which is the established neutral keycap in the design kit.
func leaderKeycap(c *ui.Context, theme *ui.Theme, key string) {
	kitKeycap(c, theme, key, keyToneQuiet)
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
	if state.OmniboxTab == config.TabAI {
		a.ensureAIUsageRefresh()
	}
	if state.OmniboxTab == config.TabGlossary {
		a.ensureLexiconHitsRefresh()
	}
	if a.uiState.lastPhase != engine.PhaseOmnibox || a.uiState.lastTab != state.OmniboxTab {
		a.uiState.searchHandle.RequestFocus(c)
	}

	rootCol := ui.Column(c).FillWidth().Grow(1).Padding(space10, space14).Gap(8)
	rootCol.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind != ui.InputKeyDown {
			return false
		}
		// IME owns unmodified Enter/Tab/arrows/Escape during preedit, and the
		// arbitrated Shift+Tab and Cmd+←/→; other Cmd/Ctrl chords are
		// application shortcuts and must still work.
		if a.uiState.isComposing && imeOwnsKey(ev.Key, ev.Mods) {
			return false
		}
		return a.handleOmniboxKey(ev.Key, ev.Mods, state)
	})
	if state.OmniboxTab == config.TabAgent {
		c.OnWindowShortcut(primaryMod(), ui.KeyR, func() { a.AgentRefresh() })
	}

	// Keep the important Omnibox chords alive after a footer/list click moves
	// focus away from the search field. Unmodified navigation remains in the
	// focused editor so the IME can claim it during preedit.
	mods := primaryMod()
	for _, shortcut := range []struct {
		key ui.Key
	}{
		{ui.KeyEnter},
		{ui.KeyK},
		{ui.KeyI},
		{ui.KeyP},
	} {
		shortcut := shortcut
		c.OnWindowShortcut(mods, shortcut.key, func() {
			// Modified chords remain application-owned during IME preedit.
			a.handleOmniboxKey(shortcut.key, mods, state)
		})
	}
	// Cmd+←/→ switch tabs from anywhere in the panel (spec §3.1). The
	// focused search field takes them first, as the arrows edit; here they
	// reach a list or footer that has the focus. An IME preedit keeps them.
	for _, key := range []ui.Key{ui.KeyLeft, ui.KeyRight} {
		key := key
		c.OnWindowShortcut(mods, key, func() {
			if a.uiState.isComposing {
				return
			}
			a.handleOmniboxKey(key, mods, state)
		})
	}
	// Do not claim Escape while an IME preedit is active: the focused text
	// editor must be able to cancel the candidate composition first.
	if a.omniboxActionMenuOpen(state) && !a.uiState.isComposing {
		c.OnWindowShortcut(0, ui.KeyEscape, func() {
			capsuleUI.setActionMenuShown(a, false)
		})
	}

	rootCol.Children(func() {
		query := state.OmniboxQuery
		if state.OmniboxTab == config.TabAI {
			a.renderAIUsageBand(c, theme)
		}
		if state.OmniboxTab == config.TabGlossary {
			a.ensureLexiconHitsRefresh()
			a.ensureLexiconTopWordsRefresh()
			days, err, loaded, _ := a.lexiconHitsSnapshot()
			renderLexiconStatsBand(c, theme, days, loaded, err, len(cfg.Glossary.Terms))
		}
		if state.OmniboxTab == config.TabClipboard {
			a.ensureClipboardStatsRefresh()
			stats, _ := a.clipboardStatsSnapshot()
			renderClipboardStatsBand(c, theme, stats, func(kind string) {
				a.clipboardStatsMu.Lock()
				if a.clipboardStats.filter == kind {
					a.clipboardStats.filter = ""
				} else {
					a.clipboardStats.filter = kind
				}
				a.clipboardStatsMu.Unlock()
				a.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: state.OmniboxQuery})
			})
		}
		searchRow := ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center)
		searchRow.Children(func() {
			in := ui.TextInput(c, &query).
				Bind(&a.uiState.searchHandle).
				AutoFocus().
				Search().
				Placeholder(omniboxPlaceholder(state.OmniboxTab)).
				Label(i18n.T("omnibox.search_label")).
				FontSize(typeSearch).
				Grow(1)

			if a.uiState.search.caretEnd {
				// After the completion Tab accepted, typing goes on at its end.
				a.uiState.search.caretEnd = false
				n := utf8.RuneCountInString(query)
				in.SetTextSelection(n, n)
			}

			if query != "" {
				in.TextRanges(ui.TextRange{
					Start:  0,
					End:    utf8.RuneCountInString(query),
					Color:  theme.Text,
					Weight: 500,
				})
			}
			if full, ok := omniboxCompletion(state); ok && query == state.OmniboxQuery {
				drawCompletionGhost(in, theme, query, full[len(query):], typeSearch)
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
				if ev.Kind != ui.InputKeyDown {
					return false
				}
				if a.uiState.isComposing && imeOwnsKey(ev.Key, ev.Mods) {
					return false
				}
				// Tab goes on to OnTextKey: accepting a completion replaces the
				// text the editor holds, which only a TextKey can do.
				if ev.Key == ui.KeyTab && ev.Mods == 0 {
					return false
				}
				return a.handleOmniboxKey(ev.Key, ev.Mods, state)
			})

			in.OnTextKey(func(k ui.TextKey) bool {
				if a.uiState.isComposing && imeOwnsKey(k.Key, k.Mods) {
					// The preedit's keys are its own. Taking Enter, the vertical
					// arrows, Tab and Escape keeps them from submitting, moving
					// the focus or closing; the rest go on to the editor.
					switch k.Key {
					case ui.KeyEnter, ui.KeyUp, ui.KeyDown, ui.KeyTab, ui.KeyEscape:
						return true
					}
					return false
				}
				if k.Key == ui.KeyTab && k.Mods == 0 && !a.omniboxActionMenuOpen(state) {
					// The completion follows what the editor holds, which may be
					// ahead of the query the last frame showed.
					if title, ok := omniboxCompletionTitle(state); ok {
						if full, ok := inlineCompletion(k.Text(), title); ok {
							k.SetText(full)
							a.uiState.search.caretEnd = true
							a.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: full})
							return true
						}
					}
				}
				return a.handleOmniboxKey(k.Key, k.Mods, state)
			})

			if in.Changed() && query != state.OmniboxQuery {
				a.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: query})
			}

			// A quick-route prefix (> ; @ ? : = /) switches what the box
			// searches; the chip names the mode so the prefix isn't magic.
			if icon, label, ok := omniboxRouteMode(state.OmniboxQuery); ok {
				chip(c, theme, icon, label, chipAccent)
			}
		})

		if a.omniboxActionMenuOpen(state) {
			a.renderOmniboxActionMenu(c, theme, state)
		} else if capsuleUI.actionMenuShown(a) {
			capsuleUI.setActionMenuShown(a, false)
		}

		items := state.OmniboxItems
		if state.OmniboxTab == config.TabAgent {
			sessions, diagnostics := a.cachedAgentData()
			items = agentItems(sessions, diagnostics, state.OmniboxQuery)
		}
		if state.OmniboxTab == config.TabClipboard {
			if kind := a.clipboardStatsFilter(); kind != "" {
				filtered := make([]engine.OmniboxItem, 0, len(items))
				for _, item := range items {
					if clipboardItemMatchesKind(item, kind) {
						filtered = append(filtered, item)
					}
				}
				items = filtered
			}
		}
		if len(items) == 0 {
			if state.OmniboxTab == config.TabAgent && a.agentRefreshing() {
				kitStateView(c, theme, kitStateLoading, "arrow.clockwise", "正在加载 Agent 会话…", "", nil)
			} else {
				kitStateView(c, theme, kitStateEmpty, omniboxTabIcon(state.OmniboxTab), omniboxEmptyText(state.OmniboxTab), "", nil)
			}
		} else {
			selectedIdx := state.SelectedIndex
			if state.OmniboxTab == config.TabAgent {
				selectedIdx = a.uiState.agentSelected
			}
			if selectedIdx < 0 {
				selectedIdx = 0
			}
			if selectedIdx >= len(items) {
				selectedIdx = len(items) - 1
			}
			// Only scroll when the selection left the viewport. Pointer hover
			// and arrow keys inside the visible rows must not move the list:
			// scrolling under a resting pointer makes rows slide beneath it and
			// re-trigger hover, which reads as jitter.
			if a.uiState.lastSelectedIdx != selectedIdx || a.uiState.lastTab != state.OmniboxTab {
				first, last := a.uiState.listState.Visible()
				if first > last || selectedIdx < first || selectedIdx > last {
					a.uiState.listState.ScrollIntoView(selectedIdx)
				}
			}
			a.uiState.listState.Selected = &selectedIdx
			a.uiState.listState.Label = func(row int) string {
				if row >= 0 && row < len(items) {
					return items[row].Title
				}
				return ""
			}

			renderList := func() {
				// ⌥1..⌥9 quick-select badges appear only while Option (Alt)
				// is held, so the resting list stays quiet.
				altHeld := cfg.Main.Appearance.ShowKeyHints && c.Modifiers()&ui.Alt != 0
				prefs := c.Preferences()
				plan := resolveStagePlan(stageInputs{
					Phase:              state.Phase,
					Preset:             cfg.Main.Appearance.VisualPreset,
					Animations:         cfg.Main.Appearance.Animations,
					Intensity:          cfg.Main.Appearance.StageIntensity,
					ObliqueRows:        cfg.Main.Appearance.ObliqueRows,
					Composing:          a.uiState.isComposing,
					ReducedMotion:      prefs.ReduceMotion,
					ReduceTransparency: prefs.ReduceTransparency,
					HighContrast:       prefs.HighContrast,
					// Row fan geometry is renderer-independent. Shader capability is
					// queried inside Draw callbacks, never used to shape rows.
				})
				// The list paints its chosen row in the theme accent. A
				// transparent accent in a scoped theme hands that job to the
				// row, which animates a soft tint instead: a solid accent fill
				// swallowed the accent-colored icon and match highlights.
				ui.Themed(c, rowSelectionTheme(theme)).Background(ui.Transparent).FillWidth().Grow(1).Children(func() {
					list := stageRowList(c, &a.uiState.listState, plan, len(items), selectedIdx, func(i int) ui.Element {
						item := items[i]
						isSelected := i == selectedIdx
						row := ui.Row(c).
							FillWidth().
							Padding(space6, space8).
							Radius(radiusRow).
							Gap(space10).
							AlignItems(ui.Center).
							Transition(ui.ElementTransition{
								Animation: profile.rowEnterAnimation(i),
								Enter:     profile.rowEnterMotion(),
								ExitDelay: -1, // rows leave at once; only entering staggers
							})
						// The chosen row is marked by a 2px accent bar at its leading
						// edge over a faint tint — never a fully blue row, outline or
						// tile. The 1px transparent border keeps the row's geometry.
						fill, bar := ui.Transparent, ui.Transparent
						if isSelected {
							fill, bar = rowSelectionFill(theme), rowSelectionBar(theme)
						}
						if selAnim := profile.selectionAnimation(); selAnim.Duration > 0 {
							fill = row.AnimateColor("row-sel", fill, selAnim)
							bar = row.AnimateColor("row-bar", bar, selAnim)
						}
						row.Background(fill).Border(space1, ui.Transparent)
						if bar.A > 0 {
							row.Draw(func(p *ui.Painter, r ui.Rect) {
								p.Fill(rowSelectionBarRect(r), bar, rowSelectionBarWidth/2)
							})
						}

						row.Children(func() {
							if state.OmniboxTab == config.TabAgent {
								status := agentsession.StatusUnknown
								sessions, _ := a.cachedAgentData()
								if true {
									if session, ok := agentSessionForItem(item, sessions); ok {
										status = session.Status
									}
								}
								color := secondaryInk(theme)
								if status == agentsession.StatusRunning {
									color = theme.Accent
								}
								ui.Box(c).Size(space8, space8).Radius(radiusChip).Background(color).Shrink(0)
								renderAgentRowBadge(c, theme, item, sessions)
							} else {
								a.renderOmniboxRowLeadingIcon(c, theme, item)
							}

							ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(1).Children(func() {
								ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
									spans := buildHighlightedSpans(item.Title, item.MatchedIndices, matchMark(theme), theme.Text)
									// One weight whether chosen or not: a bolder chosen title
									// re-measures and shifts its ellipsis as hover moves.
									ui.RichText(c, spans...).FontSize(typeBody).FontWeight(500).SingleLine().Shrink(1)
									if item.Kind == engine.ItemClipboardEntry && item.KeyHint == engine.KeyHintPinned {
										renderSemanticIcon(c, "pin.fill", typeMeta, secondaryInk(theme))
									}
								})
								subtitle := item.Subtitle
								if item.Kind == engine.ItemClipboardEntry {
									subtitle = clipboardRowMeta(item, clipboardClock())
								}
								if subtitle != "" {
									ui.Text(c, subtitle).FontSize(typeMeta).TextColor(secondaryInk(theme)).SingleLine()
								}
							})

							if cfg.Main.Appearance.ShowKeyHints && item.KeyHint != "" && item.KeyHint != engine.KeyHintPinned {
								keyHint(c, theme, item.KeyHint, "", keyToneNeutral)
							}
							if state.OmniboxTab == config.TabGlossary && item.Kind == engine.ItemGlossaryTerm {
								words, _, _ := a.lexiconTopWordsSnapshot()
								uses, maxUse := lexiconTopWordMap(words)
								if use := uses[item.Title]; maxUse > 0 && use > 0 {
									width := lexiconMicrobarWidth(use, maxUse)
									color := lexiconMicrobarColor(use, maxUse)
									ui.Box(c).Width(width).Height(4).Radius(radius2).Background(color).Shrink(0)
								}
							}
							if altHeld && i < 9 {
								keyHint(c, theme, quickIndexLabel(i), "", keyToneAccent)
							}
						})

						// Pointer hover selects the row so the preview follows the
						// mouse immediately; the row is confirmed only by a click,
						// Enter or an explicit action.
						if row.Hovered() {
							a.hoverSelect(i)
						}

						if row.RowDoubleClicked() {
							a.SelectIndex(i)
							if state.OmniboxTab == config.TabAgent {
								_ = a.agentItemAction(item)
							} else {
								a.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
							}
						} else if row.RowClicked() {
							a.SelectIndex(i)
							if state.OmniboxTab == config.TabAgent {
								a.uiState.agentSelected = i
								_ = a.agentItemAction(item)
								return row
							}
							// Clipboard rows use Maccy semantics: a click pastes; pinning is
							// available only through the explicit action menu.
							if item.Kind == engine.ItemClipboardEntry {
								a.Dispatch(engine.Event{Type: engine.EventActivateClipboardItem})
							} else {
								a.uiState.searchHandle.RequestFocus(c)
							}
						}
						return row
					}).FillWidth().Grow(1)

					if list.Changed() && selectedIdx >= 0 && selectedIdx < len(items) && selectedIdx != state.SelectedIndex {
						if state.OmniboxTab == config.TabAgent {
							a.uiState.agentSelected = selectedIdx
						}
						a.SelectIndex(selectedIdx)
					}
					if list.Submitted() {
						if state.OmniboxTab == config.TabAgent && selectedIdx >= 0 && selectedIdx < len(items) {
							_ = a.agentItemAction(items[selectedIdx])
						} else {
							a.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
						}
					}
				})
			}

			// Side-by-side split: results list on the left, rich preview on the right.
			previewItem, hasPreview := omniboxPreviewItem(state)
			if !hasPreview || previewItem.ClipboardMedia == nil || !previewItem.ClipboardMedia.IsMedia() {
				// Media zoom/pan is scoped to the selected media candidate; never
				// leak it through a text candidate or a later selection.
				a.uiState.mediaPreview = mediaPreviewState{}
			}
			if hasPreview && a.previewPaneVisible(cfg) {
				// The list and the preview share the body at a divider the
				// user drags (or moves with the arrows once focused); a double
				// click restores the default. Each pane keeps a little air
				// from the 1px line between them.
				pct := a.previewListPercent()
				split := ui.SplitPercent(c, &pct, func() {
					ui.Column(c).Fill().Padding(space0, space6, space0, space0).Children(renderList)
				}, func() {
					// The preview shares the content surface. Its former elevated
					// card consumed horizontal width and created a second frame.
					ui.Column(c).Fill().Padding(space0, space8, space4, space0).Children(func() {
						a.renderOmniboxPreviewCard(c, theme, state, previewItem)
					})
				}).FillWidth().Grow(1).SplitReset(defaultPreviewListPercent)
				a.renderPreviewDividerState(c, theme, &pct)
				if split.Changed() {
					a.setPreviewListPercent(pct)
				}
			} else {
				renderList()
			}
		}

		ui.Divider(c)
		ui.Row(c).FillWidth().Gap(space12).AlignItems(ui.Center).Children(func() {
			prefix := modPrefix()
			a.renderOmniboxFooterHints(c, theme, cfg, state)
			ui.Spacer(c)

			a.renderOmniboxFooterFrameToggle(c, theme, "eye", prefix+i18n.T("omnibox.footer.preview"),
				a.previewPaneVisible(cfg), func() {
					capsuleUI.togglePreview(a, cfg.Main.Appearance.ShowPreviewPane)
					a.uiState.searchHandle.RequestFocus(c)
				})
			// The label already carries the shortcut (⌘K); the icon names
			// the function — a "more actions" ellipsis, not the ⌘ key again.
			a.renderOmniboxFooterFrameToggle(c, theme, "ellipsis", prefix+i18n.T("omnibox.footer.actions"),
				a.omniboxActionMenuOpen(state), func() {
					a.toggleOmniboxActionMenu()
					a.uiState.searchHandle.RequestFocus(c)
				})
		})
	})
}

// renderPreviewDividerState overlays the SplitPercent divider with the
// specification's 8pt hit target and delayed hover treatment. The underlying
// MyGo split remains the source of drag/reset behavior and pointer capture.
func (a *AppController) renderPreviewDividerState(c *ui.Context, theme *ui.Theme, pct *float32) {
	width, _ := c.Size()
	dividerX := width * *pct / 100
	if dividerX < 0 || dividerX > width {
		return
	}
	hit := ui.Box(c).Absolute().Left(dividerX - space4).Top(0).Width(space8).FillHeight()
	hovered := hit.Hovered()
	_, _, dragging := hit.Dragged()
	dragging = dragging || a.uiState.previewDividerDragging
	now := c.Now()
	if hovered {
		if a.uiState.previewDividerHoverSince.IsZero() {
			a.uiState.previewDividerHoverSince = now
		}
		if now.Sub(a.uiState.previewDividerHoverSince) >= 150*time.Millisecond {
			a.uiState.previewDividerHot = true
		}
	} else if !dragging {
		a.uiState.previewDividerHoverSince = time.Time{}
		a.uiState.previewDividerHot = false
	}
	if dragging {
		a.uiState.previewDividerHot = true
	}
	if a.uiState.previewDividerHot != a.uiState.previewDividerLastHot {
		a.uiState.previewDividerRun++
		a.uiState.previewDividerLastHot = a.uiState.previewDividerHot
	}
	normal := ui.RGBA(0, 0, 0, 0.09)
	if theme.Dark {
		normal = ui.RGBA(255, 255, 255, 0.11)
	}
	progress := float32(0)
	if a.uiState.previewDividerRun > 0 {
		frames := []ui.Keyframe{{At: 0, Value: 1}, {At: 100 * time.Millisecond, Value: 0, Ease: ui.EaseOut}}
		if a.uiState.previewDividerHot {
			frames = []ui.Keyframe{{At: 0, Value: 0}, {At: 100 * time.Millisecond, Value: 1, Ease: ui.EaseOut}}
		}
		progress = hit.Keyframes("preview-divider-hover", frames, ui.KeyframeOptions{Run: a.uiState.previewDividerRun})
	}
	lineWidth := float32(1) + progress
	lineColor := mixColor(normal, previewDividerAccent(0.55), progress)
	if dragging {
		lineWidth = 2
		lineColor = previewDividerAccent(1)
	}
	cursor := ui.CursorResizeEW
	if *pct <= minPreviewListPercent {
		cursor = ui.CursorResizeE
	} else if *pct >= maxPreviewListPercent {
		cursor = ui.CursorResizeW
	}
	hit.Cursor(cursor).PassThrough()
	if hit.Pressed() {
		a.uiState.previewDividerDragging = true
	}
	if a.uiState.previewDividerDragging && !hit.Pressed() {
		a.uiState.previewDividerDragging = false
	}
	hit.DrawOver(func(p *ui.Painter, r ui.Rect) {
		if lineWidth == 1 {
			p.Fill(ui.Rect{X: r.X + space4 - 0.5, Y: r.Y, W: 1, H: r.H}, lineColor, 0)
			return
		}
		p.Fill(ui.Rect{X: r.X + space4 - lineWidth/2, Y: r.Y, W: lineWidth, H: r.H}, lineColor, 0)
	})
}

func previewDividerAccent(alpha float32) ui.Color {
	return ui.RGBA(10, 132, 255, alpha)
}

func renderAgentRowBadge(c *ui.Context, theme *ui.Theme, item engine.OmniboxItem, sessions []agentsession.Session) {
	label := ""
	if session, ok := agentSessionForItem(item, sessions); ok {
		label = agentProviderLabel(session.Provider)
	} else if strings.HasPrefix(item.UtilityValue, "agent-command:") {
		label = "命令"
	} else if strings.HasPrefix(item.UtilityValue, "agent-diagnostic") {
		label = "诊断"
	}
	if label == "" {
		return
	}
	ui.Box(c).Padding(space1, space4).Radius(radius4).Background(tileFill(theme)).Border(space1, hairline(theme)).Shrink(0).Children(func() {
		ui.Text(c, label).FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
	})
}

// renderOmniboxFooterHints names what the keys do to the chosen row rather
// than listing every shortcut: its primary (↵) and secondary (⌘↵) actions,
// then the ⌥-number quick select. The rest lives in the ⌘K action menu.
// Last comes what the Tab arbitration leaves the field: "Tab Complete"
// while the query shows a completion, "⇧Tab Switch tab" otherwise. When the
// row would not fit beside the footer toggles, the quick select gives way
// first, then the secondary action; a truncated caption helps no one.
func (a *AppController) renderOmniboxFooterHints(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig, state engine.State) {
	_, completing := omniboxCompletion(state)
	tab := searchTabHint(completing)
	tab.drop = 1
	selected, ok := selectedOmniboxItem(state)
	if !ok {
		renderFooterHints(c, theme, []footerHint{tab, {keys: "esc", caption: i18n.T("omnibox.footer.close")}})
		return
	}
	var hints []footerHint
	for i, spec := range engine.AvailableItemActions(selected) {
		if i >= 2 {
			break
		}
		hints = append(hints, footerHint{keys: adaptActionShortcut(spec.Shortcut), caption: spec.Label, drop: 2 * i})
	}
	if cfg.Main.Appearance.ShowKeyHints && len(state.OmniboxItems) > 1 {
		tone := keyToneNeutral
		if c.Modifiers()&ui.Alt != 0 {
			tone = keyToneAccent
		}
		hints = append(hints, footerHint{keys: altPrefix() + "1–9", caption: i18n.T("omnibox.footer.quick_select"), tone: tone, drop: 3})
	}
	hints = append(hints, tab)
	renderFooterHints(c, theme, fitFooterHints(c, hints, omniboxFooterRoom(c)))
}

// omniboxFooterRoom is the width the footer leaves its hints: the window
// less the panel's side padding (space14), the preview and actions toggles
// (kitIconButton: space8 padding, a 16pt icon, space4, the label, 1px
// border) and the space12 gaps around the spacer between them.
func omniboxFooterRoom(c *ui.Context) float32 {
	w, _ := c.Size()
	room := w - 2*space14 - 3*space12
	for _, label := range []string{i18n.T("omnibox.footer.preview"), i18n.T("omnibox.footer.actions")} {
		lw, _ := c.MeasureText(0, ui.Span{Text: modPrefix() + label, Size: typeMeta})
		room -= 2*space8 + IconSize16 + space4 + lw + 2*space1
	}
	return room
}

// omniboxRouteMode names the search mode a quick-route prefix selects, for
// the chip beside the search field.
func omniboxRouteMode(query string) (icon, label string, ok bool) {
	prefix, _, routed := engine.ParseOmniboxPrefix(query)
	if !routed {
		return "", "", false
	}
	switch prefix {
	case '>':
		return "command", i18n.T("omnibox.mode.command"), true
	case ';':
		return "text.badge.plus", i18n.T("omnibox.mode.snippets"), true
	case '@':
		return "doc.on.clipboard", i18n.T("omnibox.mode.clipboard"), true
	case '?':
		return "sparkles", i18n.T("omnibox.mode.ai"), true
	case ':':
		return "face.smiling", i18n.T("omnibox.mode.emoji"), true
	case '=':
		return "calculator", i18n.T("omnibox.mode.calc"), true
	case '/':
		return "wand.and.stars", i18n.T("omnibox.mode.prompts"), true
	}
	return "", "", false
}

// renderOmniboxFooterFrameToggle renders a small footer toggle button.
func (a *AppController) renderOmniboxFooterFrameToggle(
	c *ui.Context,
	theme *ui.Theme,
	icon string,
	label string,
	active bool,
	onClick func(),
) {
	kitIconButton(c, theme, icon, active, label, onClick)
}

// renderOmniboxRowLeadingIcon draws a row's leading glyph at one fixed tile
// size, so titles align whatever the item: media thumbnails and app icons
// show as they are, a hex color shows as its own swatch, clipboard text gets
// a content-type icon, everything else its semantic icon in a neutral tile.
// Tiles stay neutral on the chosen row too: the selection bar carries it.
func (a *AppController) renderOmniboxRowLeadingIcon(c *ui.Context, theme *ui.Theme, item engine.OmniboxItem) {
	slot := func(body func()) {
		ui.Box(c).Size(rowLeadingSlot, rowLeadingSlot).Center().Shrink(0).Children(body)
	}
	switch item.Kind {
	case engine.ItemClipboardEntry:
		// Images are the content: a thumbnail filling the whole slot tells
		// them apart at a glance (it was the 26px icon tile's size).
		if m := item.ClipboardMedia; m != nil && m.ThumbPath != "" {
			if bmp := a.ThumbBitmap(m.ThumbPath); bmp != nil {
				ui.Image(c, bmp).Size(rowLeadingSlot, rowLeadingSlot).Fit(ui.Cover).Radius(radiusTile).Shrink(0)
				return
			}
		}
		if _, ok := parseHexSwatch(item.ClipboardContent); ok {
			ColorSwatch3D(c, item.ClipboardContent, rowLeadingSlot, effectiveSpatialAppearance(c, a.Config().Main.Appearance)).Border(1, hairline(theme))
			return
		}
		icon := item.Icon
		if item.ClipboardMedia == nil || item.ClipboardMedia.Kind == "" {
			icon = clipboardContentIcon(item.ClipboardContent, item.Icon)
		}
		slot(func() { iconTile(c, theme, icon, false) })

	case engine.ItemInstalledApp:
		if bmp := a.AppIconBitmap(item.AppPath); bmp != nil {
			slot(func() { ui.Image(c, bmp).Size(24, 24).Radius(radiusChip) })
			return
		}
		iconName := item.Icon
		if iconName == "" {
			iconName = "app.badge"
		}
		slot(func() { iconTile(c, theme, iconName, false) })

	case engine.ItemEmoji:
		glyph := strings.TrimSpace(item.UtilityValue)
		if glyph != "" {
			slot(func() {
				ui.Box(c).Size(rowIconTile, rowIconTile).Radius(radiusTile).Center().Background(tileFill(theme)).Children(func() {
					ui.Text(c, glyph).FontSize(typeGlyph).SingleLine()
				})
			})
			return
		}
		slot(func() { iconTile(c, theme, "face.smiling", false) })

	default:
		iconName := fallbackActionIcon(item.Icon, item.Title, item.Subtitle)
		slot(func() { iconTile(c, theme, iconName, false) })
	}
}

// rowSelectionTheme is the theme the Omnibox list builds with: the accent
// the list would paint its chosen row with is made transparent so the row
// draws its own soft, animated tint (see renderOmniboxView), and text on the
// chosen row keeps the regular text color.
func rowSelectionTheme(theme *ui.Theme) *ui.Theme {
	t := *theme
	t.Accent = ui.Transparent
	t.InactiveAccent = ui.Transparent
	t.AccentText = theme.Text
	return &t
}

func (a *AppController) handleOmniboxKey(key ui.Key, mods ui.Modifiers, state engine.State) bool {
	if state.OmniboxTab == config.TabAgent && mods == 0 && (key == ui.KeyDown || key == ui.KeyUp) {
		items := a.agentVisibleItems(state.OmniboxQuery)
		if len(items) == 0 {
			return true
		}
		idx := a.uiState.agentSelected
		if key == ui.KeyDown {
			idx = (idx + 1) % len(items)
		} else {
			idx = (idx - 1 + len(items)) % len(items)
		}
		a.uiState.agentSelected = idx
		a.SelectIndex(idx)
		return true
	}
	if state.OmniboxTab == config.TabAgent && key == ui.KeyEnter && mods == 0 {
		items := a.agentVisibleItems(state.OmniboxQuery)
		if state.SelectedIndex >= 0 && state.SelectedIndex < len(items) {
			return a.agentItemAction(items[state.SelectedIndex])
		}
		return true
	}
	// The contextual action menu takes navigation keys while it is open.
	if a.handleActionMenuKey(key, mods, state) {
		return true
	}

	// Direct row quick-select: ⌥1..⌥9 copies the row, ⌥⌘1..⌥⌘9 runs the secondary action.
	if mods == ui.Alt {
		if idx, ok := rowQuickKeyIndex(key); ok && idx < len(state.OmniboxItems) {
			a.Dispatch(engine.Event{Type: engine.EventSelectAndConfirmIndex, Index: idx})
			return true
		}
	}
	if mods == (ui.Alt|primaryMod()) || mods == (ui.Alt|ui.Cmd) || mods == (ui.Alt|ui.Ctrl) {
		if idx, ok := rowQuickKeyIndex(key); ok && idx < len(state.OmniboxItems) {
			a.Dispatch(engine.Event{
				Type:      engine.EventSelectAndConfirmIndex,
				Index:     idx,
				Secondary: true,
			})
			return true
		}
	}

	// Tab, Shift+Tab and Cmd+←/→: IME preedit > inline completion > tab
	// navigation. The focused search field accepts a completion itself
	// (OnTextKey); this path serves the field after a footer or list click.
	full, completing := omniboxCompletion(state)
	switch arbitrateSearchKey(key, mods, a.uiState.isComposing, completing) {
	case searchKeyIME:
		return false
	case searchKeyAccept:
		a.Dispatch(engine.Event{Type: engine.EventUpdateQuery, Query: full})
		return true
	case searchKeyInert:
		return true
	case searchKeyNextTab:
		a.switchOmniboxTab(cycleOmniboxTab(state.OmniboxTab, false))
		return true
	case searchKeyPrevTab:
		a.switchOmniboxTab(cycleOmniboxTab(state.OmniboxTab, true))
		return true
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
	review, hasReview := a.aiReview(state)
	if hasReview {
		c.OnWindowShortcut(primaryMod(), ui.KeyD, func() {
			a.toggleAIReview(review)
		})
	}

	ui.Column(c).FillWidth().Grow(1).Padding(space12, space14).Gap(8).Children(func() {
		if profiles := cfg.Main.AI.Profiles; len(profiles) > 1 {
			active := a.ActiveAIProfile()
			selected := 0
			for i, p := range profiles {
				if strings.EqualFold(p.ID, active.ID) {
					selected = i
					break
				}
			}
			CoverFlowDeck3D(c, len(profiles), selected, effectiveSpatialAppearance(c, cfg.Main.Appearance), func(i int, active bool) {
				p := profiles[i]
				button := ui.ButtonBase(c).Width(180).Height(34).Radius(radiusPill).FocusRing(false)
				if active {
					button.Background(theme.Accent)
				} else {
					button.Background(tileFill(theme)).Border(space1, hairline(theme))
				}
				button.Children(func() {
					ui.Text(c, p.ID).FontSize(typeMeta).FontWeight(600).TextColor(map[bool]ui.Color{true: theme.AccentText, false: theme.Text}[active]).SingleLine()
				})
				if button.Clicked() {
					_ = a.SelectAIProfile(p.ID)
				}
			})
		}
		if state.AISourceText != "" {
			srcRunes := utf8.RuneCountInString(state.AISourceText)
			headerLabel := i18n.Tf("ai.source_text", srcRunes)
			toggle := ui.ButtonBase(c).
				FillWidth().
				Padding(space6, space10).
				Radius(radius8).
				Background(theme.Surface).
				Border(1, theme.Border)
			toggle.Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, headerLabel).FontSize(typeLabel).FontWeight(500).TextColor(theme.TextMuted).SingleLine()
					ui.Spacer(c)
					chevron := "▸"
					if a.uiState.sourceExpanded {
						chevron = "▾"
					}
					ui.Text(c, chevron).FontSize(typeLabel).TextColor(theme.TextMuted).SingleLine()
				})
			})
			if toggle.Clicked() {
				a.uiState.sourceExpanded = !a.uiState.sourceExpanded
			}
			if a.uiState.sourceExpanded {
				ui.Box(c).
					FillWidth().
					MaxHeight(84).
					Padding(space8, space10).
					Radius(radius8).
					Background(theme.Surface).
					Border(1, withAlpha(theme.Border, alphaStrong)).
					Children(func() {
						ui.Scroll(c).FillWidth().Children(func() {
							ui.Text(c, state.AISourceText).FontSize(typeLabel).TextColor(theme.TextMuted)
						})
					})
			}
		}

		// Render previous multi-turn history pills when present
		if len(state.AIHistory) > 0 {
			ui.Box(c).
				FillWidth().
				MaxHeight(96).
				Padding(space6, space8).
				Radius(radius8).
				Background(withAlpha(theme.Surface, alphaStrong)).
				Border(1, withAlpha(theme.Border, alphaStrong)).
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
											Padding(space1, space6).
											Radius(radius4).
											Background(withAlpha(roleColor, alphaSubtle)).
											Children(func() {
												ui.Text(c, roleLabel).FontSize(typeMicro).FontWeight(700).TextColor(roleColor).SingleLine()
											})
										ui.Text(c, turn.Content).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine().Grow(1)
									})
							}
						})
					})
				})
		}

		if hasReview {
			a.renderAIReviewToggle(c, theme, review)
		}

		applyProseLineHeight(ui.Scroll(c).
			FillWidth().
			Grow(1).
			Padding(space10, space12).
			Radius(radiusCard).
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
					errBox := ui.Column(c).FillWidth().Children(func() {
						kitStateView(c, theme, kitStateError, "exclamationmark.circle", i18n.T("common.error"), state.AIError, func() {
							a.Dispatch(engine.Event{Type: engine.EventFollowUpAI, Prompt: state.AIPrompt})
						})
					})
					if frames := shakeKeyframes(profile); frames != nil {
						jolt := errBox.Keyframes("ai-error-shake", frames, ui.KeyframeOptions{Run: a.uiState.aiShakeRun})
						errBox.Translate(jolt*3, 0)
					}
				case state.AIOutput == "":
					kitStateView(c, theme, kitStateLoading, "arrow.clockwise", i18n.T("ai.generating"), "", nil)
				case hasReview && a.aiReviewShowsChanges(review):
					renderAIReviewChanges(c, theme, review)
				default:
					markdown.View(c, state.AIOutput, &a.uiState.mdOpts)
					if !state.AIComplete {
						// Pulsing caret while the answer streams in (900ms loop).
						caret := ui.Box(c).Width(7).Height(15).Radius(radius4)
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
				if ev.Kind != ui.InputKeyDown {
					return false
				}
				if a.followUpComposing() && ev.Mods == 0 {
					return false
				}
				return a.handleOmniboxKey(ev.Key, ev.Mods, state)
			})

			in.OnTextKey(func(k ui.TextKey) bool {
				if a.followUpComposing() && k.Mods == 0 {
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
	hud := kitCard(c, theme, cardFlat, func() {
		ui.Row(c).Fill().Center().Gap(space8).Children(func() {
			// Same vocabulary as the floating pill: success, failure, input
			// method or info, each in its readable semantic color.
			icon, color := pillMessageIcon(theme, state.HUDMessage)
			renderSemanticIcon(c, icon, typeBody, color)
			ui.Text(c, state.HUDMessage).FontSize(typeBody).FontWeight(600).TextColor(theme.Text).SingleLine().Shrink(1)
		})
	})
	hud.Fill()
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
	case config.TabAgent:
		return "Agent"
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
	case config.TabAgent:
		return "person.crop.circle"
	default:
		return "command"
	}
}

func omniboxPlaceholder(tab config.CapsuleTab) string {
	switch tab {
	case config.TabAgent:
		return "搜索 Agent 会话，输入 / 补全命令"
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
	case config.TabAgent:
		return "没有发现 Agent 会话"
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
	cycleTabs := make([]config.CapsuleTab, 0, len(omniboxTabs))
	for _, t := range omniboxTabs {
		cycleTabs = append(cycleTabs, t)
	}
	for i, t := range cycleTabs {
		if t == current {
			idx = i
			break
		}
	}
	if backward {
		idx = (idx - 1 + len(cycleTabs)) % len(cycleTabs)
	} else {
		idx = (idx + 1) % len(cycleTabs)
	}
	return cycleTabs[idx]
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
	case ui.Key6:
		return 5, true
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

// buildHighlightedSpans splits title into spans, marking the fuzzy/pinyin
// matched runes with a marker-pen background (mark) and a heavier weight.
// The glyphs keep the readable text color, so a match stays legible on any
// row tint and with any system accent.
func buildHighlightedSpans(title string, matchedIndices []int, mark, normal ui.Color) []ui.Span {
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
				Text:       buf.String(),
				Color:      normal,
				Weight:     700,
				Background: mark,
				Radius:     3,
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
