package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

type stubVisionDoer struct{}

func (*stubVisionDoer) Do(*http.Request) (*http.Response, error) { return nil, nil }

func TestResolveVisionProfilePriorityAndFallback(t *testing.T) {
	cfg := config.DefaultAIConfig()
	// Default active is deepseek (no Vision) -> falls back to first Vision-capable profile (claude).
	p, err := ResolveVisionProfile(cfg)
	if err != nil {
		t.Fatalf("ResolveVisionProfile default fallback: %v", err)
	}
	if !p.Capabilities.Vision || p.ID != "claude" {
		t.Fatalf("expected claude fallback, got %+v", p)
	}

	// Explicit VisionProfileID overrides fallback order.
	cfg.VisionProfileID = "gemini"
	p, err = ResolveVisionProfile(cfg)
	if err != nil {
		t.Fatalf("ResolveVisionProfile explicit gemini: %v", err)
	}
	if p.ID != "gemini" || !p.Capabilities.Vision {
		t.Fatalf("expected gemini, got %+v", p)
	}

	// No Vision-capable profiles -> vision_unsupported.
	cfg.VisionProfileID = ""
	cfg.ActiveProfileID = "deepseek"
	for i := range cfg.Profiles {
		cfg.Profiles[i].Capabilities.Vision = false
	}
	_, err = ResolveVisionProfile(cfg)
	var ce *ClientError
	if !errors.As(err, &ce) || ce.Kind != "vision_unsupported" {
		t.Fatalf("expected vision_unsupported error, got %v", err)
	}
}

func TestBuildRequestVisionThreeProtocolsAndGuards(t *testing.T) {
	client := NewOpenAICompatibleClient(config.DefaultAIConfig(), engine.GlossaryEngine{}, &stubVisionDoer{}, func(context.Context) (string, error) {
		return "k", nil
	})
	imgBytes := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3}
	wantB64 := base64.StdEncoding.EncodeToString(imgBytes)

	// 1. OpenAI-compatible multimodal payload
	oaiProfile := &config.AIProfile{
		ID:           "oai-vis",
		Protocol:     config.ProtocolOpenAICompatible,
		BaseURL:      "https://api.openai.com/v1",
		Model:        "gpt-4o-mini",
		Temperature:  0.1,
		Capabilities: config.ModelCapabilities{Vision: true, Streaming: true},
	}
	req, err := client.BuildRequest(context.Background(), CompletionRequest{
		Mode:    "ocr",
		Prompt:  "Extract text",
		Profile: oaiProfile,
		Images:  []ImageAttachment{{MIMEType: "image/png", Data: imgBytes, Detail: "high"}},
	}, "sk-test")
	if err != nil {
		t.Fatalf("OpenAI BuildRequest: %v", err)
	}
	raw, _ := io.ReadAll(req.Body)
	var oaiBody map[string]any
	if err := json.Unmarshal(raw, &oaiBody); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "data:image/png;base64,"+wantB64) {
		t.Fatalf("OpenAI payload missing data URL image: %s", string(raw))
	}

	// 2. Anthropic multimodal payload
	antProfile := &config.AIProfile{
		ID:           "claude",
		Protocol:     config.ProtocolAnthropic,
		BaseURL:      "https://api.anthropic.com/v1",
		Model:        "claude-3-7-sonnet-latest",
		Temperature:  0.1,
		Capabilities: config.ModelCapabilities{Vision: true, Streaming: true},
	}
	req, err = client.BuildRequest(context.Background(), CompletionRequest{
		Mode:    "ocr",
		Prompt:  "Extract text",
		Profile: antProfile,
		Images:  []ImageAttachment{{MIMEType: "image/png", Data: imgBytes}},
	}, "sk-ant")
	if err != nil {
		t.Fatalf("Anthropic BuildRequest: %v", err)
	}
	raw, _ = io.ReadAll(req.Body)
	if !strings.Contains(string(raw), `"type":"base64"`) || !strings.Contains(string(raw), `"media_type":"image/png"`) || !strings.Contains(string(raw), wantB64) {
		t.Fatalf("Anthropic payload malformed: %s", string(raw))
	}

	// 3. Gemini multimodal payload
	gemProfile := &config.AIProfile{
		ID:           "gemini",
		Protocol:     config.ProtocolGemini,
		BaseURL:      "https://generativelanguage.googleapis.com/v1beta",
		Model:        "gemini-2.5-flash",
		Temperature:  0.1,
		Capabilities: config.ModelCapabilities{Vision: true, Streaming: true},
	}
	req, err = client.BuildRequest(context.Background(), CompletionRequest{
		Mode:    "ocr",
		Prompt:  "Extract text",
		Profile: gemProfile,
		Images:  []ImageAttachment{{MIMEType: "image/png", Data: imgBytes}},
	}, "gm-key")
	if err != nil {
		t.Fatalf("Gemini BuildRequest: %v", err)
	}
	raw, _ = io.ReadAll(req.Body)
	if !strings.Contains(string(raw), `"inline_data"`) || !strings.Contains(string(raw), `"mime_type":"image/png"`) || !strings.Contains(string(raw), wantB64) {
		t.Fatalf("Gemini payload malformed: %s", string(raw))
	}

	// 4. Reject vision_unsupported when Vision capability is false
	noVis := &config.AIProfile{
		ID:           "text-only",
		Protocol:     config.ProtocolOpenAICompatible,
		BaseURL:      "https://api.deepseek.com/v1",
		Model:        "deepseek-chat",
		Capabilities: config.ModelCapabilities{Vision: false},
	}
	_, err = client.BuildRequest(context.Background(), CompletionRequest{
		Prompt:  "OCR",
		Profile: noVis,
		Images:  []ImageAttachment{{MIMEType: "image/png", Data: imgBytes}},
	}, "k")
	var ce *ClientError
	if !errors.As(err, &ce) || ce.Kind != "vision_unsupported" {
		t.Fatalf("expected vision_unsupported error, got %v", err)
	}

	// 5. Reject oversized (>10MiB) or unsupported MIME
	_, err = client.BuildRequest(context.Background(), CompletionRequest{
		Prompt:  "OCR",
		Profile: oaiProfile,
		Images:  []ImageAttachment{{MIMEType: "image/bmp", Data: imgBytes}},
	}, "k")
	if !errors.As(err, &ce) || ce.Kind != "invalid_image" {
		t.Fatalf("expected invalid_image for unsupported MIME, got %v", err)
	}
	big := make([]byte, MaxImageAttachmentBytes+1)
	_, err = client.BuildRequest(context.Background(), CompletionRequest{
		Prompt:  "OCR",
		Profile: oaiProfile,
		Images:  []ImageAttachment{{MIMEType: "image/png", Data: big}},
	}, "k")
	if !errors.As(err, &ce) || ce.Kind != "invalid_image" {
		t.Fatalf("expected invalid_image for >10MiB payload, got %v", err)
	}
}
