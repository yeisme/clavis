//go:build darwin

package bridge

import (
	"context"
	"testing"

	"github.com/yeisme/clavis/pkg/core/clipboard"
)

func TestClassifyDarwinClipboard(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		types []string
		want  clipboard.Kind
	}{
		{name: "text wins over file and image", text: "hello", types: []string{"public.file-url", "public.png"}, want: clipboard.KindText},
		{name: "file URL", types: []string{"public.file-url"}, want: clipboard.KindFile},
		{name: "PNG", types: []string{"public.png"}, want: clipboard.KindImage},
		{name: "image MIME", types: []string{"image/png"}, want: clipboard.KindImage},
		{name: "file wins over image", types: []string{"public.png", "public.file-url"}, want: clipboard.KindFile},
		{name: "unknown representations", types: []string{"public.rtf", "org.example.custom"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyDarwinClipboard(tt.text, tt.types); got != tt.want {
				t.Fatalf("classifyDarwinClipboard(%q, %v) = %q, want %q", tt.text, tt.types, got, tt.want)
			}
		})
	}
}

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
