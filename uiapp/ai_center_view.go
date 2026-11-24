package uiapp

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// aiInteractionPhase models the five explicit, non-percentage states of the AI
// Interaction Center, each rendered with a semantic symbol + text label.
type aiInteractionPhase string

const (
	aiPhasePending       aiInteractionPhase = "pending"
	aiPhaseRunning       aiInteractionPhase = "running"
	aiPhaseAwaitingHuman aiInteractionPhase = "awaiting_human"
	aiPhaseDone          aiInteractionPhase = "done"
	aiPhaseFailed        aiInteractionPhase = "failed"
)

type aiStepStatus uint8

const (
	aiStepPending aiStepStatus = iota
	aiStepActive
	aiStepDone
	aiStepApproval
	aiStepError
)

type aiStepLogEntry struct {
	Index  int
	Key    string
	Title  string
	Detail string
	Status aiStepStatus
}

// resolveAIInteractionPhase maps real engine.State and aiReview eligibility to
// the 5 explicit interaction states without fabricating progress percentages.
func resolveAIInteractionPhase(state engine.State, hasReview bool) aiInteractionPhase {
	switch {
	case state.AIError != "":
		return aiPhaseFailed
	case !state.AIComplete:
		if strings.TrimSpace(state.AIPrompt) == "" && state.AIOutput == "" && state.AISourceText == "" {
			return aiPhasePending
		}
		return aiPhaseRunning
	case hasReview:
		return aiPhaseAwaitingHuman
	case state.AIOutput == "" && strings.TrimSpace(state.AIPrompt) == "":
		return aiPhasePending
	default:
		return aiPhaseDone
	}
}

// aiCenterPhaseStatus returns the header status pill icon, text, and tone for
// each of the 5 AI interaction states.
func (a *AppController) aiCenterPhaseStatus(state engine.State) (string, chipTone) {
	_, hasReview := a.aiReview(state)
	phase := resolveAIInteractionPhase(state, hasReview)
	return aiPhaseBadgeLabel(phase), aiPhaseChipTone(phase)
}

func aiPhaseBadgeLabel(phase aiInteractionPhase) string {
	zh := i18n.IsChinese()
	switch phase {
	case aiPhasePending:
		if zh {
			return "○ 待开始"
		}
		return "○ Pending"
	case aiPhaseRunning:
		if zh {
			return "● " + i18n.T("ai.status.streaming")
		}
		return "● " + i18n.T("ai.status.streaming")
	case aiPhaseAwaitingHuman:
		if zh {
			return "▲ 待人工确认"
		}
		return "▲ Awaiting approval"
	case aiPhaseFailed:
		if zh {
			return "✕ " + i18n.T("common.error")
		}
		return "✕ " + i18n.T("common.error")
	default:
		if zh {
			return "✓ " + i18n.T("common.done")
		}
		return "✓ " + i18n.T("common.done")
	}
}

func aiPhaseChipTone(phase aiInteractionPhase) chipTone {
	switch phase {
	case aiPhaseRunning:
		return chipAccent
	case aiPhaseAwaitingHuman:
		return chipWarning
	case aiPhaseFailed:
		return chipDanger
	case aiPhaseDone:
		return chipSuccess
	default:
		return chipNeutral
	}
}

