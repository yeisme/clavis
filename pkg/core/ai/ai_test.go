package ai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestSSEStreamParserSplitUTF8AndEvents(t *testing.T) {
	var parser ai.SSEStreamParser

	// Split a 4-byte emoji (😀 = F0 9F 98 80) and CRLF across chunks
	line1 := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hi ")
	emoji := []byte("😀")
	line1End := []byte("\"}}]}\r")
	line2 := []byte("\n: keepalive comment\r\n\r\n")
	multiline := []byte("event: message\ndata: {\"choices\":[{\"delta\":\ndata: {\"content\":\" world\"}}]}\n\ndata: [DONE]\n\n")

	var events []ai.SSEEvent
	events = append(events, parser.Feed(line1)...)
	events = append(events, parser.Feed(emoji[:2])...)
	events = append(events, parser.Feed(emoji[2:])...)
	events = append(events, parser.Feed(line1End)...)
	events = append(events, parser.Feed(line2)...)
	events = append(events, parser.Feed(multiline)...)
	events = append(events, parser.Finish()...)

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %+v", events)
	}
	if events[0].Kind != ai.SSEDelta || events[0].Text != "Hi 😀" {
		t.Fatalf("unexpected event 0: %+v", events[0])
	}
	if events[1].Kind != ai.SSEDelta || events[1].Text != " world" {
		t.Fatalf("unexpected event 1: %+v", events[1])
	}
	if events[2].Kind != ai.SSEDone {
		t.Fatalf("unexpected event 2: %+v", events[2])
	}
}

func TestSSEStreamParserErrorJSON(t *testing.T) {
	var parser ai.SSEStreamParser
	events := parser.Feed([]byte("data: {\"error\":{\"message\":\"Rate limit exceeded\"}}\n\n"))
	if len(events) != 1 || events[0].Kind != ai.SSEError || events[0].Text != "Rate limit exceeded" {
		t.Fatalf("unexpected error event: %+v", events)
	}
}

type mockDoer struct {
	fn func(req *http.Request) (*http.Response, error)
}

func (m mockDoer) Do(req *http.Request) (*http.Response, error) {
	return m.fn(req)
}

func TestOpenAICompatibleClientRequestAndStreaming(t *testing.T) {
	cfg := config.DefaultAIConfig()
	cfg.BaseURL = "https://example.com/v1/"
	glossary := engine.NewGlossaryEngine([]config.GlossaryTerm{
		{Term: "Clavis", Aliases: []string{"clavis"}, Hint: "Capitalize.", Category: "product"},
	})

	var capturedReq *http.Request
	var capturedBody map[string]any

	doer := mockDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			capturedReq = req
			raw, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(raw, &capturedBody)
			sseBody := "data: {\"choices\":[{\"delta\":{\"content\":\"Hello \"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"Clavis\"}}]}\n\ndata: [DONE]\n\n"
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(sseBody)),
			}, nil
		},
	}

	client := ai.NewOpenAICompatibleClient(cfg, glossary, doer, func(ctx context.Context) (string, error) {
		return "sk-test-token", nil
	})

	var out strings.Builder
	err := client.StreamCompletion(context.Background(), ai.CompletionRequest{
		Mode:       "polish",
		Prompt:     "Polish this",
		SourceText: "use clavis",
	}, func(delta string) {
		out.WriteString(delta)
	})
	if err != nil {
		t.Fatalf("StreamCompletion failed: %v", err)
	}
	if out.String() != "Hello Clavis" {
		t.Fatalf("unexpected output: %q", out.String())
	}
	if capturedReq.URL.String() != "https://example.com/v1/chat/completions" {
		t.Fatalf("unexpected URL: %s", capturedReq.URL.String())
	}
	if capturedReq.Header.Get("Authorization") != "Bearer sk-test-token" {
		t.Fatalf("missing bearer auth header: %q", capturedReq.Header.Get("Authorization"))
	}
	msgs := capturedBody["messages"].([]any)
	sysMsg := msgs[0].(map[string]any)["content"].(string)
	if !strings.Contains(sysMsg, "<glossary>") || !strings.Contains(sysMsg, "Clavis") {
		t.Fatalf("expected glossary injected into system prompt: %s", sysMsg)
	}

	// Verify already-suffixed base URL does not double /chat/completions
	u, err := ai.ResolveCompletionsURL("https://example.com/v1/chat/completions/")
	if err != nil || u != "https://example.com/v1/chat/completions" {
		t.Fatalf("unexpected resolved URL: %q (%v)", u, err)
	}
}

func TestOpenAIClientCancellation(t *testing.T) {
	cfg := config.DefaultAIConfig()
	ctx, cancel := context.WithCancel(context.Background())

	pr, pw := io.Pipe()
	doer := mockDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			go func() {
				_, _ = pw.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"part1\"}}]}\n\n"))
				cancel()
				_ = pw.Close()
			}()
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       pr,
			}, nil
		},
	}

	client := ai.NewOpenAICompatibleClient(cfg, engine.GlossaryEngine{}, doer, func(ctx context.Context) (string, error) {
		return "sk-test", nil
	})

	err := client.StreamCompletion(ctx, ai.CompletionRequest{Prompt: "hi"}, func(string) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
