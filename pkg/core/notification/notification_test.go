package notification

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/host"
)

func TestDedupKeyStableAndCrossSourceIsolation(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	dshEv := NotificationEvent{
		Source:      SourceDSH,
		EventID:     "evt-42",
		TextPreview: "build succeeded",
		At:          now,
		ObservedAt:  now,
	}
	larkEv := NotificationEvent{
		Source:      SourceLark,
		EventID:     "evt-42",
		TextPreview: "build succeeded",
		At:          now,
		ObservedAt:  now,
	}

	dshClass, dshKey := ComputeDedupKey(dshEv)
	larkClass, larkKey := ComputeDedupKey(larkEv)

	if dshClass != DedupStable || larkClass != DedupStable {
		t.Fatalf("expected stable dedup class, got dsh=%q lark=%q", dshClass, larkClass)
	}
	if dshKey != "dsh\x00stable\x00evt-42" {
		t.Fatalf("unexpected dsh key: %q", dshKey)
	}
	if larkKey != "lark\x00stable\x00evt-42" {
		t.Fatalf("unexpected lark key: %q", larkKey)
	}
	if dshKey == larkKey {
		t.Fatalf("different sources with identical EventID must not share a dedup key")
	}
}

func TestDedupKeyApproximateBucketing(t *testing.T) {
	t1 := time.Date(2026, 11, 24, 10, 15, 5, 0, time.UTC)
	t2 := time.Date(2026, 11, 24, 10, 15, 54, 0, time.UTC)
	t3 := time.Date(2026, 11, 24, 10, 16, 1, 0, time.UTC)

	ev1 := NotificationEvent{
		Source:          SourceLark,
		ConversationRef: "oc_chat_1",
		SenderRef:       "ou_sender_1",
		TextPreview:     "  deploy   done  ",
		At:              t1,
	}
	ev2 := NotificationEvent{
		Source:          SourceLark,
		ConversationRef: "oc_chat_1",
		SenderRef:       "ou_sender_1",
		TextPreview:     "deploy done",
		At:              t2,
	}
	ev3 := NotificationEvent{
		Source:          SourceLark,
		ConversationRef: "oc_chat_1",
		SenderRef:       "ou_sender_1",
		TextPreview:     "deploy done",
		At:              t3,
	}

	c1, k1 := ComputeDedupKey(ev1)
	c2, k2 := ComputeDedupKey(ev2)
	c3, k3 := ComputeDedupKey(ev3)

	if c1 != DedupApprox || c2 != DedupApprox || c3 != DedupApprox {
		t.Fatalf("expected approximate class, got %q %q %q", c1, c2, c3)
	}
	if k1 != k2 {
		t.Fatalf("events in same 60s bucket with same preview must match:\nk1=%q\nk2=%q", k1, k2)
	}
	if k1 == k3 {
		t.Fatalf("events in different 60s buckets must not match:\nk1=%q\nk3=%q", k1, k3)
	}

	// NormalizeEvent must NEVER copy approximate digest back into EventID.
	norm, diags, err := NormalizeEvent(ev1, t1)
	if err != nil {
		t.Fatalf("NormalizeEvent failed: %v", err)
	}
	if norm.EventID != "" {
		t.Fatalf("approximate event must keep empty EventID, got %q", norm.EventID)
	}
	if norm.DedupClass != DedupApprox {
		t.Fatalf("expected DedupApprox, got %q", norm.DedupClass)
	}
	if norm.DedupExpiresAt == nil || !norm.DedupExpiresAt.Equal(t1.Add(DefaultApproxDedupTTL)) {
		t.Fatalf("expected 7-day approx expiry, got %v", norm.DedupExpiresAt)
	}
	if len(diags) == 0 || diags[0].Code != "approximate_dedup" {
		t.Fatalf("expected approximate_dedup diagnostic, got %+v", diags)
	}
}

func TestNotificationSanitizationAndBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	rawPreview := "\x1b[31mAlert\x1b[0m Bearer supersecret12345 sk-secretkey987654321 " +
		"token=topsecret999 ghp_abcdef1234567890 xoxb-1234567890-abcdef " +
		"https://open.feishu.cn/item?access_token=leakme&x=1 " +
		"/Users/alice/private/secrets.env C:\\Users\\alice\\keys.pem " +
		"```go\nfmt.Println(\"secret code\")\n```\x00\x07 " + strings.Repeat("汉", 300)

	ev := NotificationEvent{
		Source:          SourceDSH,
		EventID:         "evt-sanitize-1",
		ConversationRef: "chat/alice@example.com?token=abc123456",
		SenderRef:       "user +86 13800138000 /Users/alice/home",
		Kind:            "Mention Event!",
		TextPreview:     rawPreview,
		SourceCursor:    "dsh:v1:events.jsonl:10",
	}

	norm, diags, err := NormalizeEvent(ev, now)
	if err != nil {
		t.Fatalf("NormalizeEvent failed: %v", err)
	}
	if !norm.TimeUnreliable || !norm.At.Equal(now) {
		t.Fatalf("missing At should default to now and mark TimeUnreliable: at=%v unreliable=%v", norm.At, norm.TimeUnreliable)
	}
	foundTimeDiag := false
	for _, d := range diags {
		if d.Code == "time_unreliable" {
			foundTimeDiag = true
		}
	}
	if !foundTimeDiag {
		t.Fatalf("expected time_unreliable diagnostic, got %+v", diags)
	}

	for _, forbidden := range []string{
		"supersecret12345",
		"sk-secretkey987654321",
		"topsecret999",
		"ghp_abcdef1234567890",
		"xoxb-1234567890-abcdef",
		"leakme",
		"/Users/alice/private",
		"C:\\Users\\alice",
		"secret code",
		"\x1b",
		"\x00",
		"\x07",
	} {
		if strings.Contains(norm.TextPreview, forbidden) {
			t.Fatalf("sanitized TextPreview leaked %q: %q", forbidden, norm.TextPreview)
		}
	}
	if count := utf8.RuneCountInString(norm.TextPreview); count > MaxTextPreviewRunes {
		t.Fatalf("TextPreview exceeded %d runes: got %d", MaxTextPreviewRunes, count)
	}

	for _, forbidden := range []string{"alice@example.com", "abc123456", "13800138000", "/Users/alice"} {
		if strings.Contains(norm.ConversationRef, forbidden) || strings.Contains(norm.SenderRef, forbidden) {
			t.Fatalf("sanitized refs leaked %q: conv=%q sender=%q", forbidden, norm.ConversationRef, norm.SenderRef)
		}
	}

	// Unknown source must be rejected.
	if _, _, err := NormalizeEvent(NotificationEvent{Source: "slack", EventID: "1"}, now); !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("expected ErrUnknownSource for unknown source, got %v", err)
	}

	// Invalid cursor (control char / space / too long) must be rejected.
	for _, badCursor := range []string{"has space", "line\nbreak", "nul\x00byte", strings.Repeat("a", MaxCursorLength+1)} {
		if err := ValidateCursor(badCursor); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("expected ErrInvalidCursor for %q, got %v", badCursor, err)
		}
	}
}

type fakePageResponse struct {
	page NotificationPage
	err  error
}

type fakeSource struct {
	name      string
	available bool
	availErr  error
	mu        sync.Mutex
	pages     map[string]fakePageResponse
	reads     []string
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) Available(ctx context.Context) (bool, error) {
	if f.availErr != nil {
		return false, f.availErr
	}
	return f.available, nil
}

func (f *fakeSource) Read(ctx context.Context, cursor string, limit int) (NotificationPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, cursor)
	resp, ok := f.pages[cursor]
	if !ok {
		return NotificationPage{Source: f.name, NextCursor: cursor}, nil
	}
	return resp.page, resp.err
}

type memStore struct {
	mu      sync.Mutex
	events  map[string]NotificationEvent
	order   []string
	cursors map[string]string
	failErr error
}

func newMemStore() *memStore {
	return &memStore{
		events:  map[string]NotificationEvent{},
		cursors: map[string]string{},
	}
}

func (m *memStore) Seen(ctx context.Context, source, dedupKey string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.events[source+"\x00"+dedupKey]
	return ok, nil
}

func (m *memStore) Put(ctx context.Context, ev NotificationEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := ev.Source + "\x00" + ev.DedupKey
	if _, exists := m.events[k]; !exists {
		m.events[k] = ev
		m.order = append(m.order, k)
	}
	return nil
}

func (m *memStore) CommitCursor(ctx context.Context, source, cursor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cursors[source] = cursor
	return nil
}

func (m *memStore) Cursor(ctx context.Context, source string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cursors[source], nil
}

func (m *memStore) ApplyPage(ctx context.Context, source string, page NotificationPage) (ApplyPageResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failErr != nil {
		return ApplyPageResult{}, m.failErr
	}
	res := ApplyPageResult{}
	for _, ev := range page.Events {
		k := source + "\x00" + ev.DedupKey
		if _, exists := m.events[k]; exists {
			res.Duplicates++
			continue
		}
		m.events[k] = ev
		m.order = append(m.order, k)
		res.Inserted++
	}
	if page.NextCursor != "" {
		m.cursors[source] = page.NextCursor
	}
	res.CommittedCursor = m.cursors[source]
	return res, nil
}

func (m *memStore) Recent(ctx context.Context, limit int) ([]NotificationEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]NotificationEvent, 0, len(m.order))
	for i := len(m.order) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, m.events[m.order[i]])
	}
	return out, nil
}

