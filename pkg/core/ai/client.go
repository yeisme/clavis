package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/jsonschema"
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
	// ContextPolicy controls ambient context injection through one shared allowlist.
	ContextPolicy   ContextPolicy
	MCPResources    []string
	DeveloperPrompt string
	// OutputSchema and StructuredOutput request validated structured output.
	OutputSchema     map[string]any
	StructuredOutput bool
	// SystemPromptOverride is used by transient workflows such as prompt optimization.
	SystemPromptOverride string
	// Images carries binary image attachments for multimodal Vision profiles.
	Images []ImageAttachment
}

// ClientError categorizes AI client failures.
type ClientError struct {
	Kind       string
	StatusCode int
	Message    string
	cause      error
}

func (e *ClientError) Error() string {
	if e == nil {
		return ""
	}
	message := sanitizeProviderText(e.Message, "")
	if e.StatusCode > 0 {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, message)
	}
	return message
}

func (e *ClientError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

const (
	ErrorCodeOK            = "ok"
	ErrorCodeCancelled     = "cancelled"
	ErrorCodeTimeout       = "timeout"
	ErrorCodeTransport     = "transport_error"
	ErrorCodeMissingAPIKey = "missing_api_key"
)

// ErrorCode returns the stable runtime classification used by provider smoke
// checks and UI diagnostics. It never exposes the underlying error text.
func ErrorCode(err error) string {
	if err == nil {
		return ErrorCodeOK
	}
	var clientErr *ClientError
	if errors.As(err, &clientErr) && clientErr != nil && clientErr.Kind != "" {
		return clientErr.Kind
	}
	if errors.Is(err, context.Canceled) {
		return ErrorCodeCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorCodeTimeout
	}
	return ErrorCodeTransport
}

// SafeErrorMessage returns a bounded provider error message suitable for UI or
// diagnostics. It strips known credential formats and never includes a raw
// response body beyond the bounded, sanitized text retained by ClientError.
func SafeErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return sanitizeProviderText(err.Error(), "")
}

var providerSecretRE = regexp.MustCompile(`(?i)(?:bearer\s+|sk-[a-z0-9_-]{8,}|sk_(?:live|test)_[a-z0-9_-]{8,}|gh[pous]_[a-z0-9_]{8,}|github_pat_[a-z0-9_]{8,}|xox[bp]-[a-z0-9_-]{8,}|(?:api[_-]?key|secret[_-]?key|access[_-]?token|password|passwd|token)\s*[=:]\s*)[^\s"']+`)
var providerQuerySecretRE = regexp.MustCompile(`(?i)([?&](?:api[_-]?key|key|token|access[_-]?token|authorization)=)[^&\s]+`)

func sanitizeProviderText(message, secret string) string {
	message = strings.TrimSpace(message)
	if secret = strings.TrimSpace(secret); len(secret) >= 4 {
		message = strings.ReplaceAll(message, secret, "[redacted]")
	}
	message = providerSecretRE.ReplaceAllString(message, "[redacted]")
	message = providerQuerySecretRE.ReplaceAllString(message, `${1}[redacted]`)
	runes := []rune(message)
	if len(runes) > 512 {
		message = string(runes[:512]) + "…"
	}
	return message
}

func transportError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &ClientError{Kind: ErrorCodeTimeout, Message: "AI request timed out", cause: err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &ClientError{Kind: ErrorCodeTimeout, Message: "AI request timed out", cause: err}
	}
	return &ClientError{Kind: ErrorCodeTransport, Message: "AI provider request failed", cause: err}
}

