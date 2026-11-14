package bridge

import (
	"image"
	"os"

	"github.com/yeisme/clavis/pkg/core/clipboard"
)

// winClipFormats records which clipboard formats are present (no data is read to decide).
type winClipFormats struct {
	Text  bool // non-empty CF_UNICODETEXT
	HDrop bool // CF_HDROP file list
	PNG   bool // registered "PNG"
	DIB   bool // CF_DIB or CF_DIBV5
}

// classifyClipboardFormats applies docs/design/media-preview.md §5 priority: text wins (so
// Excel/Word copies that also carry a bitmap stay text), then files, then images.
func classifyClipboardFormats(f winClipFormats) clipboard.Kind {
	switch {
	case f.Text:
		return clipboard.KindText
	case f.HDrop:
		return clipboard.KindFile
	case f.PNG || f.DIB:
		return clipboard.KindImage
	}
	return ""
}

// FileMediaProber is implemented by bridges that can thumbnail/probe copied files (Windows).
type FileMediaProber interface {
	FileProbe() clipboard.FileProbe
}

// bgraToRGBA converts top-down 32bpp BGRA DIB bits. Shell bitmaps for opaque content often
// carry alpha=0 everywhere; in that case alpha is forced to 255 so the thumbnail is visible.
func bgraToRGBA(buf []byte, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	n := min(len(buf), w*h*4)
	anyAlpha := false
	for i := 3; i < n; i += 4 {
		if buf[i] != 0 {
			anyAlpha = true
			break
		}
	}
	for i := 0; i+3 < n; i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2] = buf[i+2], buf[i+1], buf[i]
		if anyAlpha {
			img.Pix[i+3] = buf[i+3]
		} else {
			img.Pix[i+3] = 255
		}
	}
	return img
}

// hundredNsToMs converts PKEY_Media_Duration (100 ns units) to milliseconds.
func hundredNsToMs(v uint64) int64 { return int64(v / 10_000) }

// statFileSize reports a regular file's size; false when missing or a directory.
func statFileSize(p string) (int64, bool) {
	fi, err := os.Stat(p)
	if err != nil {
		return 0, false
	}
	if fi.IsDir() {
		return 0, true // folders are valid drops; size unknown
	}
	return fi.Size(), true
}
