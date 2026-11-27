package uiapp

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// agentLaneKind classifies every Agent session into a single scannable workbench
// lane so the user sees at a glance who is running, who is blocked on approval,
// and who has finished — without opening the log drawer.
type agentLaneKind string

const (
	agentLaneAll      agentLaneKind = ""
	agentLaneRunning  agentLaneKind = "running"
	agentLaneApproval agentLaneKind = "approval"
	agentLaneDone     agentLaneKind = "done"
	agentLaneWaiting  agentLaneKind = "waiting"
)

type agentLaneCounts struct {
	Total    int
	Running  int
	Approval int
	Done     int
}

func classifyAgentSessionLane(s agentsession.Session) agentLaneKind {
	if s.PendingReview {
		return agentLaneApproval
	}
	switch s.Status {
	case agentsession.StatusRunning:
		return agentLaneRunning
	case agentsession.StatusStopped:
		return agentLaneDone
	default:
		return agentLaneWaiting
	}
}

func countAgentLanes(sessions []agentsession.Session) agentLaneCounts {
	var counts agentLaneCounts
	counts.Total = len(sessions)
	for _, s := range sessions {
		switch classifyAgentSessionLane(s) {
		case agentLaneRunning:
			counts.Running++
		case agentLaneApproval:
			counts.Approval++
		case agentLaneDone:
			counts.Done++
		}
	}
	return counts
}

