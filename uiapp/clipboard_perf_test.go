package uiapp

import (
	"slices"
	"strings"
	"testing"
)

// The per-frame text helpers were rewritten not to copy or split a whole
// entry. These oracles are the previous implementations, kept to prove the
// output did not change.
func oldPreviewLines(text string, limit int) ([]string, int) {
	if strings.TrimSpace(text) == "" {
		return nil, 0
	}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(normalized, "\n"), "\n")
	hidden := 0
	if limit > 0 && len(lines) > limit {
		hidden = len(lines) - limit
		lines = lines[:limit:limit]
	}
	return lines, hidden
}

func oldPreviewLineCount(text string) int {
	return len(strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n"))
}

func oldClipboardLineCount(content string) int {
	s := strings.Trim(content, " \t\r\n")
	if s == "" {
		return 0
	}
	return strings.Count(strings.ReplaceAll(s, "\r\n", "\n"), "\n") + 1
}

func TestPreviewTextHelpersMatchPreviousOutput(t *testing.T) {
	texts := []string{
		"", "  ", "one", "one\n", "one\n\n\n", "a\nb", "a\r\nb\r\n", "a\r\r\n", "x\r\r\nz",
		"y\r", "\n\nlead", "a\n\nb\n", " \t\n x \n", "中文\r\n第二行\r\n第三行",
		strings.Repeat("line\n", 100), strings.Repeat("crlf\r\n", 57) + "tail",
	}
	for _, text := range texts {
		for _, limit := range []int{0, 1, 2, 3, 20, 40} {
			gotL, gotH := previewLines(text, limit)
			wantL, wantH := oldPreviewLines(text, limit)
			if !slices.Equal(gotL, wantL) || gotH != wantH {
				t.Fatalf("previewLines(%q, %d) = %q,%d; want %q,%d", text, limit, gotL, gotH, wantL, wantH)
			}
		}
		if got, want := previewLineCount(text), oldPreviewLineCount(text); got != want {
			t.Fatalf("previewLineCount(%q) = %d, want %d", text, got, want)
		}
		if got, want := clipboardLineCount(text), oldClipboardLineCount(text); got != want {
			t.Fatalf("clipboardLineCount(%q) = %d, want %d", text, got, want)
		}
	}
}

func TestPreviewIsMonoMatchesPreviousOutputForShortText(t *testing.T) {
	old := func(text string) bool {
		trimmed := strings.TrimSpace(text)
		return looksLikeCode(text) || looksLikeShellCommand(strings.ToLower(trimmed)) || looksLikePath(trimmed)
	}
	for _, text := range []string{"", "plain words", "func main() {}", "git status", "/usr/local/bin", "~/notes.txt",
		"SELECT 1;", "Hello\n\tindented", "微信支付回调签名校验失败", "  ls -la  ", "https://example.com"} {
		if got, want := previewIsMono(text), old(text); got != want {
			t.Fatalf("previewIsMono(%q) = %v, want %v", text, got, want)
		}
	}
	if !previewIsMono("package main\n\nfunc main() {\n" + strings.Repeat("\tprintln(1)\n", 5000) + "}") {
		t.Fatal("a long Go file must still preview in monospace")
	}
}

func TestClipboardContentIconReadsOnlyTheHead(t *testing.T) {
	long := strings.Repeat("2026-11-21 INFO served /api status=200\n", 4000)
	if got := clipboardContentIcon(long, ""); got != "doc.text" {
		t.Fatalf("long log icon = %q, want doc.text", got)
	}
	code := "package main\n\nfunc main() {\n" + strings.Repeat("\tprintln(1)\n", 5000) + "}\n"
	if got := clipboardContentIcon(code, ""); got != "curlybraces" {
		t.Fatalf("long code icon = %q, want curlybraces", got)
	}
	// Short single lines keep their kinds.
	for content, want := range map[string]string{
		"https://example.com/a": "link", "me@example.com": "envelope",
		"/usr/local/bin": "folder", "git status": "terminal", "func main() {}": "curlybraces", "plain words": "doc.text",
	} {
		if got := clipboardContentIcon(content, ""); got != want {
			t.Fatalf("clipboardContentIcon(%q) = %q, want %q", content, got, want)
		}
	}
}

// BenchmarkClipboardRowText measures the per-row, per-frame text work for one
// 256 KiB entry: its leading icon, its meta line and its preview text.
func BenchmarkClipboardRowText(b *testing.B) {
	text := strings.Repeat("2026-11-21T10:00:00Z INFO request served path=/api/v1/items status=200 latency=12ms\n", 3000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = clipboardContentIcon(text, "")
		_ = clipboardLineCount(text)
		_, _ = previewLines(text, 40)
		_ = previewLineCount(text)
		_ = previewIsMono(text)
	}
}
