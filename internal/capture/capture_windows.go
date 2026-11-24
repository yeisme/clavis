//go:build windows

package capture

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32                     = windows.NewLazySystemDLL("user32.dll")
	modGDI32                      = windows.NewLazySystemDLL("gdi32.dll")
	modKernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	modShell32                    = windows.NewLazySystemDLL("shell32.dll")
	procGetSystemMetrics          = modUser32.NewProc("GetSystemMetrics")
	procGetDC                     = modUser32.NewProc("GetDC")
	procReleaseDC                 = modUser32.NewProc("ReleaseDC")
	procOpenClipboard             = modUser32.NewProc("OpenClipboard")
	procCloseClipboard            = modUser32.NewProc("CloseClipboard")
	procGetClipboardData          = modUser32.NewProc("GetClipboardData")
	procRegisterClipboardFormatW  = modUser32.NewProc("RegisterClipboardFormatW")
	procGetClipboardSequenceNum   = modUser32.NewProc("GetClipboardSequenceNumber")
	procGetForegroundWindow       = modUser32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId  = modUser32.NewProc("GetWindowThreadProcessId")
	procCreateCompatibleDC        = modGDI32.NewProc("CreateCompatibleDC")
	procDeleteDC                  = modGDI32.NewProc("DeleteDC")
	procCreateCompatibleBitmap    = modGDI32.NewProc("CreateCompatibleBitmap")
	procSelectObject              = modGDI32.NewProc("SelectObject")
	procDeleteObject              = modGDI32.NewProc("DeleteObject")
	procBitBlt                    = modGDI32.NewProc("BitBlt")
	procGetDIBits                 = modGDI32.NewProc("GetDIBits")
	procGlobalLock                = modKernel32.NewProc("GlobalLock")
	procGlobalUnlock              = modKernel32.NewProc("GlobalUnlock")
	procGlobalSize                = modKernel32.NewProc("GlobalSize")
	procQueryFullProcessImageName = modKernel32.NewProc("QueryFullProcessImageNameW")
	procShellExecuteW             = modShell32.NewProc("ShellExecuteW")
)

const (
	smXVirtualScreen               = 76
	smYVirtualScreen               = 77
	smCXVirtualScreen              = 78
	smCYVirtualScreen              = 79
	smCXScreen                     = 0
	smCYScreen                     = 1
	srccopy                        = 0x00CC0020
	captureBlt                     = 0x40000000
	biRGB                          = 0
	biBitfields                    = 3
	dibRGBColors                   = 0
	cfDIB                          = 8
	processQueryLimitedInformation = 0x1000

	winSnipPollInterval       = 100 * time.Millisecond
	winSnipAppearTimeout      = 2500 * time.Millisecond
	winSnipDismissGrace       = 450 * time.Millisecond
	winSnipDefaultMaxWait     = 60 * time.Second
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

var (
	winCapturePNGFmtOnce sync.Once
	winCapturePNGFmt     uint32
)

func winNativePtr(addr uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&addr))
}

func getWinCapturePNGFormat() uint32 {
	winCapturePNGFmtOnce.Do(func() {
		p, err := windows.UTF16PtrFromString("PNG")
		if err != nil {
			return
		}
		r, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p)))
		winCapturePNGFmt = uint32(r)
	})
	return winCapturePNGFmt
}

type windowsCapturer struct {
	runner CommandRunner
}

// NewCapturer constructs the zero-CGo Windows interactive/GDI capturer.
func NewCapturer() Capturer {
	return &windowsCapturer{}
}

// NewCapturerWithRunner constructs a Windows capturer backed by r when non-nil
// (primarily for deterministic unit testing without live desktop windows/clipboard).
func NewCapturerWithRunner(r CommandRunner) Capturer {
	return &windowsCapturer{runner: r}
}

