package uiapp

import (
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

	sources := []dataSourceHealth{
		classifyAIHealth(aiDays, aiErr, aiLoaded, aiRefreshing),
		classifyClipboardHealth(clipStats),
		classifyLexiconHealth(lexDays, lexErr, lexLoaded, len(cfg.Glossary.Terms)),
		classifyAgentHealth(sessions, diagnostics, agentRes, agentRefreshing),
	}

	ui.Column(c).Key("stats-hub-view").Label("stats-hub-view").FillWidth().Grow(1).MinHeight(0).Gap(space8).Children(func() {
		// Header strip: title + back + Refresh All.
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			backLbl := i18n.T("stats.action.back")
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
			ui.Text(c, i18n.T("stats.title")).FontSize(typeBody).FontWeight(700).TextColor(theme.Text).SingleLine()
			ui.Spacer(c)

			refLbl := i18n.T("stats.action.refresh_all")
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

		// 2x2 real-data metric cards inside scroll viewport. The grid must not
		// shrink to the viewport (the Scroll default would squeeze the second row
		// and clip it): at the Omnibox-sized Capsule it fits exactly, and in a
		// smaller one it overflows and scrolls.
		ui.Scroll(c).Key("stats-scroll").Label("stats-scroll").FillWidth().Grow(1).MinHeight(0).Children(func() {
			ui.Grid(c).Key("stats-grid").Label("stats-grid").Columns(2).Gap(space10).FillWidth().Shrink(0).Children(func() {
				a.renderStatsAICard(c, theme, aiDays, aiErr, aiLoaded, aiRefreshing)
				a.renderStatsClipboardCard(c, theme, clipStats)
				a.renderStatsLexiconCard(c, theme, cfg, lexDays, lexErr, lexLoaded, topWords, topLoaded)
				a.renderStatsAgentCard(c, theme, sessions, diagnostics, agentRes, agentRefreshing)
			})
		})
	})
}

func classifyAIHealth(days []AIUsageDay, err error, loaded, refreshing bool) dataSourceHealth {
	label := i18n.T("stats.source.ai")
	switch {
	case err != nil:
		return dataSourceHealth{Key: "ai", Label: label, Status: i18n.T("stats.status.unavailable"), Kind: sourceHealthError}
	case !loaded || refreshing:
		return dataSourceHealth{Key: "ai", Label: label, Status: i18n.T("stats.status.loading"), Kind: sourceHealthLoading}
	case aiUsageTotalRequests(days) == 0:
		return dataSourceHealth{Key: "ai", Label: label, Status: i18n.T("stats.status.no_records"), Kind: sourceHealthEmpty}
	default:
		return dataSourceHealth{Key: "ai", Label: label, Status: i18n.T("stats.status.ready"), Kind: sourceHealthReady}
	}
}

func classifyClipboardHealth(st clipboardStatsState) dataSourceHealth {
	label := i18n.T("stats.source.clipboard")
	count, _ := clipboardStatsTotals(st.kinds)
	switch {
	case st.err != nil:
		return dataSourceHealth{Key: "clipboard", Label: label, Status: i18n.T("stats.status.unavailable"), Kind: sourceHealthError}
	case !st.loaded || st.loading:
		return dataSourceHealth{Key: "clipboard", Label: label, Status: i18n.T("stats.status.loading"), Kind: sourceHealthLoading}
	case count == 0:
		return dataSourceHealth{Key: "clipboard", Label: label, Status: i18n.T("stats.status.empty"), Kind: sourceHealthEmpty}
	default:
		return dataSourceHealth{Key: "clipboard", Label: label, Status: i18n.T("stats.status.ready"), Kind: sourceHealthReady}
	}
}

func classifyLexiconHealth(days []LexiconHitDay, err error, loaded bool, catalogCount int) dataSourceHealth {
	label := i18n.T("stats.source.lexicon")
	switch {
	case err != nil:
		return dataSourceHealth{Key: "lexicon", Label: label, Status: i18n.T("stats.status.unavailable"), Kind: sourceHealthError}
	case !loaded:
		return dataSourceHealth{Key: "lexicon", Label: label, Status: i18n.T("stats.status.loading"), Kind: sourceHealthLoading}
	case lexiconTotalEvents(days) == 0 && catalogCount == 0:
		return dataSourceHealth{Key: "lexicon", Label: label, Status: i18n.T("stats.status.empty"), Kind: sourceHealthEmpty}
	default:
		return dataSourceHealth{Key: "lexicon", Label: label, Status: i18n.T("stats.status.ready"), Kind: sourceHealthReady}
	}
}

