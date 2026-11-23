// Package automation contains the provider-independent model for declarative
// workflows. It deliberately does not execute actions or depend on any UI,
// MCP, or plugin package.
package automation

import "time"

// RiskLevel describes the amount of care an action requires before it is run.
type RiskLevel string

// ActionRisk is a descriptive alias for RiskLevel.
type ActionRisk = RiskLevel

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

// ProviderType identifies the family of provider which implements an action.
// The values are labels only: this package never imports a provider package.
type ProviderType string

// ActionProvider is a descriptive alias for ProviderType.
type ActionProvider = ProviderType

const (
	ProviderBuiltin ProviderType = "builtin"
	ProviderCLI     ProviderType = "cli"
	ProviderScript  ProviderType = "script"
	ProviderHTTP    ProviderType = "http"
	ProviderMCP     ProviderType = "mcp"
	ProviderPlugin  ProviderType = "plugin"
	ProviderCustom  ProviderType = "custom"
)

// Permission is a capability requested by an action.
type Permission string

// PermissionType is a descriptive alias for Permission.
type PermissionType = Permission

const (
	PermissionClipboardRead   Permission = "clipboard_read"
	PermissionClipboardWrite  Permission = "clipboard_write"
	PermissionFilesystemRead  Permission = "filesystem_read"
	PermissionFilesystemWrite Permission = "filesystem_write"
	PermissionNetwork         Permission = "network"
	PermissionShell           Permission = "shell"
	PermissionAccessibility   Permission = "accessibility"
	PermissionAutomation      Permission = "automation"
	PermissionScreenRecording Permission = "screen_recording"
	PermissionUserInput       Permission = "user_input"
)

// OutputMode controls what an action's result means to the workflow consumer.
type OutputMode string

const (
	OutputDiscard OutputMode = "discard"
	OutputCapture OutputMode = "capture"
	OutputReplace OutputMode = "replace"
	OutputNotify  OutputMode = "notify"
	// OutputSilent is a common spelling for discard mode.
	OutputSilent = OutputDiscard
)

// FailureMode controls whether a workflow continues after a failed step.
type FailureMode string

const (
	FailureStop     FailureMode = "stop"
	FailureContinue FailureMode = "continue"
	// FailureAbort is a common spelling for stop mode.
	FailureAbort = FailureStop
)

// ResultStatus is the terminal state of an invocation.
type ResultStatus string

const (
	ResultSucceeded     ResultStatus = "succeeded"
	ResultFailed        ResultStatus = "failed"
	ResultSkipped       ResultStatus = "skipped"
	ResultAwaitingHuman ResultStatus = "awaiting_human"
	ResultUnknown       ResultStatus = "unknown"
	ResultRolledBack    ResultStatus = "rolled_back"
)

// ExecutionStatus is the stable workflow-level state exposed to workbench and
// input surfaces. AwaitingHuman means the workflow is paused before a gated
// action and has not been submitted to its provider.
type ExecutionStatus string

const (
	ExecutionPending       ExecutionStatus = "pending"
	ExecutionRunning       ExecutionStatus = "running"
	ExecutionAwaitingHuman ExecutionStatus = "awaiting_human"
	ExecutionSucceeded     ExecutionStatus = "succeeded"
	ExecutionFailed        ExecutionStatus = "failed"
	ExecutionCanceled      ExecutionStatus = "canceled"
	ExecutionRolledBack    ExecutionStatus = "rolled_back"
	ExecutionUnknown       ExecutionStatus = "unknown"
)

type StepStatus string

const (
	StepPending       StepStatus = "pending"
	StepRunning       StepStatus = "running"
	StepAwaitingHuman StepStatus = "awaiting_human"
	StepSucceeded     StepStatus = "succeeded"
	StepFailed        StepStatus = "failed"
	StepSkipped       StepStatus = "skipped"
	StepRolledBack    StepStatus = "rolled_back"
	StepUnknown       StepStatus = "unknown"
)

type ApprovalStatus string

const (
	ApprovalNotRequired ApprovalStatus = "not_required"
	ApprovalPending     ApprovalStatus = "pending"
	ApprovalApproved    ApprovalStatus = "approved"
	ApprovalRejected    ApprovalStatus = "rejected"
	ApprovalUnknown     ApprovalStatus = "unknown"
)

type ApprovalState struct {
	Status      ApprovalStatus `json:"status"`
	StepID      string         `json:"stepId,omitempty"`
	Reason      string         `json:"reason,omitempty"`
	RequestedAt time.Time      `json:"requestedAt,omitzero"`
	DecidedAt   time.Time      `json:"decidedAt,omitzero"`
}

