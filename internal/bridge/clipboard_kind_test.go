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
