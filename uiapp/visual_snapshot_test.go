package uiapp

import (
	"context"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// TestVisualSnapshots renders every transient surface (Leader, Omnibox,
// Clipboard + preview, AI, HUD, floating pills) and two Settings pages to PNG
// for visual review. It is opt-in so the regular suite stays fast and does not
// depend on platform fonts:
//
//	CLAVIS_SNAPSHOT_DIR=/tmp/clavis-shots go test -run TestVisualSnapshots ./uiapp/
//
// The headless Tester rasterizes real pixels, so before/after comparisons of a
// visual change need neither the running app nor its OS permissions.
func TestVisualSnapshots(t *testing.T) {
	dir := os.Getenv("CLAVIS_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set CLAVIS_SNAPSHOT_DIR to write visual snapshots")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	langs := []struct {
		tag  string
		lang i18n.Language
	}{{"zh", i18n.Chinese}, {"en", i18n.English}}

	for _, l := range langs {
		for _, dark := range []bool{false, true} {
			mode := "light"
			if dark {
				mode = "dark"
			}
			for _, preset := range []string{"classic", "liquid_glass", "spatial_3d"} {
				for _, motion := range []string{"full", "reduced"} {
					suffix := l.tag + "-" + mode + "-" + preset + "-" + motion
					t.Run(suffix, func(t *testing.T) {
						defer i18n.SetLanguage(i18n.English)
						i18n.SetLanguage(l.lang)
						renderSnapshotMatrix(t, dir, suffix, dark, preset, motion)
					})
				}
			}
		}
	}
	validateVisualSnapshotPNGs(t, dir)
}

func renderSnapshotMatrix(t *testing.T, dir, suffix string, dark bool, preset, motion string) {
	core := preset == "spatial_3d" && motion == "full"
	coreFrame := func(name string) bool {
		return name == "01-leader" || name == "02-omnibox" ||
			name == "06-ai" || name == "06a-ai-streaming" || name == "06b-ai-review" ||
			name == "06c-ai-error" || name == "06d-ai-followup" || name == "06e-ai-pending" ||
			(len(name) >= 2 && name[:2] == "09") || (len(name) >= 2 && name[:2] == "18")
	}
	if core {
		t.Log("snapshot coverage: full matrix for core frames; non-core frames are narrowed to spatial_3d/full")
	} else {
		t.Log("snapshot coverage narrowed: Leader, Omnibox, AI, Settings General, and all onboarding frames only; non-core frames omitted outside spatial_3d/full")
	}
	doer := mockHTTPDoer{fn: func(req *http.Request) (*http.Response, error) {
		return sseResponse("这段代码的作用是：", "\\n\\n1. 读取配置文件；", "\\n2. 校验字段并返回错误；", "\\n3. 原子写回磁盘。"), nil
	}}
	f := newInteractionFixture(t, doer)
	// Clipboard rows show "source · relative time · N lines": a fixed clock and
	// back-dated captures keep that meta line identical from run to run.
	snapNow := time.Date(2026, 11, 20, 15, 4, 0, 0, time.Local)
	restoreClock := clipboardClock
	clipboardClock = func() time.Time { return snapNow }
	defer func() { clipboardClock = restoreClock }()
	ctxRecord := context.Background()
	mediaPath := filepath.Join(t.TempDir(), "preview.png")
	if err := os.WriteFile(mediaPath, testPNG16(t), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, clip := range []clipboard.CaptureCandidate{
		{Content: "https://github.com/yeisme/clavis/pull/12", SourceAppName: "Safari", Timestamp: snapNow.AddDate(0, 0, -4)},
		{Content: "preview-media-deck", SourceAppName: "Photos", Timestamp: snapNow.Add(-26 * time.Hour),
			Media: clipboard.Media{Kind: clipboard.KindImage, BlobSHA: "deck-sha", MediaPath: mediaPath, ThumbPath: mediaPath, Width: 1920, Height: 1080, ByteSize: 2400000}},
		{Content: "git commit -m \"feat: 输入法候选窗\"", SourceAppName: "Terminal", Timestamp: snapNow.Add(-3 * time.Hour)},
		{Content: "微信支付回调签名校验失败，请检查商户证书与 APIv3 密钥是否匹配，然后重新发起退款请求。", Timestamp: snapNow.Add(-18 * time.Minute)},
		{Content: "#3B82F6", SourceAppName: "Figma", Timestamp: snapNow.Add(-2 * time.Minute)},
		{Content: "SELECT id, content, kind\nFROM clipboard_items\nWHERE kind = 'image'\nORDER BY updated_at DESC\nLIMIT 20;", SourceAppName: "Notes", Timestamp: snapNow.Add(-20 * time.Second)},
	} {
		if _, _, err := f.clips.Record(ctxRecord, clip); err != nil {
			t.Fatal(err)
		}
	}
	lang := i18n.Current()
	_ = f.ctrl.UpdateMainConfig(func(m *config.MainConfig) {
		m.Appearance.VisualPreset = preset
		m.Appearance.Animations = motion
		m.Appearance.ObliqueRows = true
		// Saving the config re-resolves [appearance].language; pin it to the
		// subtest's language or every "zh" frame silently renders English.
		m.Appearance.Language = string(lang)
	})
	appearance := f.ctrl.Config().Main.Appearance
	ctx := &engine.CapsuleContext{
		AppName:          "Visual Studio Code",
		SelectedText:     "func main() {}",
		ClipboardPreview: "go test ./uiapp/... -count=1",
	}

	// Seed deterministic real Agent session data on f.ctrl so 01-leader renders
	// live System Status and Recent Activity across classic/liquid_glass/spatial_3d.
	f.ctrl.agentMu.Lock()
	f.ctrl.agentRefreshStarted = true
	f.ctrl.agentSessions = []agentsession.Session{
		{
			ID:             "dsh-leader-01",
			Provider:       "dsh",
			Title:          "AI 交互中心五态改写验收",
			Status:         agentsession.StatusWaiting,
			PendingReview:  true,
			LastActivityAt: snapNow.Add(-4 * time.Minute),
			LastMessage:    "已完成 5 态渲染快照与 IME 回归测试，等待验收",
		},
	}
	f.ctrl.agentDiagnostics = nil
	setAgentOperationResult(f.ctrl, host.OperationResult{State: host.StateReady, ObservedAt: snapNow})
	f.ctrl.agentMu.Unlock()

	shoot := func(name string, view func(c *ui.Context), w, h int, scale float32, drive func(tt *ui.Tester)) {
		t.Helper()
		if !core && !coreFrame(name) {
			return
		}
		tt := ui.NewTester(view, w, h)
		tt.SetDark(dark)
		tt.SetScale(scale)
		tt.Frame()
		if drive != nil {
			drive(tt)
		}
		tt.Advance(2 * time.Second)
		tt.Frame()
		out, err := os.Create(filepath.Join(dir, name+"-"+suffix+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		if err := png.Encode(out, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}

	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader, Context: ctx})
	w, h := PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("01-leader", f.ctrl.CapsuleView, w, h, 2, nil)
	// Inside a group (AI rewrite): the back button, the group title and a
	// window sized for its two rows.
	f.ctrl.Dispatch(engine.Event{Type: engine.EventLeaderKeyPress, Key: "a"})
	gw, gh := PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("01b-leader-group", f.ctrl.CapsuleView, gw, gh, 2, nil)

	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: ctx})
	w, h = PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("02-omnibox", f.ctrl.CapsuleView, w, h, 2, nil)

	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: ctx})
	shoot("03-omnibox-query", f.ctrl.CapsuleView, w, h, 2, func(tt *ui.Tester) {
		tt.Type("fy")
		tt.Frame()
	})

	// Tab arbitration (spec §3.1): a query the chosen row completes shows
	// the rest of its title muted and "Tab Complete" in the footer; one it
	// does not shows "⇧Tab Switch tab".
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: ctx})
	probe := []rune(f.ctrl.State().OmniboxItems[0].Title)
	shoot("03d-omnibox-completion", f.ctrl.CapsuleView, w, h, 2, func(tt *ui.Tester) {
		tt.Type(string(probe[:min(3, len(probe)-1)]))
		tt.Frame()
		tt.Frame()
	})
	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: ctx})
	shoot("03e-omnibox-no-completion", f.ctrl.CapsuleView, w, h, 2, func(tt *ui.Tester) {
		tt.Type("zqx")
		tt.Frame()
	})

	f.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: ctx})
	shoot("03b-omnibox-hover", f.ctrl.CapsuleView, w, h, 2, func(tt *ui.Tester) {
		tt.Move(float32(w/2), float32(h/2+20))
		tt.Frame()
	})
	shoot("03c-omnibox-focused", f.ctrl.CapsuleView, w, h, 2, func(tt *ui.Tester) {
		tt.Key(0, ui.KeyDown)
		tt.Frame()
	})
	shoot("04-omnibox-alt-held", f.ctrl.CapsuleView, w, h, 2, func(tt *ui.Tester) {
		tt.HoldModifiers(ui.Alt)
		tt.Frame()
	})

	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	waitUntil(t, 2*time.Second, func() bool { return len(f.ctrl.State().OmniboxItems) >= 5 }, "clipboard items")
	w, h = PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("05-clipboard", f.ctrl.CapsuleView, w, h, 2, func(tt *ui.Tester) {
		tt.Key(0, ui.KeyDown)
		tt.Key(0, ui.KeyDown)
		tt.Frame()
	})
	f.ctrl.OpenOmnibox(config.TabClipboard, "#3B82F6")
	w, h = PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("05b-hex-colour", f.ctrl.CapsuleView, w, h, 2, nil)
	f.ctrl.OpenOmnibox(config.TabClipboard, "")
	for idx, candidate := range f.ctrl.State().OmniboxItems {
		if candidate.ClipboardMedia != nil && candidate.ClipboardMedia.IsMedia() {
			f.ctrl.SelectIndex(idx)
			break
		}
	}
	w, h = PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("05c-preview-media-deck", f.ctrl.CapsuleView, w, h, 2, nil)

	// Agent Workbench snapshots: parallel task lanes (list), approval blocking gate,
	// completed state, and redacted failure/retry state.
	agentSessionsFixture := []agentsession.Session{
		{
			ID:             "cx-build-01",
			Provider:       "codex",
			Title:          "输入法候选窗 GPU 排版优化",
			Status:         agentsession.StatusRunning,
			LastActivityAt: snapNow.Add(-1 * time.Minute),
			LastMessage:    "正在运行 go test ./uiapp/... 并核对 12pt 基准线",
			Workspace:      "/Users/alice/workspace/clavis",
			TargetRef:      "branch:main",
			Source:         "codex-sqlite",
		},
		{
			ID:             "dsh-review-02",
			Provider:       "dsh",
			Title:          "AI 交互中心五态改写验收",
			Status:         agentsession.StatusWaiting,
			PendingReview:  true,
			LastActivityAt: snapNow.Add(-6 * time.Minute),
			LastMessage:    "已完成 5 态渲染快照与 IME 回归测试，等待人工验收合并",
			Workspace:      "/Users/alice/workspace/clavis",
			TargetRef:      "commit:fbf5b15",
			Source:         "dsh-thread",
		},
		{
			ID:             "cl-done-03",
			Provider:       "claude",
			Title:          "剪贴板 SQLite 索引与脱敏审计",
			Status:         agentsession.StatusStopped,
			LastActivityAt: snapNow.Add(-42 * time.Minute),
			LastMessage:    "全部 18 项脱敏与 FTS5 查询用例通过，无敏感信息泄漏",
			Workspace:      "/Users/alice/workspace/clavis",
			TargetRef:      "pr:#42",
			Source:         "claude-project",
		},
	}
	aw := newInteractionFixture(t, doer)
	_ = aw.ctrl.UpdateMainConfig(func(m *config.MainConfig) {
		m.Appearance.VisualPreset = preset
		m.Appearance.Animations = motion
		m.Appearance.ObliqueRows = true
		m.Appearance.Language = string(lang)
	})
	// Let NewAppController's startup refresh and TabAgent open refresh settle
	// before seeding deterministic workbench sessions so background discovery
	// cannot race the rendered snapshots.
	waitUntil(t, 3*time.Second, func() bool {
		r := aw.ctrl.agentRecordedOperationResult()
		return !aw.ctrl.agentRefreshing() && r.State != "" && r.State != host.StateRefreshing
	}, "startup agent refresh settle")
	beforeAgentOpen := aw.ctrl.agentRecordedOperationResult().ObservedAt
	aw.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabAgent, Context: ctx})
	waitUntil(t, 3*time.Second, func() bool {
		r := aw.ctrl.agentRecordedOperationResult()
		return !aw.ctrl.agentRefreshing() && r.ObservedAt.After(beforeAgentOpen)
	}, "open agent tab refresh settle")
	aw.ctrl.agentMu.Lock()
	aw.ctrl.agentSessions = agentSessionsFixture
	aw.ctrl.agentDiagnostics = nil
	setAgentOperationResult(aw.ctrl, host.OperationResult{State: host.StateReady, ObservedAt: snapNow})
	aw.ctrl.agentMu.Unlock()
	w, h = PreferredCapsuleSizeForState(aw.ctrl.State(), appearance)
	aw.ctrl.mu.Lock()
	aw.ctrl.uiState.agentSelected = 0
	aw.ctrl.mu.Unlock()
	shoot("05d-agent-workbench", aw.ctrl.CapsuleView, w, h, 2, nil)

	aw.ctrl.mu.Lock()
	aw.ctrl.uiState.agentSelected = 1
	aw.ctrl.mu.Unlock()
	shoot("05e-agent-approval", aw.ctrl.CapsuleView, w, h, 2, nil)

	aw.ctrl.mu.Lock()
	aw.ctrl.uiState.agentSelected = 2
	aw.ctrl.mu.Unlock()
	shoot("05f-agent-done", aw.ctrl.CapsuleView, w, h, 2, nil)

	// Agent error & redacted diagnostic retry state.
	aw.ctrl.agentMu.Lock()
	aw.ctrl.agentSessions = nil
	aw.ctrl.agentDiagnostics = []agentsession.Diagnostic{
		{Provider: "codex", Code: "read_failed", Message: "token=sk-secretvalue123456 /Users/alice/.codex/state_5.sqlite locked"},
		{Provider: "claude", Code: "unavailable", Message: "/Users/alice/.claude/projects directory unreadable"},
	}
	setAgentOperationResult(aw.ctrl, host.OperationResult{
		State:      host.StateFailed,
		Code:       "read_failed",
		Message:    "Codex、Claude 数据源不可用",
		Retryable:  true,
		ObservedAt: snapNow,
	})
	aw.ctrl.agentMu.Unlock()
	aw.ctrl.mu.Lock()
	aw.ctrl.uiState.agentSelected = 0
	aw.ctrl.mu.Unlock()
	shoot("05g-agent-error", aw.ctrl.CapsuleView, w, h, 2, nil)

	// AI active streaming (running) + clean completion (done) via a real held pipe
	// so 06a-ai-streaming deterministically captures the live step log & Stop CTA.
	applySnapAppearance := func(ctrl *AppController) {
		_ = ctrl.UpdateMainConfig(func(m *config.MainConfig) {
			m.Appearance.VisualPreset = preset
			m.Appearance.Animations = motion
			m.Appearance.ObliqueRows = true
			m.Appearance.Language = string(lang)
		})
	}
	streamRelease := make(chan struct{})
	sf := newInteractionFixture(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		pr, pw := io.Pipe()
		go func() {
			_, _ = fmt.Fprint(pw, "data: {\"choices\":[{\"delta\":{\"content\":\"这段代码的作用是：\\n\\n1. 读取配置文件；\"}}]}\n\n")
			<-streamRelease
			_, _ = fmt.Fprint(pw, "data: {\"choices\":[{\"delta\":{\"content\":\"\\n2. 校验字段并返回错误；\\n3. 原子写回磁盘。\"}}]}\n\ndata: [DONE]\n\n")
			_ = pw.Close()
		}()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       pr,
		}, nil
	}})
	applySnapAppearance(sf.ctrl)
	sf.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: ctx})
	stTester := ui.NewTester(sf.ctrl.CapsuleView, w, h)
	stTester.Frame()
	stTester.Type("?解释这段代码")
	stTester.Frame()
	stTester.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		st := sf.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && !st.AIComplete && st.AIOutput != ""
	}, "AI active streaming chunk")
	w, h = PreferredCapsuleSizeForState(sf.ctrl.State(), appearance)
	shoot("06a-ai-streaming", sf.ctrl.CapsuleView, w, h, 2, nil)
	close(streamRelease)
	waitUntil(t, 2*time.Second, func() bool {
		st := sf.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "AI completion")
	w, h = PreferredCapsuleSizeForState(sf.ctrl.State(), appearance)
	shoot("06-ai", sf.ctrl.CapsuleView, w, h, 2, nil)

	// AI rewrite review: a close edit opens on the awaiting_human blocking approval gate.
	rf := newInteractionFixture(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		return sseResponse("这段代码负责读取配置文件，并校验所有字段后原子写回磁盘。"), nil
	}})
	applySnapAppearance(rf.ctrl)
	rf.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: &engine.CapsuleContext{
		AppName: "Notes", SelectedText: "这段代码的作用是读取配置文件，然后校验字段并写回磁盘。",
	}})
	rt := ui.NewTester(rf.ctrl.CapsuleView, w, h)
	rt.Frame()
	rt.Type("?润色")
	rt.Frame()
	rt.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		st := rf.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "AI review completion")
	w, h = PreferredCapsuleSizeForState(rf.ctrl.State(), appearance)
	shoot("06b-ai-review", rf.ctrl.CapsuleView, w, h, 2, nil)

	// AI failure & recovery state: error banner, retry CTA (⌘R), and editable retry input.
	ef := newInteractionFixture(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	}})
	applySnapAppearance(ef.ctrl)
	ef.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: ctx})
	et := ui.NewTester(ef.ctrl.CapsuleView, w, h)
	et.Frame()
	et.Type("?分析这段配置加载超时原因")
	et.Frame()
	et.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		st := ef.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIError != ""
	}, "AI error state")
	w, h = PreferredCapsuleSizeForState(ef.ctrl.State(), appearance)
	shoot("06c-ai-error", ef.ctrl.CapsuleView, w, h, 2, nil)

	// AI multi-turn follow-up state: Turn 1 history + Turn 2 prompt + refined output.
	turn := 0
	mf := newInteractionFixture(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		turn++
		if turn == 1 {
			return sseResponse("这段代码负责读取配置、校验字段并原子写回磁盘。"), nil
		}
		return sseResponse("浓缩版：", "\\n- 读取配置", "\\n- 校验字段", "\\n- 原子落盘"), nil
	}})
	applySnapAppearance(mf.ctrl)
	mf.ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Context: ctx})
	mt := ui.NewTester(mf.ctrl.CapsuleView, w, h)
	mt.Frame()
	mt.Type("?解释这段代码")
	mt.Frame()
	mt.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		st := mf.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "AI turn 1 completion")
	mf.ctrl.Dispatch(engine.Event{Type: engine.EventFollowUpAI, Prompt: "用三点极简总结"})
	waitUntil(t, 2*time.Second, func() bool {
		st := mf.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete && len(st.AIHistory) >= 2
	}, "AI turn 2 follow-up completion")
	w, h = PreferredCapsuleSizeForState(mf.ctrl.State(), appearance)
	shoot("06d-ai-followup", mf.ctrl.CapsuleView, w, h, 2, nil)

	// AI pending/empty state: ready to compose a multi-line prompt before sending.
	pf := newInteractionFixture(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		return sseResponse("ok"), nil
	}})
	applySnapAppearance(pf.ctrl)
	pwAI, phAI := PreferredCapsuleSize(engine.PhaseAIStreaming, appearance)
	shoot("06e-ai-pending", func(c *ui.Context) {
		cfgSnap := pf.ctrl.Config()
		theme := resolveAppearanceTheme(c, cfgSnap.Main.Appearance.Theme)
		c.SetTheme(theme)
		pendingState := engine.State{
			Phase:   engine.PhaseAIStreaming,
			AIMode:  "",
			Context: *ctx,
		}
		radius := NormalizeCornerRadius(cfgSnap.Main.Appearance)
		card := applyCapsuleCardSurface(c, applyTextLocale(ui.Column(c).Fill().Radius(radius).Clip()), theme, cfgSnap.Main.Appearance)
		card.Children(func() {
			pf.ctrl.renderCapsuleHeader(c, theme, cfgSnap, pendingState, pf.ctrl.Diagnostics())
			ui.Divider(c)
			pf.ctrl.renderAIStreamingView(c, theme, cfgSnap, pendingState)
		})
	}, pwAI, phAI, 2, nil)

	// Input Composer snapshots (6 distinct states: plain, multiline, @mention menu,
	// /command menu, context chips, and running/locked state).
	if core {
		renderComposerSnapshot := func(name string, st engine.State, draft string, chips []composerChip, agents []agentsession.Session) {
			cf := newInteractionFixture(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
				return sseResponse("ok"), nil
			}})
			applySnapAppearance(cf.ctrl)
			if len(agents) > 0 {
				cf.ctrl.agentMu.Lock()
				cf.ctrl.agentRefreshStarted = true
				cf.ctrl.agentSessions = agents
				cf.ctrl.agentMu.Unlock()
			}
			cf.ctrl.uiState.followUpQuery = draft
			resetComposerState(cf.ctrl)
			for _, ch := range chips {
				cf.ctrl.addComposerChip(ch)
			}
			shoot(name, func(c *ui.Context) {
				cfgSnap := cf.ctrl.Config()
				theme := resolveAppearanceTheme(c, cfgSnap.Main.Appearance.Theme)
				c.SetTheme(theme)
				radius := NormalizeCornerRadius(cfgSnap.Main.Appearance)
				card := applyCapsuleCardSurface(c, applyTextLocale(ui.Column(c).Fill().Radius(radius).Clip()), theme, cfgSnap.Main.Appearance)
				card.Children(func() {
					cf.ctrl.renderCapsuleHeader(c, theme, cfgSnap, st, cf.ctrl.Diagnostics())
					ui.Divider(c)
					cf.ctrl.renderAIStreamingView(c, theme, cfgSnap, st)
				})
			}, pwAI, phAI, 2, nil)
		}

		composerCtx := engine.CapsuleContext{
			AppName:          "Visual Studio Code",
			SelectedText:     "func loadConfig(path string) (Config, error) { ... }",
			ClipboardPreview: "go test ./uiapp/... -count=1",
		}
		doneState := engine.State{
			Phase:        engine.PhaseAIStreaming,
			AIMode:       "",
			AIPrompt:     "解释这段配置加载函数",
			AISourceText: composerCtx.SelectedText,
			AIOutput:     "该函数从磁盘读取配置并执行字段校验，失败时返回具体路径错误。",
			AIComplete:   true,
			Context:      composerCtx,
		}
		sampleChips := []composerChip{
			{ID: "selection", Kind: "selection", Icon: "text.badge.plus", Label: "选中文本", Detail: "52", Payload: composerCtx.SelectedText},
			{ID: "clipboard", Kind: "clipboard", Icon: "doc.on.clipboard", Label: "剪贴板", Detail: "go test ./uiapp/…", Payload: composerCtx.ClipboardPreview},
			{ID: "app:Visual Studio Code", Kind: "app", Icon: "macwindow", Label: "Visual Studio Code", Payload: "Active application: Visual Studio Code"},
		}
		renderComposerSnapshot("06f-composer-plain", doneState, "再帮我补一个错误分支的单元测试", nil, nil)
		renderComposerSnapshot("06g-composer-multiline", doneState, "请按以下 3 点改写：\n1. 使用 errors.Is 区分文件不存在\n2. 补充超时上下文\n3. 输出最小 diff", nil, nil)
		renderComposerSnapshot("06h-composer-mention", doneState, "请结合 @", nil, agentSessionsFixture[:2])
		renderComposerSnapshot("06i-composer-slash", doneState, "/", nil, nil)
		renderComposerSnapshot("06j-composer-chips", doneState, "结合这些上下文给出最小修复方案", sampleChips, nil)
		runningState := engine.State{
			Phase:        engine.PhaseAIStreaming,
			AIMode:       "",
			AIPrompt:     "结合这些上下文给出最小修复方案",
			AISourceText: composerCtx.SelectedText,
			AIOutput:     "正在结合选中代码与剪贴板日志生成修复方案：\n\n1. 先校验文件读取权限；",
			AIComplete:   false,
			Context:      composerCtx,
		}
		renderComposerSnapshot("06k-composer-running", runningState, "", sampleChips[:2], nil)
	}

	f.ctrl.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: i18n.T("hud.copied")})
	w, h = PreferredCapsuleSizeForState(f.ctrl.State(), appearance)
	shoot("07-hud", f.ctrl.CapsuleView, w, h, 2, nil)
	f.ctrl.Dispatch(engine.Event{Type: engine.EventDismiss})

	for i, label := range []string{"中", "EN", "LAYER • NAV", "已绑定 Notes → 英文"} {
		lbl := label
		pw, ph := PreferredPillSizeForLabel(lbl)
		shoot("08-pill-"+string(rune('a'+i)), func(c *ui.Context) { IMEPillView(c, lbl) }, pw, ph, 3, nil)
	}

	// Own input engine candidate window: a mid-composition page with the
	// selection on the first cell and an AI suggestion, in both orientations.
	view := inputengine.View{
		Composing: true,
		Preedit:   "shu'ru'fa",
		Candidates: []inputengine.Candidate{
			{Text: "输入法"}, {Text: "书如法"}, {Text: "输入"}, {Text: "舒"}, {Text: "输"},
			{Text: "输入法候选窗", AI: true},
		},
		Selected: 0,
		Page:     0,
		HasNext:  true,
	}
	for _, orientation := range []string{"horizontal", "vertical"} {
		app := appearance
		app.CandidateBarOrientation = orientation
		cw, ch := PreferredCandidatePanelSizeFor(view, panelOf(app))
		shoot("11-candidates-"+orientation, func(c *ui.Context) { InputCandidateViewFor(c, view, panelOf(app)) }, cw, ch, 2, nil)
	}

	f.ctrl.OpenSettings(SettingsTabGeneral)
	shoot("09-settings-general", f.ctrl.SettingsView, 960, 640, 1.5, nil)
	shoot("09b-settings-general-full", f.ctrl.SettingsView, 960, 1000, 1.5, nil)
	f.ctrl.OpenSettings(SettingsTabIMEKeymap)
	shoot("10-settings-ime", f.ctrl.SettingsView, 960, 640, 1.5, nil)
	shoot("10b-settings-ime-full", f.ctrl.SettingsView, 960, 1500, 1.5, nil)
	// The window's minimum size: two narrow columns.
	shoot("10c-settings-ime-min", f.ctrl.SettingsView, 760, 500, 1.5, nil)
	// A declared workflow so the Workflows editor snapshot shows its fields
	// rather than the empty state.
	if err := f.ctrl.ConfigStore.SaveWorkflows(config.WorkflowsConfig{Workflows: []config.Workflow{{
		ID:          "summarize-selection",
		Name:        "Summarize Selection",
		Description: "Rewrite the selection into bullet points.",
		Enabled:     true,
		Source:      config.WorkflowSourceSelection,
		Output:      config.WorkflowOutputReplace,
		Failure:     config.WorkflowFailureStop,
		Execution:   config.WorkflowExecutionSequential,
		Steps: []config.WorkflowStep{{
			ID: "draft", Name: "Draft", Action: "script.echo", Input: "text",
			Enabled: true, Source: config.WorkflowSourceSelection, Output: config.WorkflowOutputReplace,
			Failure: config.WorkflowFailureStop, Execution: config.WorkflowExecutionSequential,
		}},
	}}}); err != nil {
		t.Fatalf("seed workflows: %v", err)
	}
	for _, sec := range []struct {
		name  string
		index int
	}{
		{"16-settings-permissions-telemetry", settingsSectionPermissions},
		{"12-settings-glossary-clipboard", settingsSectionGlossary},
		{"13-settings-leader", settingsSectionLeader},
		{"14-settings-snippets", settingsSectionSnippets},
		{"15-settings-ai", settingsSectionAI},
		{"17-settings-configs", settingsSectionConfigs},
		{"19-settings-workflows", settingsSectionWorkflows},
	} {
		f.ctrl.setSettingsSection(sec.index)
		shoot(sec.name, f.ctrl.SettingsView, 960, 900, 1.5, nil)
	}

	// Password / Security Vault UI snapshots: metadata-only list plus locked, missing, denied states.
	if _, err := f.ctrl.VaultPutSecret("snap-vault-1", "clavis.prod.db", "Production DB", secrets.SecretKindPassword, "snap-secret-never-rendered"); err == nil {
		f.ctrl.setSettingsSection(settingsSectionSecurity)
		shoot("22-settings-security", f.ctrl.SettingsView, 960, 900, 1.5, nil)
		st := vaultUIState(f.ctrl)
		st.mu.Lock()
		st.setStatusLocked(vaultStatusLocked, secrets.CopyErrorKeychainLocked, pickLang(i18n.IsChinese(), "钥匙串已锁定；解锁后请重试 TTL 复制。", "Keychain is locked; unlock and retry TTL Copy."))
		st.mu.Unlock()
		shoot("22b-settings-security-locked", f.ctrl.SettingsView, 960, 900, 1.5, nil)
		st.mu.Lock()
		st.setStatusLocked(vaultStatusMissing, secrets.CopyErrorRefNotFound, pickLang(i18n.IsChinese(), "未找到 SecretRef 或底层凭据，请重新写入。", "Missing SecretRef or backing credential; re-add reference."))
		st.mu.Unlock()
		shoot("22c-settings-security-missing", f.ctrl.SettingsView, 960, 900, 1.5, nil)
		_, _ = f.ctrl.VaultAgentCopyAttempt("snap-vault-1")
		shoot("22d-settings-security-denied", f.ctrl.SettingsView, 960, 900, 1.5, nil)
		st.mu.Lock()
		st.clearStatusLocked()
		st.mu.Unlock()
	}

	// The two other floating dialogs: the prompt composer (header, slots,
	// preview, footer buttons) and the selection action bar.
	composerTpl := config.PromptTemplate{
		ID: "snap-review", Trigger: "/cr", Title: "代码审查 Code Review",
		Template: "请审查以下 {{language}} 代码，重点关注{{focus}}。\n\n代码：\n{{code}}",
		Slots: []config.PromptSlot{
			{Name: "language", Label: "语言 Language", Kind: "select", Options: []string{"Go", "TypeScript", "Python"}, Default: "Go"},
			{Name: "focus", Label: "关注点 Focus", Kind: "text", Default: "并发安全"},
			{Name: "code", Label: "代码 Code", Kind: "text", Required: true},
		},
	}
	f.ctrl.OpenPromptComposer(composerTpl, engine.CapsuleContext{AppName: "Visual Studio Code"})
	cw, ch := PreferredPromptComposerPanelSize(len(composerTpl.Slots))
	shoot("20-prompt-composer", f.ctrl.PromptComposerView, cw, ch, 2, nil)
	f.ctrl.ClosePromptComposer()
	selBar := SelectionBarPayload{Text: "func main() {}", Actions: []string{"optimize_prompt", "explain", "translate", "polish", "copy"}}
	bw, bh := SelectionBarSize(selBar)
	shoot("21-selection-bar", func(c *ui.Context) { SelectionBarView(c, selBar, appearance) }, bw, bh, 2, nil)

	// The first-run guide, from a fresh Mac to a working Leader key, its
	// smallest window, and Windows.
	guide := func(goos string) func(c *ui.Context) {
		return func(c *ui.Context) { f.ctrl.renderOnboarding(c, goos) }
	}
	_ = f.ctrl.Stop()
	f.bridge.mu.Lock()
	f.bridge.perms = host.PermissionStatus{}
	f.bridge.mu.Unlock()
	_, _ = f.ctrl.RefreshPermissions(context.Background())
	shoot("18-onboarding-fresh", guide("darwin"), 520, 520, 1.5, nil)
	shoot("18b-onboarding-min", guide("darwin"), 520, 520, 1.5, nil)
	f.bridge.mu.Lock()
	f.bridge.perms = host.PermissionStatus{Accessibility: true, InputMonitoring: true, Granted: true}
	f.bridge.mu.Unlock()
	_, _ = f.ctrl.RefreshPermissions(context.Background())
	f.ctrl.SetRelaunchNeeded(true)
	shoot("18c-onboarding-relaunch", guide("darwin"), 520, 520, 1.5, nil)
	_ = f.ctrl.Start(context.Background())
	shoot("18d-onboarding-waiting", guide("darwin"), 520, 520, 1.5, nil)
	f.ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerLeaderKey})
	f.ctrl.Dispatch(engine.Event{Type: engine.EventDismiss})
	shoot("18e-onboarding-ready", guide("darwin"), 520, 520, 1.5, nil)
	shoot("18f-onboarding-windows", guide("windows"), 520, 520, 1.5, nil)
}
