package uiapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	internalagentsession "github.com/yeisme/clavis/internal/agentsession"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
	"github.com/yeisme/clavis/pkg/core/i18n"
	"github.com/yeisme/clavis/pkg/core/terminal"
)

type agentCommand struct{ ID, Label, Detail string }

type agentCommandSpec struct {
	ID       string
	LabelZH  string
	LabelEN  string
	DetailZH string
	DetailEN string
}

var agentFocuserMu sync.RWMutex
var agentFocuser agentsession.Focuser = agentsession.NewHerdrFocuser()

// agentCommands retains the default command catalog referenced by agent_workbench_view.go.
var agentCommands = []agentCommand{
	{ID: "continue", Label: "Continue current task", Detail: "Resume the most recent work turn"},
	{ID: "review", Label: "Summarize pending review", Detail: "List items awaiting review in this session"},
	{ID: "test", Label: "Run tests & report evidence", Detail: "Request test results and key output"},
}

var agentCommandSpecs = []agentCommandSpec{
	{
		ID:       "continue",
		LabelZH:  "继续当前任务",
		LabelEN:  "Continue current task",
		DetailZH: "继续最近一轮工作",
		DetailEN: "Resume the most recent work turn",
	},
	{
		ID:       "review",
		LabelZH:  "总结待验收项",
		LabelEN:  "Summarize pending review",
		DetailZH: "整理会话中的验收状态",
		DetailEN: "List items awaiting review in this session",
	},
	{
		ID:       "test",
		LabelZH:  "运行测试并回报证据",
		LabelEN:  "Run tests & report evidence",
		DetailZH: "请求测试结果和关键输出",
		DetailEN: "Request test results and key output",
	},
}

func localizedAgentCommands(zh bool) []agentCommand {
	out := make([]agentCommand, len(agentCommandSpecs))
	for i, spec := range agentCommandSpecs {
		out[i] = agentCommand{
			ID:     spec.ID,
			Label:  pickLang(zh, spec.LabelZH, spec.LabelEN),
			Detail: pickLang(zh, spec.DetailZH, spec.DetailEN),
		}
	}
	return out
}

type agentOutputSnapshot struct {
	Target    string
	Content   string
	Loading   bool
	ErrText   string
	FetchedAt time.Time
}

// agentSessionsSubState holds per-controller Agent operation result and Herdr
// output cache state inside AppController.subStates.
type agentSessionsSubState struct {
	mu          sync.Mutex
	hasOpResult bool
	opResult    host.OperationResult
	outputs     map[string]agentOutputSnapshot
	inFlight    map[string]bool
}

func (c *AppController) currentHerdrExecutor() agentsession.CommandExecutor {
	c.agentMu.RLock()
	exec := c.herdrExecutor
	c.agentMu.RUnlock()
	if exec == nil {
		return agentsession.NewCommandExecutor()
	}
	return exec
}

func (c *AppController) cachedAgentWorkspaces() []agentsession.HerdrWorkspace {
	c.agentMu.RLock()
	defer c.agentMu.RUnlock()
	return append([]agentsession.HerdrWorkspace(nil), c.agentWorkspaces...)
}

func (c *AppController) cachedAgentOutput(target string) agentOutputSnapshot {
	target = strings.TrimSpace(target)
	if target == "" {
		return agentOutputSnapshot{}
	}
	st := c.agentSessionsStateFor()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.outputs == nil {
		return agentOutputSnapshot{Target: target}
	}
	return st.outputs[target]
}

// EnsureAgentOutput triggers an asynchronous read of recent terminal output for
// a Herdr pane/agent target when missing or stale (> 3s), never blocking UI frames.
func (c *AppController) EnsureAgentOutput(target string) {
	c.fetchAgentOutput(target, false)
}

// ForceRefreshAgentOutput bypasses the 3s freshness window and pulls fresh output.
func (c *AppController) ForceRefreshAgentOutput(target string) {
	c.fetchAgentOutput(target, true)
}

