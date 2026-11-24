package agentsession

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/host"
)

var allowedHerdrAgentCLIs = map[string]bool{
	"claude":   true,
	"codex":    true,
	"pi":       true,
	"agy":      true,
	"gemini":   true,
	"opencode": true,
}

var sendSecretPattern = regexp.MustCompile(`(?i)\b(sk-[a-z0-9_-]{8,}|ghp_[a-z0-9]{10,}|glpat-[a-z0-9_-]{10,}|akia[0-9a-z]{12,}|-----begin [a-z ]+private key-----)`)

// SendPromptRequest carries gated parameters for `herdr agent send`.
type SendPromptRequest struct {
	Target    string
	Message   string
	Confirmed bool
}

// HerdrController provides controlled write & focus operations against the local
// `herdr` daemon CLI with strict confirmation, allowlist, and StateUnknown safety.
type HerdrController struct {
	executor CommandExecutor
}

// NewHerdrController creates a HerdrController backed by executor (defaults to OS PATH).
func NewHerdrController(executor CommandExecutor) *HerdrController {
	if executor == nil {
		executor = osCommandExecutor{}
	}
	return &HerdrController{executor: executor}
}

func (c *HerdrController) resolveBinary() (string, bool) {
	if c == nil || c.executor == nil {
		return "", false
	}
	p, err := c.executor.LookPath("herdr")
	if err != nil || strings.TrimSpace(p) == "" {
		return "", false
	}
	return p, true
}

// FocusPane switches focus directly to paneID via `herdr agent focus <paneID>`.
func (c *HerdrController) FocusPane(ctx context.Context, paneID string) (host.OperationResult, error) {
	return NewHerdrClientWithExecutor(c.executor).FocusPane(ctx, paneID)
}

// FocusPane focuses a pane via HerdrClient cleanly.
func (c *HerdrClient) FocusPane(ctx context.Context, paneID string) (host.OperationResult, error) {
	res := host.OperationResult{ObservedAt: time.Now()}
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		res.State, res.Code, res.Message = host.StateFailed, "invalid_target", "缺少目标面板 ID"
		return res, fmt.Errorf("agentsession: empty pane id")
	}
	path, ok := c.resolveBinary()
	if !ok {
		res.State, res.Code, res.Message = host.StateFailed, "herdr_unavailable", "未检测到 herdr"
		return res, fmt.Errorf("herdr binary not found")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if _, err := c.executor.Output(callCtx, path, "agent", "focus", paneID); err != nil {
		if callCtx.Err() == context.DeadlineExceeded {
			res.State, res.Code, res.Message, res.Retryable = host.StateUnknown, "focus_timeout", "切换焦点超时，状态未知", false
			return res, err
		}
		res.State, res.Code, res.Message, res.Retryable = host.StateFailed, "focus_failed", SanitizeDiagnosticText(err.Error()), true
		return res, err
	}
	res.State, res.Code = host.StateReady, "ok"
	return res, nil
}

// FocusWorkspace switches Herdr workspace via `herdr workspace focus <workspaceID>`.
func (c *HerdrController) FocusWorkspace(ctx context.Context, workspaceID string) (host.OperationResult, error) {
	res := host.OperationResult{ObservedAt: time.Now()}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		res.State, res.Code, res.Message = host.StateFailed, "invalid_workspace", "缺少目标工作区 ID"
		return res, fmt.Errorf("agentsession: empty workspace id")
	}
	path, ok := c.resolveBinary()
	if !ok {
		res.State, res.Code, res.Message = host.StateFailed, "herdr_unavailable", "未检测到 herdr"
		return res, fmt.Errorf("herdr binary not found")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if _, err := c.executor.Output(callCtx, path, "workspace", "focus", workspaceID); err != nil {
		if callCtx.Err() == context.DeadlineExceeded {
			res.State, res.Code, res.Message, res.Retryable = host.StateUnknown, "workspace_focus_timeout", "切换工作区超时", false
			return res, err
		}
		res.State, res.Code, res.Message, res.Retryable = host.StateFailed, "workspace_focus_failed", SanitizeDiagnosticText(err.Error()), true
		return res, err
	}
	res.State, res.Code = host.StateReady, "ok"
	return res, nil
}

// RenameAgent sets the Herdr agent name (`herdr agent rename <target> <newName>`).
func (c *HerdrController) RenameAgent(ctx context.Context, target, newName string) (host.OperationResult, error) {
	res := host.OperationResult{ObservedAt: time.Now()}
	target = strings.TrimSpace(target)
	cleanName := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(newName, "\n", " "), "\r", " "))
	if target == "" || cleanName == "" || len([]rune(cleanName)) > 40 {
		res.State, res.Code, res.Message = host.StateFailed, "invalid_name", "名称无效（需 1-40 字符）"
		return res, fmt.Errorf("agentsession: invalid target or name")
	}
	path, ok := c.resolveBinary()
	if !ok {
		res.State, res.Code, res.Message = host.StateFailed, "herdr_unavailable", "未检测到 herdr"
		return res, fmt.Errorf("herdr binary not found")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if _, err := c.executor.Output(callCtx, path, "agent", "rename", target, cleanName); err != nil {
		if callCtx.Err() == context.DeadlineExceeded {
			res.State, res.Code, res.Message, res.Retryable = host.StateUnknown, "rename_timeout", "重命名超时，请刷新确认", false
			return res, err
		}
		res.State, res.Code, res.Message, res.Retryable = host.StateFailed, "rename_failed", SanitizeDiagnosticText(err.Error()), true
		return res, err
	}
	res.State, res.Code = host.StateReady, "ok"
	return res, nil
}

