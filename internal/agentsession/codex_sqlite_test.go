package agentsession

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/plugins/sqlite"
)

func TestCodexSQLiteReaderUsesTemporaryReadOnlyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state_5.sqlite")
	db, err := sqlite.Open(context.Background(), path, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Execute(context.Background(), `CREATE TABLE threads (id TEXT, title TEXT, cwd TEXT, updated_at TEXT, status TEXT, last_message TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Execute(context.Background(), `INSERT INTO threads VALUES (?,?,?,?,?,?)`, "cx-1", "Codex", "/work/project", "2026-11-21T11:00:00Z", "completed", "finished"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	reader, err := NewCodexSQLiteReader(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := reader.Threads(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "cx-1" || rows[0].Title != "Codex" {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestCodexSQLiteReaderRejectsImmutableURI(t *testing.T) {
	if _, err := NewCodexSQLiteReader("file:/tmp/state_5.sqlite?immutable=1"); err == nil {
		t.Fatal("immutable URI accepted")
	}
}
