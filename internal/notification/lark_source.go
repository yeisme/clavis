package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeisme/clavis/pkg/core/host"
	corenotif "github.com/yeisme/clavis/pkg/core/notification"
)

const (
	defaultLarkBaseURL          = "https://open.feishu.cn"
	defaultLarkEventsPath       = "/open-apis/im/v1/messages"
	defaultLarkContainerIDType  = "chat"
	defaultLarkRequestTimeout   = 5 * time.Second
	defaultLarkMaxResponseBytes = 1 << 20 // 1 MiB
	larkCursorPrefix            = "lark:v1:"
)

// HTTPDoer abstracts http.Client.Do for pure, deterministic testing of LarkSource.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// LarkSourceConfig configures the strictly read-only Lark/Feishu event source.
type LarkSourceConfig struct {
	// BaseURL defaults to https://open.feishu.cn.
	BaseURL string
	// EventsPath must be a strictly read-only history/list endpoint (default /open-apis/im/v1/messages).
	EventsPath string
	// ContainerID optionally scopes read-only message listing to a chat/thread container.
	ContainerID string
	// ContainerIDType defaults to "chat" when ContainerID is non-empty.
	ContainerIDType string
	// Doer overrides HTTP execution for unit tests.
	Doer HTTPDoer
	// TokenProvider returns a read-only bearer token. Returned tokens are NEVER logged or stored.
	TokenProvider func(ctx context.Context) (string, error)
	// RequestTimeout bounds a single read-only HTTP request. Defaults to 5s.
	RequestTimeout time.Duration
	// MaxResponseBytes caps response body reads. Defaults to 1 MiB.
	MaxResponseBytes int64
	// Now overrides time.Now for deterministic timestamps.
	Now func() time.Time
}

// LarkSource reads Lark/Feishu events using strictly read-only HTTP GET calls.
// It never sends messages, replies, recalls, approves, updates tasks, or mutates remote state.
type LarkSource struct {
	mu               sync.RWMutex
	baseURL          string
	eventsPath       string
	containerID      string
	containerIDType  string
	doer             HTTPDoer
	tokenProvider    func(ctx context.Context) (string, error)
	requestTimeout   time.Duration
	maxResponseBytes int64
	nowFn            func() time.Time
	lastOp           host.OperationResult
}

var (
	_ corenotif.NotificationSource = (*LarkSource)(nil)
	_ corenotif.CapabilitySource   = (*LarkSource)(nil)
)

// NewLarkSource constructs a read-only Lark notification source.
func NewLarkSource(cfg LarkSourceConfig) *LarkSource {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultLarkBaseURL
	}
	eventsPath := strings.TrimSpace(cfg.EventsPath)
	if eventsPath == "" {
		eventsPath = defaultLarkEventsPath
	}
	containerType := strings.TrimSpace(cfg.ContainerIDType)
	if containerType == "" {
		containerType = defaultLarkContainerIDType
	}
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = defaultLarkRequestTimeout
	}
	maxBytes := cfg.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = defaultLarkMaxResponseBytes
	}
	doer := cfg.Doer
	if doer == nil {
		doer = &http.Client{Timeout: timeout}
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &LarkSource{
		baseURL:          baseURL,
		eventsPath:       eventsPath,
		containerID:      strings.TrimSpace(cfg.ContainerID),
		containerIDType:  containerType,
		doer:             doer,
		tokenProvider:    cfg.TokenProvider,
		requestTimeout:   timeout,
		maxResponseBytes: maxBytes,
		nowFn:            nowFn,
	}
}

func (s *LarkSource) now() time.Time { return s.nowFn().UTC() }

func (s *LarkSource) setLastOp(op host.OperationResult) {
	s.mu.Lock()
	s.lastOp = op
	s.mu.Unlock()
}

// LastOperation returns the most recent structured, secret-free diagnostic result.
func (s *LarkSource) LastOperation() host.OperationResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastOp
}

