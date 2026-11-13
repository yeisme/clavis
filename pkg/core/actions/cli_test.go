package actions_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/actions"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestCLIExecutorRejectsEmptyCommand(t *testing.T) {
	ex := actions.NewCLIExecutor()
	_, err := ex.Execute(context.Background(), "   ", nil, engine.CapsuleContext{}, time.Second)
	if !errors.Is(err, actions.ErrEmptyCommand) {
		t.Fatalf("expected ErrEmptyCommand, got %v", err)
	}
}

func TestCLIExecutorInjectsEnvAndNoShellInterpolation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix printenv/echo test")
	}

	ex := actions.NewCLIExecutor()
	capsuleCtx := engine.CapsuleContext{
		BundleID:     "com.apple.Notes",
		AppName:      "Notes",
		SelectedText: "hello; echo INJECTED",
	}

	// Verify no shell interpolation: ';' and '$CLAVIS_SELECTED_TEXT' passed literally to /bin/echo
	res, err := ex.Execute(context.Background(), "/bin/echo", []string{"$CLAVIS_SELECTED_TEXT", ";", "uname"}, capsuleCtx, 2*time.Second)
	if err != nil {
		t.Fatalf("echo failed: %v", err)
	}
	if res.ExitCode != 0 || res.TimedOut {
		t.Fatalf("unexpected echo result: %+v", res)
	}
	if strings.TrimSpace(res.Stdout) != "$CLAVIS_SELECTED_TEXT ; uname" {
		t.Fatalf("expected literal args (no shell interpolation), got %q", res.Stdout)
	}

	// Verify env vars are injected
	if _, err := exec.LookPath("printenv"); err == nil {
		resEnv, err := ex.Execute(context.Background(), "printenv", []string{"CLAVIS_SELECTED_TEXT"}, capsuleCtx, 2*time.Second)
		if err != nil || strings.TrimSpace(resEnv.Stdout) != "hello; echo INJECTED" {
			t.Fatalf("unexpected CLAVIS_SELECTED_TEXT env: %+v (%v)", resEnv, err)
		}
		resBundle, err := ex.Execute(context.Background(), "printenv", []string{"CLAVIS_APP_BUNDLE_ID"}, capsuleCtx, 2*time.Second)
		if err != nil || strings.TrimSpace(resBundle.Stdout) != "com.apple.Notes" {
			t.Fatalf("unexpected CLAVIS_APP_BUNDLE_ID env: %+v (%v)", resBundle, err)
		}
		resApp, err := ex.Execute(context.Background(), "printenv", []string{"CLAVIS_APP_NAME"}, capsuleCtx, 2*time.Second)
		if err != nil || strings.TrimSpace(resApp.Stdout) != "Notes" {
			t.Fatalf("unexpected CLAVIS_APP_NAME env: %+v (%v)", resApp, err)
		}
	}
}

func TestCLIExecutorTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sleep test")
	}
	if _, err := os.Stat("/bin/sleep"); err != nil {
		t.Skip("/bin/sleep not present")
	}
	ex := actions.NewCLIExecutor()
	res, err := ex.Execute(context.Background(), "/bin/sleep", []string{"5"}, engine.CapsuleContext{}, 60*time.Millisecond)
	if err != nil {
		t.Fatalf("expected nil error with TimedOut=true, got %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("expected TimedOut=true, got %+v", res)
	}
}
