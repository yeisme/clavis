package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/taxonomy"
)

func TestAnnotationStoreCRUDAndPersistence(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "ann.db")
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewAnnotationStore(db)
	if err != nil {
		t.Fatalf("NewAnnotationStore: %v", err)
	}
	if store.Gen() != 1 {
		t.Fatalf("initial Gen() = %d, want 1", store.Gen())
	}

	saved, err := store.Upsert(ctx, EntityAnnotation{
		EntityKind:  AnnotationKindAgentSession,
		EntityKey:   "codex:sess-1",
		CustomTitle: "  Refactor Auth  ",
		Tags:        []string{"#Review", "review", "ENV:Work", "中文标签"},
		Favorite:    true,
		Notes:       "  check edge cases  ",
		Props: map[string]string{
			"branch": "main",
			"model":  "gpt-5",
		},
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if saved.CustomTitle != "Refactor Auth" || saved.Notes != "check edge cases" {
		t.Fatalf("unexpected trimmed fields: %+v", saved)
	}
	wantTags := []string{"review", "env:work", "中文标签"}
	if !reflect.DeepEqual(saved.Tags, wantTags) {
		t.Fatalf("saved.Tags = %v, want %v", saved.Tags, wantTags)
	}
	if store.Gen() != 2 {
		t.Fatalf("Gen() after Upsert = %d, want 2", store.Gen())
	}

	got, ok := store.Get(AnnotationKindAgentSession, "codex:sess-1")
	if !ok {
		t.Fatal("Get returned false")
	}
	// Mutating returned clone must not affect store snapshot.
	got.Tags[0] = "mutated"
	got.Props["branch"] = "dirty"
	got2, ok := store.Get(AnnotationKindAgentSession, "codex:sess-1")
	if !ok || got2.Tags[0] != "review" || got2.Props["branch"] != "main" {
		t.Fatalf("snapshot was mutated through returned copy: %+v", got2)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen DB and verify persistence across restarts.
	db2, err := sqlite.Open(ctx, dbPath, sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	store2, err := NewAnnotationStore(db2)
	if err != nil {
		t.Fatalf("reopen NewAnnotationStore: %v", err)
	}
	reloaded, ok := store2.Get(AnnotationKindAgentSession, "codex:sess-1")
	if !ok {
		t.Fatal("annotation missing after reopen")
	}
	if reloaded.CustomTitle != "Refactor Auth" || !reloaded.Favorite || !reflect.DeepEqual(reloaded.Tags, wantTags) || reloaded.Props["model"] != "gpt-5" {
		t.Fatalf("reloaded mismatch: %+v", reloaded)
	}

	// SetFavorite / SetTags / Delete lifecycle.
	if err := store2.SetFavorite(ctx, AnnotationKindClipboard, "clip:1", true); err != nil {
		t.Fatalf("SetFavorite: %v", err)
	}
	if err := store2.SetTags(ctx, AnnotationKindClipboard, "clip:1", []string{"#Pin", "Go"}); err != nil {
		t.Fatalf("SetTags: %v", err)
	}
	clipAnn, ok := store2.Get(AnnotationKindClipboard, "clip:1")
	if !ok || !clipAnn.Favorite || !reflect.DeepEqual(clipAnn.Tags, []string{"pin", "go"}) {
		t.Fatalf("clipAnn mismatch: %+v ok=%v", clipAnn, ok)
	}

	// Clearing favorite & tags prunes empty row automatically.
	if err := store2.SetFavorite(ctx, AnnotationKindClipboard, "clip:1", false); err != nil {
		t.Fatalf("unset SetFavorite: %v", err)
	}
	if err := store2.SetTags(ctx, AnnotationKindClipboard, "clip:1", nil); err != nil {
		t.Fatalf("clear SetTags: %v", err)
	}
	if _, ok := store2.Get(AnnotationKindClipboard, "clip:1"); ok {
		t.Fatal("expected empty annotation row to be pruned")
	}

	if err := store2.Delete(ctx, AnnotationKindAgentSession, "codex:sess-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := store2.Get(AnnotationKindAgentSession, "codex:sess-1"); ok {
		t.Fatal("expected deleted annotation to disappear")
	}
}

func TestAnnotationStoreNormalizationAndD4FailClosed(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:", sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store, err := NewAnnotationStore(db)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Upsert(ctx, EntityAnnotation{
		EntityKind: AnnotationKindTemplate,
		EntityKey:  "pr:code-review",
		Tags: []string{
			"  #VeryLongTagNameThatExceeds24Runes  ",
			"t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9",
		},
		Props: map[string]string{
			"p1": "1", "p2": "2", "p3": "3", "p4": "4", "p5": "5",
			"p6": "6", "p7": "7", "p8": "8", "p9": "9",
		},
	})
	if err != nil {
		t.Fatalf("Upsert normalization: %v", err)
	}
	got, ok := store.Get(AnnotationKindTemplate, "pr:code-review")
	if !ok {
		t.Fatal("expected template annotation")
	}
	if len(got.Tags) != taxonomy.MaxTagsPerEntity || len([]rune(got.Tags[0])) != taxonomy.MaxTagRunes {
		t.Fatalf("tags normalization/cap failed: %v", got.Tags)
	}
	if len(got.Props) != MaxAnnotationProps {
		t.Fatalf("props cap = %d, want %d", len(got.Props), MaxAnnotationProps)
	}

	genBefore := store.Gen()

	// Reject secret in Tags (including 9th tag or >24 runes).
	for _, badTags := range [][]string{
		{"sk-proj-1234567890abcdef"},
		{"ghp_1234567890abcdef"},
		{"my-password"},
		{"secret"},
		{"api_token"},
		{"-----BEGIN PRIVATE KEY-----"},
		{"aB3dE6gH9jK2mN5pQ8rS"},
		{"ok1", "ok2", "ok3", "ok4", "ok5", "ok6", "ok7", "ok8", "sk-hidden-ninth"},
	} {
		if _, err := store.Upsert(ctx, EntityAnnotation{
			EntityKind: AnnotationKindTemplate,
			EntityKey:  "pr:code-review",
			Tags:       badTags,
		}); !errors.Is(err, taxonomy.ErrUnsafeMetadataTag) {
			t.Fatalf("Upsert badTags=%v got err=%v, want ErrUnsafeMetadataTag", badTags, err)
		}
		if err := store.SetTags(ctx, AnnotationKindTemplate, "pr:code-review", badTags); !errors.Is(err, taxonomy.ErrUnsafeMetadataTag) {
			t.Fatalf("SetTags badTags=%v got err=%v, want ErrUnsafeMetadataTag", badTags, err)
		}
	}

	// Reject secret in Props key or value.
	if _, err := store.Upsert(ctx, EntityAnnotation{
		EntityKind: AnnotationKindTemplate,
		EntityKey:  "pr:code-review",
		Props:      map[string]string{"api_key": "harmless"},
	}); !errors.Is(err, taxonomy.ErrUnsafeMetadataTag) {
		t.Fatalf("expected ErrUnsafeMetadataTag for secret prop key, got %v", err)
	}
	if _, err := store.Upsert(ctx, EntityAnnotation{
		EntityKind: AnnotationKindTemplate,
		EntityKey:  "pr:code-review",
		Props:      map[string]string{"note": "ghp_1234567890abcdef"},
	}); !errors.Is(err, taxonomy.ErrUnsafeMetadataTag) {
		t.Fatalf("expected ErrUnsafeMetadataTag for secret prop value, got %v", err)
	}

	// Verify existing snapshot and Gen() were completely untouched by rejected writes.
	if store.Gen() != genBefore {
		t.Fatalf("Gen() changed on rejected writes: got %d want %d", store.Gen(), genBefore)
	}
	after, _ := store.Get(AnnotationKindTemplate, "pr:code-review")
	if !reflect.DeepEqual(after.Tags, got.Tags) || !reflect.DeepEqual(after.Props, got.Props) {
		t.Fatalf("snapshot modified after rejected write: %+v vs %+v", after, got)
	}
}

func TestAnnotationStoreOrderingAndTagCounts(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:", sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store, err := NewAnnotationStore(db)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = store.Upsert(ctx, EntityAnnotation{
		EntityKind: AnnotationKindAgentSession,
		EntityKey:  "s-old-normal",
		Tags:       []string{"go", "review"},
		Favorite:   false,
	})
	time.Sleep(2 * time.Millisecond)
	_, _ = store.Upsert(ctx, EntityAnnotation{
		EntityKind: AnnotationKindAgentSession,
		EntityKey:  "s-fav-older",
		Tags:       []string{"go", "env:work"},
		Favorite:   true,
	})
	time.Sleep(2 * time.Millisecond)
	_, _ = store.Upsert(ctx, EntityAnnotation{
		EntityKind: AnnotationKindAgentSession,
		EntityKey:  "s-new-normal",
		Tags:       []string{"alpha"},
		Favorite:   false,
	})
	time.Sleep(2 * time.Millisecond)
	_, _ = store.Upsert(ctx, EntityAnnotation{
		EntityKind: AnnotationKindAgentSession,
		EntityKey:  "s-fav-newer",
		Tags:       []string{"review"},
		Favorite:   true,
	})
	_, _ = store.Upsert(ctx, EntityAnnotation{
		EntityKind: AnnotationKindClipboard,
		EntityKey:  "c-1",
		Tags:       []string{"go"},
		Favorite:   true,
	})

	list := store.ListByKind(AnnotationKindAgentSession)
	if len(list) != 4 {
		t.Fatalf("ListByKind len = %d, want 4", len(list))
	}
	gotOrder := []string{list[0].EntityKey, list[1].EntityKey, list[2].EntityKey, list[3].EntityKey}
	wantOrder := []string{"s-fav-newer", "s-fav-older", "s-new-normal", "s-old-normal"}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("ListByKind order = %v, want %v", gotOrder, wantOrder)
	}

	counts := store.TagCounts(AnnotationKindAgentSession)
	wantCounts := []taxonomy.TagCount{
		{Tag: "go", Count: 2},
		{Tag: "review", Count: 2},
		{Tag: "alpha", Count: 1},
		{Tag: "env:work", Count: 1},
	}
	if !reflect.DeepEqual(counts, wantCounts) {
		t.Fatalf("TagCounts = %+v, want %+v", counts, wantCounts)
	}
}

