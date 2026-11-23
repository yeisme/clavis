package agentsession

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	core "github.com/yeisme/clavis/pkg/core/agentsession"
)

// CodexSQLiteReader is the platform adapter for the Codex state database.
// It opens the database read-only for every bounded discovery call.
type CodexSQLiteReader struct{ path string }

func NewCodexSQLiteReader(path string) (*CodexSQLiteReader, error) {
	trimmed := strings.TrimSpace(path)
	if strings.Contains(strings.ToLower(trimmed), "immutable=1") {
		return nil, fmt.Errorf("codex: immutable=1 is forbidden; use mode=ro")
	}
	if trimmed == "" {
		return nil, fmt.Errorf("codex: empty database path")
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "file:") {
		return &CodexSQLiteReader{path: trimmed}, nil
	}
	return &CodexSQLiteReader{path: filepath.Clean(trimmed)}, nil
}

func (r *CodexSQLiteReader) Threads(ctx context.Context, limit int) ([]core.CodexThread, error) {
	if r == nil || strings.TrimSpace(r.path) == "" {
		return nil, fmt.Errorf("codex: reader is not configured")
	}
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	uri := readOnlyURI(r.path)
	db, err := sqlite.Open(ctx, uri, sqlite.OpenOptions{ReadOnly: true, BusyTimeout: 100 * time.Millisecond})
	if err != nil {
		// Some bundled SQLite builds do not enable URI filenames. The plugin's
		// ReadOnly flag is still safe and is never replaced by a writable open.
		db, err = sqlite.Open(ctx, r.path, sqlite.OpenOptions{ReadOnly: true, BusyTimeout: 100 * time.Millisecond})
		if err != nil {
			return nil, fmt.Errorf("open read-only Codex state: %w", err)
		}
	}
	defer db.Close()
	rows, err := db.Query(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='threads'")
	if err != nil {
		return nil, fmt.Errorf("inspect threads table: %w", err)
	}
	if len(rows.Values) == 0 {
		return nil, fmt.Errorf("threads table not found")
	}
	cols, err := db.Query(ctx, "PRAGMA table_info(threads)")
	if err != nil {
		return nil, fmt.Errorf("inspect threads schema: %w", err)
	}
	known := map[string]bool{}
	for _, row := range cols.Values {
		if len(row) > 1 {
			if name, ok := row[1].(string); ok {
				known[name] = true
			}
		}
	}
	idCol := pick(known, "id", "thread_id", "conversation_id")
	if idCol == "" {
		return nil, fmt.Errorf("threads has no stable id column")
	}
	selects := []string{idCol}
	for _, col := range []string{"title", pick(known, "cwd", "workspace", "working_directory"), pick(known, "updated_at", "last_activity_at", "updatedAt", "lastActivityAt"), pick(known, "status", "state"), pick(known, "last_message", "lastMessage"), pick(known, "current_action", "currentAction"), pick(known, "result_line", "resultLine"), pick(known, "awaiting_human", "awaitingHuman"), pick(known, "pending_review", "pendingReview")} {
		if col != "" && known[col] {
			selects = append(selects, col)
		}
	}
	orderCol := pick(known, "updated_at", "last_activity_at", "updatedAt", "lastActivityAt", idCol)
	quoted := make([]string, len(selects))
	for i, col := range selects {
		quoted[i] = quoteIdentifier(col)
	}
	query := "SELECT " + strings.Join(quoted, ",") + " FROM threads ORDER BY " + quoteIdentifier(orderCol) + " DESC LIMIT ?"
	rows, err = db.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("query threads: %w", err)
	}
	out := make([]core.CodexThread, 0, len(rows.Values))
	for _, row := range rows.Values {
		if len(row) == 0 {
			continue
		}
		thread := core.CodexThread{}
		for i, col := range selects {
			value := text(row[i])
			switch col {
			case idCol:
				thread.ID = value
			case "title":
				thread.Title = value
			case "cwd", "workspace", "working_directory":
				if thread.Workspace == "" {
					thread.Workspace = value
				}
			case "status", "state":
				thread.Status = value
			case "last_message", "lastMessage":
				thread.LastMessage = value
			case "current_action", "currentAction":
				thread.CurrentAction = value
			case "result_line", "resultLine":
				thread.ResultLine = value
			case "awaiting_human", "awaitingHuman":
				thread.AwaitingHuman = boolValue(row[i])
			case "pending_review", "pendingReview":
				thread.PendingReview = boolValue(row[i])
			default:
				if at := dbTime(row[i]); at.After(thread.LastActivityAt) {
					thread.LastActivityAt = at
				}
			}
		}
		if thread.ID != "" {
			out = append(out, thread)
		}
	}
	return out, nil
}

func readOnlyURI(raw string) string {
	if strings.HasPrefix(strings.ToLower(raw), "file:") {
		parsed, err := url.Parse(raw)
		if err == nil {
			query := parsed.Query()
			query.Set("mode", "ro")
			parsed.RawQuery = query.Encode()
			return parsed.String()
		}
	}
	query := url.Values{}
	query.Set("mode", "ro")
	return (&url.URL{Scheme: "file", Path: raw, RawQuery: query.Encode()}).String()
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func pick(known map[string]bool, names ...string) string {
	for _, name := range names {
		if known[name] {
			return name
		}
	}
	return ""
}
func text(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	if b, ok := value.([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(value)
}
func dbTime(value any) time.Time {
	if value == nil {
		return time.Time{}
	}
	if at, ok := value.(time.Time); ok {
		return at
	}
	s := text(value)
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if n, ok := value.(int64); ok {
		if n > 1e12 {
			return time.UnixMilli(n)
		}
		return time.Unix(n, 0)
	}
	if n, ok := value.(int); ok {
		return time.Unix(int64(n), 0)
	}
	if n, ok := value.(float64); ok {
		if n > 1e12 {
			return time.UnixMilli(int64(n))
		}
		return time.Unix(int64(n), 0)
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
		if n > 1e12 {
			return time.UnixMilli(n)
		}
		return time.Unix(n, 0)
	}
	return time.Time{}
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case int64:
		return typed != 0
	case int:
		return typed != 0
	case float64:
		return typed != 0
	case []byte:
		value = string(typed)
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
		return strings.TrimSpace(typed) == "1"
	}
	return false
}