func filterAgentItemsByLane(items []engine.OmniboxItem, sessions []agentsession.Session, lane agentLaneKind) []engine.OmniboxItem {
	if lane == agentLaneAll {
		return items
	}
	filtered := make([]engine.OmniboxItem, 0, len(items))
	for _, item := range items {
		if session, ok := agentSessionForItem(item, sessions); ok {
			if classifyAgentSessionLane(session) == lane {
				filtered = append(filtered, item)
			}
			continue
		}
		// Keep slash commands and active error status rows visible.
		if strings.HasPrefix(item.UtilityValue, "agent-command:") || item.UtilityValue == "agent-refresh-status" {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

// agentOneLineConclusion produces a sanitized, single-sentence current action
// or outcome for a session so users never need to open the log drawer just to
// understand what happened.
func agentOneLineConclusion(s agentsession.Session) string {
	if msg := strings.TrimSpace(agentsession.SanitizeDiagnosticText(s.LastMessage)); msg != "" {
		return msg
	}
	zh := i18n.IsChinese()
	switch classifyAgentSessionLane(s) {
	case agentLaneApproval:
		if zh {
			return "已产出变更，等待人工审批与验收"
		}
		return "Output ready; awaiting human review"
	case agentLaneRunning:
		if zh {
			return "正在工作区执行任务…"
		}
		return "Actively executing in workspace…"
	case agentLaneDone:
		if zh {
			return "任务已完成并停止运行"
		}
		return "Task completed"
	default:
		if zh {
			return "会话空闲，等待下一步指令"
		}
		return "Idle; waiting for next instruction"
	}
}

func agentLaneSymbolAndLabel(s agentsession.Session) (string, chipTone) {
	zh := i18n.IsChinese()
	switch classifyAgentSessionLane(s) {
	case agentLaneApproval:
		if zh {
			return "▲ 待审批", chipWarning
		}
		return "▲ Approval", chipWarning
	case agentLaneRunning:
		if zh {
			return "● 运行中", chipAccent
		}
		return "● Running", chipAccent
	case agentLaneDone:
		if zh {
			return "✓ 已完成", chipSuccess
		}
		return "✓ Done", chipSuccess
	default:
		if zh {
			return "○ 等待中", chipNeutral
		}
		return "○ Waiting", chipNeutral
	}
}

// renderAgentWorkbenchBand renders the compact parallel-track summary strip
// above the Agent list, showing real lane counts (Running / Approval / Done)
// and a one-click Refresh trigger.
func (a *AppController) renderAgentWorkbenchBand(c *ui.Context, theme *ui.Theme) {
	sessions, diagnostics := a.cachedAgentData()
	counts := countAgentLanes(sessions)
	activeLane := agentLaneKind(capsuleUI.laneFilter(a))
	result := a.agentRecordedOperationResult()
	refreshing := a.agentRefreshing()
	zh := i18n.IsChinese()

	ui.Row(c).Key("agent-workbench-band").Label("agent-workbench-band").FillWidth().Wrap().Padding(space2, space0).Gap(space2).AlignItems(ui.Center).Children(func() {
		laneChip := func(lane agentLaneKind, label string, dotColor ui.Color, showDot bool) {
			selected := activeLane == lane
			btn := ui.ButtonBase(c).Key("agent-lane:"+string(lane)).Label(label).
				Padding(space2, space4).Radius(radiusChip).FocusRing(false).Shrink(0)
			fg := secondaryInk(theme)
			if selected {
				btn.Background(selectionFill(theme)).Border(space1, selectionEdge(theme))
				fg = accentInk(theme, surfaceBase(theme))
			} else {
				btn.Background(tileFill(theme)).Border(space1, hairline(theme))
			}
			btn.Children(func() {
				ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
					if showDot {
						ui.Box(c).Size(space6, space6).Radius(radiusPill).Background(dotColor).Shrink(0)
					}
					ui.Text(c, label).FontSize(typeMicro).FontWeight(600).FontFeatures("tnum").TextColor(fg).SingleLine()
				})
			})
			if btn.Clicked() {
				if selected {
					capsuleUI.setLaneFilter(a, "")
				} else {
					capsuleUI.setLaneFilter(a, string(lane))
				}
				a.mu.Lock()
				a.uiState.agentSelected = 0
				a.mu.Unlock()
			}
		}

		allLbl := fmt.Sprintf("All %d", counts.Total)
		runLbl := fmt.Sprintf("Running %d", counts.Running)
		appLbl := fmt.Sprintf("Approval %d", counts.Approval)
		doneLbl := fmt.Sprintf("Done %d", counts.Done)
		if zh {
			allLbl = fmt.Sprintf("全部 %d", counts.Total)
			runLbl = fmt.Sprintf("运行 %d", counts.Running)
			appLbl = fmt.Sprintf("待审 %d", counts.Approval)
			doneLbl = fmt.Sprintf("完成 %d", counts.Done)
		}

		laneChip(agentLaneAll, allLbl, ui.Transparent, false)
		if counts.Running > 0 || activeLane == agentLaneRunning {
			laneChip(agentLaneRunning, runLbl, kitStateLoadingColor(theme), true)
		}
		if counts.Approval > 0 || activeLane == agentLaneApproval {
			laneChip(agentLaneApproval, appLbl, kitStateWarningColor(theme), true)
		}
		if counts.Done > 0 || activeLane == agentLaneDone {
			laneChip(agentLaneDone, doneLbl, theme.Success, true)
		}

		ui.Spacer(c)

		if refreshing {
			kitStatusDot(c, kitStateLoadingColor(theme), true)
		} else if result.State == host.StateFailed || result.State == host.StateDegraded || len(diagnostics) > 0 {
			iconColor := kitStateWarningColor(theme)
			if result.State == host.StateFailed {
				iconColor = kitStateErrorColor(theme)
			}
			renderSemanticIcon(c, "exclamationmark.triangle", typeMeta, iconColor)
		}

		refreshLbl := "Refresh"
		if zh {
			refreshLbl = "刷新"
		}
		refBtn := ui.ButtonBase(c).Key("agent-band-refresh").Label(refreshLbl).
			Padding(space2, space6).Radius(radiusChip).Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false).Shrink(0)
		refBtn.Children(func() {
			ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
				renderSemanticIcon(c, "arrow.clockwise", typeMicro, secondaryInk(theme))
				ui.Text(c, refreshLbl).FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
			})
		})
		if refBtn.Clicked() {
			a.AgentRefresh()
		}
	})
	a.renderAgentWorkspaceStrip(c, theme)
}