func TestAnnotationStoreConcurrentRace(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:", sqlite.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store, err := NewAnnotationStore(db)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := range 25 {
				key := fmt.Sprintf("k-%d-%d", worker, i%5)
				if _, err := store.Upsert(ctx, EntityAnnotation{
					EntityKind:  AnnotationKindAgentSession,
					EntityKey:   key,
					CustomTitle: fmt.Sprintf("Title %d", i),
					Tags:        []string{"go", fmt.Sprintf("w%d", worker)},
					Favorite:    i%2 == 0,
				}); err != nil {
					t.Errorf("concurrent Upsert: %v", err)
				}
				if err := store.SetFavorite(ctx, AnnotationKindAgentSession, key, i%3 == 0); err != nil {
					t.Errorf("concurrent SetFavorite: %v", err)
				}
				if err := store.SetTags(ctx, AnnotationKindAgentSession, key, []string{"shared", fmt.Sprintf("t%d", i%3)}); err != nil {
					t.Errorf("concurrent SetTags: %v", err)
				}
				if i%7 == 0 {
					if err := store.Delete(ctx, AnnotationKindAgentSession, key); err != nil {
						t.Errorf("concurrent Delete: %v", err)
					}
				}
			}
		}(w)
	}

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				_ = store.Gen()
				_, _ = store.Get(AnnotationKindAgentSession, fmt.Sprintf("k-0-%d", i%5))
				_ = store.ListByKind(AnnotationKindAgentSession)
				_ = store.TagCounts(AnnotationKindAgentSession)
			}
		}()
	}
	wg.Wait()
}
