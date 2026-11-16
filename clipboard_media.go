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
	ReadPNG          func() ([]byte, error)
	ReadFiles        func() ([]string, error)
	Probe            clipboard.FileProbe
	Sequence         func() int64 // nil when the bridge has no change counter
	ObservedSequence int64        // sequence returned with PollClipboardCandidate
}

// clipboardSequenceMatches verifies that the clipboard still contains the snapshot that
// produced the candidate. The observed sequence is supplied by PollClipboardCandidate, rather
// than sampled after the candidate has already been returned.
func clipboardSequenceMatches(src mediaSource) bool {
	return src.Sequence == nil || src.Sequence() == src.ObservedSequence
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
		if !clipboardSequenceMatches(src) {
			return false, errClipboardChanged
		}
		data, err := src.ReadPNG()
		if err != nil {
			return false, err
		}
		if !clipboardSequenceMatches(src) {
			return false, errClipboardChanged
		}
		media, err := ms.SaveImage(data)
		if err != nil {
			return false, err
		}
		cand.Media = media
		return true, nil
	case clipboard.KindFile, clipboard.KindVideo:
		if src.ReadFiles == nil {
			return false, nil
		}
		if !clipboardSequenceMatches(src) {
			return false, errClipboardChanged
		}
		paths, err := src.ReadFiles()
		if err != nil {
			return false, err
		}
		if !clipboardSequenceMatches(src) {
			return false, errClipboardChanged
		}
		// Files are referenced, not copied: only a thumbnail may be written.
		media, names, err := ms.SaveFileList(paths, src.Probe)
		if err != nil {
			return false, err
		}
		cand.Media = media
		cand.Content = names // searchable by file name
		return true, nil
	default:
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