func (c *AppController) fetchAgentOutput(target string, force bool) {
	target = strings.TrimSpace(target)
	if target == "" || c.isClosed() {
		return
	}
	st := c.agentSessionsStateFor()
	st.mu.Lock()
	if st.outputs == nil {
		st.outputs = make(map[string]agentOutputSnapshot)
	}
	if st.inFlight == nil {
		st.inFlight = make(map[string]bool)
	}
	if st.inFlight[target] {
		st.mu.Unlock()
		return
	}
	existing := st.outputs[target]
	if !force && existing.ErrText == "" && !existing.FetchedAt.IsZero() && time.Since(existing.FetchedAt) < 3*time.Second {
		st.mu.Unlock()
		return
	}
	st.inFlight[target] = true
	existing.Target = target
	existing.Loading = true
	st.outputs[target] = existing
	st.mu.Unlock()

	exec := c.currentHerdrExecutor()
	lifeCtx := c.lifeCtx()
	go func() {
		ctx, cancel := context.WithTimeout(lifeCtx, 1500*time.Millisecond)
		defer cancel()
		text, _, err := agentsession.NewHerdrClientWithExecutor(exec).ReadOutput(ctx, target, 80)

		st.mu.Lock()
		if lifeCtx.Err() == nil && st.outputs != nil {
			snap := agentOutputSnapshot{
				Target:    target,
				Content:   text,
				Loading:   false,
				FetchedAt: time.Now(),
			}
			if err != nil {
				snap.ErrText = agentsession.SanitizeDiagnosticText(err.Error())
				if snap.Content == "" {
					snap.Content = existing.Content
				}
			}
			st.outputs[target] = snap
		}
		if st.inFlight != nil {
			delete(st.inFlight, target)
		}
		st.mu.Unlock()

		if lifeCtx.Err() != nil || c.isClosed() {
			return
		}
		c.mu.Lock()
		repaint := c.OnRepaint
		c.mu.Unlock()
		if repaint != nil {
			repaint()
		}
	}()
}

var (
	agentCodexDBPathMu sync.RWMutex
	agentCodexDBPath   string

	agentTerminalResolverMu      sync.RWMutex
	agentTerminalResolverFactory = func(cfg config.TerminalConfig) *terminal.Resolver {
		return terminal.NewResolver(cfg)
	}
)

func currentCodexDBPath() string {
	agentCodexDBPathMu.RLock()
	override := strings.TrimSpace(agentCodexDBPath)
	agentCodexDBPathMu.RUnlock()
	if override != "" {
		return override
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "state_5.sqlite")
}

func setAgentCodexDBPathForTest(path string) func() {
	agentCodexDBPathMu.Lock()
	previous := agentCodexDBPath
	agentCodexDBPath = path
	agentCodexDBPathMu.Unlock()
	return func() {
		agentCodexDBPathMu.Lock()
		agentCodexDBPath = previous
		agentCodexDBPathMu.Unlock()
	}
}

func setAgentTerminalResolverForTest(factory func(config.TerminalConfig) *terminal.Resolver) func() {
	agentTerminalResolverMu.Lock()
	previous := agentTerminalResolverFactory
	agentTerminalResolverFactory = factory
	agentTerminalResolverMu.Unlock()
	return func() {
		agentTerminalResolverMu.Lock()
		agentTerminalResolverFactory = previous
		agentTerminalResolverMu.Unlock()
	}
}

