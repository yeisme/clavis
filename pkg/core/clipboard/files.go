package clipboard

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"strings"
)

// File-list / video clipboard entries (docs/design/media-preview.md §8, P3). The original files
// are referenced by path and never copied or deleted; only a thumbnail is stored.

const MaxFileListPaths = 100

var ErrNoFiles = errors.New("clipboard: file list empty or files missing")

var videoExts = map[string]bool{
	".mp4": true, ".m4v": true, ".mov": true, ".mkv": true, ".avi": true, ".wmv": true,
	".webm": true, ".flv": true, ".mpg": true, ".mpeg": true, ".3gp": true, ".mts": true, ".m2ts": true,
}

// IsVideoPath reports whether p has a common video container extension (case-insensitive).
func IsVideoPath(p string) bool {
	b := baseName(p)
	if i := strings.LastIndexByte(b, '.'); i >= 0 {
		return videoExts[strings.ToLower(b[i:])]
	}
	return false
}

// baseName splits on both separators so Windows paths work in tests on any OS.
func baseName(p string) string {
	p = strings.TrimRight(p, `/\`)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// FileProbe supplies platform facts about files; any nil func is skipped.
type FileProbe struct {
	Size       func(path string) (int64, bool)        // false = missing
	Thumbnail  func(path string) (image.Image, error) // Windows: shell thumbnail (video first frame)
	DurationMs func(path string) int64                // Windows: System.Media.Duration
	Dimensions func(path string) (width, height int)  // optional pixel dimensions; zero when unknown
	MIME       func(path string) string               // optional stable MIME type; empty when unknown
}

// SaveFileList builds Media for a CF_HDROP file list and returns the searchable content
// (file names, one per line). A single video file becomes KindVideo, anything else KindFile.
func (m *MediaStore) SaveFileList(paths []string, probe FileProbe) (Media, string, error) {
	var clean []string
	for _, p := range paths {
		if p = strings.TrimSpace(p); p != "" && !strings.ContainsAny(p, "\r\n") {
			clean = append(clean, p)
		}
		if len(clean) == MaxFileListPaths {
			break
		}
	}
	if len(clean) == 0 {
		return Media{}, "", ErrNoFiles
	}
	var total int64
	present := 0
	names := make([]string, len(clean))
	for i, p := range clean {
		names[i] = baseName(p)
		if probe.Size == nil {
			present++
			continue
		}
		if n, ok := probe.Size(p); ok {
			total += n
			present++
		}
	}
	if present == 0 {
		return Media{}, "", ErrNoFiles
	}
	joined := JoinFilePaths(clean)
	sum := sha256.Sum256([]byte(strings.ToLower(joined))) // Windows paths are case-insensitive
	media := Media{Kind: KindFile, MediaPath: joined, BlobSHA: hex.EncodeToString(sum[:]), ByteSize: total}
	if len(clean) == 1 {
		if IsVideoPath(clean[0]) {
			media.Kind = KindVideo
			if probe.DurationMs != nil {
				media.DurationMs = probe.DurationMs(clean[0])
			}
		}
		if probe.Dimensions != nil {
			media.Width, media.Height = probe.Dimensions(clean[0])
		}
		if probe.MIME != nil {
			media.MediaMime = probe.MIME(clean[0])
		}
	}
	if probe.Thumbnail != nil {
		if img, err := probe.Thumbnail(clean[0]); err == nil && img != nil {
			out := m.thumbPath(media.BlobSHA)
			if SaveThumbnail(out, img) == nil {
				media.ThumbPath = out
			}
		}
	}
	return media, strings.Join(names, "\n"), nil
}
