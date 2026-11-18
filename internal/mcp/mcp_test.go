package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTransport struct {
	mu      sync.Mutex
	calls   []string
	notices []string
	results map[string]json.RawMessage
	errors  map[string]error
	closed  bool
}

func (f *fakeTransport) Request(_ context.Context, method string, _ any) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, method)
	if err := f.errors[method]; err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), f.results[method]...), nil
}
func (f *fakeTransport) Notify(_ context.Context, method string, _ any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, method)
	return nil
}
func (f *fakeTransport) Cancel(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, MethodCancelled+":"+id)
	return nil
}
func (f *fakeTransport) Close() error { f.mu.Lock(); defer f.mu.Unlock(); f.closed = true; return nil }

func TestClientInitializeListsAndInvokes(t *testing.T) {
	transport := &fakeTransport{results: map[string]json.RawMessage{
		"initialize":     json.RawMessage(`{"protocolVersion":"2024-11-05","serverInfo":{"name":"demo","version":"1"},"capabilities":{"tools":{"listChanged":true}},"_meta":{"tenant":"test"}}`),
		"tools/list":     json.RawMessage(`{"tools":[{"name":"echo","inputSchema":{"type":"object"},"_meta":{"readOnly":true}}]}`),
		"resources/list": json.RawMessage(`{"resources":[{"uri":"file:///readme","name":"README","mimeType":"text/plain"}]}`),
		"prompts/list":   json.RawMessage(`{"prompts":[{"name":"summarize","arguments":[{"name":"text","required":true}]}]}`),
		"tools/call":     json.RawMessage(`{"content":[{"type":"text","text":"ok"}],"structuredContent":{"ok":true}}`),
	}, errors: map[string]error{}}
	client := NewClient(transport, ClientOptions{ClientName: "test"})
	manifest, err := client.Initialize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "demo" || !manifest.Capabilities.Tools.ListChanged {
		t.Fatalf("manifest = %#v", manifest)
	}
	tools, err := client.ListTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %#v, err = %v", tools, err)
	}
	resources, err := client.ListResources(context.Background())
	if err != nil || len(resources) != 1 {
		t.Fatalf("resources = %#v, err = %v", resources, err)
	}
	prompts, err := client.ListPrompts(context.Background())
	if err != nil || len(prompts) != 1 || prompts[0].Arguments[0].Required != true {
		t.Fatalf("prompts = %#v, err = %v", prompts, err)
	}
	result, err := client.Invoke(context.Background(), "echo", map[string]any{"value": "hello"})
	if err != nil || len(result.Content) != 1 {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	result.Content[0]["text"] = "mutated"
	again, err := client.Invoke(context.Background(), "echo", nil)
	if err != nil || again.Content[0]["text"] != "ok" {
		t.Fatalf("metadata/result alias leaked: %#v, %v", again, err)
	}
	if err := client.Cancel(context.Background(), "request-1"); err != nil {
		t.Fatal(err)
	}
}

