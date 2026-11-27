package uiapp

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
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
	if m.Kind != clipboard.KindImage && strings.TrimSpace(m.MediaPath) != "" {
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
	case "file", "video", "audio", "document", "archive":
		if len(m.FilePaths) == 0 {
			return pasteNone
		}
		return pasteFiles
	}
	return pasteNone
}

// MediaCategory classifies a clipboard or search media entry into one of the
// six supported media families (plus "all" for unfiltered browsing).
type MediaCategory string

const (
	MediaCategoryAll      MediaCategory = "all"
	MediaCategoryImage    MediaCategory = "image"
	MediaCategoryVideo    MediaCategory = "video"
	MediaCategoryAudio    MediaCategory = "audio"
	MediaCategoryDocument MediaCategory = "document"
	MediaCategoryArchive  MediaCategory = "archive"
	MediaCategoryFile     MediaCategory = "file"
)

var (
	imageMediaExts = map[string]bool{
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
		".bmp": true, ".tif": true, ".tiff": true, ".heic": true, ".heif": true,
		".svg": true, ".ico": true, ".avif": true,
	}
	audioMediaExts = map[string]bool{
		".mp3": true, ".wav": true, ".flac": true, ".aac": true, ".m4a": true,
		".ogg": true, ".opus": true, ".wma": true, ".aiff": true, ".aif": true,
		".mid": true, ".midi": true,
	}
	documentMediaExts = map[string]bool{
		".pdf": true, ".md": true, ".markdown": true, ".txt": true, ".rtf": true,
		".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
		".pages": true, ".numbers": true, ".key": true, ".odt": true, ".ods": true, ".odp": true,
		".csv": true, ".tsv": true, ".json": true, ".yaml": true, ".yml": true,
		".toml": true, ".xml": true, ".html": true, ".htm": true, ".log": true,
	}
	archiveMediaExts = map[string]bool{
		".zip": true, ".tar": true, ".gz": true, ".tgz": true, ".bz2": true,
		".xz": true, ".zst": true, ".7z": true, ".rar": true, ".dmg": true, ".iso": true,
	}
)

// mediaPrimaryPath returns the primary filesystem path for a media descriptor.
func mediaPrimaryPath(m engine.ClipboardMedia) string {
	if len(m.FilePaths) > 0 {
		if p := strings.TrimSpace(m.FilePaths[0]); p != "" {
			return p
		}
	}
	if p := strings.TrimSpace(m.Path); p != "" {
		if parts := clipboard.SplitFilePaths(p); len(parts) > 0 {
			return parts[0]
		}
		return p
	}
	return ""
}

