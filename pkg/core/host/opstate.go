package host

import "time"

// OperationState describes the observable outcome of a cross-process, file,
// or network operation. Unknown means the caller cannot confirm the outcome.
type OperationState string

const (
	StateReady      OperationState = "ready"
	StateRefreshing OperationState = "refreshing"
	StateDegraded   OperationState = "degraded"
	StateFailed     OperationState = "failed"
	StateUnknown    OperationState = "unknown"
)

// OperationResult is the stable, user-observable result contract for host operations.
type OperationResult struct {
	State      OperationState `json:"state"`
	Code       string         `json:"code"`
	Message    string         `json:"message"`
	ObservedAt time.Time      `json:"observedAt"`
	Retryable  bool           `json:"retryable"`
}