// aiStepLog derives countable, truthful step entries strictly from real runtime
// state (prompt/source capture, active model streaming, output verification).
// No fake tool calls, web sources, or estimated costs are ever invented.
func aiStepLog(state engine.State, activeProf config.AIProfile, hasReview bool) []aiStepLogEntry {
	zh := i18n.IsChinese()
	phase := resolveAIInteractionPhase(state, hasReview)

	// Step 1: Prompt & context capture
	ctxParts := make([]string, 0, 2)
	if srcLen := utf8.RuneCountInString(strings.TrimSpace(state.AISourceText)); srcLen > 0 {
		if zh {
			ctxParts = append(ctxParts, fmt.Sprintf("选中文本 %d 字", srcLen))
		} else {
			ctxParts = append(ctxParts, fmt.Sprintf("Selection %d chars", srcLen))
		}
	}
	if hist := aiDisplayHistory(state.AIHistory); len(hist) > 0 {
		turns := aiTurnCount(hist)
		if zh {
			ctxParts = append(ctxParts, fmt.Sprintf("第 %d 轮对话", turns))
		} else {
			ctxParts = append(ctxParts, fmt.Sprintf("Turn %d", turns))
		}
	}
	step1Status := aiStepDone
	if phase == aiPhasePending {
		step1Status = aiStepPending
	}
	step1Title := "Capture prompt & context"
	if zh {
		step1Title = "采集提问与上下文"
	}

	// Step 2: Model streaming
	modelName := strings.TrimSpace(activeProf.Model)
	if modelName == "" {
		modelName = strings.TrimSpace(activeProf.ID)
	}
	outChars := utf8.RuneCountInString(strings.TrimSpace(state.AIOutput))
	step2Title := "Model stream generation"
	if zh {
		step2Title = "模型流式生成"
	}
	step2Detail := modelName
	step2Status := aiStepPending
	switch phase {
	case aiPhasePending:
		step2Status = aiStepPending
	case aiPhaseRunning:
		step2Status = aiStepActive
		if outChars == 0 {
			if zh {
				step2Detail = joinNonEmpty(" · ", modelName, "等待首个输出分片")
			} else {
				step2Detail = joinNonEmpty(" · ", modelName, "Waiting for first chunk")
			}
		} else {
			lines := previewLineCount(state.AIOutput)
			if zh {
				step2Detail = joinNonEmpty(" · ", modelName, fmt.Sprintf("已接收 %d 字 / %d 行", outChars, lines))
			} else {
				step2Detail = joinNonEmpty(" · ", modelName, fmt.Sprintf("%d chars / %d lines", outChars, lines))
			}
		}
	case aiPhaseFailed:
		step2Status = aiStepError
		if zh {
			step2Detail = joinNonEmpty(" · ", modelName, "请求中断或失败")
		} else {
			step2Detail = joinNonEmpty(" · ", modelName, "Request interrupted or failed")
		}
	default:
		step2Status = aiStepDone
		if outChars > 0 {
			if zh {
				step2Detail = joinNonEmpty(" · ", modelName, fmt.Sprintf("共 %d 字", outChars))
			} else {
				step2Detail = joinNonEmpty(" · ", modelName, fmt.Sprintf("%d chars", outChars))
			}
		}
	}

	// Step 3: Verification / Human approval / Delivery
	step3Title := "Verify & deliver result"
	if zh {
		step3Title = "结果校验与交付"
	}
	step3Detail := ""
	step3Status := aiStepPending
	switch phase {
	case aiPhaseAwaitingHuman:
		step3Status = aiStepApproval
		if zh {
			step3Title = "等待人工确认替换"
			if app := strings.TrimSpace(state.Context.AppName); app != "" {
				step3Detail = "目标应用：" + app
			}
		} else {
			step3Title = "Awaiting human approval"
			if app := strings.TrimSpace(state.Context.AppName); app != "" {
				step3Detail = "Target: " + app
			}
		}
	case aiPhaseDone:
		step3Status = aiStepDone
		if zh {
			step3Detail = "可替换、复制或继续追问"
		} else {
			step3Detail = "Ready to replace, copy, or follow up"
		}
	}

	return []aiStepLogEntry{
		{Index: 1, Key: "context", Title: step1Title, Detail: strings.Join(ctxParts, " · "), Status: step1Status},
		{Index: 2, Key: "stream", Title: step2Title, Detail: step2Detail, Status: step2Status},
		{Index: 3, Key: "deliver", Title: step3Title, Detail: step3Detail, Status: step3Status},
	}
}

func joinNonEmpty(sep string, parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, sep)
}

func aiRetryPrompt(state engine.State) string {
	if p := strings.TrimSpace(state.AIPrompt); p != "" {
		return p
	}
	for i := len(state.AIHistory) - 1; i >= 0; i-- {
		if !strings.EqualFold(state.AIHistory[i].Role, "assistant") {
			if p := strings.TrimSpace(state.AIHistory[i].Content); p != "" {
				return p
			}
		}
	}
	return strings.TrimSpace(aiModeLabel(state.AIMode))
}

