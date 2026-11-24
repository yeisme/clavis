//go:build windows

package capture

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32                     = windows.NewLazySystemDLL("user32.dll")
	modGDI32                      = windows.NewLazySystemDLL("gdi32.dll")
	procGetSystemMetrics          = modUser32.NewProc("GetSystemMetrics")
	procGetDC                     = modUser32.NewProc("GetDC")
	procReleaseDC                 = modUser32.NewProc("ReleaseDC")
	procCreateCompatibleDC        = modGDI32.NewProc("CreateCompatibleDC")
	procDeleteDC                  = modGDI32.NewProc("DeleteDC")
	procCreateCompatibleBitmap    = modGDI32.NewProc("CreateCompatibleBitmap")
	procSelectObject              = modGDI32.NewProc("SelectObject")
	procDeleteObject              = modGDI32.NewProc("DeleteObject")
	procBitBlt                    = modGDI32.NewProc("BitBlt")
	procGetDIBits                 = modGDI32.NewProc("GetDIBits")
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
	smCXScreen        = 0
	smCYScreen        = 1
	srccopy           = 0x00CC0020
	captureBlt        = 0x40000000
	biRGB             = 0
	dibRGBColors      = 0
)

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type windowsCapturer struct{}

// NewCapturer constructs the zero-CGo Windows GDI capturer.
func NewCapturer() Capturer {
	return &windowsCapturer{}
}

func NewCapturerWithRunner(CommandRunner) Capturer {
	return &windowsCapturer{}
}

func (c *windowsCapturer) CaptureInteractive(ctx context.Context) (Result, error) {
	return c.CaptureFull(ctx)
}

func (c *windowsCapturer) CaptureFull(_ context.Context) (Result, error) {
	vx, _, _ := procGetSystemMetrics.Call(smXVirtualScreen)
	vy, _, _ := procGetSystemMetrics.Call(smYVirtualScreen)
	vw, _, _ := procGetSystemMetrics.Call(smCXVirtualScreen)
	vh, _, _ := procGetSystemMetrics.Call(smCYVirtualScreen)

	x := int(int32(vx))
	y := int(int32(vy))
	w := int(int32(vw))
	h := int(int32(vh))
	if w <= 0 || h <= 0 {
		x, y = 0, 0
		sw, _, _ := procGetSystemMetrics.Call(smCXScreen)
		sh, _, _ := procGetSystemMetrics.Call(smCYScreen)
		w, h = int(int32(sw)), int(int32(sh))
	}
	if w <= 0 || h <= 0 {
		return Result{}, ErrCaptureUnavailable
	}

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return Result{}, fmt.Errorf("windows capture: GetDC failed")
	}
	defer procReleaseDC.Call(0, screenDC)

	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return Result{}, fmt.Errorf("windows capture: CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(memDC)

	bmp, _, _ := procCreateCompatibleBitmap.Call(screenDC, uintptr(w), uintptr(h))
	if bmp == 0 {
		return Result{}, fmt.Errorf("windows capture: CreateCompatibleBitmap failed")
	}
	defer procDeleteObject.Call(bmp)

	old, _, _ := procSelectObject.Call(memDC, bmp)
	defer procSelectObject.Call(memDC, old)

	ok, _, err := procBitBlt.Call(memDC, 0, 0, uintptr(w), uintptr(h), screenDC, uintptr(x), uintptr(y), srccopy|captureBlt)
	if ok == 0 {
		return Result{}, fmt.Errorf("windows capture: BitBlt failed: %v", err)
	}

	hdr := bitmapInfoHeader{
		biSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		biWidth:       int32(w),
		biHeight:      -int32(h), // top-down scanlines
		biPlanes:      1,
		biBitCount:    32,
		biCompression: biRGB,
	}
	pixels := make([]byte, w*h*4)
	lines, _, err := procGetDIBits.Call(
		memDC,
		bmp,
		0,
		uintptr(h),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&hdr)),
		dibRGBColors,
	)
	if lines == 0 {
		return Result{}, fmt.Errorf("windows capture: GetDIBits failed: %v", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i+3 < len(pixels); i += 4 {
		// Windows GDI returns BGRA; convert to RGBA with opaque alpha.
		img.Pix[i+0] = pixels[i+2]
		img.Pix[i+1] = pixels[i+1]
		img.Pix[i+2] = pixels[i+0]
		img.Pix[i+3] = 255
	}
	var buf bytes.Buffer
	if encErr := png.Encode(&buf, img); encErr != nil {
		return Result{}, encErr
	}
	return Result{
		PNG:        buf.Bytes(),
		Width:      w,
		Height:     h,
		CapturedAt: time.Now(),
	}, nil
}