// SendPrompt dispatches `herdr agent send <target> --message <msg>` strictly after:
//  1. Explicit user confirmation (req.Confirmed == true),
//  2. Secret-prefix inspection blocking accidental credential leaks into terminals,
//  3. Live target existence re-check against `herdr agent list`,
//  4. Returning StateUnknown (Retryable=false) on ambiguous timeouts so duplicates never happen.
func (c *HerdrController) SendPrompt(ctx context.Context, req SendPromptRequest) (host.OperationResult, error) {
	res := host.OperationResult{ObservedAt: time.Now()}
	if !req.Confirmed {
		res.State, res.Code, res.Message, res.Retryable = host.StateFailed, "confirmation_required", "需要明确点击确认才能向 Agent 发送指令", false
		return res, fmt.Errorf("agentsession: send prompt requires explicit confirmation")
	}
	target := strings.TrimSpace(req.Target)
	msg := strings.TrimSpace(req.Message)
	if target == "" || msg == "" {
		res.State, res.Code, res.Message = host.StateFailed, "invalid_send_request", "目标面板或指令内容为空"
		return res, fmt.Errorf("agentsession: empty target or message")
	}
	if sendSecretPattern.MatchString(msg) {
		res.State, res.Code, res.Message, res.Retryable = host.StateFailed, "secret_blocked", "检测到疑似密钥或令牌前缀，已阻止向终端明文发送", false
		return res, fmt.Errorf("agentsession: blocked secret-like token in prompt")
	}
	path, ok := c.resolveBinary()
	if !ok {
		res.State, res.Code, res.Message = host.StateFailed, "herdr_unavailable", "未检测到 herdr"
		return res, fmt.Errorf("herdr binary not found")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Re-validate live target before executing write action.
	agents, err := NewHerdrClientWithExecutor(c.executor).ListAgents(ctx)
	if err != nil {
		res.State, res.Code, res.Message, res.Retryable = host.StateFailed, "target_verify_failed", "无法实时核对目标 Agent", true
		return res, err
	}
	found := false
	for _, a := range agents {
		if a.PaneID == target || a.Name == target || a.AgentSession.Value == target {
			found = true
			break
		}
	}
	if !found {
		res.State, res.Code, res.Message, res.Retryable = host.StateFailed, "target_not_found", "目标 Agent 面板已不存在或已退出", false
		return res, fmt.Errorf("agentsession: target %q not found in live herdr agents", target)
	}

	callCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	if _, err := c.executor.Output(callCtx, path, "agent", "send", target, "--message", msg); err != nil {
		res.State, res.Code, res.Message, res.Retryable = host.StateUnknown, "send_outcome_unknown", "指令已投递或超时（状态未知，禁止自动重试）", false
		return res, err
	}
	res.State, res.Code = host.StateReady, "sent"
	return res, nil
}

// StartAgent spawns an allowlisted agent CLI inside a Herdr workspace.
func (c *HerdrController) StartAgent(ctx context.Context, workspace, agentCLI, name string) (host.OperationResult, error) {
	res := host.OperationResult{ObservedAt: time.Now()}
	cli := strings.ToLower(strings.TrimSpace(agentCLI))
	if !allowedHerdrAgentCLIs[cli] {
		res.State, res.Code, res.Message = host.StateFailed, "disallowed_agent_cli", "仅允许启动受信任白名单 Agent (claude/codex/pi/agy/gemini/opencode)"
		return res, fmt.Errorf("agentsession: disallowed agent CLI %q", agentCLI)
	}
	path, ok := c.resolveBinary()
	if !ok {
		res.State, res.Code, res.Message = host.StateFailed, "herdr_unavailable", "未检测到 herdr"
		return res, fmt.Errorf("herdr binary not found")
	}
	args := []string{"agent", "start", "--agent", cli}
	if ws := strings.TrimSpace(workspace); ws != "" {
		args = append(args, "--workspace", ws)
	}
	if nm := strings.TrimSpace(name); nm != "" {
		args = append(args, "--name", nm)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := c.executor.Output(callCtx, path, args...); err != nil {
		res.State, res.Code, res.Message, res.Retryable = host.StateFailed, "agent_start_failed", SanitizeDiagnosticText(err.Error()), true
		return res, err
	}
	res.State, res.Code = host.StateReady, "started"
	return res, nil
}

// WaitState waits via `herdr agent wait <target> --state <state>` for completion/idle notifications.
func (c *HerdrController) WaitState(ctx context.Context, target, state string, timeout time.Duration) (host.OperationResult, error) {
	res := host.OperationResult{ObservedAt: time.Now()}
	target = strings.TrimSpace(target)
	if target == "" {
		res.State, res.Code, res.Message = host.StateFailed, "invalid_target", "缺少监听目标"
		return res, fmt.Errorf("agentsession: empty wait target")
	}
	if strings.TrimSpace(state) == "" {
		state = "idle"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	path, ok := c.resolveBinary()
	if !ok {
		res.State, res.Code, res.Message = host.StateFailed, "herdr_unavailable", "未检测到 herdr"
		return res, fmt.Errorf("herdr binary not found")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	secs := max(1, int(timeout.Seconds()))
	if _, err := c.executor.Output(callCtx, path, "agent", "wait", target, "--state", state, "--timeout", fmt.Sprintf("%d", secs)); err != nil {
		if callCtx.Err() == context.DeadlineExceeded {
			res.State, res.Code, res.Message, res.Retryable = host.StateUnknown, "wait_timeout", "等待状态超时", false
			return res, err
		}
		res.State, res.Code, res.Message, res.Retryable = host.StateFailed, "wait_failed", SanitizeDiagnosticText(err.Error()), false
		return res, err
	}
	res.State, res.Code = host.StateReady, "matched"
	return res, nil
}