// renderAgentRowLeading renders a semantic status symbol + provider badge for
// each Agent workbench lane row so status (Running / Approval / Done / Error)
// is immediately visible without opening the drawer.
func renderAgentRowLeading(c *ui.Context, theme *ui.Theme, item engine.OmniboxItem, sessions []agentsession.Session) {
	if session, ok := agentSessionForItem(item, sessions); ok {
		switch classifyAgentSessionLane(session) {
		case agentLaneApproval:
			renderSemanticIcon(c, "exclamationmark.triangle", typeMeta, kitStateWarningColor(theme))
		case agentLaneRunning:
			kitStatusDot(c, kitStateLoadingColor(theme), true)
		case agentLaneDone:
			renderSemanticIcon(c, "checkmark", typeMeta, theme.Success)
		default:
			kitStatusDot(c, kitStateEmptyColor(theme), false)
		}
		renderAgentRowBadge(c, theme, item, sessions)
		return
	}
	if item.UtilityValue == "agent-refresh-status" || strings.HasPrefix(item.UtilityValue, "agent-diagnostic") {
		renderSemanticIcon(c, "exclamationmark.circle", typeMeta, kitStateErrorColor(theme))
		renderAgentRowBadge(c, theme, item, sessions)
		return
	}
	renderSemanticIcon(c, "command", typeMeta, secondaryInk(theme))
	renderAgentRowBadge(c, theme, item, sessions)
}

func (a *AppController) copyAgentCommandPreset(commandID string) {
	for _, command := range agentCommands {
		if command.ID == commandID {
			msg := command.Label + "\n" + command.Detail
			if err := a.writeClipboardText(msg); err != nil {
				a.ShowToastOrHUD(agentsession.SanitizeDiagnosticText(err.Error()))
			} else {
				a.ShowToastOrHUD("已复制待发消息，未发送")
			}
			return
		}
	}
}

