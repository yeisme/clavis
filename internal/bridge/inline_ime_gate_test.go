//go:build darwin

package bridge

import (
	"context"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/host"
)

func TestHookMarkedTextUsesFreshObservation(t *testing.T) {
	b := &darwinBridge{}
	now := time.Now()
	if !b.hookMarkedText(now) {
		t.Fatal("unobserved native IME state allowed snippet expansion")
	}
	b.markedText.Store(false, now)
	if b.hookMarkedText(now) || !b.hookMarkedText(now.Add(time.Second)) {
		t.Fatal("only a fresh negative observation may allow snippet expansion")
	}
	b.markedText.Store(true, now)
	if !b.hookMarkedText(now) {
		t.Fatal("active preedit allowed snippet expansion")
	}
	b.observeInlineInputSource(host.InputSourceInfo{ID: "abc", IsASCII: true})
	if b.hookMarkedText(now) {
		t.Fatal("ASCII input source must bypass native preedit observation")
	}
}

func TestInlineIMEGateUsesOnlyObservedSource(t *testing.T) {
	b := &darwinBridge{}
	current, err := b.CurrentInputSource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b.observeInlineInputSource(host.InputSourceInfo{ID: "different", IsASCII: !inlineIMEGateFor(current)})
	if got := b.inlineIMEGate(); got == inlineIMEGateFor(current) {
		t.Fatalf("hook queried live source %q instead of the observed source", current.ID)
	}
	b.observeInlineInputSource(host.InputSourceInfo{})
	if b.inlineIMEGate() {
		t.Fatal("unknown source permitted takeover")
	}
}

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
