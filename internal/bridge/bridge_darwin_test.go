//go:build darwin

package bridge

import (
	"context"
	"testing"
)

func TestDarwinBridge_PureGoRuntimeCalls(t *testing.T) {
	b := NewHostService().(*darwinBridge)
	ctx := context.Background()

	if err := initDarwin(); err != nil {
		t.Fatalf("initDarwin failed: %v", err)
	}

	if _, err := b.Permissions(ctx); err != nil {
		t.Fatalf("Permissions failed: %v", err)
	}

	sources, err := b.InputSources(ctx)
	if err != nil {
		t.Fatalf("InputSources failed: %v", err)
	}
	if len(sources) == 0 {
		t.Fatal("expected at least one macOS input source")
	}

	cur, err := b.CurrentInputSource(ctx)
	if err != nil {
		t.Fatalf("CurrentInputSource failed: %v", err)
	}
	if cur.ID == "" {
		t.Fatalf("expected non-empty current input source ID, got %+v", cur)
	}

	displays := darwinDisplays()
	if len(displays) == 0 {
		t.Fatal("expected at least one display from darwinDisplays()")
	}

	snap, err := b.SnapshotContext(ctx)
	if err != nil {
		t.Fatalf("SnapshotContext failed: %v", err)
	}
	if snap.CursorAnchor == nil || !snap.CursorAnchor.Valid {
		t.Fatalf("expected valid fallback cursor anchor, got %+v", snap.CursorAnchor)
	}
}
