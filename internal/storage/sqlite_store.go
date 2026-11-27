package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
)

// SQLiteClipboardStoreOptions configures DB pooling, >64 KiB overflow text offloading,
// and slow-query / telemetry observers.
type SQLiteClipboardStoreOptions struct {
	MediaDir           string
	MaxReaders         int
	BusyTimeout        time.Duration
	CacheSizeKB        int
	MmapSizeBytes      int64
	SlowQueryThreshold time.Duration
	Observer           QueryObserver
}

// SQLiteClipboardStore implements pkg/core/clipboard.Store backed by a pooled WAL
// SQLite engine (1 serialized writer + N concurrent ReadOnly readers) + FTS5/Trigram.
type SQLiteClipboardStore struct {
	pool       *DBPool
	mu         sync.RWMutex
	filter     clipboard.PrivacyFilter
	mediaDir   string
	hasTrigram atomic.Bool
	generation atomic.Uint64
}

var _ clipboard.Store = (*SQLiteClipboardStore)(nil)

// NewSQLiteClipboardStore opens or creates the SQLite database at dbPath (or ":memory:")
// and initializes the clipboard_items table, FTS5/Trigram virtual tables, and reader pool.
func NewSQLiteClipboardStore(ctx context.Context, dbPath string, filter clipboard.PrivacyFilter) (*SQLiteClipboardStore, error) {
	return NewSQLiteClipboardStoreWithOptions(ctx, dbPath, filter, SQLiteClipboardStoreOptions{})
}

// NewSQLiteClipboardStoreFromConfig creates a SQLiteClipboardStore using config.ClipboardConfig.
func NewSQLiteClipboardStoreFromConfig(ctx context.Context, dbPath string, cfg config.ClipboardConfig) (*SQLiteClipboardStore, error) {
	return NewSQLiteClipboardStore(ctx, dbPath, clipboard.NewPrivacyFilterFromConfig(cfg))
}

// NewSQLiteClipboardStoreWithOptions opens SQLiteClipboardStore with pooled WAL readers,
// optional telemetry observer, and optional >64 KiB overflow text directory.
func NewSQLiteClipboardStoreWithOptions(ctx context.Context, dbPath string, filter clipboard.PrivacyFilter, opts SQLiteClipboardStoreOptions) (*SQLiteClipboardStore, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pool, err := OpenDBPool(ctx, DBPoolOptions{
		Path:               dbPath,
		MaxReaders:         opts.MaxReaders,
		BusyTimeout:        opts.BusyTimeout,
		CacheSizeKB:        opts.CacheSizeKB,
		MmapSizeBytes:      opts.MmapSizeBytes,
		SlowQueryThreshold: opts.SlowQueryThreshold,
		Observer:           opts.Observer,
	})
	if err != nil {
		return nil, err
	}

	store := &SQLiteClipboardStore{
		pool:     pool,
		filter:   filter,
		mediaDir: strings.TrimSpace(opts.MediaDir),
	}
	if err := store.initSchema(ctx, dbPath); err != nil {
		_ = pool.Close()
		return nil, err
	}
	return store, nil
}

// SetMediaDir configures the content-addressed directory used for >64 KiB overflow text blobs.
func (s *SQLiteClipboardStore) SetMediaDir(dir string) {
	s.mu.Lock()
	s.mediaDir = strings.TrimSpace(dir)
	s.mu.Unlock()
}

func (s *SQLiteClipboardStore) currentMediaDir() string {
	s.mu.RLock()
	dir := s.mediaDir
	s.mu.RUnlock()
	return dir
}

// SetPrivacyFilter updates the active PrivacyFilter (e.g. after config hot-reload).
func (s *SQLiteClipboardStore) SetPrivacyFilter(filter clipboard.PrivacyFilter) {
	s.mu.Lock()
	s.filter = filter
	s.mu.Unlock()
}

// ClipboardDay holds one local-calendar day's captured item count.
type ClipboardDay struct {
	Day   string
	Count int
}

// ClipboardKindStat holds per-kind item counts and byte totals.
type ClipboardKindStat struct {
	Kind  string
	Count int
	Bytes int64
}

// ClipboardDaily returns per-day item counts for the last n local calendar days,
// oldest first, including zero rows for missing days, aggregated directly in SQLite.
func (s *SQLiteClipboardStore) ClipboardDaily(ctx context.Context, n int) ([]ClipboardDay, error) {
	if n <= 0 {
		return []ClipboardDay{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	loc := time.Local
	today := time.Now().In(loc)
	startDate := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(n - 1))
	end := startDate.AddDate(0, 0, n)

	rows, err := s.pool.QueryRead(ctx, "clipboard.stats.daily",
		`SELECT date(created_at_ns / 1000000000, 'unixepoch', 'localtime') AS day_key, COUNT(*)
		 FROM clipboard_items
		 WHERE created_at_ns >= ? AND created_at_ns < ?
		 GROUP BY day_key`,
		startDate.UnixNano(), end.UnixNano(),
	)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(rows.Values))
	for _, row := range rows.Values {
		if len(row) >= 2 {
			counts[str(row, 0)] = int(int64val(row, 1))
		}
	}
	out := make([]ClipboardDay, n)
	for i := range out {
		day := startDate.AddDate(0, 0, i).Format("2006-01-02")
		out[i] = ClipboardDay{Day: day, Count: counts[day]}
	}
	return out, nil
}

