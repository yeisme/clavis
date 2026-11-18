package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// NewClient constructs a client over an already-created transport. This is
// useful for tests and for transports other than stdio.
func NewClient(transport Transport, options ClientOptions) Client {
	// Legacy constructor preserves historical unrestricted behavior.
	return NewConfiguredClient(transport, ServerConfig{AllowedTools: []string{"*"}}, options)
}

// NewConfiguredClient applies request timeout and tool permissions from config.
func NewConfiguredClient(transport Transport, config ServerConfig, options ClientOptions) Client {
	o := options.normalized()
	return &client{transport: transport, options: o, timeout: config.Timeout, allowedTools: cloneStrings(config.AllowedTools), serverName: config.Name}
}

// NewStdioClient launches the configured server through the supplied launcher.
// A nil launcher is rejected; no executable is started implicitly.
func NewStdioClient(ctx context.Context, config ServerConfig, launcher ProcessLauncher, options ClientOptions) (Client, error) {
	transport, err := NewStdioTransport(ctx, config, launcher)
	if err != nil {
		return nil, err
	}
	return NewConfiguredClient(transport, config, options), nil
}

type client struct {
	transport    Transport
	options      ClientOptions
	mu           sync.RWMutex
	manifest     ServerManifest
	ready        bool
	closed       bool
	timeout      time.Duration
	allowedTools []string
	serverName   string
}