func classifyAgentHealth(sessions []agentsession.Session, diagnostics []agentsession.Diagnostic, res host.OperationResult, refreshing bool) dataSourceHealth {
	label := i18n.T("stats.source.agent")
	switch {
	case refreshing:
		return dataSourceHealth{Key: "agent", Label: label, Status: i18n.T("stats.status.refreshing"), Kind: sourceHealthLoading}
	case res.State == host.StateFailed:
		return dataSourceHealth{Key: "agent", Label: label, Status: i18n.T("stats.status.failed"), Kind: sourceHealthError}
	case res.State == host.StateDegraded || len(diagnostics) > 0:
		return dataSourceHealth{Key: "agent", Label: label, Status: i18n.T("stats.status.degraded"), Kind: sourceHealthDegraded}
	case len(sessions) == 0:
		return dataSourceHealth{Key: "agent", Label: label, Status: i18n.T("stats.status.no_sessions"), Kind: sourceHealthEmpty}
	default:
		return dataSourceHealth{Key: "agent", Label: label, Status: i18n.T("stats.status.ready"), Kind: sourceHealthReady}
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

// renderHubCardHeader is the icon, title and optional action button every hub
// card opens with.
func renderHubCardHeader(c *ui.Context, theme *ui.Theme, key, icon, title, actionLabel string, onAction func()) {
	ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
		renderSemanticIcon(c, icon, typeLabel, secondaryInk(theme))
		ui.Text(c, title).FontSize(typeLabel).FontWeight(700).TextColor(theme.Text).SingleLine().Grow(1).MinWidth(0)
		if actionLabel != "" && onAction != nil {
			btn := ui.ButtonBase(c).Key(key+"-action").Label(actionLabel).
				Padding(space1, space6).Radius(radius4).Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false).Shrink(0)
			btn.Children(func() {
				ui.Text(c, actionLabel).FontSize(typeMicro).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
			})
			if btn.Clicked() {
				onAction()
			}
		}
	})
}

func renderHubMetricCard(c *ui.Context, theme *ui.Theme, key, icon, title, actionLabel string, onAction func(), body func()) {
	ui.Column(c).Key(key).Label(key).FillWidth().Padding(space10, space12).Gap(space8).
		Radius(radiusCard).Background(theme.Surface).Border(space1, hairline(theme)).Children(func() {
		renderHubCardHeader(c, theme, key, icon, title, actionLabel, onAction)
		kitDivider(c, theme)
		body()
	})
}

// statsCardHeight is the one height of every Stats card. Four cards in two
// rows plus the grid gap fit the Omnibox-sized Capsule's hub viewport in both
// languages (369.8dp in Chinese, 375.3dp in English, a few dp to spare; see
// TestStatsHubCardsAreEqualAndWholeAtTheCapsuleSize), so the dashboard shows
// whole without scrolling and no card is shorter than its neighbour. In a
// smaller Capsule the grid keeps this height and the viewport scrolls.
const statsCardHeight float32 = 178

// renderStatsChartCard is a Stats card: the hub card chrome at a fixed height,
// on an OPAQUE surface (the chart's gaps and rings are cut from it, so it must
// not be glass), whose body is a column that fills what the header leaves.
func renderStatsChartCard(c *ui.Context, theme *ui.Theme, key, icon, title, actionLabel string, onAction func(), body func()) {
	ui.Column(c).Key(key).Label(key).FillWidth().Height(statsCardHeight).Padding(space10, space12).Gap(space8).
		Radius(radiusCard).Background(chartOpaque(theme.Surface)).Border(space1, hairline(theme)).Children(func() {
		renderHubCardHeader(c, theme, key, icon, title, actionLabel, onAction)
		kitDivider(c, theme)
		ui.Column(c).Key(key + "-body").FillWidth().Grow(1).MinHeight(space0).Children(body)
	})
}

// statsCardState centres a loading, error or empty notice in the card body, so
// a card without a chart still fills its height with something deliberate.
func statsCardState(c *ui.Context, notice func()) {
	ui.Column(c).FillWidth().Grow(1).MinHeight(space0).Justify(ui.Center).AlignItems(ui.Start).Children(notice)
}

func (a *AppController) renderStatsAICard(c *ui.Context, theme *ui.Theme, days []AIUsageDay, err error, loaded, refreshing bool) {
	renderStatsChartCard(c, theme, "stats-card-ai", "sparkles", i18n.T("stats.card.ai.title"), i18n.T("stats.card.ai.action"), func() {
		a.CloseHubSurface()
		a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: config.TabAI})
	}, func() {
		switch {
		case err != nil:
			statsCardState(c, func() { kitInlineError(c, theme, "stats-ai-err", i18n.T("stats.card.ai.unavailable"), a.retryAIUsage) })
		case !loaded || refreshing:
			statsCardState(c, func() { kitInlineLoading(c, theme, "stats-ai-loading", i18n.T("stats.card.ai.loading")) })
		case aiUsageTotalRequests(days) == 0:
			statsCardState(c, func() {
				ui.Text(c, i18n.T("stats.card.ai.empty")).
					FontSize(typeMeta).TextColor(theme.TextMuted).Wrap()
			})
		default:
			renderAIUsageHubChart(c, theme, days)
		}
	})
}

