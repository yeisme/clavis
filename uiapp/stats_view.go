package uiapp

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

type dataSourceHealthKind uint8

const (
	sourceHealthReady dataSourceHealthKind = iota
	sourceHealthLoading
	sourceHealthEmpty
	sourceHealthDegraded
	sourceHealthError
)

type dataSourceHealth struct {
	Key    string
	Label  string
	Status string
	Kind   dataSourceHealthKind
}

// renderStatsHubView draws the real-data aggregated Stats hub inside the
// Capsule Omnibox. Every metric comes from existing async cached snapshots
// (AI usage, Clipboard stats, Lexicon hits/top words, Agent sessions) — never
// executing SQL or network calls on build frames, and never fabricating demo numbers.
func (a *AppController) renderStatsHubView(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	// Trigger idempotent cached async refreshes once (guarded by loaded||loading).
	a.ensureAIUsageRefresh()
	a.ensureClipboardStatsRefresh()
	a.ensureLexiconHitsRefresh()
	a.ensureLexiconTopWordsRefresh()

	aiDays, aiErr, aiLoaded, aiRefreshing := a.aiUsageSnapshot()
	clipStats, _ := a.clipboardStatsSnapshot()
	lexDays, lexErr, lexLoaded, _ := a.lexiconHitsSnapshot()
	topWords, _, topLoaded := a.lexiconTopWordsSnapshot()
	sessions, diagnostics := a.cachedAgentData()
	agentRes := a.agentRecordedOperationResult()
	agentRefreshing := a.agentRefreshing()
	zh := i18n.IsChinese()

	sources := []dataSourceHealth{
		classifyAIHealth(zh, aiDays, aiErr, aiLoaded, aiRefreshing),
		classifyClipboardHealth(zh, clipStats),
		classifyLexiconHealth(zh, lexDays, lexErr, lexLoaded, len(cfg.Glossary.Terms)),
		classifyAgentHealth(zh, sessions, diagnostics, agentRes, agentRefreshing),
	}

	ui.Column(c).Key("stats-hub-view").Label("stats-hub-view").FillWidth().Grow(1).MinHeight(0).Gap(space8).Children(func() {
		// Header strip: title + back + Refresh All.
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			backLbl := pickLang(zh, "返回", "Back")
			backBtn := ui.ButtonBase(c).Key("stats-back").Label(backLbl).
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

			renderSemanticIcon(c, "square.3.layers.3d", IconSize16, secondaryInk(theme))
			ui.Text(c, pickLang(zh, "统计总览", "Stats Overview")).FontSize(typeBody).FontWeight(700).TextColor(theme.Text).SingleLine()
			ui.Spacer(c)

			refLbl := pickLang(zh, "刷新全部", "Refresh All")
			refBtn := ui.ButtonBase(c).Key("stats-refresh-all").Label(refLbl).
				Padding(space2, space8).Radius(radiusChip).Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false)
			refBtn.Children(func() {
				ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
					renderSemanticIcon(c, "arrow.clockwise", typeMicro, secondaryInk(theme))
					ui.Text(c, refLbl).FontSize(typeMeta).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
				})
			})
			if refBtn.Clicked() {
				a.refreshActiveHubSources()
			}
		})

		// Data source health strip (4 real sources, semantic icon + text).
		ui.Row(c).Key("stats-source-health").Label("stats-source-health").FillWidth().Wrap().Gap(space6).AlignItems(ui.Center).Children(func() {
			for _, src := range sources {
				renderDataSourceHealthPill(c, theme, src)
			}
		})

		// 2x2 real-data metric cards inside scroll viewport.
		ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
			ui.Grid(c).Columns(2).Gap(space10).FillWidth().Children(func() {
				a.renderStatsAICard(c, theme, aiDays, aiErr, aiLoaded, aiRefreshing)
				a.renderStatsClipboardCard(c, theme, clipStats)
				a.renderStatsLexiconCard(c, theme, cfg, lexDays, lexErr, lexLoaded, topWords, topLoaded)
				a.renderStatsAgentCard(c, theme, sessions, diagnostics, agentRes, agentRefreshing)
			})
		})
	})
}

func classifyAIHealth(zh bool, days []AIUsageDay, err error, loaded, refreshing bool) dataSourceHealth {
	label := pickLang(zh, "AI 用量", "AI Usage")
	switch {
	case err != nil:
		return dataSourceHealth{Key: "ai", Label: label, Status: pickLang(zh, "不可用", "Unavailable"), Kind: sourceHealthError}
	case !loaded || refreshing:
		return dataSourceHealth{Key: "ai", Label: label, Status: pickLang(zh, "加载中", "Loading"), Kind: sourceHealthLoading}
	case aiUsageTotalRequests(days) == 0:
		return dataSourceHealth{Key: "ai", Label: label, Status: pickLang(zh, "无记录", "No records"), Kind: sourceHealthEmpty}
	default:
		return dataSourceHealth{Key: "ai", Label: label, Status: pickLang(zh, "正常", "Ready"), Kind: sourceHealthReady}
	}
}