// aiDisplayHistory removes consecutive duplicate user turns that can occur when
// EventFollowUpAI retries a failed turn with the same prompt.
func aiDisplayHistory(history []engine.ChatTurn) []engine.ChatTurn {
	if len(history) <= 1 {
		return history
	}
	out := make([]engine.ChatTurn, 0, len(history))
	for _, turn := range history {
		if len(out) > 0 {
			prev := out[len(out)-1]
			if strings.EqualFold(prev.Role, turn.Role) && strings.TrimSpace(prev.Content) == strings.TrimSpace(turn.Content) {
				continue
			}
		}
		out = append(out, turn)
	}
	return out
}

func aiTurnCount(history []engine.ChatTurn) int {
	turns := 1
	for _, h := range history {
		if strings.EqualFold(h.Role, "assistant") {
			turns++
		}
	}
	return turns
}

// stopActiveAIStream cancels the live HTTP stream and transitions cleanly into
// the failed/stopped state without dismissing the AI Interaction Center so the
// user can inspect, edit their prompt, switch profile, or retry immediately.
func (a *AppController) stopActiveAIStream() {
	a.cancelAIStream()
	msg := "Generation stopped"
	if i18n.IsChinese() {
		msg = "已手动停止生成"
	}
	a.Dispatch(engine.Event{
		Type:  engine.EventAIFailed,
		Error: msg,
	})
}

func (a *AppController) submitAIFollowUpOrRetry(state engine.State, prompt string) bool {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		trimmed = aiRetryPrompt(state)
	}
	if trimmed == "" {
		return false
	}
	a.uiState.followUpQuery = ""
	capsuleUI.setReviseOpen(a, false)
	a.Dispatch(engine.Event{Type: engine.EventFollowUpAI, Prompt: trimmed})
	return true
}

