package uiapp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
	runtimecoord "github.com/yeisme/clavis/pkg/core/runtime"
)

const maxAgentSessionControlStates = 64

type agentSessionControlState struct {
	renameOpen   bool
	renameDraft  string
	promptOpen   bool
	promptDraft  string
	confirmArmed bool
	statusMsg    string
	statusIsErr  bool
	touchedAt    time.Time
}

func (cs *agentSessionControlState) isEmpty() bool {
	return cs == nil || (!cs.renameOpen && cs.renameDraft == "" && !cs.promptOpen && cs.promptDraft == "" && !cs.confirmArmed && cs.statusMsg == "")
}

type agentManageUIState struct {
	mu              sync.Mutex
	workspaceFilter string
	startOpen       bool
	startCLI        string
	startName       string
	statusMsg       string
	statusIsErr     bool
	watching        map[string]bool
	sessions        map[string]*agentSessionControlState
}

func agentManageStateFor(c *AppController) *agentManageUIState {
	return c.agentManageStateForCtrl()
}

func agentSessionControlKey(session agentsession.Session) string {
	if id := strings.TrimSpace(session.PaneID); id != "" {
		return id
	}
	return strings.TrimSpace(session.ID)
}

func (c *AppController) resolveAgentControlKey(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	sessions, _ := c.cachedAgentData()
	for _, s := range sessions {
		if strings.TrimSpace(s.PaneID) == target || strings.TrimSpace(s.ID) == target || strings.TrimSpace(s.AgentName) == target {
			if key := agentSessionControlKey(s); key != "" {
				return key
			}
		}
	}
	return target
}

func (s *agentManageUIState) sessionLocked(sessionID string, create bool) *agentSessionControlState {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	if s.sessions == nil {
		if !create {
			return nil
		}
		s.sessions = make(map[string]*agentSessionControlState)
	}
	if cs, ok := s.sessions[sessionID]; ok && cs != nil {
		cs.touchedAt = time.Now()
		return cs
	}
	if !create {
		return nil
	}
	s.evictSessionsLocked()
	cs := &agentSessionControlState{touchedAt: time.Now()}
	s.sessions[sessionID] = cs
	return cs
}

func (s *agentManageUIState) evictSessionsLocked() {
	if len(s.sessions) < maxAgentSessionControlStates {
		return
	}
	for k, v := range s.sessions {
		if v.isEmpty() {
			delete(s.sessions, k)
		}
	}
	for len(s.sessions) >= maxAgentSessionControlStates {
		var oldestKey string
		var oldestTime time.Time
		first := true
		for k, v := range s.sessions {
			if first || v.touchedAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.touchedAt
				first = false
			}
		}
		if oldestKey == "" {
			break
		}
		delete(s.sessions, oldestKey)
	}
}

func (s *agentManageUIState) pruneIfEmptyLocked(sessionID string) {
	if cs := s.sessions[sessionID]; cs != nil && cs.isEmpty() {
		delete(s.sessions, sessionID)
	}
}

func (c *AppController) agentWorkspaceFilter() string {
	st := agentManageStateFor(c)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.workspaceFilter
}

func (c *AppController) SetAgentWorkspaceFilter(wsID string) {
	st := agentManageStateFor(c)
	st.mu.Lock()
	if st.workspaceFilter == wsID {
		st.workspaceFilter = ""
	} else {
		st.workspaceFilter = wsID
	}
	st.mu.Unlock()
	c.mu.Lock()
	c.uiState.agentSelected = 0
	c.mu.Unlock()
}

