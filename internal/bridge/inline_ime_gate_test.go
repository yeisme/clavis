//go:build darwin

package bridge

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/host"
)

func TestInlineIMEGateFor(t *testing.T) {
	cases := []struct {
		name string
		src  host.InputSourceInfo
		want bool
	}{
		{"abc layout", host.InputSourceInfo{ID: "com.apple.keylayout.ABC", IsASCII: true}, true},
		{"us layout", host.InputSourceInfo{ID: "com.apple.keylayout.US", IsASCII: true}, true},
		{"apple pinyin", host.InputSourceInfo{ID: "com.apple.inputmethod.SCIM.ITABC", IsASCII: false}, false},
		{"sogou pinyin", host.InputSourceInfo{ID: "com.sogou.inputmethod.sogou.pinyin", IsASCII: false}, false},
		{"squirrel", host.InputSourceInfo{ID: "im.rime.inputmethod.Squirrel", IsASCII: false}, false},
		{"clavis imk", host.InputSourceInfo{ID: "com.yeisme.clavis.inputmethod", IsASCII: true}, false},
		{"windows style ascii mode", host.InputSourceInfo{ID: "00000804", IsASCII: false, Mode: host.InputModeASCII}, true},
		{"windows style native mode", host.InputSourceInfo{ID: "00000804", IsASCII: true, Mode: host.InputModeNative}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inlineIMEGateFor(tc.src); got != tc.want {
				t.Fatalf("inlineIMEGateFor(%+v) = %v, want %v", tc.src, got, tc.want)
			}
		})
	}
}
