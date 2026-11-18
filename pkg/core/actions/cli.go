package actions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// CLIExecutionResult is retained for compatibility with the original API.
type CLIExecutionResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	TimedOut bool   `json:"timedOut"`
}

type CommandRequest struct {
	Profile   *config.CLITrustedProfile
	Command   string
	Args      []string
	Dir       string
	Stdin     []byte
	Timeout   time.Duration
	Env       []string
	DryRun    bool
	ProfileID string
}
type CommandResult struct {
	Stdout          string
	Stderr          string
	ExitCode        int
	TimedOut        bool
	Canceled        bool
	Err             error
	ResolvedArgv    []string
	EnvPreview      []string
	ExpectedTimeout time.Duration
	DryRun          bool
}
type Executor interface {
	ExecuteRequest(context.Context, CommandRequest) CommandResult
}

var ErrEmptyCommand = errors.New("cli command cannot be empty")
var ErrUntrustedCommand = errors.New("cli command is not trusted by the selected profile")

// CLIExecutor executes external commands directly without shell interpolation.
type CLIExecutor struct{ DefaultTimeout time.Duration }

func NewCLIExecutor() CLIExecutor { return CLIExecutor{DefaultTimeout: 15 * time.Second} }

// ResolveTrusted resolves an executable only when it is listed by an enabled profile.
func ResolveTrusted(profile config.CLITrustedProfile, command string) (string, error) {
	if !profile.Enabled {
		return "", fmt.Errorf("profile %q is disabled", profile.ID)
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return "", ErrEmptyCommand
	}
	for _, allowed := range profile.Commands {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if !filepath.IsAbs(allowed) {
			continue
		}
		clean := filepath.Clean(allowed)
		if filepath.Clean(command) != clean || !filepath.IsAbs(command) {
			continue
		}
		info, err := os.Stat(clean)
		if err != nil || info.IsDir() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) || !info.Mode().IsRegular() {
			continue
		}
		if !secureExecutablePath(clean, info) {
			continue
		}
		resolved, err := exec.LookPath(clean)
		if err != nil || resolved != clean {
			continue
		}
		// Reject symlink escapes: the configured path must resolve to itself.
		real, err := filepath.EvalSymlinks(clean)
		if err != nil || real != clean {
			continue
		}
		return resolved, nil
	}
	return "", fmt.Errorf("%w: %s", ErrUntrustedCommand, command)
}

// secureExecutablePath rejects world/group-writable executable locations. Owner
// writable files are allowed because normal user-owned profiles necessarily need
// that permission; writable shared directories such as /tmp are not trusted.
func secureExecutablePath(path string, info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
		return false
	}
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		stat, err := os.Stat(dir)
		if err != nil || !stat.IsDir() || stat.Mode()&0022 != 0 {
			return false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return true
}

var sensitiveEnv = []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "AUTH", "CREDENTIAL", "COOKIE", "SESSION"}

