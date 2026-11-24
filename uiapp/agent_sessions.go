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
	"github.com/yeisme/clavis/pkg/core/terminal"
)

type agentCommand struct{ ID, Label, Detail string }

var agentOperationResults sync.Map
var agentFocuserMu sync.RWMutex
var agentFocuser agentsession.Focuser = agentsession.NewHerdrFocuser()
var agentLatestResult struct {
	sync.RWMutex
	result host.OperationResult
}

var agentCommands = []agentCommand{
	{ID: "continue", Label: "继续当前任务", Detail: "继续最近一轮工作"},
	{ID: "review", Label: "总结待验收项", Detail: "整理会话中的验收状态"},
	{ID: "test", Label: "运行测试并回报证据", Detail: "请求测试结果和关键输出"},
}

type agentOutputSnapshot struct {
	Target    string
	Content   string
	Loading   bool
	ErrText   string
	FetchedAt time.Time
}

var agentOutputStore = struct {
	mu       sync.Mutex
	byCtl    map[*AppController]map[string]agentOutputSnapshot
	inFlight map[*AppController]map[string]bool
}{
	byCtl:    make(map[*AppController]map[string]agentOutputSnapshot),
	inFlight: make(map[*AppController]map[string]bool),
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
	agentOutputStore.mu.Lock()
	defer agentOutputStore.mu.Unlock()
	m := agentOutputStore.byCtl[c]
	if m == nil {
		return agentOutputSnapshot{Target: target}
	}
	return m[target]
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
	if target == "" {
		return
	}
	agentOutputStore.mu.Lock()
	m := agentOutputStore.byCtl[c]
	if m == nil {
		m = make(map[string]agentOutputSnapshot)
		agentOutputStore.byCtl[c] = m
	}
	inflight := agentOutputStore.inFlight[c]
	if inflight == nil {
		inflight = make(map[string]bool)
		agentOutputStore.inFlight[c] = inflight
	}
	if inflight[target] {
		agentOutputStore.mu.Unlock()
		return
	}
	existing := m[target]
	if !force && existing.ErrText == "" && !existing.FetchedAt.IsZero() && time.Since(existing.FetchedAt) < 3*time.Second {
		agentOutputStore.mu.Unlock()
		return
	}
	inflight[target] = true
	existing.Target = target
	existing.Loading = true
	m[target] = existing
	agentOutputStore.mu.Unlock()

	exec := c.currentHerdrExecutor()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		text, _, err := agentsession.NewHerdrClientWithExecutor(exec).ReadOutput(ctx, target, 80)

		agentOutputStore.mu.Lock()
		if cm := agentOutputStore.byCtl[c]; cm != nil {
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
			cm[target] = snap
		}
		if inf := agentOutputStore.inFlight[c]; inf != nil {
			delete(inf, target)
		}
		agentOutputStore.mu.Unlock()

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
	sessions, diagnostics := agentsession.Discover(context.Background(), agentsession.Options{
		CodexDB:       db,
		CodexReader:   codexReader,
		HerdrExecutor: herdrExec,
	})
	var workspaces []agentsession.HerdrWorkspace
	if herdrExec != nil {
		wsCtx, wsCancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
		if ws, wsErr := agentsession.NewHerdrClientWithExecutor(herdrExec).ListWorkspaces(wsCtx); wsErr == nil {
			workspaces = ws
		}
		wsCancel()
	}
	result := agentOperationResult(sessions, diagnostics, len(previousSessions) > 0)
	if result.State == host.StateUnknown {
		diagnostics = append(diagnostics, agentsession.Diagnostic{Provider: "aggregate", Code: "unknown", Message: "状态未知"})
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

	c.syncAgentQuickJumpTargets(allWS, sessions)

	c.mu.Lock()
	repaint := c.OnRepaint
	c.mu.Unlock()
	if repaint != nil {
		repaint()
	}
}

func agentStatusMessage(result host.OperationResult) string {
	switch result.State {
	case host.StateRefreshing:
		return "刷新中…"
	case host.StateDegraded:
		if result.Message != "" {
			return "部分源不可用 · " + result.Message
		}
		return "部分源不可用"
	case host.StateFailed:
		if result.Message != "" {
			return result.Message
		}
		return "Agent 刷新失败"
	case host.StateUnknown:
		return "状态未知"
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
	providers := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		provider := agentProviderLabel(diagnostic.Provider)
		if provider != "" {
			providers = append(providers, provider)
		}
	}
	if len(providers) == 0 {
		return "Agent 数据源不可用"
	}
	return strings.Join(providers, "、") + " 数据源不可用"
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
	if value, ok := agentOperationResults.Load(c); ok {
		return value.(host.OperationResult)
	}
	return host.OperationResult{State: host.StateUnknown, ObservedAt: time.Now(), Retryable: false}
}

func setAgentOperationResult(c *AppController, result host.OperationResult) {
	agentOperationResults.Store(c, result)
	agentLatestResult.Lock()
	agentLatestResult.result = result
	agentLatestResult.Unlock()
}

func (c *AppController) agentRecordedOperationResult() host.OperationResult {
	if value, ok := agentOperationResults.Load(c); ok {
		return value.(host.OperationResult)
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
	if at.IsZero() {
		return "时间未知"
	}
	d := now.Sub(at)
	if d < time.Minute {
		return "刚刚"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d 分钟前", int(d/time.Minute))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d 小时前", int(d/time.Hour))
	}
	return fmt.Sprintf("%d 天前", int(d/(24*time.Hour)))
}
func agentStatusLabel(s agentsession.Status) string {
	switch s {
	case agentsession.StatusRunning:
		return "运行中"
	case agentsession.StatusStopped:
		return "已停止"
	case agentsession.StatusWaiting:
		return "等待中"
	default:
		return "未知"
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
	agentLatestResult.RLock()
	result := agentLatestResult.result
	agentLatestResult.RUnlock()
	return agentItemsForResult(sessions, diagnostics, query, result)
}

func agentItemsForResult(sessions []agentsession.Session, diagnostics []agentsession.Diagnostic, query string, result host.OperationResult) []engine.OmniboxItem {
	trimmed := strings.TrimSpace(query)
	if strings.HasPrefix(trimmed, "/") {
		rest := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "/")))
		items := make([]engine.OmniboxItem, 0, len(agentCommands))
		for _, command := range agentCommands {
			if rest != "" && !strings.Contains(strings.ToLower(command.Label+" "+command.ID+" "+command.Detail), rest) {
				continue
			}
			items = append(items, engine.OmniboxItem{ID: "agent-command:" + command.ID, Title: command.Label, Subtitle: command.Detail, Icon: "command", Kind: engine.ItemUtility, UtilityValue: "agent-command:" + command.ID})
		}
		return items
	}
	items := make([]engine.OmniboxItem, 0, len(sessions)+len(diagnostics)+1)
	// 刷新中且列表为空时，状态交给「居中加载态」渲染（capsule_view 的 len(items)==0 分支），
	// 不要在这种情况再塞一条状态行——否则 items 永不为空，居中加载态成了死代码，
	// 用户只看到列表里孤零零一行「刷新中…」。有缓存会话时仍保留状态行，否则用户看不到刷新中的提示。
	// 2026-11-22 实测 TestAgentTabShowsLoadingStateDuringRefresh 因此失败。
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
			subtitle += " · 待验收"
		}
		if session.LastMessage != "" {
			subtitle += " · " + agentsession.SanitizeDiagnosticText(session.LastMessage)
		}
		items = append(items, engine.OmniboxItem{ID: "agent-session:" + session.Provider + ":" + session.ID, Title: session.Title, Subtitle: subtitle, Keywords: []string{session.Provider, session.Workspace, session.WorkspaceLabel, session.PaneID, session.ID, session.LastMessage}, Icon: "person.crop.circle", Kind: engine.ItemUtility, UtilityValue: "agent-session:" + session.Provider + ":" + session.ID})
	}
	if showStatusRow && !statusAtTop {
		appendStatusRow()
	}
	for _, diagnostic := range diagnostics {
		items = append(items, engine.OmniboxItem{ID: "agent-diagnostic:" + diagnostic.Provider + ":" + diagnostic.Code, Title: agentProviderLabel(diagnostic.Provider), Subtitle: "诊断：" + agentsession.SanitizeDiagnosticText(diagnostic.Message), Icon: "info.circle", Kind: engine.ItemUtility, UtilityValue: "agent-diagnostic"})
	}
	return items
}