func classifyClipboardHealth(zh bool, st clipboardStatsState) dataSourceHealth {
	label := pickLang(zh, "剪贴板", "Clipboard")
	count, _ := clipboardStatsTotals(st.kinds)
	switch {
	case st.err != nil:
		return dataSourceHealth{Key: "clipboard", Label: label, Status: pickLang(zh, "不可用", "Unavailable"), Kind: sourceHealthError}
	case !st.loaded || st.loading:
		return dataSourceHealth{Key: "clipboard", Label: label, Status: pickLang(zh, "加载中", "Loading"), Kind: sourceHealthLoading}
	case count == 0:
		return dataSourceHealth{Key: "clipboard", Label: label, Status: pickLang(zh, "空", "Empty"), Kind: sourceHealthEmpty}
	default:
		return dataSourceHealth{Key: "clipboard", Label: label, Status: pickLang(zh, "正常", "Ready"), Kind: sourceHealthReady}
	}
}

func classifyLexiconHealth(zh bool, days []LexiconHitDay, err error, loaded bool, catalogCount int) dataSourceHealth {
	label := pickLang(zh, "词库", "Lexicon")
	switch {
	case err != nil:
		return dataSourceHealth{Key: "lexicon", Label: label, Status: pickLang(zh, "不可用", "Unavailable"), Kind: sourceHealthError}
	case !loaded:
		return dataSourceHealth{Key: "lexicon", Label: label, Status: pickLang(zh, "加载中", "Loading"), Kind: sourceHealthLoading}
	case lexiconTotalEvents(days) == 0 && catalogCount == 0:
		return dataSourceHealth{Key: "lexicon", Label: label, Status: pickLang(zh, "空", "Empty"), Kind: sourceHealthEmpty}
	default:
		return dataSourceHealth{Key: "lexicon", Label: label, Status: pickLang(zh, "正常", "Ready"), Kind: sourceHealthReady}
	}
}

func classifyAgentHealth(zh bool, sessions []agentsession.Session, diagnostics []agentsession.Diagnostic, res host.OperationResult, refreshing bool) dataSourceHealth {
	label := "Agent"
	switch {
	case refreshing:
		return dataSourceHealth{Key: "agent", Label: label, Status: pickLang(zh, "刷新中", "Refreshing"), Kind: sourceHealthLoading}
	case res.State == host.StateFailed:
		return dataSourceHealth{Key: "agent", Label: label, Status: pickLang(zh, "失败", "Failed"), Kind: sourceHealthError}
	case res.State == host.StateDegraded || len(diagnostics) > 0:
		return dataSourceHealth{Key: "agent", Label: label, Status: pickLang(zh, "部分降级", "Degraded"), Kind: sourceHealthDegraded}
	case len(sessions) == 0:
		return dataSourceHealth{Key: "agent", Label: label, Status: pickLang(zh, "无会话", "No sessions"), Kind: sourceHealthEmpty}
	default:
		return dataSourceHealth{Key: "agent", Label: label, Status: pickLang(zh, "正常", "Ready"), Kind: sourceHealthReady}
	}
}

func renderDataSourceHealthPill(c *ui.Context, theme *ui.Theme, src dataSourceHealth) {
	ui.Row(c).Key("source-health:"+src.Key).Padding(space2, space8).Radius(radiusPill).Gap(space4).
		AlignItems(ui.Center).Background(tileFill(theme)).Border(space1, hairline(theme)).Shrink(0).Children(func() {
		switch src.Kind {
		case sourceHealthReady:
			renderSemanticIcon(c, "checkmark", typeMicro, theme.Success)
		case sourceHealthLoading:
			kitStatusDot(c, kitStateLoadingColor(theme), true)
		case sourceHealthDegraded:
			renderSemanticIcon(c, "exclamationmark.triangle", typeMicro, kitStateWarningColor(theme))
		case sourceHealthError:
			renderSemanticIcon(c, "exclamationmark.circle", typeMicro, kitStateErrorColor(theme))
		default:
			kitStatusDot(c, kitStateEmptyColor(theme), false)
		}
		ui.Text(c, src.Label).FontSize(typeMicro).FontWeight(600).TextColor(theme.Text).SingleLine()
		ui.Text(c, src.Status).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
	})
}