func (a *AppController) renderStatsClipboardCard(c *ui.Context, theme *ui.Theme, st clipboardStatsState) {
	renderStatsChartCard(c, theme, "stats-card-clipboard", "doc.on.clipboard", i18n.T("stats.card.clipboard.title"), i18n.T("stats.card.clipboard.action"), func() {
		a.CloseHubSurface()
		a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: config.TabClipboard})
	}, func() {
		switch {
		case st.err != nil:
			statsCardState(c, func() { kitInlineError(c, theme, "stats-clip-err", clipboardStatsErrorText(), a.retryClipboardStats) })
		case !st.loaded || st.loading:
			statsCardState(c, func() { kitInlineLoading(c, theme, "stats-clip-loading", clipboardStatsLoadingText()) })
		default:
			if count, _ := clipboardStatsTotals(st.kinds); count == 0 {
				statsCardState(c, func() {
					ui.Text(c, i18n.T("stats.card.clipboard.empty")).
						FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
				})
				return
			}
			renderClipboardHubChart(c, theme, st)
		}
	})
}

func (a *AppController) renderStatsLexiconCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig, days []LexiconHitDay, err error, loaded bool, topWords []LexiconTopWord, topLoaded bool) {
	renderStatsChartCard(c, theme, "stats-card-lexicon", "character.book.closed", i18n.T("stats.card.lexicon.title"), i18n.T("stats.card.lexicon.action"), func() {
		a.CloseHubSurface()
		a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: config.TabGlossary})
	}, func() {
		switch {
		case err != nil:
			statsCardState(c, func() {
				kitInlineError(c, theme, "stats-lex-err", i18n.T("stats.card.lexicon.unavailable"), a.retryLexiconHits)
			})
		case !loaded:
			statsCardState(c, func() { kitInlineLoading(c, theme, "stats-lex-loading", i18n.T("stats.card.lexicon.loading")) })
		default:
			renderLexiconHubChart(c, theme, cfg, days, topWords, topLoaded)
		}
	})
}

func (a *AppController) renderStatsAgentCard(c *ui.Context, theme *ui.Theme, sessions []agentsession.Session, diagnostics []agentsession.Diagnostic, res host.OperationResult, refreshing bool) {
	counts := countAgentLanes(sessions)
	renderStatsChartCard(c, theme, "stats-card-agent", "person.crop.circle", i18n.T("stats.card.agent.title"), i18n.T("stats.source.agent"), func() {
		a.CloseHubSurface()
		a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: config.TabAgent})
	}, func() {
		if refreshing && len(sessions) == 0 {
			statsCardState(c, func() { kitInlineLoading(c, theme, "stats-agent-loading", i18n.T("stats.card.agent.loading")) })
			return
		}
		if res.State == host.StateFailed && len(sessions) == 0 {
			statsCardState(c, func() {
				kitInlineError(c, theme, "stats-agent-err", agentsession.SanitizeDiagnosticText(agentStatusMessage(res)), a.AgentRefresh)
			})
			return
		}
		renderAgentLaneChart(c, theme, counts, diagnostics)
	})
}

// agentLanePart is one block of the lane distribution bar.
type agentLanePart struct {
	Kind  agentLaneKind
	Count int
}

// agentLaneParts lists the lanes that hold sessions, in the order a session
// moves through them (running, awaiting approval, done), then idle sessions,
// so the blocks always add up to the session total the card headlines.
func agentLaneParts(counts agentLaneCounts) []agentLanePart {
	parts := []agentLanePart{
		{Kind: agentLaneRunning, Count: counts.Running},
		{Kind: agentLaneApproval, Count: counts.Approval},
		{Kind: agentLaneDone, Count: counts.Done},
		{Kind: agentLaneWaiting, Count: counts.Total - counts.Running - counts.Approval - counts.Done},
	}
	out := parts[:0]
	for _, part := range parts {
		if part.Count > 0 {
			out = append(out, part)
		}
	}
	return out
}

