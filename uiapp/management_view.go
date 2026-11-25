package uiapp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
	coresync "github.com/yeisme/clavis/pkg/core/sync"
)

// manageSyncSnapshot is the lock-free value snapshot of readSyncStatus rendered
// by ManagementHubView so render frames never invoke disk/SQL/network reads.
type manageSyncSnapshot struct {
	loaded   bool
	loading  bool
	snapshot coresync.StatusSnapshot
	result   host.OperationResult
	err      error
}

type manageSyncState struct {
	mu   sync.Mutex
	snap manageSyncSnapshot
}

func (a *AppController) ensureManageSyncRefresh() {
	if a.isClosed() {
		return
	}
	st := a.manageSyncStateFor()
	st.mu.Lock()
	if st.snap.loaded || st.snap.loading {
		st.mu.Unlock()
		return
	}
	st.snap.loading = true
	st.mu.Unlock()

	lifeCtx := a.lifeCtx()
	go func() {
		ctx, cancel := context.WithTimeout(lifeCtx, 2*time.Second)
		defer cancel()
		snap, res, err := readSyncStatus(ctx)
		if lifeCtx.Err() != nil {
			st.mu.Lock()
			st.snap.loading = false
			st.mu.Unlock()
			return
		}
		st.mu.Lock()
		st.snap.snapshot = snap
		st.snap.result = res
		st.snap.err = err
		st.snap.loaded = true
		st.snap.loading = false
		st.mu.Unlock()
		if a.isClosed() {
			return
		}
		a.mu.Lock()
		repaint := a.OnRepaint
		a.mu.Unlock()
		if repaint != nil {
			repaint()
		}
	}()
}

func (a *AppController) retryManageSyncStatus() {
	st := a.manageSyncStateFor()
	st.mu.Lock()
	st.snap.loaded = false
	st.snap.err = nil
	st.mu.Unlock()
	a.ensureManageSyncRefresh()
}

func (a *AppController) manageSyncStatusSnapshot() manageSyncSnapshot {
	st := a.manageSyncStateFor()
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.snap
}

// renderManagementHubView renders the operational Management hub (Sync,
// Media/Clipboard storage, Terminal/Agent runtime, Lexicon/Glossary) with
// real status indicators and direct actions — intentionally avoiding raw
// Settings form duplication.
func (a *AppController) renderManagementHubView(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	a.ensureManageSyncRefresh()
	a.ensureClipboardStatsRefresh()
	a.ensureLexiconHitsRefresh()

	syncState := a.manageSyncStatusSnapshot()
	clipStats, _ := a.clipboardStatsSnapshot()
	lexDays, _, lexLoaded, _ := a.lexiconHitsSnapshot()
	sessions, diagnostics := a.cachedAgentData()
	zh := i18n.IsChinese()

	ui.Column(c).Key("management-hub-view").Label("management-hub-view").FillWidth().Grow(1).MinHeight(0).Gap(space8).Children(func() {
		// Header strip: Back + Title + Open Full Settings + Refresh.
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			backLbl := pickLang(zh, "返回", "Back")
			backBtn := ui.ButtonBase(c).Key("manage-back").Label(backLbl).
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

			renderSemanticIcon(c, "gearshape", IconSize16, secondaryInk(theme))
			ui.Text(c, pickLang(zh, "运行管理中心", "Operations & Management")).FontSize(typeBody).FontWeight(700).TextColor(theme.Text).SingleLine()
			ui.Spacer(c)

			refLbl := pickLang(zh, "刷新状态", "Refresh")
			refBtn := ui.ButtonBase(c).Key("manage-refresh-all").Label(refLbl).
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

			cfgBtnLbl := pickLang(zh, "完整设置", "Settings")
			cfgBtn := ui.ButtonBase(c).Key("manage-open-settings").Label(cfgBtnLbl).
				Padding(space2, space8).Radius(radiusChip).Background(tileFill(theme)).Border(space1, hairline(theme)).FocusRing(false)
			cfgBtn.Children(func() {
				ui.Text(c, cfgBtnLbl).FontSize(typeMeta).FontWeight(600).TextColor(secondaryInk(theme)).SingleLine()
			})
			if cfgBtn.Clicked() {
				a.OpenSettings(SettingsTabGeneral)
			}
		})

		// 2x3 operational cards: Sync, Media/Clipboard, Terminal/Agent, Lexicon, AI/Vision, Vault & QuickJump.
		ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
			ui.Grid(c).Columns(2).Gap(space10).FillWidth().Children(func() {
				a.renderManageSyncCard(c, theme, syncState)
				a.renderManageMediaCard(c, theme, cfg, clipStats)
				a.renderManageTerminalCard(c, theme, cfg, sessions, diagnostics)
				a.renderManageLexiconCard(c, theme, cfg, lexDays, lexLoaded)
				a.renderManageAICard(c, theme, cfg)
				a.renderManageVaultQuickJumpCard(c, theme, cfg)
			})
		})
	})
}