func renderHubMetricCard(c *ui.Context, theme *ui.Theme, key, icon, title, actionLabel string, onAction func(), body func()) {
	ui.Column(c).Key(key).Label(key).FillWidth().Padding(space10, space12).Gap(space8).
		Radius(radiusCard).Background(theme.Surface).Border(space1, hairline(theme)).Children(func() {
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, icon, typeLabel, secondaryInk(theme))
			ui.Text(c, title).FontSize(typeLabel).FontWeight(700).TextColor(theme.Text).SingleLine().Grow(1).MinWidth(0)
			if actionLabel != "" && onAction != nil {
				btn := ui.ButtonBase(c).Key(key + "-action").Label(actionLabel).
					Padding(space1, space6).Radius(radius4).Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false).Shrink(0)
				btn.Children(func() {
					ui.Text(c, actionLabel).FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
				})
				if btn.Clicked() {
					onAction()
				}
			}
		})
		kitDivider(c, theme)
		body()
	})
}

func (a *AppController) renderStatsAICard(c *ui.Context, theme *ui.Theme, days []AIUsageDay, err error, loaded, refreshing bool) {
	zh := i18n.IsChinese()
	renderHubMetricCard(c, theme, "stats-card-ai", "sparkles", pickLang(zh, "AI 用量 · 近 7 日", "AI Usage · 7d"), pickLang(zh, "AI 工作台", "Open AI"), func() {
		a.CloseHubSurface()
		a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: config.TabAI})
	}, func() {
		switch {
		case err != nil:
			kitInlineError(c, theme, "stats-ai-err", pickLang(zh, "AI 用量暂不可用", "AI usage unavailable"), a.retryAIUsage)
		case !loaded || refreshing:
			kitInlineLoading(c, theme, "stats-ai-loading", pickLang(zh, "正在加载 AI 用量…", "Loading AI usage…"))
		case aiUsageTotalRequests(days) == 0:
			ui.Text(c, pickLang(zh, "近 7 日无 AI 调用记录", "No AI calls recorded in last 7 days")).
				FontSize(typeMeta).TextColor(theme.TextMuted).Wrap()
		default:
			totalTokens := aiUsageTotalTokens(days)
			totalReqs := aiUsageTotalRequests(days)
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Gap(space2).Grow(1).Children(func() {
					ui.Text(c, aiUsageTodayLabel(days)).FontSize(typeKPI).FontWeight(700).FontFeatures("tnum").TextColor(theme.Text).SingleLine()
					sub := fmt.Sprintf("7d: %s tok · %d calls", formatTokenCount(totalTokens), totalReqs)
					if zh {
						sub = fmt.Sprintf("7 日累计 %s tok · %d 次", formatTokenCount(totalTokens), totalReqs)
					}
					ui.Text(c, sub).FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
				})
				renderAIUsageChart(c, theme, days)
			})
		}
	})
}

func (a *AppController) renderStatsClipboardCard(c *ui.Context, theme *ui.Theme, st clipboardStatsState) {
	zh := i18n.IsChinese()
	renderHubMetricCard(c, theme, "stats-card-clipboard", "doc.on.clipboard", pickLang(zh, "剪贴板存储", "Clipboard Storage"), pickLang(zh, "剪贴板", "Open"), func() {
		a.CloseHubSurface()
		a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: config.TabClipboard})
	}, func() {
		switch {
		case st.err != nil:
			kitInlineError(c, theme, "stats-clip-err", clipboardStatsErrorText(), a.retryClipboardStats)
		case !st.loaded || st.loading:
			kitInlineLoading(c, theme, "stats-clip-loading", clipboardStatsLoadingText())
		default:
			count, bytes := clipboardStatsTotals(st.kinds)
			if count == 0 {
				ui.Text(c, pickLang(zh, "暂无剪贴板统计记录", "No clipboard entries recorded")).
					FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
				return
			}
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Gap(space2).Grow(1).Children(func() {
					ui.Text(c, clipboardStatsCountLabel(count, bytes)).FontSize(typeBody).FontWeight(700).FontFeatures("tnum").TextColor(theme.Text).SingleLine()
					parts := make([]string, 0, len(st.kinds))
					for _, k := range st.kinds {
						if k.Count > 0 {
							parts = append(parts, fmt.Sprintf("%s %d", clipboardKindLabel(k.Kind), k.Count))
						}
					}
					if len(parts) > 0 {
						ui.Text(c, strings.Join(parts, " · ")).FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
					}
				})
				if len(st.days) >= 7 && count >= 5 {
					renderClipboardSparkline(c, theme, st.days)
				}
			})
		}
	})
}