// FilterAgentItemsByWorkspace filters visible TabAgent items by workspaceID.
func FilterAgentItemsByWorkspace(items []engine.OmniboxItem, sessions []agentsession.Session, wsID string) []engine.OmniboxItem {
	wsID = strings.TrimSpace(wsID)
	if wsID == "" {
		return items
	}
	filtered := make([]engine.OmniboxItem, 0, len(items))
	for _, item := range items {
		if session, ok := agentSessionForItem(item, sessions); ok {
			if session.WorkspaceID == wsID || strings.EqualFold(session.WorkspaceLabel, wsID) {
				filtered = append(filtered, item)
			}
			continue
		}
		if strings.HasPrefix(item.UtilityValue, "agent-command:") || item.UtilityValue == "agent-refresh-status" {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (c *AppController) triggerAgentManageRepaint() {
	c.mu.Lock()
	repaint := c.OnRepaint
	c.mu.Unlock()
	if repaint != nil {
		repaint()
	}
}

func (c *AppController) setAgentManageStatus(msg string, isErr bool) {
	st := agentManageStateFor(c)
	st.mu.Lock()
	st.statusMsg = msg
	st.statusIsErr = isErr
	st.mu.Unlock()
	c.triggerAgentManageRepaint()
}

func (c *AppController) setAgentSessionStatus(target, msg string, isErr bool) {
	key := c.resolveAgentControlKey(target)
	if key == "" {
		c.setAgentManageStatus(msg, isErr)
		return
	}
	st := agentManageStateFor(c)
	st.mu.Lock()
	cs := st.sessionLocked(key, true)
	cs.statusMsg = msg
	cs.statusIsErr = isErr
	st.pruneIfEmptyLocked(key)
	st.mu.Unlock()
	c.triggerAgentManageRepaint()
}

// FocusHerdrWorkspace focuses a Herdr workspace synchronously or async.
func (c *AppController) FocusHerdrWorkspace(workspaceID string) (host.OperationResult, error) {
	ctx, cancel := context.WithTimeout(c.lifeCtx(), 2*time.Second)
	defer cancel()
	hc := agentsession.NewHerdrController(c.currentHerdrExecutor())
	res, err := hc.FocusWorkspace(ctx, workspaceID)
	if err != nil {
		c.setAgentManageStatus(nonEmptyOr(res.Message, err.Error()), true)
		c.ShowToastOrHUD(nonEmptyOr(res.Message, err.Error()))
		return res, err
	}
	c.setAgentManageStatus(pickLang(i18n.IsChinese(), "已切换工作区 "+workspaceID, "Focused workspace "+workspaceID), false)
	return res, nil
}

// RenameHerdrAgent renames an agent pane and refreshes discovery.
func (c *AppController) RenameHerdrAgent(target, newName string) (host.OperationResult, error) {
	ctx, cancel := context.WithTimeout(c.lifeCtx(), 2*time.Second)
	defer cancel()
	key := c.resolveAgentControlKey(target)
	hc := agentsession.NewHerdrController(c.currentHerdrExecutor())
	res, err := hc.RenameAgent(ctx, target, newName)
	if err != nil {
		c.setAgentSessionStatus(key, nonEmptyOr(res.Message, err.Error()), true)
		return res, err
	}
	st := agentManageStateFor(c)
	st.mu.Lock()
	if cs := st.sessionLocked(key, false); cs != nil {
		cs.renameOpen = false
		cs.renameDraft = ""
		st.pruneIfEmptyLocked(key)
	}
	st.mu.Unlock()
	c.setAgentSessionStatus(key, pickLang(i18n.IsChinese(), "已重命名为 "+newName, "Renamed to "+newName), false)
	c.AgentRefresh()
	return res, nil
}

// SendHerdrPrompt sends a gated prompt to target (`confirmed == true` required).
func (c *AppController) SendHerdrPrompt(target, message string, confirmed bool) (host.OperationResult, error) {
	ctx, cancel := context.WithTimeout(c.lifeCtx(), 3*time.Second)
	defer cancel()
	key := c.resolveAgentControlKey(target)
	hc := agentsession.NewHerdrController(c.currentHerdrExecutor())
	res, err := hc.SendPrompt(ctx, agentsession.SendPromptRequest{
		Target:    target,
		Message:   message,
		Confirmed: confirmed,
	})
	st := agentManageStateFor(c)
	st.mu.Lock()
	if cs := st.sessionLocked(key, false); cs != nil {
		cs.confirmArmed = false
		if err == nil {
			cs.promptOpen = false
			cs.promptDraft = ""
		}
		st.pruneIfEmptyLocked(key)
	}
	st.mu.Unlock()

	if err != nil {
		c.setAgentSessionStatus(key, nonEmptyOr(res.Message, err.Error()), true)
		return res, err
	}
	c.setAgentSessionStatus(key, pickLang(i18n.IsChinese(), "指令已发送至终端", "Prompt delivered"), false)
	c.ForceRefreshAgentOutput(target)
	return res, nil
}

// StartHerdrAgent starts an allowlisted AI CLI inside a Herdr workspace.
func (c *AppController) StartHerdrAgent(workspace, cli, name string) (host.OperationResult, error) {
	ctx, cancel := context.WithTimeout(c.lifeCtx(), 4*time.Second)
	defer cancel()
	hc := agentsession.NewHerdrController(c.currentHerdrExecutor())
	res, err := hc.StartAgent(ctx, workspace, cli, name)
	if err != nil {
		c.setAgentManageStatus(nonEmptyOr(res.Message, err.Error()), true)
		return res, err
	}
	st := agentManageStateFor(c)
	st.mu.Lock()
	st.startOpen = false
	st.startName = ""
	st.mu.Unlock()
	c.setAgentManageStatus(pickLang(i18n.IsChinese(), "已启动 "+cli, "Started "+cli), false)
	c.AgentRefresh()
	return res, nil
}

// WatchHerdrAgentCompletion starts a background wait for idle completion and notifies via HUD.
func (c *AppController) WatchHerdrAgentCompletion(target, title string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return
	}
	st := agentManageStateFor(c)
	st.mu.Lock()
	if st.watching[target] {
		st.mu.Unlock()
		return
	}
	st.watching[target] = true
	st.mu.Unlock()

	lbl := nonEmptyOr(strings.TrimSpace(title), target)
	regCtx, done := c.ensureRuntime().Begin(runtimecoord.AgentWait, "Agent wait "+lbl)
	exec := c.currentHerdrExecutor()
	go func() {
		defer done()
		defer func() {
			st.mu.Lock()
			delete(st.watching, target)
			st.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(regCtx, 60*time.Second)
		defer cancel()
		res, err := agentsession.NewHerdrController(exec).WaitState(ctx, target, "idle", 45*time.Second)
		if err != nil {
			if regCtx.Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			c.ShowToastOrHUD(nonEmptyOr(res.Message, err.Error()))
			return
		}
		if regCtx.Err() != nil {
			return
		}
		c.ShowToastOrHUD(pickLang(i18n.IsChinese(), "Agent "+lbl+" 已完成当前回合", "Agent "+lbl+" finished turn"))
		c.AgentRefresh()
		c.ForceRefreshAgentOutput(target)
	}()
}

func (a *AppController) renderAgentWorkspaceStrip(c *ui.Context, theme *ui.Theme) {
	workspaces := a.cachedAgentWorkspaces()
	zh := i18n.IsChinese()
	st := agentManageStateFor(a)
	st.mu.Lock()
	activeWS := st.workspaceFilter
	startOpen := st.startOpen
	startCLI := st.startCLI
	startName := st.startName
	wsStatusMsg := st.statusMsg
	wsStatusErr := st.statusIsErr
	st.mu.Unlock()

	if len(workspaces) == 0 && !startOpen && wsStatusMsg == "" {
		return
	}

	ui.Column(c).Key("agent-workspace-strip").FillWidth().Gap(space4).Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
			for _, ws := range workspaces {
				wCopy := ws
				selected := activeWS == wCopy.WorkspaceID
				lbl := nonEmptyOr(wCopy.Label, wCopy.WorkspaceID)
				b := ui.ButtonBase(c).Key("agent-ws:"+wCopy.WorkspaceID).Label(lbl).Padding(space2, space6).Radius(radiusChip).FocusRing(false)
				if selected {
					b.Background(theme.Accent.Alpha(alphaSubtle)).Border(space1, theme.Accent.Alpha(alphaHalf))
				} else {
					b.Background(tileFill(theme)).Border(space1, hairline(theme))
				}
				fg := secondaryInk(theme)
				if selected {
					fg = accentInk(theme, surfaceBase(theme))
				}
				b.Children(func() {
					ui.Text(c, lbl).FontSize(typeMicro).FontWeight(600).TextColor(fg).SingleLine()
				})
				if b.Clicked() {
					a.SetAgentWorkspaceFilter(wCopy.WorkspaceID)
				}
			}

			ui.Spacer(c)

			startLbl := pickLang(zh, "+ 启动", "+ Start")
			if startOpen {
				startLbl = pickLang(zh, "收起", "Close")
			}
			sb := ui.ButtonBase(c).Key("agent-start-toggle").Label(startLbl).Padding(space2, space6).
				Radius(radiusChip).Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false)
			sb.Children(func() {
				ui.Text(c, startLbl).FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
			})
			if sb.Clicked() {
				st.mu.Lock()
				st.startOpen = !st.startOpen
				st.mu.Unlock()
			}
		})

		if startOpen {
			kitCard(c, theme, cardInset, func() {
				ui.Column(c).FillWidth().Padding(space6, space8).Gap(space4).Children(func() {
					ui.Row(c).FillWidth().Gap(space4).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "CLI:").FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
						for _, cli := range []string{"codex", "claude", "pi", "agy"} {
							cliOpt := cli
							active := startCLI == cliOpt
							b := ui.ButtonBase(c).Key("agent-cli-"+cliOpt).Label(cliOpt).Padding(space2, space6).Radius(radius4).FocusRing(false)
							if active {
								b.Background(theme.Accent.Alpha(alphaSubtle)).Border(space1, theme.Accent.Alpha(alphaHalf))
							} else {
								b.Background(theme.Surface).Border(space1, hairline(theme))
							}
							fg := secondaryInk(theme)
							if active {
								fg = accentInk(theme, surfaceBase(theme))
							}
							b.Children(func() {
								ui.Text(c, cliOpt).FontSize(typeMicro).FontWeight(600).TextColor(fg).SingleLine()
							})
							if b.Clicked() {
								st.mu.Lock()
								st.startCLI = cliOpt
								st.mu.Unlock()
							}
						}
					})
					ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
						nmIn := ui.TextInput(c.Key("agent-start-name"), &startName).
							Placeholder(pickLang(zh, "可选名称 (例: fe-helper)", "Optional name")).Grow(1)
						if nmIn.Changed() {
							st.mu.Lock()
							st.startName = startName
							st.mu.Unlock()
						}
						goLbl := pickLang(zh, "启动 Agent", "Launch")
						if ui.Button(c, goLbl).Key("agent-start-launch").Label(goLbl).Padding(space2, space8).Clicked() {
							wsTarget := activeWS
							if wsTarget == "" && len(workspaces) > 0 {
								wsTarget = workspaces[0].WorkspaceID
							}
							go a.StartHerdrAgent(wsTarget, startCLI, startName)
						}
					})
				})
			})
		}
		if wsStatusMsg != "" {
			if wsStatusErr {
				kitWarningBanner(c, theme, wsStatusMsg)
			} else {
				ui.Text(c, wsStatusMsg).FontSize(typeMicro).TextColor(theme.TextMuted).Wrap()
			}
		}
	})
}

