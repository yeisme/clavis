package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/taxonomy"
)

const (
	AnnotationKindAgentSession = "agent_session"
	AnnotationKindClipboard    = "clipboard"
	AnnotationKindTemplate     = "template"
	AnnotationKindVaultRef     = "vault_ref"
	AnnotationKindQuickJump    = "quickjump"

	MaxAnnotationProps = 8
)

// EntityAnnotation stores user-authored metadata overrides (custom title, tags,
// favorite flag, notes, and non-secret properties) in SQLite without mutating
// external read-only sources or Vault plaintext.
type EntityAnnotation struct {
	EntityKind  string            `json:"entityKind"`
	EntityKey   string            `json:"entityKey"`
	CustomTitle string            `json:"customTitle,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Favorite    bool              `json:"favorite,omitempty"`
	Notes       string            `json:"notes,omitempty"`
	Props       map[string]string `json:"props,omitempty"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

type annotationKey struct {
	kind string
	key  string
}

// AnnotationStore provides thread-safe persistence and a lock-free/RLock-backed
// in-memory snapshot of entity_annotations on the shared app SQLite DB.
type AnnotationStore struct {
	db      *sqlite.DB
	writeMu sync.Mutex
	mu      sync.RWMutex
	items   map[annotationKey]EntityAnnotation
	gen     atomic.Uint64
}

// NewAnnotationStore ensures the app SQLite schema is migrated to v3 and loads
// the in-memory annotation snapshot.
func NewAnnotationStore(db *sqlite.DB) (*AnnotationStore, error) {
	return NewAnnotationStoreWithContext(context.Background(), db)
}

// NewAnnotationStoreWithContext ensures the app SQLite schema is migrated to v3
// and loads the in-memory annotation snapshot using ctx.
func NewAnnotationStoreWithContext(ctx context.Context, db *sqlite.DB) (*AnnotationStore, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if db == nil {
		return nil, fmt.Errorf("nil sqlite database")
	}
	if _, err := Migrate(ctx, db, DBKindApp, MigrationOptions{AllowCreate: true}); err != nil {
		return nil, fmt.Errorf("init annotation schema: %w", err)
	}
	s := &AnnotationStore{
		db:    db,
		items: make(map[annotationKey]EntityAnnotation),
	}
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	s.gen.Store(1)
	return s, nil
}

func (s *AnnotationStore) load(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `SELECT entity_kind, entity_key, custom_title, tags_json, is_favorite, notes, props_json, updated_at_ns FROM entity_annotations`)
	if err != nil {
		return fmt.Errorf("load entity_annotations: %w", err)
	}
	loaded := make(map[annotationKey]EntityAnnotation, len(rows.Values))
	for _, r := range rows.Values {
		if len(r) < 8 {
			continue
		}
		kind := strings.TrimSpace(str(r, 0))
		key := strings.TrimSpace(str(r, 1))
		if kind == "" || key == "" {
			continue
		}
		var tags []string
		if rawTags := strings.TrimSpace(str(r, 3)); rawTags != "" {
			_ = json.Unmarshal([]byte(rawTags), &tags)
			tags = taxonomy.NormalizeTags(tags)
		}
		var props map[string]string
		if rawProps := strings.TrimSpace(str(r, 6)); rawProps != "" && rawProps != "{}" {
			_ = json.Unmarshal([]byte(rawProps), &props)
			if len(props) == 0 {
				props = nil
			}
		}
		var updatedAt time.Time
		if ns := int64val(r, 7); ns > 0 {
			updatedAt = time.Unix(0, ns).UTC()
		}
		ann := EntityAnnotation{
			EntityKind:  kind,
			EntityKey:   key,
			CustomTitle: strings.TrimSpace(str(r, 2)),
			Tags:        tags,
			Favorite:    int64val(r, 4) != 0,
			Notes:       strings.TrimSpace(str(r, 5)),
			Props:       props,
			UpdatedAt:   updatedAt,
		}
		loaded[annotationKey{kind: kind, key: key}] = ann
	}
	s.mu.Lock()
	s.items = loaded
	s.mu.Unlock()
	return nil
}

// Gen returns the monotonic mutation counter for UI cache invalidation.
func (s *AnnotationStore) Gen() uint64 {
	if s == nil {
		return 0
	}
	return s.gen.Load()
}

// Get returns a cloned annotation from the in-memory snapshot without SQLite I/O.
func (s *AnnotationStore) Get(kind, key string) (EntityAnnotation, bool) {
	if s == nil {
		return EntityAnnotation{}, false
	}
	k := annotationKey{kind: strings.TrimSpace(kind), key: strings.TrimSpace(key)}
	if k.kind == "" || k.key == "" {
		return EntityAnnotation{}, false
	}
	s.mu.RLock()
	ann, ok := s.items[k]
	s.mu.RUnlock()
	if !ok {
		return EntityAnnotation{}, false
	}
	return cloneAnnotation(ann), true
}