func (a *AppController) renderAIStreamingView(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig, state engine.State) {
	profile := appMotionProfile(c, cfg.Main.Appearance)
	review, hasReview := a.aiReview(state)
	phase := resolveAIInteractionPhase(state, hasReview)
	activeProf := a.ActiveAIProfile()

	// Window-level keyboard shortcuts; IME preedit always wins for unmodified keys.
	c.OnWindowShortcut(0, ui.KeyEscape, func() {
		if a.followUpComposing() {
			return
		}
		if a.composerDismissActiveMenu(cfg, state) {
			return
		}
		a.Dispatch(engine.Event{Type: engine.EventEscape})
	})
	// Cycle AI profiles with ⌘⇧M / Ctrl+Shift+M.
	c.OnWindowShortcut(primaryMod()|ui.Shift, ui.KeyM, func() {
		if nextID := nextAIProfileID(a.Config().Main.AI, a.ActiveAIProfile().ID); nextID != "" {
			_ = a.SelectAIProfile(nextID)
		}
	})
	if phase == aiPhaseRunning {
		c.OnWindowShortcut(primaryMod(), ui.KeyPeriod, func() {
			a.stopActiveAIStream()
		})
	}
	if phase == aiPhaseDone || phase == aiPhaseFailed || phase == aiPhasePending || phase == aiPhaseAwaitingHuman {
		c.OnWindowShortcut(0, ui.KeyTab, func() {
			if a.followUpComposing() {
				return
			}
			if a.composerTryAcceptCompletion(cfg, state) {
				a.uiState.followUpHandle.RequestFocus(c)
				return
			}
			if phase == aiPhaseAwaitingHuman {
				capsuleUI.setReviseOpen(a, true)
			}
			a.uiState.followUpHandle.RequestFocus(c)
		})
	}
	if phase == aiPhaseDone || phase == aiPhaseFailed || phase == aiPhaseAwaitingHuman {
		c.OnWindowShortcut(primaryMod(), ui.KeyR, func() {
			if a.followUpComposing() {
				return
			}
			a.submitAIFollowUpOrRetry(state, a.uiState.followUpQuery)
		})
	}
	if state.AIError == "" && state.AIOutput != "" {
		c.OnWindowShortcut(0, ui.KeyEnter, func() {
			if a.followUpComposing() {
				return
			}
			if a.composerTryAcceptCompletion(cfg, state) {
				a.uiState.followUpHandle.RequestFocus(c)
				return
			}
			trimmed := strings.TrimSpace(a.uiState.followUpQuery)
			if trimmed != "" {
				a.recordComposerHistory(trimmed)
				a.submitAIFollowUpOrRetry(state, a.composePromptWithChips(trimmed))
				return
			}
			a.Dispatch(engine.Event{Type: engine.EventAcceptAIOutput})
		})
	} else if phase == aiPhaseFailed || phase == aiPhasePending {
		c.OnWindowShortcut(0, ui.KeyEnter, func() {
			if a.followUpComposing() {
				return
			}
			if a.composerTryAcceptCompletion(cfg, state) {
				a.uiState.followUpHandle.RequestFocus(c)
				return
			}
			a.submitAIFollowUpOrRetry(state, a.composePromptWithChips(a.uiState.followUpQuery))
		})
	}
	if state.AIOutput != "" {
		c.OnWindowShortcut(primaryMod(), ui.KeyC, func() {
			a.Dispatch(engine.Event{Type: engine.EventCopyAIOutput})
		})
	}
	if hasReview {
		c.OnWindowShortcut(primaryMod(), ui.KeyD, func() {
			a.toggleAIReview(review)
		})
	}

	steps := aiStepLog(state, activeProf, hasReview)

	ui.Column(c).FillWidth().Grow(1).Padding(space10, space14).Gap(space8).Children(func() {
		a.renderAICenterTopStrip(c, theme, cfg, activeProf)
		a.renderOCRTranslateBar(c, theme, cfg, state)
		a.renderAICenterPromptAndContext(c, theme, state, phase)

		// Process visibility discipline:
		// - running / failed: show countable step log directly so progress or failure point is unmistakable.
		// - done / awaiting_human: collapse process by default to keep result & next step front and center;
		//   clicking the compact summary chip expands the step log on demand.
		switch phase {
		case aiPhaseRunning, aiPhaseFailed:
			a.renderAICenterStepLog(c, theme, steps, phase)
		case aiPhaseDone, aiPhaseAwaitingHuman:
			a.renderAICenterCollapsedStepsToggle(c, theme, steps, phase)
		}

		a.renderAICenterHistory(c, theme, state)

		if hasReview {
			a.renderAIReviewToggle(c, theme, review)
		}

		// Main response / diff / loading / error / pending viewport.
		applyProseLineHeight(ui.Scroll(c).
			FillWidth().
			Grow(1).
			MinHeight(0).
			Padding(space10, space12).
			Radius(radiusCard).
			Background(theme.Surface).
			Border(space1, theme.Border)).
			Children(func() {
				switch phase {
				case aiPhasePending:
					title := "Ready for your question"
					body := "Type a multi-line prompt below and press Enter to send (Shift+Enter for newline)."
					if i18n.IsChinese() {
						title = "待开始：输入你的问题"
						body = "在下方多行输入框输入提问，按 Enter 发送（Shift+Enter 换行）。"
					}
					kitStateView(c, theme, kitStateEmpty, "sparkles", title, body, nil)
				case aiPhaseFailed:
					// Two quick jolts (2 × 60ms) whenever a new error lands.
					if state.AIError != a.uiState.lastAIError {
						a.uiState.lastAIError = state.AIError
						a.uiState.aiShakeRun++
					}
					errBox := ui.Column(c).FillWidth().Children(func() {
						kitStateView(c, theme, kitStateError, "exclamationmark.circle", i18n.T("common.error"), state.AIError, func() {
							a.submitAIFollowUpOrRetry(state, a.uiState.followUpQuery)
						})
					})
					if frames := shakeKeyframes(profile); frames != nil {
						jolt := errBox.Keyframes("ai-error-shake", frames, ui.KeyframeOptions{Run: a.uiState.aiShakeRun})
						errBox.Translate(jolt*3, 0)
					}
				case aiPhaseRunning:
					if state.AIOutput == "" {
						modelHint := activeProf.Model
						kitStateView(c, theme, kitStateLoading, "arrow.clockwise", i18n.T("ai.generating"), modelHint, nil)
					} else {
						a.renderClavisMarkdown(c, theme, state.AIOutput, true)
						// Pulsing caret while the answer streams in (900ms loop).
						caret := ui.Box(c).Width(7).Height(15).Radius(radius4)
						caret.Background(theme.Accent.Alpha(cursorOpacity(caret, profile)))
					}
				case aiPhaseAwaitingHuman:
					if a.aiReviewShowsChanges(review) {
						renderAIReviewChanges(c, theme, review)
					} else {
						a.renderClavisMarkdown(c, theme, state.AIOutput, false)
					}
				default:
					a.renderClavisMarkdown(c, theme, state.AIOutput, false)
				}
			})

		// Blocking human-approval action zone for awaiting_human, or upgraded
		// multi-line Input Composer for pending / running / done / failed
		// (and on-demand revision in awaiting_human).
		if phase == aiPhaseAwaitingHuman {
			a.renderAICenterBlockingApprovalGate(c, theme, state, review)
			if capsuleUI.reviseOpen(a) {
				a.renderInputComposer(c, theme, cfg, state, phase)
			}
			return
		}

		a.renderInputComposer(c, theme, cfg, state, phase)

		ui.Divider(c)
		a.renderAICenterFooter(c, theme, state, phase)
	})
}

