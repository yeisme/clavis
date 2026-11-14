package bridge

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/clipboard"
)

func TestClassifyClipboardFormats(t *testing.T) {
	cases := []struct {
		f    winClipFormats
		want clipboard.Kind
	}{
		{winClipFormats{Text: true, DIB: true}, clipboard.KindText},
		{winClipFormats{HDrop: true, DIB: true}, clipboard.KindFile},
		{winClipFormats{PNG: true}, clipboard.KindImage},
		{winClipFormats{DIB: true}, clipboard.KindImage},
		{winClipFormats{}, ""},
	}
	for i, c := range cases {
		if got := classifyClipboardFormats(c.f); got != c.want {
			t.Fatalf("case %d: %v want %v", i, got, c.want)
		}
	}
}

func TestBGRAToRGBAAndDuration(t *testing.T) {
	img := bgraToRGBA([]byte{10, 20, 30, 0, 1, 2, 3, 0}, 2, 1)
	if p := img.Pix[:4]; p[0] != 30 || p[1] != 20 || p[2] != 10 || p[3] != 255 {
		t.Fatalf("opaque fix: %v", p)
	}
	img = bgraToRGBA([]byte{10, 20, 30, 128}, 1, 1)
	if img.Pix[3] != 128 {
		t.Fatal("real alpha lost")
	}
	if got := hundredNsToMs(425_000_000); got != 42_500 {
		t.Fatalf("duration %d", got)
	}
	if _, ok := statFileSize("/definitely/missing/file"); ok {
		t.Fatal("missing file reported present")
	}
}
