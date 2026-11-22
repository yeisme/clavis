package uiapp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/engine"
)

type agentCommand struct{ ID, Label, Detail string }

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
	c.agentMu.Unlock()
	home, _ := os.UserHomeDir()
	db := filepath.Join(home, ".codex", "state_5.sqlite")
	sessions, diagnostics := agentsession.Discover(context.Background(), agentsession.Options{CodexDB: db, CodexReader: codexSQLiteReader{path: db}})
	c.agentMu.Lock()
	c.agentSessions = append([]agentsession.Session(nil), sessions...)
	c.agentDiagnostics = append([]agentsession.Diagnostic(nil), diagnostics...)
	c.agentRefreshStarted = false
	c.agentMu.Unlock()
	c.mu.Lock()
	repaint := c.OnRepaint
	c.mu.Unlock()
	if repaint != nil {
		repaint()
	}
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
	items := make([]engine.OmniboxItem, 0, len(sessions)+len(diagnostics))
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
			subtitle += " · " + session.LastMessage
		}
		items = append(items, engine.OmniboxItem{ID: "agent-session:" + session.Provider + ":" + session.ID, Title: session.Title, Subtitle: subtitle, Keywords: []string{session.Provider, session.Workspace, session.ID, session.LastMessage}, Icon: "person.crop.circle", Kind: engine.ItemUtility, UtilityValue: "agent-session:" + session.Provider + ":" + session.ID})
	}
	for _, diagnostic := range diagnostics {
		items = append(items, engine.OmniboxItem{ID: "agent-diagnostic:" + diagnostic.Provider + ":" + diagnostic.Code, Title: agentProviderLabel(diagnostic.Provider), Subtitle: "诊断：" + diagnostic.Message, Icon: "info.circle", Kind: engine.ItemUtility, UtilityValue: "agent-diagnostic"})
	}
	return items
}
func isAgentItem(item engine.OmniboxItem) bool { return strings.HasPrefix(item.ID, "agent-") }
func (c *AppController) agentItemAction(item engine.OmniboxItem) bool {
	value := item.UtilityValue
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
