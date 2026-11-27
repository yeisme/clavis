package clipboard

import (
	"context"
	"fmt"
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
	CopyCount      int       `json:"copyCount"`
	IsPinned       bool      `json:"isPinned"`
	Media
}

// ClipboardSummary carries a bounded preview (up to 512 chars) and metadata for fast virtual list scrolling.
type ClipboardSummary struct {
	ID               string    `json:"id"`
	Preview          string    `json:"preview"`
	ContentHash      string    `json:"contentHash"`
	ContentTruncated bool      `json:"contentTruncated,omitempty"`
	OverflowPath     string    `json:"overflowPath,omitempty"`
	SourceBundleID   string    `json:"sourceBundleId,omitempty"`
	SourceAppName    string    `json:"sourceAppName,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	CopyCount        int       `json:"copyCount"`
	IsPinned         bool      `json:"isPinned"`
	Media
}

// ClipboardCursor identifies an exact keyset position in `(is_pinned DESC, updated_at_ns DESC, id ASC)` order.
type ClipboardCursor struct {
	IsPinned    bool   `json:"isPinned"`
	UpdatedAtNs int64  `json:"updatedAtNs"`
	ID          string `json:"id"`
}

// ClipboardQuerySpec expresses pushdown filters and keyset cursor pagination for SQLite queries.
type ClipboardQuerySpec struct {
	Text       string
	Kind       Kind
	AppNeedle  string
	PinnedOnly bool
	SinceNs    int64
	Limit      int
	Cursor     *ClipboardCursor
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

// DedupeHash is the content_hash used for an item: the blob hash for media, else the text hash.
// Malformed media candidates get a kind-prefixed fallback so they can never share a text hash;
// stores reject them via Validate before persistence.
func (c CaptureCandidate) DedupeHash() string {
	if c.IsMedia() {
		if strings.TrimSpace(c.BlobSHA) != "" {
			return string(c.Kind) + ":" + c.BlobSHA
		}
		return string(c.Kind) + ":" + contentSHA256(c.Content)
	}
	return contentSHA256(c.Content)
}

// Validate rejects candidates that cannot be safely deduplicated as media.
func (c CaptureCandidate) Validate() error {
	switch c.Kind {
	case "", KindText:
		return nil
	case KindImage, KindFile, KindVideo:
		if strings.TrimSpace(c.BlobSHA) == "" {
			return fmt.Errorf("media candidate of kind %q is missing BlobSHA", c.Kind)
		}
	default:
		return fmt.Errorf("media candidate has unsupported kind %q", c.Kind)
	}
	if c.ByteSize < 0 || c.DurationMs < 0 || c.Width < 0 || c.Height < 0 {
		return fmt.Errorf("media candidate of kind %q has negative metadata", c.Kind)
	}
	return nil
}

// MergeMediaMetadata repairs an existing media row with every non-zero field from incoming.
// KindText is the storage representation for text rows and must not erase media metadata.
func MergeMediaMetadata(existing, incoming Media) Media {
	if incoming.Kind != "" && incoming.Kind != KindText {
		existing.Kind = incoming.Kind
	}
	if incoming.MediaPath != "" {
		existing.MediaPath = incoming.MediaPath
	}
	if incoming.MediaMime != "" {
		existing.MediaMime = incoming.MediaMime
	}
	if incoming.Width != 0 {
		existing.Width = incoming.Width
	}
	if incoming.Height != 0 {
		existing.Height = incoming.Height
	}
	if incoming.BlobSHA != "" {
		existing.BlobSHA = incoming.BlobSHA
	}
	if incoming.ThumbPath != "" {
		existing.ThumbPath = incoming.ThumbPath
	}
	if incoming.ByteSize != 0 {
		existing.ByteSize = incoming.ByteSize
	}
	if incoming.DurationMs != 0 {
		existing.DurationMs = incoming.DurationMs
	}
	return existing
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

// UnpinnedClearer is optionally implemented by Store backends to clear unpinned
// history items while strictly preserving pinned items.
type UnpinnedClearer interface {
	ClearUnpinned(ctx context.Context) (int, error)
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