func (a *AppController) renderAgentManageControls(c *ui.Context, theme *ui.Theme, session agentsession.Session) {
	if !session.HerdrManaged || strings.TrimSpace(session.PaneID) == "" {
		return
	}
	zh := i18n.IsChinese()
	target := strings.TrimSpace(session.PaneID)
	sessionKey := agentSessionControlKey(session)
	st := agentManageStateFor(a)
	st.mu.Lock()
	var (
		renameOpen   bool
		renameDraft  string
		promptOpen   bool
		promptDraft  string
		confirmArmed bool
		statusMsg    string
		statusErr    bool
	)
	if cs := st.sessionLocked(sessionKey, false); cs != nil {
		renameOpen = cs.renameOpen
		renameDraft = cs.renameDraft
		promptOpen = cs.promptOpen
		promptDraft = cs.promptDraft
		confirmArmed = cs.confirmArmed
		statusMsg = cs.statusMsg
		statusErr = cs.statusIsErr
	}
	watching := st.watching[target]
	st.mu.Unlock()

	ui.Column(c).Key("agent-manage-controls:" + sessionKey).FillWidth().Gap(space4).Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
			actionBtn := func(key, label string, active bool, fn func()) {
				b := ui.ButtonBase(c).Key(key + ":" + sessionKey).Label(label).Padding(space2, space6).Radius(radius4).FocusRing(false)
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
					ui.Text(c, label).FontSize(typeMicro).FontWeight(600).TextColor(fg).SingleLine()
				})
				if b.Clicked() {
					fn()
				}
			}

			if session.WorkspaceID != "" {
				actionBtn("agent-focus-ws", pickLang(zh, "切工作区", "Focus WS"), false, func() {
					go a.FocusHerdrWorkspace(session.WorkspaceID)
				})
			}
			actionBtn("agent-rename-toggle", pickLang(zh, "重命名", "Rename"), renameOpen, func() {
				st.mu.Lock()
				cs := st.sessionLocked(sessionKey, true)
				cs.renameOpen = !cs.renameOpen
				if cs.renameOpen && cs.renameDraft == "" {
					cs.renameDraft = nonEmptyOr(session.AgentName, session.Title)
				}
				st.pruneIfEmptyLocked(sessionKey)
				st.mu.Unlock()
			})
			watchLbl := pickLang(zh, "完成后提醒", "Watch Done")
			if watching {
				watchLbl = pickLang(zh, "监听中…", "Watching…")
			}
			actionBtn("agent-watch-done", watchLbl, watching, func() {
				a.WatchHerdrAgentCompletion(target, session.Title)
			})
			actionBtn("agent-send-toggle", pickLang(zh, "发送指令", "Send Prompt"), promptOpen, func() {
				st.mu.Lock()
				cs := st.sessionLocked(sessionKey, true)
				cs.promptOpen = !cs.promptOpen
				cs.confirmArmed = false
				st.pruneIfEmptyLocked(sessionKey)
				st.mu.Unlock()
			})
		})

		if statusMsg != "" {
			if statusErr {
				kitWarningBanner(c, theme, statusMsg)
			} else {
				ui.Text(c, statusMsg).FontSize(typeMicro).TextColor(theme.TextMuted).Wrap()
			}
		}

		if renameOpen {
			ui.Row(c).FillWidth().Gap(space4).AlignItems(ui.Center).Children(func() {
				in := ui.TextInput(c.Key("agent-rename-input:"+sessionKey), &renameDraft).
					Placeholder(pickLang(zh, "输入新名称", "New agent name")).Grow(1)
				if in.Changed() {
					st.mu.Lock()
					cs := st.sessionLocked(sessionKey, true)
					cs.renameDraft = renameDraft
					st.mu.Unlock()
				}
				applyLbl := pickLang(zh, "应用", "Apply")
				if ui.Button(c, applyLbl).Key("agent-rename-apply:"+sessionKey).Label(applyLbl).Padding(space2, space8).Clicked() {
					go a.RenameHerdrAgent(target, renameDraft)
				}
			})
		}

		if promptOpen {
			kitCard(c, theme, cardInset, func() {
				ui.Column(c).FillWidth().Padding(space6, space8).Gap(space4).Children(func() {
					ui.Row(c).FillWidth().Wrap().Gap(space4).Children(func() {
						for _, preset := range []string{"继续并修复", "总结当前改动", "仅只读检查测试"} {
							pCopy := preset
							pb := ui.ButtonBase(c).Key("agent-preset-"+pCopy+":"+sessionKey).Label(pCopy).Padding(space2, space6).Radius(radius4).
								Background(theme.Surface).Border(space1, hairline(theme)).FocusRing(false)
							pb.Children(func() {
								ui.Text(c, pCopy).FontSize(typeMicro).TextColor(secondaryInk(theme)).SingleLine()
							})
							if pb.Clicked() {
								st.mu.Lock()
								cs := st.sessionLocked(sessionKey, true)
								cs.promptDraft = pCopy
								cs.confirmArmed = false
								st.mu.Unlock()
							}
						}
					})
					in := ui.TextInput(c.Key("agent-prompt-input:"+sessionKey), &promptDraft).
						Placeholder(pickLang(zh, "输入终端指令（需二次确认）", "Prompt text (requires confirmation)")).FillWidth()
					if in.Changed() {
						st.mu.Lock()
						cs := st.sessionLocked(sessionKey, true)
						cs.promptDraft = promptDraft
						cs.confirmArmed = false
						st.mu.Unlock()
					}
					ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, pickLang(zh, "目标: "+target, "Target: "+target)).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine().Grow(1)
						if !confirmArmed {
							armLbl := pickLang(zh, "准备发送", "Prepare Send")
							if ui.Button(c, armLbl).Key("agent-send-arm:"+sessionKey).Label(armLbl).Padding(space2, space8).Clicked() {
								if strings.TrimSpace(promptDraft) != "" {
									st.mu.Lock()
									cs := st.sessionLocked(sessionKey, true)
									cs.confirmArmed = true
									st.mu.Unlock()
								}
							}
						} else {
							confirmLbl := pickLang(zh, "确认发送到终端", "Confirm Send")
							if ui.PrimaryButton(c, confirmLbl).Key("agent-send-confirm:"+sessionKey).Label(confirmLbl).Padding(space2, space10).Clicked() {
								go a.SendHerdrPrompt(target, promptDraft, true)
							}
						}
					})
				})
			})
		}
	})
}
