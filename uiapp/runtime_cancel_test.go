package uiapp

import (
	"testing"
	"time"

	runtimecoord "github.com/yeisme/clavis/pkg/core/runtime"
)

func TestStopAllCancelsExternalOperationsWithoutWaiting(t *testing.T) {
	c := &AppController{}
	mcpCancelled := make(chan struct{})
	agentCancelled := make(chan struct{})
	release := make(chan struct{})
	c.RegisterCancellable(runtimecoord.MCPRequest, func() {
		close(mcpCancelled)
		<-release
	}, "MCP request")
	c.RegisterCancellable(runtimecoord.AgentWait, func() { close(agentCancelled) }, "agent wait")

	start := time.Now()
	report := c.StopAll("user")
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("StopAll waited for an external cancellation function")
	}
	if len(report.Operations) != 2 {
		t.Fatalf("report operations = %+v", report.Operations)
	}
	select {
	case <-mcpCancelled:
	case <-time.After(time.Second):
		t.Fatal("MCP cancellation was not invoked")
	}
	select {
	case <-agentCancelled:
	case <-time.After(time.Second):
		t.Fatal("agent cancellation was not invoked")
	}
	close(release)
}
