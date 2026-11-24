package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/host"
	corenotif "github.com/yeisme/clavis/pkg/core/notification"
)

func TestDedupSQLiteNotificationStoreStableAndApprox(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 9, 30, 10, 0, time.UTC)
	curNow := now
	store, err := NewSQLiteNotificationStore(ctx, ":memory:", StoreOptions{
		Now: func() time.Time { return curNow },
	})
	if err != nil {
		t.Fatalf("NewSQLiteNotificationStore failed: %v", err)
	}
	defer store.Close()

	// 1. Duplicate stable EventID yields exactly 1 row.
	ev := corenotif.NotificationEvent{
		Source:      corenotif.SourceDSH,
		EventID:     "evt-stable-1",
		Kind:        "message",
		TextPreview: "agent finished build",
		At:          now,
		ObservedAt:  now,
	}
	if err := store.Put(ctx, ev); err != nil {
		t.Fatalf("first Put failed: %v", err)
	}
	if err := store.Put(ctx, ev); err != nil {
		t.Fatalf("duplicate Put failed: %v", err)
	}
	_, stableKey := corenotif.ComputeDedupKey(ev)
	seen, err := store.Seen(ctx, corenotif.SourceDSH, stableKey)
	if err != nil || !seen {
		t.Fatalf("expected Seen=true for stable key, got seen=%v err=%v", seen, err)
	}

	// 2. Different source with identical EventID must NOT dedup against dsh.
	larkSameID := ev
	larkSameID.Source = corenotif.SourceLark
	if err := store.Put(ctx, larkSameID); err != nil {
		t.Fatalf("lark Put failed: %v", err)
	}

	recent, err := store.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("Recent failed: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 rows (1 dsh + 1 lark), got %d", len(recent))
	}

	// 3. Approximate dedup when EventID is missing: same 60s UTC bucket -> 1 row.
	approx1 := corenotif.NotificationEvent{
		Source:          corenotif.SourceLark,
		ConversationRef: "oc_group_1",
		SenderRef:       "ou_user_1",
		Kind:            "text",
		TextPreview:     "please review PR #42",
		At:              now,
		ObservedAt:      now,
	}
	approx2 := approx1
	approx2.At = now.Add(35 * time.Second) // still in same 09:30 minute bucket
	approx2.ObservedAt = now.Add(35 * time.Second)

	res, err := store.ApplyPage(ctx, corenotif.SourceLark, corenotif.NotificationPage{
		Source:     corenotif.SourceLark,
		Events:     []corenotif.NotificationEvent{approx1, approx2},
		NextCursor: "lark:v1:p1",
	})
	if err != nil {
		t.Fatalf("ApplyPage approx failed: %v", err)
	}
	if res.Inserted != 1 || res.Duplicates != 1 || res.CommittedCursor != "lark:v1:p1" {
		t.Fatalf("expected 1 inserted + 1 duplicate for approx pair, got %+v", res)
	}

	larkPage, err := store.RecentBySource(ctx, corenotif.SourceLark, 10)
	if err != nil {
		t.Fatalf("RecentBySource failed: %v", err)
	}
	if len(larkPage.Events) != 2 {
		t.Fatalf("expected 2 lark events (1 stable + 1 approx), got %d", len(larkPage.Events))
	}
	var foundApprox bool
	for _, item := range larkPage.Events {
		if item.DedupClass == corenotif.DedupApprox {
			foundApprox = true
			if item.EventID != "" {
				t.Fatalf("approx event must keep EventID empty, got %q", item.EventID)
			}
			if item.DedupExpiresAt == nil {
				t.Fatal("approx event must record DedupExpiresAt")
			}
		}
	}
	if !foundApprox {
		t.Fatal("expected stored approximate event")
	}

	// 4. After 7-day approximate dedup window expires, Seen becomes false and re-ingest succeeds.
	_, approxKey := corenotif.ComputeDedupKey(approx1)
	curNow = now.Add(corenotif.DefaultApproxDedupTTL + time.Minute)
	seenAfterExpiry, err := store.Seen(ctx, corenotif.SourceLark, approxKey)
	if err != nil || seenAfterExpiry {
		t.Fatalf("expected expired approx key to report Seen=false, got seen=%v err=%v", seenAfterExpiry, err)
	}
	approxAfterExpiry := approx1
	approxAfterExpiry.ObservedAt = curNow
	reRes, err := store.ApplyPage(ctx, corenotif.SourceLark, corenotif.NotificationPage{
		Source:     corenotif.SourceLark,
		Events:     []corenotif.NotificationEvent{approxAfterExpiry},
		NextCursor: "lark:v1:p2",
	})
	if err != nil {
		t.Fatalf("ApplyPage after approx expiry failed: %v", err)
	}
	if reRes.Inserted != 1 || reRes.Duplicates != 0 {
		t.Fatalf("expected expired approx event to be accepted again, got %+v", reRes)
	}
}

