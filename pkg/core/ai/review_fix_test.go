package ai

import (
	"strings"
	"testing"
)

func TestIsKeylessAllowedRequiresLocalEndpoint(t *testing.T) {
	if !IsKeylessAllowed("ollama", "http://127.0.0.1:11434/v1") {
		t.Fatal("local Ollama should be keyless")
	}
	if IsKeylessAllowed("ollama", "https://remote.example/v1") {
		t.Fatal("remote Ollama must require credentials")
	}
	if !IsKeylessAllowed("openai", "http://localhost:8000/v1") {
		t.Fatal("local endpoint should remain keyless")
	}
}

func TestSanitizeMCPResourceRedactsJSONSecrets(t *testing.T) {
	raw := `{"apiKey": "secret", "authorization": "Basic dXNlcjpwYXNz", "token": "abc\"TAIL", "cookie": "session=abc"}`
	got := sanitizeMCPResource(raw, 2048)
	for _, secret := range []string{"secret", "dXNlcjpwYXNz", "abc\\\"TAIL", "session=abc"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q remained: %s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected redaction marker: %s", got)
	}
}

func TestParseStructuredOutputEnforcesIntegerAndEnum(t *testing.T) {
	if _, err := ParseStructuredOutput(`"not an integer"`, map[string]any{"type": "integer"}); err == nil || !strings.Contains(err.Error(), "must be integer") {
		t.Fatalf("expected integer mismatch, got %v", err)
	}
	if _, err := ParseStructuredOutput(`"bad"`, map[string]any{"type": "string", "enum": []any{"open", "closed"}}); err == nil || !strings.Contains(err.Error(), "enum") {
		t.Fatalf("expected enum mismatch, got %v", err)
	}
	if _, err := ParseStructuredOutput(`"open"`, map[string]any{"type": "string", "enum": []any{"open", "closed"}}); err != nil {
		t.Fatalf("valid enum rejected: %v", err)
	}
	got, err := ParseStructuredOutput(`9007199254740993`, map[string]any{"type": "integer"})
	if err != nil {
		t.Fatalf("large integer rejected: %v", err)
	}
	if got != int64(9007199254740993) {
		t.Fatalf("large integer precision lost: %#v", got)
	}
	if _, err := ParseStructuredOutput(`1.5`, map[string]any{"type": "number"}); err != nil {
		t.Fatalf("json number rejected: %v", err)
	}
	if _, err := ParseStructuredOutput(`1e3`, map[string]any{"type": "integer"}); err != nil {
		t.Fatalf("exponent integer rejected: %v", err)
	}
	if _, err := ParseStructuredOutput(`1`, map[string]any{"type": "integer", "enum": []any{float64(1)}}); err != nil {
		t.Fatalf("numeric enum rejected: %v", err)
	}
	if _, err := ParseStructuredOutput(`{"ok":true} trailing`, map[string]any{"type": "object"}); err == nil || !strings.Contains(err.Error(), "trailing data") {
		t.Fatalf("expected trailing data error, got %v", err)
	}
}