// renderAgentPreviewBody renders the right-hand Agent Workbench inspector:
//   - Always visible (drawer closed): who (Provider), state pill, title, one-line
//     conclusion / current action, session ID, and state-specific actions
//     (blocking Approval gate, Completed summary, or Failure retry CTA).
//   - Collapsible log drawer (closed by default): sanitized workspace, target ref,
//     source, timestamps, full sanitized message, and redacted diagnostics.
func (a *AppController) renderAgentPreviewBody(c *ui.Context, theme *ui.Theme, item engine.OmniboxItem) {
	sessions, diagnostics := a.cachedAgentData()
	zh := i18n.IsChinese()

	if session, ok := agentSessionForItem(item, sessions); ok {
		lane := classifyAgentSessionLane(session)
		statusText, statusTone := agentLaneSymbolAndLabel(session)
		if session.HerdrManaged && session.PaneID != "" {
			a.EnsureAgentOutput(session.PaneID)
		}

		// Header: title + provider + semantic state pill + workspace/pane + relative time.
		ui.Text(c, session.Title).FontSize(typeBody).FontWeight(700).TextColor(theme.Text).Wrap()
		ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
			if provider := agentProviderLabel(session.Provider); provider != "" {
				previewBadge(c, theme, "person.crop.circle", provider, false)
			}
			kitStatusPill(c, theme, statusText, statusTone)
			if ws := safeAgentWorkspaceLabel(session); ws != "" {
				previewBadge(c, theme, "folder", ws, false)
			}
			if pane := strings.TrimSpace(session.PaneID); pane != "" {
				previewBadge(c, theme, "terminal", pane, false)
			}
			previewBadge(c, theme, "", agentRelativeTime(session.LastActivityAt, time.Now()), false)
		})

		// One-line conclusion / live Herdr terminal Markdown output card.
		conclusionTitle := "Latest outcome"
		if lane == agentLaneRunning {
			conclusionTitle = "Current action"
		} else if lane == agentLaneApproval {
			conclusionTitle = "Pending summary"
		}
		if zh {
			switch lane {
			case agentLaneRunning:
				conclusionTitle = "当前动作"
			case agentLaneApproval:
				conclusionTitle = "待审批摘要"
			default:
				conclusionTitle = "一句结论"
			}
		}

		var liveOut agentOutputSnapshot
		if session.HerdrManaged && session.PaneID != "" {
			liveOut = a.cachedAgentOutput(session.PaneID)
		}
		hasLiveOutput := strings.TrimSpace(liveOut.Content) != ""
		if hasLiveOutput {
			conclusionTitle = pickLang(zh, "实时终端输出（已脱敏）", "Live Output (Redacted)")
		}

		ui.Column(c).Key("agent-conclusion-card").FillWidth().Padding(space8, space10).Gap(space4).
			Radius(radius8).Background(tileFill(theme)).Border(space1, hairline(theme)).Children(func() {
			ui.Row(c).FillWidth().Gap(space4).AlignItems(ui.Center).Children(func() {
				ui.Text(c, conclusionTitle).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine().Grow(1)
				if session.HerdrManaged && session.PaneID != "" {
					if liveOut.Loading {
						kitStatusDot(c, kitStateLoadingColor(theme), true)
					}
					if hasLiveOutput {
						copyOutLbl := pickLang(zh, "复制", "Copy")
						copyOutBtn := ui.ButtonBase(c).Key("agent-output-copy").Label(copyOutLbl).
							Padding(space1, space6).Radius(radius4).Background(theme.Surface).Border(space1, hairline(theme)).FocusRing(false)
						copyOutBtn.Children(func() {
							ui.Text(c, copyOutLbl).FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
						})
						if copyOutBtn.Clicked() {
							if err := a.writeClipboardText(liveOut.Content); err != nil {
								a.ShowToastOrHUD(agentsession.SanitizeDiagnosticText(err.Error()))
							} else {
								a.ShowToastOrHUD(i18n.T("hud.copied"))
							}
						}
					}
					refOutLbl := pickLang(zh, "刷新输出", "Pull")
					refOutBtn := ui.ButtonBase(c).Key("agent-output-refresh").Label(refOutLbl).
						Padding(space1, space6).Radius(radius4).Background(theme.Surface).Border(space1, hairline(theme)).FocusRing(false)
					refOutBtn.Children(func() {
						ui.Text(c, refOutLbl).FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
					})
					if refOutBtn.Clicked() {
						a.ForceRefreshAgentOutput(session.PaneID)
					}
				}
			})
			if hasLiveOutput {
				a.renderClavisMarkdown(c, theme, liveOut.Content, false)
			} else {
				conclusion := agentOneLineConclusion(session)
				if detectPreviewContentKind(conclusion, "") == previewKindMarkdown {
					a.renderClavisMarkdown(c, theme, conclusion, false)
				} else {
					ui.Text(c, conclusion).FontSize(typeLabel).FontWeight(500).TextColor(theme.Text).Wrap()
				}
			}
		})

		// State-specific action zone: Blocking approval gate vs Done vs Running/Waiting.
		switch lane {
		case agentLaneApproval:
			a.renderAgentApprovalGate(c, theme, item, session)
		case agentLaneDone:
			a.renderAgentDoneBanner(c, theme, item)
		default:
			focusLbl := "Focus / Copy ID"
			if zh {
				focusLbl = "跳转 / 复制 ID"
			}
			ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
				btn := ui.PrimaryButton(c, focusLbl).Key("agent-focus-btn").Label(focusLbl)
				if btn.Clicked() {
					_ = a.agentItemAction(item)
				}
			})
		}

		// Keep compact session ID always inspectable (and tested by agent_sessions_test).
		if id := strings.TrimSpace(session.ID); id != "" {
			ui.Text(c, "ID: "+id).Font("monospace").FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		}

		a.renderAgentManageControls(c, theme, session)

		// Collapsible log & technical details drawer (collapsed by default).
		a.renderAgentLogDrawer(c, theme, session, diagnostics)
		return
	}

	if strings.HasPrefix(item.UtilityValue, "agent-command:") {
		cmdLabel := "Command"
		if zh {
			cmdLabel = "快捷命令"
		}
		ui.Row(c).FillWidth().Gap(space4).AlignItems(ui.Center).Children(func() {
			previewBadge(c, theme, "command", cmdLabel, false)
		})
		ui.Text(c, item.Title).FontSize(typeBody).FontWeight(700).TextColor(theme.Text).Wrap()
		if item.Subtitle != "" {
			kitInsetWell(c, theme, func() {
				renderPreviewLines(c, theme, item.Title+"\n"+item.Subtitle, 8)
			})
		}
		copyLbl := "Copy prompt"
		if zh {
			copyLbl = "复制待发命令"
		}
		if ui.PrimaryButton(c, copyLbl).Key("agent-copy-cmd").Label(copyLbl).Clicked() {
			_ = a.agentItemAction(item)
		}
		return
	}

	if item.UtilityValue == "agent-refresh-status" {
		res := a.agentRecordedOperationResult()
		a.renderAgentErrorCard(c, theme, item.Title, item.Subtitle, res, diagnostics)
		return
	}

	// Diagnostic item: always show redacted diagnostic + retry CTA if retryable.
	res := a.agentRecordedOperationResult()
	a.renderAgentErrorCard(c, theme, item.Title, agentsession.SanitizeDiagnosticText(item.Subtitle), res, diagnostics)
}

