package clipboard

import (
	"context"
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
}

// CaptureCandidate represents a candidate pasteboard snapshot before privacy filtering.
type CaptureCandidate struct {
	Content         string    `json:"content"`
	SourceBundleID  string    `json:"sourceBundleId,omitempty"`
	SourceAppName   string    `json:"sourceAppName,omitempty"`
	PasteboardTypes []string  `json:"pasteboardTypes,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
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