func (c *AppController) refreshAgentSessions() {
	lifeCtx := c.lifeCtx()
	if lifeCtx.Err() != nil {
		return
	}
	c.agentMu.Lock()
	if c.agentRefreshStarted {
		c.agentMu.Unlock()
		return
	}
	c.agentRefreshStarted = true
	setAgentOperationResult(c, host.OperationResult{State: host.StateRefreshing, ObservedAt: time.Now()})
	c.agentMu.Unlock()

	db := currentCodexDBPath()
	var codexReader agentsession.CodexThreadsReader
	if reader, err := internalagentsession.NewCodexSQLiteReader(db); err == nil {
		codexReader = reader
	}
	previousSessions, _ := c.cachedAgentData()
	herdrExec := c.currentHerdrExecutor()
	sessions, diagnostics := agentsession.Discover(lifeCtx, agentsession.Options{
		CodexDB:       db,
		CodexReader:   codexReader,
		HerdrExecutor: herdrExec,
	})
	if lifeCtx.Err() != nil {
		c.agentMu.Lock()
		c.agentRefreshStarted = false
		c.agentMu.Unlock()
		return
	}
	var workspaces []agentsession.HerdrWorkspace
	if herdrExec != nil {
		wsCtx, wsCancel := context.WithTimeout(lifeCtx, 600*time.Millisecond)
		if ws, wsErr := agentsession.NewHerdrClientWithExecutor(herdrExec).ListWorkspaces(wsCtx); wsErr == nil {
			workspaces = ws
		}
		wsCancel()
	}
	if lifeCtx.Err() != nil {
		c.agentMu.Lock()
		c.agentRefreshStarted = false
		c.agentMu.Unlock()
		return
	}
	result := agentOperationResult(sessions, diagnostics, len(previousSessions) > 0)
	if result.State == host.StateUnknown {
		diagnostics = append(diagnostics, agentsession.Diagnostic{
			Provider: "aggregate",
			Code:     "unknown",
			Message:  pickLang(i18n.IsChinese(), "状态未知", "Status unknown"),
		})
	}
	c.agentMu.Lock()
	c.agentSessions = append([]agentsession.Session(nil), sessions...)
	if len(workspaces) > 0 {
		c.agentWorkspaces = workspaces
	}
	c.agentDiagnostics = append([]agentsession.Diagnostic(nil), diagnostics...)
	c.agentRefreshStarted = false
	setAgentOperationResult(c, result)
	allWS := append([]agentsession.HerdrWorkspace(nil), c.agentWorkspaces...)
	c.agentMu.Unlock()

	if c.isClosed() {
		return
	}
	c.syncAgentQuickJumpTargets(allWS, sessions)

	c.mu.Lock()
	repaint := c.OnRepaint
	c.mu.Unlock()
	if repaint != nil {
		repaint()
	}
}

func agentStatusMessage(result host.OperationResult) string {
	zh := i18n.IsChinese()
	switch result.State {
	case host.StateRefreshing:
		return pickLang(zh, "刷新中…", "Refreshing…")
	case host.StateDegraded:
		if result.Message != "" {
			return pickLang(zh, "部分源不可用 · ", "Partial sources unavailable · ") + result.Message
		}
		return pickLang(zh, "部分源不可用", "Partial sources unavailable")
	case host.StateFailed:
		if result.Message != "" {
			return result.Message
		}
		return pickLang(zh, "Agent 刷新失败", "Agent refresh failed")
	case host.StateUnknown:
		return pickLang(zh, "状态未知", "Status unknown")
	default:
		return ""
	}
}

func agentOperationResult(sessions []agentsession.Session, diagnostics []agentsession.Diagnostic, hadCache bool) host.OperationResult {
	result := host.OperationResult{State: host.StateReady, ObservedAt: time.Now()}
	if len(diagnostics) == 0 {
		return result
	}
	result.Code = diagnostics[0].Code
	result.Message = agentDiagnosticSummary(diagnostics)
	result.Retryable = true
	if len(sessions) > 0 || hadCache {
		result.State = host.StateDegraded
		return result
	}
	result.State = host.StateFailed
	return result
}

func agentOperationResultUnknown() host.OperationResult {
	return host.OperationResult{State: host.StateUnknown, ObservedAt: time.Now(), Retryable: false}
}

func agentDiagnosticSummary(diagnostics []agentsession.Diagnostic) string {
	zh := i18n.IsChinese()
	providers := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		provider := agentProviderLabel(diagnostic.Provider)
		if provider != "" {
			providers = append(providers, provider)
		}
	}
	if len(providers) == 0 {
		return pickLang(zh, "Agent 数据源不可用", "Agent data sources unavailable")
	}
	return pickLang(zh,
		strings.Join(providers, "、")+" 数据源不可用",
		strings.Join(providers, ", ")+" source unavailable",
	)
}

