// Package terminal resolves configured terminal profiles into safe, testable open plans.
package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

type TerminalPlan struct {
	Profile config.TerminalProfile
	Program string
	Args    []string
	Dir     string
}

type Runner interface {
	Run(program string, args ...string) error
}
type Detector interface {
	Exists(profile config.TerminalProfile) bool
}

// ExecRunner executes a resolved TerminalPlan via os/exec.
type ExecRunner struct{}

func (ExecRunner) Run(program string, args ...string) error {
	cmd := exec.Command(program, args...)
	if program == "open" {
		return cmd.Run()
	}
	return cmd.Start()
}

// Resolver resolves and executes configured terminal profiles.
type Resolver struct {
	cfg      config.TerminalConfig
	detector Detector
	runner   Runner
}

// NewResolver constructs a Resolver backed by the system detector and ExecRunner.
func NewResolver(cfg config.TerminalConfig) *Resolver {
	return NewResolverWith(cfg, systemDetector{}, ExecRunner{})
}

// NewResolverWith constructs a Resolver with custom detector/runner seams for tests.
func NewResolverWith(cfg config.TerminalConfig, detector Detector, runner Runner) *Resolver {
	if detector == nil {
		detector = systemDetector{}
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Resolver{
		cfg:      cfg,
		detector: detector,
		runner:   runner,
	}
}

// Resolve selects a terminal profile and constructs its open plan without launching.
func (r *Resolver) Resolve(dir string) (TerminalPlan, host.OperationResult, error) {
	if r == nil {
		return ResolveTerminal(config.DefaultTerminalConfig(), dir)
	}
	return ResolveTerminalWithDetector(r.cfg, dir, r.detector)
}

// Open resolves the terminal profile and executes the plan via the configured Runner.
func (r *Resolver) Open(dir string) (TerminalPlan, host.OperationResult, error) {
	plan, res, err := r.Resolve(dir)
	if err != nil {
		return plan, res, err
	}
	runner := Runner(ExecRunner{})
	if r != nil && r.runner != nil {
		runner = r.runner
	}
	if err := Execute(plan, runner); err != nil {
		res.State = host.StateFailed
		res.Code = "terminal_open_failed"
		res.Message = err.Error()
		res.Retryable = true
		return plan, res, err
	}
	return plan, res, nil
}

type systemDetector struct{}

func (systemDetector) Exists(p config.TerminalProfile) bool {
	if p.BundleID != "" && p.Command != "" {
		roots := []string{"/Applications", "/System/Applications", "/System/Applications/Utilities", "/System/Library/CoreServices"}
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			roots = append(roots, filepath.Join(home, "Applications"))
		}
		for _, root := range roots {
			if _, err := os.Stat(filepath.Join(root, p.Command+".app")); err == nil {
				return true
			}
		}
	}
	if p.Command == "" {
		return false
	}
	_, err := exec.LookPath(p.Command)
	return err == nil
}

// ResolveTerminal picks the configured profile. The preferred Ghostty profile
// degrades to Apple Terminal when unavailable, and never silently selects an
// unrelated profile. No process is started by this function.
func ResolveTerminal(cfg config.TerminalConfig, dir string) (TerminalPlan, host.OperationResult, error) {
	return resolveTerminal(cfg, dir, systemDetector{})
}

func ResolveTerminalWithDetector(cfg config.TerminalConfig, dir string, detector Detector) (TerminalPlan, host.OperationResult, error) {
	if detector == nil {
		detector = systemDetector{}
	}
	return resolveTerminal(cfg, dir, detector)
}