func (a *AppController) renderAgentApprovalGate(c *ui.Context, theme *ui.Theme, item engine.OmniboxItem, session agentsession.Session) {
	zh := i18n.IsChinese()
	warning := kitStateWarningColor(theme)
	bg := warning.Alpha(alphaSubtle)
	fg := readableOn(warning, theme.Text, bg.Over(surfaceBase(theme)), minTextContrast)

	headline := "Blocked on human approval"
	focusLbl := "Focus & Review"
	copyReviewLbl := "Copy Review Prompt"
	if zh {
		headline = "审批阻塞：等待人工验收确认"
		focusLbl = "跳转并验收"
		copyReviewLbl = "复制验收指令"
	}

	ui.Column(c).Key("agent-approval-gate").Label("agent-approval-gate").FillWidth().Padding(space8, space10).Gap(space6).
		Radius(radius8).Background(bg).Border(space1, warning.Alpha(alphaHalf)).Children(func() {
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, "exclamationmark.triangle", typeLabel, fg)
			ui.Text(c, headline).FontSize(typeMeta).FontWeight(600).TextColor(fg).Wrap().Grow(1)
		})
		ui.Row(c).FillWidth().Wrap().Gap(space6).AlignItems(ui.Center).Children(func() {
			if ui.PrimaryButton(c, focusLbl).Key("agent-approve-focus").Label(focusLbl).Clicked() {
				_ = a.agentItemAction(item)
			}
			if ui.Button(c, copyReviewLbl).Key("agent-approve-copy").Label(copyReviewLbl).Clicked() {
				a.copyAgentCommandPreset("review")
			}
		})
	})
}

