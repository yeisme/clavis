package agentsession

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	core "github.com/yeisme/clavis/pkg/core/agentsession"
)

// CodexSQLiteReader is the platform adapter for the Codex state database.
// It opens the database read-only for every bounded discovery call.
type CodexSQLiteReader struct{ path string }

func NewCodexSQLiteReader(path string) (*CodexSQLiteReader, error) {
	if strings.Contains(path, "immutable=1") {
		return nil, fmt.Errorf("codex: immutable=1 is forbidden; use mode=ro")
	}
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("codex: empty database path")
	}
	return &CodexSQLiteReader{path: filepath.Clean(path)}, nil
}

func (r *CodexSQLiteReader) Threads(ctx context.Context, limit int) ([]core.CodexThread, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	uri := "file:" + r.path + "?mode=ro"
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
	if err != nil || len(rows.Values) == 0 {
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
	for _, col := range []string{"title", "cwd", "workspace", "updated_at", "last_activity_at", "status", "state", "last_message"} {
		if known[col] {
			selects = append(selects, col)
		}
	}
	rows, err = db.Query(ctx, "SELECT "+strings.Join(selects, ",")+" FROM threads ORDER BY rowid DESC LIMIT ?", limit)
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
			case "cwd", "workspace":
				if thread.Workspace == "" {
					thread.Workspace = value
				}
			case "status", "state":
				thread.Status = value
			case "last_message":
				thread.LastMessage = value
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

func pick(known map[string]bool, names ...string) string {
	for _, name := range names {
		if known[name] {
			return name
		}
	}
	return ""
}
func text(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	if b, ok := value.([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(value)
}
func dbTime(value any) time.Time {
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
	return time.Time{}
}
