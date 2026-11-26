package agentsession

import (
	"context"
	"errors"
	"testing"

	"github.com/yeisme/clavis/pkg/core/host"
)

type missingHerdrSmokeExecutor struct{}

func (missingHerdrSmokeExecutor) LookPath(string) (string, error) {
	return "", errors.New("herdr not installed")
}

func (missingHerdrSmokeExecutor) Output(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("herdr must not be invoked")
}

func TestRuntimeSmokeNoHerdrHasStableCode(t *testing.T) {
	result, err := NewHerdrFocuserWithExecutor(missingHerdrSmokeExecutor{}).FocusPane(context.Background(), "pane-1")
	if err != nil {
		t.Fatalf("missing Herdr returned transport error: %v", err)
	}
	if result.State != host.StateFailed || result.Code != "herdr_unavailable" || result.Retryable {
		t.Fatalf("missing Herdr result=%+v", result)
	}
	if result.Message != "未检测到 herdr" {
		t.Fatalf("missing Herdr message=%q", result.Message)
	}
}
