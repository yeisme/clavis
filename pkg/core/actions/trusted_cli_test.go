package actions_test

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/actions"
	"github.com/yeisme/clavis/pkg/core/config"
)

func TestTrustedCLIRejectsUnlistedAndRelativeCommands(t *testing.T) {
	p := config.CLITrustedProfile{ID: "safe", Enabled: true, Commands: []string{"/bin/echo"}}
	if _, err := actions.ResolveTrusted(p, "echo"); err == nil {
		t.Fatal("relative command unexpectedly trusted")
	}
	if _, err := actions.ResolveTrusted(p, "/bin/sh"); err == nil {
		t.Fatal("unlisted command unexpectedly trusted")
	}
}

func TestCLIExecutorPassesArgumentsLiterally(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix test")
	}
	p := config.CLITrustedProfile{ID: "safe", Enabled: true, Commands: []string{"/bin/echo"}}
	res := (actions.NewCLIExecutor()).ExecuteRequest(context.Background(), actions.CommandRequest{Profile: &p, Command: "/bin/echo", Args: []string{"; rm -rf /"}})
	if res.Err != nil || strings.TrimSpace(res.Stdout) != "; rm -rf /" {
		t.Fatalf("literal argv lost: %+v", res)
	}
}

func TestCLIExecutorScrubsSecrets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix test")
	}
	old := os.Getenv("DEEPSEEK_API_KEY")
	_ = os.Setenv("DEEPSEEK_API_KEY", "must-not-leak")
	defer os.Setenv("DEEPSEEK_API_KEY", old)
	p := config.CLITrustedProfile{ID: "safe", Enabled: true, Commands: []string{"/usr/bin/printenv", "/bin/printenv"}}
	command := "/usr/bin/printenv"
	if _, err := os.Stat(command); err != nil {
		command = "/bin/printenv"
	}
	res := (actions.NewCLIExecutor()).ExecuteRequest(context.Background(), actions.CommandRequest{Profile: &p, Command: command, Args: []string{"DEEPSEEK_API_KEY"}})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if strings.TrimSpace(res.Stdout) != "" {
		t.Fatalf("secret leaked: %q", res.Stdout)
	}
}

func TestCLIExecutorDryRunAndCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix test")
	}
	p := config.CLITrustedProfile{ID: "safe", Enabled: true, Commands: []string{"/bin/sleep"}, TimeoutSeconds: 15}
	ex := actions.NewCLIExecutor()
	dry := ex.ExecuteRequest(context.Background(), actions.CommandRequest{Profile: &p, Command: "/bin/sleep", Args: []string{"2"}, DryRun: true})
	if !dry.DryRun || len(dry.ResolvedArgv) == 0 || dry.ExpectedTimeout != 15*time.Second {
		t.Fatalf("bad dry run: %+v", dry)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := ex.ExecuteRequest(ctx, actions.CommandRequest{Profile: &p, Command: "/bin/sleep", Args: []string{"2"}})
	if !got.Canceled {
		t.Fatalf("expected cancellation: %+v", got)
	}
}