func (a *AppController) renderAgentDoneBanner(c *ui.Context, theme *ui.Theme, item engine.OmniboxItem) {
	zh := i18n.IsChinese()
	bg := theme.Success.Alpha(alphaSubtle)
	fg := readableOn(theme.Success, theme.Text, bg.Over(surfaceBase(theme)), minTextContrast)

	doneMsg := "Session finished — ready to review or continue"
	focusLbl := "Open / Copy ID"
	contLbl := "Copy Continue Prompt"
	if zh {
		doneMsg = "已完成：可跳转复盘或复制续接指令"
		focusLbl = "跳转 / 复制 ID"
		contLbl = "复制续接指令"
	}

	ui.Column(c).Key("agent-done-card").Label("agent-done-card").FillWidth().Padding(space8, space10).Gap(space6).
		Radius(radius8).Background(bg).Border(space1, theme.Success.Alpha(alphaHalf)).Children(func() {
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, "checkmark", typeLabel, fg)
			ui.Text(c, doneMsg).FontSize(typeMeta).FontWeight(600).TextColor(fg).Wrap().Grow(1)
		})
		ui.Row(c).FillWidth().Wrap().Gap(space6).AlignItems(ui.Center).Children(func() {
			if ui.PrimaryButton(c, focusLbl).Key("agent-done-focus").Label(focusLbl).Clicked() {
				_ = a.agentItemAction(item)
			}
			if ui.Button(c, contLbl).Key("agent-done-continue").Label(contLbl).Clicked() {
				a.copyAgentCommandPreset("continue")
			}
		})
	})
}

func (a *AppController) renderAgentErrorCard(c *ui.Context, theme *ui.Theme, title, subtitle string, res host.OperationResult, diagnostics []agentsession.Diagnostic) {
	zh := i18n.IsChinese()
	pillText := "✕ Sync Error"
	if zh {
		pillText = "✕ 同步异常"
	}
	if res.State == host.StateDegraded {
		pillText = "▲ Degraded"
		if zh {
			pillText = "▲ 部分源降级"
		}
	} else if res.State == host.StateUnknown {
		pillText = "○ Unknown"
		if zh {
			pillText = "○ 状态未知"
		}
	}

	tone := chipDanger
	if res.State == host.StateDegraded {
		tone = chipWarning
	} else if res.State == host.StateUnknown {
		tone = chipNeutral
	}

	ui.Row(c).FillWidth().Gap(space4).AlignItems(ui.Center).Children(func() {
		kitStatusPill(c, theme, pillText, tone)
	})

	errSummary := agentsession.SanitizeDiagnosticText(title)
	if subtitle != "" && subtitle != title {
		errSummary = errSummary + " · " + agentsession.SanitizeDiagnosticText(subtitle)
	}
	if res.State == host.StateDegraded || res.State == host.StateUnknown {
		kitWarningBanner(c, theme, errSummary)
	} else {
		kitErrorBanner(c, theme, errSummary)
	}

	if res.State != host.StateUnknown && (res.Retryable || len(diagnostics) > 0) {
		retryLbl := "Retry Refresh"
		if zh {
			retryLbl = "重试刷新"
		}
		if ui.PrimaryButton(c, retryLbl).Key("agent-retry-refresh").Label(retryLbl).Clicked() {
			a.AgentRefresh()
		}
	}

	if len(diagnostics) > 0 {
		diagTitle := "Redacted diagnostics"
		if zh {
			diagTitle = "已脱敏诊断详情"
		}
		kitInsetWell(c, theme, func() {
			ui.Text(c, diagTitle).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
			for _, d := range diagnostics {
				line := fmt.Sprintf("• %s (%s): %s", agentProviderLabel(d.Provider), d.Code, agentsession.SanitizeDiagnosticText(d.Message))
				ui.Text(c, line).FontSize(typeMicro).TextColor(theme.Text).Wrap()
			}
		})
	}
}