func (c *AppController) cachedAgentData() ([]agentsession.Session, []agentsession.Diagnostic) {
	c.agentMu.RLock()
	defer c.agentMu.RUnlock()
	return append([]agentsession.Session(nil), c.agentSessions...), append([]agentsession.Diagnostic(nil), c.agentDiagnostics...)
}

func (c *AppController) agentRefreshing() bool {
	c.agentMu.RLock()
	defer c.agentMu.RUnlock()
	return c.agentRefreshStarted
}

func (c *AppController) agentOperationResult() host.OperationResult {
	st := c.agentSessionsStateFor()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.hasOpResult {
		return st.opResult
	}
	return host.OperationResult{State: host.StateUnknown, ObservedAt: time.Now(), Retryable: false}
}

func setAgentOperationResult(c *AppController, result host.OperationResult) {
	if c == nil {
		return
	}
	st := c.agentSessionsStateFor()
	st.mu.Lock()
	st.opResult = result
	st.hasOpResult = true
	st.mu.Unlock()
}

func (c *AppController) agentRecordedOperationResult() host.OperationResult {
	st := c.agentSessionsStateFor()
	st.mu.Lock()
	has := st.hasOpResult
	res := st.opResult
	st.mu.Unlock()
	if has {
		return res
	}
	if c.agentRefreshing() {
		return host.OperationResult{State: host.StateRefreshing}
	}
	return host.OperationResult{}
}

func (c *AppController) agentVisibleItems(query string) []engine.OmniboxItem {
	sessions, diagnostics := c.cachedAgentData()
	return agentItemsForResult(sessions, diagnostics, query, c.agentRecordedOperationResult())
}

func agentSessionForItem(item engine.OmniboxItem, sessions []agentsession.Session) (agentsession.Session, bool) {
	parts := strings.SplitN(strings.TrimPrefix(item.UtilityValue, "agent-session:"), ":", 2)
	if len(parts) != 2 {
		return agentsession.Session{}, false
	}
	for _, session := range sessions {
		if session.Provider == parts[0] && session.ID == parts[1] {
			return session, true
		}
	}
	return agentsession.Session{}, false
}

func agentRelativeTime(at, now time.Time) string {
	zh := i18n.IsChinese()
	if at.IsZero() {
		return pickLang(zh, "时间未知", "Unknown time")
	}
	d := now.Sub(at)
	if d < time.Minute {
		return pickLang(zh, "刚刚", "Just now")
	}
	if d < time.Hour {
		mins := int(d / time.Minute)
		return pickLang(zh, fmt.Sprintf("%d 分钟前", mins), fmt.Sprintf("%dm ago", mins))
	}
	if d < 24*time.Hour {
		hours := int(d / time.Hour)
		return pickLang(zh, fmt.Sprintf("%d 小时前", hours), fmt.Sprintf("%dh ago", hours))
	}
	days := int(d / (24 * time.Hour))
	return pickLang(zh, fmt.Sprintf("%d 天前", days), fmt.Sprintf("%dd ago", days))
}

func agentStatusLabel(s agentsession.Status) string {
	zh := i18n.IsChinese()
	switch s {
	case agentsession.StatusRunning:
		return pickLang(zh, "运行中", "Running")
	case agentsession.StatusStopped:
		return pickLang(zh, "已停止", "Stopped")
	case agentsession.StatusWaiting:
		return pickLang(zh, "等待中", "Waiting")
	default:
		return pickLang(zh, "未知", "Unknown")
	}
}

func agentProviderLabel(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "codex":
		return "Codex"
	case "claude", "claude-code":
		return "Claude"
	case "dsh":
		return "DSH"
	case "agy", "antigravity":
		return "AGY"
	case "pi":
		return "Pi"
	case "gemini":
		return "Gemini"
	default:
		return p
	}
}

func agentItems(sessions []agentsession.Session, diagnostics []agentsession.Diagnostic, query string) []engine.OmniboxItem {
	return agentItemsForResult(sessions, diagnostics, query, host.OperationResult{})
}

