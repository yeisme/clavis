package clipboard

import (
	"context"
	"strings"
	"time"
)

// ClipboardItem represents one persisted clipboard history entry.
type ClipboardItem struct {
	ID             string    `json:"id"`
	Content        string    `json:"content"`
	ContentHash    string    `json:"contentHash"`
	SourceBundleID string    `json:"sourceBundleId,omitempty"`
	SourceAppName  string    `json:"sourceAppName,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	IsPinned       bool      `json:"isPinned"`
	Media
}

// Kind classifies a clipboard entry. The zero value means text.
type Kind string

const (
	KindText  Kind = "text"
	KindImage Kind = "image"
	KindFile  Kind = "file"
	KindVideo Kind = "video"
)

// Media holds the non-text payload metadata of an entry (docs/design/media-preview.md §2).
// All zero for text entries, so existing text behaviour is unchanged.
type Media struct {
	Kind       Kind   `json:"kind,omitempty"`
	MediaPath  string `json:"mediaPath,omitempty"`
	MediaMime  string `json:"mediaMime,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	BlobSHA    string `json:"blobSha,omitempty"`
	ThumbPath  string `json:"thumbPath,omitempty"`
	ByteSize   int64  `json:"byteSize,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
}

// IsMedia reports whether the entry carries a non-text payload.
func (m Media) IsMedia() bool { return m.Kind != "" && m.Kind != KindText }

// DedupeHash is the content_hash used for an item: the blob/paths hash for media, else the text hash.
func (c CaptureCandidate) DedupeHash() string {
	if c.IsMedia() && c.BlobSHA != "" {
		return string(c.Kind) + ":" + c.BlobSHA
	}
	return contentSHA256(c.Content)
}

// CaptureCandidate represents a candidate pasteboard snapshot before privacy filtering.
type CaptureCandidate struct {
	Content         string    `json:"content"`
	SourceBundleID  string    `json:"sourceBundleId,omitempty"`
	SourceAppName   string    `json:"sourceAppName,omitempty"`
	PasteboardTypes []string  `json:"pasteboardTypes,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
	// Media is filled by the poller after privacy filtering; the bridge only sets Kind (and
	// FilePaths for file drops) so no secret payload is read before the filter runs.
	Media
	FilePaths []string `json:"filePaths,omitempty"`
}

// Store is the platform-neutral clipboard history persistence interface.
type Store interface {
	Record(ctx context.Context, candidate CaptureCandidate) (*ClipboardItem, bool, error)
	Search(ctx context.Context, query string, limit int) ([]ClipboardItem, error)
	Recent(ctx context.Context, limit int) ([]ClipboardItem, error)
	Pin(ctx context.Context, id string, pinned bool) error
	Delete(ctx context.Context, id string) error
	Clear(ctx context.Context) error
	PruneExpired(ctx context.Context, retentionDays int, maxItems int) (int, error)
}

// GenerationTracker is optionally implemented by Store backends that expose a
// monotonically increasing mutation generation counter for incremental sync.
type GenerationTracker interface {
	Generation() uint64
}

// File/video entries store their paths newline-joined in MediaPath (paths cannot contain '\n'
// on Windows, and the original files are never copied or deleted by Clavis).

// JoinFilePaths encodes a file list for MediaPath.
func JoinFilePaths(paths []string) string { return strings.Join(paths, "\n") }

// SplitFilePaths decodes MediaPath of a file/video entry.
func SplitFilePaths(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
