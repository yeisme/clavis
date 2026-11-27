package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/clipboard"
)

func openTestStore(t *testing.T, dbPath string, filter clipboard.PrivacyFilter) *SQLiteClipboardStore {
	t.Helper()
	store, err := NewSQLiteClipboardStore(context.Background(), dbPath, filter)
	if err != nil {
		t.Fatalf("NewSQLiteClipboardStore(%q): %v", dbPath, err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	return store
}

func TestSQLiteClipboardStore_PrivacyFilter(t *testing.T) {
	filter := clipboard.NewPrivacyFilter()
	filter.ExcludedBundleIDs["com.1password.1password"] = struct{}{}
	store := openTestStore(t, ":memory:", filter)
	ctx := context.Background()

	// Empty/whitespace ignored
	if item, recorded, err := store.Record(ctx, clipboard.CaptureCandidate{Content: "   \n\t "}); err != nil || recorded || item != nil {
		t.Fatalf("expected whitespace to be ignored, got item=%v recorded=%v err=%v", item, recorded, err)
	}
	// Concealed pasteboard type ignored
	if _, recorded, err := store.Record(ctx, clipboard.CaptureCandidate{
		Content:         "super-secret",
		PasteboardTypes: []string{"public.utf8-plain-text", "org.nspasteboard.ConcealedType"},
	}); err != nil || recorded {
		t.Fatalf("expected concealed type to be ignored, got recorded=%v err=%v", recorded, err)
	}
	// Windows clipboard exclusion marker ignored
	if _, recorded, err := store.Record(ctx, clipboard.CaptureCandidate{
		Content:         "win-secret",
		PasteboardTypes: []string{"ExcludeClipboardContentFromMonitorProcessing"},
	}); err != nil || recorded {
		t.Fatalf("expected Windows exclusion marker to be ignored, got recorded=%v err=%v", recorded, err)
	}
	// Excluded bundle ID ignored
	if _, recorded, err := store.Record(ctx, clipboard.CaptureCandidate{
		Content:        "pw-123",
		SourceBundleID: "com.1password.1password",
		SourceAppName:  "1Password",
	}); err != nil || recorded {
		t.Fatalf("expected excluded bundle id to be ignored, got recorded=%v err=%v", recorded, err)
	}
	// Normal item recorded
	item, recorded, err := store.Record(ctx, clipboard.CaptureCandidate{
		Content:        "hello world",
		SourceBundleID: "com.apple.Notes",
		SourceAppName:  "Notes",
	})
	if err != nil || !recorded || item == nil {
		t.Fatalf("expected normal item to record, got item=%v recorded=%v err=%v", item, recorded, err)
	}
}

func TestSQLiteClipboardStore_DedupPreservesPin(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "clipboard.db")
	store := openTestStore(t, dbPath, clipboard.NewPrivacyFilter())
	ctx := context.Background()

	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	first, ok, err := store.Record(ctx, clipboard.CaptureCandidate{
		Content:        "reusable snippet",
		SourceBundleID: "com.apple.Notes",
		SourceAppName:  "Notes",
		Timestamp:      t1,
	})
	if err != nil || !ok {
		t.Fatalf("first Record failed: ok=%v err=%v", ok, err)
	}

	if err := store.Pin(ctx, first.ID, true); err != nil {
		t.Fatalf("Pin failed: %v", err)
	}

	t2 := t1.Add(5 * time.Minute)
	dup, ok, err := store.Record(ctx, clipboard.CaptureCandidate{
		Content:        "reusable snippet",
		SourceBundleID: "com.apple.Terminal",
		SourceAppName:  "Terminal",
		Timestamp:      t2,
	})
	if err != nil || !ok {
		t.Fatalf("dedup Record failed: ok=%v err=%v", ok, err)
	}
	if dup.ID != first.ID {
		t.Fatalf("expected dedup to keep ID %q, got %q", first.ID, dup.ID)
	}
	if first.CopyCount != 1 || dup.CopyCount != 2 {
		t.Fatalf("expected CopyCount 1 -> 2 on dedup, got first=%d dup=%d", first.CopyCount, dup.CopyCount)
	}
	if !dup.CreatedAt.Equal(t1) || !dup.UpdatedAt.Equal(t2) {
		t.Fatalf("expected CreatedAt=%v UpdatedAt=%v, got CreatedAt=%v UpdatedAt=%v", t1, t2, dup.CreatedAt, dup.UpdatedAt)
	}
	if !dup.IsPinned {
		t.Fatal("expected dedup to preserve IsPinned=true")
	}
	if !dup.UpdatedAt.Equal(t2) {
		t.Fatalf("expected UpdatedAt=%v, got %v", t2, dup.UpdatedAt)
	}
	if dup.SourceAppName != "Terminal" {
		t.Fatalf("expected SourceAppName updated to Terminal, got %q", dup.SourceAppName)
	}

	recent, err := store.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("Recent failed: %v", err)
	}
	if len(recent) != 1 || !recent[0].IsPinned {
		t.Fatalf("expected 1 pinned item, got %+v", recent)
	}
}