func (a *AppController) renderStatsLexiconCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig, days []LexiconHitDay, err error, loaded bool, topWords []LexiconTopWord, topLoaded bool) {
	zh := i18n.IsChinese()
	renderHubMetricCard(c, theme, "stats-card-lexicon", "character.book.closed", pickLang(zh, "输入法与词库 · 14 日", "Lexicon · 14d"), pickLang(zh, "术语表", "Open"), func() {
		a.CloseHubSurface()
		a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: config.TabGlossary})
	}, func() {
		switch {
		case err != nil:
			kitInlineError(c, theme, "stats-lex-err", pickLang(zh, "词库统计暂不可用", "Lexicon stats unavailable"), a.retryLexiconHits)
		case !loaded:
			kitInlineLoading(c, theme, "stats-lex-loading", pickLang(zh, "正在加载词库统计…", "Loading lexicon stats…"))
		default:
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Gap(space2).Grow(1).Children(func() {
					if rate, ok := lexiconOverallHitRate(days); ok {
						kpi := fmt.Sprintf("Hit %d%%", rate)
						if zh {
							kpi = fmt.Sprintf("命中 %d%%", rate)
						}
						ui.Text(c, kpi).FontSize(typeKPI).FontWeight(700).FontFeatures("tnum").TextColor(theme.Text).SingleLine()
					} else {
						ui.Text(c, pickLang(zh, fmt.Sprintf("已收录 %d 词", len(cfg.Glossary.Terms)), fmt.Sprintf("%d terms", len(cfg.Glossary.Terms)))).
							FontSize(typeBody).FontWeight(700).FontFeatures("tnum").TextColor(theme.Text).SingleLine()
					}
					sub := pickLang(zh,
						fmt.Sprintf("术语 %d · 用户词 %d", len(cfg.Glossary.Terms), len(cfg.Main.InputEngine.UserWords)),
						fmt.Sprintf("Glossary %d · User words %d", len(cfg.Glossary.Terms), len(cfg.Main.InputEngine.UserWords)),
					)
					ui.Text(c, sub).FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
				})
				if lexiconTotalEvents(days) > 0 {
					renderLexiconSparkline(c, theme, days)
				}
			})
			if topLoaded && len(topWords) > 0 {
				words := topWords
				if len(words) > 3 {
					words = words[:3]
				}
				names := make([]string, 0, len(words))
				for _, w := range words {
					if w.Word != "" && w.UseCount > 0 {
						names = append(names, fmt.Sprintf("%s(%d)", w.Word, w.UseCount))
					}
				}
				if len(names) > 0 {
					ui.Text(c, pickLang(zh, "高频词：", "Top: ")+strings.Join(names, " · ")).
						FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
				}
			}
		}
	})
}

func (a *AppController) renderStatsAgentCard(c *ui.Context, theme *ui.Theme, sessions []agentsession.Session, diagnostics []agentsession.Diagnostic, res host.OperationResult, refreshing bool) {
	zh := i18n.IsChinese()
	counts := countAgentLanes(sessions)
	renderHubMetricCard(c, theme, "stats-card-agent", "person.crop.circle", pickLang(zh, "Agent 并行轨道", "Agent Workbench"), "Agent", func() {
		a.CloseHubSurface()
		a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: config.TabAgent})
	}, func() {
		if refreshing && len(sessions) == 0 {
			kitInlineLoading(c, theme, "stats-agent-loading", pickLang(zh, "正在发现本地 Agent 会话…", "Discovering local Agent sessions…"))
			return
		}
		if res.State == host.StateFailed && len(sessions) == 0 {
			kitInlineError(c, theme, "stats-agent-err", agentsession.SanitizeDiagnosticText(agentStatusMessage(res)), a.AgentRefresh)
			return
		}
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			kpi := fmt.Sprintf("%d sessions", counts.Total)
			if zh {
				kpi = fmt.Sprintf("%d 个会话", counts.Total)
			}
			ui.Text(c, kpi).FontSize(typeBody).FontWeight(700).FontFeatures("tnum").TextColor(theme.Text).SingleLine()
			ui.Spacer(c)
			if counts.Running > 0 {
				kitStatusPill(c, theme, pickLang(zh, fmt.Sprintf("● %d 运行", counts.Running), fmt.Sprintf("● %d run", counts.Running)), chipAccent)
			}
			if counts.Approval > 0 {
				kitStatusPill(c, theme, pickLang(zh, fmt.Sprintf("▲ %d 待审", counts.Approval), fmt.Sprintf("▲ %d review", counts.Approval)), chipWarning)
			}
		})
		summary := pickLang(zh,
			fmt.Sprintf("运行 %d · 待审批 %d · 已完成 %d", counts.Running, counts.Approval, counts.Done),
			fmt.Sprintf("Running %d · Approval %d · Done %d", counts.Running, counts.Approval, counts.Done),
		)
		ui.Text(c, summary).FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
		if len(diagnostics) > 0 {
			diagText := agentsession.SanitizeDiagnosticText(agentDiagnosticSummary(diagnostics))
			ui.Text(c, "▲ "+diagText).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		}
	})
}
