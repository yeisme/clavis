package storage

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OrphanGCResult summarizes a reconcile pass over managed media and overflow-text directories.
type OrphanGCResult struct {
	ScannedFiles   int   `json:"scannedFiles"`
	RemovedFiles   int   `json:"removedFiles"`
	ReclaimedBytes int64 `json:"reclaimedBytes"`
}

// ReconcileOrphanMedia removes unreferenced content-addressed blobs/thumbnails/overflow-text
// files inside mediaDir whose modification age exceeds gracePeriod. Symlinks and unrelated
// user files are never deleted.
func ReconcileOrphanMedia(ctx context.Context, pool *DBPool, mediaDir string, gracePeriod time.Duration) (OrphanGCResult, error) {
	if pool == nil {
		return OrphanGCResult{}, errors.New("orphan gc: nil db pool")
	}
	mediaDir = strings.TrimSpace(mediaDir)
	if mediaDir == "" {
		return OrphanGCResult{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if gracePeriod < 0 {
		gracePeriod = 0
	}

	keepAbs := make(map[string]struct{})
	keepRel := make(map[string]struct{})
	recordRef := func(raw string) {
		p := strings.TrimSpace(raw)
		if p == "" {
			return
		}
		norm := filepath.ToSlash(filepath.Clean(p))
		keepRel[norm] = struct{}{}
		if filepath.IsAbs(p) {
			keepAbs[filepath.Clean(p)] = struct{}{}
		} else {
			keepAbs[filepath.Clean(filepath.Join(mediaDir, filepath.FromSlash(norm)))] = struct{}{}
		}
	}

	clipRows, err := pool.QueryRead(ctx, "gc.clipboard_refs",
		`SELECT media_path, thumb_path, content_overflow_path FROM clipboard_items
		 WHERE media_path <> '' OR thumb_path <> '' OR content_overflow_path <> ''`,
	)
	if err != nil {
		return OrphanGCResult{}, err
	}
	for _, r := range clipRows.Values {
		recordRef(str(r, 0))
		recordRef(str(r, 1))
		recordRef(str(r, 2))
	}

	if assetRows, err := pool.QueryRead(ctx, "gc.capture_refs",
		`SELECT file_path, thumb_path FROM capture_assets WHERE file_path <> '' OR thumb_path <> ''`,
	); err == nil {
		for _, r := range assetRows.Values {
			recordRef(str(r, 0))
			recordRef(str(r, 1))
		}
	}

	cutoff := time.Now().Add(-gracePeriod)
	var res OrphanGCResult
	subdirs := []string{"", "thumbs", "text"}
	for _, sub := range subdirs {
		dir := mediaDir
		if sub != "" {
			dir = filepath.Join(mediaDir, sub)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return res, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !isManagedGCFile(entry.Name()) {
				continue
			}
			full := filepath.Clean(filepath.Join(dir, entry.Name()))
			info, err := os.Lstat(full)
			if err != nil || info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
				continue
			}
			res.ScannedFiles++
			if _, ok := keepAbs[full]; ok {
				continue
			}
			rel := entry.Name()
			if sub != "" {
				rel = sub + "/" + entry.Name()
			}
			if _, ok := keepRel[rel]; ok {
				continue
			}
			if gracePeriod > 0 && info.ModTime().After(cutoff) {
				continue
			}
			if rmErr := os.Remove(full); rmErr == nil {
				res.RemovedFiles++
				res.ReclaimedBytes += info.Size()
			}
		}
	}
	return res, nil
}

func isManagedGCFile(name string) bool {
	if strings.HasPrefix(name, ".tmp-") {
		return true
	}
	stem := name
	if strings.HasSuffix(stem, ".txt.gz") {
		stem = strings.TrimSuffix(stem, ".txt.gz")
	} else {
		stem = strings.TrimSuffix(stem, filepath.Ext(stem))
	}
	if len(stem) != 64 {
		return false
	}
	_, err := hex.DecodeString(stem)
	return err == nil
}