func (a *AppController) renderAICenterTopStrip(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig, active config.AIProfile) {
	profiles := cfg.Main.AI.Profiles
	usageDays, usageErr, usageLoaded, _ := a.aiUsageSnapshot()
	hasUsage := usageLoaded && usageErr == nil && aiUsageTotalRequests(usageDays) > 0
	if len(profiles) <= 1 && !hasUsage {
		return
	}
	ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
		if len(profiles) > 1 {
			ui.Row(c).Wrap().Gap(space6).AlignItems(ui.Center).Grow(1).Children(func() {
				for _, p := range profiles {
					prof := p
					isActive := strings.EqualFold(prof.ID, active.ID)
					button := ui.ButtonBase(c).Key("ai-profile:"+prof.ID).Height(space24).Padding(space2, space10).Radius(radiusChip).FocusRing(false)
					ink := secondaryInk(theme)
					if isActive {
						button.Background(theme.Accent)
						ink = theme.AccentText
					} else {
						button.Background(tileFill(theme)).Border(space1, hairline(theme))
					}
					button.Children(func() {
						ui.Text(c, prof.ID).FontSize(typeMeta).FontWeight(600).TextColor(ink).SingleLine()
					})
					if button.Clicked() {
						_ = a.SelectAIProfile(prof.ID)
					}
				}
			})
		} else {
			ui.Spacer(c)
		}
		if hasUsage {
			usageLabel := "Today " + aiUsageTodayLabel(usageDays)
			if i18n.IsChinese() {
				usageLabel = "今日 " + aiUsageTodayLabel(usageDays)
			}
			ui.Box(c).Key("ai-center-usage").Padding(space2, space8).Radius(radiusPill).Background(tileFill(theme)).Border(space1, hairline(theme)).Shrink(0).Children(func() {
				ui.Text(c, usageLabel).FontSize(typeMicro).FontWeight(600).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
			})
		}
	})
}

func (a *AppController) renderAICenterStepLog(c *ui.Context, theme *ui.Theme, steps []aiStepLogEntry, phase aiInteractionPhase) {
	ui.Column(c).Key("ai-step-log").Label("ai-step-log").FillWidth().Padding(space6, space8).Gap(space4).
		Radius(radius8).Background(tileFill(theme)).Border(space1, hairline(theme)).Children(func() {
		for _, step := range steps {
			renderAIStepLogRow(c, theme, step, phase == aiPhaseRunning)
		}
	})
}

func (a *AppController) renderAICenterCollapsedStepsToggle(c *ui.Context, theme *ui.Theme, steps []aiStepLogEntry, phase aiInteractionPhase) {
	expanded := capsuleUI.stepsExpanded(a)
	completedCount := 0
	for _, s := range steps {
		if s.Status == aiStepDone {
			completedCount++
		}
	}
	summary := fmt.Sprintf("Process · %d/%d steps", completedCount, len(steps))
	if i18n.IsChinese() {
		summary = fmt.Sprintf("执行过程 · %d/%d 步", completedCount, len(steps))
	}
	chevron := "▸"
	if expanded {
		chevron = "▾"
	}
	ui.Column(c).FillWidth().Gap(space4).Children(func() {
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			toggle := ui.ButtonBase(c).Key("ai-steps-toggle").Label(summary).Padding(space2, space8).Radius(radiusChip).
				Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false)
			toggle.Children(func() {
				ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
					icon := "checkmark"
					iconColor := theme.Success
					if phase == aiPhaseAwaitingHuman {
						icon = "exclamationmark.triangle"
						iconColor = kitStateWarningColor(theme)
					}
					renderSemanticIcon(c, icon, typeMicro, iconColor)
					ui.Text(c, summary).FontSize(typeMicro).FontWeight(600).FontFeatures("tnum").TextColor(secondaryInk(theme)).SingleLine()
					ui.Text(c, chevron).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
				})
			})
			if toggle.Clicked() {
				capsuleUI.toggleStepsExpanded(a)
			}
			ui.Spacer(c)
			if turns := aiTurnCount(aiDisplayHistory(a.State().AIHistory)); turns > 1 {
				turnLabel := fmt.Sprintf("Turn %d", turns)
				if i18n.IsChinese() {
					turnLabel = fmt.Sprintf("第 %d 轮", turns)
				}
				ui.Text(c, turnLabel).FontSize(typeMicro).FontWeight(600).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
			}
		})
		if expanded {
			a.renderAICenterStepLog(c, theme, steps, phase)
		}
	})
}

