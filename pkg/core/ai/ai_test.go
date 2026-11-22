package ai_test

import (
	"bytes"
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

func TestProviderUsageParsingReportedAndUnavailable(t *testing.T) {
	got := ai.ParseProviderUsage([]byte(`{"usage":{"prompt_tokens":12,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":3}}}`))
	if !got.Reported || got.InputTokens != 12 || got.OutputTokens != 7 || got.CachedTokens != 3 {
		t.Fatalf("usage=%+v", got)
	}
	missing := ai.ParseProviderUsage([]byte(`{"choices":[]}`))
	if missing.Reported || missing.InputTokens != 0 || missing.OutputTokens != 0 {
		t.Fatalf("missing usage=%+v", missing)
	}
}

func TestProviderUsageResponseParsing(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(`{"usage":{"prompt_tokens":8,"completion_tokens":5}}`))}
	got, err := ai.ParseProviderUsageResponse(resp)
	if err != nil || !got.Reported || got.InputTokens != 8 || got.OutputTokens != 5 {
		t.Fatalf("usage=%+v err=%v", got, err)
	}
	missing := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(`{"choices":[{"message":{"content":"ok"}}]}`))}
	got, err = ai.ParseProviderUsageResponse(missing)
	if err != nil || got.Reported {
		t.Fatalf("missing usage=%+v err=%v", got, err)
	}
}

func TestSSEStreamParserUsageFinalChunk(t *testing.T) {
	var parser ai.SSEStreamParser
	events := parser.Feed([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n"))
	found := false
	for _, event := range events {
		if event.Kind == ai.SSEUsage && event.Usage.Reported && event.Usage.InputTokens == 4 && event.Usage.OutputTokens == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("events=%+v", events)
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
			sseBody := "data: {\"choices\":[{\"delta\":{\"content\":\"Hello \"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"Clavis\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n"
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
	usage, err := client.StreamCompletionWithUsage(context.Background(), ai.CompletionRequest{
		Mode:       "polish",
		Prompt:     "Polish this",
		SourceText: "use clavis",
	}, func(delta string) {
		out.WriteString(delta)
	})
	if err != nil {
		t.Fatalf("StreamCompletion failed: %v", err)
	}
	if !usage.Reported || usage.InputTokens != 9 || usage.OutputTokens != 4 {
		t.Fatalf("usage=%+v", usage)
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

func TestAnthropicGeminiAndKeylessOllamaStreaming(t *testing.T) {
	cfg := config.DefaultAIConfig()
	glossary := engine.GlossaryEngine{}

	// 1. Anthropic Claude streaming + multi-turn history
	claudeProf, _ := cfg.ProfileByID("claude")
	var anthropicReq *http.Request
	var anthropicBody map[string]any

	anthropicDoer := mockDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			anthropicReq = req
			raw, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(raw, &anthropicBody)
			sse := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Claude \"}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"reply\"}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(sse)),
			}, nil
		},
	}

	client := ai.NewOpenAICompatibleClient(cfg, glossary, anthropicDoer, func(ctx context.Context) (string, error) {
		return "sk-ant-test", nil
	})
	var claudeOut strings.Builder
	err := client.StreamCompletion(context.Background(), ai.CompletionRequest{
		Prompt:  "Make it shorter",
		Profile: &claudeProf,
		History: []ai.ChatMessage{
			{Role: "user", Content: "Original draft"},
			{Role: "assistant", Content: "First revision"},
		},
	}, func(d string) {
		claudeOut.WriteString(d)
	})
	if err != nil {
		t.Fatalf("Anthropic StreamCompletion failed: %v", err)
	}
	if claudeOut.String() != "Claude reply" {
		t.Fatalf("unexpected Anthropic output: %q", claudeOut.String())
	}
	if anthropicReq.URL.String() != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("unexpected Anthropic URL: %s", anthropicReq.URL.String())
	}
	if anthropicReq.Header.Get("x-api-key") != "sk-ant-test" || anthropicReq.Header.Get("anthropic-version") != "2023-06-01" {
		t.Fatalf("missing Anthropic headers: %+v", anthropicReq.Header)
	}
	if msgs, ok := anthropicBody["messages"].([]any); !ok || len(msgs) != 3 {
		t.Fatalf("expected 3 messages in Anthropic payload, got %+v", anthropicBody["messages"])
	}

	// 2. Gemini streaming + multi-turn history
	geminiProf, _ := cfg.ProfileByID("gemini")
	var geminiReq *http.Request
	var geminiBody map[string]any

	geminiDoer := mockDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			geminiReq = req
			raw, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(raw, &geminiBody)
			sse := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Gemini \"}]}}]}\n\n" +
				"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"stream\"}]},\"finishReason\":\"STOP\"}]}\n\n"
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(sse)),
			}, nil
		},
	}

	client.Doer = geminiDoer
	var geminiOut strings.Builder
	err = client.StreamCompletion(context.Background(), ai.CompletionRequest{
		Prompt:  "Follow up",
		Profile: &geminiProf,
		History: []ai.ChatMessage{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
		},
	}, func(d string) {
		geminiOut.WriteString(d)
	})
	if err != nil {
		t.Fatalf("Gemini StreamCompletion failed: %v", err)
	}
	if geminiOut.String() != "Gemini stream" {
		t.Fatalf("unexpected Gemini output: %q", geminiOut.String())
	}
	if geminiReq.URL.String() != "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse" {
		t.Fatalf("unexpected Gemini URL: %s", geminiReq.URL.String())
	}
	if contents, ok := geminiBody["contents"].([]any); !ok || len(contents) != 3 {
		t.Fatalf("expected 3 contents in Gemini payload, got %+v", geminiBody["contents"])
	}

	// 3. Keyless local Ollama streaming (empty API key permitted on localhost / ollama)
	ollamaProf, _ := cfg.ProfileByID("ollama")
	var ollamaReq *http.Request
	ollamaDoer := mockDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			ollamaReq = req
			sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Local Qwen\"}}]}\n\ndata: [DONE]\n\n"
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(sse)),
			}, nil
		},
	}
	ollamaClient := ai.NewOpenAICompatibleClient(cfg, glossary, ollamaDoer, func(ctx context.Context) (string, error) {
		return "", errors.New("no key in keychain")
	})
	var ollamaOut strings.Builder
	err = ollamaClient.StreamCompletion(context.Background(), ai.CompletionRequest{
		Prompt:  "Hello local",
		Profile: &ollamaProf,
	}, func(d string) {
		ollamaOut.WriteString(d)
	})
	if err != nil {
		t.Fatalf("Keyless Ollama StreamCompletion failed: %v", err)
	}
	if ollamaOut.String() != "Local Qwen" || ollamaReq.Header.Get("Authorization") != "" {
		t.Fatalf("unexpected Ollama response or auth header: out=%q auth=%q", ollamaOut.String(), ollamaReq.Header.Get("Authorization"))
	}
}
