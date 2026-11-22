package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
