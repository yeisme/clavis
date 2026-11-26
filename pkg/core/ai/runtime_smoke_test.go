package ai_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

type runtimeSmokeDoer struct {
	fn func(*http.Request) (*http.Response, error)
}

func (d runtimeSmokeDoer) Do(req *http.Request) (*http.Response, error) {
	return d.fn(req)
}

func smokeSSE(text string) *http.Response {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"" + text + "\"}}]}\n\ndata: [DONE]\n\n"
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
}

func TestRuntimeSmokeProviderPathsHaveStableCodes(t *testing.T) {
	cfg := config.DefaultAIConfig()
	textProfile := cfg.ActiveProfile()
	textProfile.BaseURL = "https://provider.example/v1"
	textProfile.Model = "text-model"

	client := ai.NewOpenAICompatibleClient(cfg, engine.GlossaryEngine{}, runtimeSmokeDoer{
		fn: func(*http.Request) (*http.Response, error) { return smokeSSE("text ok"), nil },
	}, func(context.Context) (string, error) { return "fixture-key", nil })
	var text strings.Builder
	if err := client.StreamCompletion(context.Background(), ai.CompletionRequest{Prompt: "hello", Profile: &textProfile}, func(delta string) {
		_, _ = text.WriteString(delta)
	}); err != nil {
		t.Fatalf("text provider err=%v code=%q", err, ai.ErrorCode(err))
	}
	if text.String() != "text ok" || ai.ErrorCode(nil) != ai.ErrorCodeOK {
		t.Fatalf("text provider output=%q code=%q", text.String(), ai.ErrorCode(nil))
	}

	visionProfile := textProfile
	visionProfile.ID = "vision-smoke"
	visionProfile.Model = "vision-model"
	visionProfile.Capabilities.Vision = true
	visionClient := ai.NewOpenAICompatibleClient(cfg, engine.GlossaryEngine{}, runtimeSmokeDoer{
		fn: func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(body), "image_url") {
				return nil, errors.New("vision payload missing image")
			}
			return smokeSSE("vision ok"), nil
		},
	}, func(context.Context) (string, error) { return "fixture-key", nil })
	var vision strings.Builder
	if err := visionClient.StreamCompletion(context.Background(), ai.CompletionRequest{
		Prompt:  "describe",
		Profile: &visionProfile,
		Images:  []ai.ImageAttachment{{MIMEType: "image/png", Data: []byte("png")}},
	}, func(delta string) {
		_, _ = vision.WriteString(delta)
	}); err != nil {
		t.Fatalf("vision provider err=%v code=%q", err, ai.ErrorCode(err))
	}
	if vision.String() != "vision ok" {
		t.Fatalf("vision provider output=%q", vision.String())
	}

	missingClient := ai.NewOpenAICompatibleClient(cfg, engine.GlossaryEngine{}, runtimeSmokeDoer{
		fn: func(*http.Request) (*http.Response, error) { return nil, errors.New("request must not run") },
	}, func(context.Context) (string, error) {
		return "", errors.New("keychain missing sk-runtime-secret-123456")
	})
	missingErr := missingClient.StreamCompletion(context.Background(), ai.CompletionRequest{Prompt: "hello", Profile: &textProfile}, nil)
	if got := ai.ErrorCode(missingErr); got != ai.ErrorCodeMissingAPIKey {
		t.Fatalf("missing key code=%q err=%v", got, missingErr)
	}
	if strings.Contains(missingErr.Error(), "sk-runtime-secret-123456") {
		t.Fatalf("missing key leaked through error: %v", missingErr)
	}

	timeoutClient := ai.NewOpenAICompatibleClient(cfg, engine.GlossaryEngine{}, runtimeSmokeDoer{
		fn: func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
	}, func(context.Context) (string, error) { return "fixture-key", nil })
	timeoutErr := timeoutClient.StreamCompletion(context.Background(), ai.CompletionRequest{Prompt: "hello", Profile: &textProfile}, nil)
	if got := ai.ErrorCode(timeoutErr); got != ai.ErrorCodeTimeout {
		t.Fatalf("timeout code=%q err=%v", got, timeoutErr)
	}
}

func TestRuntimeSmokeProviderErrorRedactsResponseSecret(t *testing.T) {
	secret := "sk-runtime-secret-123456"
	cfg := config.DefaultAIConfig()
	profile := cfg.ActiveProfile()
	profile.BaseURL = "https://provider.example/v1"
	client := ai.NewOpenAICompatibleClient(cfg, engine.GlossaryEngine{}, runtimeSmokeDoer{
		fn: func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"invalid key ` + secret + `"}}`)),
			}, nil
		},
	}, func(context.Context) (string, error) { return secret, nil })
	err := client.StreamCompletion(context.Background(), ai.CompletionRequest{Prompt: "hello", Profile: &profile}, nil)
	if got := ai.ErrorCode(err); got != "http_error" {
		t.Fatalf("provider error code=%q err=%v", got, err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("provider response leaked secret: %v", err)
	}
}
