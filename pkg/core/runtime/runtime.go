// Package runtime provides cancellation coordination for in-flight operations.
package runtime

import (
	"context"
	"sort"
	"sync"
)

// Kind identifies a class of cancellable operation. It is intentionally string
// based so integrations can add kinds without changing this package.
type Kind string

const (
	AIStream         Kind = "ai_stream"
	AISuggestion     Kind = "ai_suggestion"
	AIConnectionTest Kind = "ai_connection_test"
	MCPRequest       Kind = "mcp_request"
	AgentWait        Kind = "agent_wait"
	WorkflowRun      Kind = "workflow_run"
	ClipboardPaste   Kind = "clipboard_paste"
)

// Operation is one operation included in a StopAll snapshot.
type Operation struct {
	Kind         Kind
	Label        string
	WasCancelled bool
}

// Report is a non-blocking snapshot returned by StopAll.
type Report struct {
	Reason     string
	Operations []Operation
}

// Registry coordinates cancellation without waiting for cancelled operations
// to observe their context. A registration is removed only by its matching
// done function, so an old operation cannot remove a newer one.
type Registry struct {
	mu     sync.Mutex
	parent context.Context
	next   uint64
	ops    map[uint64]registration
}

type registration struct {
	kind   Kind
	label  string
	cancel context.CancelFunc
	async  bool
}

// NewRegistry returns a registry whose Begin contexts derive from parent.
func NewRegistry(parent context.Context) *Registry {
	if parent == nil {
		parent = context.Background()
	}
	return &Registry{parent: parent, ops: make(map[uint64]registration)}
}

// Begin starts a context tracked by the registry. done must be called when the
// operation finishes; it is safe to call more than once.
func (r *Registry) Begin(kind Kind, label string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(r.parent)
	return ctx, r.add(kind, label, cancel, false)
}

// Register tracks cancellation owned by an adapter that already has a cancel
// function. unregister is safe to call more than once.
func (r *Registry) Register(kind Kind, cancel context.CancelFunc, label string) func() {
	if cancel == nil {
		return func() {}
	}
	return r.add(kind, label, cancel, true)
}

func (r *Registry) add(kind Kind, label string, cancel context.CancelFunc, async bool) func() {
	r.mu.Lock()
	r.next++
	id := r.next
	r.ops[id] = registration{kind: kind, label: label, cancel: cancel, async: async}
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.ops, id)
			r.mu.Unlock()
		})
	}
}

// StopAll invokes every cancellation function and returns immediately. The
// functions are called after releasing the registry lock and are not joined.
func (r *Registry) StopAll(reason string) Report {
	r.mu.Lock()
	ops := make([]registration, 0, len(r.ops))
	for id, op := range r.ops {
		ops = append(ops, op)
		delete(r.ops, id)
	}
	r.mu.Unlock()

	report := Report{Reason: reason, Operations: make([]Operation, 0, len(ops))}
	for _, op := range ops {
		// Begin's context cancellation is synchronous and makes the returned
		// context observable as canceled when StopAll returns. Adapter-owned
		// cancellation may be misbehaved, so Register calls it asynchronously.
		if op.async {
			go op.cancel()
		} else {
			op.cancel()
		}
		report.Operations = append(report.Operations, Operation{Kind: op.kind, Label: op.label, WasCancelled: true})
	}
	sort.Slice(report.Operations, func(i, j int) bool {
		if report.Operations[i].Kind != report.Operations[j].Kind {
			return report.Operations[i].Kind < report.Operations[j].Kind
		}
		return report.Operations[i].Label < report.Operations[j].Label
	})
	return report
}

// Len reports the number of currently registered operations.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ops)
}