// OpenAICompatibleClient streams completions from OpenAI-compatible, Anthropic, or Gemini endpoints.
type OpenAICompatibleClient struct {
	Config         config.AIConfig
	GlossaryEngine engine.GlossaryEngine
	Doer           HTTPDoer
	APIKeyProvider func(ctx context.Context) (string, error)
	UsageRecorder  UsageRecorder
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
	u, err := url.Parse(strings.TrimSpace(rawBaseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	local := host == "localhost" || host == "127.0.0.1" || host == "::1"
	// Ollama is keyless only when its endpoint is local; remote endpoints require credentials.
	return local
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
			Kind:    ErrorCodeMissingAPIKey,
			Message: fmt.Sprintf("Missing API key for %s. Save one in Keychain or set %s.", profile.Provider, envHint),
		}
	}

	activeGlossary := c.GlossaryEngine
	if req.GlossaryTerms != nil {
		activeGlossary = engine.NewGlossaryEngine(req.GlossaryTerms)
	}
	if req.ContextPolicy != (ContextPolicy{}) {
		activeGlossary = engine.GlossaryEngine{}
	}
	if err := validateStructuredOutputRequest(profile, req); err != nil {
		return nil, err
	}
	if err := validateVisionRequest(profile, req.Images); err != nil {
		return nil, err
	}

	var contextText string
	// An explicit policy owns every ambient field. The legacy concatenation is
	// deliberately retained only for callers that did not opt into the allowlist.
	if req.ContextPolicy == (ContextPolicy{}) && req.Context != nil {
		var ctxParts []string
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
		contextText = strings.Join(ctxParts, "\n")
	}

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
	// The policy path is additive and opt-in for legacy callers; new callers use it
	// instead of hand-concatenating glossary/app/selection/MCP values.
	if req.ContextPolicy != (ContextPolicy{}) {
		bundle := ContextBundleFromCapsule(req.Context)
		if req.ContextPolicy.IncludeGlossary {
			bundle.Glossary = req.GlossaryTerms
		}
		bundle.MCPResources = append([]string(nil), req.MCPResources...)
		messages := ApplyContextPolicy(req.ContextPolicy, bundle, systemPrompt, req.DeveloperPrompt, "")
		if len(messages.System) > 0 {
			systemPrompt = messages.System[0].Content
		}
	}

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
		messages := make([]map[string]any, 0, len(req.History)+1)
		for _, turn := range req.History {
			role := strings.ToLower(strings.TrimSpace(turn.Role))
			if role == "system" || strings.TrimSpace(turn.Content) == "" {
				continue
			}
			if role != "assistant" {
				role = "user"
			}
			messages = append(messages, map[string]any{
				"role":    role,
				"content": turn.Content,
			})
		}
		messages = append(messages, map[string]any{
			"role":    "user",
			"content": encodeAnthropicUserContent(userPrompt, req.Images),
		})
		if strings.TrimSpace(req.DeveloperPrompt) != "" {
			systemPrompt = strings.TrimSpace(systemPrompt + "\n\n" + req.DeveloperPrompt)
		}
		payload := map[string]any{
			"model":       profile.Model,
			"max_tokens":  4096,
			"stream":      true,
			"temperature": profile.Temperature,
			"system":      systemPrompt,
			"messages":    messages,
		}
		if err := applyStructuredOutput(payload, profile.Protocol, req); err != nil {
			return nil, err
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
			"parts": encodeGeminiUserParts(userPrompt, req.Images),
		})
		payload := map[string]any{
			"contents": contents,
			"generationConfig": map[string]any{
				"temperature": profile.Temperature,
			},
		}
		if err := applyStructuredOutput(payload, profile.Protocol, req); err != nil {
			return nil, err
		}
		if strings.TrimSpace(systemPrompt+req.DeveloperPrompt) != "" {
			instruction := systemPrompt
			if strings.TrimSpace(req.DeveloperPrompt) != "" {
				instruction += "\n\n" + req.DeveloperPrompt
			}
			payload["systemInstruction"] = map[string]any{
				"parts": []map[string]string{{"text": instruction}},
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
		messages := make([]map[string]any, 0, len(req.History)+3)
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": systemPrompt,
		})
		if strings.TrimSpace(req.DeveloperPrompt) != "" {
			messages = append(messages, map[string]any{"role": "developer", "content": req.DeveloperPrompt})
		}
		for _, turn := range req.History {
			if strings.TrimSpace(turn.Content) == "" {
				continue
			}
			role := strings.ToLower(strings.TrimSpace(turn.Role))
			if role != "assistant" && role != "system" {
				role = "user"
			}
			messages = append(messages, map[string]any{
				"role":    role,
				"content": turn.Content,
			})
		}
		messages = append(messages, map[string]any{
			"role":    "user",
			"content": encodeOpenAIUserContent(userPrompt, req.Images),
		})
		payload := map[string]any{
			"model":          profile.Model,
			"stream":         true,
			"stream_options": map[string]any{"include_usage": true},
			"temperature":    profile.Temperature,
			"messages":       messages,
		}
		if err := applyStructuredOutput(payload, profile.Protocol, req); err != nil {
			return nil, err
		}
		// Reasoning models think before the first token; the profile may cap
		// it (DeepSeek V4.1 Flash: seconds with "low", up to a minute and
		// more without).
		if effort := strings.TrimSpace(profile.ReasoningEffort); effort != "" {
			payload["reasoning_effort"] = effort
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
func (c *OpenAICompatibleClient) StreamCompletion(ctx context.Context, req CompletionRequest, onDelta func(delta string)) error {
	_, err := c.StreamCompletionWithUsage(ctx, req, onDelta)
	return err
}

// StreamCompletionWithUsage executes a stream and returns provider-reported
// usage. If the provider omits usage, Reported remains false.
func (c *OpenAICompatibleClient) StreamCompletionWithUsage(
	ctx context.Context,
	req CompletionRequest,
	onDelta func(delta string),
) (ProviderUsage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	startedAt := time.Now().UTC()
	if err := ctx.Err(); err != nil {
		return ProviderUsage{}, transportError(ctx, err)
	}
	profile := c.resolveProfile(req)
	keylessOK := IsKeylessAllowed(profile.Provider, profile.BaseURL)

	var apiKey string
	if c.APIKeyProvider == nil {
		if !keylessOK {
			return ProviderUsage{}, &ClientError{Kind: ErrorCodeMissingAPIKey, Message: "Missing API key provider"}
		}
	} else {
		key, err := c.APIKeyProvider(ctx)
		if err != nil {
			if keylessOK {
				key = ""
			} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return ProviderUsage{}, transportError(ctx, err)
			} else {
				// Do not retain or unwrap the key-store error. Some keychain
				// backends include account material in their diagnostic text.
				return ProviderUsage{}, &ClientError{Kind: ErrorCodeMissingAPIKey, Message: "Missing API key"}
			}
		}
		apiKey = key
	}
	if err := ctx.Err(); err != nil {
		return ProviderUsage{}, transportError(ctx, err)
	}

	httpReq, err := c.BuildRequest(ctx, req, apiKey)
	if err != nil {
		return ProviderUsage{}, err
	}

	doer := c.Doer
	if doer == nil {
		doer = http.DefaultClient
	}

	resp, err := doer.Do(httpReq)
	if err != nil {
		return ProviderUsage{}, transportError(ctx, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
		msg := sanitizeProviderText(extractHTTPErrorMessage(errBytes, resp.StatusCode), apiKey)
		return ProviderUsage{}, &ClientError{
			Kind:       "http_error",
			StatusCode: resp.StatusCode,
			Message:    msg,
		}
	}

	parser := SSEStreamParser{Protocol: profile.Protocol}
	buf := make([]byte, 4096)
	didFinish := false
	var usage ProviderUsage

	for !didFinish {
		if err := ctx.Err(); err != nil {
			return usage, transportError(ctx, err)
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
				case SSEUsage:
					usage = ev.Usage
				case SSEError:
					return usage, &ClientError{Kind: "api_error", Message: sanitizeProviderText(ev.Text, apiKey)}
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return usage, transportError(ctx, readErr)
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
			case SSEUsage:
				usage = ev.Usage
			case SSEError:
				return usage, &ClientError{Kind: "api_error", Message: sanitizeProviderText(ev.Text, apiKey)}
			}
		}
	}
	if !didFinish {
		if err := ctx.Err(); err != nil {
			return usage, transportError(ctx, err)
		}
		// A clean transport EOF is not a completed streaming response. Treating
		// truncated output as success lets callers persist and automate against
		// an incomplete answer, and also reports misleading usage. Providers must
		// emit [DONE] or their protocol-specific finish event.
		return usage, &ClientError{Kind: "stream_incomplete", Message: "AI stream ended before completion"}
	}

	if c.UsageRecorder != nil {
		profileName := profile.ID
		if profileName == "" {
			profileName = profile.Provider
		}
		c.UsageRecorder.RecordAIUsage(UsageRecord{At: startedAt, Profile: profileName, Provider: profile.Provider, Model: profile.Model, Action: req.Mode, Usage: usage, Latency: time.Since(startedAt)})
	}
	// Once the provider emitted its terminal event the response is complete.
	// A cancellation racing with that terminal event must not turn a confirmed
	// completion into a false cancellation result.
	return usage, nil
}

