// Package notification defines the pure-stdlib, strictly read-only notification
// ingestion contract, normalization/sanitization rules, stable/approximate
// deduplication keys, and page-level ingestion orchestration.
//
// Hard boundary: this package never sends messages, stops remote sessions,
// mutates remote objects, stores raw payloads/tokens/attachments, or imports
// third-party / MyGo packages.
package notification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/host"
)

const (
	// SourceDSH is the canonical allowlisted source identifier for DSH events.
	SourceDSH = "dsh"
	// SourceLark is the canonical allowlisted source identifier for Lark/Feishu events.
	SourceLark = "lark"

	// MaxTextPreviewRunes caps sanitized preview text stored per notification.
	MaxTextPreviewRunes = 240
	// MaxEventIDRunes caps normalized stable EventID length.
	MaxEventIDRunes = 256
	// MaxRefRunes caps sanitized ConversationRef, SenderRef, and SessionID values.
	MaxRefRunes = 128
	// MaxKindRunes caps normalized event kind length.
	MaxKindRunes = 64
	// MaxCursorLength caps opaque source cursor byte length.
	MaxCursorLength = 512

	// DefaultPageLimit is used when callers pass limit <= 0.
	DefaultPageLimit = 50
	// MaxPageLimit bounds a single pull page read.
	MaxPageLimit = 200

	// DefaultApproxBucketSize buckets approximate dedup timestamps into 60s UTC windows.
	DefaultApproxBucketSize = time.Minute
	// DefaultApproxDedupTTL defines how long an approximate dedup key suppresses duplicates.
	DefaultApproxDedupTTL = 7 * 24 * time.Hour
)

var (
	// ErrUnknownSource indicates a source outside the fixed allowlist.
	ErrUnknownSource = errors.New("unknown_source")
	// ErrInvalidCursor indicates a cursor with invalid charset, length, or foreign source prefix.
	ErrInvalidCursor = errors.New("invalid_cursor")
	// ErrCursorExpired indicates the source rejected the stored cursor as expired or rotated.
	// Callers must NOT silently delete stored events or wipe the cursor on this error.
	ErrCursorExpired = errors.New("cursor_expired")
	// ErrPermissionDenied indicates the source lacks read authorization.
	ErrPermissionDenied = errors.New("permission_denied")
	// ErrSchemaDrift indicates the source payload/file schema changed or is malformed.
	ErrSchemaDrift = errors.New("schema_drift")
	// ErrSourceUnavailable indicates the source is unreachable or not configured.
	ErrSourceUnavailable = errors.New("source_unavailable")
	// ErrReadOnlyViolation indicates an attempt to configure a write/mutating endpoint or action.
	ErrReadOnlyViolation = errors.New("read_only_violation")
	// ErrSchemaTooNew indicates the local notification database schema version exceeds binary support.
	ErrSchemaTooNew = errors.New("schema_too_new")
)

// SourceError attaches a structured, secret-free host.OperationResult to a sentinel error.
type SourceError struct {
	Cause     error
	Operation host.OperationResult
}

