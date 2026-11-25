package uiapp

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// omniboxTabSpec is the single source of truth for Clavis's top-level
// workbenches, shared by the Omnibox tab bar and the Leader Dashboard.
type omniboxTabSpec struct {
	Tab       config.CapsuleTab
	QuickKey  string
	Icon      string
	LabelFunc func() string
}

var omniboxTabSpecs = []omniboxTabSpec{
	{Tab: config.TabActions, QuickKey: "1", Icon: "bolt", LabelFunc: func() string { return i18n.T("omnibox.tab.actions") }},
	{Tab: config.TabClipboard, QuickKey: "2", Icon: "doc.on.clipboard", LabelFunc: func() string { return i18n.T("omnibox.tab.clipboard") }},
	{Tab: config.TabSnippets, QuickKey: "3", Icon: "text.badge.plus", LabelFunc: func() string { return i18n.T("omnibox.tab.snippets") }},
	{Tab: config.TabAI, QuickKey: "4", Icon: "sparkles", LabelFunc: func() string { return i18n.T("omnibox.tab.ai") }},
	{Tab: config.TabGlossary, QuickKey: "5", Icon: "character.book.closed", LabelFunc: func() string { return i18n.T("omnibox.tab.glossary") }},
	{Tab: config.TabAgent, QuickKey: "6", Icon: "person.crop.circle", LabelFunc: func() string { return "Agent" }},
}

func omniboxTabSpecFor(tab config.CapsuleTab) (omniboxTabSpec, bool) {
	for _, spec := range omniboxTabSpecs {
		if spec.Tab == tab {
			return spec, true
		}
	}
	return omniboxTabSpec{}, false
}

func omniboxTabByQuickKey(key string) (config.CapsuleTab, bool) {
	for _, spec := range omniboxTabSpecs {
		if spec.QuickKey == key {
			return spec.Tab, true
		}
	}
	return "", false
}

type leaderActivityItem struct {
	Key      string
	Icon     string
	Tag      string
	Summary  string
	Meta     string
	Tone     chipTone
	Target   config.CapsuleTab
	HasBadge bool
}

// leaderRecentActivity collects up to 2 real, sanitized activity entries from
// live Agent sessions, Clipboard preview, and active selection context.
// Never invents placeholder or synthetic records.
func (a *AppController) leaderRecentActivity(state engine.State) []leaderActivityItem {
	items := make([]leaderActivityItem, 0, 2)

	// 1. Live Agent session (prioritize awaiting-approval, then running, then latest).
	sessions, _ := a.cachedAgentData()
	if len(sessions) > 0 {
		chosen := sessions[0]
		for _, s := range sessions {
			if s.PendingReview {
				chosen = s
				break
			}
			if s.Status == agentsession.StatusRunning && chosen.Status != agentsession.StatusRunning {
				chosen = s
			}
		}
		_, tone := agentLaneSymbolAndLabel(chosen)
		tag := agentProviderLabel(chosen.Provider)
		if chosen.PendingReview {
			tag = tag + " · " + i18n.T("leader.activity.review")
		}
		items = append(items, leaderActivityItem{
			Key:      "activity:agent:" + chosen.ID,
			Icon:     "person.crop.circle",
			Tag:      tag,
			Summary:  truncateRunes(nonEmptyOr(chosen.Title, agentOneLineConclusion(chosen)), 28),
			Meta:     truncateRunes(agentOneLineConclusion(chosen), 34),
			Tone:     tone,
			Target:   config.TabAgent,
			HasBadge: true,
		})
	}

	// 2. Latest real clipboard preview from current context.
	if clip := strings.TrimSpace(state.Context.ClipboardPreview); clip != "" && len(items) < 2 {
		sanitized := agentsession.SanitizeDiagnosticText(clip)
		items = append(items, leaderActivityItem{
			Key:     "activity:clipboard",
			Icon:    "doc.on.clipboard",
			Tag:     i18n.T("omnibox.tab.clipboard"),
			Summary: truncateRunes(sanitized, 32),
			Target:  config.TabClipboard,
		})
	}

	// 3. Active selection in foreground app (opens AI rewrite/ask).
	if sel := strings.TrimSpace(state.Context.SelectedText); sel != "" && len(items) < 2 {
		runes := utf8.RuneCountInString(sel)
		app := nonEmptyOr(state.Context.AppName, i18n.T("leader.activity.active_app"))
		items = append(items, leaderActivityItem{
			Key:     "activity:selection",
			Icon:    "sparkles",
			Tag:     app,
			Summary: i18n.Tf("leader.activity.selection", runes),
			Target:  config.TabAI,
		})
	}

	return items
}

