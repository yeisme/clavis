package runtime

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestRegistryBeginStopAllAndIdempotency(t *testing.T) {
	r := NewRegistry(context.Background())
	ctx, done := r.Begin(AIStream, "stream")
	if r.Len() != 1 {
		t.Fatalf("Len = %d, want 1", r.Len())
	}
	report := r.StopAll("user")
	if ctx.Err() != context.Canceled {
		t.Fatalf("context error = %v, want canceled", ctx.Err())
	}
	if report.Reason != "user" || len(report.Operations) != 1 || !report.Operations[0].WasCancelled {
		t.Fatalf("unexpected report: %+v", report)
	}
	if got := r.StopAll("again"); len(got.Operations) != 0 {
		t.Fatalf("second StopAll = %+v, want empty", got)
	}
	done()
}

func TestRegistryStaleDoneDoesNotRemoveNewRegistration(t *testing.T) {
	r := NewRegistry(context.Background())
	_, oldDone := r.Begin(AIStream, "old")
	oldDone()
	_, newDone := r.Begin(AIStream, "new")
	oldDone()
	if r.Len() != 1 {
		t.Fatalf("stale done removed new operation; Len = %d", r.Len())
	}
	newDone()
	if r.Len() != 0 {
		t.Fatalf("Len after done = %d", r.Len())
	}
}

func TestRegistryRegisterReturnsWithoutWaiting(t *testing.T) {
	r := NewRegistry(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	r.Register(MCPRequest, func() {
		close(started)
		<-release
	}, "ignores cancellation")
	begin := time.Now()
	report := r.StopAll("user")
	if elapsed := time.Since(begin); elapsed > 100*time.Millisecond {
		t.Fatalf("StopAll blocked for %s", elapsed)
	}
	if len(report.Operations) != 1 || report.Operations[0].Kind != MCPRequest {
		t.Fatalf("unexpected report: %+v", report)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("registered cancel was not invoked")
	}
	close(release)
}

func TestRegistryConcurrentBeginStopAll(t *testing.T) {
	r := NewRegistry(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, done := r.Begin(AIStream, "concurrent")
				if j%2 == 0 {
					done()
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		r.StopAll("race")
	}
	wg.Wait()
	r.StopAll("final")
}
