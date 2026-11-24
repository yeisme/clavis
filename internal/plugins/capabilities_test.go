package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/automation"
	"github.com/yeisme/clavis/pkg/core/pluginprotocol"
)

func capabilityPlugin(t *testing.T, caps string, handler func(string) any) (*Supervisor, func()) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "id=\"demo\"\nname=\"Demo\"\nversion=\"1\"\nentrypoint=\"fake\"\ncapabilities=[" + caps + "]\nstyle_fields=[\"text\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p := newFakeProcess(func(p *fakeProcess) {
		d := pluginprotocol.NewDecoder(p.stdinR)
		e := pluginprotocol.NewEncoder(p.stdoutW)
		for {
			m, err := d.Read()
			if err != nil {
				return
			}
			if m.Request == nil {
				continue
			}
			switch m.Request.Method {
			case "initialize":
				writeResponse(e, m.Request.ID, map[string]bool{"ok": true})
			case "list_capabilities":
				writeResponse(e, m.Request.ID, capabilitiesResult{Capabilities: []Capability{{Name: "candidates.lookup"}, {Name: "style.render"}, {Name: "action.run"}}})
			case "invoke":
				var in invokeParams
				_ = json.Unmarshal(m.Request.Params, &in)
				writeResponse(e, m.Request.ID, handler(in.Capability))
			case "shutdown":
				_ = p.Kill()
				return
			}
		}
	})
	s, err := NewSupervisor(Options{Directory: root, Launcher: fakeLauncher{process: p}, LookupTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return s, func() { _ = s.Shutdown(context.Background()) }
}

func TestCapabilitiesLookupTimeoutAndStyleAllowList(t *testing.T) {
	s, done := capabilityPlugin(t, "\"candidates.lookup\",\"style.render\",\"action.run\"", func(cap string) any {
		if cap == "style.render" {
			return map[string]any{"text": "ok", "secret": "no"}
		}
		if cap == "candidates.lookup" {
			return []Candidate{{Text: "x"}}
		}
		return map[string]any{"status": "succeeded"}
	})
	defer done()
	if got := s.LookupCandidates(context.Background(), "demo", CandidateRequest{TextSummary: "safe"}); len(got) != 1 || got[0].Text != "x" {
		t.Fatalf("lookup=%#v", got)
	}
	style, err := s.RenderStyle(context.Background(), "demo", json.RawMessage(`{}`))
	if err != nil || style["text"] != "ok" || style["secret"] != nil {
		t.Fatalf("style=%#v err=%v", style, err)
	}
}

func TestCapabilitiesRunActionUsesPluginProvider(t *testing.T) {
	s, done := capabilityPlugin(t, "\"action.run\"", func(string) any {
		return map[string]any{"actionId": "demo-action", "status": "succeeded", "output": "ok"}
	})
	defer done()
	runner, err := s.NewAutomationActionRunner("demo", AllowAllGate{})
	if err != nil {
		t.Fatal(err)
	}
	plan := []automation.WorkflowStep{{ID: "one", ActionID: "demo-action"}}
	result, err := runner.Run(context.Background(), plan, nil)
	if err != nil || len(result.Steps) != 1 || result.Steps[0].Result.Status != automation.ResultSucceeded {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

type funcGate func(context.Context, string, []string) error

func (f funcGate) Allow(ctx context.Context, id string, perms []string) error {
	return f(ctx, id, perms)
}

func TestNilActionGateFailsClosed(t *testing.T) {
	var invoked atomic.Int32
	s, done := capabilityPlugin(t, "\"action.run\"", func(string) any {
		invoked.Add(1)
		return map[string]any{"actionId": "demo-action", "status": "succeeded", "output": "ok"}
	})
	defer done()

	// 1. DenyAllGate directly returns ErrPermissionDenied.
	if err := (DenyAllGate{}).Allow(context.Background(), "demo", nil); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("DenyAllGate.Allow err = %v, want ErrPermissionDenied", err)
	}

	// 2. RunAction with nil gate fails closed without invoking the plugin.
	plan := automation.ExecutionPlan{{ID: "one", ActionID: "demo-action"}}
	baseRunner := automation.NewRunner(nil)
	if _, err := s.RunAction(context.Background(), "demo", baseRunner, nil, plan, nil); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("RunAction(nil gate) err = %v, want ErrPermissionDenied", err)
	}

	// 3. NewAutomationActionRunner with nil gate fails closed without invoking the plugin.
	runner, err := s.NewAutomationActionRunner("demo", nil)
	if err != nil {
		t.Fatalf("NewAutomationActionRunner(nil gate): %v", err)
	}
	execRes, err := runner.Run(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("runner.Run unexpected transport error: %v", err)
	}
	if len(execRes.Steps) != 1 || execRes.Steps[0].Result.Status != automation.ResultFailed {
		t.Fatalf("expected failed step on nil gate, got %+v", execRes)
	}
	if !strings.Contains(execRes.Steps[0].Result.Error, ErrPermissionDenied.Error()) {
		t.Fatalf("step error = %q, want %q", execRes.Steps[0].Result.Error, ErrPermissionDenied.Error())
	}

	// 4. actionProvider with nil gate fails closed directly.
	prov := actionProvider{supervisor: s, pluginID: "demo", gate: nil}
	actionRes, err := prov.Invoke(context.Background(), automation.ActionInvocation{ActionID: "demo-action"})
	if !errors.Is(err, ErrPermissionDenied) || actionRes.Status != automation.ResultFailed {
		t.Fatalf("actionProvider.Invoke(nil gate) = (%+v, %v), want ResultFailed + ErrPermissionDenied", actionRes, err)
	}

	if invoked.Load() != 0 {
		t.Fatalf("plugin action handler was invoked %d times despite nil gate", invoked.Load())
	}

	// 5. Explicit policy gate allows when approved and denies when rejected.
	denyPolicy := funcGate(func(context.Context, string, []string) error {
		return errors.New("policy rejected")
	})
	if _, err := s.RunAction(context.Background(), "demo", baseRunner, denyPolicy, plan, nil); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("RunAction(denyPolicy) err = %v, want ErrPermissionDenied", err)
	}
	if invoked.Load() != 0 {
		t.Fatalf("plugin action handler was invoked %d times despite denyPolicy", invoked.Load())
	}

	allowRes, err := s.RunAction(context.Background(), "demo", baseRunner, AllowAllGate{}, plan, nil)
	if err != nil || len(allowRes.Steps) != 1 || allowRes.Steps[0].Result.Status != automation.ResultSucceeded {
		t.Fatalf("RunAction(AllowAllGate) = (%+v, %v), want succeeded", allowRes, err)
	}
	if invoked.Load() != 1 {
		t.Fatalf("plugin action handler invocations = %d, want 1", invoked.Load())
	}
}
