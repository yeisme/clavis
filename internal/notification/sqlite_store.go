// Package notification provides the SQLite-backed read-only NotificationStore
// and strictly read-only DSH / Lark NotificationSource adapters.
package notification

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/host"
	corenotif "github.com/yeisme/clavis/pkg/core/notification"
)

const (
	// NotificationSchemaVersion is the monotonic schema version stored in
	// notification_schema_meta. Newer versions trigger safe read-only degradation.
	NotificationSchemaVersion = 1

	// DefaultMaxEventsPerSource bounds stored rows per source during retention pruning.
	DefaultMaxEventsPerSource = 500
	// DefaultRecentMaxLimit caps Recent / RecentBySource page size.
	DefaultRecentMaxLimit = 1000
)

// RetentionPolicy configures bounded local retention for notification_events.
// Cursors in notification_cursors are NEVER deleted by retention pruning.
type RetentionPolicy struct {
	// MaxEventsPerSource keeps at most N newest rows per source (<=0 uses DefaultMaxEventsPerSource).
	MaxEventsPerSource int
	// MaxAge drops events older than MaxAge relative to Now when > 0.
	MaxAge time.Duration
	// DisableAutoPrune skips automatic pruning inside ApplyPage when true.
	DisableAutoPrune bool
}

// PruneResult reports rows removed by retention pruning.
type PruneResult struct {
	RemovedByAge   int `json:"removedByAge"`
	RemovedByLimit int `json:"removedByLimit"`
}

// StoreOptions configures SQLiteNotificationStore.
type StoreOptions struct {
	Retention RetentionPolicy
	Now       func() time.Time
}

// SQLiteNotificationStore implements pkg/core/notification.NotificationStore
// backed by MyGo's SQLite plugin with atomic event+cursor transactions.
type SQLiteNotificationStore struct {
	mu        sync.RWMutex
	db        *sqlite.DB
	closed    bool
	readOnly  bool
	schemaOp  host.OperationResult
	retention RetentionPolicy
	nowFn     func() time.Time
}

var _ corenotif.NotificationStore = (*SQLiteNotificationStore)(nil)

// NewSQLiteNotificationStore opens or creates the SQLite notification database
// at dbPath (or ":memory:") and initializes the notification tables.
//
// If the database contains a notification_schema_meta version newer than
// NotificationSchemaVersion, the store enters safe read-only mode: existing
// events and cursors remain readable, while all write operations return
// corenotif.ErrSchemaTooNew without dropping tables or resetting cursors.
func NewSQLiteNotificationStore(ctx context.Context, dbPath string, opts StoreOptions) (*SQLiteNotificationStore, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dbPath == "" {
		dbPath = ":memory:"
	}
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, fmt.Errorf("create notification db dir: %w", err)
			}
		}
	}

	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		return nil, fmt.Errorf("open notification sqlite db: %w", err)
	}

	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	retention := opts.Retention
	if retention.MaxEventsPerSource <= 0 {
		retention.MaxEventsPerSource = DefaultMaxEventsPerSource
	}

	store := &SQLiteNotificationStore{
		db:        db,
		retention: retention,
		nowFn:     nowFn,
	}
	if err := store.initSchema(ctx, dbPath); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteNotificationStore) now() time.Time {
	return s.nowFn().UTC()
}

// IsReadOnly reports whether the store degraded to read-only mode (e.g. schema_too_new).
func (s *SQLiteNotificationStore) IsReadOnly() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readOnly
}

// SchemaOperation returns the structured schema status OperationResult.
func (s *SQLiteNotificationStore) SchemaOperation() host.OperationResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.schemaOp
}

// Close closes the underlying SQLite connection.
func (s *SQLiteNotificationStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		s.closed = true
		return nil
	}
	s.closed = true
	return s.db.Close()
}

