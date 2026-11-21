package agentsession

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
)

func discoverCodex(ctx context.Context, opts Options) ([]Session, []Diagnostic) {
	if strings.Contains(opts.CodexDB, "immutable=1") {
		return nil, []Diagnostic{{"codex", "unsafe_read_mode", "immutable=1 is forbidden; use mode=ro"}}
	}
	if _, err := os.Stat(opts.CodexDB); err != nil {
		return nil, nil
	}
	uri := "file:" + filepath.Clean(opts.CodexDB) + "?mode=ro"
	db, err := sqlite.Open(ctx, uri, sqlite.OpenOptions{ReadOnly: true, BusyTimeout: 100 * time.Millisecond})
	if err != nil {
		// mygo's bundled SQLite engine on some builds does not enable URI
		// filenames; its ReadOnly flag is the equivalent safe open. Never fall
		// back to a writable connection and retain a diagnostic for observability.
		db, err = sqlite.Open(ctx, filepath.Clean(opts.CodexDB), sqlite.OpenOptions{ReadOnly: true, BusyTimeout: 100 * time.Millisecond})
		if err != nil {
			return nil, []Diagnostic{{"codex", "open_failed", "state_5.sqlite could not be opened read-only"}}
		}
	}
	defer db.Close()
	rows, err := db.Query(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='threads'")
	if err != nil || len(rows.Values) == 0 {
		return nil, []Diagnostic{{"codex", "schema_unknown", "threads table was not found"}}
	}
	cols, err := db.Query(ctx, "PRAGMA table_info(threads)")
	if err != nil {
		return nil, []Diagnostic{{"codex", "schema_unknown", "threads schema could not be inspected"}}
	}
	known := map[string]bool{}
	for _, row := range cols.Values {
		if len(row) > 1 {
			if s, ok := row[1].(string); ok {
				known[s] = true
			}
		}
	}
	idCol := pickColumn(known, "id", "thread_id", "conversation_id")
	if idCol == "" {
		return nil, []Diagnostic{{"codex", "schema_unknown", "threads has no stable id column"}}
	}
	selects := []string{idCol}
	for _, col := range []string{"title", "cwd", "workspace", "updated_at", "last_activity_at", "status", "state", "last_message"} {
		if known[col] {
			selects = append(selects, col)
		}
	}
	query := "SELECT " + strings.Join(selects, ",") + " FROM threads ORDER BY rowid DESC LIMIT 500"
	rows, err = db.Query(ctx, query)
	if err != nil {
		return nil, []Diagnostic{{"codex", "query_failed", "threads could not be read"}}
	}
	now := opts.Now()
	out := make([]Session, 0, len(rows.Values))
	for _, row := range rows.Values {
		if len(row) == 0 {
			continue
		}
		s := Session{Provider: "codex", Source: opts.CodexDB, ObservedAt: now}
		for i, col := range selects {
			value := valueString(row[i])
			switch col {
			case idCol:
				s.ID = value
			case "title":
				s.Title = value
			case "cwd", "workspace":
				if s.Workspace == "" {
					s.Workspace = basename(value)
				}
			case "last_message":
				s.LastMessage = sanitizeMessage(value)
			case "status", "state":
				if s.Status == StatusUnknown || s.Status == "" {
					s.Status = Status(value)
				}
			default:
				if t := parseDBTime(row[i]); !t.IsZero() && t.After(s.LastActivityAt) {
					s.LastActivityAt = t
				}
			}
		}
		if s.ID == "" {
			continue
		}
		s.Status = statusFromActivity(now, s.LastActivityAt, string(s.Status), false)
		out = append(out, s)
	}
	return out, nil
}

func pickColumn(known map[string]bool, names ...string) string {
	for _, name := range names {
		if known[name] {
			return name
		}
	}
	return ""
}
func valueString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}
func parseDBTime(v any) time.Time {
	s := valueString(v)
	if t, e := time.Parse(time.RFC3339Nano, s); e == nil {
		return t
	}
	if n, ok := v.(int64); ok {
		if n > 1e12 {
			return time.UnixMilli(n)
		}
		return time.Unix(n, 0)
	}
	return time.Time{}
}