// Name returns the canonical allowlisted source name "lark".
func (s *LarkSource) Name() string { return corenotif.SourceLark }

func validateReadOnlyLarkConfig(baseURL, eventsPath string, now time.Time) (*url.URL, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, corenotif.NewSourceError(
			corenotif.ErrSourceUnavailable,
			host.StateFailed,
			"invalid_endpoint",
			"lark base url is invalid",
			false,
			now,
		)
	}
	lowerPath := strings.ToLower(strings.TrimSpace(eventsPath))
	if lowerPath == "" || !strings.HasPrefix(lowerPath, "/") {
		return nil, corenotif.NewSourceError(
			corenotif.ErrReadOnlyViolation,
			host.StateFailed,
			"read_only_violation",
			"lark events path must be a relative read-only api path",
			false,
			now,
		)
	}
	forbiddenPathParts := []string{
		"/send", "/reply", "/recall", "/delete", "/remove",
		"/update", "/patch", "/approve", "/reject", "/stop",
		"/control", "/reactions", "/urgent", "/forward", "/merge_forward",
	}
	for _, bad := range forbiddenPathParts {
		if strings.Contains(lowerPath, bad) {
			return nil, corenotif.NewSourceError(
				corenotif.ErrReadOnlyViolation,
				host.StateFailed,
				"read_only_violation",
				"lark write or mutating endpoint is blocked by read-only policy",
				false,
				now,
			)
		}
	}
	resolved := *u
	resolved.Path = strings.TrimRight(u.Path, "/") + eventsPath
	resolved.RawQuery = ""
	resolved.Fragment = ""
	return &resolved, nil
}

// Capabilities reports Lark read-only capabilities after validating endpoint and auth availability.
func (s *LarkSource) Capabilities(ctx context.Context) (corenotif.SourceCapabilities, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := s.now()
	avail, err := s.Available(ctx)
	if err != nil {
		return corenotif.SourceCapabilities{ObservedAt: now}, err
	}
	if !avail {
		return corenotif.SourceCapabilities{ObservedAt: now}, nil
	}
	return corenotif.SourceCapabilities{
		ReadEvents:    true,
		StableEventID: true,
		Cursor:        true,
		History:       true,
		ObservedAt:    now,
	}, nil
}

// Available validates read-only endpoint policy and non-empty auth token availability.
func (s *LarkSource) Available(ctx context.Context) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := s.now()
	if err := ctx.Err(); err != nil {
		op := corenotif.OperationFromError(err, now)
		s.setLastOp(op)
		return false, err
	}
	if _, err := validateReadOnlyLarkConfig(s.baseURL, s.eventsPath, now); err != nil {
		s.setLastOp(corenotif.OperationFromError(err, now))
		return false, err
	}
	if s.tokenProvider == nil {
		s.setLastOp(host.OperationResult{
			State:      host.StateDegraded,
			Code:       "auth_unconfigured",
			Message:    "lark read-only credentials are not configured",
			ObservedAt: now,
			Retryable:  false,
		})
		return false, nil
	}
	token, err := s.tokenProvider(ctx)
	if err != nil {
		srcErr := corenotif.NewSourceError(
			corenotif.ErrPermissionDenied,
			host.StateFailed,
			"permission_denied",
			"lark read-only credentials unavailable",
			false,
			now,
		)
		s.setLastOp(srcErr.Operation)
		return false, srcErr
	}
	if strings.TrimSpace(token) == "" {
		s.setLastOp(host.OperationResult{
			State:      host.StateDegraded,
			Code:       "auth_unconfigured",
			Message:    "lark read-only token is empty",
			ObservedAt: now,
			Retryable:  false,
		})
		return false, nil
	}
	s.setLastOp(host.OperationResult{
		State:      host.StateReady,
		Code:       "ok",
		Message:    "lark read-only source available",
		ObservedAt: now,
	})
	return true, nil
}