func (c *windowsCapturer) CaptureInteractive(ctx context.Context) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if c.runner != nil {
		return c.captureInteractiveWithRunner(ctx)
	}
	return c.captureInteractiveNative(ctx)
}

func (c *windowsCapturer) captureInteractiveWithRunner(ctx context.Context) (Result, error) {
	tmpPath, err := createTempCaptureFile(".png")
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(tmpPath)

	runErr := c.runner.Run(ctx, "ms-screenclip:", tmpPath)
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if errors.Is(runErr, ErrCaptureUnavailable) {
		return c.CaptureFull(ctx)
	}
	if _, statErr := os.Stat(tmpPath); statErr != nil {
		// No image file produced (e.g. user pressed Esc to dismiss snip overlay).
		return Result{Cancelled: true}, nil
	}
	if runErr != nil {
		return Result{}, runErr
	}
	return readCapturedPNG(tmpPath)
}

// captureInteractiveNative launches Windows 10/11 interactive region snipping via the
// ms-screenclip: URI handler and watches the clipboard for the resulting PNG/DIB image.
// If ms-screenclip: is unavailable (e.g. headless session or Windows Server) it falls
// back cleanly to CaptureFull.
func (c *windowsCapturer) captureInteractiveNative(ctx context.Context) (Result, error) {
	beforeSeq := winCaptureClipboardSeq()
	if !launchWinScreenClip() {
		return c.CaptureFull(ctx)
	}

	waitCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		waitCtx, cancel = context.WithTimeout(ctx, winSnipDefaultMaxWait)
		defer cancel()
	}

	ticker := time.NewTicker(winSnipPollInterval)
	defer ticker.Stop()

	startedAt := time.Now()
	overlaySeen := false
	var overlayLostAt time.Time

	for {
		select {
		case <-waitCtx.Done():
			return Result{}, waitCtx.Err()
		case now := <-ticker.C:
			if seq := winCaptureClipboardSeq(); seq != beforeSeq {
				if pngBytes, w, h, ok := readWinClipboardImagePNG(); ok {
					return Result{
						PNG:        pngBytes,
						Width:      w,
						Height:     h,
						CapturedAt: now,
					}, nil
				}
				// Clipboard changed to something else; keep watching only until grace/dismiss.
				beforeSeq = seq
			}

			if isWinSnipOverlayForeground() {
				overlaySeen = true
				overlayLostAt = time.Time{}
				continue
			}

			if overlaySeen {
				if overlayLostAt.IsZero() {
					overlayLostAt = now
					continue
				}
				if now.Sub(overlayLostAt) >= winSnipDismissGrace {
					return Result{Cancelled: true}, nil
				}
				continue
			}

			if now.Sub(startedAt) >= winSnipAppearTimeout {
				// Overlay never appeared (headless / disabled Snipping Tool): fall back cleanly.
				return c.CaptureFull(ctx)
			}
		}
	}
}

func launchWinScreenClip() bool {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return false
	}
	uri, err := windows.UTF16PtrFromString("ms-screenclip:")
	if err != nil {
		return false
	}
	r, _, _ := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(uri)), 0, 0, 1)
	return r > 32
}

func winCaptureClipboardSeq() uint32 {
	r, _, _ := procGetClipboardSequenceNum.Call()
	return uint32(r)
}

func isWinSnipOverlayForeground() bool {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return false
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return false
	}
	hProc, err := windows.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(hProc)

	var buf [windows.MAX_PATH]uint16
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageName.Call(uintptr(hProc), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 || size == 0 {
		return false
	}
	base := strings.ToLower(filepath.Base(windows.UTF16ToString(buf[:size])))
	return base == "screenclippinghost.exe" || base == "snippingtool.exe" || base == "screensketch.exe"
}

