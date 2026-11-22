package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yeisme/clavis/pkg/core/pluginprotocol"
)

var (
	ErrPluginUnavailable = errors.New("plugin is unavailable")
	ErrPluginNotFound    = errors.New("plugin not found")
	ErrPluginClosed      = errors.New("plugin supervisor is closed")
	ErrPluginTimeout     = errors.New("plugin request timed out")
)

// Process is the small process surface needed by Supervisor. Implementations
// can be fakes in tests; no shell is involved in launching a process.
type Process interface {
	Stdin() (io.WriteCloser, error)
	Stdout() (io.Reader, error)
	Stderr() (io.Reader, error)
	Start() error
	Wait() error
	Kill() error
}

// ProcessLauncher creates a process for one plugin. The context is advisory
// for launch; Supervisor still calls Kill on request cancellation.
type ProcessLauncher interface {
	Launch(ctx context.Context, command string, args []string, dir string) (Process, error)
}

type execLauncher struct{}

func (execLauncher) Launch(ctx context.Context, command string, args []string, dir string) (Process, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	return &execProcess{cmd: cmd}, nil
}

type execProcess struct{ cmd *exec.Cmd }

func (p *execProcess) Stdin() (io.WriteCloser, error) { return p.cmd.StdinPipe() }
func (p *execProcess) Stdout() (io.Reader, error)     { return p.cmd.StdoutPipe() }
func (p *execProcess) Stderr() (io.Reader, error)     { return p.cmd.StderrPipe() }
func (p *execProcess) Start() error                   { return p.cmd.Start() }
func (p *execProcess) Wait() error                    { return p.cmd.Wait() }
func (p *execProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

// Options configures a Supervisor.
type Options struct {
	Directory      string
	Registry       Registry
	Launcher       ProcessLauncher
	RequestTimeout time.Duration
	MaxMessageSize int
	MaxOutputSize  int
	LookupTimeout  time.Duration
	CrashLimit     int
}

// Supervisor scans and owns local plugin processes. A plugin process is
// started only on its first Invoke call.
type Supervisor struct {
	mu            sync.RWMutex
	plugins       map[string]*runtime
	order         []string
	registry      Registry
	launcher      ProcessLauncher
	timeout       time.Duration
	max           int
	maxOutput     int
	lookupTimeout time.Duration
	crashLimit    int
	closed        bool
	watchCancel   context.CancelFunc
	manifestState map[string]string
	directoryPath string
}

type runtime struct {
	manifest      Manifest
	mu            sync.Mutex
	process       Process
	stdin         io.WriteCloser
	encoder       *pluginprotocol.Encoder
	responses     chan pluginprotocol.Response
	done          chan struct{}
	doneOnce      sync.Once
	available     bool
	nextID        atomic.Uint64
	startErr      error
	crashes       int
	disabled      bool
	registry      Registry
	registered    map[string]struct{}
	crashLimit    int
	stopping      bool
	crashRecorded bool
	owner         *Supervisor
}

// NewSupervisor scans opts.Directory and registers every valid manifest.
func NewSupervisor(opts Options) (*Supervisor, error) {
	if opts.Directory == "" {
		return nil, errors.New("plugin directory is required")
	}
	if opts.Registry == nil {
		opts.Registry = nopRegistry{}
	}
	if opts.Launcher == nil {
		opts.Launcher = execLauncher{}
	}
	if opts.MaxMessageSize <= 0 {
		opts.MaxMessageSize = pluginprotocol.DefaultMaxMessageSize
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = 30 * time.Second
	}
	if opts.MaxOutputSize <= 0 {
		opts.MaxOutputSize = opts.MaxMessageSize
	}
	manifests, err := scanManifests(opts.Directory)
	if err != nil {
		return nil, err
	}
	if opts.LookupTimeout <= 0 {
		opts.LookupTimeout = 100 * time.Millisecond
	}
	if opts.CrashLimit <= 0 {
		opts.CrashLimit = 3
	}
	s := &Supervisor{plugins: make(map[string]*runtime), registry: opts.Registry, launcher: opts.Launcher, timeout: opts.RequestTimeout, max: opts.MaxMessageSize, maxOutput: opts.MaxOutputSize, lookupTimeout: opts.LookupTimeout, crashLimit: opts.CrashLimit, manifestState: make(map[string]string), directoryPath: opts.Directory}
	for _, manifest := range manifests {
		if _, exists := s.plugins[manifest.ID]; exists {
			return nil, fmt.Errorf("duplicate plugin id %q", manifest.ID)
		}
		r := newRuntime(s, manifest, opts.CrashLimit)
		s.manifestState[manifest.ID] = manifestSignature(manifest)
		s.plugins[manifest.ID] = r
		s.order = append(s.order, manifest.ID)
		metadata := Metadata{ID: manifest.ID, Name: manifest.Name, Version: manifest.Version, Description: manifest.Description, Capabilities: append([]string(nil), manifest.Capabilities...), StyleFields: append([]string(nil), manifest.StyleFields...), Permissions: manifest.Permissions, ManifestPath: filepath.Join(manifest.Dir, "plugin.toml")}
		if err := opts.Registry.RegisterPlugin(metadata); err != nil {
			return nil, fmt.Errorf("register plugin %q: %w", manifest.ID, err)
		}
		for _, name := range manifest.Capabilities {
			if err := opts.Registry.RegisterCapability(manifest.ID, Capability{Name: name}); err != nil {
				return nil, fmt.Errorf("register capability %q: %w", name, err)
			}
			r.registered[name] = struct{}{}
		}
	}
	return s, nil
}

// Plugins returns registered plugin metadata, in filesystem scan order.
func (s *Supervisor) Plugins() []Metadata {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Metadata, 0, len(s.plugins))
	for _, id := range s.order {
		r := s.plugins[id]
		out = append(out, Metadata{ID: r.manifest.ID, Name: r.manifest.Name, Version: r.manifest.Version, Description: r.manifest.Description, Capabilities: append([]string(nil), r.manifest.Capabilities...), StyleFields: append([]string(nil), r.manifest.StyleFields...), Permissions: r.manifest.Permissions, ManifestPath: filepath.Join(r.manifest.Dir, "plugin.toml")})
	}
	return out
}

