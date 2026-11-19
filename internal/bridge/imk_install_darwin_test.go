//go:build darwin

package bridge

import (
	"context"
	"strings"
	"testing"
)

func TestInstallMacOSInputMethodIsNotImplemented(t *testing.T) {
	got, err := InstallMacOSInputMethod(context.Background(), "/definitely/not/read")
	if got != "" {
		t.Fatalf("install path = %q, want empty", got)
	}
	if err == nil || err.Error() != "IMK host is not implemented" {
		t.Fatalf("install error = %v, want stable not-implemented error", err)
	}
}

func TestInstallMacOSInputMethodHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := InstallMacOSInputMethod(ctx, "")
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("cancelled install error = %v, want context cancellation", err)
	}
}
