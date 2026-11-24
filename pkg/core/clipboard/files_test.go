package clipboard

import (
	"errors"
	"image"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestIsVideoPath(t *testing.T) {
	for p, want := range map[string]bool{
		`C:\Users\a\clip.MP4`: true, "/tmp/x.mkv": true, `D:\v\movie.final.webm`: true,
		`C:\doc.txt`: false, "noext": false, `C:\dir.mp4\file`: false,
	} {
		if got := IsVideoPath(p); got != want {
			t.Fatalf("%q: %v", p, got)
		}
	}
}

func TestSaveFileListVideoAndFiles(t *testing.T) {
	ms := NewMediaStore(t.TempDir())
	thumbCalls := 0
	probe := FileProbe{
		Size:       func(p string) (int64, bool) { return 1000, !strings.Contains(p, "missing") },
		Thumbnail:  func(string) (image.Image, error) { thumbCalls++; return image.NewRGBA(image.Rect(0, 0, 640, 360)), nil },
		DurationMs: func(string) int64 { return 42_000 },
		Dimensions: func(string) (int, int) { return 1920, 1080 },
		MIME:       func(string) string { return "video/mp4" },
	}
	m, content, err := ms.SaveFileList([]string{`C:\Videos\Trip.mp4`}, probe)
	if err != nil {
		t.Fatal(err)
	}
	if m.Kind != KindVideo || m.DurationMs != 42_000 || m.Width != 1920 || m.Height != 1080 || m.MediaMime != "video/mp4" || m.ByteSize != 1000 || content != "Trip.mp4" || m.ThumbPath == "" {
		t.Fatalf("video media = %+v content=%q", m, content)
	}
	if _, err := os.Stat(m.ThumbPath); err != nil {
		t.Fatal("thumbnail not written")
	}
	// Windows paths are case-insensitive; Unix paths are not.
	m2, _, _ := ms.SaveFileList([]string{`c:\videos\trip.MP4`}, probe)
	if runtime.GOOS == "windows" && m2.BlobSHA != m.BlobSHA {
		t.Fatal("path case changed dedupe hash on Windows")
	}
	if runtime.GOOS != "windows" && m2.BlobSHA == m.BlobSHA {
		t.Fatal("distinct path case unexpectedly deduplicated on Unix")
	}
	// Multi-file drop is KindFile even if it contains a video; missing files are tolerated.
	f, content, err := ms.SaveFileList([]string{`C:\a.txt`, `C:\Videos\b.mp4`, `C:\missing.doc`}, probe)
	if err != nil || f.Kind != KindFile || f.DurationMs != 0 || f.ByteSize != 2000 || content != "a.txt\nb.mp4\nmissing.doc" {
		t.Fatalf("files = %+v %q %v", f, content, err)
	}
	if got := SplitFilePaths(f.MediaPath); len(got) != 3 {
		t.Fatalf("paths = %v", got)
	}
}

func TestSaveFileListSingleImageMetadata(t *testing.T) {
	ms := NewMediaStore(t.TempDir())
	calls := 0
	m, content, err := ms.SaveFileList([]string{`C:\Pictures\still.PNG`}, FileProbe{
		Size: func(string) (int64, bool) { return 7, true },
		Dimensions: func(string) (int, int) {
			calls++
			return 800, 600
		},
		MIME: func(string) string { return "image/png" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Kind != KindFile || m.Width != 800 || m.Height != 600 || m.MediaMime != "image/png" || content != "still.PNG" || calls != 1 {
		t.Fatalf("image media = %+v content=%q dimensions calls=%d", m, content, calls)
	}
}

func TestSaveFileListFailures(t *testing.T) {
	ms := NewMediaStore(t.TempDir())
	if _, _, err := ms.SaveFileList(nil, FileProbe{}); !errors.Is(err, ErrNoFiles) {
		t.Fatal(err)
	}
	gone := FileProbe{Size: func(string) (int64, bool) { return 0, false }}
	if _, _, err := ms.SaveFileList([]string{`C:\gone.mp4`}, gone); !errors.Is(err, ErrNoFiles) {
		t.Fatal(err)
	}
	// Thumbnail failure is non-fatal.
	bad := FileProbe{Thumbnail: func(string) (image.Image, error) { return nil, errors.New("no shell") }}
	m, _, err := ms.SaveFileList([]string{`C:\x.mov`}, bad)
	if err != nil || m.Kind != KindVideo || m.ThumbPath != "" {
		t.Fatalf("%+v %v", m, err)
	}
	// Path list is capped.
	many := make([]string, MaxFileListPaths+20)
	for i := range many {
		many[i] = `C:\f` + strings.Repeat("x", i%5) + ".txt"
	}
	m, _, _ = ms.SaveFileList(many, FileProbe{})
	if len(SplitFilePaths(m.MediaPath)) != MaxFileListPaths {
		t.Fatal("cap not applied")
	}
}

func TestSaveFileListClampsInvalidNegativeSize(t *testing.T) {
	ms := NewMediaStore(t.TempDir())
	m, _, err := ms.SaveFileList([]string{"/tmp/clip.bin"}, FileProbe{
		Size: func(string) (int64, bool) { return -1, true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.ByteSize != 0 {
		t.Fatalf("negative probe size leaked into metadata: %d", m.ByteSize)
	}
}

func TestSaveFileListSaturatesSizeOverflow(t *testing.T) {
	ms := NewMediaStore(t.TempDir())
	paths := []string{"/tmp/a.bin", "/tmp/b.bin"}
	m, _, err := ms.SaveFileList(paths, FileProbe{
		Size: func(string) (int64, bool) { return int64(^uint64(0) >> 1), true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.ByteSize != int64(^uint64(0)>>1) {
		t.Fatalf("overflowed probe size: %d", m.ByteSize)
	}
}
