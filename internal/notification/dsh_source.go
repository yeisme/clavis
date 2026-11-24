package notification

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeisme/clavis/pkg/core/host"
	corenotif "github.com/yeisme/clavis/pkg/core/notification"
)

const (
	dshCursorPrefix        = "dsh:v1:"
	dshSupportedSchemaVer  = 1
	defaultDSHMaxFileBytes = 4 << 20 // 4 MiB per log file
)

// DSHFileReader is a strictly read-only filesystem hook for testing DSHSource
// without touching live host directories or invoking external commands.
type DSHFileReader interface {
	Stat(path string) (fs.FileInfo, error)
	ReadDir(dir string) ([]fs.DirEntry, error)
	ReadFile(path string) ([]byte, error)
}

type osDSHFileReader struct{}

func (osDSHFileReader) Stat(path string) (fs.FileInfo, error)        { return os.Stat(path) }
func (osDSHFileReader) ReadDir(dir string) ([]fs.DirEntry, error)    { return os.ReadDir(dir) }
func (osDSHFileReader) ReadFile(path string) ([]byte, error)         { return os.ReadFile(path) }

// DSHSourceConfig configures the strictly read-only DSH notification source.
type DSHSourceConfig struct {
	// EventsDir points to a local read-only directory containing DSH .jsonl or .json event files.
	EventsDir string
	// Reader overrides filesystem reads for unit tests. Defaults to read-only os helpers.
	Reader DSHFileReader
	// MaxFileBytes bounds per-file reads to avoid loading unbounded transcripts. Defaults to 4 MiB.
	MaxFileBytes int64
	// Now overrides time.Now for deterministic timestamps.
	Now func() time.Time
}

// DSHSource reads local DSH notification logs strictly read-only.
// It never invokes shell commands, never sends messages, and never stops sessions.
type DSHSource struct {
	mu           sync.RWMutex
	eventsDir    string
	reader       DSHFileReader
	maxFileBytes int64
	nowFn        func() time.Time
	lastOp       host.OperationResult
}

var (
	_ corenotif.NotificationSource = (*DSHSource)(nil)
	_ corenotif.CapabilitySource   = (*DSHSource)(nil)
)

// NewDSHSource constructs a read-only DSH notification source.
func NewDSHSource(cfg DSHSourceConfig) *DSHSource {
	reader := cfg.Reader
	if reader == nil {
		reader = osDSHFileReader{}
	}
	maxBytes := cfg.MaxFileBytes
	if maxBytes <= 0 {
		maxBytes = defaultDSHMaxFileBytes
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &DSHSource{
		eventsDir:    strings.TrimSpace(cfg.EventsDir),
		reader:       reader,
		maxFileBytes: maxBytes,
		nowFn:        nowFn,
	}
}

func (s *DSHSource) now() time.Time { return s.nowFn().UTC() }

func (s *DSHSource) setLastOp(op host.OperationResult) {
	s.mu.Lock()
	s.lastOp = op
	s.mu.Unlock()
}

// LastOperation returns the most recent structured, secret-free diagnostic result.
func (s *DSHSource) LastOperation() host.OperationResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastOp
}

// Name returns the canonical allowlisted source name "dsh".
func (s *DSHSource) Name() string { return corenotif.SourceDSH }

// Capabilities probes read-only DSH directory access and reports supported capabilities.
func (s *DSHSource) Capabilities(ctx context.Context) (corenotif.SourceCapabilities, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := s.now()
	if err := ctx.Err(); err != nil {
		op := corenotif.OperationFromError(err, now)
		s.setLastOp(op)
		return corenotif.SourceCapabilities{ObservedAt: now}, err
	}
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

// Available reports whether the configured DSH event directory is readable.
func (s *DSHSource) Available(ctx context.Context) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := s.now()
	if err := ctx.Err(); err != nil {
		op := corenotif.OperationFromError(err, now)
		s.setLastOp(op)
		return false, err
	}
	if s.eventsDir == "" {
		op := host.OperationResult{
			State:      host.StateDegraded,
			Code:       "source_unconfigured",
			Message:    "dsh event directory is not configured",
			ObservedAt: now,
			Retryable:  false,
		}
		s.setLastOp(op)
		return false, nil
	}
	info, err := s.reader.Stat(s.eventsDir)
	if err != nil {
		return false, s.classifyFSError(err, now)
	}
	if !info.IsDir() {
		srcErr := corenotif.NewSourceError(
			corenotif.ErrSchemaDrift,
			host.StateDegraded,
			"schema_drift",
			"dsh event path is not a directory",
			false,
			now,
		)
		s.setLastOp(srcErr.Operation)
		return false, srcErr
	}
	if _, err := s.reader.ReadDir(s.eventsDir); err != nil {
		return false, s.classifyFSError(err, now)
	}
	s.setLastOp(host.OperationResult{
		State:      host.StateReady,
		Code:       "ok",
		Message:    "dsh read-only source available",
		ObservedAt: now,
	})
	return true, nil
}

