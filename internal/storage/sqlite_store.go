package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
)

// SQLiteClipboardStore implements pkg/core/clipboard.Store backed by MyGo's SQLite plugin + FTS5.
type SQLiteClipboardStore struct {
	db     *sqlite.DB
	mu     sync.RWMutex
	filter clipboard.PrivacyFilter
}

var _ clipboard.Store = (*SQLiteClipboardStore)(nil)

// NewSQLiteClipboardStore opens or creates the SQLite database at dbPath (or ":memory:")
// and initializes the clipboard_items table, FTS5 virtual table, and sync triggers.
func NewSQLiteClipboardStore(ctx context.Context, dbPath string, filter clipboard.PrivacyFilter) (*SQLiteClipboardStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, fmt.Errorf("create clipboard db dir: %w", err)
			}
		}
	}

	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		return nil, fmt.Errorf("open clipboard sqlite db: %w", err)
	}

	store := &SQLiteClipboardStore{
		db:     db,
		filter: filter,
	}
	if err := store.initSchema(ctx, dbPath != ":memory:"); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// NewSQLiteClipboardStoreFromConfig creates a SQLiteClipboardStore using config.ClipboardConfig.
func NewSQLiteClipboardStoreFromConfig(ctx context.Context, dbPath string, cfg config.ClipboardConfig) (*SQLiteClipboardStore, error) {
	return NewSQLiteClipboardStore(ctx, dbPath, clipboard.NewPrivacyFilterFromConfig(cfg))
}

// SetPrivacyFilter updates the active PrivacyFilter (e.g. after config hot-reload).
func (s *SQLiteClipboardStore) SetPrivacyFilter(filter clipboard.PrivacyFilter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filter = filter
}

