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

// ChatMessage represents a single conversation turn in multi-turn AI sessions.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CompletionRequest describes a streaming chat completion request.
type CompletionRequest struct {
	Mode          string
	Prompt        string
	SourceText    string
	Context       *engine.CapsuleContext
	GlossaryTerms []config.GlossaryTerm
	History       []ChatMessage
	Profile       *config.AIProfile
	// SystemPromptOverride is used by transient workflows such as prompt optimization.
	SystemPromptOverride string
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

// OpenAICompatibleClient streams completions from OpenAI-compatible, Anthropic, or Gemini endpoints.
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
		active := cfg.ActiveProfile()
		timeout := time.Duration(active.TimeoutSeconds * float64(time.Second))
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

// IsKeylessAllowed reports whether an empty API key is permitted for the given provider and baseURL
// (e.g. local Ollama or localhost / 127.0.0.1 / ::1 endpoints).
func IsKeylessAllowed(provider string, rawBaseURL string) bool {
	if strings.EqualFold(strings.TrimSpace(provider), "ollama") {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(rawBaseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func parseAndValidateBaseURL(rawBaseURL string) (*url.URL, error) {
	trimmed := strings.TrimSpace(rawBaseURL)
	if trimmed == "" {
		return nil, &ClientError{Kind: "invalid_base_url", Message: fmt.Sprintf("Invalid AI base URL: %s", rawBaseURL)}
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, &ClientError{Kind: "invalid_base_url", Message: fmt.Sprintf("Invalid AI base URL: %s", rawBaseURL)}
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Host == "" {
		return nil, &ClientError{Kind: "invalid_base_url", Message: fmt.Sprintf("Invalid AI base URL: %s", rawBaseURL)}
	}
	return u, nil
}

// ResolveCompletionsURL normalizes baseURL and appends /chat/completions if absent.
func ResolveCompletionsURL(rawBaseURL string) (string, error) {
	u, err := parseAndValidateBaseURL(rawBaseURL)
	if err != nil {
		return "", err
	}
	p := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(p, "/chat/completions") {
		p += "/chat/completions"
	}
	u.Path = p
	return u.String(), nil
}

// ResolveAnthropicMessagesURL normalizes baseURL and appends /messages if absent.
func ResolveAnthropicMessagesURL(rawBaseURL string) (string, error) {
	u, err := parseAndValidateBaseURL(rawBaseURL)
	if err != nil {
		return "", err
	}
	p := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(p, "/messages") {
		if p == "" {
			p = "/v1/messages"
		} else {
			p += "/messages"
		}
	}
	u.Path = p
	return u.String(), nil
}

// ResolveGeminiStreamURL normalizes baseURL and constructs the /models/<model>:streamGenerateContent?alt=sse endpoint.
func ResolveGeminiStreamURL(rawBaseURL string, model string) (string, error) {
	u, err := parseAndValidateBaseURL(rawBaseURL)
	if err != nil {
		return "", err
	}
	trimmedModel := strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if trimmedModel == "" {
		trimmedModel = "gemini-2.5-flash"
	}
	p := strings.TrimRight(u.Path, "/")
	if !strings.Contains(p, ":streamGenerateContent") {
		if p == "" {
			p = "/v1beta"
		}
		p += "/models/" + trimmedModel + ":streamGenerateContent"
	}
	u.Path = p
	q := u.Query()
	if q.Get("alt") == "" {
		q.Set("alt", "sse")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *OpenAICompatibleClient) resolveProfile(req CompletionRequest) config.AIProfile {
	if req.Profile != nil {
		p := *req.Profile
		if p.Protocol == "" {
			p.Protocol = config.ProtocolOpenAICompatible
		}
		return p
	}
	return c.Config.ActiveProfile()
}

// BuildRequest builds the *http.Request for the active AI protocol with glossary injection,
// multi-turn history, and appropriate authentication headers.
func (c *OpenAICompatibleClient) BuildRequest(ctx context.Context, req CompletionRequest, apiKey string) (*http.Request, error) {
	profile := c.resolveProfile(req)
	trimmedKey := strings.TrimSpace(apiKey)
	if trimmedKey == "" && !IsKeylessAllowed(profile.Provider, profile.BaseURL) {
		envHint := profile.APIKeyEnvVar
		if envHint == "" {
			envHint = "DEEPSEEK_API_KEY"
		}
		return nil, &ClientError{
			Kind:    "missing_api_key",
			Message: fmt.Sprintf("Missing API key for %s. Save one in Keychain or set %s.", profile.Provider, envHint),
		}
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
		if req.Context.WindowTitle != "" {
			ctxParts = append(ctxParts, req.Context.WindowTitle)
		}
		if req.Context.ActiveURL != "" {
			ctxParts = append(ctxParts, req.Context.ActiveURL)
		}
		if req.Context.SurroundingText != "" {
			ctxParts = append(ctxParts, req.Context.SurroundingText)
		}
	}
	contextText := strings.Join(ctxParts, "\n")

	systemBase := profile.SystemPrompt
	if strings.TrimSpace(req.SystemPromptOverride) != "" {
		systemBase = req.SystemPromptOverride
	}
	systemPrompt := activeGlossary.InjectIntoSystemPrompt(
		systemBase,
		req.Prompt+"\n"+req.SourceText,
		contextText,
		0,
	)

	userPrompt := req.Prompt
	if len(req.History) == 0 && strings.TrimSpace(req.SourceText) != "" {
		userPrompt = req.Prompt + "\n\n" + req.SourceText
	}

	switch profile.Protocol {
	case config.ProtocolAnthropic:
		endpoint, err := ResolveAnthropicMessagesURL(profile.BaseURL)
		if err != nil {
			return nil, err
		}
		messages := make([]map[string]string, 0, len(req.History)+1)
		for _, turn := range req.History {
			role := strings.ToLower(strings.TrimSpace(turn.Role))
			if role == "system" || strings.TrimSpace(turn.Content) == "" {
				continue
			}
			if role != "assistant" {
				role = "user"
			}
			messages = append(messages, map[string]string{
				"role":    role,
				"content": turn.Content,
			})
		}
		messages = append(messages, map[string]string{
			"role":    "user",
			"content": userPrompt,
		})
		payload := map[string]any{
			"model":       profile.Model,
			"max_tokens":  4096,
			"stream":      true,
			"temperature": profile.Temperature,
			"system":      systemPrompt,
			"messages":    messages,
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
		httpReq.Header.Set("anthropic-version", "2023-06-01")
		if trimmedKey != "" {
			httpReq.Header.Set("x-api-key", trimmedKey)
		}
		return httpReq, nil

	case config.ProtocolGemini:
		endpoint, err := ResolveGeminiStreamURL(profile.BaseURL, profile.Model)
		if err != nil {
			return nil, err
		}
		contents := make([]map[string]any, 0, len(req.History)+1)
		for _, turn := range req.History {
			role := strings.ToLower(strings.TrimSpace(turn.Role))
			if role == "system" || strings.TrimSpace(turn.Content) == "" {
				continue
			}
			geminiRole := "user"
			if role == "assistant" || role == "model" {
				geminiRole = "model"
			}
			contents = append(contents, map[string]any{
				"role":  geminiRole,
				"parts": []map[string]string{{"text": turn.Content}},
			})
		}
		contents = append(contents, map[string]any{
			"role":  "user",
			"parts": []map[string]string{{"text": userPrompt}},
		})
		payload := map[string]any{
			"contents": contents,
			"generationConfig": map[string]any{
				"temperature": profile.Temperature,
			},
		}
		if strings.TrimSpace(systemPrompt) != "" {
			payload["systemInstruction"] = map[string]any{
				"parts": []map[string]string{{"text": systemPrompt}},
			}
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
		if trimmedKey != "" {
			httpReq.Header.Set("x-goog-api-key", trimmedKey)
		}
		return httpReq, nil

	default:
		endpoint, err := ResolveCompletionsURL(profile.BaseURL)
		if err != nil {
			return nil, err
		}
		messages := make([]map[string]string, 0, len(req.History)+2)
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": systemPrompt,
		})
		for _, turn := range req.History {
			if strings.TrimSpace(turn.Content) == "" {
				continue
			}
			role := strings.ToLower(strings.TrimSpace(turn.Role))
			if role != "assistant" && role != "system" {
				role = "user"
			}
			messages = append(messages, map[string]string{
				"role":    role,
				"content": turn.Content,
			})
		}
		messages = append(messages, map[string]string{
			"role":    "user",
			"content": userPrompt,
		})
		payload := map[string]any{
			"model":       profile.Model,
			"stream":      true,
			"temperature": profile.Temperature,
			"messages":    messages,
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
		if trimmedKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+trimmedKey)
		}
		return httpReq, nil
	}
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
	profile := c.resolveProfile(req)
	keylessOK := IsKeylessAllowed(profile.Provider, profile.BaseURL)

	var apiKey string
	if c.APIKeyProvider == nil {
		if !keylessOK {
			return &ClientError{Kind: "missing_api_key", Message: "Missing API key provider"}
		}
	} else {
		key, err := c.APIKeyProvider(ctx)
		if err != nil && !keylessOK {
			return err
		}
		apiKey = key
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

	parser := SSEStreamParser{Protocol: profile.Protocol}
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
