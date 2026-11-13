package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// HTTPDoer abstracts http.Client.Do for pure mockable testing.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// CompletionRequest describes a streaming chat completion request.
type CompletionRequest struct {
	Mode          string
	Prompt        string
	SourceText    string
	Context       *engine.CapsuleContext
	GlossaryTerms []config.GlossaryTerm
}

// ClientError categorizes AI client failures.
type ClientError struct {
	Kind       string
	StatusCode int
	Message    string
}

func (e *ClientError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
	}
	return e.Message
}

// OpenAICompatibleClient streams completions from an OpenAI-compatible /chat/completions endpoint.
type OpenAICompatibleClient struct {
	Config         config.AIConfig
	GlossaryEngine engine.GlossaryEngine
	Doer           HTTPDoer
	APIKeyProvider func(ctx context.Context) (string, error)
}

// NewOpenAICompatibleClient creates a client using a static API key or provider function.
func NewOpenAICompatibleClient(
	cfg config.AIConfig,
	glossary engine.GlossaryEngine,
	doer HTTPDoer,
	apiKeyProvider func(ctx context.Context) (string, error),
) *OpenAICompatibleClient {
	if doer == nil {
		timeout := time.Duration(cfg.TimeoutSeconds * float64(time.Second))
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		doer = &http.Client{Timeout: timeout}
	}
	return &OpenAICompatibleClient{
		Config:         cfg,
		GlossaryEngine: glossary,
		Doer:           doer,
		APIKeyProvider: apiKeyProvider,
	}
}

// ResolveCompletionsURL normalizes baseURL and appends /chat/completions if absent.
func ResolveCompletionsURL(rawBaseURL string) (string, error) {
	trimmed := strings.TrimSpace(rawBaseURL)
	if trimmed == "" {
		return "", &ClientError{Kind: "invalid_base_url", Message: fmt.Sprintf("Invalid AI base URL: %s", rawBaseURL)}
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", &ClientError{Kind: "invalid_base_url", Message: fmt.Sprintf("Invalid AI base URL: %s", rawBaseURL)}
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Host == "" {
		return "", &ClientError{Kind: "invalid_base_url", Message: fmt.Sprintf("Invalid AI base URL: %s", rawBaseURL)}
	}

	p := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(p, "/chat/completions") {
		p += "/chat/completions"
	}
	u.Path = p
	return u.String(), nil
}

// BuildRequest builds the *http.Request for /chat/completions with glossary injection and Bearer auth.
func (c *OpenAICompatibleClient) BuildRequest(ctx context.Context, req CompletionRequest, apiKey string) (*http.Request, error) {
	trimmedKey := strings.TrimSpace(apiKey)
	if trimmedKey == "" {
		return nil, &ClientError{Kind: "missing_api_key", Message: "Missing API key. Save one in Keychain or set OPENAI_API_KEY."}
	}
	endpoint, err := ResolveCompletionsURL(c.Config.BaseURL)
	if err != nil {
		return nil, err
	}

	activeGlossary := c.GlossaryEngine
	if req.GlossaryTerms != nil {
		activeGlossary = engine.NewGlossaryEngine(req.GlossaryTerms)
	}

	var ctxParts []string
	if req.Context != nil {
		if req.Context.AppName != "" {
			ctxParts = append(ctxParts, req.Context.AppName)
		}
		if req.Context.BundleID != "" {
			ctxParts = append(ctxParts, req.Context.BundleID)
		}
		if req.Context.SurroundingText != "" {
			ctxParts = append(ctxParts, req.Context.SurroundingText)
		}
	}
	contextText := strings.Join(ctxParts, "\n")

	systemPrompt := activeGlossary.InjectIntoSystemPrompt(
		c.Config.SystemPrompt,
		req.Prompt+"\n"+req.SourceText,
		contextText,
		0,
	)

	userPrompt := req.Prompt
	if strings.TrimSpace(req.SourceText) != "" {
		userPrompt = req.Prompt + "\n\n" + req.SourceText
	}

	payload := map[string]any{
		"model":       c.Config.Model,
		"stream":      true,
		"temperature": c.Config.Temperature,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, &ClientError{Kind: "encoding_error", Message: err.Error()}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+trimmedKey)
	return httpReq, nil
}

// StreamCompletion executes the streaming request and invokes onDelta for each text delta.
func (c *OpenAICompatibleClient) StreamCompletion(
	ctx context.Context,
	req CompletionRequest,
	onDelta func(delta string),
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.APIKeyProvider == nil {
		return &ClientError{Kind: "missing_api_key", Message: "Missing API key provider"}
	}
	apiKey, err := c.APIKeyProvider(ctx)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	httpReq, err := c.BuildRequest(ctx, req, apiKey)
	if err != nil {
		return err
	}

	doer := c.Doer
	if doer == nil {
		doer = http.DefaultClient
	}

	resp, err := doer.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
		msg := extractHTTPErrorMessage(errBytes, resp.StatusCode)
		return &ClientError{
			Kind:       "http_error",
			StatusCode: resp.StatusCode,
			Message:    msg,
		}
	}

	var parser SSEStreamParser
	buf := make([]byte, 4096)
	didFinish := false

	for !didFinish {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			events := parser.Feed(buf[:n])
			for _, ev := range events {
				switch ev.Kind {
				case SSEDelta:
					if onDelta != nil {
						onDelta(ev.Text)
					}
				case SSEDone:
					didFinish = true
				case SSEError:
					return &ClientError{Kind: "api_error", Message: ev.Text}
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return readErr
		}
	}

	if !didFinish {
		for _, ev := range parser.Finish() {
			switch ev.Kind {
			case SSEDelta:
				if onDelta != nil {
					onDelta(ev.Text)
				}
			case SSEDone:
				didFinish = true
			case SSEError:
				return &ClientError{Kind: "api_error", Message: ev.Text}
			}
		}
	}

	return ctx.Err()
}

func extractHTTPErrorMessage(data []byte, statusCode int) string {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err == nil {
		if msg := extractErrorMessage(raw); msg != "" {
			return msg
		}
		if msg, ok := raw["message"].(string); ok && msg != "" {
			return msg
		}
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed != "" {
		return trimmed
	}
	return fmt.Sprintf("HTTP status %d", statusCode)
}
