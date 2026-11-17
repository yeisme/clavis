package plugins

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/pluginprotocol"
)

type recordingRegistry struct {
	mu           sync.Mutex
	plugins      []Metadata
	capabilities []Capability
}

func (r *recordingRegistry) RegisterPlugin(m Metadata) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plugins = append(r.plugins, m)
	return nil
}
func (r *recordingRegistry) RegisterCapability(_ string, c Capability) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.capabilities = append(r.capabilities, c)
	return nil
}

type fakeProcess struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	stderrR *io.PipeReader
	stderrW *io.PipeWriter
	done    chan struct{}
	start   func(*fakeProcess)
}

func newFakeProcess(start func(*fakeProcess)) *fakeProcess {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	return &fakeProcess{stdinR: inR, stdinW: inW, stdoutR: outR, stdoutW: outW, stderrR: errR, stderrW: errW, done: make(chan struct{}), start: start}
}
func (p *fakeProcess) Stdin() (io.WriteCloser, error) { return p.stdinW, nil }
func (p *fakeProcess) Stdout() (io.Reader, error)     { return p.stdoutR, nil }
func (p *fakeProcess) Stderr() (io.Reader, error)     { return p.stderrR, nil }
func (p *fakeProcess) Start() error                   { go p.start(p); return nil }
func (p *fakeProcess) Wait() error                    { <-p.done; return nil }
func (p *fakeProcess) Kill() error {
	select {
	case <-p.done:
	default:
		close(p.done)
	}
	_ = p.stdinR.Close()
	_ = p.stdinW.Close()
	_ = p.stdoutR.Close()
	_ = p.stdoutW.Close()
	_ = p.stderrR.Close()
	_ = p.stderrW.Close()
	return nil
}

type fakeLauncher struct {
	process *fakeProcess
}

func (l fakeLauncher) Launch(context.Context, string, []string, string) (Process, error) {
	return l.process, nil
}

func writeResponse(enc *pluginprotocol.Encoder, id any, result any) {
	_ = enc.Write(pluginprotocol.Response{JSONRPC: pluginprotocol.Version, ID: id, Result: mustJSON(result)})
}

func TestSupervisorHandshakeInvokeAndLazyStart(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "demo")
	if err := os.Mkdir(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "api_version = \"1\"\nid = \"demo\"\nname = \"Demo\"\nversion = \"1.0.0\"\nentrypoint = \"fake\"\ncapabilities = [\"echo\"]\n[permissions]\nfilesystem = [\"read:config\"]\nnetwork = [\"api.example\"]\n"
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	process := newFakeProcess(func(p *fakeProcess) {
		decoder := pluginprotocol.NewDecoder(p.stdinR)
		encoder := pluginprotocol.NewEncoder(p.stdoutW)
		for {
			message, err := decoder.Read()
			if err != nil {
				return
			}
			if message.Request == nil {
				continue
			}
			switch message.Request.Method {
			case "initialize":
				writeResponse(encoder, message.Request.ID, map[string]string{"ok": "yes"})
			case "list_capabilities":
				writeResponse(encoder, message.Request.ID, capabilitiesResult{Capabilities: []Capability{{Name: "echo", Description: "echoes"}}})
			case "invoke":
				writeResponse(encoder, message.Request.ID, map[string]string{"value": "hello"})
			case "shutdown":
				_ = p.Kill()
				return
			}
		}
	})
	registry := &recordingRegistry{}
	s, err := NewSupervisor(Options{Directory: root, Registry: registry, Launcher: fakeLauncher{process: process}})
	if err != nil {
		t.Fatal(err)
	}
	if process.start != nil && s.Available("demo") != true {
		t.Fatal("manifest should be available before lazy start")
	}
	if len(registry.plugins) != 1 || registry.plugins[0].Permissions.Network[0] != "api.example" {
		t.Fatalf("metadata not registered: %#v", registry.plugins)
	}
	if len(registry.capabilities) != 1 || registry.capabilities[0].Name != "echo" {
		t.Fatalf("manifest capabilities not registered: %#v", registry.capabilities)
	}

	result, err := s.Invoke(context.Background(), "demo", "echo", json.RawMessage(`{"value":"input"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"value":"hello"}` {
		t.Fatalf("unexpected result %s", result)
	}
	if len(registry.capabilities) != 1 {
		t.Fatalf("manifest capability should be enriched without duplicate registration: %#v", registry.capabilities)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorTimeoutMarksPluginUnavailable(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "slow")
	if err := os.Mkdir(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "id = \"slow\"\nname = \"Slow\"\nversion = \"1\"\nentrypoint = \"fake\"\n"
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.toml"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	process := newFakeProcess(func(p *fakeProcess) {
		decoder := pluginprotocol.NewDecoder(p.stdinR)
		encoder := pluginprotocol.NewEncoder(p.stdoutW)
		for {
			message, err := decoder.Read()
			if err != nil {
				return
			}
			if message.Request != nil && message.Request.Method == "initialize" {
				writeResponse(encoder, message.Request.ID, map[string]bool{"ok": true})
			} else if message.Request != nil && message.Request.Method == "list_capabilities" {
				writeResponse(encoder, message.Request.ID, []Capability{{Name: "slow"}})
			}
		}
	})
	s, err := NewSupervisor(Options{Directory: root, Launcher: fakeLauncher{process: process}, RequestTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := s.Invoke(ctx, "slow", "slow", nil); err == nil {
		t.Fatal("expected timeout")
	}
	if s.Available("slow") {
		t.Fatal("timed out plugin should be unavailable")
	}
}
