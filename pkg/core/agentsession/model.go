// Package agentsession provides a read-only, provider-neutral view of local agent sessions.
package agentsession

import (
	"context"
	"os"
	"path/filepath"
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
	ID             string
	Provider       string
	Title          string
	Status         Status
	LastActivityAt time.Time
	LastMessage    string
	PendingReview  bool
	Workspace      string
	TargetRef      string
	Source         string
	ObservedAt     time.Time
}

type Diagnostic struct {
	Provider string
	Code     string
	Message  string
}

// CodexThread is the provider-neutral row returned by a Codex state reader.
// The reader lives outside pkg/core because this package must remain pure stdlib.
type CodexThread struct {
	ID             string
	Title          string
	Workspace      string
	Status         string
	LastActivityAt time.Time
	LastMessage    string
	PendingReview  bool
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