// Close closes the underlying SQLite database connection.
func (s *SQLiteClipboardStore) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteClipboardStore) initSchema(ctx context.Context, enableWAL bool) error {
	if enableWAL {
		// PRAGMA journal_mode=WAL is single-statement and safe outside transactions.
		if _, err := s.db.Execute(ctx, `PRAGMA journal_mode=WAL`); err != nil {
			return fmt.Errorf("enable WAL mode: %w", err)
		}
	}

	// Execute each DDL statement individually (plugins/sqlite executes one statement per Execute call
	// and enforces authorizer restrictions on transaction control / ATTACH).
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS clipboard_items (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			content_hash TEXT NOT NULL UNIQUE,
			source_bundle_id TEXT NOT NULL DEFAULT '',
			source_app_name TEXT NOT NULL DEFAULT '',
			created_at_ns INTEGER NOT NULL,
			updated_at_ns INTEGER NOT NULL,
			is_pinned INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_clipboard_items_order ON clipboard_items(is_pinned DESC, updated_at_ns DESC, id ASC)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS clipboard_fts USING fts5(
			content,
			source_app_name,
			content='clipboard_items',
			content_rowid='rowid'
		)`,
		`CREATE TRIGGER IF NOT EXISTS clipboard_items_ai AFTER INSERT ON clipboard_items BEGIN
			INSERT INTO clipboard_fts(rowid, content, source_app_name)
			VALUES (new.rowid, new.content, new.source_app_name);
		END`,
		`CREATE TRIGGER IF NOT EXISTS clipboard_items_ad AFTER DELETE ON clipboard_items BEGIN
			INSERT INTO clipboard_fts(clipboard_fts, rowid, content, source_app_name)
			VALUES ('delete', old.rowid, old.content, old.source_app_name);
		END`,
		`CREATE TRIGGER IF NOT EXISTS clipboard_items_au AFTER UPDATE ON clipboard_items BEGIN
			INSERT INTO clipboard_fts(clipboard_fts, rowid, content, source_app_name)
			VALUES ('delete', old.rowid, old.content, old.source_app_name);
			INSERT INTO clipboard_fts(rowid, content, source_app_name)
			VALUES (new.rowid, new.content, new.source_app_name);
		END`,
	}
	for _, stmt := range ddl {
		if _, err := s.db.Execute(ctx, stmt); err != nil {
			return fmt.Errorf("init clipboard schema: %w", err)
		}
	}
	return s.migrateMediaColumns(ctx)
}

// itemColumns is the column list every item query selects, in rowToItem order.
const itemColumns = `id, content, content_hash, source_bundle_id, source_app_name, created_at_ns, updated_at_ns, is_pinned,
	kind, media_path, media_mime, width, height, blob_sha, thumb_path, byte_size, duration_ms`

// ciItemColumns is itemColumns qualified with the "ci." alias for FTS joins.
var ciItemColumns = qualifyColumns("ci.", itemColumns)

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
	{"kind", "TEXT NOT NULL DEFAULT 'text'"},
	{"media_path", "TEXT NOT NULL DEFAULT ''"},
	{"media_mime", "TEXT NOT NULL DEFAULT ''"},
	{"width", "INTEGER NOT NULL DEFAULT 0"},
	{"height", "INTEGER NOT NULL DEFAULT 0"},
	{"blob_sha", "TEXT NOT NULL DEFAULT ''"},
	{"thumb_path", "TEXT NOT NULL DEFAULT ''"},
	{"byte_size", "INTEGER NOT NULL DEFAULT 0"},
	{"duration_ms", "INTEGER NOT NULL DEFAULT 0"},
}

func (s *SQLiteClipboardStore) migrateMediaColumns(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `PRAGMA table_info(clipboard_items)`)
	if err != nil {
		return fmt.Errorf("inspect clipboard schema: %w", err)
	}
	nameIdx := slices.Index(rows.Columns, "name")
	have := map[string]bool{}
	for _, r := range rows.Values {
		if nameIdx >= 0 && nameIdx < len(r) {
			if n, ok := r[nameIdx].(string); ok {
				have[n] = true
			}
		}
	}
	for _, col := range mediaColumns {
		if have[col[0]] {
			continue
		}
		if _, err := s.db.Execute(ctx, `ALTER TABLE clipboard_items ADD COLUMN `+col[0]+` `+col[1]); err != nil {
			return fmt.Errorf("add clipboard column %s: %w", col[0], err)
		}
	}
	return nil
}

func (s *SQLiteClipboardStore) Record(ctx context.Context, candidate clipboard.CaptureCandidate) (*clipboard.ClipboardItem, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	s.mu.RLock()
	filter := s.filter
	s.mu.RUnlock()

	if !filter.ShouldRecord(candidate) {
		return nil, false, nil
	}

	now := candidate.Timestamp.UTC()
	if candidate.Timestamp.IsZero() {
		now = time.Now().UTC()
	}
	nowNs := now.UnixNano()
	hash := candidate.DedupeHash()

	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(ctx,
		`SELECT `+itemColumns+`
		 FROM clipboard_items WHERE content_hash = ? LIMIT 1`,
		hash,
	)
	if err != nil {
		return nil, false, fmt.Errorf("query existing clipboard item: %w", err)
	}

	if len(rows.Values) > 0 {
		existing := rowToItem(rows.Values[0])
		newBundleID := existing.SourceBundleID
		if candidate.SourceBundleID != "" {
			newBundleID = candidate.SourceBundleID
		}
		newAppName := existing.SourceAppName
		if candidate.SourceAppName != "" {
			newAppName = candidate.SourceAppName
		}

		if _, err := s.db.Execute(ctx,
			`UPDATE clipboard_items
			 SET updated_at_ns = ?, source_bundle_id = ?, source_app_name = ?
			 WHERE id = ?`,
			nowNs, newBundleID, newAppName, existing.ID,
		); err != nil {
			return nil, false, fmt.Errorf("update deduplicated clipboard item: %w", err)
		}

		existing.UpdatedAt = now
		existing.SourceBundleID = newBundleID
		existing.SourceAppName = newAppName
		return &existing, true, nil
	}

	id := newItemID(nowNs)
	if _, err := s.db.Execute(ctx,
		`INSERT INTO clipboard_items (id, content, content_hash, source_bundle_id, source_app_name, created_at_ns, updated_at_ns, is_pinned,
			kind, media_path, media_mime, width, height, blob_sha, thumb_path, byte_size, duration_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, candidate.Content, hash, candidate.SourceBundleID, candidate.SourceAppName, nowNs, nowNs,
		kindOrText(candidate.Kind), candidate.MediaPath, candidate.MediaMime, candidate.Width, candidate.Height,
		candidate.BlobSHA, candidate.ThumbPath, candidate.ByteSize, candidate.DurationMs,
	); err != nil {
		return nil, false, fmt.Errorf("insert clipboard item: %w", err)
	}

	item := clipboard.ClipboardItem{
		ID:             id,
		Content:        candidate.Content,
		ContentHash:    hash,
		SourceBundleID: candidate.SourceBundleID,
		SourceAppName:  candidate.SourceAppName,
		CreatedAt:      now,
		UpdatedAt:      now,
		IsPinned:       false,
		Media:          candidate.Media,
	}
	return &item, true, nil
}

func (s *SQLiteClipboardStore) Recent(ctx context.Context, limit int) ([]clipboard.ClipboardItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var rows sqlite.Rows
	var err error
	if limit > 0 {
		rows, err = s.db.Query(ctx,
			`SELECT `+itemColumns+`
			 FROM clipboard_items
			 ORDER BY is_pinned DESC, updated_at_ns DESC, id ASC
			 LIMIT ?`,
			limit,
		)
	} else {
		rows, err = s.db.Query(ctx,
			`SELECT `+itemColumns+`
			 FROM clipboard_items
			 ORDER BY is_pinned DESC, updated_at_ns DESC, id ASC`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("query recent clipboard items: %w", err)
	}
	return rowsToItems(rows), nil
}

func (s *SQLiteClipboardStore) Search(ctx context.Context, query string, limit int) ([]clipboard.ClipboardItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return s.Recent(ctx, limit)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	ftsQuery := buildFTS5PrefixQuery(trimmed)
	if ftsQuery != "" && !containsCJKOrSpecialPunct(trimmed) {
		items, err := s.searchFTS5(ctx, ftsQuery, limit)
		if err == nil && len(items) > 0 {
			return items, nil
		}
	}

	return s.searchLike(ctx, trimmed, limit)
}

func (s *SQLiteClipboardStore) searchFTS5(ctx context.Context, ftsQuery string, limit int) ([]clipboard.ClipboardItem, error) {
	var rows sqlite.Rows
	var err error
	if limit > 0 {
		rows, err = s.db.Query(ctx,
			`SELECT `+ciItemColumns+`
			 FROM clipboard_fts fts
			 JOIN clipboard_items ci ON ci.rowid = fts.rowid
			 WHERE clipboard_fts MATCH ?
			 ORDER BY ci.is_pinned DESC, ci.updated_at_ns DESC, ci.id ASC
			 LIMIT ?`,
			ftsQuery, limit,
		)
	} else {
		rows, err = s.db.Query(ctx,
			`SELECT `+ciItemColumns+`
			 FROM clipboard_fts fts
			 JOIN clipboard_items ci ON ci.rowid = fts.rowid
			 WHERE clipboard_fts MATCH ?
			 ORDER BY ci.is_pinned DESC, ci.updated_at_ns DESC, ci.id ASC`,
			ftsQuery,
		)
	}
	if err != nil {
		return nil, err
	}
	return rowsToItems(rows), nil
}

func (s *SQLiteClipboardStore) searchLike(ctx context.Context, rawQuery string, limit int) ([]clipboard.ClipboardItem, error) {
	pattern := "%" + escapeLike(rawQuery) + "%"
	var rows sqlite.Rows
	var err error
	if limit > 0 {
		rows, err = s.db.Query(ctx,
			`SELECT `+itemColumns+`
			 FROM clipboard_items
			 WHERE content LIKE ? ESCAPE '\' OR source_app_name LIKE ? ESCAPE '\'
			 ORDER BY is_pinned DESC, updated_at_ns DESC, id ASC
			 LIMIT ?`,
			pattern, pattern, limit,
		)
	} else {
		rows, err = s.db.Query(ctx,
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
	return rowsToItems(rows), nil
}

func (s *SQLiteClipboardStore) Pin(ctx context.Context, id string, pinned bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	val := int64(0)
	if pinned {
		val = 1
	}
	res, err := s.db.Execute(ctx, `UPDATE clipboard_items SET is_pinned = ? WHERE id = ?`, val, id)
	if err != nil {
		return fmt.Errorf("pin clipboard item: %w", err)
	}
	if res.Changes == 0 {
		return fmt.Errorf("clipboard item %q not found", id)
	}
	return nil
}

func (s *SQLiteClipboardStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.Execute(ctx, `DELETE FROM clipboard_items WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete clipboard item: %w", err)
	}
	return nil
}

func (s *SQLiteClipboardStore) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.Execute(ctx, `DELETE FROM clipboard_items`); err != nil {
		return fmt.Errorf("clear clipboard items: %w", err)
	}
	return nil
}

func (s *SQLiteClipboardStore) PruneExpired(ctx context.Context, retentionDays int, maxItems int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	if retentionDays > 0 {
		cutoffNs := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour).UnixNano()
		res, err := s.db.Execute(ctx,
			`DELETE FROM clipboard_items WHERE is_pinned = 0 AND updated_at_ns < ?`,
			cutoffNs,
		)
		if err != nil {
			return 0, fmt.Errorf("prune expired clipboard items: %w", err)
		}
		removed += int(res.Changes)
	}

	if maxItems > 0 {
		rows, err := s.db.Query(ctx, `SELECT COUNT(*) FROM clipboard_items`)
		if err != nil {
			return removed, fmt.Errorf("count clipboard items: %w", err)
		}
		if len(rows.Values) > 0 {
			total, _ := rows.Values[0][0].(int64)
			if int(total) > maxItems {
				excess := int(total) - maxItems
				res, err := s.db.Execute(ctx,
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

	return removed, nil
}

func contentSHA256(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func newItemID(nowNs int64) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("clip-%d-%s", nowNs, hex.EncodeToString(b[:]))
}

func rowToItem(row []any) clipboard.ClipboardItem {
	str := func(idx int) string {
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
	return clipboard.ClipboardItem{
		ID:             str(0),
		Content:        str(1),
		ContentHash:    str(2),
		SourceBundleID: str(3),
		SourceAppName:  str(4),
		CreatedAt:      time.Unix(0, num(5)).UTC(),
		UpdatedAt:      time.Unix(0, num(6)).UTC(),
		IsPinned:       num(7) != 0,
		Media: clipboard.Media{
			Kind:       textKindToZero(clipboard.Kind(str(8))),
			MediaPath:  str(9),
			MediaMime:  str(10),
			Width:      int(num(11)),
			Height:     int(num(12)),
			BlobSHA:    str(13),
			ThumbPath:  str(14),
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

// textKindToZero keeps text rows' Media zero-valued so they compare equal to pre-media items.
func textKindToZero(k clipboard.Kind) clipboard.Kind {
	if k == clipboard.KindText {
		return ""
	}
	return k
}

func rowsToItems(rows sqlite.Rows) []clipboard.ClipboardItem {
	items := make([]clipboard.ClipboardItem, 0, len(rows.Values))
	for _, row := range rows.Values {
		items = append(items, rowToItem(row))
	}
	return items
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
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()

	return strings.Join(tokens, " ")
}

func containsCJKOrSpecialPunct(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			return true
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsSpace(r) && r != '_' {
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