// ClipboardKindStats returns current stored item counts and their byte totals.
func (s *SQLiteClipboardStore) ClipboardKindStats(ctx context.Context) ([]ClipboardKindStat, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := s.pool.QueryRead(ctx, "clipboard.stats.kinds",
		`SELECT CASE WHEN kind='' THEN 'text' ELSE kind END, COUNT(*), COALESCE(SUM(byte_size),0)
		 FROM clipboard_items
		 GROUP BY CASE WHEN kind='' THEN 'text' ELSE kind END
		 ORDER BY 1`,
	)
	if err != nil {
		return nil, err
	}
	out := make([]ClipboardKindStat, 0, len(rows.Values))
	for _, row := range rows.Values {
		if len(row) >= 3 {
			out = append(out, ClipboardKindStat{Kind: str(row, 0), Count: int(int64val(row, 1)), Bytes: int64val(row, 2)})
		}
	}
	return out, nil
}

// Close closes the underlying DBPool (pooled readers + writer).
func (s *SQLiteClipboardStore) Close() error {
	if s == nil || s.pool == nil {
		return nil
	}
	return s.pool.Close()
}

func (s *SQLiteClipboardStore) initSchema(ctx context.Context, dbPath string) error {
	db := s.pool.Writer()
	if _, err := Migrate(ctx, db, DBKindApp, MigrationOptions{
		DBPath:      dbPath,
		AllowCreate: true,
	}); err != nil {
		return fmt.Errorf("init clipboard schema: %w", err)
	}
	if err := s.pool.TuneAfterMigrate(ctx); err != nil {
		return err
	}
	if _, err := s.pool.ExecWrite(ctx, "clipboard.init_trigger_ai", clipboardFTSInsertTrigger); err != nil {
		return fmt.Errorf("ensure clipboard FTS insert trigger: %w", err)
	}
	if _, err := s.pool.ExecWrite(ctx, "clipboard.init_trigger_ad", clipboardFTSDeleteTrigger); err != nil {
		return fmt.Errorf("ensure clipboard FTS delete trigger: %w", err)
	}
	if err := s.backfillClipboardMetadata(ctx); err != nil {
		return err
	}
	if err := s.pool.InitReaders(ctx); err != nil {
		return err
	}
	if tRows, err := s.pool.QueryRead(ctx, "clipboard.check_trigram",
		`SELECT 1 FROM sqlite_master WHERE type='table' AND name='clipboard_fts_trigram' LIMIT 1`,
	); err == nil && len(tRows.Values) > 0 {
		s.hasTrigram.Store(true)
	}
	return nil
}

// itemColumns is the column list every item query selects, in rowToItem order.
const itemColumns = `id, content, content_hash, source_bundle_id, source_app_name, created_at_ns, updated_at_ns, is_pinned,
	kind, media_path, media_mime, width, height, blob_sha, thumb_path, byte_size, duration_ms, copy_count,
	content_overflow_path, content_truncated`

// ciItemColumns is itemColumns qualified with the "ci." alias for FTS joins.
var ciItemColumns = qualifyColumns("ci.", itemColumns)

// ciSummaryColumns projects at most 512 characters of content for fast virtual scrolling.
const ciSummaryColumns = `ci.id, substr(ci.content, 1, 512), ci.content_hash, ci.source_bundle_id, ci.source_app_name, ci.created_at_ns, ci.updated_at_ns, ci.is_pinned,
	ci.kind, ci.media_path, ci.media_mime, ci.width, ci.height, ci.blob_sha, ci.thumb_path, ci.byte_size, ci.duration_ms, ci.copy_count,
	ci.content_overflow_path, ci.content_truncated`

