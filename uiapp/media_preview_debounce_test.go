package uiapp

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writePreviewPNG(t *testing.T, dir, name string) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 24, 24))
	for x := range 24 {
		for y := range 24 {
			img.Set(x, y, color.NRGBA{R: uint8(x * 8), G: uint8(y * 8), B: 160, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode preview png: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write preview png: %v", err)
	}
	return path
}

// TestScheduleOriginalBitmapDebouncesSweep pins the scroll/hover smoothness
// contract: sweeping the pointer across rows must decode only the image the
// selection settles on, not one per row.
func TestScheduleOriginalBitmapDebouncesSweep(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	dir := t.TempDir()
	first := writePreviewPNG(t, dir, "first.png")
	second := writePreviewPNG(t, dir, "second.png")

	for _, path := range []string{first, second} {
		ctrl.scheduleOriginalBitmap(path)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, cachedSecond := ctrl.cachedOriginalBitmap(second)
		if cachedSecond {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if bmp, ok := ctrl.cachedOriginalBitmap(second); !ok || bmp == nil {
		t.Fatalf("settled image was not decoded: cached=%v bmp=%v", ok, bmp)
	}
	if _, ok := ctrl.cachedOriginalBitmap(first); ok {
		t.Fatal("a swept-past image was decoded; the dwell debounce did not cancel it")
	}
}

// TestScheduleOriginalBitmapIgnoresEmptyPath keeps the render path cheap when a
// row has no decodable original behind it.
func TestScheduleOriginalBitmapIgnoresEmptyPath(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	ctrl.scheduleOriginalBitmap("")
	ctrl.scheduleOriginalBitmap("   ")

	ctrl.mu.Lock()
	pending := ctrl.originalTimer != nil
	ctrl.mu.Unlock()
	if pending {
		t.Fatal("blank path scheduled a decode")
	}
}
