// Package mcp contains the provider-neutral foundation for communicating with
// Model Context Protocol (MCP) servers. It deliberately has no UI, workflow,
// credential, or remote-network integration.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// DefaultProtocolVersion is the MCP protocol version sent by new clients.
	DefaultProtocolVersion = "2024-11-05"
	// MethodCancelled is the MCP cancellation notification method.
	MethodCancelled = "notifications/cancelled"
)

// ServerConfig describes an MCP server without containing executable code.
// Stdio servers are started only when a caller supplies a ProcessLauncher.
type ServerConfig struct {
	Name         string            `json:"name"`
	Version      string            `json:"version,omitempty"`
	Command      string            `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	Environment  map[string]string `json:"environment,omitempty"`
	WorkingDir   string            `json:"workingDir,omitempty"`
	URL          string            `json:"url,omitempty"`
	OptIn        bool              `json:"optIn,omitempty"`
	Timeout      time.Duration     `json:"timeout,omitempty"`
	AllowedTools []string          `json:"allowedTools,omitempty"`
}

// ServerSection is the neutral declarative shape used by config adapters.
// It intentionally avoids making config packages depend on this internal package.
type ServerSection struct {
	Name, Command, WorkingDir, URL  string
	Args, Environment, AllowedTools []string
	OptIn                           bool
	TimeoutSeconds, MaxConcurrency  int
}

// ConfigFromSection converts a neutral configuration section to ServerConfig.
func ConfigFromSection(s ServerSection) (ServerConfig, error) {
	if s.Name == "" {
		return ServerConfig{}, errors.New("mcp: server name is required")
	}
	env := make(map[string]string, len(s.Environment))
	for _, entry := range s.Environment {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return ServerConfig{}, fmt.Errorf("mcp: invalid environment entry %q", entry)
		}
		env[parts[0]] = parts[1]
	}
	return ServerConfig{Name: s.Name, Command: s.Command, Args: cloneStrings(s.Args), Environment: env, WorkingDir: s.WorkingDir, URL: s.URL, OptIn: s.OptIn, Timeout: time.Duration(s.TimeoutSeconds) * time.Second, AllowedTools: cloneStrings(s.AllowedTools)}, nil
}

// ProcessSpec is the executable portion of ServerConfig passed to an injected
// process launcher. Environment is a copy and may be safely modified by a
// launcher.
type ProcessSpec struct {
	Command     string
	Args        []string
	Environment map[string]string
	WorkingDir  string
}

// ServerManifest is read-only metadata returned by initialize. Maps and
// slices returned by Client methods are fresh copies; callers may not use them
// to mutate client state.
type ServerManifest struct {
	Name            string             `json:"name"`
	Version         string             `json:"version,omitempty"`
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities,omitempty"`
	Instructions    string             `json:"instructions,omitempty"`
	Metadata        map[string]any     `json:"metadata,omitempty"`
}

// ServerCapabilities describes capability families advertised by a server.
type ServerCapabilities struct {
	Tools        *CapabilityDescriptor `json:"tools,omitempty"`
	Resources    *CapabilityDescriptor `json:"resources,omitempty"`
	Prompts      *CapabilityDescriptor `json:"prompts,omitempty"`
	Logging      *CapabilityDescriptor `json:"logging,omitempty"`
	Experimental map[string]any        `json:"experimental,omitempty"`
}

// CapabilityDescriptor is intentionally permissive: MCP servers can advertise
// capability-specific fields (for example listChanged) as well as extensions.
type CapabilityDescriptor struct {
	ListChanged bool           `json:"listChanged,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// ToolDescriptor is read-only metadata for one MCP tool.
type ToolDescriptor struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema,omitempty"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
	Metadata     map[string]any `json:"_meta,omitempty"`
}

// ResourceDescriptor is read-only metadata for one MCP resource.
type ResourceDescriptor struct {
	URI         string         `json:"uri"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	MIMEType    string         `json:"mimeType,omitempty"`
	Size        int64          `json:"size,omitempty"`
	Metadata    map[string]any `json:"_meta,omitempty"`
}

// PromptDescriptor is read-only metadata for one MCP prompt.
type PromptDescriptor struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
	Metadata    map[string]any   `json:"_meta,omitempty"`
}

// PromptArgument describes one optional or required prompt argument.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// ToolResult is the provider-neutral result of tools/call.
type ToolResult struct {
	Content           []ContentItem  `json:"content,omitempty"`
	StructuredContent any            `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
	Metadata          map[string]any `json:"_meta,omitempty"`
}

// ContentItem is an MCP content block. Data is retained as decoded JSON so
// servers may use text, image, audio, or extension content types.
type ContentItem map[string]any

// ClientOptions controls initialize. Capabilities is client-advertised
// metadata and is copied when the client is created.
type ClientOptions struct {
	ProtocolVersion string
	ClientName      string
	ClientVersion   string
	Capabilities    map[string]any
}

// Client is independent of any provider or transport implementation.
type Client interface {
	Initialize(context.Context) (ServerManifest, error)
	ListTools(context.Context) ([]ToolDescriptor, error)
	ListResources(context.Context) ([]ResourceDescriptor, error)
	ListPrompts(context.Context) ([]PromptDescriptor, error)
	Invoke(context.Context, string, map[string]any) (ToolResult, error)
	InvokeWithID(context.Context, string, map[string]any) (ToolResult, string, error)
	Cancel(context.Context, string) error
	Close() error
}

// Transport is the small JSON-RPC contract consumed by Client. Implementations
// must preserve JSON-RPC errors as *RPCError and must honor request contexts.
type Transport interface {
	Request(context.Context, string, any) (json.RawMessage, error)
	Notify(context.Context, string, any) error
	Cancel(context.Context, string) error
	Close() error
}

// RPCError is an error response returned by an MCP server.
type RPCError struct {
	Code    int
	Message string
	Data    json.RawMessage
}

func (e *RPCError) Error() string {
	if e == nil {
		return "mcp: nil JSON-RPC error"
	}
	if len(e.Data) == 0 {
		return fmt.Sprintf("mcp: JSON-RPC error %d: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("mcp: JSON-RPC error %d: %s (%s)", e.Code, e.Message, e.Data)
}

var (
	ErrNotInitialized = errors.New("mcp: client is not initialized")
	ErrClosed         = errors.New("mcp: transport is closed")
	ErrHTTPOptIn      = errors.New("mcp: HTTP transport requires explicit opt-in")
	ErrHTTPNotLocal   = errors.New("mcp: HTTP transport URL must be local")
	ErrToolNotAllowed = errors.New("mcp: tool is not allowed")
)

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneJSONValue(v)
	}
	return out
}

func cloneJSONValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return cloneMap(x)
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = cloneJSONValue(x[i])
		}
		return out
	default:
		return x
	}
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

func (c ServerConfig) processSpec() (ProcessSpec, error) {
	if c.Command == "" {
		return ProcessSpec{}, errors.New("mcp: server command is required")
	}
	env := make(map[string]string, len(c.Environment))
	for k, v := range c.Environment {
		env[k] = v
	}
	return ProcessSpec{Command: c.Command, Args: cloneStrings(c.Args), Environment: env, WorkingDir: c.WorkingDir}, nil
}

func (o ClientOptions) normalized() ClientOptions {
	if o.ProtocolVersion == "" {
		o.ProtocolVersion = DefaultProtocolVersion
	}
	if o.ClientName == "" {
		o.ClientName = "clavis"
	}
	if o.ClientVersion == "" {
		o.ClientVersion = "dev"
	}
	o.Capabilities = cloneMap(o.Capabilities)
	return o
}