type RollbackStatus string

const (
	RollbackNotRequested RollbackStatus = "not_requested"
	RollbackRunning      RollbackStatus = "running"
	RollbackSucceeded    RollbackStatus = "succeeded"
	RollbackFailed       RollbackStatus = "failed"
	RollbackUnknown      RollbackStatus = "unknown"
)

type RollbackState struct {
	Status RollbackStatus `json:"status"`
	Steps  []StepResult   `json:"steps,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// ActionDescriptor is the declarative metadata needed to validate and authorize
// an action. InputSchema and OutputSchema contain JSON-compatible schema data;
// their shape is intentionally not prescribed by this package.
type ActionDescriptor struct {
	ID               string         `json:"id"`
	Name             string         `json:"name,omitempty"`
	Description      string         `json:"description,omitempty"`
	Provider         ProviderType   `json:"provider"`
	Risk             RiskLevel      `json:"risk"`
	Permissions      []Permission   `json:"permissions,omitempty"`
	InputSchema      map[string]any `json:"inputSchema,omitempty"`
	OutputSchema     map[string]any `json:"outputSchema,omitempty"`
	OutputMode       OutputMode     `json:"outputMode,omitempty"`
	RequiresApproval bool           `json:"requiresApproval,omitempty"`
	RollbackActionID string         `json:"rollbackActionId,omitempty"`
}

// ActionInvocation is a request to run one action. Input and Metadata are
// JSON-friendly values and do not contain executable callbacks.
type ActionInvocation struct {
	ActionID string         `json:"actionId"`
	Input    map[string]any `json:"input,omitempty"`
	Inputs   map[string]any `json:"inputs,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ActionResult is a provider-neutral result envelope. Error is a string so the
// value can be marshalled without depending on a provider's error type.
type ActionResult struct {
	ActionID      string         `json:"actionId"`
	Status        ResultStatus   `json:"status"`
	Output        any            `json:"output,omitempty"`
	Error         string         `json:"error,omitempty"`
	ErrorCategory string         `json:"errorCategory,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// WorkflowInput declares a named workflow input.
type WorkflowInput struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Default     any    `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

// WorkflowStep is a node in a workflow dependency graph. ActionID is the
// canonical action reference. Action is accepted as a convenient JSON/backward
// compatible alias; validation and planning reject conflicting values.
type WorkflowStep struct {
	ID          string         `json:"id"`
	ActionID    string         `json:"actionId,omitempty"`
	Action      string         `json:"action,omitempty"`
	Input       map[string]any `json:"input,omitempty"`
	Inputs      map[string]any `json:"inputs,omitempty"`
	DependsOn   []string       `json:"dependsOn,omitempty"`
	FailureMode FailureMode    `json:"failureMode"`
	OutputMode  OutputMode     `json:"outputMode"`
	// RequiredInputs is populated by plan builders from WorkflowDefinition.Inputs.
	// Keeping the declaration on each step preserves the historical slice-shaped
	// ExecutionPlan API while allowing the runner to enforce it independently of
	// any UI adapter.
	RequiredInputs   []string `json:"requiredInputs,omitempty"`
	RequiresApproval bool     `json:"requiresApproval,omitempty"`
	RollbackActionID string   `json:"rollbackActionId,omitempty"`
}

// WorkflowDefinition is a declarative, serializable workflow.
type WorkflowDefinition struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Goal        string `json:"goal,omitempty"`
	// FailureMode is the policy inherited by steps without an explicit policy.
	FailureMode FailureMode     `json:"failureMode,omitempty"`
	Inputs      []WorkflowInput `json:"inputs,omitempty"`
	Steps       []WorkflowStep  `json:"steps"`
}

// ActionRegistry is intentionally open so callers can supply their own
// registry without importing a package into automation. A registry should
// implement one of GetAction, LookupAction, Lookup, or FindAction, each with
// signature (string) (ActionDescriptor, bool). A map[string]ActionDescriptor
// is also accepted.
type ActionRegistry any

// MapActionRegistry adapts a descriptor map to ActionRegistry.
type MapActionRegistry map[string]ActionDescriptor

// GetAction implements the conventional registry shape.
func (r MapActionRegistry) GetAction(id string) (ActionDescriptor, bool) {
	d, ok := r[id]
	return d, ok
}

// ExecutionPlan is a deterministic serial order of workflow steps. The plan
// contains no executable functions or process handles.
type ExecutionPlan []WorkflowStep