func (s *SQLiteNotificationStore) initSchema(ctx context.Context, dbPath string) error {
	now := s.now()
	// Check whether notification_schema_meta already exists before writing anything.
	metaCheck, err := s.db.Query(ctx, `SELECT 1 FROM sqlite_master WHERE type='table' AND name='notification_schema_meta'`)
	if err != nil {
		return fmt.Errorf("inspect notification schema meta: %w", err)
	}
	if len(metaCheck.Values) > 0 {
		verRows, err := s.db.Query(ctx, `SELECT version FROM notification_schema_meta WHERE id = 1`)
		if err != nil {
			return fmt.Errorf("read notification schema version: %w", err)
		}
		if len(verRows.Values) > 0 && len(verRows.Values[0]) > 0 {
			ver := int(asInt64(verRows.Values[0], 0))
			if ver > NotificationSchemaVersion {
				// Per design §8.8: degrade to read-only without dropping tables or resetting cursors.
				s.readOnly = true
				s.schemaOp = host.OperationResult{
					State:      host.StateDegraded,
					Code:       "schema_too_new",
					Message:    fmt.Sprintf("notification schema version %d exceeds supported version %d", ver, NotificationSchemaVersion),
					ObservedAt: now,
					Retryable:  false,
				}
				return nil
			}
		}
	}

	if _, err := s.db.Execute(ctx, `PRAGMA busy_timeout=5000`); err != nil {
		return fmt.Errorf("set sqlite busy_timeout: %w", err)
	}
	if dbPath != ":memory:" {
		if _, err := s.db.Execute(ctx, `PRAGMA journal_mode=WAL`); err != nil {
			return fmt.Errorf("enable WAL mode: %w", err)
		}
	}

	stmts := []sqlite.Statement{
		{SQL: `CREATE TABLE IF NOT EXISTS notification_schema_meta (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			version INTEGER NOT NULL,
			updated_at_ns INTEGER NOT NULL
		)`},
		{SQL: `CREATE TABLE IF NOT EXISTS notification_events (
			source TEXT NOT NULL,
			dedup_key TEXT NOT NULL,
			dedup_class TEXT NOT NULL,
			event_id TEXT NOT NULL DEFAULT '',
			conversation_ref TEXT NOT NULL DEFAULT '',
			sender_ref TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL,
			text_preview TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			at_ns INTEGER NOT NULL,
			source_cursor TEXT NOT NULL DEFAULT '',
			observed_at_ns INTEGER NOT NULL,
			dedup_expires_at_ns INTEGER,
			time_unreliable INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (source, dedup_key)
		)`},
		{SQL: `CREATE INDEX IF NOT EXISTS idx_notification_recent
			ON notification_events(source, at_ns DESC, observed_at_ns DESC, dedup_key DESC)`},
		{SQL: `CREATE INDEX IF NOT EXISTS idx_notification_global_recent
			ON notification_events(at_ns DESC, observed_at_ns DESC, dedup_key DESC)`},
		{SQL: `CREATE INDEX IF NOT EXISTS idx_notification_expires
			ON notification_events(dedup_expires_at_ns) WHERE dedup_expires_at_ns IS NOT NULL`},
		{SQL: `CREATE TABLE IF NOT EXISTS notification_cursors (
			source TEXT PRIMARY KEY,
			cursor TEXT NOT NULL,
			committed_at_ns INTEGER NOT NULL
		)`},
		{
			SQL: `INSERT INTO notification_schema_meta (id, version, updated_at_ns)
				VALUES (1, ?, ?)
				ON CONFLICT(id) DO UPDATE SET version = excluded.version, updated_at_ns = excluded.updated_at_ns`,
			Args: []any{int64(NotificationSchemaVersion), now.UnixNano()},
		},
	}
	if _, err := s.db.Transaction(ctx, stmts); err != nil {
		return fmt.Errorf("init notification schema: %w", err)
	}
	s.schemaOp = host.OperationResult{
		State:      host.StateReady,
		Code:       "ok",
		Message:    "notification schema ready",
		ObservedAt: now,
	}
	return nil
}

func (s *SQLiteNotificationStore) checkWritable() error {
	if s.closed || s.db == nil {
		return sqlite.ErrClosed
	}
	if s.readOnly {
		return corenotif.NewSourceError(
			corenotif.ErrSchemaTooNew,
			host.StateDegraded,
			"schema_too_new",
			"notification store is read-only because schema is newer than supported",
			false,
			s.now(),
		)
	}
	return nil
}

