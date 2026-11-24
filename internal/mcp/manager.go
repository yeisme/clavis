package mcp

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// Spec declares one managed server. Transport and Launcher are injection seams;
// production callers may supply either a pre-built transport or a launcher.
type Spec struct {
	ID             string
	Config         ServerConfig
	Transport      Transport
	Launcher       ProcessLauncher
	Factory        func(context.Context, Spec) (Transport, error)
	MaxConcurrency int
}

type Options struct {
	ClientOptions      ClientOptions
	MaxRestartAttempts int
	BaseBackoff        time.Duration
	Sleep              func(context.Context, time.Duration) error
}

type ServerStatus struct {
	ID        string
	Running   bool
	Tools     []ToolDescriptor
	LastError error
	Restarts  int
	StartedAt time.Time
}

type Manager struct {
	mu          sync.RWMutex
	lifecycleMu sync.Mutex
	specs       map[string]Spec
	entries     map[string]*managerEntry
	opts        Options
}

type managerEntry struct {
	client    Client
	transport Transport
	sem       chan struct{}
	status    ServerStatus
}

func NewManager(specs []Spec, opts Options) *Manager {
	if opts.MaxRestartAttempts < 0 {
		opts.MaxRestartAttempts = 0
	}
	if opts.BaseBackoff <= 0 {
		opts.BaseBackoff = 100 * time.Millisecond
	}
	if opts.Sleep == nil {
		opts.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	m := &Manager{specs: make(map[string]Spec), entries: make(map[string]*managerEntry), opts: opts}
	for _, spec := range specs {
		if spec.ID != "" {
			m.specs[spec.ID] = spec
		}
	}
	return m
}

func (m *Manager) Start(ctx context.Context, id string) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	return m.start(ctx, id)
}

func (m *Manager) start(ctx context.Context, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	spec, ok := m.specs[id]
	if !ok {
		m.mu.Unlock()
		return errors.New("mcp: unknown server " + id)
	}
	if e := m.entries[id]; e != nil && e.status.Running {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	cfg := spec.Config
	if cfg.Name == "" {
		cfg.Name = spec.ID
	}
	transport := spec.Transport
	var err error
	if transport == nil && spec.Factory != nil {
		transport, err = spec.Factory(ctx, spec)
	}
	if transport == nil && err == nil {
		if spec.Launcher == nil {
			err = errors.New("mcp: server transport or launcher is required")
		} else {
			transport, err = NewStdioTransport(ctx, cfg, spec.Launcher)
		}
	}
	if err != nil {
		m.recordError(id, err)
		return err
	}
	client := NewConfiguredClient(transport, cfg, m.opts.ClientOptions)
	if spec.MaxConcurrency <= 0 {
		spec.MaxConcurrency = 4
	}
	managed := &managedClient{Client: client, sem: make(chan struct{}, spec.MaxConcurrency)}
	_, initErr := managed.Initialize(ctx)
	if initErr != nil {
		_ = managed.Close()
		m.recordError(id, initErr)
		return initErr
	}
	tools, toolsErr := managed.ListTools(ctx)
	if toolsErr != nil {
		// Discovery happens after initialize, so this client owns a live
		// transport even though it is not usable. Close it before returning;
		// otherwise a failed start leaks the process/socket and a retry can
		// incorrectly observe a half-started entry.
		closeErr := managed.Close()
		if closeErr != nil {
			toolsErr = errors.Join(toolsErr, closeErr)
		}
		m.recordError(id, toolsErr)
		return toolsErr
	}
	m.mu.Lock()
	restarts := 0
	if old := m.entries[id]; old != nil {
		restarts = old.status.Restarts
	}
	m.entries[id] = &managerEntry{client: managed, transport: transport, sem: managed.sem, status: ServerStatus{ID: id, Running: true, Tools: tools, Restarts: restarts, StartedAt: time.Now()}}
	m.mu.Unlock()
	return nil
}

func (m *Manager) StartAll(ctx context.Context) error {
	var first error
	for id := range m.specs {
		if err := m.Start(ctx, id); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (m *Manager) Stop(ctx context.Context, id string) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	return m.stop(ctx, id)
}

func (m *Manager) stop(ctx context.Context, id string) error {
	_ = ctx
	m.mu.Lock()
	e := m.entries[id]
	if e == nil || !e.status.Running {
		m.mu.Unlock()
		return nil
	}
	e.status.Running = false
	m.mu.Unlock()
	if e.client == nil {
		return nil
	}
	if err := e.client.Close(); err != nil {
		m.recordError(id, err)
		return err
	}
	return nil
}
func (m *Manager) StopAll(ctx context.Context) error {
	var first error
	m.mu.RLock()
	ids := make([]string, 0, len(m.entries))
	for id := range m.entries {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	for _, id := range ids {
		if err := m.Stop(ctx, id); err != nil && first == nil {
			first = err
		}
	}
	return first
}
func (m *Manager) Restart(ctx context.Context, id string) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if err := m.stop(ctx, id); err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		startErr := m.start(ctx, id)
		if startErr == nil {
			return nil
		}
		if attempt >= m.opts.MaxRestartAttempts {
			return startErr
		}
		if err := m.opts.Sleep(ctx, m.opts.BaseBackoff*time.Duration(1<<attempt)); err != nil {
			return err
		}
		m.mu.Lock()
		if e := m.entries[id]; e != nil {
			e.status.Restarts++
		}
		m.mu.Unlock()
	}
}
func (m *Manager) Client(id string) Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if e := m.entries[id]; e != nil && e.status.Running {
		return e.client
	}
	return nil
}
func (m *Manager) Status() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ServerStatus, 0, len(m.specs))
	ids := make([]string, 0, len(m.specs))
	for id := range m.specs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := ServerStatus{ID: id}
		if e := m.entries[id]; e != nil {
			s = e.status
			s.Tools = append([]ToolDescriptor(nil), s.Tools...)
		}
		out = append(out, s)
	}
	return out
}
func (m *Manager) recordError(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil {
		e = &managerEntry{status: ServerStatus{ID: id}}
		m.entries[id] = e
	}
	e.status.LastError = err
}

type managedClient struct {
	Client
	sem chan struct{}
}

func (c *managedClient) Invoke(ctx context.Context, n string, a map[string]any) (ToolResult, error) {
	r, _, e := c.InvokeWithID(ctx, n, a)
	return r, e
}
func (c *managedClient) InvokeWithID(ctx context.Context, n string, a map[string]any) (ToolResult, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return ToolResult{}, "", ctx.Err()
	}
	return c.Client.InvokeWithID(ctx, n, a)
}

var _ Client = (*managedClient)(nil)