func TestConfiguredClientDeniesUnlistedToolWithoutTransportRequest(t *testing.T) {
	transport := &fakeTransport{results: map[string]json.RawMessage{
		"initialize": json.RawMessage(`{"protocolVersion":"2024-11-05"}`),
	}, errors: map[string]error{}}
	client := NewConfiguredClient(transport, ServerConfig{Name: "restricted", AllowedTools: []string{"allowed"}}, ClientOptions{})
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	transport.mu.Lock()
	before := len(transport.calls)
	transport.mu.Unlock()
	if _, err := client.Invoke(context.Background(), "denied", nil); !errors.Is(err, ErrToolNotAllowed) {
		t.Fatalf("denial error = %v", err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.calls) != before {
		t.Fatalf("denied invocation sent transport request: calls=%v", transport.calls)
	}
}

func TestManagerDiscoveryFailureClosesTransportAndRetryStartsFresh(t *testing.T) {
	first := &fakeTransport{results: map[string]json.RawMessage{
		"initialize": json.RawMessage(`{"protocolVersion":"2024-11-05"}`),
	}, errors: map[string]error{"tools/list": errors.New("discovery failed")}}
	second := &fakeTransport{results: map[string]json.RawMessage{
		"initialize": json.RawMessage(`{"protocolVersion":"2024-11-05"}`),
		"tools/list": json.RawMessage(`{"tools":[{"name":"ok"}]}`),
	}, errors: map[string]error{}}
	attempt := 0
	manager := NewManager([]Spec{{ID: "demo", Config: ServerConfig{Name: "demo"}, Factory: func(context.Context, Spec) (Transport, error) {
		attempt++
		if attempt == 1 {
			return first, nil
		}
		return second, nil
	}}}, Options{})
	if err := manager.Start(context.Background(), "demo"); err == nil {
		t.Fatal("discovery failure unexpectedly succeeded")
	}
	if !first.closed {
		t.Fatal("failed discovery transport was not closed")
	}
	if status := manager.Status()[0]; status.Running {
		t.Fatalf("failed server is still running: %+v", status)
	}
	if err := manager.Start(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if !second.closed && manager.Client("demo") == nil {
		t.Fatal("retry did not install a running client")
	}
	if status := manager.Status()[0]; !status.Running || len(status.Tools) != 1 {
		t.Fatalf("retry status = %+v", status)
	}
	if err := manager.StopAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !second.closed {
		t.Fatal("stop all did not close retry transport")
	}
}

func TestClientRequiresInitializationAndPreservesRPCError(t *testing.T) {
	rpcErr := &RPCError{Code: -32601, Message: "missing", Data: json.RawMessage(`{"method":"tools/list"}`)}
	transport := &fakeTransport{results: map[string]json.RawMessage{}, errors: map[string]error{"tools/list": rpcErr}}
	client := NewClient(transport, ClientOptions{})
	if _, err := client.ListTools(context.Background()); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("error = %v", err)
	}
	transport.results["initialize"] = json.RawMessage(`{"protocolVersion":"2024-11-05"}`)
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := client.ListTools(context.Background())
	if !errors.Is(err, rpcErr) || !strings.Contains(err.Error(), "-32601") {
		t.Fatalf("error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTools(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("after close = %v", err)
	}
}

type launcherFunc struct {
	got     ProcessSpec
	process Process
}

func (l *launcherFunc) Launch(_ context.Context, spec ProcessSpec) (Process, error) {
	l.got = spec
	return l.process, nil
}

type fakeProcess struct {
	io.Reader
	io.Writer
	closed bool
	killed bool
}

func (p *fakeProcess) Close() error { p.closed = true; return nil }
func (p *fakeProcess) Kill() error  { p.killed = true; return nil }
func (p *fakeProcess) Wait() error  { return nil }

func TestNewStdioClientInjectsProcessSpec(t *testing.T) {
	// The launcher is intentionally exercised for config plumbing; no real
	// process is created and no network is contacted.
	process := &fakeProcess{Reader: strings.NewReader(""), Writer: io.Discard}
	launcher := &launcherFunc{process: process}
	client, err := NewStdioClient(context.Background(), ServerConfig{Name: "server", Command: "fake", Args: []string{"--stdio"}, Environment: map[string]string{"MODE": "test"}, WorkingDir: "/tmp"}, launcher, ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if launcher.got.Command != "fake" || launcher.got.Args[0] != "--stdio" || launcher.got.Environment["MODE"] != "test" || launcher.got.WorkingDir != "/tmp" {
		t.Fatalf("spec = %#v", launcher.got)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

type fakeHTTPDoer struct {
	response *http.Response
	request  *http.Request
}

func (f *fakeHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	f.request = req
	return f.response, nil
}

func TestLocalHTTPTransportIsOptInAndLocalOnly(t *testing.T) {
	if _, err := NewLocalHTTPTransport(HTTPTransportOptions{URL: "http://127.0.0.1:1234", Client: &fakeHTTPDoer{}}); !errors.Is(err, ErrHTTPOptIn) {
		t.Fatalf("opt-in error = %v", err)
	}
	if _, err := NewLocalHTTPTransport(HTTPTransportOptions{URL: "http://example.com", OptIn: true, Client: &fakeHTTPDoer{}}); !errors.Is(err, ErrHTTPNotLocal) {
		t.Fatalf("remote URL error = %v", err)
	}
	doer := &fakeHTTPDoer{response: &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","result":{"ok":true},"id":"http-1"}
`))}}
	transport, err := NewLocalHTTPTransport(HTTPTransportOptions{URL: "http://localhost:1234", OptIn: true, Client: doer})
	if err != nil {
		t.Fatal(err)
	}
	result, err := transport.Request(context.Background(), "ping", map[string]any{"x": 1})
	if err != nil || string(result) != `{"ok":true}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if doer.request.URL.Hostname() != "localhost" || doer.request.Method != http.MethodPost {
		t.Fatalf("request = %#v", doer.request)
	}
}

func TestStdioTransportContextDeadline(t *testing.T) {
	reader, writer := io.Pipe()
	process := &fakeProcess{Reader: reader, Writer: writer}
	transport, err := NewStdioTransport(context.Background(), ServerConfig{Command: "fake"}, &launcherFunc{process: process})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := transport.Request(ctx, "slow", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	_ = transport.Close()
}