func TestCursorAtomicRollbackLeavesCursorUnadvanced(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 11, 0, 0, 0, time.UTC)
	store, err := NewSQLiteNotificationStore(ctx, ":memory:", StoreOptions{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// Seed an initial valid page and cursor.
	initialPage := corenotif.NotificationPage{
		Source: corenotif.SourceDSH,
		Events: []corenotif.NotificationEvent{
			{EventID: "seed-1", Kind: "message", TextPreview: "seed", At: now},
		},
		NextCursor: "dsh:v1:log.jsonl:1",
	}
	if _, err := store.ApplyPage(ctx, corenotif.SourceDSH, initialPage); err != nil {
		t.Fatalf("seed ApplyPage failed: %v", err)
	}

	// Inject a real SQLite failure triggered right when notification_cursors is updated
	// (AFTER the event INSERT statements in the same transaction have executed).
	_, err = store.db.Execute(ctx, `
		CREATE TRIGGER fail_cursor_update
		BEFORE UPDATE ON notification_cursors
		BEGIN
			SELECT RAISE(ABORT, 'injected_cursor_write_failure');
		END
	`)
	if err != nil {
		t.Fatalf("create fail trigger: %v", err)
	}

	failingPage := corenotif.NotificationPage{
		Source: corenotif.SourceDSH,
		Events: []corenotif.NotificationEvent{
			{EventID: "rolled-back-2", Kind: "message", TextPreview: "must roll back", At: now.Add(time.Minute)},
			{EventID: "rolled-back-3", Kind: "message", TextPreview: "must also roll back", At: now.Add(2 * time.Minute)},
		},
		NextCursor: "dsh:v1:log.jsonl:3",
	}
	if _, err := store.ApplyPage(ctx, corenotif.SourceDSH, failingPage); err == nil {
		t.Fatal("expected ApplyPage to fail due to injected cursor write trigger")
	}

	// Verify BOTH cursor AND events rolled back atomically.
	cur, err := store.Cursor(ctx, corenotif.SourceDSH)
	if err != nil {
		t.Fatalf("Cursor failed: %v", err)
	}
	if cur != "dsh:v1:log.jsonl:1" {
		t.Fatalf("cursor advanced despite transaction rollback: got %q", cur)
	}
	_, rolledBackKey := corenotif.ComputeDedupKey(corenotif.NotificationEvent{Source: corenotif.SourceDSH, EventID: "rolled-back-2"})
	seen, err := store.Seen(ctx, corenotif.SourceDSH, rolledBackKey)
	if err != nil || seen {
		t.Fatalf("event from rolled-back transaction leaked into DB: seen=%v err=%v", seen, err)
	}
	recent, err := store.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("Recent failed: %v", err)
	}
	if len(recent) != 1 || recent[0].EventID != "seed-1" {
		t.Fatalf("expected only seed-1 after rollback, got %+v", recent)
	}
}