// renderLeaderView renders the keyboard-first Leader Dashboard at root (two-column:
// pinned single-key Actions on the left + real-data Workbenches, System Status,
// and Recent Activity on the right) and collapses cleanly to a compact single-column
// list inside sub-groups or narrow/short frames.
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
		Padding(space0, space14).
		Gap(space0)

	if a.uiState.lastPhase != engine.PhaseLeader || !a.uiState.leaderHandle.Focused(c) {
		a.uiState.leaderHandle.RequestFocus(c)
	}

	container.HandleInput(func(ev ui.InputEvent) bool {
		return a.handleLeaderInput(ev)
	})

	winW, winH := c.Size()
	showDashboardRail := len(state.LeaderPath) == 0 && winW >= 580 && winH >= 220

	renderActionsList := func() {
		ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
			if len(state.LeaderNodes) == 0 {
				ui.Box(c).FillWidth().Padding(space24).Center().Children(func() {
					ui.Text(c, i18n.T("leader.empty")).TextColor(theme.TextMuted)
				})
				return
			}
			ui.Column(c).FillWidth().Padding(space6, space0).Gap(float32(leaderGridGap)).Children(func() {
				for _, node := range state.LeaderNodes {
					n := node
					isGroup := n.Action.Type == config.ActionGroup
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
						button.Draw(func(p *ui.Painter, r ui.Rect) {
							p.Fill(ui.Rect{X: r.X, Y: r.Y, W: rowSelectionBarWidth, H: r.H}, leaderSelectionBar(), 0)
						})
					}
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
					if button.Clicked() {
						a.Dispatch(engine.Event{Type: engine.EventLeaderKeyPress, Key: n.Key})
						a.uiState.leaderHandle.RequestFocus(c)
					}
				}
			})
		})
	}

	container.Children(func() {
		if showDashboardRail {
			ui.Row(c).FillWidth().Grow(1).MinHeight(0).Gap(space12).Children(func() {
				ui.Column(c).Grow(11).MinWidth(0).FillHeight().Children(func() {
					renderActionsList()
				})
				// Crisp 1px vertical hairline separating Actions from the Dashboard rail.
				ui.Box(c).Width(space1).FillHeight().Padding(space6, space0).Background(hairline(theme)).Shrink(0)
				activities := a.leaderRecentActivity(state)
				rows := leaderRailActivityRows(leaderRailHeight(winH), len(activities))
				ui.Column(c).Key("leader-dashboard-rail").Label("leader-dashboard-rail").Grow(10).MinWidth(0).FillHeight().
					Padding(leaderRailPadY, space0).Gap(leaderRailSection).Clip().Children(func() {
					a.renderLeaderWorkbenches(c, theme, state)
					a.renderLeaderSystemStatus(c, theme)
					if rows > 0 {
						a.renderLeaderRecentActivity(c, theme, state, activities[:rows])
					}
					// Quick tools sit on the rail's floor, level with the end of
					// the action list, whatever the activity block took.
					ui.Spacer(c)
					a.renderLeaderQuickTools(c, theme)
				})
			})
		} else {
			renderActionsList()
		}

		kitDivider(c, theme)
		ui.Row(c).FillWidth().Height(float32(leaderFooterHeight)).Gap(space12).AlignItems(ui.Center).Children(func() {
			keyHint(c, theme, "a–z", i18n.T("leader.hint.trigger"), keyToneNeutral)
			if len(state.LeaderPath) == 0 {
				keyHint(c, theme, "1–6", i18n.T("leader.dashboard.workbenches"), keyToneNeutral)
			}
			keyHint(c, theme, "/", i18n.T("leader.hint.search"), keyToneNeutral)
			// ⌫ goes up a level only inside a group; at the top it closes the
			// panel like esc, so "Back" there promised something it did not do.
			if len(state.LeaderPath) > 0 {
				keyHint(c, theme, "⌫", i18n.T("leader.hint.back"), keyToneNeutral)
			}
			keyHint(c, theme, "esc", i18n.T("leader.hint.close"), keyToneNeutral)
			ui.Spacer(c)
			if a.asciiLockedForCapsule() {
				chip(c, theme, "lock.fill", i18n.T("leader.ascii_locked"), chipNeutral)
			}
		})
	})
}

