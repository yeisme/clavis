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
	raw := `{"apiKey": "secret", "Authorization": "Bearer bearer-secret", "token": "abc"}`
	got := sanitizeMCPResource(raw, 2048)
	for _, secret := range []string{"secret", "bearer-secret", "abc"} {
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
}
