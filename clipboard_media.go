package main

import (
	"context"
	"errors"

	"github.com/yeisme/clavis/internal/storage"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
)

// Clipboard media capture glue for the poller (docs/design/media-preview.md §4–§5).

var errClipboardChanged = errors.New("clipboard changed while reading media")

// mediaSource abstracts the platform reads so enrichMediaCandidate is unit-testable.
type mediaSource struct {
	ReadPNG  func() ([]byte, error)
	Sequence func() int64 // nil when the bridge has no change counter
}

// enrichMediaCandidate fills an image candidate's payload. It returns false when the candidate
// must be dropped. Order matters for privacy: the filter runs BEFORE any bytes are read or
// written to disk, so excluded apps / concealed markers never leave a blob behind.
func enrichMediaCandidate(cand *clipboard.CaptureCandidate, cfg config.ClipboardConfig, ms *clipboard.MediaStore, src mediaSource) (bool, error) {
	if !cand.IsMedia() {
		return true, nil
	}
	if !cfg.MediaCapture || ms == nil {
		return false, nil
	}
	if !clipboard.NewPrivacyFilterFromConfig(cfg).ShouldRecord(*cand) {
		return false, nil
	}
	switch cand.Kind {
	case clipboard.KindImage:
		var before int64
		if src.Sequence != nil {
			before = src.Sequence()
		}
		data, err := src.ReadPNG()
		if err != nil {
			return false, err
		}
		if src.Sequence != nil && src.Sequence() != before {
			return false, errClipboardChanged
		}
		media, err := ms.SaveImage(data)
		if err != nil {
			return false, err
		}
		cand.Media = media
		return true, nil
	default:
		// File/video capture is wired in P3.
		return false, nil
	}
}

// maintainClipboardMedia evicts over-quota image rows and removes unreferenced blobs.
func maintainClipboardMedia(ctx context.Context, store clipboard.Store, ms *clipboard.MediaStore, cfg config.ClipboardConfig) {
	if store == nil || ms == nil {
		return
	}
	items, err := store.Recent(ctx, 0)
	if err != nil {
		return
	}
	evict := clipboard.QuotaEvictions(items, int64(cfg.MediaQuotaMB)<<20)
	if len(evict) > 0 {
		gone := make(map[string]bool, len(evict))
		for _, id := range evict {
			if store.Delete(ctx, id) == nil {
				gone[id] = true
			}
		}
		kept := items[:0]
		for _, it := range items {
			if !gone[it.ID] {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	_, _ = ms.GC(items)
}

// sqliteStoreFor reports whether media can be recorded: blobs are only kept alongside the
// persistent store so GC has a durable reference set.
func sqliteStoreFor(store clipboard.Store) bool {
	_, ok := store.(*storage.SQLiteClipboardStore)
	return ok
}