func (s *SQLiteNotificationStore) checkReadable() error {
	if s.closed || s.db == nil {
		return sqlite.ErrClosed
	}
	return nil
}

const upsertEventSQL = `INSERT INTO notification_events (
	source, dedup_key, dedup_class, event_id, conversation_ref, sender_ref,
	kind, text_preview, session_id, at_ns, source_cursor, observed_at_ns,
	dedup_expires_at_ns, time_unreliable
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source, dedup_key) DO UPDATE SET
	dedup_class = excluded.dedup_class,
	event_id = excluded.event_id,
	conversation_ref = excluded.conversation_ref,
	sender_ref = excluded.sender_ref,
	kind = excluded.kind,
	text_preview = excluded.text_preview,
	session_id = excluded.session_id,
	at_ns = excluded.at_ns,
	source_cursor = excluded.source_cursor,
	observed_at_ns = excluded.observed_at_ns,
	dedup_expires_at_ns = excluded.dedup_expires_at_ns,
	time_unreliable = excluded.time_unreliable
WHERE notification_events.dedup_expires_at_ns IS NOT NULL
  AND notification_events.dedup_expires_at_ns <= excluded.observed_at_ns`

func eventUpsertStatement(ev corenotif.NotificationEvent) sqlite.Statement {
	var expiresArg any
	if ev.DedupExpiresAt != nil && !ev.DedupExpiresAt.IsZero() {
		expiresArg = ev.DedupExpiresAt.UTC().UnixNano()
	}
	var unreliable int64
	if ev.TimeUnreliable {
		unreliable = 1
	}
	return sqlite.Statement{
		SQL: upsertEventSQL,
		Args: []any{
			ev.Source,
			ev.DedupKey,
			string(ev.DedupClass),
			ev.EventID,
			ev.ConversationRef,
			ev.SenderRef,
			ev.Kind,
			ev.TextPreview,
			ev.SessionID,
			ev.At.UTC().UnixNano(),
			ev.SourceCursor,
			ev.ObservedAt.UTC().UnixNano(),
			expiresArg,
			unreliable,
		},
	}
}

// Seen reports whether (source, dedupKey) is currently recorded and unexpired.
func (s *SQLiteNotificationStore) Seen(ctx context.Context, source, dedupKey string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	normSource := corenotif.NormalizeSourceName(source)
	if !corenotif.IsAllowedSource(normSource) {
		return false, corenotif.ErrUnknownSource
	}
	if dedupKey == "" {
		return false, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.checkReadable(); err != nil {
		return false, err
	}

	rows, err := s.db.Query(ctx,
		`SELECT 1 FROM notification_events
		 WHERE source = ? AND dedup_key = ?
		   AND (dedup_expires_at_ns IS NULL OR dedup_expires_at_ns > ?)
		 LIMIT 1`,
		normSource, dedupKey, s.now().UnixNano(),
	)
	if err != nil {
		return false, fmt.Errorf("check seen notification: %w", err)
	}
	return len(rows.Values) > 0, nil
}

// Put normalizes and idempotently inserts a single notification event.
func (s *SQLiteNotificationStore) Put(ctx context.Context, event corenotif.NotificationEvent) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	norm, _, err := corenotif.NormalizeEvent(event, s.now())
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkWritable(); err != nil {
		return err
	}

	stmt := eventUpsertStatement(norm)
	if _, err := s.db.Execute(ctx, stmt.SQL, stmt.Args...); err != nil {
		return fmt.Errorf("put notification event: %w", err)
	}
	return nil
}

