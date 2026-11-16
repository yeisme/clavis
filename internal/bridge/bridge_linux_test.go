//go:build !darwin && !windows

package bridge

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/clipboard"
)

func TestLinuxClipboardHasTextFormat(t *testing.T) {
	if !linuxClipboardHasTextFormat([]string{"TARGETS", "UTF8_STRING"}) {
		t.Fatal("UTF8_STRING should be recognized as a text target")
	}
	if linuxClipboardHasTextFormat([]string{"image/png", "text/uri-list"}) {
		t.Fatal("media-only targets should not be recognized as text")
	}
}

func TestLinuxClipboardKindPreservesTextPriority(t *testing.T) {
	for _, tc := range []struct {
		name    string
		formats []string
		text    string
		want    clipboard.Kind
	}{
		{name: "text over image", formats: []string{"text/plain", "image/png"}, text: "hello", want: clipboard.KindText},
		{name: "file uri", formats: []string{"text/uri-list"}, want: clipboard.KindFile},
		{name: "mygo file list", formats: []string{"application/x-mygo-file-list"}, want: clipboard.KindFile},
		{name: "image mime", formats: []string{"image/png"}, want: clipboard.KindImage},
		{name: "empty text is not text", formats: []string{"text/plain"}, want: ""},
		{name: "unknown format", formats: []string{"application/octet-stream"}, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := linuxClipboardKind(tc.formats, tc.text); got != tc.want {
				t.Fatalf("linuxClipboardKind(%q, %q) = %q, want %q", tc.formats, tc.text, got, tc.want)
			}
		})
	}
}

func TestLinuxClipboardFormatSignatureDeduplicatesAndSorts(t *testing.T) {
	first := linuxClipboardFormatSignature([]string{" IMAGE/PNG ", "text/plain", "image/png"})
	second := linuxClipboardFormatSignature([]string{"text/plain", "image/png"})
	if first != second {
		t.Fatalf("format signatures differ for equivalent target sets: %q != %q", first, second)
	}
	if got := linuxClipboardFormatSignature([]string{"text/plain", "image/png"}); got == linuxClipboardFormatSignature([]string{"text/plain", "text/uri-list"}) {
		t.Fatal("different target sets must have different signatures")
	}
}
