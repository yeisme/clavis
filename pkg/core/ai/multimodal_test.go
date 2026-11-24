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

func TestMultimodalValidationAndProfileEdgePaths(t *testing.T) {
	// MIME normalization aliases, parameters, whitespace, and rejections.
	mimeCases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{" IMAGE/JPG ; charset=binary ", "image/jpeg", true},
		{"image/webp", "image/webp", true},
		{"image/gif", "image/gif", true},
		{"image/svg+xml", "", false},
		{"", "", false},
	}
	for _, tc := range mimeCases {
		got, ok := NormalizeImageMIMEType(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Fatalf("NormalizeImageMIMEType(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}

	// Attachment slice boundaries: nil/0 allowed, 8 allowed, 9 rejected, empty Data rejected.
	if err := ValidateImageAttachments(nil); err != nil {
		t.Fatalf("nil attachments should succeed, got %v", err)
	}
	validOne := ImageAttachment{MIMEType: "image/jpg", Data: []byte{1, 2, 3}}
	if err := ValidateImageAttachments([]ImageAttachment{validOne, validOne, validOne, validOne, validOne, validOne, validOne, validOne}); err != nil {
		t.Fatalf("MaxImageAttachments (%d) should pass, got %v", MaxImageAttachments, err)
	}
	tooMany := make([]ImageAttachment, MaxImageAttachments+1)
	for i := range tooMany {
		tooMany[i] = validOne
	}
	var ce *ClientError
	if err := ValidateImageAttachments(tooMany); !errors.As(err, &ce) || ce.Kind != "invalid_image" {
		t.Fatalf("expected invalid_image for >MaxImageAttachments, got %v", err)
	}
	if err := ValidateImageAttachments([]ImageAttachment{{MIMEType: "image/png", Data: nil}}); !errors.As(err, &ce) || ce.Kind != "invalid_image" {
		t.Fatalf("expected invalid_image for empty payload, got %v", err)
	}

	// ResolveVisionProfile priorities:
	// 1. Unknown/non-Vision VisionProfileID ignored -> ActiveProfile selected if Vision=true.
	// 2. ActiveProfile Vision=false -> fallback to first Vision profile & fill blank Protocol.
	cfg := config.AIConfig{
		ActiveProfileID: "active-vis",
		VisionProfileID: "missing-id",
		Profiles: []config.AIProfile{
			{ID: "first-vis", Model: "fallback", Capabilities: config.ModelCapabilities{Vision: true}},
			{ID: "active-vis", Protocol: config.ProtocolAnthropic, Model: "claude", Capabilities: config.ModelCapabilities{Vision: true}},
		},
	}
	p, err := ResolveVisionProfile(cfg)
	if err != nil || p.ID != "active-vis" {
		t.Fatalf("expected ActiveProfile vision priority over fallback, got %+v err=%v", p, err)
	}
	cfg.ActiveProfileID = "no-vis"
	cfg.Profiles = []config.AIProfile{
		{ID: "no-vis", Model: "ds", Capabilities: config.ModelCapabilities{Vision: false}},
		{ID: "blank-proto-vis", Protocol: "", Model: "gpt-4o", Capabilities: config.ModelCapabilities{Vision: true}},
	}
	p, err = ResolveVisionProfile(cfg)
	if err != nil || p.ID != "blank-proto-vis" || p.Protocol != config.ProtocolOpenAICompatible {
		t.Fatalf("expected blank protocol normalized to OpenAICompatible, got %+v err=%v", p, err)
	}
	cfg.VisionProfileID = "blank-proto-vis"
	p, err = ResolveVisionProfile(cfg)
	if err != nil || p.Protocol != config.ProtocolOpenAICompatible {
		t.Fatalf("expected explicit VisionProfileID protocol normalization, got %+v err=%v", p, err)
	}

	// Encoders with whitespace-only prompt & no detail omit optional fields; zero images returns plain string.
	if s, ok := encodeOpenAIUserContent("hello", nil).(string); !ok || s != "hello" {
		t.Fatalf("expected plain string for zero images, got %#v", encodeOpenAIUserContent("hello", nil))
	}
	if s, ok := encodeAnthropicUserContent("hello", nil).(string); !ok || s != "hello" {
		t.Fatalf("expected plain Anthropic string for zero images, got %#v", encodeAnthropicUserContent("hello", nil))
	}
	oaiParts, _ := encodeOpenAIUserContent("   ", []ImageAttachment{{MIMEType: "image/jpg", Data: []byte("img"), Detail: "  "}}).([]map[string]any)
	if len(oaiParts) != 1 || oaiParts[0]["type"] != "image_url" {
		t.Fatalf("expected only image_url part when prompt blank, got %+v", oaiParts)
	}
	if _, hasDetail := oaiParts[0]["image_url"].(map[string]any)["detail"]; hasDetail {
		t.Fatalf("expected blank detail omitted, got %+v", oaiParts[0])
	}
	antParts, _ := encodeAnthropicUserContent("", []ImageAttachment{{MIMEType: "image/png", Data: []byte("img")}}).([]map[string]any)
	if len(antParts) != 1 || antParts[0]["type"] != "image" {
		t.Fatalf("expected single Anthropic image block without empty text part, got %+v", antParts)
	}
	gemParts := encodeGeminiUserParts("  ", []ImageAttachment{{MIMEType: "image/png", Data: []byte("img")}})
	if len(gemParts) != 1 || gemParts[0]["inline_data"] == nil {
		t.Fatalf("expected single Gemini inline_data part, got %+v", gemParts)
	}

	// BuildRequest guards: remote keyless blocked, localhost keyless allowed, invalid base URL rejected.
	client := NewOpenAICompatibleClient(cfg, engine.GlossaryEngine{}, &stubVisionDoer{}, nil)
	_, err = client.BuildRequest(context.Background(), CompletionRequest{
		Prompt:  "OCR",
		Profile: &config.AIProfile{ID: "remote", BaseURL: "https://api.openai.com/v1", Model: "gpt-4o", Capabilities: config.ModelCapabilities{Vision: true}},
	}, "")
	if !errors.As(err, &ce) || ce.Kind != "missing_api_key" {
		t.Fatalf("expected missing_api_key for remote URL, got %v", err)
	}
	if _, err := client.BuildRequest(context.Background(), CompletionRequest{
		Prompt:  "OCR",
		Profile: &config.AIProfile{ID: "local", BaseURL: "http://127.0.0.1:11434/v1", Model: "llava", Capabilities: config.ModelCapabilities{Vision: true}},
		Images:  []ImageAttachment{{MIMEType: "image/png", Data: []byte("img")}},
	}, ""); err != nil {
		t.Fatalf("localhost keyless vision request should succeed, got %v", err)
	}
	_, err = client.BuildRequest(context.Background(), CompletionRequest{
		Prompt:  "OCR",
		Profile: &config.AIProfile{ID: "bad-url", BaseURL: "ftp://bad", Model: "m"},
	}, "k")
	if !errors.As(err, &ce) || ce.Kind != "invalid_base_url" {
		t.Fatalf("expected invalid_base_url for ftp scheme, got %v", err)
	}
}