func (a *AppController) renderLeaderWorkbenches(c *ui.Context, theme *ui.Theme, state engine.State) {
	ui.Column(c).Key("leader-workbenches").Label("leader-workbenches").FillWidth().Gap(leaderRailInner).Shrink(0).Children(func() {
		ui.Row(c).FillWidth().Height(leaderRailCaption).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("leader.dashboard.workbenches")).FontSize(typeMicro).FontWeight(700).TextColor(theme.TextMuted).SingleLine()
			ui.Spacer(c)
			ui.Text(c, i18n.T("leader.dashboard.workbenches_hint")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		})
		ui.Grid(c).Columns(3).GapX(leaderRailInner).GapY(leaderRailInner).FillWidth().Children(func() {
			for _, spec := range omniboxTabSpecs {
				sp := spec
				lbl := sp.LabelFunc()
				btn := ui.ButtonBase(c).Key("leader-wb:"+string(sp.Tab)).Label("leader-wb:"+string(sp.Tab)).
					FillWidth().Height(leaderRailTile).Padding(space0, space6).Radius(radiusChip).
					Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false)
				if btn.Hovered() {
					btn.Background(leaderSelectionFill(theme)).Border(space1, selectionEdge(theme))
				}
				btn.Children(func() {
					ui.Row(c).FillWidth().Gap(space4).AlignItems(ui.Center).Children(func() {
						ui.Text(c, sp.QuickKey).Font("monospace").FontSize(typeMicro).FontWeight(700).TextColor(theme.TextMuted).SingleLine().Shrink(0)
						renderSemanticIcon(c, sp.Icon, typeMeta, secondaryInk(theme))
						ui.Text(c, lbl).FontSize(typeMicro).FontWeight(600).TextColor(theme.Text).SingleLine().Grow(1).MinWidth(0)
					})
				})
				if btn.Clicked() {
					ctxCopy := state.Context
					a.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: sp.Tab, Context: &ctxCopy})
				}
			}
		})
	})
}

func (a *AppController) renderLeaderSystemStatus(c *ui.Context, theme *ui.Theme) {
	prof := a.ActiveAIProfile()
	diag := a.Diagnostics()
	sessions, diagnostics := a.cachedAgentData()
	counts := countAgentLanes(sessions)
	usageDays, usageErr, usageLoaded, _ := a.aiUsageSnapshot()

	modelLabel := nonEmptyOr(prof.Model, nonEmptyOr(prof.ID, "AI"))
	if usageLoaded && usageErr == nil && aiUsageTotalRequests(usageDays) > 0 {
		modelLabel = modelLabel + " · " + aiUsageTodayLabel(usageDays)
	}

	agentSummary := i18n.T("leader.status.ready")
	agentDot := kitStateEmptyColor(theme)
	switch {
	case counts.Approval > 0:
		agentSummary = i18n.Tf("leader.status.review", counts.Approval)
		agentDot = kitStateWarningColor(theme)
	case counts.Running > 0:
		agentSummary = i18n.Tf("leader.status.active", counts.Running)
		agentDot = kitStateLoadingColor(theme)
	case counts.Total > 0:
		agentSummary = i18n.Tf("leader.status.sessions", counts.Total)
		agentDot = theme.Success
	case len(diagnostics) > 0:
		agentSummary = i18n.T("leader.status.degraded")
		agentDot = kitStateErrorColor(theme)
	}

	imeLabel := nonEmptyOr(diag.ActiveInputSource.Name, diag.ActiveInputSource.ID)
	if imeLabel == "" {
		imeLabel = i18n.T("leader.status.default_ime")
	}

	ui.Row(c).Key("leader-system-status").Label("leader-system-status").FillWidth().Height(leaderRailStatus).Shrink(0).Padding(space0, space8).Gap(space8).
		Radius(radiusChip).Background(tileFill(theme)).Border(space1, hairline(theme)).AlignItems(ui.Center).Children(func() {
		renderSemanticIcon(c, "sparkles", typeMicro, secondaryInk(theme))
		ui.Text(c, modelLabel).FontSize(typeMicro).FontWeight(600).FontFeatures("tnum").TextColor(theme.Text).SingleLine().Grow(1).MinWidth(0)
		ui.Row(c).Gap(space4).AlignItems(ui.Center).Shrink(0).Children(func() {
			kitStatusDot(c, agentDot, false)
			ui.Text(c, agentSummary).FontSize(typeMicro).FontWeight(600).FontFeatures("tnum").TextColor(secondaryInk(theme)).SingleLine()
		})
		ui.Text(c, imeLabel).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine().Shrink(0)
	})
}

