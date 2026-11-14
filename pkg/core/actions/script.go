package actions

import (
	"context"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// ExecuteScriptComponent runs a declarative script without shell interpolation.
// The component's mode is intentionally left to the caller/UI; stdout and stderr are
// always captured so silent, notification, and replace modes have identical safety.
func ExecuteScriptComponent(ctx context.Context, spec config.ScriptComponentSpec, capCtx engine.CapsuleContext) (CLIExecutionResult, error) {
	if !spec.Enabled {
		return CLIExecutionResult{ExitCode: -1}, ErrEmptyCommand
	}
	timeout := time.Duration(spec.TimeoutMs) * time.Millisecond
	args := make([]string, len(spec.Args))
	copy(args, spec.Args)
	// A small, explicit placeholder vocabulary avoids invoking a shell while still
	// making declarative scripts useful in the active capsule context.
	for i, arg := range args {
		arg = strings.ReplaceAll(arg, "{{selection}}", capCtx.SelectedText)
		arg = strings.ReplaceAll(arg, "{{app}}", capCtx.AppName)
		arg = strings.ReplaceAll(arg, "{{bundle_id}}", capCtx.BundleID)
		arg = strings.ReplaceAll(arg, "{{url}}", capCtx.ActiveURL)
		args[i] = arg
	}
	return NewCLIExecutor().Execute(ctx, spec.Command, args, capCtx, timeout)
}