// Available reports whether a plugin is registered and has not crashed or
// timed out. It does not start the plugin.
func (s *Supervisor) Available(id string) bool {
	s.mu.RLock()
	r := s.plugins[id]
	s.mu.RUnlock()
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.available && !r.disabled
}

// PluginStatus is a diagnostic snapshot for a discovered plugin.
type PluginStatus struct {
	ID           string
	Available    bool
	Disabled     bool
	CrashCount   int
	LastError    string
	Capabilities []string
}

func (s *Supervisor) Status(id string) (PluginStatus, error) {
	r, err := s.plugin(id)
	if err != nil {
		return PluginStatus{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	status := PluginStatus{ID: r.manifest.ID, Available: r.available && !r.disabled, Disabled: r.disabled, CrashCount: r.crashes, Capabilities: append([]string(nil), r.manifest.Capabilities...)}
	if r.startErr != nil {
		status.LastError = r.startErr.Error()
	}
	return status, nil
}

func (r *runtime) recordCrash(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping || r.disabled || r.crashRecorded {
		return
	}
	r.crashRecorded = true
	r.crashes++
	r.startErr = err
	r.available = r.crashes < r.crashLimit
	if r.crashes >= r.crashLimit {
		r.disabled = true
	}
	r.process = nil
	r.encoder = nil
	r.responses = nil
	if r.crashes < r.crashLimit {
		r.available = true
	}
}

type initializeParams struct {
	APIVersion string `json:"api_version"`
}
type invokeParams struct {
	Capability string          `json:"capability"`
	Params     json.RawMessage `json:"params,omitempty"`
}
type cancelParams struct {
	ID uint64 `json:"id"`
}
type capabilitiesResult struct {
	Capabilities []Capability `json:"capabilities"`
}

func (s *Supervisor) plugin(id string) (*runtime, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrPluginClosed
	}
	r := s.plugins[id]
	if r == nil {
		return nil, ErrPluginNotFound
	}
	return r, nil
}

func (s *Supervisor) ensureStarted(ctx context.Context, r *runtime) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.disabled {
		return ErrPluginUnavailable
	}
	if !r.available {
		if r.startErr != nil {
			return fmt.Errorf("%w: %v", ErrPluginUnavailable, r.startErr)
		}
		return ErrPluginUnavailable
	}
	if r.process != nil {
		return nil
	}
	r.stopping = false
	r.crashRecorded = false
	r.startErr = nil
	r.doneOnce = sync.Once{}
	command, args := r.manifest.executable()
	p, err := s.launcher.Launch(ctx, command, args, r.manifest.Dir)
	if err != nil {
		r.available = false
		r.startErr = err
		return fmt.Errorf("launch plugin: %w", err)
	}
	// Pipe endpoints must be acquired before Start for os/exec and are also
	// acquired here for fakes so launch behavior is deterministic.
	stdin, stdinErr := p.Stdin()
	stdout, stdoutErr := p.Stdout()
	stderr, stderrErr := p.Stderr()
	if stdinErr != nil || stdoutErr != nil || stderrErr != nil || stdin == nil || stdout == nil || stderr == nil {
		r.available = false
		r.startErr = fmt.Errorf("process stdio is incomplete: stdin=%v stdout=%v stderr=%v", stdinErr, stdoutErr, stderrErr)
		_ = p.Kill()
		return r.startErr
	}
	if err := p.Start(); err != nil {
		r.available = false
		r.startErr = err
		_ = p.Kill()
		return fmt.Errorf("start plugin: %w", err)
	}
	r.process, r.stdin = p, stdin
	r.encoder = pluginprotocol.NewEncoderSize(stdin, s.max)
	r.responses = make(chan pluginprotocol.Response, 8)
	r.done = make(chan struct{})
	go r.readResponses(stdout, s.max)
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go r.waitProcess()
	return r.handshakeLocked(ctx)
}

