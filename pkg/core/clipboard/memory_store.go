package clipboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MemoryStore is a pure-stdlib in-memory reference implementation of Store.
type MemoryStore struct {
	mu         sync.RWMutex
	filter     PrivacyFilter
	items      map[string]ClipboardItem
	byHash     map[string]string
	seq        atomic.Uint64
	generation atomic.Uint64
}

var (
	_ Store             = (*MemoryStore)(nil)
	_ GenerationTracker = (*MemoryStore)(nil)
)

// NewMemoryStore constructs an empty MemoryStore with the given PrivacyFilter.
func NewMemoryStore(filter PrivacyFilter) *MemoryStore {
	return &MemoryStore{
		filter: filter,
		items:  make(map[string]ClipboardItem),
		byHash: make(map[string]string),
	}
}

func contentSHA256(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func (s *MemoryStore) Record(ctx context.Context, candidate CaptureCandidate) (*ClipboardItem, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if !s.filter.ShouldRecord(candidate) {
		return nil, false, nil
	}

	now := candidate.Timestamp
	if now.IsZero() {
		now = time.Now().UTC()
	}
	hash := candidate.DedupeHash()

	s.mu.Lock()
	defer s.mu.Unlock()

	if existingID, ok := s.byHash[hash]; ok {
		item := s.items[existingID]
		item.UpdatedAt = now
		if candidate.SourceBundleID != "" {
			item.SourceBundleID = candidate.SourceBundleID
		}
		if candidate.SourceAppName != "" {
			item.SourceAppName = candidate.SourceAppName
		}
		s.items[existingID] = item
		s.generation.Add(1)
		copied := item
		return &copied, true, nil
	}

	id := fmt.Sprintf("clip-%d", s.seq.Add(1))
	item := ClipboardItem{
		ID:             id,
		Content:        candidate.Content,
		ContentHash:    hash,
		SourceBundleID: candidate.SourceBundleID,
		SourceAppName:  candidate.SourceAppName,
		CreatedAt:      now,
		UpdatedAt:      now,
		IsPinned:       false,
		Media:          candidate.Media,
	}
	s.items[id] = item
	s.byHash[hash] = id
	s.generation.Add(1)
	copied := item
	return &copied, true, nil
}

func (s *MemoryStore) Search(ctx context.Context, query string, limit int) ([]ClipboardItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return s.Recent(ctx, limit)
	}
	lowerQ := strings.ToLower(trimmed)

	s.mu.RLock()
	var matched []ClipboardItem
	for _, item := range s.items {
		if strings.Contains(strings.ToLower(item.Content), lowerQ) ||
			strings.Contains(strings.ToLower(item.SourceAppName), lowerQ) {
			matched = append(matched, item)
		}
	}
	s.mu.RUnlock()

	sortClipboardItems(matched)
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func (s *MemoryStore) Recent(ctx context.Context, limit int) ([]ClipboardItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	list := make([]ClipboardItem, 0, len(s.items))
	for _, item := range s.items {
		list = append(list, item)
	}
	s.mu.RUnlock()

	sortClipboardItems(list)
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

func (s *MemoryStore) Pin(ctx context.Context, id string, pinned bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok {
		return fmt.Errorf("clipboard item %q not found", id)
	}
	if item.IsPinned != pinned {
		item.IsPinned = pinned
		s.items[id] = item
	}
	s.generation.Add(1)
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok {
		return nil
	}
	delete(s.byHash, item.ContentHash)
	delete(s.items, id)
	s.generation.Add(1)
	return nil
}

func (s *MemoryStore) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) > 0 {
		s.items = make(map[string]ClipboardItem)
		s.byHash = make(map[string]string)
		s.generation.Add(1)
	}
	return nil
}

func (s *MemoryStore) PruneExpired(ctx context.Context, retentionDays int, maxItems int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	if retentionDays > 0 {
		cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
		for id, item := range s.items {
			if !item.IsPinned && item.UpdatedAt.Before(cutoff) {
				delete(s.byHash, item.ContentHash)
				delete(s.items, id)
				removed++
			}
		}
	}

	if maxItems > 0 && len(s.items) > maxItems {
		all := make([]ClipboardItem, 0, len(s.items))
		for _, item := range s.items {
			all = append(all, item)
		}
		sortClipboardItems(all)
		// Remove oldest unpinned items from the tail until len <= maxItems
		for i := len(all) - 1; i >= 0 && len(s.items) > maxItems; i-- {
			if all[i].IsPinned {
				continue
			}
			delete(s.byHash, all[i].ContentHash)
			delete(s.items, all[i].ID)
			removed++
		}
	}

	if removed > 0 {
		s.generation.Add(1)
	}
	return removed, nil
}

// Generation returns a monotonically increasing mutation generation.
func (s *MemoryStore) Generation() uint64 { return s.generation.Load() }

func sortClipboardItems(items []ClipboardItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsPinned != items[j].IsPinned {
			return items[i].IsPinned
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].ID < items[j].ID
	})
}
