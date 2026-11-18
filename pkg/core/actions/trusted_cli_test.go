package actions_test

import (
	"context"
	"os"
	"path/filepath"
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

func TestResolveTrustedRejectsSharedWritableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions test")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "tool")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	profile := config.CLITrustedProfile{ID: "shared", Enabled: true, Commands: []string{path}}
	if _, err := actions.ResolveTrusted(profile, path); err == nil {
		t.Fatal("trusted executable in temporary/shared ancestry was accepted")
	}
	if _, err := actions.ResolveTrusted(config.CLITrustedProfile{ID: "system", Enabled: true, Commands: []string{"/bin/echo"}}, "/bin/echo"); err != nil {
		t.Fatalf("system executable rejected: %v", err)
	}
}

func TestCLIExecutorDenylistCannotBeOverriddenByAllowEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix printenv test")
	}
	command := "/usr/bin/printenv"
	if _, err := os.Stat(command); err != nil {
		command = "/bin/printenv"
	}
	for _, name := range []string{"KEY_VALUE", "TOKEN_VALUE", "SECRET_VALUE", "PASSWORD_VALUE", "AUTH_VALUE", "CREDENTIAL_VALUE", "COOKIE_VALUE", "SESSION_VALUE", "CLAVIS_INTERNAL", "CUSTOM_MODE"} {
		t.Setenv(name, "hidden")
	}
	profile := config.CLITrustedProfile{ID: "env", Enabled: true, Commands: []string{command}, AllowEnv: []string{"KEY_VALUE", "TOKEN_VALUE", "SECRET_VALUE", "PASSWORD_VALUE", "AUTH_VALUE", "CREDENTIAL_VALUE", "COOKIE_VALUE", "SESSION_VALUE", "CLAVIS_INTERNAL", "CUSTOM_MODE"}}
	for _, name := range []string{"KEY_VALUE", "TOKEN_VALUE", "SECRET_VALUE", "PASSWORD_VALUE", "AUTH_VALUE", "CREDENTIAL_VALUE", "COOKIE_VALUE", "SESSION_VALUE", "CLAVIS_INTERNAL"} {
		result := actions.NewCLIExecutor().ExecuteRequest(context.Background(), actions.CommandRequest{Profile: &profile, Command: command, Args: []string{name}})
		if strings.TrimSpace(result.Stdout) != "" {
			t.Errorf("sensitive %s leaked: %q", name, result.Stdout)
		}
	}
	result := actions.NewCLIExecutor().ExecuteRequest(context.Background(), actions.CommandRequest{Profile: &profile, Command: command, Args: []string{"CUSTOM_MODE"}})
	if strings.TrimSpace(result.Stdout) != "hidden" {
		t.Fatalf("ordinary allowlisted env missing: %q (%v)", result.Stdout, result.Err)
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