func (a *AppController) renderManageSyncCard(c *ui.Context, theme *ui.Theme, st manageSyncSnapshot) {
	zh := i18n.IsChinese()
	renderHubMetricCard(c, theme, "manage-card-sync", "arrow.clockwise", pickLang(zh, "同步状态", "Sync Status"), pickLang(zh, "刷新", "Refresh"), a.retryManageSyncStatus, func() {
		switch {
		case !st.loaded || st.loading:
			kitInlineLoading(c, theme, "manage-sync-loading", pickLang(zh, "正在读取本地同步快照…", "Reading local sync snapshot…"))
		case st.result.State == host.StateUnknown:
			kitWarningBanner(c, theme, pickLang(zh, "同步本地状态暂不可用（只读）", "Local sync state unavailable (read-only)"))
		case !st.snapshot.LoggedIn:
			ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
				kitStatusDot(c, kitStateEmptyColor(theme), false)
				kitStatusPill(c, theme, pickLang(zh, "未配置同步", "Not configured"), chipNeutral)
			})
			ui.Text(c, pickLang(zh, "配置与数据仅保存在本机，未执行网络同步。", "Local-only mode; no sync account configured.")).
				FontSize(typeMicro).TextColor(theme.TextMuted).Wrap()
		default:
			ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
				kitStatusPill(c, theme, pickLang(zh, "已登录", "Signed in"), chipSuccess)
				ui.Text(c, nonEmptyOr(st.snapshot.Account, st.snapshot.DeviceID)).FontSize(typeMeta).FontWeight(600).TextColor(theme.Text).SingleLine().Grow(1).MinWidth(0)
			})
			meta := fmt.Sprintf("v%d · %s · %d↑", st.snapshot.LocalVersion, formatSyncRelativeTime(st.snapshot.LastSyncAt, time.Now()), st.snapshot.PendingUploads)
			ui.Text(c, meta).FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
		}
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, pickLang(zh, "同步详情", "Sync Details")).Key("manage-sync-details").Clicked() {
				a.CloseHubSurface()
				a.OpenSettingsSection(settingsSectionAbout)
			}
		})
	})
}

func (a *AppController) renderManageMediaCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig, st clipboardStatsState) {
	zh := i18n.IsChinese()
	clip := cfg.Main.Clipboard
	renderHubMetricCard(c, theme, "manage-card-media", "photo", pickLang(zh, "媒体与剪贴板", "Media & Clipboard"), pickLang(zh, "打开", "Open"), func() {
		a.CloseHubSurface()
		a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: config.TabClipboard})
	}, func() {
		count, bytes := clipboardStatsTotals(st.kinds)
		mediaTone := chipNeutral
		mediaBadge := pickLang(zh, "媒体捕获已关闭", "Media capture off")
		if clip.MediaCapture {
			mediaTone = chipSuccess
			mediaBadge = pickLang(zh, fmt.Sprintf("媒体捕获 · 配额 %d MB", clip.MediaQuotaMB), fmt.Sprintf("Media on · %d MB quota", clip.MediaQuotaMB))
		}
		ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
			kitStatusPill(c, theme, mediaBadge, mediaTone)
			if st.loaded && st.err == nil {
				ui.Text(c, clipboardStatsCountLabel(count, bytes)).FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
			}
		})
		// Real usage against the real limits: image blobs against the media
		// quota (the only kind QuotaEvictions counts) and rows against the
		// history cap.
		if st.loaded && st.err == nil {
			images := clipboardKindBytes(st.kinds, "image")
			kitQuotaMeter(c, theme, "manage-media-quota", "photo",
				formatClipboardBytes(images)+" / "+fmtInt(clip.MediaQuotaMB)+" MB",
				float64(images), float64(int64(clip.MediaQuotaMB)<<20))
			kitQuotaMeter(c, theme, "manage-history-cap", "doc.on.clipboard",
				fmtInt(count)+" / "+fmtInt(clip.MaxHistoryItems),
				float64(count), float64(clip.MaxHistoryItems))
		}
		policy := pickLang(zh,
			fmt.Sprintf("历史上限 %d 条 · 保留 %d 天", clip.MaxHistoryItems, clip.RetentionDays),
			fmt.Sprintf("Max %d items · %d days retention", clip.MaxHistoryItems, clip.RetentionDays),
		)
		ui.Text(c, policy).FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, pickLang(zh, "刷新统计", "Refresh")).Key("manage-media-refresh").Clicked() {
				a.retryClipboardStats()
			}
			if ui.Button(c, pickLang(zh, "存储策略", "Policy")).Key("manage-media-policy").Clicked() {
				a.CloseHubSurface()
				a.OpenSettingsSection(settingsSectionClipboard)
			}
		})
	})
}