// agentLaneFill is the exact color of a lane. Running is a state, so it may
// take the accent; awaiting approval takes the system orange (never alone: its
// icon and its name always accompany it); done is neutral gray and idle the
// lighter step.
func agentLaneFill(theme *ui.Theme, pal chartPalette, kind agentLaneKind) ui.Color {
	switch kind {
	case agentLaneRunning:
		return pal.accent
	case agentLaneApproval:
		return kitStateWarningColor(theme)
	case agentLaneDone:
		return pal.strong
	}
	return pal.soft
}

// agentLaneIcon is the glyph that identifies a lane without its color.
func agentLaneIcon(kind agentLaneKind) string {
	switch kind {
	case agentLaneRunning:
		return "bolt"
	case agentLaneApproval:
		return "exclamationmark.triangle"
	case agentLaneDone:
		return "checkmark"
	}
	return "clock"
}

// agentLaneName is the lane's name in the active language, taken from the very
// label the Agent tab puts on a session of that lane ("▲ Approval") without
// its leading symbol, so both surfaces say the same word.
func agentLaneName(kind agentLaneKind) string {
	var probe agentsession.Session
	switch kind {
	case agentLaneApproval:
		probe.PendingReview = true
	case agentLaneRunning:
		probe.Status = agentsession.StatusRunning
	case agentLaneDone:
		probe.Status = agentsession.StatusStopped
	}
	label, _ := agentLaneSymbolAndLabel(probe)
	if _, name, ok := strings.Cut(label, " "); ok {
		return name
	}
	return label
}

// renderAgentLaneChart is the body of the Agent card: the session total, one
// tall bar split by lane with each lane's count printed inside it where it
// fits, and a legend row per lane (icon chip, name, share; plus the count for
// a block too narrow to print it).
func renderAgentLaneChart(c *ui.Context, theme *ui.Theme, counts agentLaneCounts, diagnostics []agentsession.Diagnostic) {
	pal := newChartPalette(theme)
	parts := agentLaneParts(counts)
	values := make([]float64, len(parts))
	texts := make([]string, len(parts))
	for i, part := range parts {
		values[i] = float64(part.Count)
		texts[i] = fmtInt(part.Count)
	}
	fits := chartInlineFits(values, texts, typeMeta)
	percents := chartPercents(values)
	bar := make([]chartPart, len(parts))
	cells := make([][]chartKeyCell, len(parts))
	for i, part := range parts {
		name := agentLaneName(part.Kind)
		bar[i] = chartPart{
			Key:   "stats-agent-lane-" + string(part.Kind),
			Value: values[i],
			Fill:  agentLaneFill(theme, pal, part.Kind),
			Text:  texts[i],
			Tip:   name + " · " + texts[i] + " · " + fmtInt(percents[i]) + "%",
		}
		inline := ""
		if !fits[i] {
			inline = texts[i] // too narrow to print inside the block: the legend carries it
		}
		cells[i] = []chartKeyCell{{Text: inline, Strong: true}, {Text: fmtInt(percents[i]) + "%"}}
	}
	widths := chartKeyWidths(c, cells)

	ui.Column(c).Key("stats-agent-body").FillWidth().Grow(1).MinHeight(space0).Gap(space6).Children(func() {
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.Tf("stats.card.agent.sessions", counts.Total)).Key("stats-agent-total").
				FontSize(typeBody).FontWeight(600).FontFeatures("tnum").TextColor(theme.Text).SingleLine().Shrink(0)
			ui.Spacer(c)
			if len(diagnostics) > 0 {
				// Diagnostics ride the headline row (one truncated line, full text on
				// hover) so the worst case, four lanes plus diagnostics, still fits.
				diagText := agentsession.SanitizeDiagnosticText(agentDiagnosticSummary(diagnostics))
				ui.Row(c).Key("stats-agent-diagnostics").Gap(space4).AlignItems(ui.Center).MinWidth(space0).Tooltip(diagText).Children(func() {
					renderSemanticIcon(c, "exclamationmark.triangle", typeMicro, kitStateWarningColor(theme))
					ui.Text(c, diagText).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine().MinWidth(space0)
				})
			}
		})
		if len(parts) == 0 {
			ui.Text(c, i18n.T("stats.status.no_sessions")).FontSize(typeMeta).TextColor(pal.label).SingleLine()
			return
		}
		kitChartSegmentBar(c, chartBarSpec{Key: "stats-agent-bar", Parts: bar, Height: chartTallBarSize})
		ui.Column(c).Key("stats-agent-legend").Label("stats-agent-legend").FillWidth().Gap(space2).Children(func() {
			for i, part := range parts {
				kitChartKeyRow(c, pal, "stats-agent-key-"+string(part.Kind), bar[i].Fill,
					agentLaneIcon(part.Kind), agentLaneName(part.Kind), cells[i], widths)
			}
		})
	})
}
