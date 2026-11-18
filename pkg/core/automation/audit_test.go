package automation

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAuditEventJSONRoundTripPreservesAllFields(t *testing.T) {
	original := AuditEvent{
		At:               time.Date(2026, time.November, 18, 16, 0, 0, 0, time.UTC),
		Actor:            "clavis",
		Profile:          "deepseek",
		Template:         "code-review",
		Target:           "pi",
		Tool:             "mcp.echo",
		InputFingerprint: FingerprintInput("payload"),
		Summary:          "completed",
		Status:           "succeeded",
		ErrorCategory:    "execution_failed",
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"actor", "profile", "template", "target", "tool", "inputFingerprint", "summary", "status", "errorCategory"} {
		if !strings.Contains(string(data), `"`+field+`"`) {
			t.Fatalf("serialized audit event is missing %q: %s", field, data)
		}
	}
	var decoded AuditEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("audit event round trip mismatch:\n got %#v\nwant %#v", decoded, original)
	}
}

func TestAuditLogClassifiesErrorsAndIsolatesSnapshots(t *testing.T) {
	if got := ErrorCategory(nil); got != "" {
		t.Fatalf("nil error category = %q", got)
	}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errors.New("request canceled by user"), "cancelled"},
		{errors.New("context deadline timeout"), "timeout"},
		{errors.New("provider rejected request"), "execution_failed"},
	} {
		if got := ErrorCategory(tc.err); got != tc.want {
			t.Errorf("ErrorCategory(%q) = %q, want %q", tc.err, got, tc.want)
		}
	}
	if first, second := FingerprintInput("same"), FingerprintInput("same"); first != second || !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("fingerprint is not deterministic: %q, %q", first, second)
	}

	var log AuditLog
	log.Append(AuditEvent{Actor: "clavis", Status: "running"})
	snapshot := log.Snapshot()
	if len(snapshot) != 1 || snapshot[0].At.IsZero() {
		t.Fatalf("append did not assign timestamp: %#v", snapshot)
	}
	snapshot[0].Actor = "mutated"
	if got := log.Snapshot()[0].Actor; got != "clavis" {
		t.Fatalf("snapshot mutation leaked into log: %q", got)
	}
}