func renderAIStepLogRow(c *ui.Context, theme *ui.Theme, step aiStepLogEntry, allowPulse bool) {
	titleColor := theme.TextMuted
	fontWeight := 500
	switch step.Status {
	case aiStepActive:
		titleColor = accentInk(theme, tileFill(theme).Over(surfaceBase(theme)))
		fontWeight = 600
	case aiStepDone:
		titleColor = theme.Text
		fontWeight = 500
	case aiStepApproval:
		titleColor = readableOn(kitStateWarningColor(theme), theme.Text, tileFill(theme).Over(surfaceBase(theme)), minTextContrast)
		fontWeight = 600
	case aiStepError:
		titleColor = readableOn(kitStateErrorColor(theme), theme.Text, tileFill(theme).Over(surfaceBase(theme)), minTextContrast)
		fontWeight = 600
	}

	ui.Row(c).Key("ai-step:"+step.Key).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("%d.", step.Index)).FontSize(typeMicro).FontWeight(600).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine().Shrink(0)
		switch step.Status {
		case aiStepActive:
			kitStatusDot(c, kitStateLoadingColor(theme), allowPulse)
		case aiStepDone:
			renderSemanticIcon(c, "checkmark", typeMicro, theme.Success)
		case aiStepApproval:
			renderSemanticIcon(c, "exclamationmark.triangle", typeMicro, kitStateWarningColor(theme))
		case aiStepError:
			renderSemanticIcon(c, "exclamationmark.circle", typeMicro, kitStateErrorColor(theme))
		default:
			kitStatusDot(c, kitStateEmptyColor(theme), false)
		}
		ui.Text(c, step.Title).FontSize(typeMicro).FontWeight(fontWeight).TextColor(titleColor).SingleLine().Shrink(0)
		if step.Detail != "" {
			ui.Text(c, step.Detail).FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine().Grow(1).MinWidth(0)
		}
	})
}

func (a *AppController) renderAICenterPromptAndContext(c *ui.Context, theme *ui.Theme, state engine.State, phase aiInteractionPhase) {
	promptText := strings.TrimSpace(state.AIPrompt)
	hasSource := state.AISourceText != ""
	if promptText == "" && !hasSource {
		return
	}

	ui.Column(c).FillWidth().Gap(space4).Children(func() {
		if promptText != "" {
			promptBadge := "Prompt"
			if i18n.IsChinese() {
				promptBadge = "提问"
			}
			ui.Row(c).Key("ai-center-prompt").FillWidth().Padding(space4, space8).Radius(radius8).Gap(space6).
				AlignItems(ui.Center).Background(tileFill(theme)).Border(space1, hairline(theme)).Children(func() {
				ui.Text(c, promptBadge).FontSize(typeMicro).FontWeight(700).TextColor(theme.TextMuted).SingleLine().Shrink(0)
				ui.Text(c, promptText).FontSize(typeMeta).FontWeight(500).TextColor(theme.Text).SingleLine().Grow(1).MinWidth(0)
				if hasSource {
					srcRunes := utf8.RuneCountInString(state.AISourceText)
					headerLabel := i18n.Tf("ai.source_text", srcRunes)
					chevron := "▸"
					if a.uiState.sourceExpanded {
						chevron = "▾"
					}
					srcBtn := ui.ButtonBase(c).Key("ai-source-toggle").Padding(space1, space6).Radius(radius4).
						Background(theme.Surface).Border(space1, hairline(theme)).FocusRing(false).Shrink(0)
					srcBtn.Children(func() {
						ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
							ui.Text(c, headerLabel).FontSize(typeMicro).FontWeight(500).TextColor(theme.TextMuted).SingleLine()
							ui.Text(c, chevron).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
						})
					})
					if srcBtn.Clicked() {
						a.uiState.sourceExpanded = !a.uiState.sourceExpanded
					}
				}
			})
		} else if hasSource {
			srcRunes := utf8.RuneCountInString(state.AISourceText)
			headerLabel := i18n.Tf("ai.source_text", srcRunes)
			toggle := ui.ButtonBase(c).
				Key("ai-source-toggle").
				FillWidth().
				Padding(space6, space10).
				Radius(radius8).
				Background(theme.Surface).
				Border(space1, theme.Border)
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
		}

		if hasSource && a.uiState.sourceExpanded && phase != aiPhasePending {
			ui.Box(c).
				FillWidth().
				MaxHeight(84).
				Padding(space8, space10).
				Radius(radius8).
				Background(theme.Surface).
				Border(space1, withAlpha(theme.Border, alphaStrong)).
				Children(func() {
					ui.Scroll(c).FillWidth().Children(func() {
						ui.Text(c, state.AISourceText).FontSize(typeLabel).TextColor(theme.TextMuted).Wrap()
					})
				})
		}
	})
}

