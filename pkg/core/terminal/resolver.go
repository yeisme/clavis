// Package terminal resolves configured terminal profiles into safe, testable open plans.
package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

type systemDetector struct{}

func (systemDetector) Exists(p config.TerminalProfile) bool {
	if p.BundleID != "" {
		for _, root := range []string{"/Applications", "/System/Applications", "/System/Library/CoreServices"} {
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

func makePlan(p config.TerminalProfile, dir string) TerminalPlan {
	template := strings.TrimSpace(p.ArgTemplate)
	if template == "" {
		template = "--working-directory={dir}"
	}
	args := templateArgs(template, dir)
	if filepath.IsAbs(p.Command) && p.BundleID == "" {
		return TerminalPlan{Profile: p, Program: p.Command, Args: args, Dir: dir}
	}
	openArgs := []string{"-na"}
	if p.BundleID != "" {
		openArgs = append(openArgs, "-b", p.BundleID)
	} else {
		openArgs = append(openArgs, p.Command)
	}
	openArgs = append(openArgs, "--args")
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
