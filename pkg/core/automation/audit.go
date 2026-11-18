package automation

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// AuditEvent intentionally stores references, fingerprints and classifications only.
type AuditEvent struct {
	At               time.Time `json:"at"`
	Actor            string    `json:"actor,omitempty"`
	Profile          string    `json:"profile,omitempty"`
	Template         string    `json:"template,omitempty"`
	Target           string    `json:"target,omitempty"`
	Tool             string    `json:"tool,omitempty"`
	InputFingerprint string    `json:"inputFingerprint,omitempty"`
	Summary          string    `json:"summary,omitempty"`
	Status           string    `json:"status,omitempty"`
	ErrorCategory    string    `json:"errorCategory,omitempty"`
}
type AuditLog struct {
	mu     sync.Mutex
	events []AuditEvent
}

func (l *AuditLog) Append(e AuditEvent) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
}
func (l *AuditLog) Snapshot() []AuditEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]AuditEvent(nil), l.events...)
}
func FingerprintInput(s string) string {
	h := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(h[:])
}

const (
	ErrorCategoryCancelled     = "cancelled"
	ErrorCategoryTimeout       = "timeout"
	ErrorCategoryOutputSchema  = "output_schema"
	ErrorCategoryExecutionFail = "execution_failed"
)

func ErrorCategory(err error) string {
	if err == nil {
		return ""
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "cancel") {
		return ErrorCategoryCancelled
	}
	if strings.Contains(s, "timeout") {
		return ErrorCategoryTimeout
	}
	if strings.Contains(s, "output schema") || strings.Contains(s, "output_schema") {
		return ErrorCategoryOutputSchema
	}
	return ErrorCategoryExecutionFail
}