func TestCursorDisconnectAndResumeWithoutDuplicates(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "notifications.sqlite")
	now := time.Date(2026, 11, 24, 13, 0, 0, 0, time.UTC)

	// Session 1: write event without committing cursor (simulating crash/disconnect before cursor commit).
	store1, err := NewSQLiteNotificationStore(ctx, dbPath, StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("open store1: %v", err)
	}
	if err := store1.Put(ctx, corenotif.NotificationEvent{
		Source:      corenotif.SourceDSH,
		EventID:     "dsh-1",
		Kind:        "message",
		TextPreview: "first event",
		At:          now,
	}); err != nil {
		t.Fatalf("Put dsh-1 failed: %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("close store1: %v", err)
	}

	// Session 2: reopen from disk; cursor is still "", so replay reads dsh-1 + dsh-2.
	store2, err := NewSQLiteNotificationStore(ctx, dbPath, StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("reopen store2: %v", err)
	}
	defer store2.Close()

	cur, err := store2.Cursor(ctx, corenotif.SourceDSH)
	if err != nil || cur != "" {
		t.Fatalf("expected empty cursor before commit, got cur=%q err=%v", cur, err)
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "001.jsonl")
	lines := strings.Join([]string{
		`{"event_id":"dsh-1","kind":"message","preview":"first event","timestamp":"2026-11-24T13:00:00Z","session_id":"sess-1"}`,
		`{"event_id":"dsh-2","kind":"message","preview":"second event","timestamp":"2026-11-24T13:01:00Z","session_id":"sess-1"}`,
		`{"event_id":"dsh-3","kind":"message","preview":"third event","timestamp":"2026-11-24T13:02:00Z","session_id":"sess-2"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(lines), 0o600); err != nil {
		t.Fatalf("write dsh log: %v", err)
	}

	dshSrc := NewDSHSource(DSHSourceConfig{EventsDir: dir, Now: func() time.Time { return now }})

	// Page 1 (limit 2): dsh-1 deduplicated, dsh-2 inserted, cursor advances to line 2.
	r1, err := corenotif.IngestPage(ctx, dshSrc, store2, 2)
	if err != nil {
		t.Fatalf("ingest page 1 failed: %v", err)
	}
	if r1.Inserted != 1 || r1.Duplicates != 1 || !r1.HasMore || r1.NextCursor != "dsh:v1:001.jsonl:2" {
		t.Fatalf("unexpected page 1 ingest result: %+v", r1)
	}

	// Page 2 (limit 2): resumes at line 2, reads only dsh-3.
	r2, err := corenotif.IngestPage(ctx, dshSrc, store2, 2)
	if err != nil {
		t.Fatalf("ingest page 2 failed: %v", err)
	}
	if r2.Inserted != 1 || r2.Duplicates != 0 || r2.HasMore || r2.NextCursor != "dsh:v1:001.jsonl:3" {
		t.Fatalf("unexpected page 2 ingest result: %+v", r2)
	}

	// Page 3 (at EOF): 0 events, cursor stays at line 3.
	r3, err := corenotif.IngestPage(ctx, dshSrc, store2, 2)
	if err != nil {
		t.Fatalf("ingest page 3 failed: %v", err)
	}
	if r3.ReadCount != 0 || r3.NextCursor != "dsh:v1:001.jsonl:3" {
		t.Fatalf("unexpected page 3 ingest result: %+v", r3)
	}

	all, err := store2.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("Recent failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 deduplicated events after resume, got %d", len(all))
	}
	if all[0].EventID != "dsh-3" || all[0].SessionID != "sess-2" {
		t.Fatalf("expected newest event dsh-3 first, got %+v", all[0])
	}
}

func TestNotificationBoundedRetentionPruningAndSchemaTooNew(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "retention.sqlite")
	now := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)

	store, err := NewSQLiteNotificationStore(ctx, dbPath, StoreOptions{
		Retention: RetentionPolicy{
			MaxEventsPerSource: 3,
			MaxAge:             24 * time.Hour,
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	// Insert 5 events (1 older than MaxAge + 4 recent): auto-prune should keep 3 newest AND keep cursor intact.
	events := []corenotif.NotificationEvent{
		{EventID: "old-expired", Kind: "message", TextPreview: "too old", At: now.Add(-48 * time.Hour)},
		{EventID: "e1", Kind: "message", TextPreview: "1", At: now.Add(-4 * time.Minute)},
		{EventID: "e2", Kind: "message", TextPreview: "2", At: now.Add(-3 * time.Minute)},
		{EventID: "e3", Kind: "message", TextPreview: "3", At: now.Add(-2 * time.Minute)},
		{EventID: "e4", Kind: "message", TextPreview: "4", At: now.Add(-1 * time.Minute)},
	}
	res, err := store.ApplyPage(ctx, corenotif.SourceDSH, corenotif.NotificationPage{
		Source:     corenotif.SourceDSH,
		Events:     events,
		NextCursor: "dsh:v1:events.jsonl:5",
	})
	if err != nil {
		t.Fatalf("ApplyPage failed: %v", err)
	}
	if res.Pruned != 2 {
		t.Fatalf("expected 2 pruned events (1 age + 1 limit), got %+v", res)
	}
	recent, err := store.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("Recent failed: %v", err)
	}
	if len(recent) != 3 || recent[0].EventID != "e4" || recent[1].EventID != "e3" || recent[2].EventID != "e2" {
		t.Fatalf("unexpected retained events: %+v", recent)
	}
	if cur, err := store.Cursor(ctx, corenotif.SourceDSH); err != nil || cur != "dsh:v1:events.jsonl:5" {
		t.Fatalf("retention pruning must preserve cursor, got cur=%q err=%v", cur, err)
	}

	// Explicit Prune removes aged rows via direct Put without touching cursor.
	if err := store.Put(ctx, corenotif.NotificationEvent{
		Source: corenotif.SourceLark, EventID: "lark-old", Kind: "text", TextPreview: "stale", At: now.Add(-72 * time.Hour),
	}); err != nil {
		t.Fatalf("Put stale lark event: %v", err)
	}
	if err := store.CommitCursor(ctx, corenotif.SourceLark, "lark:v1:kept"); err != nil {
		t.Fatalf("CommitCursor lark: %v", err)
	}
	pruned, err := store.Prune(ctx)
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}
	if pruned.RemovedByAge != 1 {
		t.Fatalf("expected 1 aged event removed by Prune, got %+v", pruned)
	}
	if cur, err := store.Cursor(ctx, corenotif.SourceLark); err != nil || cur != "lark:v1:kept" {
		t.Fatalf("Prune must preserve lark cursor, got cur=%q err=%v", cur, err)
	}

	// Simulate future schema version -> reopen enters safe read-only mode.
	if _, err := store.db.Execute(ctx, `UPDATE notification_schema_meta SET version = 99 WHERE id = 1`); err != nil {
		t.Fatalf("bump schema version: %v", err)
	}
	_ = store.Close()

	roStore, err := NewSQLiteNotificationStore(ctx, dbPath, StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("reopen newer schema failed: %v", err)
	}
	defer roStore.Close()

	if !roStore.IsReadOnly() || roStore.SchemaOperation().Code != "schema_too_new" {
		t.Fatalf("expected read-only schema_too_new state, got ro=%v op=%+v", roStore.IsReadOnly(), roStore.SchemaOperation())
	}
	// Old events & cursor remain readable.
	roEvents, err := roStore.Recent(ctx, 10)
	if err != nil || len(roEvents) != 3 {
		t.Fatalf("expected 3 readable events in read-only mode, got len=%d err=%v", len(roEvents), err)
	}
	if cur, err := roStore.Cursor(ctx, corenotif.SourceDSH); err != nil || cur != "dsh:v1:events.jsonl:5" {
		t.Fatalf("expected readable cursor in read-only mode, got cur=%q err=%v", cur, err)
	}
	// Writes are rejected with ErrSchemaTooNew.
	if err := roStore.Put(ctx, corenotif.NotificationEvent{Source: corenotif.SourceDSH, EventID: "blocked", At: now}); !errors.Is(err, corenotif.ErrSchemaTooNew) {
		t.Fatalf("expected ErrSchemaTooNew on Put, got %v", err)
	}
	if _, err := roStore.ApplyPage(ctx, corenotif.SourceDSH, corenotif.NotificationPage{Source: corenotif.SourceDSH, NextCursor: "dsh:v1:x:1"}); !errors.Is(err, corenotif.ErrSchemaTooNew) {
		t.Fatalf("expected ErrSchemaTooNew on ApplyPage, got %v", err)
	}
}

type fakeDSHFS struct {
	statErr    error
	readDirErr error
	readErr    error
	entries    []fs.DirEntry
	files      map[string][]byte
}

type fakeDirInfo struct{ name string }

func (f fakeDirInfo) Name() string       { return f.name }
func (f fakeDirInfo) Size() int64        { return 0 }
func (f fakeDirInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o700 }
func (f fakeDirInfo) ModTime() time.Time { return time.Time{} }
func (f fakeDirInfo) IsDir() bool        { return true }
func (f fakeDirInfo) Sys() any           { return nil }

type fakeFileEntry struct{ name string }

func (f fakeFileEntry) Name() string               { return f.name }
func (f fakeFileEntry) IsDir() bool                { return false }
func (f fakeFileEntry) Type() fs.FileMode          { return 0 }
func (f fakeFileEntry) Info() (fs.FileInfo, error) { return fakeDirInfo{name: f.name}, nil }

func (f *fakeDSHFS) Stat(path string) (fs.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return fakeDirInfo{name: filepath.Base(path)}, nil
}

func (f *fakeDSHFS) ReadDir(dir string) ([]fs.DirEntry, error) {
	if f.readDirErr != nil {
		return nil, f.readDirErr
	}
	return f.entries, nil
}

func (f *fakeDSHFS) ReadFile(path string) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	data, ok := f.files[filepath.Base(path)]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return data, nil
}

func TestNotificationDSHSourceDiagnosticsCursorExpiredAndSanitization(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 16, 0, 0, 0, time.UTC)
	store, err := NewSQLiteNotificationStore(ctx, ":memory:", StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	// 1. Permission denied -> structured, secret-free OperationResult.
	permFS := &fakeDSHFS{statErr: fmt.Errorf("open /Users/alice/.dsh/token=secret999: %w", fs.ErrPermission)}
	permSrc := NewDSHSource(DSHSourceConfig{EventsDir: "/Users/alice/.dsh", Reader: permFS, Now: func() time.Time { return now }})
	if avail, err := permSrc.Available(ctx); avail || !errors.Is(err, corenotif.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got avail=%v err=%v", avail, err)
	}
	op := permSrc.LastOperation()
	if op.State != host.StateFailed || op.Code != "permission_denied" || op.Retryable {
		t.Fatalf("unexpected permission diagnostic: %+v", op)
	}
	if strings.Contains(op.Message, "secret999") || strings.Contains(op.Message, "/Users/alice") {
		t.Fatalf("diagnostic leaked path or secret: %q", op.Message)
	}

	// 2. Valid read strips raw payload/secrets and preserves explicit session_id only.
	goodFS := &fakeDSHFS{
		entries: []fs.DirEntry{fakeFileEntry{name: "events.jsonl"}},
		files: map[string][]byte{
			"events.jsonl": []byte(`{"event_id":"dsh-10","kind":"alert","preview":"Bearer topsecret123 /Users/bob/key.pem done","raw_payload":"NEVER_STORE_ME","token":"sk-neverstore123456"}` + "\n"),
		},
	}
	dshSrc := NewDSHSource(DSHSourceConfig{EventsDir: "/virtual/dsh", Reader: goodFS, Now: func() time.Time { return now }})
	if _, err := corenotif.IngestPage(ctx, dshSrc, store, 10); err != nil {
		t.Fatalf("initial dsh ingest failed: %v", err)
	}
	stored, _ := store.Recent(ctx, 10)
	if len(stored) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(stored))
	}
	if strings.Contains(stored[0].TextPreview, "topsecret123") || strings.Contains(stored[0].TextPreview, "/Users/bob") {
		t.Fatalf("stored preview leaked secret/path: %q", stored[0].TextPreview)
	}
	if stored[0].SessionID != "" {
		t.Fatalf("session_id must remain empty when not explicitly bound, got %q", stored[0].SessionID)
	}

	// 3. Schema drift (unsupported schema_version) fails cleanly and keeps old events readable.
	goodFS.files["events.jsonl"] = []byte(`{"schema_version":99,"event_id":"dsh-11","preview":"future"}` + "\n")
	driftRes, err := corenotif.IngestPage(ctx, dshSrc, store, 10)
	if !errors.Is(err, corenotif.ErrSchemaDrift) {
		t.Fatalf("expected ErrSchemaDrift, got %v", err)
	}
	if driftRes.Operation.Code != "schema_drift" || driftRes.Operation.State != host.StateDegraded {
		t.Fatalf("unexpected schema drift operation: %+v", driftRes.Operation)
	}
	if stillThere, _ := store.Recent(ctx, 10); len(stillThere) != 1 {
		t.Fatalf("schema drift must not delete existing events, got %d", len(stillThere))
	}

	// 4. Cursor expired when log file is rotated/removed.
	goodFS.entries = []fs.DirEntry{fakeFileEntry{name: "events_new.jsonl"}}
	goodFS.files = map[string][]byte{"events_new.jsonl": []byte(`{"event_id":"dsh-12","preview":"rotated"}` + "\n")}
	expRes, err := corenotif.IngestPage(ctx, dshSrc, store, 10)
	if !errors.Is(err, corenotif.ErrCursorExpired) {
		t.Fatalf("expected ErrCursorExpired, got %v", err)
	}
	if expRes.Operation.Code != "cursor_expired" || !expRes.Operation.Retryable {
		t.Fatalf("unexpected cursor_expired operation: %+v", expRes.Operation)
	}
	if cur, _ := store.Cursor(ctx, corenotif.SourceDSH); cur != "dsh:v1:events.jsonl:1" {
		t.Fatalf("cursor_expired must preserve old cursor, got %q", cur)
	}
}

func TestNotificationLarkSourceReadOnlyPaginationPermissionAndSchemaDiagnostics(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 17, 0, 0, 0, time.UTC)
	const secretToken = "t-g104supersecretlarktoken99999"

	var mode atomic.Int32 // 0=ok, 1=403, 2=lark_perm_code, 3=cursor_expired, 4=schema_drift
	var seenMethods []string
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenMethods = append(seenMethods, r.Method)
		mu.Unlock()

		if r.Method != http.MethodGet {
			t.Errorf("LarkSource issued non-GET method: %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+secretToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":99991663,"msg":"invalid token ` + secretToken + `"}`))
			return
		}

		switch mode.Load() {
		case 1:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":99991672,"msg":"no permission for ` + secretToken + `"}`))
			return
		case 2:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 99991672,
				"msg":  "missing scope im:message:readonly token=" + secretToken,
			})
			return
		case 3:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 230020,
				"msg":  "page_token expired",
			})
			return
		case 4:
			_, _ = w.Write([]byte(`{"code":0,"data":{"items":"not-an-array","raw_secret":"` + secretToken + `"}}`))
			return
		}

		pt := r.URL.Query().Get("page_token")
		if pt == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"has_more":   true,
					"page_token": "tok_page_2",
					"items": []map[string]any{
						{
							"message_id":  "om_1001",
							"chat_id":     "oc_chat_alpha",
							"msg_type":    "text",
							"create_time": strconv.FormatInt(now.UnixMilli(), 10),
							"sender":      map[string]any{"id": "ou_sender_1"},
							"body":        map[string]any{"content": `{"text":"Deploy check Bearer ` + secretToken + ` /Users/alice/app"}`},
							"raw_blob":    "ignored-attachment-data",
						},
					},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"has_more":   false,
				"page_token": "tok_page_end",
				"items": []map[string]any{
					{
						// Missing message_id -> approximate dedup without forging EventID.
						"chat_id":     "oc_chat_alpha",
						"msg_type":    "file",
						"create_time": strconv.FormatInt(now.Add(time.Minute).UnixMilli(), 10),
						"sender":      map[string]any{"id": "ou_sender_2"},
						"body":        map[string]any{"content": `{"file_key":"file_v2_secret_attachment"}`},
					},
				},
			},
		})
	}))
	defer srv.Close()

	// Verify write endpoint configuration is blocked immediately.
	badWriteSrc := NewLarkSource(LarkSourceConfig{
		BaseURL:       srv.URL,
		EventsPath:    "/open-apis/im/v1/messages/send",
		Doer:          srv.Client(),
		TokenProvider: func(context.Context) (string, error) { return secretToken, nil },
		Now:           func() time.Time { return now },
	})
	if avail, err := badWriteSrc.Available(ctx); avail || !errors.Is(err, corenotif.ErrReadOnlyViolation) {
		t.Fatalf("expected ErrReadOnlyViolation for write path, got avail=%v err=%v", avail, err)
	}

	store, err := NewSQLiteNotificationStore(ctx, ":memory:", StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	larkSrc := NewLarkSource(LarkSourceConfig{
		BaseURL:       srv.URL,
		ContainerID:   "oc_chat_alpha",
		Doer:          srv.Client(),
		TokenProvider: func(context.Context) (string, error) { return secretToken, nil },
		Now:           func() time.Time { return now },
	})

	// Page 1 & Page 2 normal read-only ingestion.
	r1, err := corenotif.IngestPage(ctx, larkSrc, store, 10)
	if err != nil {
		t.Fatalf("lark page 1 failed: %v", err)
	}
	if r1.Inserted != 1 || !r1.HasMore || r1.NextCursor != "lark:v1:tok_page_2" {
		t.Fatalf("unexpected lark page 1 result: %+v", r1)
	}
	r2, err := corenotif.IngestPage(ctx, larkSrc, store, 10)
	if err != nil {
		t.Fatalf("lark page 2 failed: %v", err)
	}
	if r2.Inserted != 1 || r2.ApproxCount != 1 || r2.HasMore || r2.NextCursor != "lark:v1:tok_page_end" {
		t.Fatalf("unexpected lark page 2 result: %+v", r2)
	}

	stored, _ := store.RecentBySource(ctx, corenotif.SourceLark, 10)
	if len(stored.Events) != 2 {
		t.Fatalf("expected 2 lark events, got %d", len(stored.Events))
	}
	for _, ev := range stored.Events {
		if strings.Contains(ev.TextPreview, secretToken) || strings.Contains(ev.TextPreview, "/Users/alice") || strings.Contains(ev.TextPreview, "file_v2_secret_attachment") {
			t.Fatalf("lark stored preview leaked secret/path/attachment: %+v", ev)
		}
	}

	// Test HTTP 403, Lark API permission code, cursor expired, and schema drift diagnostics.
	for _, tc := range []struct {
		name     string
		mode     int32
		wantErr  error
		wantCode string
	}{
		{"http_403", 1, corenotif.ErrPermissionDenied, "permission_denied"},
		{"api_perm_code", 2, corenotif.ErrPermissionDenied, "permission_denied"},
		{"cursor_expired", 3, corenotif.ErrCursorExpired, "cursor_expired"},
		{"schema_drift", 4, corenotif.ErrSchemaDrift, "schema_drift"},
	} {
		mode.Store(tc.mode)
		res, err := corenotif.IngestPage(ctx, larkSrc, store, 10)
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("%s: expected error %v, got %v", tc.name, tc.wantErr, err)
		}
		if res.Operation.Code != tc.wantCode {
			t.Fatalf("%s: expected op code %q, got %+v", tc.name, tc.wantCode, res.Operation)
		}
		if strings.Contains(res.Operation.Message, secretToken) || strings.Contains(err.Error(), secretToken) {
			t.Fatalf("%s: leaked secret token in error/diagnostic: op=%q err=%q", tc.name, res.Operation.Message, err.Error())
		}
	}

	// Existing events and cursor must remain intact after all errors.
	after, _ := store.RecentBySource(ctx, corenotif.SourceLark, 10)
	if len(after.Events) != 2 || after.NextCursor != "lark:v1:tok_page_end" {
		t.Fatalf("errors mutated existing lark events/cursor: %+v", after)
	}

	// Verify every HTTP request was strictly GET.
	mu.Lock()
	defer mu.Unlock()
	if len(seenMethods) == 0 {
		t.Fatal("expected HTTP requests to fake server")
	}
	for _, m := range seenMethods {
		if m != http.MethodGet {
			t.Fatalf("non-GET method recorded: %s", m)
		}
	}
}

