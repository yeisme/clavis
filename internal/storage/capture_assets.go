package storage

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CaptureAsset records persisted screenshot or screen-recording metadata in capture_assets (Schema v5).
type CaptureAsset struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"` // "screenshot" | "recording"
	FilePath   string    `json:"filePath"`
	ThumbPath  string    `json:"thumbPath,omitempty"`
	Mime       string    `json:"mime,omitempty"`
	Width      int       `json:"width,omitempty"`
	Height     int       `json:"height,omitempty"`
	DurationMs int64     `json:"durationMs,omitempty"`
	ByteSize   int64     `json:"byteSize,omitempty"`
	Pinned     bool      `json:"pinned,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// CaptureAssetStats summarizes counts and disk footprint across capture_assets.
type CaptureAssetStats struct {
	TotalCount    int   `json:"totalCount"`
	ScreenshotCnt int   `json:"screenshotCount"`
	RecordingCnt  int   `json:"recordingCount"`
	TotalByteSize int64 `json:"totalByteSize"`
	PinnedCount   int   `json:"pinnedCount"`
}

// CaptureAssetStore persists screenshot and screen recording metadata via DBPool.
type CaptureAssetStore struct {
	pool *DBPool
}

// NewCaptureAssetStore constructs a CaptureAssetStore sharing pool.
func NewCaptureAssetStore(pool *DBPool) *CaptureAssetStore {
	return &CaptureAssetStore{pool: pool}
}

// Record inserts or replaces a capture_assets entry.
func (s *CaptureAssetStore) Record(ctx context.Context, asset CaptureAsset) (CaptureAsset, error) {
	if s == nil || s.pool == nil {
		return CaptureAsset{}, fmt.Errorf("capture asset store closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if asset.CreatedAt.IsZero() {
		asset.CreatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(asset.ID) == "" {
		asset.ID = fmt.Sprintf("cap-%d", asset.CreatedAt.UnixNano())
	}
	pinnedVal := int64(0)
	if asset.Pinned {
		pinnedVal = 1
	}
	if _, err := s.pool.ExecWrite(ctx, "capture_assets.record",
		`INSERT OR REPLACE INTO capture_assets (id, kind, file_path, thumb_path, mime, width, height, duration_ms, byte_size, pinned, created_at_ns)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		asset.ID, strings.TrimSpace(asset.Kind), strings.TrimSpace(asset.FilePath), strings.TrimSpace(asset.ThumbPath),
		strings.TrimSpace(asset.Mime), asset.Width, asset.Height, asset.DurationMs, asset.ByteSize, pinnedVal,
		asset.CreatedAt.UTC().UnixNano(),
	); err != nil {
		return CaptureAsset{}, fmt.Errorf("record capture asset: %w", err)
	}
	return asset, nil
}

// List returns recent capture assets filtered optionally by kind ("screenshot" / "recording" / "").
func (s *CaptureAssetStore) List(ctx context.Context, kind string, limit int) ([]CaptureAsset, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("capture asset store closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = 50
	}
	kind = strings.TrimSpace(kind)
	var args []any
	query := `SELECT id, kind, file_path, thumb_path, mime, width, height, duration_ms, byte_size, pinned, created_at_ns FROM capture_assets`
	if kind != "" {
		query += ` WHERE kind = ?`
		args = append(args, kind)
	}
	query += ` ORDER BY pinned DESC, created_at_ns DESC, id ASC LIMIT ?`
	args = append(args, limit)

	rows, err := s.pool.QueryRead(ctx, "capture_assets.list", query, args...)
	if err != nil {
		return nil, fmt.Errorf("list capture assets: %w", err)
	}
	out := make([]CaptureAsset, 0, len(rows.Values))
	for _, r := range rows.Values {
		out = append(out, CaptureAsset{
			ID:         str(r, 0),
			Kind:       str(r, 1),
			FilePath:   str(r, 2),
			ThumbPath:  str(r, 3),
			Mime:       str(r, 4),
			Width:      int(int64val(r, 5)),
			Height:     int(int64val(r, 6)),
			DurationMs: int64val(r, 7),
			ByteSize:   int64val(r, 8),
			Pinned:     int64val(r, 9) != 0,
			CreatedAt:  time.Unix(0, int64val(r, 10)).UTC(),
		})
	}
	return out, nil
}

// Get fetches one capture asset by ID.
func (s *CaptureAssetStore) Get(ctx context.Context, id string) (CaptureAsset, bool, error) {
	if s == nil || s.pool == nil {
		return CaptureAsset{}, false, fmt.Errorf("capture asset store closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := s.pool.QueryRead(ctx, "capture_assets.get",
		`SELECT id, kind, file_path, thumb_path, mime, width, height, duration_ms, byte_size, pinned, created_at_ns FROM capture_assets WHERE id = ? LIMIT 1`,
		id,
	)
	if err != nil {
		return CaptureAsset{}, false, fmt.Errorf("get capture asset: %w", err)
	}
	if len(rows.Values) == 0 {
		return CaptureAsset{}, false, nil
	}
	r := rows.Values[0]
	return CaptureAsset{
		ID:         str(r, 0),
		Kind:       str(r, 1),
		FilePath:   str(r, 2),
		ThumbPath:  str(r, 3),
		Mime:       str(r, 4),
		Width:      int(int64val(r, 5)),
		Height:     int(int64val(r, 6)),
		DurationMs: int64val(r, 7),
		ByteSize:   int64val(r, 8),
		Pinned:     int64val(r, 9) != 0,
		CreatedAt:  time.Unix(0, int64val(r, 10)).UTC(),
	}, true, nil
}

// SetPinned updates the pinned state for an asset.
func (s *CaptureAssetStore) SetPinned(ctx context.Context, id string, pinned bool) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("capture asset store closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	val := int64(0)
	if pinned {
		val = 1
	}
	_, err := s.pool.ExecWrite(ctx, "capture_assets.pin", `UPDATE capture_assets SET pinned = ? WHERE id = ?`, val, id)
	return err
}

// Delete removes one capture asset row by ID.
func (s *CaptureAssetStore) Delete(ctx context.Context, id string) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("capture asset store closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := s.pool.ExecWrite(ctx, "capture_assets.delete", `DELETE FROM capture_assets WHERE id = ?`, id)
	return err
}

// Stats returns aggregate counts and byte totals from capture_assets.
func (s *CaptureAssetStore) Stats(ctx context.Context) (CaptureAssetStats, error) {
	if s == nil || s.pool == nil {
		return CaptureAssetStats{}, fmt.Errorf("capture asset store closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := s.pool.QueryRead(ctx, "capture_assets.stats",
		`SELECT COUNT(*),
		        COALESCE(SUM(CASE WHEN kind='screenshot' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN kind='recording' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(byte_size), 0),
		        COALESCE(SUM(CASE WHEN pinned<>0 THEN 1 ELSE 0 END), 0)
		 FROM capture_assets`,
	)
	if err != nil {
		return CaptureAssetStats{}, fmt.Errorf("capture_assets stats: %w", err)
	}
	if len(rows.Values) == 0 {
		return CaptureAssetStats{}, nil
	}
	r := rows.Values[0]
	return CaptureAssetStats{
		TotalCount:    int(int64val(r, 0)),
		ScreenshotCnt: int(int64val(r, 1)),
		RecordingCnt:  int(int64val(r, 2)),
		TotalByteSize: int64val(r, 3),
		PinnedCount:   int(int64val(r, 4)),
	}, nil
}