func (r *runtime) readResponses(stdout io.Reader, max int) {
	decoder := pluginprotocol.NewDecoderSize(stdout, max)
	for {
		message, err := decoder.Read()
		if err != nil {
			r.closeDone()
			r.recordCrash(err)
			return
		}
		if message.Response != nil {
			select {
			case r.responses <- *message.Response:
			case <-r.done:
				return
			}
		}
	}
}
func (r *runtime) waitProcess() {
	err := r.process.Wait()
	r.closeDone()
	if err != nil {
		r.recordCrash(err)
	}
}

func (r *runtime) handshakeLocked(ctx context.Context) error {
	initializeID := r.nextID.Add(1)
	if err := r.encoder.Write(pluginprotocol.Request{JSONRPC: pluginprotocol.Version, Method: "initialize", Params: mustJSON(initializeParams{APIVersion: "1"}), ID: initializeID}); err != nil {
		return r.unavailable(err)
	}
	if _, err := r.awaitResponse(ctx, initializeID); err != nil {
		return r.unavailable(err)
	}
	id := r.nextID.Add(1)
	if err := r.encoder.Write(pluginprotocol.Request{JSONRPC: pluginprotocol.Version, Method: "list_capabilities", ID: id}); err != nil {
		return r.unavailable(err)
	}
	response, err := r.awaitResponse(ctx, id)
	if err != nil {
		return r.unavailable(err)
	}
	if response.Error != nil {
		return r.unavailable(errors.New(response.Error.Message))
	}
	var result capabilitiesResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		var list []Capability
		if err := json.Unmarshal(response.Result, &list); err != nil {
			return r.unavailable(fmt.Errorf("list_capabilities result: %w", err))
		}
		result.Capabilities = list
	}
	for _, capability := range result.Capabilities {
		if capability.Name == "" {
			return r.unavailable(errors.New("list_capabilities returned an unnamed capability"))
		}
		if _, exists := r.registered[capability.Name]; exists {
			continue
		}
		if err := r.registry.RegisterCapability(r.manifest.ID, capability); err != nil {
			return r.unavailable(fmt.Errorf("register capability %q: %w", capability.Name, err))
		}
		r.registered[capability.Name] = struct{}{}
	}
	return nil
}

