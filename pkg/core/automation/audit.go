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
	At                                     time.Time `json:"at"`
	Actor, Profile, Template, Target, Tool string    `json:"actor,omitempty"`
	InputFingerprint                       string    `json:"inputFingerprint,omitempty"`
	Summary, Status, ErrorCategory         string    `json:"summary,omitempty"`
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
func ErrorCategory(err error) string {
	if err == nil {
		return ""
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "cancel") {
		return "cancelled"
	}
	if strings.Contains(s, "timeout") {
		return "timeout"
	}
	return "execution_failed"
}
