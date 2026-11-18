package uiapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/bridge"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// Clipboard media (image/file/video) plumbing for the UI layer (docs/design/media-preview.md §7).

// engineClipboardMedia maps a stored item's media metadata onto the engine type.
func engineClipboardMedia(m clipboard.Media) engine.ClipboardMedia {
	if !m.IsMedia() {
		return engine.ClipboardMedia{}
	}
	out := engine.ClipboardMedia{
		Kind: string(m.Kind), Path: m.MediaPath, Mime: m.MediaMime, ThumbPath: m.ThumbPath,
		Width: m.Width, Height: m.Height, ByteSize: m.ByteSize, DurationMs: m.DurationMs,
	}
	if m.Kind == clipboard.KindFile || m.Kind == clipboard.KindVideo {
		out.FilePaths = clipboard.SplitFilePaths(m.MediaPath)
	}
	return out
}

// pasteFormat says how a media entry is written back to the system clipboard.
type pasteFormat int

const (
	pasteNone  pasteFormat = iota
	pastePNG               // image bytes as image/png (MyGo also offers CF_DIB on Windows)
	pasteFiles             // file list (CF_HDROP on Windows)
)

// pasteFormatFor picks the write-back format. Images whose blob was GC'd cannot be pasted.
func pasteFormatFor(m engine.ClipboardMedia) pasteFormat {
	switch m.Kind {
	case "image":
		if strings.TrimSpace(m.Path) == "" {
			return pasteNone
		}
		return pastePNG
	case "file", "video":
		if len(m.FilePaths) == 0 {
			return pasteNone
		}
		return pasteFiles
	}
	return pasteNone
}

var errMediaMissing = errors.New("clipboard media is no longer available")

// normalizeImagePNG converts stored image formats to PNG, the portable image
// representation understood by MyGo across platforms. The header guard runs
// before decoding so malformed or oversized blobs never reach image.Decode.
func normalizeImagePNG(data []byte) ([]byte, error) {
	cfg, format, err := clipboard.CheckImage(data)
	if err != nil {
		return nil, err
	}
	if format == "png" {
		return data, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	if len(out.Bytes()) > clipboard.MaxImageBytes || cfg.Width > clipboard.MaxImageDim || cfg.Height > clipboard.MaxImageDim {
		return nil, clipboard.ErrImageTooLarge
	}
	return out.Bytes(), nil
}

// writeClipboardMedia puts a media entry back on the system clipboard via MyGo's transfer API
// (no new Win32 code: MyGo's Windows backend renders PNG + CF_DIB / CF_HDROP).
func writeClipboardMedia(m engine.ClipboardMedia) error {
	if !mygo.App.IsReady() {
		return errors.New("app not ready")
	}
	switch pasteFormatFor(m) {
	case pastePNG:
		data, err := os.ReadFile(m.Path)
		if err != nil {
			return fmt.Errorf("%w: %v", errMediaMissing, err)
		}
		data, err = normalizeImagePNG(data)
		if err != nil {
			return fmt.Errorf("%w: %v", errMediaMissing, err)
		}
		if err := mygo.Clipboard.Write(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, data)))); err != nil {
			return err
		}
	case pasteFiles:
		var present []string
		for _, p := range m.FilePaths {
			if _, err := os.Stat(p); err == nil {
				present = append(present, p)
			}
		}
		if len(present) == 0 {
			return errMediaMissing
		}
		if err := mygo.Clipboard.WriteFiles(present...); err != nil {
			return err
		}
	default:
		return errMediaMissing
	}
	return mygo.Clipboard.Flush()
}

// pasteClipboardMediaAsync writes the media to the clipboard and, where the bridge supports it,
// sends the paste shortcut; otherwise it leaves it on the clipboard and tells the user.
//
// The optional callback runs only after both the clipboard write and platform paste succeed.
func (c *AppController) pasteClipboardMediaAsync(m engine.ClipboardMedia, onWritten ...func()) {
	c.pasteClipboardMediaAsyncWithWriter(m, writeClipboardMedia, firstCallback(onWritten))
}

// pasteClipboardMediaAsyncWithWriter is the testable seam for the MyGo clipboard write. Production
// callers use pasteClipboardMediaAsync, while tests can verify callback ordering without starting a
// MyGo app or mutating a process-global clipboard.
func (c *AppController) pasteClipboardMediaAsyncWithWriter(m engine.ClipboardMedia, writeMedia func(engine.ClipboardMedia) error, onWritten func()) {
	c.mu.Lock()
	br := c.Bridge
	beforeInject := c.BeforeInject
	c.mu.Unlock()
	if err := writeMedia(m); err != nil {
		c.ShowToastOrHUD(err.Error())
		return
	}
	paster, ok := br.(bridge.MediaPaster)
	if !ok || paster == nil {
		c.ShowToastOrHUD(i18n.T("clipboard.media.paste_unsupported"))
		return
	}
	go func() {
		ctx := context.Background()
		if beforeInject != nil {
			beforeInject(ctx)
		}
		if err := paster.PasteClipboard(ctx); err != nil {
			c.ShowToastOrHUD(err.Error())
			return
		}
		if onWritten != nil {
			onWritten()
		}
	}()
}