func (a *AppController) renderAICenterHistory(c *ui.Context, theme *ui.Theme, state engine.State) {
	history := aiDisplayHistory(state.AIHistory)
	if len(history) == 0 {
		return
	}
	ui.Box(c).
		FillWidth().
		MaxHeight(72).
		Padding(space6, space8).
		Radius(radius8).
		Background(withAlpha(theme.Surface, alphaStrong)).
		Border(space1, withAlpha(theme.Border, alphaStrong)).
		Children(func() {
			ui.Scroll(c).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(space4).Children(func() {
					for idx, turn := range history {
						roleLabel := i18n.T("ai.role_you")
						roleColor := accentInk(theme, theme.Surface)
						roleBg := withAlpha(theme.Accent, alphaSubtle)
						if strings.EqualFold(turn.Role, "assistant") {
							roleLabel = i18n.T("omnibox.tab.ai")
							roleColor = theme.TextMuted
							roleBg = tileFill(theme)
						}
						ui.Row(c).
							Key(fmt.Sprintf("ai-turn:%d", idx)).
							FillWidth().
							Gap(space6).
							AlignItems(ui.Center).
							Children(func() {
								ui.Box(c).
									Padding(space1, space6).
									Radius(radius4).
									Background(roleBg).
									Shrink(0).
									Children(func() {
										ui.Text(c, roleLabel).FontSize(typeMicro).FontWeight(700).TextColor(roleColor).SingleLine()
									})
								ui.Text(c, turn.Content).FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine().Grow(1).MinWidth(0)
							})
					}
				})
			})
		})
}

