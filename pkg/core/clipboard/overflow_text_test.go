package clipboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateUTF8BytesPreservesRuneBoundaries(t *testing.T) {
	// 3-byte CJK runes must never be cut mid-character.
	s := strings.Repeat("中", 25) // 75 bytes
	prefix, truncated := TruncateUTF8Bytes(s, 64)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if !utf8.ValidString(prefix) {
		t.Fatalf("truncated string is invalid UTF-8: %q", prefix)
	}
	if len(prefix) != 63 || len(prefix) > 64 {
		t.Fatalf("byte length = %d, want 63", len(prefix))
	}

	exact, truncated := TruncateUTF8Bytes(s, 75)
	if truncated || exact != s {
		t.Fatalf("exact length should not truncate: truncated=%v len=%d", truncated, len(exact))
	}
}

func TestMediaStoreSaveAndLoadOverflowText(t *testing.T) {
	dir := t.TempDir()
	ms := NewMediaStore(dir)
	large := strings.Repeat("2026-11-28T00:00:00Z INFO 日志line 日本語\n", 3000)

	rel, sha, err := ms.SaveOverflowText(large)
	if err != nil {
		t.Fatalf("SaveOverflowText: %v", err)
	}
	if !strings.HasPrefix(rel, "text/") || !strings.HasSuffix(rel, ".txt.gz") || len(sha) != 64 {
		t.Fatalf("unexpected rel=%q sha=%q", rel, sha)
	}

	got, err := ms.LoadOverflowText(rel, sha)
	if err != nil {
		t.Fatalf("LoadOverflowText: %v", err)
	}
	if got != large {
		t.Fatalf("round-trip mismatch: got len=%d want len=%d", len(got), len(large))
	}

	// Tampering or wrong SHA must fail closed.
	if _, err := ms.LoadOverflowText(rel, strings.Repeat("0", 64)); err == nil {
		t.Fatal("expected checksum mismatch error")
	}

	// Path traversal must be rejected.
	if _, err := ms.LoadOverflowText("../escape.txt.gz", ""); err == nil {
		t.Fatal("expected path traversal error")
	}

	// Regular GC on image store must leave text/ untouched.
	removed, err := ms.GC(nil)
	if err != nil || removed != 0 {
		t.Fatalf("GC removed=%d err=%v, want 0", removed, err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("overflow file deleted by GC: %v", err)
	}
}