func (a *AppController) renderLeaderQuickTools(c *ui.Context, theme *ui.Theme) {
	// One row: the caption leads its four buttons, so the tools cost the rail
	// a single tile height.
	ui.Row(c).Key("leader-quick-tools").Label("leader-quick-tools").FillWidth().Height(leaderRailTile).Shrink(0).Gap(leaderRailInner).AlignItems(ui.Center).Children(func() {
		ui.Text(c, i18n.T("leader.dashboard.quick_tools")).FontSize(typeMicro).FontWeight(700).TextColor(theme.TextMuted).SingleLine().Shrink(0).Padding(space0, space4, space0, space0)
		tool := func(key, label string, fn func()) {
			b := ui.ButtonBase(c).Key(key).Label(label).Grow(1).MinWidth(0).Height(leaderRailTile).Padding(space0, space6).Radius(radiusChip).
				Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false).Center()
			if b.Hovered() {
				b.Background(leaderSelectionFill(theme)).Border(space1, selectionEdge(theme))
			}
			b.Children(func() {
				ui.Text(c, label).FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
			})
			if b.Clicked() {
				fn()
			}
		}
		tool("leader-tool-capture", i18n.T("leader.tool.capture"), func() { go a.CaptureAndAnnotate() })
		tool("leader-tool-ocr", "OCR", func() { go a.CaptureAndOCR() })
		tool("leader-tool-translate", i18n.T("leader.tool.translate"), func() {
			a.StartTranslation(a.State().Context.SelectedText, "")
		})
		tool("leader-tool-assets", i18n.T("leader.tool.assets"), func() {
			a.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
			a.SetHubSurface(HubSurfaceAssets)
		})
	})
}

// renderLeaderRecentActivity lists the activity lines the rail has room for
// (leaderRailActivityRows): single-line list rows — tag, summary, a status dot
// — that highlight on hover like a native list, instead of boxed cards.
func (a *AppController) renderLeaderRecentActivity(c *ui.Context, theme *ui.Theme, state engine.State, activities []leaderActivityItem) {
	ui.Column(c).Key("leader-recent-activity").Label("leader-recent-activity").FillWidth().Shrink(0).Gap(leaderRailInner).Children(func() {
		ui.Row(c).FillWidth().Height(leaderRailCaption).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("leader.dashboard.recent_activity")).FontSize(typeMicro).FontWeight(700).TextColor(theme.TextMuted).SingleLine()
		})
		ui.Column(c).FillWidth().Gap(leaderRailRowGap).Children(func() {
			for _, act := range activities {
				item := act
				row := ui.ButtonBase(c).Key(item.Key).Label(item.Key).
					FillWidth().Height(leaderRailTile).Padding(space0, space8).Radius(radiusChip).FocusRing(false)
				if row.Hovered() {
					row.Background(leaderSelectionFill(theme))
				}
				row.Children(func() {
					ui.Row(c).FillWidth().FillHeight().Gap(space6).AlignItems(ui.Center).Children(func() {
						renderSemanticIcon(c, item.Icon, typeMeta, secondaryInk(theme))
						ui.Text(c, item.Tag).FontSize(typeMicro).FontWeight(700).TextColor(secondaryInk(theme)).SingleLine().Shrink(0)
						ui.Text(c, item.Summary).FontSize(typeMicro).FontWeight(500).TextColor(theme.Text).SingleLine().Grow(1).MinWidth(0)
						if dot, ok := leaderActivityDot(theme, item.Tone); ok {
							kitStatusDot(c, dot, false)
						}
					})
				})
				if row.Clicked() {
					ctxCopy := state.Context
					a.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: item.Target, Context: &ctxCopy})
				}
			}
		})
	})
}

