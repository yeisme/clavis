package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/automation"
)

var (
	ErrCapabilityDenied = errors.New("plugin capability denied")
	ErrPermissionDenied = errors.New("plugin permission denied")
)

// CandidateRequest is intentionally a summary-only input. It has no hook,
// key-injection, or full clipboard fields.
type CandidateRequest struct {
	TextSummary string `json:"text_summary,omitempty"`
	Language    string `json:"language,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type Candidate struct {
	Text  string  `json:"text"`
	Score float64 `json:"score,omitempty"`
}

// LookupCandidates runs outside the keyboard hook and returns an empty result
// on timeout. Each call has an independent 100ms budget by default.
func (s *Supervisor) LookupCandidates(ctx context.Context, id string, request CandidateRequest) []Candidate {
	if ctx == nil {
		ctx = context.Background()
	}
	budget := s.lookupTimeout
	if budget <= 0 {
		budget = 100 * time.Millisecond
	}
	callCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	result := make(chan []Candidate, 1)
	go func() {
		params, _ := json.Marshal(request)
		payload, err := s.Invoke(callCtx, id, "candidates.lookup", params)
		if err != nil {
			result <- nil
			return
		}
		var out []Candidate
		if json.Unmarshal(payload, &out) != nil {
			result <- nil
			return
		}
		result <- out
	}()
	select {
	case out := <-result:
		return out
	case <-callCtx.Done():
		return []Candidate{}
	}
}

// RenderStyle invokes style.render and filters its object result to the fields
// declared by the plugin manifest. Undeclared fields never cross the host API.
func (s *Supervisor) RenderStyle(ctx context.Context, id string, params json.RawMessage) (map[string]any, error) {
	payload, err := s.Invoke(ctx, id, "style.render", params)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	s.mu.RLock()
	r := s.plugins[id]
	s.mu.RUnlock()
	if r == nil {
		return nil, ErrPluginNotFound
	}
	r.mu.Lock()
	allowed := append([]string(nil), r.manifest.StyleFields...)
	r.mu.Unlock()
	set := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for key := range raw {
		if _, ok := set[key]; !ok {
			delete(raw, key)
		}
	}
	return raw, nil
}

// ActionGate decides whether a declared plugin permission may be used.
type ActionGate interface {
	Allow(ctx context.Context, pluginID string, permissions []string) error
}

// AllowAllGate permits all plugin action executions. Callers must inject it
// explicitly (for example in trusted tests); nil gates never default to it.
type AllowAllGate struct{}

func (AllowAllGate) Allow(context.Context, string, []string) error { return nil }

// DenyAllGate denies all plugin action executions. It is the fail-closed
// default used whenever a caller omits an explicit ActionGate.
type DenyAllGate struct{}

func (DenyAllGate) Allow(context.Context, string, []string) error {
	return fmt.Errorf("%w: no action gate configured", ErrPermissionDenied)
}

func resolveActionGate(gate ActionGate) ActionGate {
	if gate == nil {
		return DenyAllGate{}
	}
	return gate
}

func wrapPermissionDenied(err error) error {
	if errors.Is(err, ErrPermissionDenied) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrPermissionDenied, err)
}

// AutomationActionRunner adapts action.run to the existing automation.Runner.
type AutomationActionRunner struct {
	Supervisor *Supervisor
	PluginID   string
	Gate       ActionGate
}

type actionProvider struct {
	supervisor *Supervisor
	pluginID   string
	gate       ActionGate
}

func (p actionProvider) Invoke(ctx context.Context, invocation automation.ActionInvocation) (automation.ActionResult, error) {
	gate := resolveActionGate(p.gate)
	r, err := p.supervisor.plugin(p.pluginID)
	if err != nil {
		return automation.ActionResult{}, err
	}
	r.mu.Lock()
	permissions := append([]string(nil), r.manifest.Permissions.Filesystem...)
	permissions = append(permissions, r.manifest.Permissions.Network...)
	r.mu.Unlock()
	if err := gate.Allow(ctx, p.pluginID, permissions); err != nil {
		denyErr := wrapPermissionDenied(err)
		return automation.ActionResult{ActionID: invocation.ActionID, Status: automation.ResultFailed, Error: denyErr.Error()}, denyErr
	}
	params, err := json.Marshal(invocation.Input)
	if err != nil {
		return automation.ActionResult{}, err
	}
	payload, err := p.supervisor.Invoke(ctx, p.pluginID, "action.run", params)
	if err != nil {
		return automation.ActionResult{ActionID: invocation.ActionID, Status: automation.ResultFailed, Error: err.Error()}, nil
	}
	var result automation.ActionResult
	if err := json.Unmarshal(payload, &result); err != nil {
		return automation.ActionResult{ActionID: invocation.ActionID, Status: automation.ResultSucceeded, Output: json.RawMessage(payload)}, nil
	}
	if result.ActionID == "" {
		result.ActionID = invocation.ActionID
	}
	return result, nil
}

func (s *Supervisor) RunAction(ctx context.Context, id string, runner *automation.Runner, gate ActionGate, plan automation.ExecutionPlan, inputs map[string]any) (automation.ExecutionResult, error) {
	if runner == nil {
		return automation.ExecutionResult{}, errors.New("plugin action runner is nil")
	}
	gate = resolveActionGate(gate)
	r, err := s.plugin(id)
	if err != nil {
		return automation.ExecutionResult{}, err
	}
	r.mu.Lock()
	permissions := append([]string(nil), r.manifest.Permissions.Filesystem...)
	permissions = append(permissions, r.manifest.Permissions.Network...)
	r.mu.Unlock()
	if err := gate.Allow(ctx, id, permissions); err != nil {
		return automation.ExecutionResult{}, wrapPermissionDenied(err)
	}
	clone := *runner
	clone.Provider = actionProvider{supervisor: s, pluginID: id, gate: gate}
	return clone.Run(ctx, plan, inputs)
}

// NewAutomationActionRunner returns an automation runner backed by action.run.
// A nil gate defaults to fail-closed DenyAllGate.
func (s *Supervisor) NewAutomationActionRunner(id string, gate ActionGate) (*automation.Runner, error) {
	if _, err := s.plugin(id); err != nil {
		return nil, err
	}
	gate = resolveActionGate(gate)
	return automation.NewRunner(actionProvider{supervisor: s, pluginID: id, gate: gate}), nil
}

// CapabilityNames returns stable declared capability names.
func (s *Supervisor) CapabilityNames(id string) ([]string, error) {
	r, err := s.plugin(id)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]string(nil), r.manifest.Capabilities...)
	sort.Strings(out)
	return out, nil
}

func capabilityAllowed(manifest Manifest, capability string) bool {
	for _, name := range manifest.Capabilities {
		if strings.TrimSpace(name) == capability {
			return true
		}
	}
	return false
}
