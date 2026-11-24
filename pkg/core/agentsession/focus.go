package agentsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/host"
)

// Focuser can bring an agent session to the front.
type Focuser interface {
	Name() string
	Available(ctx context.Context) (bool, error)
	Focus(ctx context.Context, sessionID string) (host.OperationResult, error)
}

// PaneFocuser is an optional extension of Focuser for backends (such as Herdr)
// that already know the exact target pane ID and can focus it directly.
type PaneFocuser interface {
	FocusPane(ctx context.Context, paneID string) (host.OperationResult, error)
}

// CommandExecutor is the small process seam used by HerdrFocuser. Tests can
// provide a fake; production uses exec.LookPath and exec.CommandContext.
type CommandExecutor interface {
	LookPath(file string) (string, error)
	Output(ctx context.Context, path string, args ...string) ([]byte, error)
}

// NewCommandExecutor returns the standard OS process CommandExecutor.
func NewCommandExecutor() CommandExecutor { return osCommandExecutor{} }

type osCommandExecutor struct{}

func (osCommandExecutor) LookPath(file string) (string, error) { return exec.LookPath(file) }
func (osCommandExecutor) Output(ctx context.Context, path string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, path, args...).Output()
}

// HerdrFocuser discovers herdr capability from the installed binary and its
// machine-readable agent list. It never treats binary presence alone as
// evidence that focus is supported.
type HerdrFocuser struct {
	executor CommandExecutor
}

func NewHerdrFocuser() *HerdrFocuser { return &HerdrFocuser{executor: osCommandExecutor{}} }
func NewHerdrFocuserWithExecutor(executor CommandExecutor) *HerdrFocuser {
	if executor == nil {
		executor = osCommandExecutor{}
	}
	return &HerdrFocuser{executor: executor}
}
func (h *HerdrFocuser) Name() string { return "herdr" }

func (h *HerdrFocuser) Available(ctx context.Context) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if h == nil || h.executor == nil {
		return false, nil
	}
	path, err := h.executor.LookPath("herdr")
	if err != nil || strings.TrimSpace(path) == "" {
		return false, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	output, err := h.executor.Output(probeCtx, path, "agent", "list")
	if err != nil {
		return false, nil
	}
	if _, err := parseHerdrAgents(output); err != nil {
		return false, nil
	}
	return true, nil
}

func (h *HerdrFocuser) Focus(ctx context.Context, sessionID string) (host.OperationResult, error) {
	result := host.OperationResult{ObservedAt: time.Now()}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionID) == "" {
		result.State, result.Code, result.Message, result.Retryable = host.StateFailed, "invalid_session", "无法聚焦该会话", false
		return result, errors.New("agentsession: empty session id")
	}
	path, err := h.executor.LookPath("herdr")
	if err != nil || strings.TrimSpace(path) == "" {
		result.State, result.Code, result.Message, result.Retryable = host.StateFailed, "herdr_unavailable", "未检测到 herdr", false
		return result, nil
	}
	listCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	output, err := h.executor.Output(listCtx, path, "agent", "list")
	cancel()
	if err != nil {
		result.State, result.Code, result.Message, result.Retryable = host.StateFailed, "agent_list_failed", "herdr 会话列表不可用", true
		return result, nil
	}
	agents, err := parseHerdrAgents(output)
	if err != nil {
		result.State, result.Code, result.Message, result.Retryable = host.StateFailed, "agent_list_invalid", "herdr 会话列表不可解析", false
		return result, nil
	}
	// Match the session identifier exactly, never as a substring.
	//
	// Substring matching sent the user to the wrong terminal: asking for
	// `session-abc` also matched `/tmp/session-abc-longer.jsonl`, focused that pane, and
	// reported "已跳转到该会话" — a false success, which is worse than an honest miss
	// because the user believes they are looking at the right window.
	//
	// herdr reports the session file, whose base name is either the bare id
	// (`<id>.jsonl`) or `<timestamp>_<id>.jsonl`, so the id matches when the base name
	// equals it or ends with `_<id>`. Anything else is a different session.
	var exact []HerdrAgent
	for _, agent := range agents {
		if sessionFileMatchesID(agent.AgentSession.Value, sessionID) {
			exact = append(exact, agent)
		}
	}
	switch len(exact) {
	case 0:
		result.State, result.Code, result.Message, result.Retryable = host.StateUnknown, "session_not_found", "未找到该会话的面板", false
		return result, nil
	case 1:
		// exactly one candidate — safe to focus
	default:
		// Two panes claim the same session id (e.g. the same session opened twice).
		// Picking one at random is how the wrong window gets focused, so refuse and
		// say so; the caller can fall back to copying the id.
		result.State, result.Code, result.Message, result.Retryable = host.StateUnknown, "session_ambiguous", "该会话在多个面板中打开，无法确定要跳转哪一个", false
		return result, nil
	}
	return h.focusResolvedPane(ctx, path, exact[0].PaneID)
}

