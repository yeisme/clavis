package apps

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// DefaultIconSizePx is the default Retina icon pixel dimension (32pt @ 2x = 64px).
const DefaultIconSizePx = 64

// AppProvider discovers installed applications and loads/caches their PNG icons.
type AppProvider interface {
	ListApps(ctx context.Context) ([]engine.InstalledAppEntry, error)
	IconPNG(path string, sizePx int) ([]byte, error)
}

// RetinaSizePx computes the physical Retina pixel size from a logical DIP size and scale factor.
// Defaults to 32 DIPs at 2.0x (64px) when dipSize <= 0 or scale <= 0.
func RetinaSizePx(dipSize int, scale float64) int {
	if dipSize <= 0 {
		dipSize = 32
	}
	if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		scale = 2.0
	}
	px := int(math.Round(float64(dipSize) * scale))
	if px <= 0 {
		return DefaultIconSizePx
	}
	return px
}

func normalizeIconSizePx(sizePx int) int {
	if sizePx <= 0 {
		return DefaultIconSizePx
	}
	return sizePx
}

// MyGoAppProvider wraps mygo.Shell.InstalledApps() and mygo.Shell.FileIcon(path, sizePx)
// with thread-safe in-memory caching for the application list and PNG icons.
type MyGoAppProvider struct {
	mu         sync.RWMutex
	appsLoaded bool
	appsCache  []engine.InstalledAppEntry
	iconCache  map[string][]byte

	// Optional overrides for unit testing; when nil, uses mygo.App.IsReady() and mygo.Shell.
	IsReadyFn       func() bool
	InstalledAppsFn func() ([]mygo.InstalledApp, error)
	FileIconFn      func(path string, sizePx int) ([]byte, error)
}

var _ AppProvider = (*MyGoAppProvider)(nil)

// NewMyGoAppProvider constructs a MyGoAppProvider with an empty in-memory cache.
func NewMyGoAppProvider() *MyGoAppProvider {
	return &MyGoAppProvider{
		iconCache: make(map[string][]byte),
	}
}

// InvalidateCache clears both the cached application list and icon PNG cache.
func (p *MyGoAppProvider) InvalidateCache() {
	p.mu.Lock()
	p.appsLoaded = false
	p.appsCache = nil
	clear(p.iconCache)
	p.mu.Unlock()
}

func (p *MyGoAppProvider) isReadyForApps() bool {
	if p.IsReadyFn != nil {
		return p.IsReadyFn()
	}
	if p.InstalledAppsFn != nil {
		return true
	}
	return mygo.App.IsReady()
}

func (p *MyGoAppProvider) isReadyForIcons() bool {
	if p.IsReadyFn != nil {
		return p.IsReadyFn()
	}
	if p.FileIconFn != nil {
		return true
	}
	return mygo.App.IsReady()
}

// ListApps returns discovered installed applications, caching the list after the first successful load.
// If mygo.App.IsReady() is false (and no test override is set), it safely returns (nil, nil).
func (p *MyGoAppProvider) ListApps(ctx context.Context) ([]engine.InstalledAppEntry, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	p.mu.RLock()
	if p.appsLoaded {
		copied := make([]engine.InstalledAppEntry, len(p.appsCache))
		copy(copied, p.appsCache)
		p.mu.RUnlock()
		return copied, nil
	}
	p.mu.RUnlock()

	if !p.isReadyForApps() {
		return nil, nil
	}

	listFn := p.InstalledAppsFn
	if listFn == nil {
		listFn = mygo.Shell.InstalledApps
	}
	rawApps, err := listFn()
	if err != nil {
		return nil, err
	}

	entries := make([]engine.InstalledAppEntry, 0, len(rawApps))
	seen := make(map[string]struct{}, len(rawApps))
	for _, a := range rawApps {
		name := strings.TrimSpace(a.Name)
		id := strings.TrimSpace(a.ID)
		path := strings.TrimSpace(a.Path)
		if name == "" && path != "" {
			base := filepath.Base(path)
			name = strings.TrimSuffix(base, filepath.Ext(base))
		}
		if name == "" && id == "" && path == "" {
			continue
		}
		dedupKey := strings.ToLower(id + "\x00" + path + "\x00" + name)
		if _, dup := seen[dedupKey]; dup {
			continue
		}
		seen[dedupKey] = struct{}{}
		entries = append(entries, engine.InstalledAppEntry{
			Name:     name,
			BundleID: id,
			Path:     path,
		})
	}

	p.mu.Lock()
	p.appsCache = entries
	p.appsLoaded = true
	copied := make([]engine.InstalledAppEntry, len(p.appsCache))
	copy(copied, p.appsCache)
	p.mu.Unlock()

	return copied, nil
}