func (e *SourceError) Error() string {
	if e == nil {
		return ""
	}
	msg := SanitizeDiagnosticMessage(e.Operation.Message)
	if e.Operation.Code != "" && msg != "" {
		return fmt.Sprintf("%s: %s", e.Operation.Code, msg)
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Operation.Code
}

func (e *SourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewSourceError constructs a SourceError with sanitized diagnostic text.
func NewSourceError(cause error, state host.OperationState, code, message string, retryable bool, observedAt time.Time) *SourceError {
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	return &SourceError{
		Cause: cause,
		Operation: host.OperationResult{
			State:      state,
			Code:       strings.TrimSpace(code),
			Message:    SanitizeDiagnosticMessage(message),
			ObservedAt: observedAt.UTC(),
			Retryable:  retryable,
		},
	}
}

// OperationFromError extracts a structured OperationResult from err when available.
func OperationFromError(err error, observedAt time.Time) host.OperationResult {
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	if err == nil {
		return host.OperationResult{
			State:      host.StateReady,
			Code:       "ok",
			ObservedAt: observedAt.UTC(),
		}
	}
	var srcErr *SourceError
	if errors.As(err, &srcErr) {
		op := srcErr.Operation
		op.Message = SanitizeDiagnosticMessage(op.Message)
		if op.ObservedAt.IsZero() {
			op.ObservedAt = observedAt.UTC()
		}
		return op
	}
	switch {
	case errors.Is(err, ErrUnknownSource):
		return host.OperationResult{State: host.StateFailed, Code: "unknown_source", Message: "unsupported notification source", ObservedAt: observedAt.UTC(), Retryable: false}
	case errors.Is(err, ErrInvalidCursor):
		return host.OperationResult{State: host.StateFailed, Code: "invalid_cursor", Message: "notification cursor is invalid", ObservedAt: observedAt.UTC(), Retryable: false}
	case errors.Is(err, ErrCursorExpired):
		return host.OperationResult{State: host.StateDegraded, Code: "cursor_expired", Message: "notification cursor expired", ObservedAt: observedAt.UTC(), Retryable: true}
	case errors.Is(err, ErrPermissionDenied):
		return host.OperationResult{State: host.StateFailed, Code: "permission_denied", Message: "notification source permission denied", ObservedAt: observedAt.UTC(), Retryable: false}
	case errors.Is(err, ErrSchemaDrift):
		return host.OperationResult{State: host.StateDegraded, Code: "schema_drift", Message: "notification source schema drift detected", ObservedAt: observedAt.UTC(), Retryable: false}
	case errors.Is(err, ErrReadOnlyViolation):
		return host.OperationResult{State: host.StateFailed, Code: "read_only_violation", Message: "write operation blocked by read-only boundary", ObservedAt: observedAt.UTC(), Retryable: false}
	case errors.Is(err, ErrSchemaTooNew):
		return host.OperationResult{State: host.StateDegraded, Code: "schema_too_new", Message: "notification store schema is newer than this binary", ObservedAt: observedAt.UTC(), Retryable: false}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return host.OperationResult{State: host.StateUnknown, Code: "cancelled", Message: "notification read interrupted", ObservedAt: observedAt.UTC(), Retryable: true}
	default:
		return host.OperationResult{State: host.StateDegraded, Code: "source_unavailable", Message: SanitizeDiagnosticMessage(err.Error()), ObservedAt: observedAt.UTC(), Retryable: true}
	}
}

// DedupClass distinguishes protocol-level stable EventID deduplication from
// bounded-window approximate deduplication.
type DedupClass string

const (
	// DedupStable indicates deduplication backed by a source-issued stable EventID.
	DedupStable DedupClass = "stable"
	// DedupApprox indicates bounded-window approximate deduplication when EventID is absent.
	DedupApprox DedupClass = "approximate"
)

// NotificationEvent is the normalized, sanitized, read-only local notification record.
// It stores only opaque references and a bounded preview — never raw payloads,
// tokens, attachments, or full transcripts.
type NotificationEvent struct {
	Source          string     `json:"source"`
	EventID         string     `json:"eventId,omitempty"`
	DedupClass      DedupClass `json:"dedupClass"`
	DedupKey        string     `json:"dedupKey"`
	ConversationRef string     `json:"conversationRef,omitempty"`
	SenderRef       string     `json:"senderRef,omitempty"`
	Kind            string     `json:"kind"`
	TextPreview     string     `json:"textPreview,omitempty"`
	SessionID       string     `json:"sessionId,omitempty"`
	At              time.Time  `json:"at"`
	SourceCursor    string     `json:"sourceCursor,omitempty"`
	ObservedAt      time.Time  `json:"observedAt"`
	DedupExpiresAt  *time.Time `json:"dedupExpiresAt,omitempty"`
	TimeUnreliable  bool       `json:"timeUnreliable,omitempty"`
}

// NotificationPage is a bounded pull page returned by a NotificationSource or store query.
type NotificationPage struct {
	Source     string              `json:"source"`
	Events     []NotificationEvent `json:"events"`
	NextCursor string              `json:"nextCursor,omitempty"`
	HasMore    bool                `json:"hasMore"`
}

// SourceCapabilities reports read-only capabilities observed from a NotificationSource.
type SourceCapabilities struct {
	ReadEvents    bool      `json:"readEvents"`
	StableEventID bool      `json:"stableEventId"`
	Cursor        bool      `json:"cursor"`
	History       bool      `json:"history"`
	ObservedAt    time.Time `json:"observedAt"`
}

// Diagnostic records a non-secret, structured ingestion warning or status note.
type Diagnostic struct {
	Source  string `json:"source"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NotificationSource is the strictly read-only, pull-paged event reader contract.
// Implementations MUST NOT expose write, send, stop, mutate, or remote ack actions.
type NotificationSource interface {
	Name() string
	Available(ctx context.Context) (bool, error)
	Read(ctx context.Context, cursor string, limit int) (NotificationPage, error)
}

// CapabilitySource is an optional interface implemented by sources that support
// explicit read-only capability probing.
type CapabilitySource interface {
	Capabilities(ctx context.Context) (SourceCapabilities, error)
}

// ApplyPageResult summarizes an atomic ApplyPage transaction.
type ApplyPageResult struct {
	Inserted        int    `json:"inserted"`
	Duplicates      int    `json:"duplicates"`
	Pruned          int    `json:"pruned,omitempty"`
	CommittedCursor string `json:"committedCursor,omitempty"`
}

// NotificationStore persists normalized local notifications and source cursors.
// Production ingestion MUST use ApplyPage so event inserts and cursor advancement
// happen inside a single atomic transaction.
type NotificationStore interface {
	Seen(ctx context.Context, source, dedupKey string) (bool, error)
	Put(ctx context.Context, event NotificationEvent) error
	CommitCursor(ctx context.Context, source, cursor string) error
	Cursor(ctx context.Context, source string) (string, error)
	ApplyPage(ctx context.Context, source string, page NotificationPage) (ApplyPageResult, error)
	Recent(ctx context.Context, limit int) ([]NotificationEvent, error)
	RecentBySource(ctx context.Context, source string, limit int) (NotificationPage, error)
}

// IngestResult summarizes one pull-and-persist cycle executed by IngestPage.
type IngestResult struct {
	Source      string               `json:"source"`
	ReadCount   int                  `json:"readCount"`
	Inserted    int                  `json:"inserted"`
	Duplicates  int                  `json:"duplicates"`
	ApproxCount int                  `json:"approxCount"`
	PrevCursor  string               `json:"prevCursor,omitempty"`
	NextCursor  string               `json:"nextCursor,omitempty"`
	HasMore     bool                 `json:"hasMore"`
	Diagnostics []Diagnostic         `json:"diagnostics,omitempty"`
	Operation   host.OperationResult `json:"operation"`
}

// NormalizeSourceName canonicalizes a source identifier.
func NormalizeSourceName(source string) string {
	return strings.ToLower(strings.TrimSpace(source))
}

// IsAllowedSource reports whether source is in the fixed read-only allowlist.
func IsAllowedSource(source string) bool {
	switch NormalizeSourceName(source) {
	case SourceDSH, SourceLark:
		return true
	default:
		return false
	}
}

// ValidateCursor checks opaque cursor length and character safety.
func ValidateCursor(cursor string) error {
	if cursor == "" {
		return nil
	}
	if len(cursor) > MaxCursorLength {
		return fmt.Errorf("%w: exceeds max length %d", ErrInvalidCursor, MaxCursorLength)
	}
	for i := 0; i < len(cursor); i++ {
		b := cursor[i]
		// Restrict opaque cursors to printable non-space ASCII so control bytes,
		// NUL, newlines, or whitespace cannot corrupt persistence or queries.
		if b < 0x21 || b > 0x7E {
			return fmt.Errorf("%w: disallowed character at offset %d", ErrInvalidCursor, i)
		}
	}
	return nil
}

var (
	codeBlockRE    = regexp.MustCompile("(?s)```.*?```")
	unclosedFence  = regexp.MustCompile("(?s)```.*$")
	unixPathRE     = regexp.MustCompile(`(?:/[A-Za-z0-9._-]+){2,}`)
	winPathRE      = regexp.MustCompile(`[A-Za-z]:\\[^\s"'` + "`" + `]+`)
	urlQueryRE     = regexp.MustCompile(`(https?://[^\s"'<>?#]+)\?[^\s"'<>#]+`)
	emailRE        = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	phoneRE        = regexp.MustCompile(`(?:\+?\d{1,3}[\s\-]?)?(?:\(?\d{3}\)?[\s\-]?\d{3,4}[\s\-]?\d{4}|\b1[3-9]\d{9}\b)`)
	jwtRE          = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
	secretRE       = regexp.MustCompile(`(?i)(?:bearer\s+[^\s"']+|sk-[A-Za-z0-9_-]{8,}|gh[pous]_[A-Za-z0-9_]{8,}|github_pat_[A-Za-z0-9_]{8,}|xox[bp]-[A-Za-z0-9_-]{8,}|(?:t|u)-[a-z0-9]{16,}|(?:api_?key|secret_?key|app_?secret|access_?token|refresh_?token|password|passwd|token|authorization)\s*[=:]\s*[^\s"']+)`)
	ansiCSI        = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	ansiOSC        = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	ansiSingle     = regexp.MustCompile(`\x1b[@-Z\\-_]`)
	safeKindCharRE = regexp.MustCompile(`[^a-z0-9._:-]+`)
)

func stripANSIAndControl(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	s = ansiOSC.ReplaceAllString(s, "")
	s = ansiCSI.ReplaceAllString(s, "")
	s = ansiSingle.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			b.WriteByte(' ')
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func truncateRunes(s string, maxRunes int, ellipsis bool) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if ellipsis && maxRunes > 1 {
		return string(runes[:maxRunes-1]) + "…"
	}
	return string(runes[:maxRunes])
}

// SanitizeTextPreview redacts tokens, URL query strings, absolute paths,
// code blocks, ANSI escapes, and control characters, then collapses whitespace
// and caps length to MaxTextPreviewRunes (240 Unicode runes).
func SanitizeTextPreview(raw string) string {
	if raw == "" {
		return ""
	}
	s := raw
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	s = ansiOSC.ReplaceAllString(s, "")
	s = ansiCSI.ReplaceAllString(s, "")
	s = ansiSingle.ReplaceAllString(s, "")
	s = codeBlockRE.ReplaceAllString(s, " [code] ")
	s = unclosedFence.ReplaceAllString(s, " [code] ")
	s = jwtRE.ReplaceAllString(s, "[redacted]")
	s = secretRE.ReplaceAllString(s, "[redacted]")
	s = urlQueryRE.ReplaceAllString(s, "$1?[redacted]")
	s = winPathRE.ReplaceAllString(s, "[path]")
	s = unixPathRE.ReplaceAllString(s, "[path]")
	s = stripANSIAndControl(s)
	s = strings.Join(strings.Fields(s), " ")
	return truncateRunes(s, MaxTextPreviewRunes, true)
}

// SanitizeRef redacts PII/secrets (emails, phone numbers, URL queries, tokens,
// absolute paths) from opaque ConversationRef and SenderRef identifiers.
func SanitizeRef(raw string) string {
	if raw == "" {
		return ""
	}
	s := stripANSIAndControl(raw)
	s = jwtRE.ReplaceAllString(s, "[redacted]")
	s = secretRE.ReplaceAllString(s, "[redacted]")
	s = urlQueryRE.ReplaceAllString(s, "$1")
	s = emailRE.ReplaceAllString(s, "[email]")
	s = phoneRE.ReplaceAllString(s, "[phone]")
	s = winPathRE.ReplaceAllString(s, "[path]")
	s = unixPathRE.ReplaceAllString(s, "[path]")
	s = strings.Join(strings.Fields(s), " ")
	return truncateRunes(s, MaxRefRunes, false)
}

// SanitizeDiagnosticMessage ensures diagnostic messages remain short and secret-free.
func SanitizeDiagnosticMessage(raw string) string {
	s := SanitizeTextPreview(raw)
	s = emailRE.ReplaceAllString(s, "[email]")
	s = phoneRE.ReplaceAllString(s, "[phone]")
	return truncateRunes(s, 160, true)
}

func sanitizeOpaqueID(raw string, maxRunes int) string {
	s := stripANSIAndControl(raw)
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if secretRE.MatchString(s) || jwtRE.MatchString(s) {
		return ""
	}
	// Strip internal whitespace from opaque IDs.
	s = strings.Join(strings.Fields(s), "")
	return truncateRunes(s, maxRunes, false)
}

func normalizeKind(raw string) string {
	s := strings.ToLower(strings.TrimSpace(stripANSIAndControl(raw)))
	s = safeKindCharRE.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "notification"
	}
	return truncateRunes(s, MaxKindRunes, false)
}

// ComputeDedupKey computes the canonical deduplication class and key for ev.
//
// When a stable EventID is present:
//
//	DedupClass = DedupStable
//	DedupKey   = source + "\x00stable\x00" + eventID
//
// When EventID is absent:
//
//	previewDigest = hex(SHA-256(normalizedTextPreview))
//	DedupClass    = DedupApprox
//	DedupKey      = source + "\x00approx\x00" + conversationRef + "\x00" + senderRef + "\x00" + atBucket + "\x00" + previewDigest
func ComputeDedupKey(ev NotificationEvent) (DedupClass, string) {
	source := NormalizeSourceName(ev.Source)
	eventID := sanitizeOpaqueID(ev.EventID, MaxEventIDRunes)
	if eventID != "" {
		return DedupStable, source + "\x00stable\x00" + eventID
	}

	convRef := SanitizeRef(ev.ConversationRef)
	senderRef := SanitizeRef(ev.SenderRef)
	preview := SanitizeTextPreview(ev.TextPreview)
	at := ev.At
	if at.IsZero() {
		at = ev.ObservedAt
	}
	var bucket string
	if !at.IsZero() {
		bucket = strconv.FormatInt(at.UTC().Truncate(DefaultApproxBucketSize).Unix()/60, 10)
	}
	sum := sha256.Sum256([]byte(preview))
	digest := hex.EncodeToString(sum[:])

	key := strings.Join([]string{
		source,
		"approx",
		convRef,
		senderRef,
		bucket,
		digest,
	}, "\x00")
	return DedupApprox, key
}

// NormalizeEvent validates, sanitizes, and computes canonical dedup metadata for ev.
// It never fabricates EventID from approximate digests and rejects unknown sources
// or invalid cursors.
func NormalizeEvent(ev NotificationEvent, now time.Time) (NotificationEvent, []Diagnostic, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	source := NormalizeSourceName(ev.Source)
	if !IsAllowedSource(source) {
		return NotificationEvent{}, nil, NewSourceError(
			ErrUnknownSource,
			host.StateFailed,
			"unknown_source",
			"notification source is not in the allowlist",
			false,
			now,
		)
	}

	cursor := strings.TrimSpace(ev.SourceCursor)
	if err := ValidateCursor(cursor); err != nil {
		return NotificationEvent{}, nil, NewSourceError(
			err,
			host.StateFailed,
			"invalid_cursor",
			"notification event cursor is invalid",
			false,
			now,
		)
	}

	observedAt := ev.ObservedAt.UTC()
	if ev.ObservedAt.IsZero() {
		observedAt = now
	}

	var diags []Diagnostic
	at := ev.At.UTC()
	timeUnreliable := ev.TimeUnreliable
	if ev.At.IsZero() {
		at = observedAt
		timeUnreliable = true
		diags = append(diags, Diagnostic{
			Source:  source,
			Code:    "time_unreliable",
			Message: "event timestamp missing; defaulted to observation time",
		})
	}

	out := NotificationEvent{
		Source:          source,
		EventID:         sanitizeOpaqueID(ev.EventID, MaxEventIDRunes),
		ConversationRef: SanitizeRef(ev.ConversationRef),
		SenderRef:       SanitizeRef(ev.SenderRef),
		Kind:            normalizeKind(ev.Kind),
		TextPreview:     SanitizeTextPreview(ev.TextPreview),
		SessionID:       sanitizeOpaqueID(ev.SessionID, MaxRefRunes),
		At:              at,
		SourceCursor:    cursor,
		ObservedAt:      observedAt,
		TimeUnreliable:  timeUnreliable,
	}

	class, key := ComputeDedupKey(out)
	out.DedupClass = class
	out.DedupKey = key
	if class == DedupApprox {
		// Never copy approximate digest back into EventID.
		out.EventID = ""
		if ev.DedupExpiresAt != nil && !ev.DedupExpiresAt.IsZero() {
			exp := ev.DedupExpiresAt.UTC()
			out.DedupExpiresAt = &exp
		} else {
			exp := observedAt.Add(DefaultApproxDedupTTL)
			out.DedupExpiresAt = &exp
		}
		diags = append(diags, Diagnostic{
			Source:  source,
			Code:    "approximate_dedup",
			Message: "stable event id unavailable; using bounded approximate dedup",
		})
	} else {
		out.DedupExpiresAt = nil
	}

	return out, diags, nil
}

// IngestPage reads a single bounded page from src starting at the store's last
// committed cursor, normalizes and sanitizes every event, and atomically applies
// the events and cursor advancement through store.ApplyPage.
//
// If the source is unavailable, returns permission/schema errors, or reports
// cursor_expired, IngestPage never modifies existing stored events or cursor.
func IngestPage(ctx context.Context, src NotificationSource, store NotificationStore, limit int) (IngestResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now().UTC()
	if src == nil || store == nil {
		err := NewSourceError(ErrSourceUnavailable, host.StateFailed, "invalid_ingest_args", "notification source and store are required", false, now)
		return IngestResult{Operation: err.Operation}, err
	}
	if err := ctx.Err(); err != nil {
		op := OperationFromError(err, now)
		return IngestResult{Source: NormalizeSourceName(src.Name()), Operation: op}, err
	}

	source := NormalizeSourceName(src.Name())
	res := IngestResult{Source: source}
	if !IsAllowedSource(source) {
		err := NewSourceError(ErrUnknownSource, host.StateFailed, "unknown_source", "notification source is not in the allowlist", false, now)
		res.Operation = err.Operation
		return res, err
	}

	if limit <= 0 {
		limit = DefaultPageLimit
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}

	available, err := src.Available(ctx)
	if err != nil {
		res.Operation = OperationFromError(err, now)
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Source:  source,
			Code:    res.Operation.Code,
			Message: res.Operation.Message,
		})
		return res, err
	}
	if !available {
		err := NewSourceError(ErrSourceUnavailable, host.StateDegraded, "source_unavailable", "notification source is currently unavailable", true, now)
		res.Operation = err.Operation
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Source:  source,
			Code:    err.Operation.Code,
			Message: err.Operation.Message,
		})
		return res, err
	}

	prevCursor, err := store.Cursor(ctx, source)
	if err != nil {
		res.Operation = OperationFromError(err, now)
		return res, err
	}
	res.PrevCursor = prevCursor
	res.NextCursor = prevCursor

	page, err := src.Read(ctx, prevCursor, limit)
	if err != nil {
		res.Operation = OperationFromError(err, now)
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Source:  source,
			Code:    res.Operation.Code,
			Message: res.Operation.Message,
		})
		return res, err
	}

	if page.Source != "" && NormalizeSourceName(page.Source) != source {
		err := NewSourceError(ErrSchemaDrift, host.StateDegraded, "source_mismatch", "notification page source did not match adapter source", false, now)
		res.Operation = err.Operation
		res.Diagnostics = append(res.Diagnostics, Diagnostic{Source: source, Code: err.Operation.Code, Message: err.Operation.Message})
		return res, err
	}
	if err := ValidateCursor(page.NextCursor); err != nil {
		srcErr := NewSourceError(err, host.StateFailed, "invalid_cursor", "notification page returned an invalid cursor", false, now)
		res.Operation = srcErr.Operation
		return res, srcErr
	}

	normalized := NotificationPage{
		Source:     source,
		Events:     make([]NotificationEvent, 0, len(page.Events)),
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
	}
	res.ReadCount = len(page.Events)
	for _, rawEv := range page.Events {
		if rawEv.Source == "" {
			rawEv.Source = source
		} else if NormalizeSourceName(rawEv.Source) != source {
			err := NewSourceError(ErrSchemaDrift, host.StateDegraded, "source_mismatch", "notification event source did not match adapter source", false, now)
			res.Operation = err.Operation
			return res, err
		}
		normEv, diags, err := NormalizeEvent(rawEv, now)
		if err != nil {
			res.Operation = OperationFromError(err, now)
			return res, err
		}
		if normEv.DedupClass == DedupApprox {
			res.ApproxCount++
		}
		res.Diagnostics = append(res.Diagnostics, diags...)
		normalized.Events = append(normalized.Events, normEv)
	}

	applyRes, err := store.ApplyPage(ctx, source, normalized)
	if err != nil {
		res.Operation = OperationFromError(err, now)
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Source:  source,
			Code:    res.Operation.Code,
			Message: res.Operation.Message,
		})
		return res, err
	}

	res.Inserted = applyRes.Inserted
	res.Duplicates = applyRes.Duplicates
	res.NextCursor = applyRes.CommittedCursor
	if res.NextCursor == "" {
		res.NextCursor = prevCursor
	}
	res.HasMore = normalized.HasMore
	res.Operation = host.OperationResult{
		State:      host.StateReady,
		Code:       "ok",
		Message:    "notification page ingested",
		ObservedAt: now,
		Retryable:  false,
	}
	return res, nil
}