func (r *runtime) unavailable(err error) error {
	r.available = false
	r.startErr = err
	if r.process != nil {
		_ = r.process.Kill()
	}
	return fmt.Errorf("%w: %v", ErrPluginUnavailable, err)
}
func (r *runtime) awaitResponse(ctx context.Context, id uint64) (pluginprotocol.Response, error) {
	for {
		select {
		case response := <-r.responses:
			if response.ID == nil || fmt.Sprint(response.ID) != fmt.Sprint(id) {
				continue
			}
			return response, nil
		case <-r.done:
			return pluginprotocol.Response{}, ErrPluginUnavailable
		case <-ctx.Done():
			return pluginprotocol.Response{}, ctx.Err()
		}
	}
}
func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func (r *runtime) closeDone()        { r.doneOnce.Do(func() { close(r.done) }) }

// Invoke starts a plugin if necessary and calls one declared capability. The
// returned bytes are the JSON-RPC result, not the enclosing response.
func (s *Supervisor) Invoke(ctx context.Context, id, capability string, params json.RawMessage) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if capability == "" {
		return nil, errors.New("capability is required")
	}
	r, err := s.plugin(id)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > s.timeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	if err := s.ensureStarted(ctx, r); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.registered[capability]; !ok {
		return nil, ErrCapabilityDenied
	}
	if !r.available {
		return nil, ErrPluginUnavailable
	}
	requestID := r.nextID.Add(1)
	if err := r.encoder.Write(pluginprotocol.Request{JSONRPC: pluginprotocol.Version, Method: "invoke", Params: mustJSON(invokeParams{Capability: capability, Params: params}), ID: requestID}); err != nil {
		return nil, r.unavailable(err)
	}
	response, err := r.awaitResponse(ctx, requestID)
	if err != nil {
		_ = r.encoder.Write(pluginprotocol.Notification{JSONRPC: pluginprotocol.Version, Method: "cancel", Params: mustJSON(cancelParams{ID: requestID})})
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			r.available = false
			r.startErr = err
			_ = r.process.Kill()
			return nil, ErrPluginTimeout
		}
		return nil, r.unavailable(err)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("plugin error %d: %s", response.Error.Code, response.Error.Message)
	}
	if len(response.Result) > s.maxOutput {
		return nil, r.unavailable(pluginprotocol.ErrFrameTooLarge)
	}
	return append(json.RawMessage(nil), response.Result...), nil
}

// Cancel asks a running plugin to cancel an invocation by its JSON-RPC ID.
// Invoke returns only the result, so callers normally use this method when
// they maintain their own request IDs through a lower-level integration.
func (s *Supervisor) Cancel(ctx context.Context, id string, requestID uint64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r, err := s.plugin(id)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.available || r.encoder == nil {
		return ErrPluginUnavailable
	}
	if err := r.encoder.Write(pluginprotocol.Request{JSONRPC: pluginprotocol.Version, Method: "cancel", Params: mustJSON(cancelParams{ID: requestID})}); err != nil {
		return r.unavailable(err)
	}
	return nil
}

// Shutdown asks each running plugin to shut down and then terminates any
// process that does not exit promptly.
func (s *Supervisor) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	rs := make([]*runtime, 0, len(s.plugins))
	for _, r := range s.plugins {
		rs = append(rs, r)
	}
	s.mu.Unlock()
	for _, r := range rs {
		r.mu.Lock()
		r.stopping = true
		if r.process == nil {
			r.mu.Unlock()
			continue
		}
		_ = r.encoder.Write(pluginprotocol.Notification{JSONRPC: pluginprotocol.Version, Method: "shutdown"})
		done := r.done
		p := r.process
		r.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			_ = p.Kill()
		case <-time.After(time.Second):
			_ = p.Kill()
		}
	}
	return nil
}

type nopRegistry struct{}

func (nopRegistry) RegisterPlugin(Metadata) error               { return nil }
func (nopRegistry) RegisterCapability(string, Capability) error { return nil }