func (a *AppController) renderAgentLogDrawer(c *ui.Context, theme *ui.Theme, session agentsession.Session, diagnostics []agentsession.Diagnostic) {
	zh := i18n.IsChinese()
	open := capsuleUI.drawerOpen(a)

	logCount := 0
	if strings.TrimSpace(session.Workspace) != "" {
		logCount++
	}
	if strings.TrimSpace(session.LastMessage) != "" {
		logCount++
	}
	if strings.TrimSpace(session.TargetRef) != "" {
		logCount++
	}
	logCount += len(diagnostics)

	drawerLbl := fmt.Sprintf("Log drawer · %d", logCount)
	if zh {
		drawerLbl = fmt.Sprintf("日志抽屉 · %d 项", logCount)
	}
	chevron := "▸"
	if open {
		chevron = "▾"
	}

	toggle := ui.ButtonBase(c).Key("agent-log-drawer-toggle").Label(drawerLbl).
		FillWidth().Padding(space4, space8).Radius(radiusChip).Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false)
	toggle.Children(func() {
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, "terminal", typeMicro, secondaryInk(theme))
			ui.Text(c, drawerLbl).FontSize(typeMicro).FontWeight(600).FontFeatures("tnum").TextColor(secondaryInk(theme)).SingleLine()
			ui.Spacer(c)
			ui.Text(c, chevron).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		})
	})
	if toggle.Clicked() {
		capsuleUI.toggleDrawerOpen(a)
	}

	if !open {
		return
	}

	ui.Column(c).Key("agent-log-drawer-body").Label("agent-log-drawer-body").FillWidth().Padding(space8, space10).Gap(space4).
		Radius(radius8).Background(tileFill(theme)).Border(space1, hairline(theme)).Children(func() {
		if ws := strings.TrimSpace(session.Workspace); ws != "" {
			wsLbl := "Workspace"
			if zh {
				wsLbl = "工作区（已脱敏）"
			}
			ui.Text(c, wsLbl).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
			ui.Text(c, agentsession.SanitizeDiagnosticText(ws)).Font("monospace").FontSize(typeMicro).TextColor(theme.Text).Wrap()
		}
		if ref := strings.TrimSpace(session.TargetRef); ref != "" {
			ui.Text(c, "Ref: "+agentsession.SanitizeDiagnosticText(ref)).Font("monospace").FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		}
		if src := strings.TrimSpace(session.Source); src != "" {
			ui.Text(c, "Source: "+agentsession.SanitizeDiagnosticText(src)).Font("monospace").FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		}
		if msg := strings.TrimSpace(agentsession.SanitizeDiagnosticText(session.LastMessage)); msg != "" {
			msgLbl := "Last output"
			if zh {
				msgLbl = "最近输出（已脱敏）"
			}
			ui.Text(c, msgLbl).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
			renderPreviewLines(c, theme, msg, 10)
		}
		for _, d := range diagnostics {
			line := fmt.Sprintf("[%s] %s", agentProviderLabel(d.Provider), agentsession.SanitizeDiagnosticText(d.Message))
			ui.Text(c, line).Font("monospace").FontSize(typeMicro).TextColor(theme.TextMuted).Wrap()
		}
	})
}

func agentItemFooterHints(item engine.OmniboxItem, retryable bool) []footerHint {
	zh := i18n.IsChinese()
	refreshCaption := "Refresh"
	if zh {
		refreshCaption = "刷新"
	}
	var hints []footerHint
	switch {
	case strings.HasPrefix(item.UtilityValue, "agent-session:"):
		cap := "Focus / Copy ID"
		if zh {
			cap = "跳转 / 复制 ID"
		}
		hints = append(hints, footerHint{keys: "↵", caption: cap, drop: 0})
	case strings.HasPrefix(item.UtilityValue, "agent-command:"):
		cap := "Copy prompt"
		if zh {
			cap = "复制命令"
		}
		hints = append(hints, footerHint{keys: "↵", caption: cap, drop: 0})
	case item.UtilityValue == "agent-refresh-status" && retryable:
		cap := "Retry"
		if zh {
			cap = "重试"
		}
		hints = append(hints, footerHint{keys: "↵", caption: cap, drop: 0})
	}
	hints = append(hints, footerHint{keys: modPrefix() + "R", caption: refreshCaption, drop: 2})
	return hints
}
