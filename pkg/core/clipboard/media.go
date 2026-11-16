package clipboard

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"  // decode GIF clipboard payloads
	_ "image/jpeg" // decode JPEG clipboard payloads
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Media blob storage for clipboard images (docs/design/media-preview.md §4–§6). Pure Go,
// no cgo: blobs are content-addressed by sha256, thumbnails are box-downscaled PNGs.

const (
	MaxImageBytes   = 40 << 20 // reject larger clipboard images (decompression-bomb guard)
	MaxImageDim     = 8000     // reject images wider/taller than this
	MaxImagePixels  = 32_000_000
	ThumbMaxEdge    = 256
	thumbsDirName   = "thumbs"
	defaultQuotaMiB = 512
)

var (
	ErrImageTooLarge = errors.New("clipboard: image exceeds size limit")
	ErrNotImage      = errors.New("clipboard: payload is not a decodable image")
)

// MediaStore keeps <dir>/<sha256>.png blobs and <dir>/thumbs/<sha256>.png thumbnails.
type MediaStore struct {
	Dir string
}

func NewMediaStore(dir string) *MediaStore { return &MediaStore{Dir: dir} }

func (m *MediaStore) blobPath(sha, ext string) string { return filepath.Join(m.Dir, sha+ext) }
func (m *MediaStore) thumbPath(sha string) string {
	return filepath.Join(m.Dir, thumbsDirName, sha+".png")
}

// CheckImage validates size limits from the header only (no pixel decode).
func CheckImage(data []byte) (image.Config, string, error) {
	if len(data) == 0 {
		return image.Config{}, "", ErrNotImage
	}
	if len(data) > MaxImageBytes {
		return image.Config{}, "", ErrImageTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return image.Config{}, "", fmt.Errorf("%w: %v", ErrNotImage, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxImageDim || cfg.Height > MaxImageDim ||
		int64(cfg.Width)*int64(cfg.Height) > MaxImagePixels {
		return cfg, format, ErrImageTooLarge
	}
	return cfg, format, nil
}

// SaveImage validates data, writes the blob (deduplicated by sha) and a thumbnail, and returns
// the Media metadata for a clipboard entry. Thumbnail failure is non-fatal (ThumbPath empty).
func (m *MediaStore) SaveImage(data []byte) (Media, error) {
	cfg, format, err := CheckImage(data)
	if err != nil {
		return Media{}, err
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	ext, mime := ".png", "image/png"
	switch format {
	case "jpeg":
		ext, mime = ".jpg", "image/jpeg"
	case "gif":
		ext, mime = ".gif", "image/gif"
	}
	blob := m.blobPath(sha, ext)
	if err := writeFileAtomic(blob, data); err != nil {
		return Media{}, err
	}
	media := Media{
		Kind: KindImage, MediaPath: blob, MediaMime: mime,
		Width: cfg.Width, Height: cfg.Height, BlobSHA: sha, ByteSize: int64(len(data)),
	}
	if thumb, err := m.ensureThumb(sha, data); err == nil {
		media.ThumbPath = thumb
	}
	return media, nil
}

func (m *MediaStore) ensureThumb(sha string, data []byte) (string, error) {
	out := m.thumbPath(sha)
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	return out, SaveThumbnail(out, img)
}

// SaveThumbnail downsizes img to ThumbMaxEdge and writes a PNG atomically.
func SaveThumbnail(path string, img image.Image) error {
	var buf bytes.Buffer
	if err := png.Encode(&buf, Thumbnail(img, ThumbMaxEdge)); err != nil {
		return err
	}
	return writeFileAtomic(path, buf.Bytes())
}

// Thumbnail box-filters img so its longer edge is at most maxEdge (no upscaling).
func Thumbnail(img image.Image, maxEdge int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	tw, th := w, h
	if w >= h && w > maxEdge {
		tw, th = maxEdge, max(1, h*maxEdge/w)
	} else if h > w && h > maxEdge {
		tw, th = max(1, w*maxEdge/h), maxEdge
	}
	if tw == w && th == h {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
		return dst
	}
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	for y := 0; y < th; y++ {
		y0, y1 := b.Min.Y+y*h/th, b.Min.Y+(y+1)*h/th
		for x := 0; x < tw; x++ {
			x0, x1 := b.Min.X+x*w/tw, b.Min.X+(x+1)*w/tw
			// Sample at most 4×4 points per output pixel to bound cost for 8000px sources.
			sy, sx := max(1, (y1-y0)/4), max(1, (x1-x0)/4)
			var r, g, bl, a, n uint32
			for yy := y0; yy < y1; yy += sy {
				for xx := x0; xx < x1; xx += sx {
					cr, cg, cb, ca := img.At(xx, yy).RGBA()
					r, g, bl, a, n = r+cr, g+cg, bl+cb, a+ca, n+1
				}
			}
			if n == 0 {
				continue
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8), uint8(a / n >> 8)})
		}
	}
	return dst
}

func writeFileAtomic(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil // content-addressed: identical bytes already stored
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// QuotaEvictions returns IDs of the oldest unpinned blob-backed media entries to delete so the
// total stored blob bytes fit quotaBytes. File/video entries reference user files (never
// deleted) and only count their thumbnails, so they are not evicted here.
func QuotaEvictions(items []ClipboardItem, quotaBytes int64) []string {
	if quotaBytes <= 0 {
		quotaBytes = defaultQuotaMiB << 20
	}
	var total int64
	var candidates []ClipboardItem
	for _, it := range items {
		if it.Kind != KindImage {
			continue
		}
		total += it.ByteSize
		if !it.IsPinned {
			candidates = append(candidates, it)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].UpdatedAt.Before(candidates[j].UpdatedAt) })
	var out []string
	for _, it := range candidates {
		if total <= quotaBytes {
			break
		}
		out = append(out, it.ID)
		total -= it.ByteSize
	}
	return out
}

// GC removes blobs/thumbnails under Dir that no item references. Only files named like
// content-addressed blobs (64 hex chars) or our temp files are touched; nothing outside Dir is.
func (m *MediaStore) GC(items []ClipboardItem) (int, error) {
	keep := map[string]bool{}
	for _, it := range items {
		if it.MediaPath != "" {
			keep[filepath.Clean(it.MediaPath)] = true
		}
		if it.ThumbPath != "" {
			keep[filepath.Clean(it.ThumbPath)] = true
		}
	}
	removed := 0
	for _, dir := range []string{m.Dir, filepath.Join(m.Dir, thumbsDirName)} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return removed, err
		}
		for _, e := range entries {
			if e.IsDir() || !isManagedBlobName(e.Name()) {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if keep[filepath.Clean(p)] {
				continue
			}
			if os.Remove(p) == nil {
				removed++
			}
		}
	}
	return removed, nil
}

func isManagedBlobName(name string) bool {
	if strings.HasPrefix(name, ".tmp-") {
		return true
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if len(base) != 64 {
		return false
	}
	_, err := hex.DecodeString(base)
	return err == nil
}
