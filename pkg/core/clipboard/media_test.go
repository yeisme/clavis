package clipboard

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMediaStoreSaveImageDedupeAndThumb(t *testing.T) {
	ms := NewMediaStore(t.TempDir())
	data := pngBytes(t, 1024, 512)
	m, err := ms.SaveImage(data)
	if err != nil {
		t.Fatal(err)
	}
	if m.Kind != KindImage || m.Width != 1024 || m.Height != 512 || m.MediaMime != "image/png" || len(m.BlobSHA) != 64 {
		t.Fatalf("media = %+v", m)
	}
	if _, err := os.Stat(m.MediaPath); err != nil {
		t.Fatalf("blob missing: %v", err)
	}
	f, err := os.Open(m.ThumbPath)
	if err != nil {
		t.Fatalf("thumb missing: %v", err)
	}
	cfg, _, err := image.DecodeConfig(f)
	_ = f.Close()
	if err != nil || cfg.Width != ThumbMaxEdge || cfg.Height != ThumbMaxEdge/2 {
		t.Fatalf("thumb cfg = %+v %v", cfg, err)
	}
	m2, err := ms.SaveImage(data)
	if err != nil || m2.MediaPath != m.MediaPath {
		t.Fatalf("dedupe: %+v %v", m2, err)
	}
	entries, _ := os.ReadDir(ms.Dir)
	files := 0
	for _, e := range entries {
		if !e.IsDir() {
			files++
		}
	}
	if files != 1 {
		t.Fatalf("expected 1 blob file, got %d", files)
	}
}

func TestCheckImageLimits(t *testing.T) {
	if _, _, err := CheckImage([]byte("not an image")); err == nil {
		t.Fatal("garbage accepted")
	}
	if _, _, err := CheckImage(nil); err == nil {
		t.Fatal("empty accepted")
	}
	// Header claims 9000px wide: rejected without decoding pixels.
	big := pngBytes(t, 2, 2)
	hdr := append([]byte(nil), big...)
	// IHDR width is at bytes 16..19.
	hdr[16], hdr[17], hdr[18], hdr[19] = 0, 0, 0x23, 0x28 // 9000
	// Recompute the IHDR CRC (over type+data, bytes 12..28) so only the size check can fail.
	binary.BigEndian.PutUint32(hdr[29:33], crc32.ChecksumIEEE(hdr[12:29]))
	if _, _, err := CheckImage(hdr); err != ErrImageTooLarge {
		t.Fatalf("9000px: %v", err)
	}
	if _, _, err := CheckImage(make([]byte, MaxImageBytes+1)); err != ErrImageTooLarge {
		t.Fatalf("oversize bytes: %v", err)
	}
	if MaxImagePixels <= 0 {
		t.Fatal("pixel budget must be positive")
	}
}

func TestThumbnailNoUpscale(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 300))
	got := Thumbnail(img, 256).Bounds()
	if got.Dy() != 256 || got.Dx() != 34 {
		t.Fatalf("portrait thumb = %v", got)
	}
	if got := Thumbnail(image.NewRGBA(image.Rect(0, 0, 10, 10)), 256).Bounds(); got.Dx() != 10 {
		t.Fatalf("small image upscaled: %v", got)
	}
}

func TestSaveThumbnailRejectsNilImage(t *testing.T) {
	if err := SaveThumbnail(filepath.Join(t.TempDir(), "thumb.png"), nil); !errors.Is(err, ErrNilImage) {
		t.Fatalf("nil image error = %v, want %v", err, ErrNilImage)
	}
}

func TestThumbnailNormalizesNonPositiveEdge(t *testing.T) {
	got := Thumbnail(image.NewRGBA(image.Rect(0, 0, 10, 4)), 0).Bounds()
	if got.Dx() != 1 || got.Dy() != 1 {
		t.Fatalf("thumbnail bounds = %v, want 1x1", got)
	}
}

func TestQuotaEvictionsOldestUnpinnedImagesOnly(t *testing.T) {
	t0 := time.Unix(1000, 0)
	img := func(id string, age int, size int64, pinned bool) ClipboardItem {
		return ClipboardItem{ID: id, IsPinned: pinned, UpdatedAt: t0.Add(time.Duration(age) * time.Second),
			Media: Media{Kind: KindImage, ByteSize: size}}
	}
	items := []ClipboardItem{
		img("new", 30, 40, false),
		img("pinned-old", 0, 40, true),
		img("old", 10, 40, false),
		img("mid", 20, 40, false),
		{ID: "text", UpdatedAt: t0},
		{ID: "file", UpdatedAt: t0, Media: Media{Kind: KindFile, ByteSize: 1 << 30}},
	}
	got := QuotaEvictions(items, 100) // 160 stored → drop old, then mid
	if len(got) != 2 || got[0] != "old" || got[1] != "mid" {
		t.Fatalf("evict = %v", got)
	}
	if got := QuotaEvictions(items, 1000); len(got) != 0 {
		t.Fatalf("under quota evicted %v", got)
	}
}