func (c *client) Initialize(ctx context.Context) (ServerManifest, error) {
	if c == nil || c.transport == nil {
		return ServerManifest{}, ErrClosed
	}
	c.mu.RLock()
	closed, ready := c.closed, c.ready
	current := c.manifest
	c.mu.RUnlock()
	if closed {
		return ServerManifest{}, ErrClosed
	}
	if ready {
		return cloneManifest(current), nil
	}
	params := map[string]any{
		"protocolVersion": c.options.ProtocolVersion,
		"capabilities":    cloneMap(c.options.Capabilities),
		"clientInfo":      map[string]any{"name": c.options.ClientName, "version": c.options.ClientVersion},
	}
	result, _, err := c.request(ctx, "initialize", params)
	if err != nil {
		return ServerManifest{}, err
	}
	var wire struct {
		ProtocolVersion string             `json:"protocolVersion"`
		Capabilities    ServerCapabilities `json:"capabilities"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Instructions string         `json:"instructions"`
		Metadata     map[string]any `json:"_meta"`
	}
	if err := json.Unmarshal(result, &wire); err != nil {
		return ServerManifest{}, fmt.Errorf("mcp: decode initialize result: %w", err)
	}
	manifest := ServerManifest{Name: wire.ServerInfo.Name, Version: wire.ServerInfo.Version, ProtocolVersion: wire.ProtocolVersion, Capabilities: wire.Capabilities, Instructions: wire.Instructions, Metadata: cloneMap(wire.Metadata)}
	// MCP requires initialized before capability requests. Do not expose a
	// ready client if the required notification cannot be sent.
	if err := c.transport.Notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		return ServerManifest{}, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ServerManifest{}, ErrClosed
	}
	c.manifest, c.ready = manifest, true
	c.mu.Unlock()
	return cloneManifest(manifest), nil
}

func (c *client) ListTools(ctx context.Context) ([]ToolDescriptor, error) {
	var result struct {
		Tools []ToolDescriptor `json:"tools"`
	}
	if err := c.callReady(ctx, "tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	return cloneTools(result.Tools), nil
}

func (c *client) ListResources(ctx context.Context) ([]ResourceDescriptor, error) {
	var result struct {
		Resources []ResourceDescriptor `json:"resources"`
	}
	if err := c.callReady(ctx, "resources/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	return cloneResources(result.Resources), nil
}

func (c *client) ListPrompts(ctx context.Context) ([]PromptDescriptor, error) {
	var result struct {
		Prompts []PromptDescriptor `json:"prompts"`
	}
	if err := c.callReady(ctx, "prompts/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	return clonePrompts(result.Prompts), nil
}

func (c *client) Invoke(ctx context.Context, name string, arguments map[string]any) (ToolResult, error) {
	result, _, err := c.InvokeWithID(ctx, name, arguments)
	return result, err
}

func (c *client) InvokeWithID(ctx context.Context, name string, arguments map[string]any) (ToolResult, string, error) {
	if name == "" {
		return ToolResult{}, "", errors.New("mcp: tool name is required")
	}
	if !c.toolAllowed(name) {
		return ToolResult{}, "", fmt.Errorf("%w: server %q tool %q", ErrToolNotAllowed, c.serverName, name)
	}
	params := map[string]any{"name": name, "arguments": cloneMap(arguments)}
	result, id, err := c.callReadyRawWithID(ctx, "tools/call", params)
	if err != nil {
		return ToolResult{}, id, err
	}
	var output ToolResult
	if err := json.Unmarshal(result, &output); err != nil {
		return ToolResult{}, id, fmt.Errorf("mcp: decode tools/call result: %w", err)
	}
	output.Content = cloneContent(output.Content)
	output.StructuredContent = cloneJSONValue(output.StructuredContent)
	output.Metadata = cloneMap(output.Metadata)
	return output, id, nil
}

func (c *client) toolAllowed(name string) bool {
	for _, allowed := range c.allowedTools {
		if allowed == "*" || allowed == name {
			return true
		}
	}
	return false
}

func (c *client) Cancel(ctx context.Context, requestID string) error {
	if requestID == "" {
		return errors.New("mcp: request id is required")
	}
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	return c.transport.Cancel(ctx, requestID)
}

func (c *client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	return c.transport.Close()
}

func (c *client) callReady(ctx context.Context, method string, params any, output any) error {
	result, err := c.callReadyRaw(ctx, method, params)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(result, output); err != nil {
		return fmt.Errorf("mcp: decode %s result: %w", method, err)
	}
	return nil
}

func (c *client) callReadyRaw(ctx context.Context, method string, params any) (json.RawMessage, error) {
	result, _, err := c.callReadyRawWithID(ctx, method, params)
	return result, err
}

func (c *client) callReadyRawWithID(ctx context.Context, method string, params any) (json.RawMessage, string, error) {
	c.mu.RLock()
	ready, closed := c.ready, c.closed
	c.mu.RUnlock()
	if closed {
		return nil, "", ErrClosed
	}
	if !ready {
		return nil, "", ErrNotInitialized
	}
	return c.request(ctx, method, params)
}

func (c *client) request(ctx context.Context, method string, params any) (json.RawMessage, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.timeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, c.timeout)
			defer cancel()
		}
	}
	if r, ok := c.transport.(interface {
		RequestWithID(context.Context, string, any) (json.RawMessage, string, error)
	}); ok {
		result, id, err := r.RequestWithID(ctx, method, params)
		return result, id, err
	}
	result, err := c.transport.Request(ctx, method, params)
	return result, "", err
}

func cloneManifest(in ServerManifest) ServerManifest {
	out := in
	out.Metadata = cloneMap(in.Metadata)
	out.Capabilities.Experimental = cloneMap(in.Capabilities.Experimental)
	out.Capabilities.Tools = cloneCapability(in.Capabilities.Tools)
	out.Capabilities.Resources = cloneCapability(in.Capabilities.Resources)
	out.Capabilities.Prompts = cloneCapability(in.Capabilities.Prompts)
	out.Capabilities.Logging = cloneCapability(in.Capabilities.Logging)
	return out
}

func cloneCapability(in *CapabilityDescriptor) *CapabilityDescriptor {
	if in == nil {
		return nil
	}
	out := *in
	out.Metadata = cloneMap(in.Metadata)
	return &out
}
func cloneTools(in []ToolDescriptor) []ToolDescriptor {
	out := append([]ToolDescriptor(nil), in...)
	for i := range out {
		out[i].InputSchema = cloneMap(out[i].InputSchema)
		out[i].OutputSchema = cloneMap(out[i].OutputSchema)
		out[i].Annotations = cloneMap(out[i].Annotations)
		out[i].Metadata = cloneMap(out[i].Metadata)
	}
	return out
}
func cloneResources(in []ResourceDescriptor) []ResourceDescriptor {
	out := append([]ResourceDescriptor(nil), in...)
	for i := range out {
		out[i].Metadata = cloneMap(out[i].Metadata)
	}
	return out
}
func clonePrompts(in []PromptDescriptor) []PromptDescriptor {
	out := append([]PromptDescriptor(nil), in...)
	for i := range out {
		out[i].Arguments = append([]PromptArgument(nil), out[i].Arguments...)
		out[i].Metadata = cloneMap(out[i].Metadata)
	}
	return out
}

func cloneContent(in []ContentItem) []ContentItem {
	if in == nil {
		return nil
	}
	out := make([]ContentItem, len(in))
	for i, item := range in {
		out[i] = cloneMap(item)
	}
	return out
}

var _ Client = (*client)(nil)