func qualifyColumns(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = prefix + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// mediaColumns are added in place to pre-media databases (docs/design/media-preview.md §3).
// FTS keeps indexing content/source_app_name only, so binary payloads never enter it.
var mediaColumns = [][2]string{
	{"kind", "TEXT NOT NULL DEFAULT ''"},
	{"media_path", "TEXT NOT NULL DEFAULT ''"},
	{"media_mime", "TEXT NOT NULL DEFAULT ''"},
	{"width", "INTEGER NOT NULL DEFAULT 0"},
	{"height", "INTEGER NOT NULL DEFAULT 0"},
	{"blob_sha", "TEXT NOT NULL DEFAULT ''"},
	{"thumb_path", "TEXT NOT NULL DEFAULT ''"},
	{"byte_size", "INTEGER NOT NULL DEFAULT 0"},
	{"duration_ms", "INTEGER NOT NULL DEFAULT 0"},
}

const (
	clipboardFTSInsertTrigger = `CREATE TRIGGER IF NOT EXISTS clipboard_items_ai AFTER INSERT ON clipboard_items BEGIN
		INSERT INTO clipboard_fts(rowid, content, source_app_name)
		VALUES (new.rowid, new.content, new.source_app_name);
	END`
	clipboardFTSDeleteTrigger = `CREATE TRIGGER IF NOT EXISTS clipboard_items_ad AFTER DELETE ON clipboard_items BEGIN
		INSERT INTO clipboard_fts(clipboard_fts, rowid, content, source_app_name)
		VALUES ('delete', old.rowid, old.content, old.source_app_name);
	END`
	clipboardFTSUpdateTrigger = `CREATE TRIGGER IF NOT EXISTS clipboard_items_au
	AFTER UPDATE OF content, source_app_name ON clipboard_items BEGIN
		INSERT INTO clipboard_fts(clipboard_fts, rowid, content, source_app_name)
		VALUES ('delete', old.rowid, old.content, old.source_app_name);
		INSERT INTO clipboard_fts(rowid, content, source_app_name)
		VALUES (new.rowid, new.content, new.source_app_name);
	END`
)

// Backfill byte counts and repair the external-content index in one transaction.
func (s *SQLiteClipboardStore) backfillClipboardMetadata(ctx context.Context) error {
	rows, err := s.pool.QueryRead(ctx, "clipboard.backfill_check",
		`SELECT 1 FROM sqlite_schema WHERE name='clipboard_items_au' AND instr(sql, 'AFTER UPDATE OF content, source_app_name')=0
		 UNION ALL SELECT 1 FROM clipboard_items
		 WHERE kind='' OR (kind='text' AND byte_size=0 AND content<>'') LIMIT 1`,
	)
	if err != nil {
		return fmt.Errorf("inspect clipboard metadata backfill: %w", err)
	}
	if len(rows.Values) == 0 {
		return nil
	}
	if _, err := s.pool.TxWrite(ctx, "clipboard.backfill_tx", []sqlite.Statement{
		{SQL: `DROP TRIGGER IF EXISTS clipboard_items_au`},
		{SQL: clipboardFTSUpdateTrigger},
		{SQL: `UPDATE clipboard_items SET kind='text', byte_size=length(CAST(content AS BLOB))
			WHERE kind='' OR (kind='text' AND byte_size=0 AND content<>'')`},
		{SQL: `INSERT INTO clipboard_fts(clipboard_fts) VALUES ('rebuild')`},
	}); err != nil {
		return fmt.Errorf("backfill clipboard metadata and FTS index: %w", err)
	}
	return nil
}

func (s *SQLiteClipboardStore) Record(ctx context.Context, candidate clipboard.CaptureCandidate) (*clipboard.ClipboardItem, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	s.mu.RLock()
	filter := s.filter
	mediaDir := s.mediaDir
	s.mu.RUnlock()

	if !filter.ShouldRecord(candidate) {
		return nil, false, nil
	}
	if err := candidate.Validate(); err != nil {
		return nil, false, err
	}

	now := candidate.Timestamp.UTC()
	if candidate.Timestamp.IsZero() {
		now = time.Now().UTC()
	}
	nowNs := now.UnixNano()
	if candidate.Kind == "" {
		candidate.Kind = clipboard.KindText
	}
	fullContent := candidate.Content
	if candidate.Kind == clipboard.KindText {
		candidate.Media = clipboard.Media{
			Kind:     clipboard.KindText,
			ByteSize: int64(len([]byte(fullContent))),
		}
	}
	hash := candidate.DedupeHash()

	rows, err := s.pool.QueryRead(ctx, "clipboard.record_lookup",
		`SELECT `+itemColumns+`
		 FROM clipboard_items WHERE content_hash = ? LIMIT 1`,
		hash,
	)
	if err != nil {
		return nil, false, fmt.Errorf("query existing clipboard item: %w", err)
	}

	if len(rows.Values) > 0 {
		existing, overflowPath, truncated := rowToItemWithOverflow(rows.Values[0])
		newBundleID := existing.SourceBundleID
		if candidate.SourceBundleID != "" {
			newBundleID = candidate.SourceBundleID
		}
		newAppName := existing.SourceAppName
		if candidate.SourceAppName != "" {
			newAppName = candidate.SourceAppName
		}
		newMedia := existing.Media
		if candidate.IsMedia() {
			newMedia = clipboard.MergeMediaMetadata(newMedia, candidate.Media)
		}
		if existing.CopyCount < 1 {
			existing.CopyCount = 1
		}
		newCopyCount := existing.CopyCount + 1
		if _, err := s.pool.ExecWrite(ctx, "clipboard.record_dedup_update",
			`UPDATE clipboard_items
			 SET updated_at_ns = ?, source_bundle_id = ?, source_app_name = ?,
				 kind = ?, media_path = ?, media_mime = ?, width = ?, height = ?,
				 blob_sha = ?, thumb_path = ?, byte_size = ?, duration_ms = ?,
				 copy_count = ?, content_overflow_path = ?, content_truncated = ?
			 WHERE id = ?`,
			nowNs, newBundleID, newAppName, kindOrText(newMedia.Kind), newMedia.MediaPath,
			newMedia.MediaMime, newMedia.Width, newMedia.Height, newMedia.BlobSHA,
			newMedia.ThumbPath, newMedia.ByteSize, newMedia.DurationMs, newCopyCount,
			overflowPath, truncated, existing.ID,
		); err != nil {
			return nil, false, fmt.Errorf("update deduplicated clipboard item: %w", err)
		}

		existing.Content = fullContent
		existing.UpdatedAt = now
		existing.CopyCount = newCopyCount
		existing.SourceBundleID = newBundleID
		existing.SourceAppName = newAppName
		existing.Media = newMedia
		s.generation.Add(1)
		return &existing, true, nil
	}

	inlineContent := fullContent
	var overflowPath string
	var truncatedFlag int64
	if candidate.Kind == clipboard.KindText && len(fullContent) > clipboard.InlineTextMaxBytes && mediaDir != "" {
		relPath, sha, saveErr := clipboard.NewMediaStore(mediaDir).SaveOverflowText(fullContent)
		if saveErr != nil {
			return nil, false, fmt.Errorf("save overflow clipboard text: %w", saveErr)
		}
		inlineContent, _ = clipboard.TruncateUTF8Bytes(fullContent, clipboard.InlineTextMaxBytes)
		overflowPath = relPath
		truncatedFlag = 1
		candidate.BlobSHA = sha
	}

	id := newItemID(nowNs)
	if _, err := s.pool.ExecWrite(ctx, "clipboard.record_insert",
		`INSERT INTO clipboard_items (id, content, content_hash, source_bundle_id, source_app_name, created_at_ns, updated_at_ns, is_pinned,
			kind, media_path, media_mime, width, height, blob_sha, thumb_path, byte_size, duration_ms, copy_count,
			content_overflow_path, content_truncated)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		id, inlineContent, hash, candidate.SourceBundleID, candidate.SourceAppName, nowNs, nowNs,
		kindOrText(candidate.Kind), candidate.MediaPath, candidate.MediaMime, candidate.Width, candidate.Height,
		candidate.BlobSHA, candidate.ThumbPath, candidate.ByteSize, candidate.DurationMs,
		overflowPath, truncatedFlag,
	); err != nil {
		return nil, false, fmt.Errorf("insert clipboard item: %w", err)
	}

	item := clipboard.ClipboardItem{
		ID:             id,
		Content:        fullContent,
		ContentHash:    hash,
		SourceBundleID: candidate.SourceBundleID,
		SourceAppName:  candidate.SourceAppName,
		CreatedAt:      now,
		UpdatedAt:      now,
		CopyCount:      1,
		IsPinned:       false,
		Media:          candidate.Media,
	}
	s.generation.Add(1)
	return &item, true, nil
}

// GetContent returns the full UTF-8 content for id, transparently rehydrating >64 KiB
// overflow text from MediaStore when content_truncated=1.
func (s *SQLiteClipboardStore) GetContent(ctx context.Context, id string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	rows, err := s.pool.QueryRead(ctx, "clipboard.get_content",
		`SELECT content, content_truncated, content_overflow_path, blob_sha
		 FROM clipboard_items WHERE id = ? LIMIT 1`,
		id,
	)
	if err != nil {
		return "", fmt.Errorf("query clipboard content: %w", err)
	}
	if len(rows.Values) == 0 {
		return "", fmt.Errorf("clipboard item %q not found", id)
	}
	row := rows.Values[0]
	inline := str(row, 0)
	truncated := int64val(row, 1) != 0
	overflowPath := str(row, 2)
	blobSHA := str(row, 3)
	mediaDir := s.currentMediaDir()
	if truncated && overflowPath != "" && mediaDir != "" {
		if full, loadErr := clipboard.NewMediaStore(mediaDir).LoadOverflowText(overflowPath, blobSHA); loadErr == nil {
			return full, nil
		}
	}
	return inline, nil
}

// ListSummaryPage returns a keyset-paginated slice of ClipboardSummary rows (with content
// truncated at 512 chars) ordered by `(is_pinned DESC, updated_at_ns DESC, id ASC)`.
func (s *SQLiteClipboardStore) ListSummaryPage(ctx context.Context, spec clipboard.ClipboardQuerySpec) ([]clipboard.ClipboardSummary, *clipboard.ClipboardCursor, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	limit := spec.Limit
	if limit <= 0 {
		limit = 80
	}

	var joins string
	var conds []string
	var args []any

	text := strings.TrimSpace(spec.Text)
	useTrigram := false
	useFTSPrefix := false
	var matchExpr string
	if text != "" {
		hasSpecialOrCJK := containsCJKOrSpecialPunct(text)
		hasSpace := strings.ContainsAny(text, " \t")
		if s.hasTrigram.Load() && len([]rune(text)) >= 3 && (hasSpecialOrCJK || !hasSpace) {
			useTrigram = true
			matchExpr = buildTrigramMatchExpr(text)
			joins = ` JOIN clipboard_fts_trigram fts ON fts.rowid = ci.rowid`
			conds = append(conds, `clipboard_fts_trigram MATCH ?`)
			args = append(args, matchExpr)
		} else if !hasSpecialOrCJK {
			if ftsQ := buildFTS5PrefixQuery(text); ftsQ != "" {
				useFTSPrefix = true
				joins = ` JOIN clipboard_fts fts ON fts.rowid = ci.rowid`
				conds = append(conds, `clipboard_fts MATCH ?`)
				args = append(args, ftsQ)
			}
		}
		if !useTrigram && !useFTSPrefix {
			pat := "%" + escapeLike(text) + "%"
			conds = append(conds, `(ci.content LIKE ? ESCAPE '\' OR ci.source_app_name LIKE ? ESCAPE '\')`)
			args = append(args, pat, pat)
		}
	}

	if spec.PinnedOnly {
		conds = append(conds, `ci.is_pinned = 1`)
	}
	if spec.Kind != "" {
		switch spec.Kind {
		case clipboard.KindText:
			conds = append(conds, `(ci.kind = '' OR ci.kind = 'text')`)
		default:
			conds = append(conds, `ci.kind = ?`)
			args = append(args, string(spec.Kind))
		}
	}
	if app := strings.TrimSpace(spec.AppNeedle); app != "" {
		pat := "%" + escapeLike(strings.ToLower(app)) + "%"
		conds = append(conds, `(LOWER(ci.source_app_name) LIKE ? ESCAPE '\' OR LOWER(ci.source_bundle_id) LIKE ? ESCAPE '\')`)
		args = append(args, pat, pat)
	}
	if spec.SinceNs > 0 {
		conds = append(conds, `ci.updated_at_ns >= ?`)
		args = append(args, spec.SinceNs)
	}
	if spec.Cursor != nil {
		if spec.Cursor.IsPinned {
			conds = append(conds, `(ci.is_pinned < 1 OR (ci.is_pinned = 1 AND (ci.updated_at_ns < ? OR (ci.updated_at_ns = ? AND ci.id > ?))))`)
		} else {
			conds = append(conds, `(ci.is_pinned = 0 AND (ci.updated_at_ns < ? OR (ci.updated_at_ns = ? AND ci.id > ?)))`)
		}
		args = append(args, spec.Cursor.UpdatedAtNs, spec.Cursor.UpdatedAtNs, spec.Cursor.ID)
	}

	var b strings.Builder
	b.WriteString(`SELECT `)
	b.WriteString(ciSummaryColumns)
	b.WriteString(` FROM clipboard_items ci`)
	b.WriteString(joins)
	if len(conds) > 0 {
		b.WriteString(` WHERE `)
		b.WriteString(strings.Join(conds, ` AND `))
	}
	b.WriteString(` ORDER BY ci.is_pinned DESC, ci.updated_at_ns DESC, ci.id ASC LIMIT ?`)
	args = append(args, limit+1)

	op := "clipboard.list_summary_page"
	if useTrigram {
		op = "clipboard.search_summary_trigram"
	} else if useFTSPrefix {
		op = "clipboard.search_summary_fts"
	} else if text != "" {
		op = "clipboard.search_summary_like"
	}

	rows, err := s.pool.QueryRead(ctx, op, b.String(), args...)
	if err != nil && useTrigram {
		// Defensive fallback to substring LIKE if trigram parser rejects an unusual input.
		specFallback := spec
		useTrigram = false
		_ = specFallback
		return s.listSummaryLikeFallback(ctx, spec)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("list clipboard summary page: %w", err)
	}

	return buildSummaryPageResult(rows, limit)
}

func (s *SQLiteClipboardStore) listSummaryLikeFallback(ctx context.Context, spec clipboard.ClipboardQuerySpec) ([]clipboard.ClipboardSummary, *clipboard.ClipboardCursor, error) {
	limit := spec.Limit
	if limit <= 0 {
		limit = 80
	}
	var conds []string
	var args []any
	if text := strings.TrimSpace(spec.Text); text != "" {
		pat := "%" + escapeLike(text) + "%"
		conds = append(conds, `(ci.content LIKE ? ESCAPE '\' OR ci.source_app_name LIKE ? ESCAPE '\')`)
		args = append(args, pat, pat)
	}
	if spec.PinnedOnly {
		conds = append(conds, `ci.is_pinned = 1`)
	}
	if spec.Kind != "" {
		if spec.Kind == clipboard.KindText {
			conds = append(conds, `(ci.kind = '' OR ci.kind = 'text')`)
		} else {
			conds = append(conds, `ci.kind = ?`)
			args = append(args, string(spec.Kind))
		}
	}
	if app := strings.TrimSpace(spec.AppNeedle); app != "" {
		pat := "%" + escapeLike(strings.ToLower(app)) + "%"
		conds = append(conds, `(LOWER(ci.source_app_name) LIKE ? ESCAPE '\' OR LOWER(ci.source_bundle_id) LIKE ? ESCAPE '\')`)
		args = append(args, pat, pat)
	}
	if spec.SinceNs > 0 {
		conds = append(conds, `ci.updated_at_ns >= ?`)
		args = append(args, spec.SinceNs)
	}
	if spec.Cursor != nil {
		if spec.Cursor.IsPinned {
			conds = append(conds, `(ci.is_pinned < 1 OR (ci.is_pinned = 1 AND (ci.updated_at_ns < ? OR (ci.updated_at_ns = ? AND ci.id > ?))))`)
		} else {
			conds = append(conds, `(ci.is_pinned = 0 AND (ci.updated_at_ns < ? OR (ci.updated_at_ns = ? AND ci.id > ?)))`)
		}
		args = append(args, spec.Cursor.UpdatedAtNs, spec.Cursor.UpdatedAtNs, spec.Cursor.ID)
	}
	var b strings.Builder
	b.WriteString(`SELECT `)
	b.WriteString(ciSummaryColumns)
	b.WriteString(` FROM clipboard_items ci`)
	if len(conds) > 0 {
		b.WriteString(` WHERE `)
		b.WriteString(strings.Join(conds, ` AND `))
	}
	b.WriteString(` ORDER BY ci.is_pinned DESC, ci.updated_at_ns DESC, ci.id ASC LIMIT ?`)
	args = append(args, limit+1)

	rows, err := s.pool.QueryRead(ctx, "clipboard.search_summary_like_fallback", b.String(), args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list clipboard summary fallback: %w", err)
	}
	return buildSummaryPageResult(rows, limit)
}

func buildSummaryPageResult(rows sqlite.Rows, limit int) ([]clipboard.ClipboardSummary, *clipboard.ClipboardCursor, error) {
	hasMore := len(rows.Values) > limit
	count := len(rows.Values)
	if hasMore {
		count = limit
	}
	out := make([]clipboard.ClipboardSummary, 0, count)
	for i := 0; i < count; i++ {
		item, overflowPath, truncated := rowToItemWithOverflow(rows.Values[i])
		out = append(out, clipboard.ClipboardSummary{
			ID:               item.ID,
			Preview:          item.Content,
			ContentHash:      item.ContentHash,
			ContentTruncated: truncated != 0,
			OverflowPath:     overflowPath,
			SourceBundleID:   item.SourceBundleID,
			SourceAppName:    item.SourceAppName,
			CreatedAt:        item.CreatedAt,
			UpdatedAt:        item.UpdatedAt,
			CopyCount:        item.CopyCount,
			IsPinned:         item.IsPinned,
			Media:            item.Media,
		})
	}
	var nextCursor *clipboard.ClipboardCursor
	if hasMore && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = &clipboard.ClipboardCursor{
			IsPinned:    last.IsPinned,
			UpdatedAtNs: last.UpdatedAt.UTC().UnixNano(),
			ID:          last.ID,
		}
	}
	return out, nextCursor, nil
}