func (a *AppController) renderManageTerminalCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig, sessions []agentsession.Session, diagnostics []agentsession.Diagnostic) {
	zh := i18n.IsChinese()
	termCfg := cfg.Main.Terminal
	defaultTerm := nonEmptyOr(termCfg.Default, "ghostty")
	renderHubMetricCard(c, theme, "manage-card-terminal", "terminal", pickLang(zh, "终端与会话", "Terminal & Sessions"), "Agent", func() {
		a.CloseHubSurface()
		a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: config.TabAgent})
	}, func() {
		ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
			previewBadge(c, theme, "terminal", defaultTerm, false)
			ui.Text(c, pickLang(zh,
				fmt.Sprintf("%d 个终端配置 · %d 个会话", len(termCfg.Profiles), len(sessions)),
				fmt.Sprintf("%d profiles · %d sessions", len(termCfg.Profiles), len(sessions)),
			)).FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
		})
		if len(diagnostics) > 0 {
			ui.Text(c, "▲ "+agentsession.SanitizeDiagnosticText(agentDiagnosticSummary(diagnostics))).
				FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		} else {
			ui.Text(c, pickLang(zh, "本地会话发现正常（诊断已脱敏）", "Local session discovery healthy (redacted)")).
				FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		}
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, pickLang(zh, "刷新会话", "Refresh")).Key("manage-term-refresh").Clicked() {
				a.AgentRefresh()
			}
			if ui.Button(c, pickLang(zh, "配置目录", "Config Dir")).Key("manage-term-dir").Clicked() {
				_ = a.OpenConfigDirectory()
			}
		})
	})
}

func (a *AppController) renderManageLexiconCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig, days []LexiconHitDay, loaded bool) {
	zh := i18n.IsChinese()
	renderHubMetricCard(c, theme, "manage-card-lexicon", "character.book.closed", pickLang(zh, "词库与术语", "Lexicon & Glossary"), pickLang(zh, "术语表", "Open"), func() {
		a.CloseHubSurface()
		a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: config.TabGlossary})
	}, func() {
		summary := pickLang(zh,
			fmt.Sprintf("术语 %d 条 · 置顶用户词 %d 个", len(cfg.Glossary.Terms), len(cfg.Main.InputEngine.UserWords)),
			fmt.Sprintf("%d glossary terms · %d pinned user words", len(cfg.Glossary.Terms), len(cfg.Main.InputEngine.UserWords)),
		)
		ui.Text(c, summary).FontSize(typeMeta).FontWeight(600).FontFeatures("tnum").TextColor(theme.Text).SingleLine()
		if loaded {
			if rate, ok := lexiconOverallHitRate(days); ok {
				ui.Text(c, pickLang(zh, fmt.Sprintf("近 14 日候选命中率 %d%%", rate), fmt.Sprintf("14d candidate hit rate %d%%", rate))).
					FontSize(typeMicro).FontFeatures("tnum").TextColor(theme.TextMuted).SingleLine()
			} else {
				ui.Text(c, pickLang(zh, "近 14 日暂无词库命中记录", "No lexicon hit events in last 14 days")).
					FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
			}
		} else {
			kitInlineLoading(c, theme, "manage-lex-loading", pickLang(zh, "正在加载词库状态…", "Loading lexicon status…"))
		}
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, pickLang(zh, "管理术语", "Glossary")).Key("manage-lex-glossary").Clicked() {
				a.CloseHubSurface()
				a.OpenSettingsSection(settingsSectionGlossary)
			}
			if ui.Button(c, pickLang(zh, "我的词库", "User Words")).Key("manage-lex-ime").Clicked() {
				a.CloseHubSurface()
				a.OpenSettingsSection(settingsSectionIMEKeymap)
			}
		})
	})
}

