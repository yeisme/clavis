package ai

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// TestReasoningEffortInRequest: a profile's reasoning_effort goes into the
// OpenAI-compatible body; without one the body has no such key, as before.
func TestReasoningEffortInRequest(t *testing.T) {
	client := NewOpenAICompatibleClient(config.DefaultAIConfig(), engine.GlossaryEngine{}, nil, nil)
	for _, tc := range []struct {
		effort string
		want   any
	}{
		{"low", "low"},
		{"  high ", "high"},
		{"", nil},
	} {
		profile := config.AIProfile{
			ID: "magpie", Provider: "magpie", Protocol: config.ProtocolOpenAICompatible,
			BaseURL: "http://10.0.0.1:3425/v1", Model: "huoshan/deepseek-v4-1-flash-261017",
			Temperature: 0.2, TimeoutSeconds: 60, ReasoningEffort: tc.effort,
		}
		req, err := client.BuildRequest(context.Background(), CompletionRequest{Mode: "polish", SourceText: "x", Profile: &profile}, "sk-test")
		if err != nil {
			t.Fatalf("BuildRequest(%q): %v", tc.effort, err)
		}
		body, _ := io.ReadAll(req.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("body: %v", err)
		}
		got, present := payload["reasoning_effort"]
		if tc.want == nil && present {
			t.Errorf("effort %q: body has reasoning_effort %v, want none", tc.effort, got)
		}
		if tc.want != nil && got != tc.want {
			t.Errorf("effort %q: reasoning_effort = %v, want %v", tc.effort, got, tc.want)
		}
	}
}