func (s *SQLiteClipboardStore) Recent(ctx context.Context, limit int) ([]clipboard.ClipboardItem, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var rows sqlite.Rows
	var err error
	if limit > 0 {
		rows, err = s.pool.QueryRead(ctx, "clipboard.recent",
			`SELECT `+itemColumns+`
			 FROM clipboard_items
			 ORDER BY is_pinned DESC, updated_at_ns DESC, id ASC
			 LIMIT ?`,
			limit,
		)
	} else {
		rows, err = s.pool.QueryRead(ctx, "clipboard.recent_all",
			`SELECT `+itemColumns+`
			 FROM clipboard_items
			 ORDER BY is_pinned DESC, updated_at_ns DESC, id ASC`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("query recent clipboard items: %w", err)
	}
	return s.rehydrateRows(rows), nil
}

func (s *SQLiteClipboardStore) Search(ctx context.Context, query string, limit int) ([]clipboard.ClipboardItem, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return s.Recent(ctx, limit)
	}

	if s.hasTrigram.Load() && len([]rune(trimmed)) >= 3 && (containsCJKOrSpecialPunct(trimmed) || !strings.ContainsAny(trimmed, " \t")) {
		if items, err := s.searchTrigram(ctx, buildTrigramMatchExpr(trimmed), limit); err == nil && len(items) > 0 {
			return items, nil
		}
	}

	ftsQuery := buildFTS5PrefixQuery(trimmed)
	if ftsQuery != "" && !containsCJKOrSpecialPunct(trimmed) {
		items, err := s.searchFTS5(ctx, ftsQuery, limit)
		if err == nil && len(items) > 0 {
			return items, nil
		}
	}

	return s.searchLike(ctx, trimmed, limit)
}