// CommitCursor persists the opaque cursor for source.
func (s *SQLiteNotificationStore) CommitCursor(ctx context.Context, source, cursor string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	normSource := corenotif.NormalizeSourceName(source)
	if !corenotif.IsAllowedSource(normSource) {
		return corenotif.ErrUnknownSource
	}
	if err := corenotif.ValidateCursor(cursor); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkWritable(); err != nil {
		return err
	}

	_, err := s.db.Execute(ctx,
		`INSERT INTO notification_cursors (source, cursor, committed_at_ns)
		 VALUES (?, ?, ?)
		 ON CONFLICT(source) DO UPDATE SET
		   cursor = excluded.cursor,
		   committed_at_ns = excluded.committed_at_ns`,
		normSource, cursor, s.now().UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("commit notification cursor: %w", err)
	}
	return nil
}

// Cursor returns the last committed opaque cursor for source, or "" if unset.
func (s *SQLiteNotificationStore) Cursor(ctx context.Context, source string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	normSource := corenotif.NormalizeSourceName(source)
	if !corenotif.IsAllowedSource(normSource) {
		return "", corenotif.ErrUnknownSource
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.checkReadable(); err != nil {
		return "", err
	}

	rows, err := s.db.Query(ctx, `SELECT cursor FROM notification_cursors WHERE source = ?`, normSource)
	if err != nil {
		return "", fmt.Errorf("read notification cursor: %w", err)
	}
	if len(rows.Values) == 0 || len(rows.Values[0]) == 0 {
		return "", nil
	}
	return asString(rows.Values[0], 0), nil
}

// ApplyPage atomically inserts all normalized, deduplicated events in page AND
// advances source's cursor in a single SQLite transaction. If any write fails,
// SQLite rolls back both event inserts and cursor advancement.
func (s *SQLiteNotificationStore) ApplyPage(ctx context.Context, source string, page corenotif.NotificationPage) (corenotif.ApplyPageResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return corenotif.ApplyPageResult{}, err
	}
	normSource := corenotif.NormalizeSourceName(source)
	if !corenotif.IsAllowedSource(normSource) {
		return corenotif.ApplyPageResult{}, corenotif.ErrUnknownSource
	}
	if page.Source != "" && corenotif.NormalizeSourceName(page.Source) != normSource {
		return corenotif.ApplyPageResult{}, corenotif.ErrSchemaDrift
	}
	if err := corenotif.ValidateCursor(page.NextCursor); err != nil {
		return corenotif.ApplyPageResult{}, err
	}

	now := s.now()
	normalizedEvents := make([]corenotif.NotificationEvent, 0, len(page.Events))
	for _, ev := range page.Events {
		if ev.Source == "" {
			ev.Source = normSource
		} else if corenotif.NormalizeSourceName(ev.Source) != normSource {
			return corenotif.ApplyPageResult{}, corenotif.ErrSchemaDrift
		}
		normEv, _, err := corenotif.NormalizeEvent(ev, now)
		if err != nil {
			return corenotif.ApplyPageResult{}, err
		}
		normalizedEvents = append(normalizedEvents, normEv)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkWritable(); err != nil {
		return corenotif.ApplyPageResult{}, err
	}

	stmts := make([]sqlite.Statement, 0, len(normalizedEvents)+3)
	for _, ev := range normalizedEvents {
		stmts = append(stmts, eventUpsertStatement(ev))
	}

	cursorStmtIdx := -1
	if page.NextCursor != "" {
		cursorStmtIdx = len(stmts)
		stmts = append(stmts, sqlite.Statement{
			SQL: `INSERT INTO notification_cursors (source, cursor, committed_at_ns)
				VALUES (?, ?, ?)
				ON CONFLICT(source) DO UPDATE SET
					cursor = excluded.cursor,
					committed_at_ns = excluded.committed_at_ns`,
			Args: []any{normSource, page.NextCursor, now.UnixNano()},
		})
	}

	pruneAgeIdx := -1
	pruneLimitIdx := -1
	if !s.retention.DisableAutoPrune {
		if s.retention.MaxAge > 0 {
			pruneAgeIdx = len(stmts)
			cutoff := now.Add(-s.retention.MaxAge).UnixNano()
			stmts = append(stmts, sqlite.Statement{
				SQL:  `DELETE FROM notification_events WHERE source = ? AND at_ns < ?`,
				Args: []any{normSource, cutoff},
			})
		}
		if s.retention.MaxEventsPerSource > 0 {
			pruneLimitIdx = len(stmts)
			stmts = append(stmts, sqlite.Statement{
				SQL: `DELETE FROM notification_events
					WHERE source = ?
					  AND rowid NOT IN (
						SELECT rowid FROM notification_events
						WHERE source = ?
						ORDER BY at_ns DESC, observed_at_ns DESC, dedup_key DESC
						LIMIT ?
					  )`,
				Args: []any{normSource, normSource, int64(s.retention.MaxEventsPerSource)},
			})
		}
	}

	var results []sqlite.Result
	if len(stmts) > 0 {
		var err error
		results, err = s.db.Transaction(ctx, stmts)
		if err != nil {
			return corenotif.ApplyPageResult{}, fmt.Errorf("apply notification page: %w", err)
		}
	}

	out := corenotif.ApplyPageResult{}
	for i := 0; i < len(normalizedEvents) && i < len(results); i++ {
		if results[i].Changes > 0 {
			out.Inserted++
		} else {
			out.Duplicates++
		}
	}
	if pruneAgeIdx >= 0 && pruneAgeIdx < len(results) {
		out.Pruned += int(results[pruneAgeIdx].Changes)
	}
	if pruneLimitIdx >= 0 && pruneLimitIdx < len(results) {
		out.Pruned += int(results[pruneLimitIdx].Changes)
	}
	if cursorStmtIdx >= 0 {
		out.CommittedCursor = page.NextCursor
	} else {
		rows, err := s.db.Query(ctx, `SELECT cursor FROM notification_cursors WHERE source = ?`, normSource)
		if err == nil && len(rows.Values) > 0 && len(rows.Values[0]) > 0 {
			out.CommittedCursor = asString(rows.Values[0], 0)
		}
	}
	return out, nil
}

// Recent returns the newest normalized notification events across all sources.
func (s *SQLiteNotificationStore) Recent(ctx context.Context, limit int) ([]corenotif.NotificationEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit = clampRecentLimit(limit)

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.checkReadable(); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx,
		`SELECT source, dedup_key, dedup_class, event_id, conversation_ref, sender_ref,
		        kind, text_preview, session_id, at_ns, source_cursor, observed_at_ns,
		        dedup_expires_at_ns, time_unreliable
		 FROM notification_events
		 ORDER BY at_ns DESC, observed_at_ns DESC, dedup_key DESC
		 LIMIT ?`,
		int64(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("query recent notifications: %w", err)
	}
	return rowsToEvents(rows.Values), nil
}

// RecentBySource returns a page of newest notifications for a single source along
// with its current committed cursor and HasMore indicator.
func (s *SQLiteNotificationStore) RecentBySource(ctx context.Context, source string, limit int) (corenotif.NotificationPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return corenotif.NotificationPage{}, err
	}
	normSource := corenotif.NormalizeSourceName(source)
	if !corenotif.IsAllowedSource(normSource) {
		return corenotif.NotificationPage{}, corenotif.ErrUnknownSource
	}
	limit = clampRecentLimit(limit)

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.checkReadable(); err != nil {
		return corenotif.NotificationPage{}, err
	}

	rows, err := s.db.Query(ctx,
		`SELECT source, dedup_key, dedup_class, event_id, conversation_ref, sender_ref,
		        kind, text_preview, session_id, at_ns, source_cursor, observed_at_ns,
		        dedup_expires_at_ns, time_unreliable
		 FROM notification_events
		 WHERE source = ?
		 ORDER BY at_ns DESC, observed_at_ns DESC, dedup_key DESC
		 LIMIT ?`,
		normSource, int64(limit+1),
	)
	if err != nil {
		return corenotif.NotificationPage{}, fmt.Errorf("query recent notifications for source %s: %w", normSource, err)
	}
	events := rowsToEvents(rows.Values)
	hasMore := false
	if len(events) > limit {
		hasMore = true
		events = events[:limit]
	}

	cursorRows, err := s.db.Query(ctx, `SELECT cursor FROM notification_cursors WHERE source = ?`, normSource)
	if err != nil {
		return corenotif.NotificationPage{}, fmt.Errorf("read cursor for source %s: %w", normSource, err)
	}
	var cursor string
	if len(cursorRows.Values) > 0 && len(cursorRows.Values[0]) > 0 {
		cursor = asString(cursorRows.Values[0], 0)
	}

	return corenotif.NotificationPage{
		Source:     normSource,
		Events:     events,
		NextCursor: cursor,
		HasMore:    hasMore,
	}, nil
}

