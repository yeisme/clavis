package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/yeisme/clavis/pkg/core/pluginprotocol"
)

// Process is the minimal lifecycle and stdio contract required by StdioTransport.
type Process interface {
	io.ReadWriteCloser
	Kill() error
}

// ProcessLauncher starts a process for a server. Implementations own process
// creation policy; StdioTransport never invokes os/exec directly. Callers may
// inject ExecProcessLauncher or a custom ProcessLauncher.
type ProcessLauncher interface {
	Launch(context.Context, ProcessSpec) (Process, error)
}

// ProcessLauncherFunc adapts a function to ProcessLauncher.
type ProcessLauncherFunc func(context.Context, ProcessSpec) (Process, error)

func (f ProcessLauncherFunc) Launch(ctx context.Context, spec ProcessSpec) (Process, error) {
	return f(ctx, spec)
}

// StdioTransport implements newline-delimited JSON-RPC over an injected process.
type StdioTransport struct {
	process          Process
	encoder          *pluginprotocol.Encoder
	decoder          *pluginprotocol.Decoder
	mu               sync.Mutex
	pending          map[string]chan response
	next             atomic.Uint64
	closed           bool
	done             chan struct{}
	closeOnce        sync.Once
	processCloseOnce sync.Once
	processCloseErr  error
}

type response struct {
	result json.RawMessage
	err    error
}

// NewStdioTransport validates config and launches a process through launcher.
func NewStdioTransport(ctx context.Context, config ServerConfig, launcher ProcessLauncher) (*StdioTransport, error) {
	if launcher == nil {
		return nil, errors.New("mcp: process launcher is required")
	}
	spec, err := config.processSpec()
	if err != nil {
		return nil, err
	}
	process, err := launcher.Launch(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("mcp: launch stdio server: %w", err)
	}
	if process == nil {
		return nil, errors.New("mcp: process launcher returned nil process")
	}
	t := &StdioTransport{process: process, encoder: pluginprotocol.NewEncoder(process), decoder: pluginprotocol.NewDecoder(process), pending: make(map[string]chan response), done: make(chan struct{})}
	go t.readLoop()
	return t, nil
}

func (t *StdioTransport) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	result, _, err := t.RequestWithID(ctx, method, params)
	return result, err
}

func (t *StdioTransport) RequestWithID(ctx context.Context, method string, params any) (json.RawMessage, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	id := fmt.Sprintf("stdio-%d", t.next.Add(1))
	encoded, err := marshalParams(params)
	if err != nil {
		return nil, "", err
	}
	request := pluginprotocol.Request{JSONRPC: pluginprotocol.Version, Method: method, Params: encoded, ID: id}
	ch := make(chan response, 1)
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, "", ErrClosed
	}
	t.pending[id] = ch
	err = t.encoder.Write(request)
	if err != nil {
		delete(t.pending, id)
	}
	t.mu.Unlock()
	if err != nil {
		return nil, "", err
	}
	select {
	case result := <-ch:
		return result.result, id, result.err
	case <-ctx.Done():
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		_ = t.Cancel(context.Background(), id)
		return nil, id, ctx.Err()
	case <-t.done:
		return nil, id, ErrClosed
	}
}

func (t *StdioTransport) Notify(ctx context.Context, method string, params any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := marshalParams(params)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	return t.encoder.Write(pluginprotocol.Notification{JSONRPC: pluginprotocol.Version, Method: method, Params: encoded})
}

func (t *StdioTransport) Cancel(ctx context.Context, requestID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := json.Marshal(map[string]any{"requestId": requestID, "reason": "cancelled"})
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	return t.encoder.Write(pluginprotocol.Notification{JSONRPC: pluginprotocol.Version, Method: MethodCancelled, Params: encoded})
}

func (t *StdioTransport) readLoop() {
	for {
		message, err := t.decoder.Read()
		if err != nil {
			t.failPending(err)
			return
		}
		if message.Response == nil {
			continue
		}
		id := responseID(message.Response.ID)
		t.mu.Lock()
		ch := t.pending[id]
		if ch != nil {
			delete(t.pending, id)
		}
		t.mu.Unlock()
		if ch == nil {
			continue
		}
		if message.Response.Error != nil {
			ch <- response{err: &RPCError{Code: message.Response.Error.Code, Message: message.Response.Error.Message, Data: append(json.RawMessage(nil), message.Response.Error.Data...)}}
		} else {
			ch <- response{result: append(json.RawMessage(nil), message.Response.Result...)}
		}
	}
}

func marshalParams(params any) ([]byte, error) {
	if params == nil {
		return []byte(`{}`), nil
	}
	return json.Marshal(params)
}

func responseID(id any) string {
	switch v := id.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func (t *StdioTransport) failPending(err error) {
	t.closeOnce.Do(func() { close(t.done) })
	t.mu.Lock()
	for id, ch := range t.pending {
		delete(t.pending, id)
		ch <- response{err: err}
	}
	t.closed = true
	t.mu.Unlock()
	_ = t.closeProcess(false)
}

func (t *StdioTransport) closeProcess(kill bool) error {
	t.processCloseOnce.Do(func() {
		// Close first so real process implementations can perform a graceful
		// stdin-EOF shutdown before falling back to Kill.
		if err := t.process.Close(); err != nil {
			t.processCloseErr = err
		}
		if kill {
			if err := t.process.Kill(); t.processCloseErr == nil && err != nil {
				t.processCloseErr = err
			}
		}
	})
	return t.processCloseErr
}

func (t *StdioTransport) Close() error {
	t.closeOnce.Do(func() { close(t.done) })
	t.mu.Lock()
	alreadyClosed := t.closed
	if !alreadyClosed {
		t.closed = true
		for id, ch := range t.pending {
			delete(t.pending, id)
			ch <- response{err: ErrClosed}
		}
	}
	t.mu.Unlock()
	if alreadyClosed {
		// Wait for any in-flight closeProcess initiated by readLoop/failPending
		// so the process is guaranteed to be reaped before Close returns.
		_ = t.closeProcess(false)
		return nil
	}
	return t.closeProcess(true)
}

var _ Transport = (*StdioTransport)(nil)