func agentItemsForResult(sessions []agentsession.Session, diagnostics []agentsession.Diagnostic, query string, result host.OperationResult) []engine.OmniboxItem {
	zh := i18n.IsChinese()
	trimmed := strings.TrimSpace(query)
	if strings.HasPrefix(trimmed, "/") {
		rest := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "/")))
		commands := localizedAgentCommands(zh)
		items := make([]engine.OmniboxItem, 0, len(commands))
		for _, command := range commands {
			if rest != "" && !strings.Contains(strings.ToLower(command.Label+" "+command.ID+" "+command.Detail), rest) {
				continue
			}
			items = append(items, engine.OmniboxItem{ID: "agent-command:" + command.ID, Title: command.Label, Subtitle: command.Detail, Icon: "command", Kind: engine.ItemUtility, UtilityValue: "agent-command:" + command.ID})
		}
		return items
	}
	items := make([]engine.OmniboxItem, 0, len(sessions)+len(diagnostics)+1)
	// When refreshing with an empty session list, let capsule_view render the
	// centered loading state instead of emitting a standalone status row.
	refreshingWithNothingToShow := result.State == host.StateRefreshing && len(sessions) == 0
	showStatusRow := trimmed == "" && result.State != "" && result.State != host.StateReady && !refreshingWithNothingToShow
	statusAtTop := showStatusRow && (len(sessions) == 0 || result.State == host.StateFailed || result.State == host.StateUnknown)
	appendStatusRow := func() {
		retryable := result.Retryable && result.State != host.StateUnknown
		items = append(items, engine.OmniboxItem{ID: "agent-status:" + string(result.State), Title: agentStatusMessage(result), Subtitle: agentStatusSubtitle(result), Icon: "info.circle", Kind: engine.ItemUtility, UtilityValue: "agent-refresh-status", KeyHint: map[bool]string{true: "R", false: ""}[retryable]})
	}
	if statusAtTop {
		appendStatusRow()
	}
	for _, session := range sessions {
		searchable := strings.ToLower(strings.Join([]string{session.Title, session.LastMessage, session.Provider, session.Workspace, session.WorkspaceLabel, session.PaneID, session.AgentName, session.ID}, " "))
		if trimmed != "" && !strings.Contains(searchable, strings.ToLower(trimmed)) {
			continue
		}
		subtitle := fmt.Sprintf("%s · %s", agentStatusLabel(session.Status), agentRelativeTime(session.LastActivityAt, time.Now()))
		if ws := safeAgentWorkspaceLabel(session); ws != "" {
			subtitle = ws + " · " + subtitle
		}
		if session.PendingReview {
			subtitle += pickLang(zh, " · 待验收", " · Pending Review")
		}
		if session.LastMessage != "" {
			subtitle += " · " + agentsession.SanitizeDiagnosticText(session.LastMessage)
		}
		items = append(items, engine.OmniboxItem{ID: "agent-session:" + session.Provider + ":" + session.ID, Title: session.Title, Subtitle: subtitle, Keywords: []string{session.Provider, session.Workspace, session.WorkspaceLabel, session.PaneID, session.ID, session.LastMessage}, Icon: "person.crop.circle", Kind: engine.ItemUtility, UtilityValue: "agent-session:" + session.Provider + ":" + session.ID})
	}
	if showStatusRow && !statusAtTop {
		appendStatusRow()
	}
	diagPrefix := pickLang(zh, "诊断：", "Diagnostic: ")
	for _, diagnostic := range diagnostics {
		items = append(items, engine.OmniboxItem{ID: "agent-diagnostic:" + diagnostic.Provider + ":" + diagnostic.Code, Title: agentProviderLabel(diagnostic.Provider), Subtitle: diagPrefix + agentsession.SanitizeDiagnosticText(diagnostic.Message), Icon: "info.circle", Kind: engine.ItemUtility, UtilityValue: "agent-diagnostic"})
	}
	return items
}

func agentStatusSubtitle(result host.OperationResult) string {
	zh := i18n.IsChinese()
	if result.State == host.StateFailed && result.Retryable {
		return pickLang(zh, "点击重试", "Click to retry")
	}
	if result.State == host.StateUnknown {
		return pickLang(zh, "请手动刷新确认", "Refresh manually to verify")
	}
	return ""
}