// leaderActivityDot is the status dot that closes an activity line: orange
// for a session awaiting review, blue while one runs, green once it is done.
func leaderActivityDot(theme *ui.Theme, tone chipTone) (ui.Color, bool) {
	switch tone {
	case chipWarning:
		return kitStateWarningColor(theme), true
	case chipAccent:
		return kitStateLoadingColor(theme), true
	case chipSuccess:
		return theme.Success, true
	case chipDanger:
		return kitStateErrorColor(theme), true
	}
	return ui.Color{}, false
}

// Leader dashboard rail metrics. Every block of the rail has a fixed height,
// so the rail can be fitted to the panel exactly: the panel follows the action
// list (leaderHeightFor), and the rail shows only the recent-activity lines
// the remaining room holds rather than drawing them over the quick tools.
const (
	leaderRailPadY    = space6
	leaderRailSection = space8  // between the rail's blocks
	leaderRailInner   = space4  // between a caption and its block, and between tiles
	leaderRailCaption = space14 // a caption row: "Workbenches", "Recent activity"
	leaderRailTile    = space24 // workbench tiles, quick-tool buttons, activity lines
	leaderRailStatus  = space24 + space2
	leaderRailRowGap  = space2
)

// leaderRailHeight is the rail's inner height in a Leader panel winH tall:
// the panel less its header, footer, their two hairlines and the rail's
// vertical padding.
func leaderRailHeight(winH float32) float32 {
	return winH - leaderHeaderHeight - leaderFooterHeight - 2*space1 - 2*leaderRailPadY
}

// leaderRailActivityRows is how many recent-activity lines fit in a rail
// railH tall under its fixed blocks (workbenches, status, the activity
// caption, quick tools), capped at the entries there are. Zero hides the
// block.
func leaderRailActivityRows(railH float32, available int) int {
	fixed := leaderRailCaption + leaderRailInner + 2*leaderRailTile + leaderRailInner + // workbenches
		leaderRailSection + leaderRailStatus + // system status
		leaderRailSection + leaderRailCaption + leaderRailInner + // activity caption
		leaderRailSection + leaderRailTile // quick tools
	room := railH - fixed
	if available <= 0 || room < leaderRailTile {
		return 0
	}
	return min(int((room+leaderRailRowGap)/(leaderRailTile+leaderRailRowGap)), available)
}

func (a *AppController) leaderNodeHasKey(state engine.State, key string) bool {
	for _, n := range state.LeaderNodes {
		if strings.EqualFold(n.Key, key) {
			return true
		}
	}
	return false
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
		if ev.Key == ui.KeyTab && ev.Mods == 0 {
			a.uiState.suppressNextLeaderText = ""
			ctxCopy := a.State().Context
			a.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: &ctxCopy})
			return true
		}
		if keyStr, ok := leaderKeyString(ev.Key, ev.Mods); ok {
			a.uiState.suppressNextLeaderText = keyStr
			st := a.State()
			if len(st.LeaderPath) == 0 && !a.leaderNodeHasKey(st, keyStr) {
				if tab, ok := omniboxTabByQuickKey(keyStr); ok {
					ctxCopy := st.Context
					a.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: tab, Context: &ctxCopy})
					return true
				}
			}
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
			keyStr := string(r)
			st := a.State()
			if len(st.LeaderPath) == 0 && !a.leaderNodeHasKey(st, keyStr) {
				if tab, ok := omniboxTabByQuickKey(keyStr); ok {
					ctxCopy := st.Context
					a.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: tab, Context: &ctxCopy})
					return true
				}
			}
			a.Dispatch(engine.Event{Type: engine.EventLeaderKeyPress, Key: keyStr})
			return true
		}
	}
	return false
}