// Prune enforces retention limits (MaxAge and MaxEventsPerSource) across all
// allowlisted sources in one atomic transaction without modifying cursors.
func (s *SQLiteNotificationStore) Prune(ctx context.Context) (PruneResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return PruneResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkWritable(); err != nil {
		return PruneResult{}, err
	}

	now := s.now()
	var stmts []sqlite.Statement
	ageIdx := -1
	if s.retention.MaxAge > 0 {
		ageIdx = len(stmts)
		cutoff := now.Add(-s.retention.MaxAge).UnixNano()
		stmts = append(stmts, sqlite.Statement{
			SQL:  `DELETE FROM notification_events WHERE at_ns < ?`,
			Args: []any{cutoff},
		})
	}
	limitStart := len(stmts)
	if s.retention.MaxEventsPerSource > 0 {
		for _, src := range []string{corenotif.SourceDSH, corenotif.SourceLark} {
			stmts = append(stmts, sqlite.Statement{
				SQL: `DELETE FROM notification_events
					WHERE source = ?
					  AND rowid NOT IN (
						SELECT rowid FROM notification_events
						WHERE source = ?
						ORDER BY at_ns DESC, observed_at_ns DESC, dedup_key DESC
						LIMIT ?
					  )`,
				Args: []any{src, src, int64(s.retention.MaxEventsPerSource)},
			})
		}
	}
	if len(stmts) == 0 {
		return PruneResult{}, nil
	}

	results, err := s.db.Transaction(ctx, stmts)
	if err != nil {
		return PruneResult{}, fmt.Errorf("prune notification events: %w", err)
	}
	var out PruneResult
	if ageIdx >= 0 && ageIdx < len(results) {
		out.RemovedByAge = int(results[ageIdx].Changes)
	}
	for i := limitStart; i < len(results); i++ {
		out.RemovedByLimit += int(results[i].Changes)
	}
	return out, nil
}

