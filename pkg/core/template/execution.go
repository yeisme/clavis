package template

import (
	"context"
	"errors"
	"strings"
)

// ExecuteRequiresReview is the only contract permission that authorizes a
// template to be sent to an AI executor. The permission is intentionally
// explicit: a missing permission is not treated as an allow-list omission.
const ExecuteRequiresReview = "execute_requires_review"

const (
	ExecutionPermissionDenied Code = "execution_permission_denied"
	ExecutionReviewRequired   Code = "execution_review_required"
	ExecutionRenderFailed     Code = "execution_render_failed"
	ExecutionExecutorMissing  Code = "execution_executor_missing"
	ExecutionFailed           Code = "execution_failed"
)

// ExecutionError is a body-free, structured failure returned by the execution
// gate. It is safe to show or persist because it never includes rendered text.
type ExecutionError struct {
	Code       Code   `json:"code"`
	Permission string `json:"permission,omitempty"`
	Message    string `json:"message"`
	Cause      error  `json:"-"`
}

func (e *ExecutionError) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Code) + ": " + e.Message
}

func (e *ExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// AIExecutor is the narrow boundary used by the permission gate. Production
// callers can adapt a streaming client to this interface; tests can provide a
// fake and assert that Invoke is never reached when authorization fails.
type AIExecutor interface {
	Execute(context.Context, string) error
}

// AIExecutorFunc adapts a function to AIExecutor.
type AIExecutorFunc func(context.Context, string) error

func (f AIExecutorFunc) Execute(ctx context.Context, prompt string) error {
	return f(ctx, prompt)
}

// HasPermission reports whether the contract explicitly declares permission.
func (c Contract) HasPermission(permission string) bool {
	permission = strings.TrimSpace(permission)
	if permission == "" {
		return false
	}
	for _, declared := range c.Permissions {
		if declared == permission {
			return true
		}
	}
	return false
}

// RequiresReview reports whether this contract has the explicit AI execution
// permission. No permission, including a generic "execute", is sufficient.
func (c Contract) RequiresReview() bool {
	return c.HasPermission(ExecuteRequiresReview)
}

// AuthorizeAI performs all checks that must happen before an AI provider is
// touched. It fails closed when the contract does not explicitly opt in or
// when the one-shot human review confirmation is absent.
func (c Contract) AuthorizeAI(rendered string, reviewConfirmed bool) error {
	if strings.TrimSpace(rendered) == "" {
		return &ExecutionError{Code: ExecutionRenderFailed, Message: "rendered prompt is unavailable"}
	}
	if !c.RequiresReview() {
		return &ExecutionError{
			Code:       ExecutionPermissionDenied,
			Permission: ExecuteRequiresReview,
			Message:    "template is not authorized for AI execution",
		}
	}
	if !reviewConfirmed {
		return &ExecutionError{
			Code:       ExecutionReviewRequired,
			Permission: ExecuteRequiresReview,
			Message:    "human review confirmation is required before AI execution",
		}
	}
	return nil
}

// ExecuteAI applies the fail-closed render and permission checks before
// invoking executor. The executor is called exactly once only after every
// check succeeds. reviewConfirmed is deliberately supplied per call and is
// not stored on Contract, so confirmation cannot be reused accidentally.
func (c Contract) ExecuteAI(ctx context.Context, rendered string, reviewConfirmed bool, executor AIExecutor) error {
	if err := c.AuthorizeAI(rendered, reviewConfirmed); err != nil {
		return err
	}
	if executor == nil {
		return &ExecutionError{Code: ExecutionExecutorMissing, Message: "AI executor is unavailable"}
	}
	if err := executor.Execute(ctx, rendered); err != nil {
		return &ExecutionError{Code: ExecutionFailed, Message: "AI execution failed", Cause: err}
	}
	return nil
}

// ExecuteRenderedAI is the safe composition of RenderContract and ExecuteAI.
// It rejects a failed render before checking permissions or touching executor,
// preserving structured render diagnostics for the caller.
func (c Contract) ExecuteRenderedAI(ctx context.Context, rendered RenderResult, reviewConfirmed bool, executor AIExecutor) error {
	if len(rendered.Diagnostics) != 0 || rendered.RenderedText == "" {
		return &ExecutionError{Code: ExecutionRenderFailed, Message: "template rendering failed"}
	}
	return c.ExecuteAI(ctx, rendered.RenderedText, reviewConfirmed, executor)
}

// ExecuteAI is the package-level form for callers that do not retain a
// Contract value. It delegates to Contract.ExecuteAI so the gate has one
// implementation and one fail-closed policy.
func ExecuteAI(ctx context.Context, c Contract, rendered string, reviewConfirmed bool, executor AIExecutor) error {
	return c.ExecuteAI(ctx, rendered, reviewConfirmed, executor)
}

// IsExecutionDenied is a convenience for callers which need to classify a
// rejected request without depending on error-message text.
func IsExecutionDenied(err error) bool {
	var executionErr *ExecutionError
	return errors.As(err, &executionErr) && executionErr != nil && executionErr.Code != ExecutionFailed
}
