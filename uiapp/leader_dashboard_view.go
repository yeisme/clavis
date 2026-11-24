package uiapp

import (
	"fmt"
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
	zh := i18n.IsChinese()
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
			tag = tag + " · " + pickLang(zh, "待审", "Review")
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
			Tag:     pickLang(zh, "剪贴板", "Clipboard"),
			Summary: truncateRunes(sanitized, 32),
			Target:  config.TabClipboard,
		})
	}

	// 3. Active selection in foreground app (opens AI rewrite/ask).
	if sel := strings.TrimSpace(state.Context.SelectedText); sel != "" && len(items) < 2 {
		runes := utf8.RuneCountInString(sel)
		app := nonEmptyOr(state.Context.AppName, pickLang(zh, "当前应用", "Active app"))
		items = append(items, leaderActivityItem{
			Key:     "activity:selection",
			Icon:    "sparkles",
			Tag:     app,
			Summary: pickLang(zh, fmt.Sprintf("已选中 %d 字 · 点击进入 AI", runes), fmt.Sprintf("%d chars selected · Open AI", runes)),
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
				ui.Column(c).Key("leader-dashboard-rail").Label("leader-dashboard-rail").Grow(10).MinWidth(0).FillHeight().
					Padding(space6, space0).Gap(space8).Children(func() {
					a.renderLeaderWorkbenches(c, theme, state)
					a.renderLeaderSystemStatus(c, theme)
					a.renderLeaderRecentActivity(c, theme, state)
					a.renderLeaderQuickTools(c, theme)
				})
			})
		} else {
			renderActionsList()
		}

		ui.Divider(c)
		ui.Row(c).FillWidth().Height(float32(leaderFooterHeight)).Gap(space12).AlignItems(ui.Center).Children(func() {
			keyHint(c, theme, "a–z", i18n.T("leader.hint.trigger"), keyToneNeutral)
			if len(state.LeaderPath) == 0 {
				keyHint(c, theme, "1–6", pickLang(i18n.IsChinese(), "工作台", "Workbenches"), keyToneNeutral)
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
	zh := i18n.IsChinese()
	ui.Column(c).Key("leader-workbenches").Label("leader-workbenches").FillWidth().Gap(space4).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, pickLang(zh, "工作台", "Workbenches")).FontSize(typeMicro).FontWeight(700).TextColor(theme.TextMuted).SingleLine()
			ui.Spacer(c)
			ui.Text(c, pickLang(zh, "1–6 直达 · Tab 搜索", "1–6 Jump · Tab Search")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		})
		ui.Grid(c).Columns(3).GapX(space4).GapY(space4).FillWidth().Children(func() {
			for _, spec := range omniboxTabSpecs {
				sp := spec
				lbl := sp.LabelFunc()
				btn := ui.ButtonBase(c).Key("leader-wb:"+string(sp.Tab)).Label("leader-wb:"+string(sp.Tab)).
					FillWidth().Height(space24).Padding(space2, space6).Radius(radiusChip).
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
	zh := i18n.IsChinese()
	prof := a.ActiveAIProfile()
	diag := a.Diagnostics()
	sessions, diagnostics := a.cachedAgentData()
	counts := countAgentLanes(sessions)
	usageDays, usageErr, usageLoaded, _ := a.aiUsageSnapshot()

	modelLabel := nonEmptyOr(prof.Model, nonEmptyOr(prof.ID, "AI"))
	if usageLoaded && usageErr == nil && aiUsageTotalRequests(usageDays) > 0 {
		modelLabel = modelLabel + " · " + aiUsageTodayLabel(usageDays)
	}

	agentSummary := pickLang(zh, "就绪", "Ready")
	agentDot := kitStateEmptyColor(theme)
	switch {
	case counts.Approval > 0:
		agentSummary = pickLang(zh, fmt.Sprintf("%d 待审", counts.Approval), fmt.Sprintf("%d review", counts.Approval))
		agentDot = kitStateWarningColor(theme)
	case counts.Running > 0:
		agentSummary = pickLang(zh, fmt.Sprintf("%d 运行", counts.Running), fmt.Sprintf("%d active", counts.Running))
		agentDot = kitStateLoadingColor(theme)
	case counts.Total > 0:
		agentSummary = pickLang(zh, fmt.Sprintf("%d 会话", counts.Total), fmt.Sprintf("%d sessions", counts.Total))
		agentDot = theme.Success
	case len(diagnostics) > 0:
		agentSummary = pickLang(zh, "数据源异常", "Degraded")
		agentDot = kitStateErrorColor(theme)
	}

	imeLabel := nonEmptyOr(diag.ActiveInputSource.Name, diag.ActiveInputSource.ID)
	if imeLabel == "" {
		imeLabel = pickLang(zh, "默认输入源", "Default IME")
	}

	ui.Row(c).Key("leader-system-status").Label("leader-system-status").FillWidth().Padding(space4, space8).Gap(space8).
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
	zh := i18n.IsChinese()
	ui.Column(c).Key("leader-quick-tools").FillWidth().Gap(space4).Children(func() {
		ui.Text(c, pickLang(zh, "快捷工具", "Quick Tools")).FontSize(typeMicro).FontWeight(700).TextColor(theme.TextMuted).SingleLine()
		ui.Row(c).FillWidth().Gap(space4).AlignItems(ui.Center).Children(func() {
			tool := func(key, label string, fn func()) {
				b := ui.ButtonBase(c).Key(key).Label(label).Grow(1).Padding(space4, space6).Radius(radiusChip).
					Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false)
				b.Children(func() {
					ui.Text(c, label).FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
				})
				if b.Clicked() {
					fn()
				}
			}
			tool("leader-tool-capture", pickLang(zh, "截图", "Capture"), func() { go a.CaptureAndAnnotate() })
			tool("leader-tool-ocr", "OCR", func() { go a.CaptureAndOCR() })
			tool("leader-tool-translate", pickLang(zh, "翻译", "Translate"), func() {
				a.StartTranslation(a.State().Context.SelectedText, "")
			})
			tool("leader-tool-assets", pickLang(zh, "资产", "Assets"), func() {
				a.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
				a.SetHubSurface(HubSurfaceAssets)
			})
		})
	})
}

func (a *AppController) renderLeaderRecentActivity(c *ui.Context, theme *ui.Theme, state engine.State) {
	zh := i18n.IsChinese()
	activities := a.leaderRecentActivity(state)
	ui.Column(c).Key("leader-recent-activity").Label("leader-recent-activity").FillWidth().Grow(1).MinHeight(0).Gap(space4).Children(func() {
		ui.Text(c, pickLang(zh, "最近活动", "Recent Activity")).FontSize(typeMicro).FontWeight(700).TextColor(theme.TextMuted).SingleLine()
		if len(activities) == 0 {
			ui.Row(c).FillWidth().Padding(space6, space8).Radius(radiusChip).Background(tileFill(theme)).Border(space1, hairline(theme)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, pickLang(zh, "暂无最近活动，按 / 或 1–6 开始", "No recent activity · press / or 1–6")).
					FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
			})
			return
		}
		for _, act := range activities {
			item := act
			row := ui.ButtonBase(c).Key(item.Key).Label(item.Key).
				FillWidth().Padding(space4, space8).Radius(radiusChip).
				Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false)
			if row.Hovered() {
				row.Background(leaderSelectionFill(theme)).Border(space1, selectionEdge(theme))
			}
			row.Children(func() {
				ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
					renderSemanticIcon(c, item.Icon, typeMeta, secondaryInk(theme))
					ui.Column(c).Grow(1).MinWidth(0).Gap(space1).Children(func() {
						ui.Row(c).FillWidth().Gap(space4).AlignItems(ui.Center).Children(func() {
							ui.Text(c, item.Tag).FontSize(typeMicro).FontWeight(700).TextColor(secondaryInk(theme)).SingleLine().Shrink(0)
							ui.Text(c, item.Summary).FontSize(typeMicro).FontWeight(500).TextColor(theme.Text).SingleLine().Grow(1).MinWidth(0)
						})
						if item.Meta != "" && item.Meta != item.Summary {
							ui.Text(c, item.Meta).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
						}
					})
				})
			})
			if row.Clicked() {
				ctxCopy := state.Context
				a.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: item.Target, Context: &ctxCopy})
			}
		}
	})
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