func (a *AppController) renderManageAICard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	zh := i18n.IsChinese()
	active := a.ActiveAIProfile()
	visProf, visErr := ai.ResolveVisionProfile(cfg.Main.AI)
	renderHubMetricCard(c, theme, "manage-card-ai", "sparkles", pickLang(zh, "AI 与 Vision", "AI & Vision"), pickLang(zh, "设置", "Settings"), func() {
		a.setSettingsSection(settingsSectionAI)
		a.OpenSettings(SettingsTabAI)
	}, func() {
		ui.Text(c, pickLang(zh, fmt.Sprintf("默认: %s (%s)", active.Name, active.Model), fmt.Sprintf("Active: %s (%s)", active.Name, active.Model))).
			FontSize(typeMeta).FontWeight(600).TextColor(theme.Text).SingleLine()
		if visErr == nil {
			ui.Text(c, "Vision: "+visProf.Name+" · "+visProf.Model).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		} else {
			ui.Text(c, pickLang(zh, "Vision: 未配置支持图像的模型", "Vision: none configured")).FontSize(typeMicro).TextColor(kitStateWarningColor(theme)).SingleLine()
		}
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, pickLang(zh, "截图标注", "Capture")).Key("manage-ai-capture").Clicked() {
				go a.CaptureAndAnnotate()
			}
			if ui.Button(c, pickLang(zh, "打开 AI", "Open AI")).Key("manage-ai-open").Clicked() {
				a.CloseHubSurface()
				a.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: config.TabAI})
			}
		})
	})
}

func (a *AppController) renderManageVaultQuickJumpCard(c *ui.Context, theme *ui.Theme, cfg config.ClavisConfig) {
	zh := i18n.IsChinese()
	snap := a.VaultSnapshot()
	ttl := a.SensitiveClipboardState()
	bookmarks := len(cfg.QuickJump.Bookmarks)
	renderHubMetricCard(c, theme, "manage-card-vault-qj", "lock.rotation", pickLang(zh, "Vault 与快跳", "Vault & Quick Jump"), pickLang(zh, "打开资产", "Assets"), func() {
		a.setAssetsSubTab(assetsSubVault)
		a.SetHubSurface(HubSurfaceAssets)
	}, func() {
		ui.Text(c, pickLang(zh,
			fmt.Sprintf("SecretRef %d 条 · 快跳书签 %d 个", len(snap.SecretRefs), bookmarks),
			fmt.Sprintf("%d SecretRefs · %d QuickJump bookmarks", len(snap.SecretRefs), bookmarks),
		)).FontSize(typeMeta).FontWeight(600).FontFeatures("tnum").TextColor(theme.Text).SingleLine()
		if ttl.Active {
			rem := max(1, int(ttl.Remaining.Seconds()+0.9))
			kitStatusPill(c, theme, fmt.Sprintf("%s · %ds", nonEmptyOr(ttl.Label, "TTL"), rem), chipWarning)
		} else {
			ui.Text(c, pickLang(zh, "在动作框输入 # 进入统一快跳", "Type # in Actions for Quick Jump")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
		}
		ui.Row(c).FillWidth().Gap(space6).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, pickLang(zh, "统一快跳 (#)", "Quick Jump (#)")).Key("manage-open-qj").Clicked() {
				a.CloseHubSurface()
				a.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "#"})
			}
			if ui.Button(c, pickLang(zh, "安全", "Security")).Key("manage-open-security").Clicked() {
				a.CloseHubSurface()
				a.OpenSettingsSection(settingsSectionSecurity)
			}
		})
	})
}