func (s *SQLiteClipboardStore) searchTrigram(ctx context.Context, trigramExpr string, limit int) ([]clipboard.ClipboardItem, error) {
	var rows sqlite.Rows
	var err error
	if limit > 0 {
		rows, err = s.pool.QueryRead(ctx, "clipboard.search_trigram",
			`SELECT `+ciItemColumns+`
			 FROM clipboard_fts_trigram fts
			 JOIN clipboard_items ci ON ci.rowid = fts.rowid
			 WHERE clipboard_fts_trigram MATCH ?
			 ORDER BY ci.is_pinned DESC, ci.updated_at_ns DESC, ci.id ASC
			 LIMIT ?`,
			trigramExpr, limit,
		)
	} else {
		rows, err = s.pool.QueryRead(ctx, "clipboard.search_trigram",
			`SELECT `+ciItemColumns+`
			 FROM clipboard_fts_trigram fts
			 JOIN clipboard_items ci ON ci.rowid = fts.rowid
			 WHERE clipboard_fts_trigram MATCH ?
			 ORDER BY ci.is_pinned DESC, ci.updated_at_ns DESC, ci.id ASC`,
			trigramExpr,
		)
	}
	if err != nil {
		return nil, err
	}
	return s.rehydrateRows(rows), nil
}

