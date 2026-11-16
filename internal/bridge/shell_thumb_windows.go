//go:build windows

package bridge

import (
	"errors"
	"image"
	"mime"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/yeisme/clavis/pkg/core/clipboard"
)

// Shell thumbnail + media duration for CF_HDROP entries (docs/design/media-preview.md §8).
// Pure syscall COM (no cgo): IShellItemImageFactory::GetImage returns the same thumbnail
// Explorer shows — for videos the poster/first frame from the installed codec handlers.

var (
	ole32 = windows.NewLazySystemDLL("ole32.dll")
	gdi32 = windows.NewLazySystemDLL("gdi32.dll")

	procCoInitializeEx           = ole32.NewProc("CoInitializeEx")
	procCoUninitialize           = ole32.NewProc("CoUninitialize")
	procPropVariantClear         = ole32.NewProc("PropVariantClear")
	procSHCreateItemFromParsing  = shell32.NewProc("SHCreateItemFromParsingName")
	procSHGetPropertyStoreFromPN = shell32.NewProc("SHGetPropertyStoreFromParsingName")
	procGetObjectW               = gdi32.NewProc("GetObjectW")
	procGetDIBits                = gdi32.NewProc("GetDIBits")
	procDeleteObject             = gdi32.NewProc("DeleteObject")
	procGetDCShell               = user32.NewProc("GetDC")
	procReleaseDCShell           = user32.NewProc("ReleaseDC")
)

var (
	iidShellItemImageFactory = windows.GUID{Data1: 0xbcc18b79, Data2: 0xba16, Data3: 0x442f, Data4: [8]byte{0x80, 0xc4, 0x8a, 0x59, 0xc3, 0x0c, 0x46, 0x3b}}
	iidPropertyStore         = windows.GUID{Data1: 0x886d8eeb, Data2: 0x8cf2, Data3: 0x4446, Data4: [8]byte{0x8d, 0x02, 0xcd, 0xba, 0x1d, 0xbd, 0xcf, 0x99}}
	pkeyMediaDuration        = propertyKey{FmtID: windows.GUID{Data1: 0x64440490, Data2: 0x4c8b, Data3: 0x11d1, Data4: [8]byte{0x8b, 0x70, 0x08, 0x00, 0x36, 0xb1, 0x1a, 0x03}}, PID: 3}
)

const (
	coinitApartmentThreaded = 0x2
	siigbfThumbnailOnly     = 0x8
	shellProbeTimeout       = 3 * time.Second
	vtUI8                   = 21
)

type propertyKey struct {
	FmtID windows.GUID
	PID   uint32
}

// propVariant is laid out for both 32- and 64-bit: vt at 0, 8-byte value at offset 8.
type propVariant struct {
	VT       uint16
	_        [3]uint16
	Val      uint64
	_padding uint64
}

type winBitmap struct {
	Type, Width, Height, WidthBytes int32
	Planes, BitsPixel               uint16
	Bits                            uintptr
}

type bitmapInfoHeader struct {
	Size                         uint32
	Width, Height                int32
	Planes, BitCount             uint16
	Compression, SizeImage       uint32
	XPelsPerMeter, YPelsPerMeter int32
	ClrUsed, ClrImportant        uint32
	_colors                      [4]uint32
}

// comObject is the layout of a COM interface pointer: a pointer to its vtable.
type comObject struct {
	vtbl *[32]uintptr
}

// comCall invokes vtable slot `slot` on obj. COM interface pointers are kept as unsafe.Pointer
// end to end (never round-tripped through uintptr), which keeps go vet's unsafeptr check clean.
func comCall(obj unsafe.Pointer, slot int, args ...uintptr) uintptr {
	fn := (*comObject)(obj).vtbl[slot]
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return r
}

func comRelease(obj unsafe.Pointer) {
	if obj != nil {
		comCall(obj, 2)
	}
}