func firstCallback(callbacks []func()) func() {
	if len(callbacks) == 0 {
		return nil
	}
	return callbacks[0]
}

// ThumbBitmap decodes (and caches) a clipboard thumbnail; nil when missing or undecodable.
func (c *AppController) ThumbBitmap(path string) *ui.Bitmap {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	c.mu.Lock()
	if c.thumbCache == nil {
		c.thumbCache = map[string]*ui.Bitmap{}
	}
	if bmp, ok := c.thumbCache[path]; ok {
		c.mu.Unlock()
		return bmp
	}
	c.mu.Unlock()

	var bmp *ui.Bitmap
	if data, err := os.ReadFile(path); err == nil && len(data) <= clipboard.MaxImageBytes {
		if _, _, err := clipboard.CheckImage(data); err == nil {
			if b, err := ui.DecodeBitmap(data); err == nil {
				bmp = b
			}
		}
	}
	c.mu.Lock()
	if len(c.thumbCache) >= 128 {
		clear(c.thumbCache)
	}
	c.thumbCache[path] = bmp // cache misses too: a GC'd path stays nil until restart
	c.mu.Unlock()
	return bmp
}

// OriginalBitmap lazily decodes a selected image blob for the split preview.
// Rows continue using ThumbBitmap, keeping list rendering cheap.
func (c *AppController) OriginalBitmap(path string) *ui.Bitmap {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if bmp, ok := c.cachedOriginalBitmap(path); ok {
		return bmp
	}
	// Preserve the old helper's synchronous contract for non-render callers.
	// The media preview itself uses requestOriginalBitmap, which never blocks a
	// frame on disk I/O or bitmap decoding.
	return c.decodeOriginalBitmap(path)
}

func (c *AppController) cachedOriginalBitmap(path string) (*ui.Bitmap, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.originalCache == nil {
		c.originalCache = map[string]*ui.Bitmap{}
	}
	bmp, ok := c.originalCache[path]
	return bmp, ok
}

func (c *AppController) decodeOriginalBitmap(path string) *ui.Bitmap {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 8<<20 {
		return nil
	}
	var bmp *ui.Bitmap
	if data, err := os.ReadFile(path); err == nil {
		if _, _, err := clipboard.CheckImage(data); err == nil {
			if b, err := ui.DecodeBitmap(data); err == nil {
				bmp = b
			}
		}
	}
	c.mu.Lock()
	if c.originalCache == nil {
		c.originalCache = map[string]*ui.Bitmap{}
	}
	if len(c.originalCache) >= 32 {
		clear(c.originalCache)
	}
	c.originalCache[path] = bmp
	c.mu.Unlock()
	return bmp
}

func (c *AppController) loadOriginalBitmapAsync(path string) {
	c.mu.Lock()
	if c.originalLoading == nil {
		c.originalLoading = map[string]bool{}
	}
	if c.originalLoading[path] {
		c.mu.Unlock()
		return
	}
	c.originalLoading[path] = true
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	go func() {
		c.decodeOriginalBitmap(path)
		c.mu.Lock()
		delete(c.originalLoading, path)
		onRepaint = c.OnRepaint
		c.mu.Unlock()
		if onRepaint != nil {
			onRepaint()
		}
	}()
}

// clipboardMediaMetaRows renders "1920×1080 · 2.3 MB" / "Duration 0:42" lines for the preview.
func clipboardMediaMetaRows(m engine.ClipboardMedia) []string {
	var rows []string
	var parts []string
	if m.Width > 0 && m.Height > 0 {
		parts = append(parts, fmt.Sprintf("%d×%d", m.Width, m.Height))
	}
	if m.ByteSize > 0 {
		parts = append(parts, humanBytes(m.ByteSize))
	}
	if len(parts) > 0 {
		rows = append(rows, strings.Join(parts, " · "))
	}
	if m.DurationMs > 0 {
		s := m.DurationMs / 1000
		rows = append(rows, i18n.Tf("clipboard.media.duration", fmt.Sprintf("%d:%02d", s/60, s%60)))
	}
	return rows
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
