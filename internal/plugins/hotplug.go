package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func newRuntime(owner *Supervisor, manifest Manifest, crashLimit int) *runtime {
	return &runtime{manifest: manifest, available: true, registry: owner.registry, registered: make(map[string]struct{}), crashLimit: crashLimit, owner: owner}
}

func manifestSignature(m Manifest) string {
	info, err := os.Stat(filepath.Join(m.Dir, "plugin.toml"))
	if err != nil {
		return "missing"
	}
	contents, readErr := os.ReadFile(filepath.Join(m.Dir, "plugin.toml"))
	if readErr != nil {
		return "missing"
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%d|%s|%s|%s", filepath.Clean(m.Dir), info.Size(), info.ModTime().UnixNano(), m.Entrypoint, strings.Join(m.Args, "\x00"), contents)
	return hex.EncodeToString(h.Sum(nil))
}

// Refresh rescans plugin/ subdirectories and atomically replaces added,
// removed, or changed plugin runtimes. A changed runtime is stopped before its
// replacement is installed, so requests cannot leak into the old process.
func (s *Supervisor) Refresh(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	manifests, err := scanManifests(s.directory())
	if err != nil {
		return err
	}
	next := make(map[string]Manifest, len(manifests))
	for _, m := range manifests {
		if _, ok := next[m.ID]; ok {
			return fmt.Errorf("duplicate plugin id %q", m.ID)
		}
		next[m.ID] = m
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrPluginClosed
	}
	for id, old := range s.plugins {
		m, exists := next[id]
		if !exists || manifestSignature(m) != s.manifestState[id] {
			stopRuntime(old)
			delete(s.plugins, id)
			delete(s.manifestState, id)
		}
	}
	filteredOrder := s.order[:0]
	for _, id := range s.order {
		if _, exists := s.plugins[id]; exists {
			filteredOrder = append(filteredOrder, id)
		}
	}
	s.order = filteredOrder
	for _, m := range manifests {
		if _, exists := s.plugins[m.ID]; exists {
			continue
		}
		r := newRuntime(s, m, s.crashLimit)
		s.plugins[m.ID] = r
		s.manifestState[m.ID] = manifestSignature(m)
		s.order = append(s.order, m.ID)
		if err := s.registry.RegisterPlugin(Metadata{ID: m.ID, Name: m.Name, Version: m.Version, Description: m.Description, Capabilities: append([]string(nil), m.Capabilities...), StyleFields: append([]string(nil), m.StyleFields...), Permissions: m.Permissions, ManifestPath: filepath.Join(m.Dir, "plugin.toml")}); err != nil {
			return err
		}
		for _, capability := range m.Capabilities {
			if err := s.registry.RegisterCapability(m.ID, Capability{Name: capability}); err != nil {
				return err
			}
			r.registered[capability] = struct{}{}
		}
	}
	return nil
}

func (s *Supervisor) directory() string { return s.directoryPath }

func stopRuntime(r *runtime) {
	r.mu.Lock()
	r.stopping = true
	p, done := r.process, r.done
	r.mu.Unlock()
	if p == nil {
		return
	}
	_ = p.Kill()
	if done != nil {
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}
}

// Watch refreshes plugin discovery until ctx is canceled. It intentionally
// watches only the configured plugin directory, not the global config tree.
func (s *Supervisor) Watch(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.Refresh(ctx); err != nil && err != ErrPluginClosed {
				return err
			}
		}
	}
}
