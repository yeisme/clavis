package agentsession

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

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

func TestCodexSQLiteReaderSupportsSchemaWithoutRowidAndCompatibilityFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state?5#%.sqlite")
	db, err := sqlite.Open(context.Background(), path, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Execute(context.Background(), `CREATE TABLE threads (
		id TEXT PRIMARY KEY,
		title TEXT,
		cwd TEXT,
		updated_at TEXT,
		status TEXT,
		last_message TEXT,
		current_action TEXT,
		result_line TEXT,
		awaiting_human INTEGER,
		pending_review INTEGER
	) WITHOUT ROWID`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Execute(context.Background(), `INSERT INTO threads VALUES (?,?,?,?,?,?,?,?,?,?)`, "cx-compat", "Codex", "/work/project", "2026-11-24T10:00:00Z", "running", nil, "review output", "needs approval", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := NewCodexSQLiteReader(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := reader.Threads(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%+v", rows)
	}
	row := rows[0]
	if row.ID != "cx-compat" || row.LastMessage != "" || row.CurrentAction != "review output" || row.ResultLine != "needs approval" || !row.AwaitingHuman || !row.PendingReview {
		t.Fatalf("compatibility row=%+v", row)
	}
}

func TestCodexSQLiteReaderNormalizesNumericActivityTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state_5.sqlite")
	db, err := sqlite.Open(context.Background(), path, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Execute(context.Background(), `CREATE TABLE threads (id TEXT PRIMARY KEY, updated_at INTEGER) WITHOUT ROWID`); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC).Unix()
	if _, err := db.Execute(context.Background(), `INSERT INTO threads VALUES (?,?)`, "cx-time", stamp); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	reader, err := NewCodexSQLiteReader(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := reader.Threads(context.Background(), 1)
	if err != nil || len(rows) != 1 || !rows[0].LastActivityAt.Equal(time.Unix(stamp, 0)) {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func BenchmarkCodexSQLiteReaderThreads(b *testing.B) {
	path := filepath.Join(b.TempDir(), "state_5.sqlite")
	db, err := sqlite.Open(context.Background(), path, sqlite.OpenOptions{})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := db.Execute(context.Background(), `CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT, cwd TEXT, updated_at TEXT, status TEXT, last_message TEXT) WITHOUT ROWID`); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		if _, err := db.Execute(context.Background(), `INSERT INTO threads VALUES (?,?,?,?,?,?)`, fmt.Sprintf("cx-%03d", i), "Codex", "/work/project", "2026-11-24T10:00:00Z", "completed", "done"); err != nil {
			b.Fatal(err)
		}
	}
	_ = db.Close()
	reader, err := NewCodexSQLiteReader(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := reader.Threads(context.Background(), 500); err != nil {
			b.Fatal(err)
		}
	}
}