func TestReadOnlyAdaptersHaveNoWriteMethods(t *testing.T) {
	allowed := map[string]bool{
		"Name":          true,
		"Available":     true,
		"Read":          true,
		"Capabilities":  true,
		"LastOperation": true,
	}
	for _, typ := range []reflect.Type{
		reflect.TypeOf((*DSHSource)(nil)),
		reflect.TypeOf((*LarkSource)(nil)),
	} {
		for i := 0; i < typ.NumMethod(); i++ {
			name := typ.Method(i).Name
			if !allowed[name] {
				t.Fatalf("adapter %s exposes non-allowlisted method %q", typ, name)
			}
		}
	}
}

func TestNotificationSQLiteConcurrentRaceSafety(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 18, 0, 0, 0, time.UTC)
	store, err := NewSQLiteNotificationStore(ctx, ":memory:", StoreOptions{
		Retention: RetentionPolicy{MaxEventsPerSource: 200},
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			src := corenotif.SourceDSH
			if worker%2 == 1 {
				src = corenotif.SourceLark
			}
			for i := 0; i < 15; i++ {
				ev := corenotif.NotificationEvent{
					Source:      src,
					EventID:     fmt.Sprintf("shared-%d", i%5),
					Kind:        "message",
					TextPreview: fmt.Sprintf("worker %d msg %d", worker, i),
					At:          now.Add(time.Duration(i) * time.Second),
				}
				_, _ = store.ApplyPage(ctx, src, corenotif.NotificationPage{
					Source:     src,
					Events:     []corenotif.NotificationEvent{ev},
					NextCursor: fmt.Sprintf("%s:v1:c:%d", src, i),
				})
				_, _ = store.Recent(ctx, 20)
				_, _ = store.Cursor(ctx, src)
			}
		}(g)
	}
	wg.Wait()

	dshPage, err := store.RecentBySource(ctx, corenotif.SourceDSH, 50)
	if err != nil {
		t.Fatalf("RecentBySource dsh failed: %v", err)
	}
	larkPage, err := store.RecentBySource(ctx, corenotif.SourceLark, 50)
	if err != nil {
		t.Fatalf("RecentBySource lark failed: %v", err)
	}
	if len(dshPage.Events) != 5 || len(larkPage.Events) != 5 {
		t.Fatalf("expected 5 deduplicated events per source under concurrency, got dsh=%d lark=%d", len(dshPage.Events), len(larkPage.Events))
	}
}