func mediaPathExt(path string) string {
	path = strings.TrimRight(strings.TrimSpace(path), `/\`)
	if idx := strings.LastIndexAny(path, `/\`); idx >= 0 {
		path = path[idx+1:]
	}
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".tar.gz") {
		return ".tgz"
	}
	if strings.HasSuffix(lower, ".tar.bz2") {
		return ".bz2"
	}
	if strings.HasSuffix(lower, ".tar.xz") {
		return ".xz"
	}
	if dot := strings.LastIndexByte(lower, '.'); dot >= 0 {
		return lower[dot:]
	}
	return ""
}

// ClassifyClipboardMedia maps a ClipboardMedia entry into one of the six media
// categories (image, video, audio, document, archive, file) using real kind,
// MIME type, and file extension signals.
func ClassifyClipboardMedia(m engine.ClipboardMedia) MediaCategory {
	kind := strings.ToLower(strings.TrimSpace(m.Kind))
	mime := strings.ToLower(strings.TrimSpace(m.Mime))
	ext := mediaPathExt(mediaPrimaryPath(m))

	switch kind {
	case "image":
		return MediaCategoryImage
	case "video":
		return MediaCategoryVideo
	case "audio":
		return MediaCategoryAudio
	case "document":
		return MediaCategoryDocument
	case "archive":
		return MediaCategoryArchive
	}

	switch {
	case strings.HasPrefix(mime, "image/") || imageMediaExts[ext]:
		return MediaCategoryImage
	case strings.HasPrefix(mime, "video/") || clipboard.IsVideoPath(mediaPrimaryPath(m)):
		return MediaCategoryVideo
	case strings.HasPrefix(mime, "audio/") || audioMediaExts[ext]:
		return MediaCategoryAudio
	case mime == "application/zip" ||
		mime == "application/x-tar" ||
		mime == "application/gzip" ||
		mime == "application/x-gzip" ||
		mime == "application/x-7z-compressed" ||
		mime == "application/vnd.rar" ||
		archiveMediaExts[ext]:
		return MediaCategoryArchive
	case mime == "application/pdf" ||
		strings.HasPrefix(mime, "text/") ||
		mime == "application/json" ||
		mime == "application/xml" ||
		strings.Contains(mime, "wordprocessingml") ||
		strings.Contains(mime, "spreadsheetml") ||
		strings.Contains(mime, "presentationml") ||
		documentMediaExts[ext]:
		return MediaCategoryDocument
	default:
		return MediaCategoryFile
	}
}

// MediaCategoryLabel returns the localized display name of a media category.
func MediaCategoryLabel(cat MediaCategory) string {
	zh := i18n.IsChinese()
	switch cat {
	case MediaCategoryAll:
		return pickLang(zh, "全部", "All")
	case MediaCategoryImage:
		return pickLang(zh, "图片", "Image")
	case MediaCategoryVideo:
		return pickLang(zh, "视频", "Video")
	case MediaCategoryAudio:
		return pickLang(zh, "音频", "Audio")
	case MediaCategoryDocument:
		return pickLang(zh, "文档", "Document")
	case MediaCategoryArchive:
		return pickLang(zh, "压缩包", "Archive")
	default:
		return pickLang(zh, "文件", "File")
	}
}

// MediaCategoryIcon returns a mapped semantic icon name from icons.go for cat.
func MediaCategoryIcon(cat MediaCategory) string {
	switch cat {
	case MediaCategoryAll:
		return "square.3.layers.3d"
	case MediaCategoryImage:
		return "photo"
	case MediaCategoryVideo:
		return "film"
	case MediaCategoryAudio:
		return "clock"
	case MediaCategoryDocument:
		return "doc.text"
	case MediaCategoryArchive:
		return "doc.on.doc"
	default:
		return "folder"
	}
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
	c.pasteClipboardMediaAsyncWithOptions(m, pasteOptions{}, firstCallback(onWritten))
}

// pasteClipboardMediaAsyncWithOptions writes media to the clipboard and optionally keeps
// the Capsule open after pasting (⌘↵ continuous multi-paste).
func (c *AppController) pasteClipboardMediaAsyncWithOptions(m engine.ClipboardMedia, opts pasteOptions, onWritten func()) {
	if err := writeClipboardMedia(m); err != nil {
		c.ShowToastOrHUD(err.Error())
		return
	}
	c.startClipboardPasteWithOptions(opts, onWritten)
}

// pasteClipboardMediaAsyncWithWriter is the testable seam for the MyGo clipboard write. Production
// callers use pasteClipboardMediaAsync, while tests can verify callback ordering without starting a
// MyGo app or mutating a process-global clipboard.
func (c *AppController) pasteClipboardMediaAsyncWithWriter(m engine.ClipboardMedia, writeMedia func(engine.ClipboardMedia) error, onWritten func()) {
	if err := writeMedia(m); err != nil {
		c.ShowToastOrHUD(err.Error())
		return
	}
	c.startClipboardPaste(onWritten)
}

func firstCallback(callbacks []func()) func() {
	if len(callbacks) == 0 {
		return nil
	}
	return callbacks[0]
}

// ThumbBitmap returns a cached clipboard thumbnail and schedules a background
// decode on a cache miss. Rendering must never perform file I/O or bitmap
// decoding synchronously because list and preview frames call this repeatedly.
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
	if c.thumbLoading == nil {
		c.thumbLoading = map[string]bool{}
	}
	if c.thumbLoading[path] {
		c.mu.Unlock()
		return nil
	}
	c.thumbLoading[path] = true
	c.mu.Unlock()

	go c.loadThumbBitmap(path)
	return nil
}

func (c *AppController) loadThumbBitmap(path string) {
	c.mu.Lock()
	decoder := c.thumbDecoder
	c.mu.Unlock()
	var bmp *ui.Bitmap
	if decoder != nil {
		bmp = decoder(path)
	} else if data, err := os.ReadFile(path); err == nil && len(data) <= clipboard.MaxImageBytes {
		if _, _, err := clipboard.CheckImage(data); err == nil {
			if b, err := ui.DecodeBitmap(data); err == nil {
				bmp = b
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.thumbCache) >= 128 {
		clear(c.thumbCache)
	}
	c.thumbCache[path] = bmp // cache misses too: a bad path stays nil until restart
	delete(c.thumbLoading, path)
	if c.thumbRepaintPending {
		return
	}
	c.thumbRepaintPending = true
	go c.repaintAfterThumbBatch()
}

func (c *AppController) cachedThumbBitmap(path string) (*ui.Bitmap, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	bmp, ok := c.thumbCache[strings.TrimSpace(path)]
	return bmp, ok
}

func (c *AppController) repaintAfterThumbBatch() {
	time.Sleep(16 * time.Millisecond)
	c.mu.Lock()
	c.thumbRepaintPending = false
	onRepaint := c.OnRepaint
	c.mu.Unlock()
	if onRepaint != nil {
		onRepaint()
	}
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

// originalPreviewDwell is how long the selection must rest on an item before
// its original image is decoded. Sweeping the pointer across rows therefore
// decodes one image instead of one per row, and stale requests never win.
const originalPreviewDwell = 120 * time.Millisecond

// scheduleOriginalBitmap decodes path once the selection has rested on it for
// originalPreviewDwell. A newer call cancels the pending one.
func (c *AppController) scheduleOriginalBitmap(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	c.mu.Lock()
	if c.originalTimer != nil && c.originalPath == path {
		c.mu.Unlock()
		return
	}
	if c.originalLoading != nil && c.originalLoading[path] {
		c.mu.Unlock()
		return
	}
	if c.originalTimer != nil {
		c.originalTimer.Stop()
		c.originalTimer = nil
	}
	c.originalGen++
	gen := c.originalGen
	c.originalPath = path
	c.originalTimer = time.AfterFunc(originalPreviewDwell, func() {
		c.mu.Lock()
		stale := c.originalGen != gen
		if !stale && c.originalPath == path {
			c.originalTimer = nil
		}
		c.mu.Unlock()
		if stale {
			return
		}
		c.loadOriginalBitmapAsync(path)
	})
	c.mu.Unlock()
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