func (m *memStore) RecentBySource(ctx context.Context, source string, limit int) (NotificationPage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]NotificationEvent, 0)
	for i := len(m.order) - 1; i >= 0 && len(out) < limit; i-- {
		ev := m.events[m.order[i]]
		if ev.Source == source {
			out = append(out, ev)
		}
	}
	return NotificationPage{Source: source, Events: out, NextCursor: m.cursors[source]}, nil
}

func TestNotificationIngestPaginationAndCursorResume(t *testing.T) {
	now := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)
	store := newMemStore()
	src := &fakeSource{
		name:      SourceDSH,
		available: true,
		pages: map[string]fakePageResponse{
			"": {
				page: NotificationPage{
					Source: SourceDSH,
					Events: []NotificationEvent{
						{EventID: "e1", Kind: "message", TextPreview: "first", At: now},
						{EventID: "e2", Kind: "message", TextPreview: "second", At: now.Add(time.Second)},
					},
					NextCursor: "dsh:v1:p1",
					HasMore:    true,
				},
			},
			"dsh:v1:p1": {
				page: NotificationPage{
					Source: SourceDSH,
					Events: []NotificationEvent{
						// Duplicate overlap from previous page plus one new event.
						{EventID: "e2", Kind: "message", TextPreview: "second", At: now.Add(time.Second)},
						{EventID: "e3", Kind: "message", TextPreview: "third", At: now.Add(2 * time.Second)},
					},
					NextCursor: "dsh:v1:p2",
					HasMore:    false,
				},
			},
		},
	}

	ctx := context.Background()
	r1, err := IngestPage(ctx, src, store, 2)
	if err != nil {
		t.Fatalf("page 1 ingest failed: %v", err)
	}
	if r1.Inserted != 2 || r1.Duplicates != 0 || r1.NextCursor != "dsh:v1:p1" || !r1.HasMore || r1.Operation.State != host.StateReady {
		t.Fatalf("unexpected page 1 result: %+v", r1)
	}

	// Simulate store write failure on page 2: cursor must stay at dsh:v1:p1.
	store.failErr = errors.New("disk_full")
	if _, err := IngestPage(ctx, src, store, 2); err == nil {
		t.Fatal("expected error when store.ApplyPage fails")
	}
	cur, _ := store.Cursor(ctx, SourceDSH)
	if cur != "dsh:v1:p1" {
		t.Fatalf("cursor advanced despite ApplyPage failure: got %q", cur)
	}

	// Recover store and resume from dsh:v1:p1: duplicate e2 skipped, e3 stored, cursor advances.
	store.failErr = nil
	r2, err := IngestPage(ctx, src, store, 2)
	if err != nil {
		t.Fatalf("page 2 resume failed: %v", err)
	}
	if r2.Inserted != 1 || r2.Duplicates != 1 || r2.NextCursor != "dsh:v1:p2" || r2.HasMore {
		t.Fatalf("unexpected page 2 result: %+v", r2)
	}

	recent, _ := store.Recent(ctx, 10)
	if len(recent) != 3 {
		t.Fatalf("expected 3 deduplicated events, got %d", len(recent))
	}

	// Cursor expired must NOT wipe stored cursor or stored events.
	src.pages["dsh:v1:p2"] = fakePageResponse{
		err: NewSourceError(ErrCursorExpired, host.StateDegraded, "cursor_expired", "log rotated", true, now),
	}
	r3, err := IngestPage(ctx, src, store, 2)
	if !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("expected ErrCursorExpired, got %v", err)
	}
	if r3.Operation.Code != "cursor_expired" || r3.Operation.State != host.StateDegraded || !r3.Operation.Retryable {
		t.Fatalf("unexpected cursor_expired operation: %+v", r3.Operation)
	}
	if curAfter, _ := store.Cursor(ctx, SourceDSH); curAfter != "dsh:v1:p2" {
		t.Fatalf("cursor_expired mutated stored cursor to %q", curAfter)
	}
	if recentAfter, _ := store.Recent(ctx, 10); len(recentAfter) != 3 {
		t.Fatalf("cursor_expired cleared stored events: got %d", len(recentAfter))
	}
}

func TestReadOnlyNotificationSourceInterfaceHasNoWriteMethods(t *testing.T) {
	iface := reflect.TypeOf((*NotificationSource)(nil)).Elem()
	forbidden := []string{"send", "stop", "mutate", "ack", "write", "delete", "reply", "recall", "approve", "exec"}
	for i := 0; i < iface.NumMethod(); i++ {
		name := strings.ToLower(iface.Method(i).Name)
		for _, bad := range forbidden {
			if strings.Contains(name, bad) {
				t.Fatalf("NotificationSource exposes forbidden write method %q", iface.Method(i).Name)
			}
		}
	}
	if iface.NumMethod() != 3 {
		t.Fatalf("expected NotificationSource to have exactly 3 read-only methods, got %d", iface.NumMethod())
	}
}
