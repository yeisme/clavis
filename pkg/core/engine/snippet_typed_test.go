package engine

import (
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
)

// TestSnippetMatchKeepsTypedTrigger: the match carries the runes the user
// actually typed (here a full-width prefix from a Chinese IME), which is
// what an undo must restore, not the canonical trigger.
func TestSnippetMatchKeepsTypedTrigger(t *testing.T) {
	e := NewSnippetEngine([]config.SnippetItem{{ID: "sig", Trigger: ";sig", Content: "Best regards,"}}, 64)
	var m *SnippetMatch
	for _, r := range "x；sig" {
		if got, ok := e.Feed(r, FeedOptions{Now: time.Now()}); ok {
			m = got
		}
	}
	if m == nil {
		t.Fatal("full-width trigger did not match")
	}
	if m.TypedTrigger != "；sig" || m.DeleteCount != 4 {
		t.Fatalf("TypedTrigger = %q, DeleteCount = %d; want %q, 4", m.TypedTrigger, m.DeleteCount, "；sig")
	}
}