func (s *SQLiteClipboardStore) searchFTS5(ctx context.Context, ftsQuery string, limit int) ([]clipboard.ClipboardItem, error) {
	var rows sqlite.Rows
	var err error
	if limit > 0 {
		rows, err = s.pool.QueryRead(ctx, "clipboard.search_fts5",
			`SELECT `+ciItemColumns+`
			 FROM clipboard_fts fts
			 JOIN clipboard_items ci ON ci.rowid = fts.rowid
			 WHERE clipboard_fts MATCH ?
			 ORDER BY ci.is_pinned DESC, bm25(clipboard_fts) ASC, ci.updated_at_ns DESC, ci.id ASC
			 LIMIT ?`,
			ftsQuery, limit,
		)
	} else {
		rows, err = s.pool.QueryRead(ctx, "clipboard.search_fts5",
			`SELECT `+ciItemColumns+`
			 FROM clipboard_fts fts
			 JOIN clipboard_items ci ON ci.rowid = fts.rowid
			 WHERE clipboard_fts MATCH ?
			 ORDER BY ci.is_pinned DESC, bm25(clipboard_fts) ASC, ci.updated_at_ns DESC, ci.id ASC`,
			ftsQuery,
		)
	}
	if err != nil {
		return nil, err
	}
	return s.rehydrateRows(rows), nil
}