// IconPNG returns the PNG icon bytes for path at sizePx (defaulting to 64px Retina size when sizePx <= 0).
// Results are cached in memory by (path, sizePx). Returns (nil, nil) when called before mygo.App.IsReady().
func (p *MyGoAppProvider) IconPNG(path string, sizePx int) ([]byte, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, nil
	}
	effSize := normalizeIconSizePx(sizePx)
	cacheKey := fmt.Sprintf("%s#%d", trimmed, effSize)

	p.mu.RLock()
	if data, ok := p.iconCache[cacheKey]; ok {
		copied := make([]byte, len(data))
		copy(copied, data)
		p.mu.RUnlock()
		return copied, nil
	}
	p.mu.RUnlock()

	if !p.isReadyForIcons() {
		return nil, nil
	}

	iconFn := p.FileIconFn
	if iconFn == nil {
		iconFn = mygo.Shell.FileIcon
	}
	pngBytes, err := iconFn(trimmed, effSize)
	if err != nil {
		return nil, err
	}

	if len(pngBytes) > 0 {
		p.mu.Lock()
		if p.iconCache == nil {
			p.iconCache = make(map[string][]byte)
		}
		stored := make([]byte, len(pngBytes))
		copy(stored, pngBytes)
		p.iconCache[cacheKey] = stored
		p.mu.Unlock()
	}

	out := make([]byte, len(pngBytes))
	copy(out, pngBytes)
	return out, nil
}

// MockAppProvider is a thread-safe in-memory AppProvider for unit tests.
type MockAppProvider struct {
	mu        sync.RWMutex
	Apps      []engine.InstalledAppEntry
	Icons     map[string][]byte
	ListErr   error
	IconErr   error
	ListCalls int
	IconCalls int
}

var _ AppProvider = (*MockAppProvider)(nil)

// NewMockAppProvider constructs a MockAppProvider seeded with initial apps.
func NewMockAppProvider(initialApps ...engine.InstalledAppEntry) *MockAppProvider {
	return &MockAppProvider{
		Apps:  append([]engine.InstalledAppEntry(nil), initialApps...),
		Icons: make(map[string][]byte),
	}
}

// SetIcon registers mock PNG icon bytes for path.
func (m *MockAppProvider) SetIcon(path string, pngData []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Icons == nil {
		m.Icons = make(map[string][]byte)
	}
	copied := make([]byte, len(pngData))
	copy(copied, pngData)
	m.Icons[strings.TrimSpace(path)] = copied
}

// ListApps returns a copy of the configured mock applications.
func (m *MockAppProvider) ListApps(ctx context.Context) ([]engine.InstalledAppEntry, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ListCalls++
	if m.ListErr != nil {
		return nil, m.ListErr
	}
	out := make([]engine.InstalledAppEntry, len(m.Apps))
	copy(out, m.Apps)
	return out, nil
}

// IconPNG returns the mock PNG icon bytes for path.
func (m *MockAppProvider) IconPNG(path string, sizePx int) ([]byte, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, nil
	}
	effSize := normalizeIconSizePx(sizePx)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.IconCalls++
	if m.IconErr != nil {
		return nil, m.IconErr
	}
	if data, ok := m.Icons[fmt.Sprintf("%s#%d", trimmed, effSize)]; ok {
		return append([]byte(nil), data...), nil
	}
	if data, ok := m.Icons[trimmed]; ok {
		return append([]byte(nil), data...), nil
	}
	return nil, nil
}