func (s *DSHSource) classifyFSError(err error, now time.Time) error {
	if errors.Is(err, fs.ErrPermission) {
		srcErr := corenotif.NewSourceError(
			corenotif.ErrPermissionDenied,
			host.StateFailed,
			"permission_denied",
			"dsh event directory permission denied",
			false,
			now,
		)
		s.setLastOp(srcErr.Operation)
		return srcErr
	}
	if errors.Is(err, fs.ErrNotExist) {
		s.setLastOp(host.OperationResult{
			State:      host.StateDegraded,
			Code:       "source_unavailable",
			Message:    "dsh event directory not found",
			ObservedAt: now,
			Retryable:  true,
		})
		return nil
	}
	srcErr := corenotif.NewSourceError(
		corenotif.ErrSourceUnavailable,
		host.StateDegraded,
		"read_failed",
		"dsh event directory could not be read",
		true,
		now,
	)
	s.setLastOp(srcErr.Operation)
	return srcErr
}

// Read pulls at most limit normalized DSH notification events after cursor.
func (s *DSHSource) Read(ctx context.Context, cursor string, limit int) (corenotif.NotificationPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := s.now()
	if err := ctx.Err(); err != nil {
		op := corenotif.OperationFromError(err, now)
		s.setLastOp(op)
		return corenotif.NotificationPage{Source: corenotif.SourceDSH}, err
	}
	if limit <= 0 {
		limit = corenotif.DefaultPageLimit
	}
	if limit > corenotif.MaxPageLimit {
		limit = corenotif.MaxPageLimit
	}
	if err := corenotif.ValidateCursor(cursor); err != nil {
		srcErr := corenotif.NewSourceError(err, host.StateFailed, "invalid_cursor", "dsh cursor is invalid", false, now)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceDSH}, srcErr
	}

	curFile, curLine, hasCursor, err := parseDSHCursor(cursor)
	if err != nil {
		srcErr := corenotif.NewSourceError(err, host.StateFailed, "invalid_cursor", "dsh cursor format is invalid", false, now)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceDSH}, srcErr
	}

	entries, err := s.reader.ReadDir(s.eventsDir)
	if err != nil {
		if classified := s.classifyFSError(err, now); classified != nil {
			return corenotif.NotificationPage{Source: corenotif.SourceDSH}, classified
		}
		srcErr := corenotif.NewSourceError(corenotif.ErrSourceUnavailable, host.StateDegraded, "source_unavailable", "dsh event directory not found", true, now)
		s.setLastOp(srcErr.Operation)
		return corenotif.NotificationPage{Source: corenotif.SourceDSH}, srcErr
	}

	var fileNames []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".jsonl" || ext == ".json" {
			fileNames = append(fileNames, name)
		}
	}
	sort.Strings(fileNames)

	if hasCursor {
		foundCursorFile := false
		for _, name := range fileNames {
			if name == curFile {
				foundCursorFile = true
				break
			}
		}
		if !foundCursorFile {
			srcErr := corenotif.NewSourceError(
				corenotif.ErrCursorExpired,
				host.StateDegraded,
				"cursor_expired",
				"dsh cursor file no longer exists",
				true,
				now,
			)
			s.setLastOp(srcErr.Operation)
			return corenotif.NotificationPage{Source: corenotif.SourceDSH}, srcErr
		}
	}

	var events []corenotif.NotificationEvent
	nextCursor := cursor
	hasMore := false

	for _, name := range fileNames {
		if err := ctx.Err(); err != nil {
			op := corenotif.OperationFromError(err, now)
			s.setLastOp(op)
			return corenotif.NotificationPage{Source: corenotif.SourceDSH}, err
		}
		if hasCursor && name < curFile {
			continue
		}
		fullPath := filepath.Join(s.eventsDir, name)
		data, err := s.reader.ReadFile(fullPath)
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				srcErr := corenotif.NewSourceError(corenotif.ErrPermissionDenied, host.StateFailed, "permission_denied", "dsh event file permission denied", false, now)
				s.setLastOp(srcErr.Operation)
				return corenotif.NotificationPage{Source: corenotif.SourceDSH}, srcErr
			}
			srcErr := corenotif.NewSourceError(corenotif.ErrSourceUnavailable, host.StateDegraded, "read_failed", "dsh event file unreadable", true, now)
			s.setLastOp(srcErr.Operation)
			return corenotif.NotificationPage{Source: corenotif.SourceDSH}, srcErr
		}
		if int64(len(data)) > s.maxFileBytes {
			data = data[:s.maxFileBytes]
		}

		records, err := decodeDSHFile(name, data, now)
		if err != nil {
			s.setLastOp(corenotif.OperationFromError(err, now))
			return corenotif.NotificationPage{Source: corenotif.SourceDSH}, err
		}

		startIdx := 0
		if hasCursor && name == curFile {
			if curLine > len(records) {
				srcErr := corenotif.NewSourceError(
					corenotif.ErrCursorExpired,
					host.StateDegraded,
					"cursor_expired",
					"dsh event log was truncated past stored cursor",
					true,
					now,
				)
				s.setLastOp(srcErr.Operation)
				return corenotif.NotificationPage{Source: corenotif.SourceDSH}, srcErr
			}
			startIdx = curLine
		}

		for i := startIdx; i < len(records); i++ {
			if len(events) >= limit {
				hasMore = true
				break
			}
			recCursor := formatDSHCursor(name, i+1)
			ev, err := buildDSHEvent(records[i], recCursor, now)
			if err != nil {
				s.setLastOp(corenotif.OperationFromError(err, now))
				return corenotif.NotificationPage{Source: corenotif.SourceDSH}, err
			}
			events = append(events, ev)
			nextCursor = recCursor
		}
		if hasMore {
			break
		}
	}

	s.setLastOp(host.OperationResult{
		State:      host.StateReady,
		Code:       "ok",
		Message:    "dsh read completed",
		ObservedAt: now,
	})
	return corenotif.NotificationPage{
		Source:     corenotif.SourceDSH,
		Events:     events,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func formatDSHCursor(fileName string, nextLine int) string {
	return fmt.Sprintf("%s%s:%d", dshCursorPrefix, fileName, nextLine)
}

func parseDSHCursor(cursor string) (fileName string, line int, ok bool, err error) {
	if cursor == "" {
		return "", 0, false, nil
	}
	if !strings.HasPrefix(cursor, dshCursorPrefix) {
		return "", 0, false, corenotif.ErrInvalidCursor
	}
	rest := strings.TrimPrefix(cursor, dshCursorPrefix)
	sep := strings.LastIndexByte(rest, ':')
	if sep <= 0 || sep >= len(rest)-1 {
		return "", 0, false, corenotif.ErrInvalidCursor
	}
	fileName = rest[:sep]
	if strings.ContainsAny(fileName, `/\`) || fileName == "." || fileName == ".." {
		return "", 0, false, corenotif.ErrInvalidCursor
	}
	n, convErr := strconv.Atoi(rest[sep+1:])
	if convErr != nil || n < 0 {
		return "", 0, false, corenotif.ErrInvalidCursor
	}
	return fileName, n, true, nil
}

func decodeDSHFile(fileName string, data []byte, now time.Time) ([]map[string]any, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if strings.EqualFold(filepath.Ext(fileName), ".json") {
		var single map[string]any
		if err := json.Unmarshal(trimmed, &single); err == nil {
			if err := checkDSHSchemaVersion(single, now); err != nil {
				return nil, err
			}
			if rawItems, hasEvents := single["events"]; hasEvents {
				arr, ok := rawItems.([]any)
				if !ok {
					return nil, corenotif.NewSourceError(corenotif.ErrSchemaDrift, host.StateDegraded, "schema_drift", "dsh events field is not a list", false, now)
				}
				out := make([]map[string]any, 0, len(arr))
				for _, item := range arr {
					m, ok := item.(map[string]any)
					if !ok {
						return nil, corenotif.NewSourceError(corenotif.ErrSchemaDrift, host.StateDegraded, "schema_drift", "dsh event item is not an object", false, now)
					}
					if err := checkDSHSchemaVersion(m, now); err != nil {
						return nil, err
					}
					out = append(out, m)
				}
				return out, nil
			}
			return []map[string]any{single}, nil
		}
		var list []map[string]any
		if err := json.Unmarshal(trimmed, &list); err == nil {
			for _, m := range list {
				if m == nil {
					return nil, corenotif.NewSourceError(corenotif.ErrSchemaDrift, host.StateDegraded, "schema_drift", "dsh event list contains null entry", false, now)
				}
				if err := checkDSHSchemaVersion(m, now); err != nil {
					return nil, err
				}
			}
			return list, nil
		}
		return nil, corenotif.NewSourceError(corenotif.ErrSchemaDrift, host.StateDegraded, "schema_drift", "dsh json file is malformed", false, now)
	}

	// JSONL: one JSON object per non-empty line.
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal(line, &row); err != nil || row == nil {
			return nil, corenotif.NewSourceError(corenotif.ErrSchemaDrift, host.StateDegraded, "schema_drift", "dsh jsonl entry is malformed", false, now)
		}
		if err := checkDSHSchemaVersion(row, now); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := sc.Err(); err != nil {
		return nil, corenotif.NewSourceError(corenotif.ErrSchemaDrift, host.StateDegraded, "schema_drift", "dsh jsonl stream exceeded safe entry budget", false, now)
	}
	return out, nil
}

func checkDSHSchemaVersion(row map[string]any, now time.Time) error {
	for _, key := range []string{"schema_version", "schemaVersion"} {
		raw, ok := row[key]
		if !ok || raw == nil {
			continue
		}
		ver, ok := jsonNumberToInt(raw)
		if !ok || ver <= 0 || ver > dshSupportedSchemaVer {
			return corenotif.NewSourceError(
				corenotif.ErrSchemaDrift,
				host.StateDegraded,
				"schema_drift",
				"dsh event schema version is unsupported",
				false,
				now,
			)
		}
	}
	return nil
}

func buildDSHEvent(row map[string]any, recCursor string, now time.Time) (corenotif.NotificationEvent, error) {
	// Strict field allowlist: unknown fields (raw payloads, tokens, attachments) are ignored.
	eventID := firstStringField(row, "event_id", "eventId", "id")
	convRef := firstStringField(row, "conversation_ref", "conversationRef", "conversation_id", "conversationId", "thread_id", "threadId")
	senderRef := firstStringField(row, "sender_ref", "senderRef", "sender_id", "senderId", "sender", "role", "author")
	kind := firstStringField(row, "kind", "event_type", "eventType", "type")
	preview := firstStringField(row, "text_preview", "textPreview", "preview", "summary", "text", "message", "content")
	// SessionID is populated ONLY from an explicit binding field — never guessed from text.
	sessionID := firstStringField(row, "session_id", "sessionId")
	at := firstTimeField(row, "at", "timestamp", "created_at", "createdAt", "updated_at", "updatedAt")

	if eventID == "" && convRef == "" && kind == "" && preview == "" && sessionID == "" {
		return corenotif.NotificationEvent{}, corenotif.NewSourceError(
			corenotif.ErrSchemaDrift,
			host.StateDegraded,
			"schema_drift",
			"dsh event object contains no recognized notification fields",
			false,
			now,
		)
	}

	ev := corenotif.NotificationEvent{
		Source:          corenotif.SourceDSH,
		EventID:         eventID,
		ConversationRef: convRef,
		SenderRef:       senderRef,
		Kind:            kind,
		TextPreview:     preview,
		SessionID:       sessionID,
		At:              at,
		SourceCursor:    recCursor,
		ObservedAt:      now,
	}
	norm, _, err := corenotif.NormalizeEvent(ev, now)
	if err != nil {
		return corenotif.NotificationEvent{}, err
	}
	return norm, nil
}

func firstStringField(m map[string]any, keys ...string) string {
	for _, key := range keys {
		val, ok := m[key]
		if !ok || val == nil {
			continue
		}
		if s, ok := val.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func firstTimeField(m map[string]any, keys ...string) time.Time {
	for _, key := range keys {
		val, ok := m[key]
		if !ok || val == nil {
			continue
		}
		switch typed := val.(type) {
		case string:
			s := strings.TrimSpace(typed)
			if s == "" {
				continue
			}
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return t.UTC()
			}
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t.UTC()
			}
			if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
				return unixAutoTime(n)
			}
		case float64:
			if typed > 0 {
				return unixAutoTime(int64(typed))
			}
		case int64:
			if typed > 0 {
				return unixAutoTime(typed)
			}
		}
	}
	return time.Time{}
}

func unixAutoTime(n int64) time.Time {
	switch {
	case n >= 1e18:
		return time.Unix(0, n).UTC()
	case n >= 1e15:
		return time.UnixMicro(n).UTC()
	case n >= 1e12:
		return time.UnixMilli(n).UTC()
	default:
		return time.Unix(n, 0).UTC()
	}
}

func jsonNumberToInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}