func (s *SQLiteClipboardStore) searchLike(ctx context.Context, rawQuery string, limit int) ([]clipboard.ClipboardItem, error) {
	pattern := "%" + escapeLike(rawQuery) + "%"
	var rows sqlite.Rows
	var err error
	if limit > 0 {
		rows, err = s.pool.QueryRead(ctx, "clipboard.search_like",
			`SELECT `+itemColumns+`
			 FROM clipboard_items
			 WHERE content LIKE ? ESCAPE '\' OR source_app_name LIKE ? ESCAPE '\'
			 ORDER BY is_pinned DESC, updated_at_ns DESC, id ASC
			 LIMIT ?`,
			pattern, pattern, limit,
		)
	} else {
		rows, err = s.pool.QueryRead(ctx, "clipboard.search_like",
			`SELECT `+itemColumns+`
			 FROM clipboard_items
			 WHERE content LIKE ? ESCAPE '\' OR source_app_name LIKE ? ESCAPE '\'
			 ORDER BY is_pinned DESC, updated_at_ns DESC, id ASC`,
			pattern, pattern,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("like search clipboard items: %w", err)
	}
	return s.rehydrateRows(rows), nil
}

func (s *SQLiteClipboardStore) Pin(ctx context.Context, id string, pinned bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	val := int64(0)
	if pinned {
		val = 1
	}
	res, err := s.pool.ExecWrite(ctx, "clipboard.pin", `UPDATE clipboard_items SET is_pinned = ? WHERE id = ?`, val, id)
	if err != nil {
		return fmt.Errorf("pin clipboard item: %w", err)
	}
	if res.Changes == 0 {
		return fmt.Errorf("clipboard item %q not found", id)
	}
	s.generation.Add(1)
	return nil
}

func (s *SQLiteClipboardStore) Delete(ctx context.Context, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.pool.ExecWrite(ctx, "clipboard.delete", `DELETE FROM clipboard_items WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete clipboard item: %w", err)
	}
	s.generation.Add(1)
	return nil
}

func (s *SQLiteClipboardStore) Clear(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.pool.ExecWrite(ctx, "clipboard.clear", `DELETE FROM clipboard_items`); err != nil {
		return fmt.Errorf("clear clipboard items: %w", err)
	}
	s.generation.Add(1)
	return nil
}

// ClearUnpinned deletes all non-pinned entries while strictly preserving pinned items.
func (s *SQLiteClipboardStore) ClearUnpinned(ctx context.Context) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := s.pool.ExecWrite(ctx, "clipboard.clear_unpinned", `DELETE FROM clipboard_items WHERE is_pinned = 0`)
	if err != nil {
		return 0, fmt.Errorf("clear unpinned clipboard items: %w", err)
	}
	s.generation.Add(1)
	return int(res.Changes), nil
}

func (s *SQLiteClipboardStore) PruneExpired(ctx context.Context, retentionDays int, maxItems int) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	removed := 0
	if retentionDays > 0 {
		cutoffNs := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour).UnixNano()
		res, err := s.pool.ExecWrite(ctx, "clipboard.prune_expired",
			`DELETE FROM clipboard_items WHERE is_pinned = 0 AND updated_at_ns < ?`,
			cutoffNs,
		)
		if err != nil {
			return 0, fmt.Errorf("prune expired clipboard items: %w", err)
		}
		removed += int(res.Changes)
	}

	if maxItems > 0 {
		rows, err := s.pool.QueryRead(ctx, "clipboard.prune_count", `SELECT COUNT(*) FROM clipboard_items`)
		if err != nil {
			return removed, fmt.Errorf("count clipboard items: %w", err)
		}
		if len(rows.Values) > 0 {
			total, _ := rows.Values[0][0].(int64)
			if int(total) > maxItems {
				excess := int(total) - maxItems
				res, err := s.pool.ExecWrite(ctx, "clipboard.prune_overflow",
					`DELETE FROM clipboard_items
					 WHERE id IN (
						SELECT id FROM clipboard_items
						WHERE is_pinned = 0
						ORDER BY updated_at_ns ASC, id DESC
						LIMIT ?
					 )`,
					excess,
				)
				if err != nil {
					return removed, fmt.Errorf("prune overflow clipboard items: %w", err)
				}
				removed += int(res.Changes)
			}
		}
	}

	s.generation.Add(1)
	return removed, nil
}

// Generation returns a monotonically increasing mutation generation.
func (s *SQLiteClipboardStore) Generation() uint64 { return s.generation.Load() }

// DB exposes the shared writer database handle for sibling stores.
func (s *SQLiteClipboardStore) DB() *sqlite.DB { return s.pool.Writer() }

// Pool exposes the underlying DBPool for maintenance, stats, and capture_assets.
func (s *SQLiteClipboardStore) Pool() *DBPool { return s.pool }

func newItemID(nowNs int64) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("clip-%d-%s", nowNs, hex.EncodeToString(b[:]))
}

func (s *SQLiteClipboardStore) rehydrateRows(rows sqlite.Rows) []clipboard.ClipboardItem {
	mediaDir := s.currentMediaDir()
	var ms *clipboard.MediaStore
	items := make([]clipboard.ClipboardItem, 0, len(rows.Values))
	for _, row := range rows.Values {
		item, overflowPath, truncated := rowToItemWithOverflow(row)
		if truncated != 0 && overflowPath != "" && mediaDir != "" {
			if ms == nil {
				ms = clipboard.NewMediaStore(mediaDir)
			}
			if full, err := ms.LoadOverflowText(overflowPath, item.BlobSHA); err == nil {
				item.Content = full
			}
		}
		items = append(items, item)
	}
	return items
}

func rowToItemWithOverflow(row []any) (item clipboard.ClipboardItem, overflowPath string, truncated int64) {
	item = rowToItem(row)
	overflowPath = str(row, 18)
	truncated = int64val(row, 19)
	return item, overflowPath, truncated
}

func rowToItem(row []any) clipboard.ClipboardItem {
	s := func(idx int) string {
		if idx < len(row) {
			if v, ok := row[idx].(string); ok {
				return v
			}
		}
		return ""
	}
	num := func(idx int) int64 {
		if idx < len(row) {
			if v, ok := row[idx].(int64); ok {
				return v
			}
		}
		return 0
	}
	copyCount := int(num(17))
	if copyCount < 1 {
		copyCount = 1
	}
	return clipboard.ClipboardItem{
		ID:             s(0),
		Content:        s(1),
		ContentHash:    s(2),
		SourceBundleID: s(3),
		SourceAppName:  s(4),
		CreatedAt:      time.Unix(0, num(5)).UTC(),
		UpdatedAt:      time.Unix(0, num(6)).UTC(),
		CopyCount:      copyCount,
		IsPinned:       num(7) != 0,
		Media: clipboard.Media{
			Kind:       textKindToZero(clipboard.Kind(s(8))),
			MediaPath:  s(9),
			MediaMime:  s(10),
			Width:      int(num(11)),
			Height:     int(num(12)),
			BlobSHA:    s(13),
			ThumbPath:  s(14),
			ByteSize:   num(15),
			DurationMs: num(16),
		},
	}
}

func kindOrText(k clipboard.Kind) string {
	if k == "" {
		return string(clipboard.KindText)
	}
	return string(k)
}

func textKindToZero(k clipboard.Kind) clipboard.Kind {
	if k == clipboard.KindText {
		return ""
	}
	return k
}

func buildTrigramMatchExpr(raw string) string {
	escaped := strings.ReplaceAll(raw, `"`, `""`)
	return `"` + escaped + `"`
}

// buildFTS5PrefixQuery splits raw into safe FTS5 prefix tokens: `"token"*`.
func buildFTS5PrefixQuery(raw string) string {
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		tok := strings.ReplaceAll(cur.String(), `"`, `""`)
		tokens = append(tokens, `"`+tok+`"*`)
		cur.Reset()
	}

	for _, r := range raw {
		if unicodeIsWord(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()

	return strings.Join(tokens, " ")
}

func unicodeIsWord(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

func containsCJKOrSpecialPunct(s string) bool {
	for _, r := range s {
		if r > 0x7F || (!unicodeIsWord(r) && r != ' ' && r != '\t' && r != '\n' && r != '\r') {
			return true
		}
	}
	return false
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