func safeAgentWorkspaceLabel(session agentsession.Session) string {
	ws := strings.TrimSpace(session.WorkspaceLabel)
	if ws == "" {
		ws = strings.TrimSpace(session.Workspace)
	}
	if ws == "" {
		return ""
	}
	if ws == "~" {
		return ws
	}
	if strings.ContainsAny(ws, `/\`) {
		base := filepath.Base(filepath.Clean(ws))
		if base != "" && base != "." && base != string(filepath.Separator) {
			return base
		}
		return ""
	}
	return agentsession.SanitizeDiagnosticText(ws)
}

func isAgentItem(item engine.OmniboxItem) bool { return strings.HasPrefix(item.ID, "agent-") }

func currentAgentFocuser() agentsession.Focuser {
	agentFocuserMu.RLock()
	defer agentFocuserMu.RUnlock()
	return agentFocuser
}

func setAgentFocuserForTest(f agentsession.Focuser) func() {
	agentFocuserMu.Lock()
	previous := agentFocuser
	agentFocuser = f
	agentFocuserMu.Unlock()
	return func() { agentFocuserMu.Lock(); agentFocuser = previous; agentFocuserMu.Unlock() }
}

func (c *AppController) focusAgentSession(session agentsession.Session) {
	if c.isClosed() {
		return
	}
	zh := i18n.IsChinese()
	focuser := currentAgentFocuser()
	if focuser == nil {
		c.ShowToastOrHUD(pickLang(zh,
			"已复制会话 ID（未检测到 herdr，无法直接跳转）",
			"Copied session ID (herdr not detected, cannot jump directly)",
		))
		return
	}
	lifeCtx := c.lifeCtx()
	go func() {
		ctx, cancel := context.WithTimeout(lifeCtx, 3*time.Second)
		defer cancel()
		available, err := focuser.Available(ctx)
		if lifeCtx.Err() != nil {
			return
		}
		if err != nil || !available {
			c.ShowToastOrHUD(pickLang(zh,
				"已复制会话 ID（未检测到 herdr，无法直接跳转）",
				"Copied session ID (herdr not detected, cannot jump directly)",
			))
			c.openTerminalForAgentFallback(session)
			return
		}
		var (
			result   host.OperationResult
			focusErr error
		)
		if session.PaneID != "" {
			if pf, ok := focuser.(agentsession.PaneFocuser); ok {
				result, focusErr = pf.FocusPane(ctx, session.PaneID)
			} else {
				result, focusErr = focuser.Focus(ctx, session.ID)
			}
		} else {
			result, focusErr = focuser.Focus(ctx, session.ID)
		}
		if lifeCtx.Err() != nil {
			return
		}
		if focusErr != nil || result.State == host.StateFailed {
			message := agentsession.SanitizeDiagnosticText(result.Message)
			if message == "" {
				message = pickLang(zh, "无法直接跳转", "cannot jump directly")
			}
			c.ShowToastOrHUD(pickLang(zh,
				"已复制会话 ID（"+message+"）",
				"Copied session ID ("+message+")",
			))
			return
		}
		if result.State == host.StateUnknown {
			c.ShowToastOrHUD(pickLang(zh,
				"已复制会话 ID（未找到该会话的面板）",
				"Copied session ID (pane not found for this session)",
			))
			return
		}
		c.ShowToastOrHUD(pickLang(zh, "已跳转到该会话", "Focused agent session"))
	}()
}

func (c *AppController) openTerminalForAgentFallback(session agentsession.Session) {
	termCfg := config.DefaultTerminalConfig()
	if c != nil && c.ConfigStore != nil {
		termCfg = c.Config().Main.Terminal
	}
	dir := strings.TrimSpace(session.Workspace)
	if dir != "" && !filepath.IsAbs(dir) {
		dir = ""
	}
	agentTerminalResolverMu.RLock()
	factory := agentTerminalResolverFactory
	agentTerminalResolverMu.RUnlock()
	if factory == nil {
		factory = terminal.NewResolver
	}
	resolver := factory(termCfg)
	if resolver == nil {
		return
	}
	plan, _, err := resolver.Open(dir)
	if err != nil && plan.Profile.BundleID != "" && c != nil {
		c.mu.Lock()
		bridge := c.Bridge
		c.mu.Unlock()
		if bridge != nil {
			_ = bridge.OpenApp(c.lifeCtx(), plan.Profile.BundleID)
		}
	}
}

func (c *AppController) agentItemAction(item engine.OmniboxItem) bool {
	value := item.UtilityValue
	if value == "agent-refresh-status" {
		if c.agentRefreshRetryable() {
			c.AgentRefresh()
		}
		return true
	}
	if strings.HasPrefix(value, "agent-command:") {
		zh := i18n.IsChinese()
		message := ""
		for _, command := range localizedAgentCommands(zh) {
			if value == "agent-command:"+command.ID {
				message = command.Label + "\n" + command.Detail
				break
			}
		}
		if err := c.writeClipboardText(message); err != nil {
			c.ShowToastOrHUD(err.Error())
		} else {
			c.ShowToastOrHUD(pickLang(zh, "已复制待发消息，未发送", "Copied draft command (not sent)"))
		}
		return true
	}
	if !strings.HasPrefix(value, "agent-session:") {
		return false
	}
	parts := strings.SplitN(strings.TrimPrefix(value, "agent-session:"), ":", 2)
	if len(parts) != 2 {
		return true
	}
	var selected agentsession.Session
	c.agentMu.RLock()
	for _, session := range c.agentSessions {
		if session.Provider == parts[0] && session.ID == parts[1] {
			selected = session
			break
		}
	}
	c.agentMu.RUnlock()
	if selected.ID == "" {
		return true
	}
	if err := c.writeClipboardText(selected.ID); err != nil {
		c.ShowToastOrHUD(agentsession.SanitizeDiagnosticText(err.Error()))
		return true
	}
	c.focusAgentSession(selected)
	return true
}

func (c *AppController) syncAgentQuickJumpTargets(workspaces []agentsession.HerdrWorkspace, sessions []agentsession.Session) {
	if c == nil || c.StateMachine == nil {
		return
	}
	var targets []engine.QuickJumpTarget
	for _, ws := range workspaces {
		id := strings.TrimSpace(ws.WorkspaceID)
		if id == "" {
			continue
		}
		lbl := strings.TrimSpace(ws.Label)
		if lbl == "" {
			lbl = id
		}
		targets = append(targets, engine.QuickJumpTarget{
			ID:       "ws:" + id,
			Title:    fmt.Sprintf("Workspace %s · %s", id, lbl),
			Subtitle: fmt.Sprintf("herdr workspace · %s", strings.TrimSpace(ws.AgentStatus)),
			Kind:     "workspace",
			Target:   id,
			Tags:     []string{id, lbl, "herdr"},
		})
	}
	for _, s := range sessions {
		if !s.HerdrManaged || strings.TrimSpace(s.PaneID) == "" {
			continue
		}
		targets = append(targets, engine.QuickJumpTarget{
			ID:       "pane:" + s.PaneID,
			Title:    fmt.Sprintf("%s · %s", s.Title, s.PaneID),
			Subtitle: fmt.Sprintf("%s · %s", agentProviderLabel(s.Provider), safeAgentWorkspaceLabel(s)),
			Kind:     "agent",
			Target:   s.PaneID,
			Tags:     []string{s.PaneID, s.WorkspaceID, s.WorkspaceLabel, s.Provider, s.AgentName},
		})
	}
	c.Dispatch(engine.Event{
		Type:             engine.EventSetQuickJumpTargets,
		QuickJumpTargets: targets,
	})
}

func (c *AppController) AgentRefresh() {
	if c.isClosed() {
		return
	}
	go c.refreshAgentSessions()
}

func (c *AppController) agentRefreshRetryable() bool {
	return c.agentOperationResult().Retryable && c.agentOperationResult().State != host.StateUnknown
}
