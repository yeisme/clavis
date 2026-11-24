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

	"github.com/egoist/mygo/ui"
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

func TestScheduleOriginalBitmapDoesNotRestartForSamePath(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	path := filepath.Join(t.TempDir(), "stable.png")
	ctrl.scheduleOriginalBitmap(path)
	ctrl.mu.Lock()
	firstGen := ctrl.originalGen
	firstTimer := ctrl.originalTimer
	ctrl.mu.Unlock()
	ctrl.scheduleOriginalBitmap(path)
	ctrl.mu.Lock()
	secondGen := ctrl.originalGen
	secondTimer := ctrl.originalTimer
	ctrl.mu.Unlock()
	if secondGen != firstGen || secondTimer != firstTimer {
		t.Fatalf("same-path render restarted dwell: gen %d -> %d timer %p -> %p", firstGen, secondGen, firstTimer, secondTimer)
	}
}

func TestThumbBitmapDecodesOffRenderPath(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	ctrl.mu.Lock()
	ctrl.thumbDecoder = func(string) *ui.Bitmap {
		close(started)
		<-release
		return nil
	}
	ctrl.mu.Unlock()
	path := filepath.Join(t.TempDir(), "thumb.png")
	start := time.Now()
	if got := ctrl.ThumbBitmap(path); got != nil {
		t.Fatal("cache miss returned a bitmap before the background decoder completed")
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("cache miss blocked render for %s", elapsed)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background thumbnail decoder did not start")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := ctrl.cachedThumbBitmap(path); ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("background thumbnail decode did not publish its cache result")
}
