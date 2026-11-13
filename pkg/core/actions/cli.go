package actions

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/engine"
)

// CLIExecutionResult holds the exit status, captured output, and timeout flag.
type CLIExecutionResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	TimedOut bool   `json:"timedOut"`
}

// ErrEmptyCommand is returned when the CLI command string is blank.
var ErrEmptyCommand = errors.New("cli command cannot be empty")

// CLIExecutor executes external commands directly without shell interpolation.
type CLIExecutor struct {
	DefaultTimeout time.Duration
}

// NewCLIExecutor constructs a CLIExecutor with a default 15s timeout.
func NewCLIExecutor() CLIExecutor {
	return CLIExecutor{DefaultTimeout: 15 * time.Second}
}

// Execute runs command with args directly via exec.CommandContext (no shell interpolation),
// injecting CLAVIS_SELECTED_TEXT, CLAVIS_APP_BUNDLE_ID, and CLAVIS_APP_NAME.
func (e CLIExecutor) Execute(
	ctx context.Context,
	command string,
	args []string,
	capsuleCtx engine.CapsuleContext,
	timeout time.Duration,
) (CLIExecutionResult, error) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return CLIExecutionResult{ExitCode: -1}, ErrEmptyCommand
	}
	if err := ctx.Err(); err != nil {
		return CLIExecutionResult{ExitCode: -1}, err
	}

	effTimeout := timeout
	if effTimeout <= 0 {
		effTimeout = e.DefaultTimeout
	}
	if effTimeout <= 0 {
		effTimeout = 15 * time.Second
	}

	runCtx, cancel := context.WithTimeout(ctx, effTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, trimmed, args...)
	env := os.Environ()
	env = append(env,
		"CLAVIS_SELECTED_TEXT="+capsuleCtx.SelectedText,
		"CLAVIS_APP_BUNDLE_ID="+capsuleCtx.BundleID,
		"CLAVIS_APP_NAME="+capsuleCtx.AppName,
	)
	cmd.Env = env

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil

	exitCode := 0
	if err != nil {
		if timedOut {
			exitCode = -1
			return CLIExecutionResult{
				ExitCode: exitCode,
				Stdout:   stdoutBuf.String(),
				Stderr:   stderrBuf.String(),
				TimedOut: true,
			}, nil
		}
		if ctx.Err() != nil {
			return CLIExecutionResult{
				ExitCode: -1,
				Stdout:   stdoutBuf.String(),
				Stderr:   stderrBuf.String(),
				TimedOut: false,
			}, ctx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			return CLIExecutionResult{
				ExitCode: exitCode,
				Stdout:   stdoutBuf.String(),
				Stderr:   stderrBuf.String(),
				TimedOut: false,
			}, nil
		}
		return CLIExecutionResult{
			ExitCode: -1,
			Stdout:   stdoutBuf.String(),
			Stderr:   stderrBuf.String(),
			TimedOut: false,
		}, err
	}

	return CLIExecutionResult{
		ExitCode: exitCode,
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		TimedOut: false,
	}, nil
}
