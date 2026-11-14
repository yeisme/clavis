package clipboard_test

import (
	"context"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/clipboard"
)

func TestMemoryStoreGenerationTracksMutations(t *testing.T) {
	s := clipboard.NewMemoryStore(clipboard.NewPrivacyFilter())
	ctx := context.Background()
	if s.Generation() != 0 {
		t.Fatal("new store generation must be zero")
	}
	item, _, err := s.Record(ctx, clipboard.CaptureCandidate{Content: "generation", Timestamp: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	g := s.Generation()
	if g == 0 {
		t.Fatal("record did not advance generation")
	}
	if err := s.Pin(ctx, item.ID, true); err != nil {
		t.Fatal(err)
	}
	if s.Generation() <= g {
		t.Fatal("pin did not advance generation")
	}
	if err := s.Delete(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if s.Generation() <= g {
		t.Fatal("delete did not advance generation")
	}
}