// onCOMThread runs fn on a locked STA thread with a timeout (shell handlers may hang).
func onCOMThread[T any](fn func() (T, error)) (T, error) {
	type res struct {
		v   T
		err error
	}
	ch := make(chan res, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		var zero T
		if err := procCoInitializeEx.Find(); err != nil {
			ch <- res{zero, err}
			return
		}
		hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
		if int32(hr) < 0 {
			ch <- res{zero, syscall.Errno(hr)}
			return
		}
		defer procCoUninitialize.Call()
		v, err := fn()
		ch <- res{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(shellProbeTimeout):
		var zero T
		return zero, errors.New("shell probe timed out")
	}
}

// winShellThumbnail returns the Explorer thumbnail for path, at most edge px.
func winShellThumbnail(path string, edge int) (image.Image, error) {
	if err := procSHCreateItemFromParsing.Find(); err != nil {
		return nil, err
	}
	return onCOMThread(func() (image.Image, error) {
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return nil, err
		}
		var factory unsafe.Pointer
		hr, _, _ := procSHCreateItemFromParsing.Call(uintptr(unsafe.Pointer(p)), 0,
			uintptr(unsafe.Pointer(&iidShellItemImageFactory)), uintptr(unsafe.Pointer(&factory)))
		if int32(hr) < 0 || factory == nil {
			return nil, syscall.Errno(hr)
		}
		defer comRelease(factory)
		// SIZE is passed by value: one 8-byte register on amd64/arm64, two stack dwords on 386.
		var hbm uintptr
		var r uintptr
		if unsafe.Sizeof(uintptr(0)) == 8 {
			size := uintptr(uint64(uint32(edge)) | uint64(uint32(edge))<<32)
			r = comCall(factory, 3, size, siigbfThumbnailOnly, uintptr(unsafe.Pointer(&hbm)))
		} else {
			r = comCall(factory, 3, uintptr(edge), uintptr(edge), siigbfThumbnailOnly, uintptr(unsafe.Pointer(&hbm)))
		}
		if int32(r) < 0 || hbm == 0 {
			return nil, syscall.Errno(r)
		}
		defer procDeleteObject.Call(hbm)
		return hbitmapToImage(hbm)
	})
}

func hbitmapToImage(hbm uintptr) (image.Image, error) {
	var bm winBitmap
	if r, _, _ := procGetObjectW.Call(hbm, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); r == 0 {
		return nil, errors.New("GetObject failed")
	}
	w, h := int(bm.Width), int(bm.Height)
	if h < 0 {
		h = -h
	}
	if w <= 0 || h <= 0 || w > clipboard.MaxImageDim || h > clipboard.MaxImageDim {
		return nil, errors.New("bad thumbnail bitmap size")
	}
	bi := bitmapInfoHeader{Size: 40, Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	buf := make([]byte, w*h*4)
	hdc, _, _ := procGetDCShell.Call(0)
	if hdc == 0 {
		return nil, errors.New("GetDC failed")
	}
	lines, _, _ := procGetDIBits.Call(hdc, hbm, 0, uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), 0)
	procReleaseDCShell.Call(0, hdc)
	if lines == 0 {
		return nil, errors.New("GetDIBits failed")
	}
	return bgraToRGBA(buf, w, h), nil
}

// winMediaDurationMs reads System.Media.Duration (100ns units); 0 when unknown.
func winMediaDurationMs(path string) int64 {
	if procSHGetPropertyStoreFromPN.Find() != nil {
		return 0
	}
	ms, _ := onCOMThread(func() (int64, error) {
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return 0, err
		}
		var store unsafe.Pointer
		hr, _, _ := procSHGetPropertyStoreFromPN.Call(uintptr(unsafe.Pointer(p)), 0, 0,
			uintptr(unsafe.Pointer(&iidPropertyStore)), uintptr(unsafe.Pointer(&store)))
		if int32(hr) < 0 || store == nil {
			return 0, syscall.Errno(hr)
		}
		defer comRelease(store)
		var pv propVariant
		if r := comCall(store, 5, uintptr(unsafe.Pointer(&pkeyMediaDuration)), uintptr(unsafe.Pointer(&pv))); int32(r) < 0 {
			return 0, syscall.Errno(r)
		}
		defer procPropVariantClear.Call(uintptr(unsafe.Pointer(&pv)))
		if pv.VT != vtUI8 {
			return 0, nil
		}
		return hundredNsToMs(pv.Val), nil
	})
	return ms
}

// winMediaDimensions is the optional dimensions hook for the Windows probe.
// The property-store accessors vary by shell handler and Windows SDK version; keep
// this hook conservative until a verified property key set is available. The zero
// result is intentionally safe and lets SaveFileList preserve all existing behavior.
func winMediaDimensions(string) (int, int) { return 0, 0 }

// winMediaMIME returns a stable MIME based on the file extension. Shell's
// System.MIMEType is not consistently populated across Windows versions, so
// extension mapping is deliberately used instead of another property lookup.
func winMediaMIME(path string) string {
	name := strings.ToLower(baseNameForMIME(path))
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		// TypeByExtension is backed by Go's fixed MIME table and strips optional
		// charset parameters below, keeping persisted values stable across hosts.
		if value := mime.TypeByExtension(name[i:]); value != "" {
			if semi := strings.IndexByte(value, ';'); semi >= 0 {
				value = value[:semi]
			}
			return value
		}
	}
	return ""
}

func baseNameForMIME(path string) string {
	path = strings.TrimRight(path, `/\\`)
	if i := strings.LastIndexAny(path, `/\\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// FileProbe implements FileMediaProber for the clipboard poller.
func (b *windowsBridge) FileProbe() clipboard.FileProbe {
	return clipboard.FileProbe{
		Size:       statFileSize,
		Thumbnail:  func(p string) (image.Image, error) { return winShellThumbnail(p, clipboard.ThumbMaxEdge) },
		DurationMs: winMediaDurationMs,
		Dimensions: winMediaDimensions,
		MIME:       winMediaMIME,
	}
}
