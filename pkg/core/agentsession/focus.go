package agentsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
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

// CommandExecutor is the small process seam used by HerdrFocuser. Tests can
// provide a fake; production uses exec.LookPath and exec.CommandContext.
type CommandExecutor interface {
	LookPath(file string) (string, error)
	Output(ctx context.Context, path string, args ...string) ([]byte, error)
}

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
	for _, agent := range agents {
		if strings.Contains(agent.AgentSession.Value, sessionID) {
			if strings.TrimSpace(agent.PaneID) == "" {
				result.State, result.Code, result.Message, result.Retryable = host.StateUnknown, "pane_missing", "未找到该会话的面板", false
				return result, nil
			}
			focusCtx, focusCancel := context.WithTimeout(ctx, 3*time.Second)
			_, focusErr := h.executor.Output(focusCtx, path, "agent", "focus", agent.PaneID)
			focusCancel()
			if focusErr != nil {
				result.State, result.Code, result.Message, result.Retryable = host.StateFailed, "focus_failed", "无法聚焦该会话", true
				return result, nil
			}
			result.State, result.Code, result.Message, result.Retryable = host.StateReady, "focused", "已跳转到该会话", false
			return result, nil
		}
	}
	result.State, result.Code, result.Message, result.Retryable = host.StateUnknown, "session_not_found", "未找到该会话的面板", false
	return result, nil
}

type herdrAgent struct {
	AgentSession struct {
		Value string `json:"value"`
	} `json:"agent_session"`
	PaneID string `json:"pane_id"`
	TabID  string `json:"tab_id"`
}

func parseHerdrAgents(data []byte) ([]herdrAgent, error) {
	var list []herdrAgent
	if err := json.Unmarshal(data, &list); err == nil && list != nil {
		return list, nil
	}
	var envelope struct {
		Agents []herdrAgent `json:"agents"`
		Data   []herdrAgent `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("herdr agent list: %w", err)
	}
	if envelope.Agents != nil {
		return envelope.Agents, nil
	}
	if envelope.Data != nil {
		return envelope.Data, nil
	}
	return nil, errors.New("herdr agent list: missing agents")
}
