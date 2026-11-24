package agentsession

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/host"
)

// HerdrClient provides read-only access to the local `herdr` daemon CLI
// (workspaces, live agent panes, and terminal output snapshots).
type HerdrClient struct {
	executor CommandExecutor
}

// NewHerdrClient creates a HerdrClient backed by the system PATH `herdr`.
func NewHerdrClient() *HerdrClient {
	return &HerdrClient{executor: osCommandExecutor{}}
}

// NewHerdrClientWithExecutor creates a HerdrClient using a custom CommandExecutor.
func NewHerdrClientWithExecutor(executor CommandExecutor) *HerdrClient {
	if executor == nil {
		executor = osCommandExecutor{}
	}
	return &HerdrClient{executor: executor}
}

func (c *HerdrClient) resolveBinary() (string, bool) {
	if c == nil || c.executor == nil {
		return "", false
	}
	path, err := c.executor.LookPath("herdr")
	if err != nil || strings.TrimSpace(path) == "" {
		return "", false
	}
	return path, true
}

// ListWorkspaces queries `herdr workspace list`.
func (c *HerdrClient) ListWorkspaces(ctx context.Context) ([]HerdrWorkspace, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	path, ok := c.resolveBinary()
	if !ok {
		return nil, fmt.Errorf("herdr binary not found")
	}
	callCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	out, err := c.executor.Output(callCtx, path, "workspace", "list")
	if err != nil {
		return nil, err
	}
	return parseHerdrWorkspaces(out)
}

// ListAgents queries `herdr agent list`.
func (c *HerdrClient) ListAgents(ctx context.Context) ([]HerdrAgent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	path, ok := c.resolveBinary()
	if !ok {
		return nil, fmt.Errorf("herdr binary not found")
	}
	callCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	out, err := c.executor.Output(callCtx, path, "agent", "list")
	if err != nil {
		return nil, err
	}
	return parseHerdrAgents(out)
}

// ReadOutput reads recent unwrapped terminal output from a Herdr pane or agent
// name and returns ANSI-stripped, secret-redacted multiline Markdown-safe text.
func (c *HerdrClient) ReadOutput(ctx context.Context, target string, lines int) (string, host.OperationResult, error) {
	res := host.OperationResult{ObservedAt: time.Now()}
	if ctx == nil {
		ctx = context.Background()
	}
	target = strings.TrimSpace(target)
	if target == "" {
		res.State, res.Code, res.Message = host.StateFailed, "invalid_target", "缺少目标 Agent 面板"
		return "", res, fmt.Errorf("agentsession: empty read target")
	}
	if lines <= 0 || lines > 200 {
		lines = 60
	}
	path, ok := c.resolveBinary()
	if !ok {
		res.State, res.Code, res.Message = host.StateFailed, "herdr_unavailable", "未检测到 herdr"
		return "", res, fmt.Errorf("herdr binary not found")
	}
	callCtx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer cancel()
	out, err := c.executor.Output(callCtx, path,
		"agent", "read", target,
		"--source", "recent-unwrapped",
		"--lines", fmt.Sprintf("%d", lines),
		"--format", "text",
	)
	if err != nil {
		res.State, res.Code, res.Message, res.Retryable = host.StateFailed, "agent_read_failed", "无法读取终端输出", true
		return "", res, err
	}
	rawText := extractHerdrReadText(out)
	sanitized, _ := SanitizeMultilineOutput(rawText, lines, 20<<10)
	res.State, res.Code = host.StateReady, "ok"
	return sanitized, res, nil
}

