package uiapp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
)

type agentCommand struct{ ID, Label, Detail string }

var agentOperationResults sync.Map
var agentLatestResult struct {
	sync.RWMutex
	result host.OperationResult
}

var agentCommands = []agentCommand{
	{ID: "continue", Label: "继续当前任务", Detail: "继续最近一轮工作"},
	{ID: "review", Label: "总结待验收项", Detail: "整理会话中的验收状态"},
	{ID: "test", Label: "运行测试并回报证据", Detail: "请求测试结果和关键输出"},
}

type codexSQLiteReader struct{ path string }

func (r codexSQLiteReader) Threads(ctx context.Context, limit int) ([]agentsession.CodexThread, error) {
	query := fmt.Sprintf("SELECT id,title,cwd,updated_at,preview,source FROM threads WHERE archived=0 ORDER BY updated_at DESC LIMIT %d", limit)
	out, err := exec.CommandContext(ctx, "sqlite3", "-readonly", "-json", r.path, query).Output()
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		CWD     string `json:"cwd"`
		Updated int64  `json:"updated_at"`
		Preview string `json:"preview"`
		Source  string `json:"source"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, err
	}
	threads := make([]agentsession.CodexThread, 0, len(rows))
	for _, row := range rows {
		threads = append(threads, agentsession.CodexThread{ID: row.ID, Title: row.Title, Workspace: row.CWD, LastActivityAt: time.Unix(row.Updated, 0), LastMessage: row.Preview, Status: row.Source})
	}
	return threads, nil
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
	home, _ := os.UserHomeDir()
	db := filepath.Join(home, ".codex", "state_5.sqlite")
	previousSessions, _ := c.cachedAgentData()
	sessions, diagnostics := agentsession.Discover(context.Background(), agentsession.Options{CodexDB: db, CodexReader: codexSQLiteReader{path: db}})
	result := agentOperationResult(sessions, diagnostics, len(previousSessions) > 0)
	if result.State == host.StateUnknown {
		diagnostics = append(diagnostics, agentsession.Diagnostic{Provider: "aggregate", Code: "unknown", Message: "状态未知"})
	}
	c.agentMu.Lock()
	c.agentSessions = append([]agentsession.Session(nil), sessions...)
	c.agentDiagnostics = append([]agentsession.Diagnostic(nil), diagnostics...)
	c.agentRefreshStarted = false
	setAgentOperationResult(c, result)
	c.agentMu.Unlock()
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

func (c *AppController) agentVisibleItems(query string) []engine.OmniboxItem {
	sessions, diagnostics := c.cachedAgentData()
	return agentItems(sessions, diagnostics, query)
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
	default:
		return p
	}
}

func agentItems(sessions []agentsession.Session, diagnostics []agentsession.Diagnostic, query string) []engine.OmniboxItem {
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
	agentLatestResult.RLock()
	result := agentLatestResult.result
	agentLatestResult.RUnlock()
	// 刷新中且列表为空时，状态交给「居中加载态」渲染（capsule_view 的 len(items)==0 分支），
	// 不要在这种情况再塞一条状态行——否则 items 永不为空，居中加载态成了死代码，
	// 用户只看到列表里孤零零一行「刷新中…」。有缓存会话时仍保留状态行，否则用户看不到刷新中的提示。
	// 2026-11-22 实测 TestAgentTabShowsLoadingStateDuringRefresh 因此失败。
	refreshingWithNothingToShow := result.State == host.StateRefreshing && len(sessions) == 0
	if trimmed == "" && result.State != "" && result.State != host.StateReady && !refreshingWithNothingToShow {
		retryable := result.Retryable && result.State != host.StateUnknown
		items = append(items, engine.OmniboxItem{ID: "agent-status:" + string(result.State), Title: agentStatusMessage(result), Subtitle: agentStatusSubtitle(result), Icon: "info.circle", Kind: engine.ItemUtility, UtilityValue: "agent-refresh-status", KeyHint: map[bool]string{true: "R", false: ""}[retryable]})
	}
	for _, session := range sessions {
		searchable := strings.ToLower(strings.Join([]string{session.Title, session.LastMessage, session.Provider, session.Workspace, session.ID}, " "))
		if trimmed != "" && !strings.Contains(searchable, strings.ToLower(trimmed)) {
			continue
		}
		subtitle := fmt.Sprintf("%s · %s", agentStatusLabel(session.Status), agentRelativeTime(session.LastActivityAt, time.Now()))
		if session.PendingReview {
			subtitle += " · 待验收"
		}
		if session.LastMessage != "" {
			subtitle += " · " + agentsession.SanitizeDiagnosticText(session.LastMessage)
		}
		items = append(items, engine.OmniboxItem{ID: "agent-session:" + session.Provider + ":" + session.ID, Title: session.Title, Subtitle: subtitle, Keywords: []string{session.Provider, session.Workspace, session.ID, session.LastMessage}, Icon: "person.crop.circle", Kind: engine.ItemUtility, UtilityValue: "agent-session:" + session.Provider + ":" + session.ID})
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
func isAgentItem(item engine.OmniboxItem) bool { return strings.HasPrefix(item.ID, "agent-") }
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
		c.ShowToastOrHUD(err.Error())
	} else {
		c.ShowToastOrHUD("已复制会话 ID，可粘贴到 Agent 窗口")
	}
	c.mu.Lock()
	bridge := c.Bridge
	c.mu.Unlock()
	if bridge != nil {
		_ = bridge.OpenApp(context.Background(), "com.mitchellh.ghostty")
	}
	return true
}
func (c *AppController) AgentRefresh() { go c.refreshAgentSessions() }

func (c *AppController) agentRefreshRetryable() bool {
	return c.agentOperationResult().Retryable && c.agentOperationResult().State != host.StateUnknown
}