// FocusPane brings a known Herdr pane ID directly to the front without fuzzy
// matching.
func (h *HerdrFocuser) FocusPane(ctx context.Context, paneID string) (host.OperationResult, error) {
	result := host.OperationResult{ObservedAt: time.Now()}
	if ctx == nil {
		ctx = context.Background()
	}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		result.State, result.Code, result.Message, result.Retryable = host.StateUnknown, "pane_missing", "未找到该会话的面板", false
		return result, nil
	}
	if h == nil || h.executor == nil {
		result.State, result.Code, result.Message, result.Retryable = host.StateFailed, "herdr_unavailable", "未检测到 herdr", false
		return result, nil
	}
	path, err := h.executor.LookPath("herdr")
	if err != nil || strings.TrimSpace(path) == "" {
		result.State, result.Code, result.Message, result.Retryable = host.StateFailed, "herdr_unavailable", "未检测到 herdr", false
		return result, nil
	}
	return h.focusResolvedPane(ctx, path, paneID)
}

func (h *HerdrFocuser) focusResolvedPane(ctx context.Context, herdrPath, paneID string) (host.OperationResult, error) {
	result := host.OperationResult{ObservedAt: time.Now()}
	if strings.TrimSpace(paneID) == "" {
		result.State, result.Code, result.Message, result.Retryable = host.StateUnknown, "pane_missing", "未找到该会话的面板", false
		return result, nil
	}
	focusCtx, focusCancel := context.WithTimeout(ctx, 3*time.Second)
	_, focusErr := h.executor.Output(focusCtx, herdrPath, "agent", "focus", paneID)
	focusCancel()
	if focusErr != nil {
		result.State, result.Code, result.Message, result.Retryable = host.StateFailed, "focus_failed", "无法聚焦该会话", true
		return result, nil
	}
	result.State, result.Code, result.Message, result.Retryable = host.StateReady, "focused", "已跳转到该会话", false
	return result, nil
}

// HerdrAgent describes one agent pane returned by `herdr agent list`.
type HerdrAgent struct {
	Agent            string `json:"agent"`
	DisplayAgent     string `json:"display_agent"`
	Name             string `json:"name"`
	AgentStatus      string `json:"agent_status"`
	PaneID           string `json:"pane_id"`
	TabID            string `json:"tab_id"`
	WorkspaceID      string `json:"workspace_id"`
	CWD              string `json:"cwd"`
	ForegroundCWD    string `json:"foreground_cwd"`
	TerminalTitle    string `json:"terminal_title_stripped"`
	Focused          bool   `json:"focused"`
	InteractiveReady bool   `json:"interactive_ready"`
	StateChangeSeq   int64  `json:"state_change_seq"`
	AgentSession     struct {
		Agent  string `json:"agent"`
		Kind   string `json:"kind"`
		Source string `json:"source"`
		Value  string `json:"value"`
	} `json:"agent_session"`
}

// HerdrWorkspace describes one workspace returned by `herdr workspace list`.
type HerdrWorkspace struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Number      int    `json:"number"`
	AgentStatus string `json:"agent_status"`
	Focused     bool   `json:"focused"`
	PaneCount   int    `json:"pane_count"`
	TabCount    int    `json:"tab_count"`
	ActiveTabID string `json:"active_tab_id"`
}

func parseHerdrAgents(data []byte) ([]HerdrAgent, error) {
	var list []HerdrAgent
	if err := json.Unmarshal(data, &list); err == nil && list != nil {
		return list, nil
	}
	var envelope struct {
		Agents []HerdrAgent `json:"agents"`
		Data   []HerdrAgent `json:"data"`
		Result struct {
			Agents []HerdrAgent `json:"agents"`
			Data   []HerdrAgent `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("herdr agent list: %w", err)
	}
	if envelope.Result.Agents != nil {
		return envelope.Result.Agents, nil
	}
	if envelope.Result.Data != nil {
		return envelope.Result.Data, nil
	}
	if envelope.Agents != nil {
		return envelope.Agents, nil
	}
	if envelope.Data != nil {
		return envelope.Data, nil
	}
	return nil, errors.New("herdr agent list: missing agents")
}

func parseHerdrWorkspaces(data []byte) ([]HerdrWorkspace, error) {
	var list []HerdrWorkspace
	if err := json.Unmarshal(data, &list); err == nil && list != nil {
		return list, nil
	}
	var envelope struct {
		Workspaces []HerdrWorkspace `json:"workspaces"`
		Result     struct {
			Workspaces []HerdrWorkspace `json:"workspaces"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("herdr workspace list: %w", err)
	}
	if envelope.Result.Workspaces != nil {
		return envelope.Result.Workspaces, nil
	}
	if envelope.Workspaces != nil {
		return envelope.Workspaces, nil
	}
	return nil, errors.New("herdr workspace list: missing workspaces")
}

// sessionFileMatchesID reports whether the herdr session file belongs to id exactly.
// Substring matching is deliberately rejected: it turned `session-abc` into a match for
// `session-abc-longer` and focused the wrong pane.
func sessionFileMatchesID(value, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	base := filepath.Base(strings.TrimSpace(value))
	base = strings.TrimSuffix(base, ".jsonl")
	if base == id {
		return true
	}
	return strings.HasSuffix(base, "_"+id)
}
