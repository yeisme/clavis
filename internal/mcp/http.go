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
	url    string
	client HTTPDoer
	next   atomic.Uint64
	closed atomic.Bool
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
	if ctx == nil {
		ctx = context.Background()
	}
	if t.closed.Load() {
		return nil, ErrClosed
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	id := fmt.Sprintf("http-%d", t.next.Add(1))
	wire := pluginprotocol.Request{JSONRPC: pluginprotocol.Version, Method: method, Params: paramsJSON, ID: id}
	body, err := pluginprotocol.Marshal(wire)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("mcp: HTTP client returned nil response")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp: HTTP status %d", resp.StatusCode)
	}
	message, err := pluginprotocol.ReadMessage(resp.Body, pluginprotocol.DefaultMaxMessageSize)
	if err != nil {
		return nil, err
	}
	if message.Response == nil {
		return nil, errors.New("mcp: HTTP response is not JSON-RPC response")
	}
	if got := responseID(message.Response.ID); got != id {
		return nil, fmt.Errorf("mcp: HTTP response id %q does not match request %q", got, id)
	}
	if message.Response.Error != nil {
		e := message.Response.Error
		return nil, &RPCError{Code: e.Code, Message: e.Message, Data: append(json.RawMessage(nil), e.Data...)}
	}
	return append(json.RawMessage(nil), message.Response.Result...), nil
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
	return nil
}

var _ Transport = (*LocalHTTPTransport)(nil)