func structuredOutputRequested(req CompletionRequest) bool {
	return req.StructuredOutput || req.OutputSchema != nil
}

func validateStructuredOutputRequest(profile config.AIProfile, req CompletionRequest) error {
	if !structuredOutputRequested(req) {
		return nil
	}
	if !profile.Capabilities.StructuredOutput {
		name := profile.ID
		if name == "" {
			name = profile.Provider
		}
		return &ClientError{Kind: "structured_output_unsupported", Message: fmt.Sprintf("profile %s does not declare structured output support", name)}
	}
	if req.OutputSchema == nil {
		return &ClientError{Kind: "structured_output_invalid_schema", Message: "structured output requires an output schema"}
	}
	if err := jsonschema.ValidateDefinition(req.OutputSchema); err != nil {
		return &ClientError{Kind: "structured_output_invalid_schema", Message: err.Error()}
	}
	return nil
}

func applyStructuredOutput(payload map[string]any, protocol config.AIProtocol, req CompletionRequest) error {
	if !structuredOutputRequested(req) {
		return nil
	}
	switch protocol {
	case config.ProtocolAnthropic:
		payload["output_format"] = map[string]any{"type": "json_schema", "schema": req.OutputSchema}
	case config.ProtocolGemini:
		generation, _ := payload["generationConfig"].(map[string]any)
		if generation == nil {
			generation = map[string]any{}
			payload["generationConfig"] = generation
		}
		generation["responseMimeType"] = "application/json"
		generation["responseSchema"] = req.OutputSchema
	case config.ProtocolOpenAICompatible, "":
		payload["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name": "clavis_output", "schema": req.OutputSchema, "strict": true,
			},
		}
	default:
		return &ClientError{Kind: "structured_output_unsupported", Message: fmt.Sprintf("protocol %s does not support structured output", protocol)}
	}
	return nil
}

func extractHTTPErrorMessage(data []byte, statusCode int) string {
	raw := map[string]any{}
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
