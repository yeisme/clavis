package terminal

import (
	"slices"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

type fakeDetector map[string]bool

func (f fakeDetector) Exists(p config.TerminalProfile) bool { return f[p.Name] }

type fakeRunner struct {
	program string
	args    []string
	calls   int
}

func (r *fakeRunner) Run(program string, args ...string) error {
	r.program = program
	r.args = append([]string(nil), args...)
	r.calls++
	return nil
}

func TestResolveTerminalDefaultGhosttyAndFakeRunner(t *testing.T) {
	cfg := config.DefaultTerminalConfig()
	plan, res, err := ResolveTerminalWithDetector(cfg, "/tmp/project", fakeDetector{"ghostty": true, "terminal": true})
	if err != nil || res.State != host.StateReady || plan.Profile.Name != "ghostty" {
		t.Fatalf("plan=%+v result=%+v err=%v", plan, res, err)
	}
	r := &fakeRunner{}
	if err := Execute(plan, r); err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 || r.program != "open" || !strings.Contains(strings.Join(r.args, " "), "/tmp/project") {
		t.Fatalf("runner=%+v", r)
	}
}

func TestResolveTerminalMissingGhosttyFallsBackToTerminal(t *testing.T) {
	cfg := config.DefaultTerminalConfig()
	plan, res, err := ResolveTerminalWithDetector(cfg, "/tmp/project", fakeDetector{"ghostty": false, "terminal": true})
	if err != nil || res.State != host.StateDegraded || plan.Profile.Name != "terminal" || !strings.Contains(res.Message, "Terminal") {
		t.Fatalf("plan=%+v result=%+v err=%v", plan, res, err)
	}
	if plan.Program != "open" || !slices.Equal(plan.Args, []string{"-a", "Terminal", "/tmp/project"}) {
		t.Fatalf("Apple Terminal plan should use open -a Terminal <dir>, got program=%q args=%v", plan.Program, plan.Args)
	}
	for _, arg := range plan.Args {
		if strings.Contains(arg, "--working-directory") || arg == "--args" {
			t.Fatalf("Apple Terminal plan must not pass --working-directory or --args: %v", plan.Args)
		}
	}
}
func TestResolveTerminalBothMissingFailsWithoutPanic(t *testing.T) {
	cfg := config.DefaultTerminalConfig()
	_, res, err := ResolveTerminalWithDetector(cfg, "/tmp", fakeDetector{})
	if err == nil || res.State != host.StateFailed {
		t.Fatalf("result=%+v err=%v", res, err)
	}
}
func TestResolveTerminalReplacesDirWithSpaces(t *testing.T) {
	cfg := config.DefaultTerminalConfig()
	plan, _, err := ResolveTerminalWithDetector(cfg, "/tmp/My Project", fakeDetector{"ghostty": true})
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{}
	if err := Execute(plan, r); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(r.args, " ")
	if !strings.Contains(joined, "/tmp/My Project") || strings.Contains(joined, "{dir}") {
		t.Fatalf("args=%v", r.args)
	}
	if !slices.Equal(r.args, []string{"-n", "-b", "com.mitchellh.ghostty", "--args", "--working-directory=/tmp/My Project"}) {
		t.Fatalf("ghostty args=%v", r.args)
	}
}

func TestResolveTerminalAppleTerminalLegacyTemplateAndSpaces(t *testing.T) {
	cfg := config.TerminalConfig{
		Default: "terminal",
		Profiles: []config.TerminalProfile{{
			Name:        "terminal",
			BundleID:    "com.apple.Terminal",
			Command:     "Terminal",
			ArgTemplate: "--working-directory={dir}",
		}},
	}
	plan, res, err := ResolveTerminalWithDetector(cfg, "/tmp/My Project", fakeDetector{"terminal": true})
	if err != nil || res.State != host.StateReady {
		t.Fatalf("plan=%+v res=%+v err=%v", plan, res, err)
	}
	if plan.Program != "open" || !slices.Equal(plan.Args, []string{"-a", "Terminal", "/tmp/My Project"}) {
		t.Fatalf("legacy Apple Terminal template was not normalized: program=%q args=%v", plan.Program, plan.Args)
	}
}

func TestResolverOpenAndNonDarwinPlan(t *testing.T) {
	cfg := config.DefaultTerminalConfig()
	r := &fakeRunner{}
	resolver := NewResolverWith(cfg, fakeDetector{"ghostty": false, "terminal": true}, r)
	plan, res, err := resolver.Open("/tmp/work")
	if err != nil || res.State != host.StateDegraded || plan.Profile.Name != "terminal" {
		t.Fatalf("plan=%+v res=%+v err=%v", plan, res, err)
	}
	if r.calls != 1 || r.program != "open" || !slices.Equal(r.args, []string{"-a", "Terminal", "/tmp/work"}) {
		t.Fatalf("runner=%+v", r)
	}

	linuxPlan := makePlanForOS(config.TerminalProfile{
		Name:        "alacritty",
		Command:     "alacritty",
		ArgTemplate: "--working-directory {dir}",
	}, "/home/dev/repo", "linux")
	if linuxPlan.Program != "alacritty" || !slices.Equal(linuxPlan.Args, []string{"--working-directory", "/home/dev/repo"}) {
		t.Fatalf("linuxPlan=%+v", linuxPlan)
	}
}
func TestResolveTerminalUnknownDefaultIsReadableError(t *testing.T) {
	cfg := config.DefaultTerminalConfig()
	cfg.Default = "missing"
	_, res, err := ResolveTerminalWithDetector(cfg, "/tmp", fakeDetector{"ghostty": true, "terminal": true})
	if err == nil || res.State != host.StateFailed || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("result=%+v err=%v", res, err)
	}
}