func clampRecentLimit(limit int) int {
	if limit <= 0 {
		return corenotif.DefaultPageLimit
	}
	if limit > DefaultRecentMaxLimit {
		return DefaultRecentMaxLimit
	}
	return limit
}

func rowsToEvents(rows [][]any) []corenotif.NotificationEvent {
	out := make([]corenotif.NotificationEvent, 0, len(rows))
	for _, r := range rows {
		if len(r) < 14 {
			continue
		}
		ev := corenotif.NotificationEvent{
			Source:          asString(r, 0),
			DedupKey:        asString(r, 1),
			DedupClass:      corenotif.DedupClass(asString(r, 2)),
			EventID:         asString(r, 3),
			ConversationRef: asString(r, 4),
			SenderRef:       asString(r, 5),
			Kind:            asString(r, 6),
			TextPreview:     asString(r, 7),
			SessionID:       asString(r, 8),
			At:              time.Unix(0, asInt64(r, 9)).UTC(),
			SourceCursor:    asString(r, 10),
			ObservedAt:      time.Unix(0, asInt64(r, 11)).UTC(),
			TimeUnreliable:  asInt64(r, 13) != 0,
		}
		if r[12] != nil {
			exp := time.Unix(0, asInt64(r, 12)).UTC()
			ev.DedupExpiresAt = &exp
		}
		out = append(out, ev)
	}
	return out
}

func asString(row []any, idx int) string {
	if idx < 0 || idx >= len(row) || row[idx] == nil {
		return ""
	}
	switch v := row[idx].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

func asInt64(row []any, idx int) int64 {
	if idx < 0 || idx >= len(row) || row[idx] == nil {
		return 0
	}
	switch v := row[idx].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		return 0
	}
}