func TestSQLiteClipboardStore_SearchFTS5AndCJKAndFallback(t *testing.T) {
	store := openTestStore(t, ":memory:", clipboard.NewPrivacyFilter())
	ctx := context.Background()

	base := time.Now().UTC()
	samples := []clipboard.CaptureCandidate{
		{Content: "kubectl get pods -n production", SourceAppName: "iTerm2", Timestamp: base.Add(1 * time.Second)},
		{Content: "这是一个用于测试中文全文检索的剪贴板条目", SourceAppName: "WeChat", Timestamp: base.Add(2 * time.Second)},
		{Content: "foo_bar.baz/qux?x=1&y=2 100%", SourceAppName: "VS Code", Timestamp: base.Add(3 * time.Second)},
		{Content: "SELECT * FROM clipboard_items;", SourceAppName: "TablePlus", Timestamp: base.Add(4 * time.Second)},
	}
	for _, c := range samples {
		if _, ok, err := store.Record(ctx, c); err != nil || !ok {
			t.Fatalf("Record(%q) failed: ok=%v err=%v", c.Content, ok, err)
		}
	}

	// Prefix FTS5 search
	res, err := store.Search(ctx, "kube prod", 10)
	if err != nil || len(res) != 1 || res[0].SourceAppName != "iTerm2" {
		t.Fatalf("FTS5 prefix search failed: len=%d err=%v res=%+v", len(res), err, res)
	}

	// Source app search via FTS5
	res, err = store.Search(ctx, "TablePl", 10)
	if err != nil || len(res) != 1 || res[0].SourceAppName != "TablePlus" {
		t.Fatalf("FTS5 app search failed: len=%d err=%v res=%+v", len(res), err, res)
	}

	// CJK substring search via LIKE fallback
	res, err = store.Search(ctx, "中文全文", 10)
	if err != nil || len(res) != 1 || res[0].SourceAppName != "WeChat" {
		t.Fatalf("CJK search failed: len=%d err=%v res=%+v", len(res), err, res)
	}

	// Escaped LIKE special chars (% and _) and punctuation
	res, err = store.Search(ctx, "100%", 10)
	if err != nil || len(res) != 1 || res[0].SourceAppName != "VS Code" {
		t.Fatalf("escaped LIKE percent search failed: len=%d err=%v res=%+v", len(res), err, res)
	}
	res, err = store.Search(ctx, "foo_bar.baz", 10)
	if err != nil || len(res) != 1 || res[0].SourceAppName != "VS Code" {
		t.Fatalf("punctuation search failed: len=%d err=%v res=%+v", len(res), err, res)
	}

	// Update + Delete sync with FTS5 triggers
	if err := store.Delete(ctx, res[0].ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	resAfterDel, err := store.Search(ctx, "foo_bar.baz", 10)
	if err != nil || len(resAfterDel) != 0 {
		t.Fatalf("expected deleted item not returned, got %+v (err=%v)", resAfterDel, err)
	}
}

func TestSQLiteClipboardStore_SpecialCharacterSearch(t *testing.T) {
	store := openTestStore(t, ":memory:", clipboard.NewPrivacyFilter())
	ctx := context.Background()
	for _, content := range []string{"foo-bar", "C++", `say "foo-bar"`} {
		if _, ok, err := store.Record(ctx, clipboard.CaptureCandidate{Content: content}); err != nil || !ok {
			t.Fatalf("record %q: %v", content, err)
		}
	}
	for _, query := range []string{"foo-bar", "C++", `"foo-bar"`} {
		got, err := store.Search(ctx, query, 10)
		if err != nil || len(got) == 0 {
			t.Fatalf("search %q: got=%v err=%v", query, got, err)
		}
	}
}

func TestSQLiteClipboardStore_PruneExpiredAndMaxItemsAndPersistence(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "clavis.db")
	ctx := context.Background()

	store := openTestStore(t, dbPath, clipboard.NewPrivacyFilter())
	now := time.Now().UTC()

	// Old pinned item (should survive retention and maxItems pruning)
	oldPinned, _, err := store.Record(ctx, clipboard.CaptureCandidate{
		Content:   "old pinned",
		Timestamp: now.Add(-60 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Pin(ctx, oldPinned.ID, true); err != nil {
		t.Fatal(err)
	}

	// Old unpinned item (should be removed by retentionDays=30)
	if _, _, err := store.Record(ctx, clipboard.CaptureCandidate{
		Content:   "old unpinned",
		Timestamp: now.Add(-45 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	// Recent unpinned items
	for i := range 4 {
		if _, _, err := store.Record(ctx, clipboard.CaptureCandidate{
			Content:   "recent-" + string(rune('a'+i)),
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Total = 6; retentionDays=30 removes 1 ("old unpinned") -> 5 left; maxItems=3 removes 2 oldest recent unpinned -> 3 left (1 pinned + 2 newest).
	removed, err := store.PruneExpired(ctx, 30, 3)
	if err != nil {
		t.Fatalf("PruneExpired failed: %v", err)
	}
	if removed != 3 {
		t.Fatalf("expected 3 removed, got %d", removed)
	}

	items, err := store.Recent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items remaining, got %d: %+v", len(items), items)
	}
	if items[0].Content != "old pinned" || !items[0].IsPinned {
		t.Fatalf("expected pinned item first, got %+v", items[0])
	}
	if items[1].Content != "recent-d" || items[2].Content != "recent-c" {
		t.Fatalf("expected newest two unpinned items, got %+v", items[1:])
	}

	// Re-open persisted file and verify data + Clear
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestStore(t, dbPath, clipboard.NewPrivacyFilter())
	persisted, err := reopened.Recent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 3 {
		t.Fatalf("expected 3 persisted items after reopen, got %d", len(persisted))
	}
	if err := reopened.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	afterClear, err := reopened.Recent(ctx, 10)
	if err != nil || len(afterClear) != 0 {
		t.Fatalf("expected empty after Clear, got len=%d err=%v", len(afterClear), err)
	}
}

func TestSQLiteClipboardStore_ClearUnpinnedPreservesPinned(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "unpinned.db"), clipboard.NewPrivacyFilter())
	ctx := context.Background()

	pinned, _, err := store.Record(ctx, clipboard.CaptureCandidate{Content: "keep-pinned"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Pin(ctx, pinned.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Record(ctx, clipboard.CaptureCandidate{Content: "drop-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Record(ctx, clipboard.CaptureCandidate{Content: "drop-2"}); err != nil {
		t.Fatal(err)
	}

	removed, err := store.ClearUnpinned(ctx)
	if err != nil {
		t.Fatalf("ClearUnpinned failed: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	left, err := store.Recent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].ID != pinned.ID || !left[0].IsPinned {
		t.Fatalf("expected only pinned item preserved, got %+v", left)
	}
}

func TestSQLiteClipboardStore_OverflowTextAndGetContent(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "app.db")
	mediaDir := filepath.Join(tmp, "clipboard-media")
	store, err := NewSQLiteClipboardStoreWithOptions(ctx, dbPath, clipboard.NewPrivacyFilter(), SQLiteClipboardStoreOptions{
		MediaDir: mediaDir,
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	large := strings.Repeat("INFO 2026-11-28 处理日志记录 request_id=abc123\n", 3000)
	if len(large) <= clipboard.InlineTextMaxBytes {
		t.Fatalf("test input too small: %d", len(large))
	}
	item, ok, err := store.Record(ctx, clipboard.CaptureCandidate{
		Content:       large,
		SourceAppName: "iTerm2",
		Timestamp:     time.Now().UTC(),
	})
	if err != nil || !ok {
		t.Fatalf("Record large text: ok=%v err=%v", ok, err)
	}
	if item.Content != large {
		t.Fatalf("Record returned truncated content len=%d want %d", len(item.Content), len(large))
	}

	// Summary page only projects <=512 chars while flagging ContentTruncated=true.
	summaries, _, err := store.ListSummaryPage(ctx, clipboard.ClipboardQuerySpec{Limit: 10})
	if err != nil || len(summaries) != 1 {
		t.Fatalf("ListSummaryPage: len=%d err=%v", len(summaries), err)
	}
	if !summaries[0].ContentTruncated || len([]rune(summaries[0].Preview)) > 512 || summaries[0].OverflowPath == "" {
		t.Fatalf("unexpected summary projection: %+v", summaries[0])
	}

	// GetContent transparently rehydrates full >64 KiB text.
	full, err := store.GetContent(ctx, item.ID)
	if err != nil || full != large {
		t.Fatalf("GetContent len=%d err=%v, want %d", len(full), err, len(large))
	}

	// Deduplication keeps overflow text and bumps CopyCount.
	dup, ok, err := store.Record(ctx, clipboard.CaptureCandidate{
		Content:       large,
		SourceAppName: "Ghostty",
		Timestamp:     time.Now().UTC().Add(time.Second),
	})
	if err != nil || !ok || dup.ID != item.ID || dup.CopyCount != 2 {
		t.Fatalf("dedup large text failed: dup=%+v ok=%v err=%v", dup, ok, err)
	}
}

func TestSQLiteClipboardStore_KeysetPaginationAndSQLFilters(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "paged.db"), clipboard.NewPrivacyFilter())
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 25; i++ {
		app := "Notes"
		content := fmt.Sprintf("常规英语笔记 entry-%02d", i)
		if i%3 == 0 {
			app = "Terminal"
			content = fmt.Sprintf("中文报错日志 /api/v1/orders/%02d 超时", i)
		}
		it, _, err := store.Record(ctx, clipboard.CaptureCandidate{
			Content:       content,
			SourceAppName: app,
			Timestamp:     base.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		if i == 2 || i == 19 {
			if err := store.Pin(ctx, it.ID, true); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Page across all 25 rows with limit=10: verify pinned first, zero duplicates, exact total=25.
	var gotIDs []string
	seen := map[string]bool{}
	var cursor *clipboard.ClipboardCursor
	for {
		page, next, err := store.ListSummaryPage(ctx, clipboard.ClipboardQuerySpec{
			Limit:  10,
			Cursor: cursor,
		})
		if err != nil {
			t.Fatalf("ListSummaryPage: %v", err)
		}
		for _, s := range page {
			if seen[s.ID] {
				t.Fatalf("duplicate ID %q across pages", s.ID)
			}
			seen[s.ID] = true
			gotIDs = append(gotIDs, s.ID)
		}
		if next == nil {
			break
		}
		cursor = next
	}
	if len(gotIDs) != 25 {
		t.Fatalf("paged total=%d, want 25", len(gotIDs))
	}

	// SQL filter pushdown: CJK trigram + AppNeedle + PinnedOnly.
	cjkMatches, _, err := store.ListSummaryPage(ctx, clipboard.ClipboardQuerySpec{
		Text:      "报错日志",
		AppNeedle: "term",
		Limit:     20,
	})
	if err != nil || len(cjkMatches) != 9 {
		t.Fatalf("CJK+App filter len=%d err=%v, want 9", len(cjkMatches), err)
	}
	pinnedOnly, _, err := store.ListSummaryPage(ctx, clipboard.ClipboardQuerySpec{
		PinnedOnly: true,
		Limit:      20,
	})
	if err != nil || len(pinnedOnly) != 2 {
		t.Fatalf("PinnedOnly len=%d err=%v, want 2", len(pinnedOnly), err)
	}
}