func TestQuotaEvictionsBoundaryKeepsPinnedAndExternalMedia(t *testing.T) {
	t0 := time.Unix(2000, 0)
	items := []ClipboardItem{
		{ID: "new-image", UpdatedAt: t0.Add(30 * time.Second), Media: Media{Kind: KindImage, ByteSize: 40}},
		{ID: "pinned-image", UpdatedAt: t0, IsPinned: true, Media: Media{Kind: KindImage, ByteSize: 100}},
		{ID: "old-image", UpdatedAt: t0.Add(10 * time.Second), Media: Media{Kind: KindImage, ByteSize: 40}},
		{ID: "file", UpdatedAt: t0, Media: Media{Kind: KindFile, ByteSize: 1 << 30}},
		{ID: "video", UpdatedAt: t0, Media: Media{Kind: KindVideo, ByteSize: 1 << 30}},
	}
	const quota int64 = 140
	got := QuotaEvictions(items, quota)
	if len(got) != 1 || got[0] != "old-image" {
		t.Fatalf("evictions=%v, want oldest unpinned image only", got)
	}
	for _, id := range got {
		if id == "pinned-image" || id == "file" || id == "video" {
			t.Fatalf("evicted protected/external media %q", id)
		}
	}
	var remaining int64
	for _, item := range items {
		if item.Kind == KindImage && item.ID != got[0] {
			remaining += item.ByteSize
		}
	}
	if remaining > quota {
		t.Fatalf("remaining image bytes=%d, want <= quota %d", remaining, quota)
	}
}

func TestQuotaEvictionsHandlesOverflowAndNegativeSizes(t *testing.T) {
	t0 := time.Unix(3000, 0)
	items := []ClipboardItem{
		{ID: "old", UpdatedAt: t0, Media: Media{Kind: KindImage, ByteSize: int64(^uint64(0) >> 1)}},
		{ID: "new", UpdatedAt: t0.Add(time.Second), Media: Media{Kind: KindImage, ByteSize: int64(^uint64(0) >> 1)}},
		{ID: "bad", UpdatedAt: t0.Add(2 * time.Second), Media: Media{Kind: KindImage, ByteSize: -1}},
	}
	got := QuotaEvictions(items, int64(^uint64(0)>>1))
	if len(got) != 1 || got[0] != "old" {
		t.Fatalf("evictions = %v, want oldest oversized image only", got)
	}
}

func TestMediaStoreRejectsBlobPathDirectory(t *testing.T) {
	ms := NewMediaStore(t.TempDir())
	data := pngBytes(t, 2, 2)
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	if err := os.Mkdir(ms.blobPath(sha, ".png"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ms.SaveImage(data); err == nil {
		t.Fatal("blob directory was treated as an existing file")
	}
}

func TestMediaStoreRejectsBlobPathSymlink(t *testing.T) {
	ms := NewMediaStore(t.TempDir())
	data := pngBytes(t, 2, 2)
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, ms.blobPath(sha, ".png")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ms.SaveImage(data); err == nil {
		t.Fatal("blob symlink was treated as an existing file")
	}
}

func TestMediaStoreGCRemovesOnlyUnreferencedManagedFiles(t *testing.T) {
	ms := NewMediaStore(t.TempDir())
	keep, err := ms.SaveImage(pngBytes(t, 8, 8))
	if err != nil {
		t.Fatal(err)
	}
	drop, err := ms.SaveImage(pngBytes(t, 9, 9))
	if err != nil {
		t.Fatal(err)
	}
	user := filepath.Join(ms.Dir, "notes.txt")
	_ = os.WriteFile(user, []byte("x"), 0o600)
	n, err := ms.GC([]ClipboardItem{{Media: keep}})
	if err != nil || n != 2 { // drop blob + drop thumb
		t.Fatalf("gc removed %d, %v", n, err)
	}
	for _, p := range []string{keep.MediaPath, keep.ThumbPath, user} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s should remain", p)
		}
	}
	for _, p := range []string{drop.MediaPath, drop.ThumbPath} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s should be gone", p)
		}
	}
}

func TestPrivacyFilterMedia(t *testing.T) {
	f := NewPrivacyFilter()
	img := CaptureCandidate{Media: Media{Kind: KindImage}}
	if !f.ShouldRecord(img) {
		t.Fatal("empty-content image must be recordable")
	}
	img.SourceAppName = "KeePassXC"
	if f.ShouldRecord(img) {
		t.Fatal("password manager image must be ignored")
	}
	img.SourceAppName, img.SourceBundleID = "", "Bitwarden.exe"
	if f.ShouldRecord(img) {
		t.Fatal("password manager process (bundle id slot) must be ignored")
	}
	img.SourceBundleID = ""
	img.PasteboardTypes = []string{"ExcludeClipboardContentFromMonitorProcessing"}
	if f.ShouldRecord(img) {
		t.Fatal("exclusion marker must apply to images")
	}
	if f.ShouldRecord(CaptureCandidate{Content: "  "}) {
		t.Fatal("whitespace text still ignored")
	}
	if a, b := (CaptureCandidate{Content: "x"}).DedupeHash(), (CaptureCandidate{Content: "x", Media: Media{Kind: KindImage, BlobSHA: "s"}}).DedupeHash(); a == b {
		t.Fatal("image hash must differ from text hash")
	}
}