func sensitive(name string) bool {
	u := strings.ToUpper(name)
	if strings.HasPrefix(u, "CLAVIS_") {
		return true
	}
	for _, p := range sensitiveEnv {
		if strings.Contains(u, p) {
			return true
		}
	}
	return false
}
func sanitizeEnv(extra []string, profile config.CLITrustedProfile) []string {
	allowed := map[string]bool{"PATH": true, "HOME": true, "USER": true, "LANG": true, "TERM": true, "TMPDIR": true}
	for _, n := range profile.AllowEnv {
		allowed[n] = true
	}
	seen := map[string]bool{}
	out := []string{}
	for _, item := range append(append([]string{}, os.Environ()...), extra...) {
		n, _, ok := strings.Cut(item, "=")
		if !ok || seen[n] || !allowed[n] || sensitive(n) {
			continue
		}
		seen[n] = true
		out = append(out, item)
	}
	return out
}
func (e CLIExecutor) ExecuteRequest(ctx context.Context, req CommandRequest) CommandResult {
	res := CommandResult{ExitCode: -1}
	if ctx == nil {
		ctx = context.Background()
	}
	profile := config.CLITrustedProfile{ID: req.ProfileID, Enabled: true, Commands: []string{req.Command}, TimeoutSeconds: 15}
	if req.Profile != nil {
		profile = *req.Profile
	}
	var resolved string
	var err error
	if req.ProfileID == "" && req.Profile == nil {
		// Profileless legacy calls are kept for workflows that predate trusted profiles.
		resolved, err = exec.LookPath(req.Command)
	} else {
		resolved, err = ResolveTrusted(profile, req.Command)
	}
	if err != nil {
		res.Err = err
		return res
	}
	if req.Dir == "" {
		req.Dir = profile.DefaultDir
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = time.Duration(profile.TimeoutSeconds) * time.Second
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if timeout > 300*time.Second {
		timeout = 300 * time.Second
	}
	res.ResolvedArgv = append([]string{resolved}, req.Args...)
	env := sanitizeEnv(req.Env, profile)
	res.EnvPreview = append([]string(nil), env...)
	for i, item := range res.EnvPreview {
		name, _, _ := strings.Cut(item, "=")
		if sensitive(name) {
			res.EnvPreview[i] = name + "=<redacted>"
		}
	}
	res.ExpectedTimeout = timeout
	res.DryRun = req.DryRun
	// Keep the actual child environment separate from the redacted preview.
	childEnv := env
	if req.DryRun {
		return res
	}
	if err := ctx.Err(); err != nil {
		res.Canceled = true
		res.Err = err
		return res
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// #nosec G204 -- resolved by ResolveTrusted (or LookPath for legacy workflow compatibility), never a shell.
	cmd := exec.CommandContext(runCtx, resolved, req.Args...)
	configureProcessGroup(cmd)
	cmd.Cancel = func() error { killProcessGroup(cmd); return nil }
	cmd.Dir = req.Dir
	cmd.Env = childEnv
	if req.Stdin != nil {
		cmd.Stdin = bytes.NewReader(req.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
		return res
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		res.Canceled = true
		res.Err = ctx.Err()
		return res
	}
	if err == nil {
		res.ExitCode = 0
		return res
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res
	}
	res.Err = err
	return res
}

// Execute is the legacy compatibility wrapper. New callers should use ExecuteRequest.
func (e CLIExecutor) Execute(ctx context.Context, command string, args []string, capsuleCtx engine.CapsuleContext, timeout time.Duration) (CLIExecutionResult, error) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return CLIExecutionResult{ExitCode: -1}, ErrEmptyCommand
	}
	if ctx.Err() != nil {
		return CLIExecutionResult{ExitCode: -1}, ctx.Err()
	}
	if timeout <= 0 {
		timeout = e.DefaultTimeout
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// #nosec G204 -- legacy API deliberately executes argv directly, never through a shell.
	cmd := exec.CommandContext(runCtx, trimmed, args...)
	configureProcessGroup(cmd)
	cmd.Cancel = func() error { killProcessGroup(cmd); return nil }
	cmd.Env = append(os.Environ(), "CLAVIS_SELECTED_TEXT="+capsuleCtx.SelectedText, "CLAVIS_APP_BUNDLE_ID="+capsuleCtx.BundleID, "CLAVIS_APP_NAME="+capsuleCtx.AppName)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	runErr := cmd.Run()
	result := CLIExecutionResult{ExitCode: 0, Stdout: out.String(), Stderr: errOut.String()}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		result.ExitCode = -1
		result.TimedOut = true
		return result, nil
	}
	if runErr == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return CLIExecutionResult{ExitCode: -1, Stdout: result.Stdout, Stderr: result.Stderr}, ctx.Err()
	}
	var ex *exec.ExitError
	if errors.As(runErr, &ex) {
		result.ExitCode = ex.ExitCode()
		return result, nil
	}
	result.ExitCode = -1
	return result, runErr
}