// Upsert validates, normalizes, and persists ann to SQLite and the in-memory snapshot.
func (s *AnnotationStore) Upsert(ctx context.Context, ann EntityAnnotation) (EntityAnnotation, error) {
	if s == nil || s.db == nil {
		return EntityAnnotation{}, fmt.Errorf("annotation store is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return EntityAnnotation{}, err
	}
	kind := strings.TrimSpace(ann.EntityKind)
	key := strings.TrimSpace(ann.EntityKey)
	if kind == "" || key == "" {
		return EntityAnnotation{}, fmt.Errorf("entity_kind and entity_key are required")
	}

	tags, err := validateAndNormalizeTags(ann.Tags)
	if err != nil {
		return EntityAnnotation{}, err
	}
	props, err := validateAndNormalizeProps(ann.Props)
	if err != nil {
		return EntityAnnotation{}, err
	}

	clean := EntityAnnotation{
		EntityKind:  kind,
		EntityKey:   key,
		CustomTitle: strings.TrimSpace(ann.CustomTitle),
		Tags:        tags,
		Favorite:    ann.Favorite,
		Notes:       strings.TrimSpace(ann.Notes),
		Props:       props,
		UpdatedAt:   time.Now().UTC(),
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := s.upsertLocked(ctx, clean); err != nil {
		return EntityAnnotation{}, err
	}
	s.mu.Lock()
	s.items[annotationKey{kind: kind, key: key}] = cloneAnnotation(clean)
	s.mu.Unlock()
	s.gen.Add(1)
	return cloneAnnotation(clean), nil
}

// SetFavorite updates the favorite flag for (kind, key), creating or pruning the
// row as needed.
func (s *AnnotationStore) SetFavorite(ctx context.Context, kind, key string, fav bool) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("annotation store is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	kind = strings.TrimSpace(kind)
	key = strings.TrimSpace(key)
	if kind == "" || key == "" {
		return fmt.Errorf("entity_kind and entity_key are required")
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	k := annotationKey{kind: kind, key: key}
	s.mu.RLock()
	cur, exists := s.items[k]
	s.mu.RUnlock()

	if !exists {
		if !fav {
			return nil
		}
		cur = EntityAnnotation{EntityKind: kind, EntityKey: key}
	} else {
		cur = cloneAnnotation(cur)
	}
	cur.Favorite = fav
	cur.UpdatedAt = time.Now().UTC()

	if isAnnotationEmpty(cur) {
		if err := s.deleteLocked(ctx, kind, key); err != nil {
			return err
		}
		s.mu.Lock()
		delete(s.items, k)
		s.mu.Unlock()
		s.gen.Add(1)
		return nil
	}

	if err := s.upsertLocked(ctx, cur); err != nil {
		return err
	}
	s.mu.Lock()
	s.items[k] = cloneAnnotation(cur)
	s.mu.Unlock()
	s.gen.Add(1)
	return nil
}

// SetTags validates and normalizes tags under D4 fail-closed rules before
// updating (kind, key).
func (s *AnnotationStore) SetTags(ctx context.Context, kind, key string, tags []string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("annotation store is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	kind = strings.TrimSpace(kind)
	key = strings.TrimSpace(key)
	if kind == "" || key == "" {
		return fmt.Errorf("entity_kind and entity_key are required")
	}

	cleanTags, err := validateAndNormalizeTags(tags)
	if err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	k := annotationKey{kind: kind, key: key}
	s.mu.RLock()
	cur, exists := s.items[k]
	s.mu.RUnlock()

	if !exists {
		if len(cleanTags) == 0 {
			return nil
		}
		cur = EntityAnnotation{EntityKind: kind, EntityKey: key}
	} else {
		cur = cloneAnnotation(cur)
	}
	cur.Tags = cleanTags
	cur.UpdatedAt = time.Now().UTC()

	if isAnnotationEmpty(cur) {
		if err := s.deleteLocked(ctx, kind, key); err != nil {
			return err
		}
		s.mu.Lock()
		delete(s.items, k)
		s.mu.Unlock()
		s.gen.Add(1)
		return nil
	}

	if err := s.upsertLocked(ctx, cur); err != nil {
		return err
	}
	s.mu.Lock()
	s.items[k] = cloneAnnotation(cur)
	s.mu.Unlock()
	s.gen.Add(1)
	return nil
}

// ListByKind returns cloned annotations matching kind (or all when kind == ""),
// sorted by Favorite DESC, UpdatedAt DESC, EntityKind ASC, EntityKey ASC.
func (s *AnnotationStore) ListByKind(kind string) []EntityAnnotation {
	if s == nil {
		return nil
	}
	kind = strings.TrimSpace(kind)
	s.mu.RLock()
	out := make([]EntityAnnotation, 0, len(s.items))
	for _, ann := range s.items {
		if kind != "" && ann.EntityKind != kind {
			continue
		}
		out = append(out, cloneAnnotation(ann))
	}
	s.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Favorite != out[j].Favorite {
			return out[i].Favorite
		}
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		if out[i].EntityKind != out[j].EntityKind {
			return out[i].EntityKind < out[j].EntityKind
		}
		return out[i].EntityKey < out[j].EntityKey
	})
	return out
}

// TagCounts aggregates normalized tag frequencies across kind (or all when
// kind == ""), sorted by Count DESC, Tag ASC.
func (s *AnnotationStore) TagCounts(kind string) []taxonomy.TagCount {
	if s == nil {
		return nil
	}
	kind = strings.TrimSpace(kind)
	counts := make(map[string]int)
	s.mu.RLock()
	for _, ann := range s.items {
		if kind != "" && ann.EntityKind != kind {
			continue
		}
		for _, tg := range ann.Tags {
			if tg != "" {
				counts[tg]++
			}
		}
	}
	s.mu.RUnlock()
	if len(counts) == 0 {
		return nil
	}
	out := make([]taxonomy.TagCount, 0, len(counts))
	for tg, cnt := range counts {
		out = append(out, taxonomy.TagCount{Tag: tg, Count: cnt})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Tag < out[j].Tag
	})
	return out
}

// Delete removes any annotation for (kind, key) from SQLite and the snapshot.
func (s *AnnotationStore) Delete(ctx context.Context, kind, key string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("annotation store is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	kind = strings.TrimSpace(kind)
	key = strings.TrimSpace(key)
	if kind == "" || key == "" {
		return fmt.Errorf("entity_kind and entity_key are required")
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := s.deleteLocked(ctx, kind, key); err != nil {
		return err
	}
	k := annotationKey{kind: kind, key: key}
	s.mu.Lock()
	_, existed := s.items[k]
	delete(s.items, k)
	s.mu.Unlock()
	if existed {
		s.gen.Add(1)
	}
	return nil
}

func (s *AnnotationStore) upsertLocked(ctx context.Context, ann EntityAnnotation) error {
	tagsData := []byte("[]")
	if len(ann.Tags) > 0 {
		b, err := json.Marshal(ann.Tags)
		if err != nil {
			return fmt.Errorf("marshal annotation tags: %w", err)
		}
		tagsData = b
	}
	propsData := []byte("{}")
	if len(ann.Props) > 0 {
		b, err := json.Marshal(ann.Props)
		if err != nil {
			return fmt.Errorf("marshal annotation props: %w", err)
		}
		propsData = b
	}
	_, err := s.db.Execute(ctx, `INSERT INTO entity_annotations (
		entity_kind, entity_key, custom_title, tags_json, is_favorite, notes, props_json, updated_at_ns
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(entity_kind, entity_key) DO UPDATE SET
		custom_title = excluded.custom_title,
		tags_json = excluded.tags_json,
		is_favorite = excluded.is_favorite,
		notes = excluded.notes,
		props_json = excluded.props_json,
		updated_at_ns = excluded.updated_at_ns`,
		ann.EntityKind,
		ann.EntityKey,
		ann.CustomTitle,
		string(tagsData),
		boolInt(ann.Favorite),
		ann.Notes,
		string(propsData),
		ann.UpdatedAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("upsert entity_annotation: %w", err)
	}
	return nil
}

func (s *AnnotationStore) deleteLocked(ctx context.Context, kind, key string) error {
	if _, err := s.db.Execute(ctx, `DELETE FROM entity_annotations WHERE entity_kind=? AND entity_key=?`, kind, key); err != nil {
		return fmt.Errorf("delete entity_annotation: %w", err)
	}
	return nil
}

func validateAndNormalizeTags(raw []string) ([]string, error) {
	for _, item := range raw {
		if err := taxonomy.ValidateSafeMetadataTag(item); err != nil {
			return nil, err
		}
	}
	norm := taxonomy.NormalizeTags(raw)
	for _, item := range norm {
		if err := taxonomy.ValidateSafeMetadataTag(item); err != nil {
			return nil, err
		}
	}
	return norm, nil
}

func validateAndNormalizeProps(raw map[string]string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(raw))
	cleaned := make(map[string]string, len(raw))
	for k, v := range raw {
		ck := strings.TrimSpace(k)
		if ck == "" {
			return nil, fmt.Errorf("annotation prop key cannot be empty")
		}
		cv := strings.TrimSpace(v)
		if err := taxonomy.ValidateSafeMetadataTag(ck); err != nil {
			return nil, err
		}
		if err := taxonomy.ValidateSafeMetadataTag(cv); err != nil {
			return nil, err
		}
		if _, exists := cleaned[ck]; !exists {
			keys = append(keys, ck)
		}
		cleaned[ck] = cv
	}
	sort.Strings(keys)
	if len(keys) > MaxAnnotationProps {
		keys = keys[:MaxAnnotationProps]
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = cleaned[k]
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func isAnnotationEmpty(ann EntityAnnotation) bool {
	return !ann.Favorite &&
		ann.CustomTitle == "" &&
		len(ann.Tags) == 0 &&
		ann.Notes == "" &&
		len(ann.Props) == 0
}

func cloneAnnotation(ann EntityAnnotation) EntityAnnotation {
	cp := ann
	if len(ann.Tags) > 0 {
		cp.Tags = append([]string(nil), ann.Tags...)
	} else {
		cp.Tags = nil
	}
	if len(ann.Props) > 0 {
		cp.Props = make(map[string]string, len(ann.Props))
		maps.Copy(cp.Props, ann.Props)
	} else {
		cp.Props = nil
	}
	return cp
}
