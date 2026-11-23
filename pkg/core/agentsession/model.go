// Package agentsession provides a read-only, provider-neutral view of local agent sessions.
package agentsession

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Status string

const (
	StatusRunning Status = "running"
	StatusWaiting Status = "waiting"
	StatusStopped Status = "stopped"
	StatusUnknown Status = "unknown"
)

type Session struct {
	ID             string    `json:"id"`
	Provider       string    `json:"provider"`
	Title          string    `json:"title"`
	Status         Status    `json:"status"`
	LastActivityAt time.Time `json:"lastActivityAt"`
	LastMessage    string    `json:"lastMessage,omitempty"`
	PendingReview  bool      `json:"pendingReview"`
	CurrentAction  string    `json:"currentAction,omitempty"`
	ResultLine     string    `json:"resultLine,omitempty"`
	AwaitingHuman  bool      `json:"awaitingHuman"`
	Workspace      string    `json:"workspace,omitempty"`
	TargetRef      string    `json:"targetRef,omitempty"`
	Source         string    `json:"source,omitempty"`
	ObservedAt     time.Time `json:"observedAt"`
}

type Diagnostic struct {
	Provider string `json:"provider"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// CodexThread is the provider-neutral row returned by a Codex state reader.
// The reader lives outside pkg/core because this package must remain pure stdlib.
type CodexThread struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Workspace      string    `json:"workspace,omitempty"`
	Status         string    `json:"status,omitempty"`
	LastActivityAt time.Time `json:"lastActivityAt"`
	LastMessage    string    `json:"lastMessage,omitempty"`
	PendingReview  bool      `json:"pendingReview"`
	CurrentAction  string    `json:"currentAction,omitempty"`
	ResultLine     string    `json:"resultLine,omitempty"`
	AwaitingHuman  bool      `json:"awaitingHuman"`
}

type compatibilityFields struct {
	currentAction string
	resultLine    string
	awaitingHuman bool
	pendingReview bool
}

func compatibilityFromMap(fields map[string]any) compatibilityFields {
	result := compatibilityFields{
		currentAction: firstString(fields, "currentAction", "current_action", "activeAction", "active_action"),
		resultLine:    firstString(fields, "resultLine", "result_line", "lastResult", "result", "lastMessage", "summary"),
	}
	if outline := mapValue(fields, "turnOutline", "val"); result.resultLine == "" && outline != nil {
		if turns, ok := outline["turns"].([]any); ok && len(turns) > 0 {
			if last, ok := turns[len(turns)-1].(map[string]any); ok {
				result.currentAction = firstNonEmpty(result.currentAction, firstString(last, "currentAction", "current_action", "activeAction", "active_action"))
				result.resultLine = firstString(last, "response", "result", "resultLine", "result_line")
			}
		}
	}
	marker := pendingMarker(fields)
	if value, ok := firstBool(fields, "awaitingHuman", "awaiting_human"); ok {
		result.awaitingHuman = value || marker
	} else {
		result.awaitingHuman = marker
	}
	if value, ok := firstBool(fields, "pendingReview", "pending_review"); ok {
		result.pendingReview = value || marker
	} else {
		result.pendingReview = marker
	}
	return result
}

func firstBool(fields map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, ok := fields[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed, true
		case string:
			parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
			if err == nil {
				return parsed, true
			}
		}
	}
	return false, false
}

func pendingMarker(fields map[string]any) bool {
	for _, key := range []string{"pending_review", "pendingReview", "action_request", "actionRequest", "needs_confirmation", "needsConfirmation"} {
		if value, ok := firstBool(fields, key); ok && value {
			return true
		}
	}
	for _, value := range fields {
		switch nested := value.(type) {
		case map[string]any:
			if pendingMarker(nested) {
				return true
			}
		case []any:
			for _, item := range nested {
				if child, ok := item.(map[string]any); ok && pendingMarker(child) {
					return true
				}
			}
		}
	}
	return false
}

// CodexThreadsReader returns raw thread rows from the Codex state database.
// Implementations must be read-only and are injected by the platform layer.
type CodexThreadsReader interface {
	Threads(ctx context.Context, limit int) ([]CodexThread, error)
}

type Options struct {
	DSHDir          string
	DSHThreadsDir   string
	CodexDB         string
	CodexReader     CodexThreadsReader
	ClaudeDir       string
	ProviderTimeout time.Duration
	Now             func() time.Time
}

func (o Options) withDefaults() Options {
	home, _ := osUserHomeDir()
	if o.DSHDir == "" {
		o.DSHDir = joinPath(home, ".dsh", "storages", "session_projcache", "sessions")
	}
	if o.DSHThreadsDir == "" {
		o.DSHThreadsDir = joinPath(home, ".dsh", "threads", "pi")
	}
	if o.CodexDB == "" {
		o.CodexDB = joinPath(home, ".codex", "state_5.sqlite")
	}
	if o.ClaudeDir == "" {
		o.ClaudeDir = joinPath(home, ".claude", "projects")
	}
	if o.ProviderTimeout <= 0 || o.ProviderTimeout > 500*time.Millisecond {
		o.ProviderTimeout = 500 * time.Millisecond
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Discover returns sessions from all three local providers. Each provider has
// its own deadline and failure is represented as a diagnostic, never as a
// failure of the aggregate result.
func Discover(ctx context.Context, opts Options) ([]Session, []Diagnostic) {
	if ctx == nil {
		ctx = context.Background()
	}
	opts = opts.withDefaults()
	type result struct {
		sessions    []Session
		diagnostics []Diagnostic
	}
	jobs := []struct {
		name string
		fn   func(context.Context) result
	}{
		{"dsh", func(c context.Context) result { s, d := discoverDSH(c, opts); return result{s, d} }},
		{"codex", func(c context.Context) result { s, d := discoverCodex(c, opts); return result{s, d} }},
		{"claude", func(c context.Context) result { s, d := discoverClaude(c, opts); return result{s, d} }},
	}
	ch := make(chan struct {
		name string
		result
	}, len(jobs))
	for _, job := range jobs {
		go func(job struct {
			name string
			fn   func(context.Context) result
		}) {
			deadline, cancel := context.WithTimeout(ctx, opts.ProviderTimeout)
			defer cancel()
			ch <- struct {
				name string
				result
			}{job.name, job.fn(deadline)}
		}(job)
	}
	var all []Session
	var diagnostics []Diagnostic
	for range jobs {
		select {
		case item := <-ch:
			all = append(all, item.result.sessions...)
			diagnostics = append(diagnostics, item.result.diagnostics...)
		case <-ctx.Done():
			return sortSessions(all), append(diagnostics, Diagnostic{Provider: "aggregate", Code: "cancelled", Message: ctx.Err().Error()})
		}
	}
	return sortSessions(all), diagnostics
}

// Small wrappers keep path construction testable without exposing filesystem policy.
var osUserHomeDir = func() (string, error) { return os.UserHomeDir() }
var joinPath = filepath.Join