// renderAICenterBlockingApprovalGate renders the explicit blocking action zone
// for awaiting_human: the user must approve replacement, copy without modifying
// the source app, open revision notes, or reject/close.
func (a *AppController) renderAICenterBlockingApprovalGate(c *ui.Context, theme *ui.Theme, state engine.State, review engine.TextDiff) {
	warning := kitStateWarningColor(theme)
	bg := warning.Alpha(alphaSubtle)
	fg := readableOn(warning, theme.Text, bg.Over(surfaceBase(theme)), minTextContrast)
	target := strings.TrimSpace(state.Context.AppName)

	title := "Awaiting human approval"
	if i18n.IsChinese() {
		title = "待人工确认：将覆盖当前选中文本"
		if target != "" {
			title = fmt.Sprintf("待人工确认：确认后将替换 %s 中的选中文本", target)
		}
	} else if target != "" {
		title = fmt.Sprintf("Awaiting human approval: replace selection in %s", target)
	}
	diffSummary := fmt.Sprintf("−%d +%d", review.Deleted, review.Inserted)

	ui.Column(c).Key("ai-approval-gate").Label("ai-approval-gate").FillWidth().Padding(space8, space10).Gap(space8).
		Radius(radius8).Background(bg).Border(space1, warning.Alpha(alphaHalf)).Children(func() {
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, "exclamationmark.triangle", typeLabel, fg)
			ui.Text(c, title).FontSize(typeMeta).FontWeight(600).TextColor(fg).SingleLine().Grow(1).MinWidth(0)
			ui.Text(c, diffSummary).FontSize(typeMicro).FontWeight(600).FontFeatures("tnum").TextColor(fg).SingleLine().Shrink(0)
		})
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			replaceBtn := ui.PrimaryButton(c, i18n.T("ai.replace"))
			if replaceBtn.Clicked() {
				a.Dispatch(engine.Event{Type: engine.EventAcceptAIOutput})
			}

			copyLabel := modPrefix() + "C " + i18n.T("common.copy")
			if ui.Button(c, copyLabel).Clicked() {
				a.Dispatch(engine.Event{Type: engine.EventCopyAIOutput})
			}

			reviseLabel := "Revise"
			if i18n.IsChinese() {
				reviseLabel = "要求修改"
			}
			reviseBtn := ui.Button(c, reviseLabel).Key("ai-approval-revise").Label(reviseLabel)
			if reviseBtn.Clicked() {
				capsuleUI.setReviseOpen(a, !capsuleUI.reviseOpen(a))
				if capsuleUI.reviseOpen(a) {
					a.uiState.followUpHandle.RequestFocus(c)
				}
			}

			ui.Spacer(c)

			if ui.Button(c, i18n.T("ai.close")).Clicked() {
				a.Dispatch(engine.Event{Type: engine.EventEscape})
			}
		})
	})
}

// renderInputComposer lives in input_composer.go.

func (a *AppController) renderAICenterFooter(c *ui.Context, theme *ui.Theme, state engine.State, phase aiInteractionPhase) {
	ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
		canAccept := phase == aiPhaseDone && state.AIOutput != ""
		canCopy := state.AIOutput != ""

		switch phase {
		case aiPhaseRunning:
			stopLabel := "Stop"
			if i18n.IsChinese() {
				stopLabel = "停止生成"
			}
			stopBtn := ui.Button(c, stopLabel).Key("ai-center-stop").Label(stopLabel)
			if stopBtn.Clicked() {
				a.stopActiveAIStream()
			}
			copyLabel := modPrefix() + "C " + i18n.T("common.copy")
			copyBtn := ui.Button(c, copyLabel).Disabled(!canCopy)
			if copyBtn.Clicked() && canCopy {
				a.Dispatch(engine.Event{Type: engine.EventCopyAIOutput})
			}
		case aiPhaseFailed:
			retryLabel := modPrefix() + "R Retry"
			if i18n.IsChinese() {
				retryLabel = modPrefix() + "R 重试"
			}
			retryBtn := ui.PrimaryButton(c, retryLabel).Key("ai-footer-retry").Label(retryLabel)
			if retryBtn.Clicked() {
				a.submitAIFollowUpOrRetry(state, a.uiState.followUpQuery)
			}
		case aiPhasePending:
			sendLabel := "Send"
			if i18n.IsChinese() {
				sendLabel = "发送提问"
			}
			canSend := strings.TrimSpace(a.uiState.followUpQuery) != ""
			sendBtn := ui.PrimaryButton(c, sendLabel).Key("ai-footer-send").Label(sendLabel).Disabled(!canSend)
			if sendBtn.Clicked() && canSend {
				a.submitAIFollowUpOrRetry(state, a.uiState.followUpQuery)
			}
		default:
			replaceBtn := ui.PrimaryButton(c, i18n.T("ai.replace")).Disabled(!canAccept)
			if replaceBtn.Clicked() && canAccept {
				a.Dispatch(engine.Event{Type: engine.EventAcceptAIOutput})
			}

			copyLabel := modPrefix() + "C " + i18n.T("common.copy")
			copyBtn := ui.Button(c, copyLabel).Disabled(!canCopy)
			if copyBtn.Clicked() && canCopy {
				a.Dispatch(engine.Event{Type: engine.EventCopyAIOutput})
			}

			regenLabel := modPrefix() + "R Retry"
			if i18n.IsChinese() {
				regenLabel = modPrefix() + "R 重试"
			}
			regenBtn := ui.Button(c, regenLabel).Key("ai-footer-regen").Label(regenLabel)
			if regenBtn.Clicked() {
				a.submitAIFollowUpOrRetry(state, a.uiState.followUpQuery)
			}
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
}