func readWinClipboardImagePNG() ([]byte, int, int, bool) {
	var opened bool
	for attempt := 0; attempt < 4; attempt++ {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			opened = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !opened {
		return nil, 0, 0, false
	}
	defer procCloseClipboard.Call()

	if fmtPNG := getWinCapturePNGFormat(); fmtPNG != 0 {
		if raw, ok := readWinClipboardHGlobalBytes(uintptr(fmtPNG)); ok && len(raw) > 0 {
			if cfg, _, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil && cfg.Width > 0 && cfg.Height > 0 {
				return raw, cfg.Width, cfg.Height, true
			}
		}
	}
	if dib, ok := readWinClipboardHGlobalBytes(cfDIB); ok && len(dib) >= 40 {
		if pngBytes, w, h, err := decodeWinDIBToPNG(dib); err == nil {
			return pngBytes, w, h, true
		}
	}
	return nil, 0, 0, false
}

func readWinClipboardHGlobalBytes(fmtID uintptr) ([]byte, bool) {
	h, _, _ := procGetClipboardData.Call(fmtID)
	if h == 0 {
		return nil, false
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return nil, false
	}
	defer procGlobalUnlock.Call(h)
	sz, _, _ := procGlobalSize.Call(h)
	if sz == 0 {
		return nil, false
	}
	out := make([]byte, sz)
	copy(out, unsafe.Slice((*byte)(winNativePtr(ptr)), sz))
	return out, true
}

func decodeWinDIBToPNG(dib []byte) ([]byte, int, int, error) {
	if len(dib) < 40 {
		return nil, 0, 0, errors.New("windows capture: truncated DIB header")
	}
	hdrSize := int(binary.LittleEndian.Uint32(dib[0:4]))
	w := int(int32(binary.LittleEndian.Uint32(dib[4:8])))
	rawH := int(int32(binary.LittleEndian.Uint32(dib[8:12])))
	bitCount := int(binary.LittleEndian.Uint16(dib[14:16]))
	compression := binary.LittleEndian.Uint32(dib[16:20])
	if hdrSize < 40 || w <= 0 || rawH == 0 {
		return nil, 0, 0, errors.New("windows capture: invalid DIB dimensions")
	}
	if bitCount != 24 && bitCount != 32 {
		return nil, 0, 0, fmt.Errorf("windows capture: unsupported DIB bit depth %d", bitCount)
	}
	if compression != biRGB && compression != biBitfields {
		return nil, 0, 0, fmt.Errorf("windows capture: unsupported DIB compression %d", compression)
	}
	topDown := rawH < 0
	h := rawH
	if h < 0 {
		h = -h
	}
	pixelOffset := hdrSize
	if hdrSize == 40 && compression == biBitfields {
		pixelOffset += 12
	}
	bytesPerPixel := bitCount / 8
	rowStride := ((w*bitCount + 31) / 32) * 4
	if pixelOffset+rowStride*h > len(dib) {
		return nil, 0, 0, errors.New("windows capture: truncated DIB pixels")
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		srcY := y
		if !topDown {
			srcY = h - 1 - y
		}
		srcRow := dib[pixelOffset+srcY*rowStride : pixelOffset+(srcY+1)*rowStride]
		dstOff := y * img.Stride
		for x := 0; x < w; x++ {
			so := x * bytesPerPixel
			do := dstOff + x*4
			img.Pix[do+0] = srcRow[so+2]
			img.Pix[do+1] = srcRow[so+1]
			img.Pix[do+2] = srcRow[so+0]
			img.Pix[do+3] = 255
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), w, h, nil
}

func (c *windowsCapturer) CaptureFull(ctx context.Context) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if c.runner != nil {
		tmpPath, err := createTempCaptureFile(".png")
		if err != nil {
			return Result{}, err
		}
		defer os.Remove(tmpPath)
		if err := c.runner.Run(ctx, "gdi-capture", tmpPath); err != nil {
			return Result{}, err
		}
		return readCapturedPNG(tmpPath)
	}

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

	if err := ctx.Err(); err != nil {
		return Result{}, err
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
