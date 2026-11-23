package terminal

import (
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
}
func TestResolveTerminalUnknownDefaultIsReadableError(t *testing.T) {
	cfg := config.DefaultTerminalConfig()
	cfg.Default = "missing"
	_, res, err := ResolveTerminalWithDetector(cfg, "/tmp", fakeDetector{"ghostty": true, "terminal": true})
	if err == nil || res.State != host.StateFailed || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("result=%+v err=%v", res, err)
	}
}