// Read performs a single strictly read-only HTTP GET page fetch from Lark.
func (s *LarkSource) Read(ctx context.Context, cursor string, limit int) (corenotif.NotificationPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := s.now()
	if err := ctx.Err(); err != nil {
		op := corenotif.OperationFromError(err, now)
		s.setLastOp(op)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, err
	}
	if limit <= 0 {
		limit = corenotif.DefaultPageLimit
	}
	if limit > corenotif.MaxPageLimit {
		limit = corenotif.MaxPageLimit
	}
	if err := corenotif.ValidateCursor(cursor); err != nil {
		srcErr := corenotif.NewSourceError(err, host.StateFailed, "invalid_cursor", "lark cursor is invalid", false, now)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
	}

	pageToken, err := parseLarkCursor(cursor)
	if err != nil {
		srcErr := corenotif.NewSourceError(err, host.StateFailed, "invalid_cursor", "lark cursor format is invalid", false, now)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
	}

	endpoint, err := validateReadOnlyLarkConfig(s.baseURL, s.eventsPath, now)
	if err != nil {
		s.setLastOp(corenotif.OperationFromError(err, now))
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, err
	}

	var token string
	if s.tokenProvider != nil {
		tok, tokErr := s.tokenProvider(ctx)
		if tokErr != nil {
			srcErr := corenotif.NewSourceError(
				corenotif.ErrPermissionDenied,
				host.StateFailed,
				"permission_denied",
				"lark read-only credentials unavailable",
				false,
				now,
			)
			s.setLastOp(srcErr.Operation)
			return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
		}
		token = strings.TrimSpace(tok)
	}

	q := endpoint.Query()
	q.Set("page_size", strconv.Itoa(limit))
	if s.containerID != "" {
		q.Set("container_id_type", s.containerIDType)
		q.Set("container_id", s.containerID)
	}
	if pageToken != "" {
		q.Set("page_token", pageToken)
	}
	endpoint.RawQuery = q.Encode()

	reqCtx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()

	// Hard read-only guarantee: HTTP GET only, nil request body.
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		srcErr := corenotif.NewSourceError(corenotif.ErrSourceUnavailable, host.StateFailed, "request_build_failed", "failed to construct lark read request", false, now)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := s.doer.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			op := corenotif.OperationFromError(err, now)
			s.setLastOp(op)
			return corenotif.NotificationPage{Source: corenotif.SourceLark}, err
		}
		srcErr := corenotif.NewSourceError(
			corenotif.ErrSourceUnavailable,
			host.StateDegraded,
			"network_error",
			"lark read endpoint unreachable",
			true,
			now,
		)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, s.maxResponseBytes+1))
	if readErr != nil {
		srcErr := corenotif.NewSourceError(
			corenotif.ErrSourceUnavailable,
			host.StateDegraded,
			"read_failed",
			"lark response stream interrupted",
			true,
			now,
		)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
	}
	if int64(len(body)) > s.maxResponseBytes {
		srcErr := corenotif.NewSourceError(
			corenotif.ErrSchemaDrift,
			host.StateDegraded,
			"schema_drift",
			"lark response exceeded safe size limit",
			false,
			now,
		)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		srcErr := corenotif.NewSourceError(
			corenotif.ErrPermissionDenied,
			host.StateFailed,
			"permission_denied",
			"lark read permission denied",
			false,
			now,
		)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
	}
	if resp.StatusCode == http.StatusGone {
		srcErr := corenotif.NewSourceError(
			corenotif.ErrCursorExpired,
			host.StateDegraded,
			"cursor_expired",
			"lark pagination cursor expired",
			true,
			now,
		)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		srcErr := corenotif.NewSourceError(
			corenotif.ErrSourceUnavailable,
			host.StateDegraded,
			"source_unavailable",
			fmt.Sprintf("lark read endpoint returned status %d", resp.StatusCode),
			true,
			now,
		)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		srcErr := corenotif.NewSourceError(
			corenotif.ErrSchemaDrift,
			host.StateDegraded,
			"http_error",
			fmt.Sprintf("lark read endpoint rejected request with status %d", resp.StatusCode),
			false,
			now,
		)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
	}

	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil || envelope == nil {
		srcErr := corenotif.NewSourceError(
			corenotif.ErrSchemaDrift,
			host.StateDegraded,
			"schema_drift",
			"lark response is not a valid json object",
			false,
			now,
		)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
	}

	rawCode, hasCode := envelope["code"]
	if !hasCode {
		srcErr := corenotif.NewSourceError(
			corenotif.ErrSchemaDrift,
			host.StateDegraded,
			"schema_drift",
			"lark response missing status code field",
			false,
			now,
		)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
	}
	apiCode, ok := jsonNumberToInt(rawCode)
	if !ok {
		srcErr := corenotif.NewSourceError(
			corenotif.ErrSchemaDrift,
			host.StateDegraded,
			"schema_drift",
			"lark response status code has unexpected type",
			false,
			now,
		)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
	}
	if apiCode != 0 {
		srcErr := classifyLarkAPICode(apiCode, now)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
	}

	dataObj, ok := envelope["data"].(map[string]any)
	if !ok || dataObj == nil {
		srcErr := corenotif.NewSourceError(
			corenotif.ErrSchemaDrift,
			host.StateDegraded,
			"schema_drift",
			"lark response missing data object",
			false,
			now,
		)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
	}

	var rawItems []any
	if itemsField, exists := dataObj["items"]; exists && itemsField != nil {
		arr, ok := itemsField.([]any)
		if !ok {
			srcErr := corenotif.NewSourceError(
				corenotif.ErrSchemaDrift,
				host.StateDegraded,
				"schema_drift",
				"lark response items field is not a list",
				false,
				now,
			)
			s.setLastOp(srcErr.Operation)
			return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
		}
		rawItems = arr
	}

	nextToken := strings.TrimSpace(firstStringField(dataObj, "page_token", "next_cursor"))
	nextCursor := cursor
	if nextToken != "" {
		candidate := larkCursorPrefix + nextToken
		if err := corenotif.ValidateCursor(candidate); err != nil {
			srcErr := corenotif.NewSourceError(corenotif.ErrSchemaDrift, host.StateDegraded, "schema_drift", "lark returned an invalid pagination token", false, now)
			s.setLastOp(srcErr.Operation)
			return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
		}
		nextCursor = candidate
	}
	hasMore, _ := dataObj["has_more"].(bool)
	hasMore = hasMore && nextToken != ""

	events := make([]corenotif.NotificationEvent, 0, len(rawItems))
	for _, rawItem := range rawItems {
		item, ok := rawItem.(map[string]any)
		if !ok || item == nil {
			srcErr := corenotif.NewSourceError(
				corenotif.ErrSchemaDrift,
				host.StateDegraded,
				"schema_drift",
				"lark event item is not a json object",
				false,
				now,
			)
			s.setLastOp(srcErr.Operation)
			return corenotif.NotificationPage{Source: corenotif.SourceLark}, srcErr
		}
		ev, err := buildLarkEvent(item, nextCursor, now)
		if err != nil {
			s.setLastOp(corenotif.OperationFromError(err, now))
			return corenotif.NotificationPage{Source: corenotif.SourceLark}, err
		}
		events = append(events, ev)
		if len(events) >= limit {
			break
		}
	}

	s.setLastOp(host.OperationResult{
		State:      host.StateReady,
		Code:       "ok",
		Message:    "lark read completed",
		ObservedAt: now,
	})
	return corenotif.NotificationPage{
		Source:     corenotif.SourceLark,
		Events:     events,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func parseLarkCursor(cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	if !strings.HasPrefix(cursor, larkCursorPrefix) {
		return "", corenotif.ErrInvalidCursor
	}
	tok := strings.TrimPrefix(cursor, larkCursorPrefix)
	if tok == "" {
		return "", corenotif.ErrInvalidCursor
	}
	return tok, nil
}

func classifyLarkAPICode(code int, now time.Time) *corenotif.SourceError {
	switch code {
	case 99991661, 99991663, 99991668, 99991671, 99991672, 230027:
		return corenotif.NewSourceError(
			corenotif.ErrPermissionDenied,
			host.StateFailed,
			"permission_denied",
			fmt.Sprintf("lark api denied read access (code %d)", code),
			false,
			now,
		)
	case 230011, 230020, 99992402:
		return corenotif.NewSourceError(
			corenotif.ErrCursorExpired,
			host.StateDegraded,
			"cursor_expired",
			fmt.Sprintf("lark pagination cursor expired (code %d)", code),
			true,
			now,
		)
	default:
		return corenotif.NewSourceError(
			corenotif.ErrSourceUnavailable,
			host.StateDegraded,
			"lark_api_error",
			fmt.Sprintf("lark read api returned error code %d", code),
			true,
			now,
		)
	}
}

func buildLarkEvent(item map[string]any, pageCursor string, now time.Time) (corenotif.NotificationEvent, error) {
	// Strict field allowlist: attachments, raw payloads, and unknown keys are discarded.
	eventID := firstStringField(item, "event_id", "message_id", "id")
	convRef := firstStringField(item, "chat_id", "conversation_id", "thread_id", "root_id")
	senderRef := extractLarkSenderRef(item)
	kind := firstStringField(item, "msg_type", "event_type", "kind")
	preview := extractLarkPreview(item)
	// SessionID is populated ONLY from an explicit metadata field — never guessed from text.
	sessionID := firstStringField(item, "session_id", "sessionId")
	at := firstTimeField(item, "create_time", "update_time", "timestamp", "at")

	if eventID == "" && convRef == "" && kind == "" && preview == "" {
		return corenotif.NotificationEvent{}, corenotif.NewSourceError(
			corenotif.ErrSchemaDrift,
			host.StateDegraded,
			"schema_drift",
			"lark event item contains no recognized notification fields",
			false,
			now,
		)
	}

	ev := corenotif.NotificationEvent{
		Source:          corenotif.SourceLark,
		EventID:         eventID,
		ConversationRef: convRef,
		SenderRef:       senderRef,
		Kind:            kind,
		TextPreview:     preview,
		SessionID:       sessionID,
		At:              at,
		SourceCursor:    pageCursor,
		ObservedAt:      now,
	}
	norm, _, err := corenotif.NormalizeEvent(ev, now)
	if err != nil {
		return corenotif.NotificationEvent{}, err
	}
	return norm, nil
}

func extractLarkSenderRef(item map[string]any) string {
	if sender, ok := item["sender"].(map[string]any); ok && sender != nil {
		if id := firstStringField(sender, "id", "open_id", "union_id", "sender_id"); id != "" {
			return id
		}
		if nested, ok := sender["sender_id"].(map[string]any); ok && nested != nil {
			if id := firstStringField(nested, "open_id", "union_id", "user_id"); id != "" {
				return id
			}
		}
	}
	return firstStringField(item, "sender_id", "sender_ref")
}

func extractLarkPreview(item map[string]any) string {
	if direct := firstStringField(item, "text_preview", "preview"); direct != "" {
		return direct
	}
	bodyObj, ok := item["body"].(map[string]any)
	if !ok || bodyObj == nil {
		return firstStringField(item, "text", "summary")
	}
	content, _ := bodyObj["content"].(string)
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	// Lark text messages encode JSON {"text":"..."} inside body.content.
	// Extract only short text fields; never retain binary/file/image attachment blobs.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err == nil && parsed != nil {
		if txt := firstStringField(parsed, "text", "title"); txt != "" {
			return txt
		}
		// Non-text rich card/attachment payload: return safe placeholder only.
		msgType := strings.ToLower(firstStringField(item, "msg_type", "kind"))
		if msgType != "" {
			return "[" + msgType + "]"
		}
		return "[attachment]"
	}
	return content
}
