//go:build darwin

package bridge

import (
	"context"
	"testing"
)

// TestOnMainThreadBeforeTheAppRuns: without a running app (tests, the smoke
// check) the TIS work runs where it is called, and pthread_main_np is bound:
// a test goroutine is not the main thread.
func TestOnMainThreadBeforeTheAppRuns(t *testing.T) {
	if err := initDarwin(); err != nil {
		t.Skipf("no AppKit: %v", err)
	}
	if pthreadMainNP() != 0 {
		t.Fatal("pthread_main_np says a test goroutine is the main thread")
	}
	ran := false
	if err := onMainThread(context.Background(), func() { ran = true }); err != nil || !ran {
		t.Fatalf("onMainThread before the app runs: ran=%v err=%v", ran, err)
	}
	if _, err := (&darwinBridge{}).CurrentInputSource(context.Background()); err != nil {
		t.Fatalf("CurrentInputSource: %v", err)
	}
}
