package ai

import (
	"math/big"
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
	header := sanitizeMCPResource("Authorization: Basic dXNlcjpwYXNz\nCookie: session=abc123", 2048)
	for _, secret := range []string{"dXNlcjpwYXNz", "session=abc123"} {
		if strings.Contains(header, secret) {
			t.Fatalf("header secret %q remained: %s", secret, header)
		}
	}
	escaped := sanitizeMCPResource(`{"\u0061piKey":"escaped-secret","\u0061uthorization":"Basic escaped-auth"}`, 2048)
	for _, secret := range []string{"escaped-secret", "escaped-auth"} {
		if strings.Contains(escaped, secret) {
			t.Fatalf("escaped-key secret %q remained: %s", secret, escaped)
		}
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
	gotExponent, err := ParseStructuredOutput(`1e3`, map[string]any{"type": "integer"})
	if err != nil {
		t.Fatalf("exponent integer rejected: %v", err)
	}
	switch value := gotExponent.(type) {
	case int64:
		if value != 1000 {
			t.Fatalf("exponent integer normalized incorrectly: %#v", gotExponent)
		}
	case *big.Int:
		if value.String() != "1000" {
			t.Fatalf("exponent integer normalized incorrectly: %#v", gotExponent)
		}
	default:
		t.Fatalf("exponent integer normalized incorrectly: %#v", gotExponent)
	}
	gotBig, err := ParseStructuredOutput(`9223372036854775808`, map[string]any{"type": "integer"})
	if err != nil {
		t.Fatalf("big integer rejected: %v", err)
	}
	integer, ok := gotBig.(*big.Int)
	if !ok || integer.String() != "9223372036854775808" {
		t.Fatalf("big integer precision lost: %#v", gotBig)
	}
	if _, err := ParseStructuredOutput(`1`, map[string]any{"type": "integer", "enum": []any{float64(1)}}); err != nil {
		t.Fatalf("numeric enum rejected: %v", err)
	}
	if _, err := ParseStructuredOutput(`0.1`, map[string]any{"type": "number", "enum": []any{float64(0.1)}}); err != nil {
		t.Fatalf("decimal numeric enum rejected: %v", err)
	}
	fraction, err := ParseStructuredOutput(`9007199254740992.1`, map[string]any{"type": "number"})
	if err != nil {
		t.Fatalf("exact fraction rejected: %v", err)
	}
	fractionRat, ok := fraction.(*big.Rat)
	if !ok || fractionRat.RatString() != "90071992547409921/10" {
		t.Fatalf("fraction precision lost: %#v", fraction)
	}
	if _, err := ParseStructuredOutput("1e"+strings.Repeat("9", 5000), map[string]any{"type": "number"}); err == nil || !strings.Contains(err.Error(), "must be number") {
		t.Fatalf("oversized numeric literal was accepted: %v", err)
	}
	veryLarge := "10000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001"
	gotVeryLarge, err := ParseStructuredOutput(veryLarge, map[string]any{"type": "integer"})
	if err != nil {
		t.Fatalf("very large integer rejected: %v", err)
	}
	veryLargeInt, ok := gotVeryLarge.(*big.Int)
	if !ok || veryLargeInt.String() != veryLarge {
		t.Fatalf("very large integer precision lost: %#v", gotVeryLarge)
	}
	if _, err := ParseStructuredOutput("10000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000.5", map[string]any{"type": "integer"}); err == nil || !strings.Contains(err.Error(), "must be integer") {
		t.Fatalf("huge fractional integer was accepted: %v", err)
	}
	if _, err := ParseStructuredOutput(`{"ok":true} trailing`, map[string]any{"type": "object"}); err == nil || !strings.Contains(err.Error(), "trailing data") {
		t.Fatalf("expected trailing data error, got %v", err)
	}
}