func agentStatusSubtitle(result host.OperationResult) string {
	if result.State == host.StateFailed && result.Retryable {
		return "点击重试"
	}
	if result.State == host.StateUnknown {
		return "请手动刷新确认"
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
	focuser := currentAgentFocuser()
	if focuser == nil {
		c.ShowToastOrHUD("已复制会话 ID（未检测到 herdr，无法直接跳转）")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		available, err := focuser.Available(ctx)
		if err != nil || !available {
			c.ShowToastOrHUD("已复制会话 ID（未检测到 herdr，无法直接跳转）")
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
		if focusErr != nil || result.State == host.StateFailed {
			message := agentsession.SanitizeDiagnosticText(result.Message)
			if message == "" {
				message = "无法直接跳转"
			}
			c.ShowToastOrHUD("已复制会话 ID（" + message + "）")
			return
		}
		if result.State == host.StateUnknown {
			c.ShowToastOrHUD("已复制会话 ID（未找到该会话的面板）")
			return
		}
		c.ShowToastOrHUD("已跳转到该会话")
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
			_ = bridge.OpenApp(context.Background(), plan.Profile.BundleID)
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
		message := ""
		for _, command := range agentCommands {
			if value == "agent-command:"+command.ID {
				message = command.Label + "\n" + command.Detail
				break
			}
		}
		if err := c.writeClipboardText(message); err != nil {
			c.ShowToastOrHUD(err.Error())
		} else {
			c.ShowToastOrHUD("已复制待发消息，未发送")
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

func (c *AppController) AgentRefresh() { go c.refreshAgentSessions() }

func (c *AppController) agentRefreshRetryable() bool {
	return c.agentOperationResult().Retryable && c.agentOperationResult().State != host.StateUnknown
}