func extractHerdrReadText(data []byte) string {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") {
		var env struct {
			Text    string `json:"text"`
			Content string `json:"content"`
			Output  string `json:"output"`
			Result  struct {
				Text    string `json:"text"`
				Content string `json:"content"`
				Output  string `json:"output"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(trimmed), &env) == nil {
			for _, candidate := range []string{env.Result.Text, env.Result.Content, env.Result.Output, env.Text, env.Content, env.Output} {
				if strings.TrimSpace(candidate) != "" {
					return candidate
				}
			}
		}
	}
	return string(data)
}

func shouldDiscoverHerdr(opts Options) bool {
	return opts.HerdrExecutor != nil
}

func discoverHerdr(ctx context.Context, opts Options) ([]Session, []Diagnostic) {
	client := NewHerdrClientWithExecutor(opts.HerdrExecutor)
	if _, ok := client.resolveBinary(); !ok {
		return nil, nil
	}
	agents, err := client.ListAgents(ctx)
	if err != nil {
		code := "unavailable"
		if ctx.Err() == context.DeadlineExceeded {
			code = "timeout"
		}
		return nil, []Diagnostic{{Provider: "herdr", Code: code, Message: "herdr daemon unreachable or list failed"}}
	}
	wsLabels := make(map[string]string)
	if workspaces, wsErr := client.ListWorkspaces(ctx); wsErr == nil {
		for _, ws := range workspaces {
			if strings.TrimSpace(ws.WorkspaceID) != "" && strings.TrimSpace(ws.Label) != "" {
				wsLabels[ws.WorkspaceID] = strings.TrimSpace(ws.Label)
			}
		}
	}

	now := opts.Now()
	out := make([]Session, 0, len(agents))
	for _, a := range agents {
		out = append(out, sessionFromHerdrAgent(a, wsLabels, now))
	}
	return out, nil
}

func sessionFromHerdrAgent(a HerdrAgent, wsLabels map[string]string, now time.Time) Session {
	provider := strings.ToLower(strings.TrimSpace(a.Agent))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(a.AgentSession.Agent))
	}
	if provider == "" {
		provider = "herdr"
	}

	id := extractHerdrSessionID(a.AgentSession.Value, a.PaneID)
	cwd := strings.TrimSpace(a.ForegroundCWD)
	if cwd == "" {
		cwd = strings.TrimSpace(a.CWD)
	}
	wsBase := basename(cwd)
	wsLabel := wsLabels[a.WorkspaceID]
	if wsLabel == "" {
		wsLabel = wsBase
	}

	title := herdrAgentTitle(a, wsLabel, provider)
	status, pending := mapHerdrAgentStatus(a.AgentStatus)
	var currentAction string
	if status == StatusRunning {
		currentAction = "Active in " + firstNonEmpty(wsLabel, a.PaneID, "workspace")
	} else if pending {
		currentAction = "Waiting for confirmation in " + firstNonEmpty(a.PaneID, wsLabel)
	}

	return Session{
		ID:               id,
		Provider:         provider,
		Title:            title,
		Status:           status,
		LastActivityAt:   now,
		PendingReview:    pending,
		AwaitingHuman:    pending,
		CurrentAction:    currentAction,
		Workspace:        wsBase,
		WorkspaceID:      strings.TrimSpace(a.WorkspaceID),
		WorkspaceLabel:   wsLabel,
		PaneID:           strings.TrimSpace(a.PaneID),
		TabID:            strings.TrimSpace(a.TabID),
		AgentName:        strings.TrimSpace(a.Name),
		DisplayAgent:     strings.TrimSpace(a.DisplayAgent),
		InteractiveReady: a.InteractiveReady,
		Focused:          a.Focused,
		HerdrManaged:     true,
		TargetRef:        strings.TrimSpace(a.PaneID),
		Source:           firstNonEmpty(a.AgentSession.Value, "herdr:"+a.PaneID),
		ObservedAt:       now,
	}
}

func extractHerdrSessionID(rawVal, paneID string) string {
	val := strings.TrimSpace(rawVal)
	if val != "" {
		if strings.HasSuffix(val, ".jsonl") || strings.ContainsAny(val, `/\`) {
			base := strings.TrimSuffix(filepath.Base(val), ".jsonl")
			if idx := strings.LastIndexByte(base, '_'); idx >= 0 && idx+1 < len(base) {
				return base[idx+1:]
			}
			if base != "" {
				return base
			}
		}
		return val
	}
	if strings.TrimSpace(paneID) != "" {
		return "pane-" + strings.TrimSpace(paneID)
	}
	return ""
}

func herdrAgentTitle(a HerdrAgent, wsLabel, provider string) string {
	if name := strings.TrimSpace(a.Name); name != "" {
		return name
	}
	if disp := strings.TrimSpace(a.DisplayAgent); disp != "" {
		// Strip leading "<provider> · " prefix when redundant.
		prefix := provider + " · "
		if strings.HasPrefix(strings.ToLower(disp), strings.ToLower(prefix)) {
			if rest := strings.TrimSpace(disp[len(prefix):]); rest != "" && !strings.HasPrefix(rest, provider+" ") {
				return rest
			}
		}
		if disp != provider {
			return disp
		}
	}
	if term := strings.TrimSpace(a.TerminalTitle); term != "" && !strings.EqualFold(term, provider) {
		return term
	}
	if wsLabel != "" {
		return wsLabel + " · " + provider
	}
	return provider
}

func mapHerdrAgentStatus(raw string) (Status, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "working", "running", "active", "in_progress", "busy":
		return StatusRunning, false
	case "blocked", "awaiting_human", "needs_confirmation", "approval":
		return StatusWaiting, true
	case "done", "stopped", "completed", "closed", "exited":
		return StatusStopped, false
	case "idle", "waiting", "ready":
		return StatusWaiting, false
	default:
		return StatusUnknown, false
	}
}