func resolveTerminal(cfg config.TerminalConfig, dir string, detector Detector) (TerminalPlan, host.OperationResult, error) {
	if strings.TrimSpace(dir) == "" {
		dir, _ = os.UserHomeDir()
	}
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	profiles := cfg.Profiles
	if len(profiles) == 0 {
		profiles = config.DefaultTerminalConfig().Profiles
	}
	preferred := strings.TrimSpace(cfg.Default)
	if preferred == "" {
		preferred = "ghostty"
	}
	find := func(name string) (config.TerminalProfile, bool) {
		for _, p := range profiles {
			if p.Name == name {
				return p, true
			}
		}
		return config.TerminalProfile{}, false
	}
	p, ok := find(preferred)
	if !ok {
		return TerminalPlan{}, host.OperationResult{State: host.StateFailed, Code: "terminal_profile_missing", Message: fmt.Sprintf("terminal profile %q is not configured", preferred), ObservedAt: time.Now()}, fmt.Errorf("terminal profile %q is not configured", preferred)
	}
	if !detector.Exists(p) {
		fallback, has := find("terminal")
		if !has {
			fallback, has = find("apple-terminal")
		}
		if !has || !detector.Exists(fallback) {
			return TerminalPlan{}, host.OperationResult{State: host.StateFailed, Code: "terminal_unavailable", Message: fmt.Sprintf("terminal %q and Apple Terminal are unavailable", preferred), ObservedAt: time.Now()}, fmt.Errorf("no configured terminal is available")
		}
		p = fallback
		return makePlan(p, dir), host.OperationResult{State: host.StateDegraded, Code: "terminal_fallback", Message: fmt.Sprintf("%s 未找到，已用 Terminal", preferred), ObservedAt: time.Now(), Retryable: true}, nil
	}
	return makePlan(p, dir), host.OperationResult{State: host.StateReady, Code: "terminal_ready", Message: fmt.Sprintf("using %s", p.Name), ObservedAt: time.Now()}, nil
}

func isAppleTerminal(p config.TerminalProfile) bool {
	return strings.EqualFold(strings.TrimSpace(p.BundleID), "com.apple.Terminal") ||
		strings.EqualFold(strings.TrimSpace(p.Command), "Terminal")
}

func makePlan(p config.TerminalProfile, dir string) TerminalPlan {
	return makePlanForOS(p, dir, runtime.GOOS)
}

func makePlanForOS(p config.TerminalProfile, dir, goos string) TerminalPlan {
	template := strings.TrimSpace(p.ArgTemplate)
	if isAppleTerminal(p) {
		// Apple Terminal.app does not accept --working-directory={dir}; opening the
		// directory path via `open -a Terminal <dir>` opens a window at that path.
		if template == "" || template == "--working-directory={dir}" {
			template = "{dir}"
		}
		args := templateArgs(template, dir)
		app := strings.TrimSpace(p.Command)
		var openArgs []string
		if app != "" {
			openArgs = append([]string{"-a", app}, args...)
		} else {
			openArgs = append([]string{"-b", p.BundleID}, args...)
		}
		return TerminalPlan{Profile: p, Program: "open", Args: openArgs, Dir: dir}
	}
	if template == "" {
		template = "--working-directory={dir}"
	}
	args := templateArgs(template, dir)
	if (filepath.IsAbs(p.Command) && p.BundleID == "") || (goos != "darwin" && p.BundleID == "" && strings.TrimSpace(p.Command) != "") {
		return TerminalPlan{Profile: p, Program: p.Command, Args: args, Dir: dir}
	}
	var openArgs []string
	if p.BundleID != "" {
		openArgs = []string{"-n", "-b", p.BundleID, "--args"}
	} else {
		openArgs = []string{"-na", p.Command, "--args"}
	}
	openArgs = append(openArgs, args...)
	return TerminalPlan{Profile: p, Program: "open", Args: openArgs, Dir: dir}
}

func templateArgs(template, dir string) []string {
	fields := strings.Fields(template)
	args := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, "\\\"")
		args = append(args, strings.ReplaceAll(field, "{dir}", dir))
	}
	return args
}

// OpenLeaderTerminal resolves and executes the terminal action through the
// injected runner. It is the UI-independent Leader action boundary.
func OpenLeaderTerminal(cfg config.TerminalConfig, dir string, runner Runner) (host.OperationResult, error) {
	plan, result, err := ResolveTerminal(cfg, dir)
	if err != nil {
		return result, err
	}
	if err := Execute(plan, runner); err != nil {
		result.State = host.StateFailed
		result.Code = "terminal_open_failed"
		result.Message = err.Error()
		result.Retryable = true
		return result, err
	}
	return result, nil
}

func Execute(plan TerminalPlan, runner Runner) error {
	if runner == nil {
		return fmt.Errorf("nil terminal runner")
	}
	return runner.Run(plan.Program, plan.Args...)
}
