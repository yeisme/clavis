package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestMediaZoomClampAndModes(t *testing.T) {
	cases := []struct {
		in, want float32
	}{
		{-1, mediaZoomMin},
		{0.25, mediaZoomMin},
		{1, 1},
		{8, mediaZoomMax},
		{12, mediaZoomMax},
	}
	for _, tc := range cases {
		if got := clampMediaZoom(tc.in); got != tc.want {
			t.Fatalf("clampMediaZoom(%v)=%v, want %v", tc.in, got, tc.want)
		}
	}
	if got := mediaZoomPercent(MediaZoomFit, 4); got != 0 {
		t.Fatalf("fit percent=%d, want 0", got)
	}
	if got := mediaZoomPercent(MediaZoomOriginal, 4); got != 100 {
		t.Fatalf("original percent=%d, want 100", got)
	}
	if got := mediaZoomPercent(MediaZoomCustom, 99); got != 800 {
		t.Fatalf("custom percent=%d, want 800", got)
	}
}

func TestMediaPreviewSelectionResetsZoom(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	first := engine.OmniboxItem{ID: "one", Kind: engine.ItemClipboardEntry, ClipboardMedia: &engine.ClipboardMedia{Kind: "image", Path: "/one.png"}}
	ctrl.resetMediaPreview(first)
	ctrl.setMediaZoom(MediaZoomCustom, 3)
	ctrl.uiState.mediaPreview.panX, ctrl.uiState.mediaPreview.panY = 20, -5
	ctrl.resetMediaPreview(first)
	if ctrl.uiState.mediaPreview.mode != MediaZoomCustom || ctrl.uiState.mediaPreview.zoom != 3 {
		t.Fatal("same media selection unexpectedly reset zoom")
	}
	second := engine.OmniboxItem{ID: "two", Kind: engine.ItemClipboardEntry, ClipboardMedia: &engine.ClipboardMedia{Kind: "image", Path: "/two.png"}}
	ctrl.resetMediaPreview(second)
	p := ctrl.uiState.mediaPreview
	if p.mode != MediaZoomFit || p.zoom != 1 || p.panX != 0 || p.panY != 0 {
		t.Fatalf("selection change state=%+v, want fit with zero pan", p)
	}
}

func TestMediaPreviewKeyboardDoesNotHandleOmniboxKeys(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.uiState.mediaPreview.focused = true
	for _, key := range []ui.Key{ui.KeyUp, ui.KeyDown, ui.KeyEnter, ui.KeyEscape} {
		if ctrl.handleMediaPreviewInput(ui.InputEvent{Kind: ui.InputKeyDown, Key: key}) {
			t.Fatalf("media canvas claimed Omnibox key %v", key)
		}
	}
	if !ctrl.handleMediaPreviewInput(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.Key1}) {
		t.Fatal("media canvas did not handle original-size shortcut")
	}
	if ctrl.uiState.mediaPreview.mode != MediaZoomOriginal {
		t.Fatal("1 did not select original-size mode")
	}
}

func TestMediaPreviewOriginalFailureFallsBack(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	p := mediaPreviewState{}
	if got := ctrl.requestOriginalBitmap("/definitely/missing-media-preview.png", &p); got != nil {
		t.Fatal("missing original unexpectedly returned a bitmap")
	}
	if !p.loading {
		t.Fatal("missing original did not enter the loading state")
	}
}

func TestMediaPreviewPointerZoomAndPan(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	if !ctrl.handleMediaPreviewInput(ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, Clicks: 2}) {
		t.Fatal("double click was not handled")
	}
	if ctrl.uiState.mediaPreview.mode != MediaZoomOriginal {
		t.Fatal("double click did not switch to original")
	}
	ctrl.uiState.mediaPreview.zoom = 2
	ctrl.uiState.mediaPreview.mode = MediaZoomCustom
	ctrl.handleMediaPreviewInput(ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, X: 10, Y: 10})
	ctrl.handleMediaPreviewInput(ui.InputEvent{Kind: ui.InputPointerMove, Button: 0, X: 30, Y: 18})
	ctrl.handleMediaPreviewInput(ui.InputEvent{Kind: ui.InputPointerUp, Button: 0})
	if ctrl.uiState.mediaPreview.panX != 20 || ctrl.uiState.mediaPreview.panY != 8 {
		t.Fatalf("pan=(%v,%v), want (20,8)", ctrl.uiState.mediaPreview.panX, ctrl.uiState.mediaPreview.panY)
	}
}
