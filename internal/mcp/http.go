package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/yeisme/clavis/pkg/core/pluginprotocol"
)

// HTTPDoer is injected so callers can test the local HTTP protocol without
// opening sockets. Production callers may provide an *http.Client explicitly.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// HTTPTransportOptions controls the deliberately opt-in local HTTP transport.
type HTTPTransportOptions struct {
	URL    string
	OptIn  bool
	Client HTTPDoer
}

// LocalHTTPTransport is a JSON-RPC HTTP transport. It refuses non-local URLs
// and must be explicitly enabled with OptIn=true. It does not discover or
// contact remote endpoints.
type LocalHTTPTransport struct {
	url     string
	client  HTTPDoer
	next    atomic.Uint64
	closed  atomic.Bool
	pending sync.Map
}

// NewLocalHTTPTransport validates opt-in and local URL policy. No request is
// made by this constructor.
func NewLocalHTTPTransport(options HTTPTransportOptions) (*LocalHTTPTransport, error) {
	if !options.OptIn {
		return nil, ErrHTTPOptIn
	}
	u, err := url.Parse(options.URL)
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return nil, ErrHTTPNotLocal
	}
	if !isLocalHost(u.Hostname()) {
		return nil, ErrHTTPNotLocal
	}
	if options.Client == nil {
		return nil, errors.New("mcp: HTTP client is required")
	}
	return &LocalHTTPTransport{url: u.String(), client: options.Client}, nil
}

func isLocalHost(host string) bool {
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (t *LocalHTTPTransport) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	result, _, err := t.RequestWithID(ctx, method, params)
	return result, err
}

func (t *LocalHTTPTransport) RequestWithID(ctx context.Context, method string, params any) (json.RawMessage, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if t.closed.Load() {
		return nil, "", ErrClosed
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, "", err
	}
	id := fmt.Sprintf("http-%d", t.next.Add(1))
	wire := pluginprotocol.Request{JSONRPC: pluginprotocol.Version, Method: method, Params: paramsJSON, ID: id}
	body, err := pluginprotocol.Marshal(wire)
	if err != nil {
		return nil, "", err
	}
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	var once sync.Once
	t.pending.Store(id, cancel)
	defer t.pending.Delete(id)
	go func() {
		select {
		case <-ctx.Done():
			once.Do(func() { cancel(); _ = t.Cancel(context.Background(), id) })
		case <-done:
		}
	}()
	if err := ctx.Err(); err != nil {
		once.Do(func() { cancel(); _ = t.Cancel(context.Background(), id) })
		close(done)
		return nil, id, err
	}
	resp, err := t.client.Do(req)
	close(done)
	if err != nil {
		if ctx.Err() != nil {
			return nil, id, ctx.Err()
		}
		return nil, id, err
	}
	if resp == nil {
		return nil, id, errors.New("mcp: HTTP client returned nil response")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, id, fmt.Errorf("mcp: HTTP status %d", resp.StatusCode)
	}
	message, err := pluginprotocol.ReadMessage(resp.Body, pluginprotocol.DefaultMaxMessageSize)
	if err != nil {
		return nil, id, err
	}
	if message.Response == nil {
		return nil, id, errors.New("mcp: HTTP response is not JSON-RPC response")
	}
	if got := responseID(message.Response.ID); got != id {
		return nil, id, fmt.Errorf("mcp: HTTP response id %q does not match request %q", got, id)
	}
	if message.Response.Error != nil {
		e := message.Response.Error
		return nil, id, &RPCError{Code: e.Code, Message: e.Message, Data: append(json.RawMessage(nil), e.Data...)}
	}
	return append(json.RawMessage(nil), message.Response.Result...), id, nil
}

func (t *LocalHTTPTransport) Notify(ctx context.Context, method string, params any) error {
	// MCP notifications are represented as a request without an ID. Servers
	// commonly return 202 or an empty body; this method intentionally keeps the
	// abstraction minimal and does not require a response payload.
	if ctx == nil {
		ctx = context.Background()
	}
	if t.closed.Load() {
		return ErrClosed
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return err
	}
	body, err := pluginprotocol.Marshal(pluginprotocol.Notification{JSONRPC: pluginprotocol.Version, Method: method, Params: paramsJSON})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	if resp == nil {
		return errors.New("mcp: HTTP client returned nil response")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mcp: HTTP status %d", resp.StatusCode)
	}
	return nil
}

func (t *LocalHTTPTransport) Cancel(ctx context.Context, requestID string) error {
	return t.Notify(ctx, MethodCancelled, map[string]any{"requestId": requestID, "reason": "cancelled"})
}

func (t *LocalHTTPTransport) Close() error {
	t.closed.Store(true)
	t.pending.Range(func(_, value any) bool { value.(context.CancelFunc)(); return true })
	return nil
}

var _ Transport = (*LocalHTTPTransport)(nil)
