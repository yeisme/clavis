package apps

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestMyGoAppProvider_SafeBeforeAppIsReady(t *testing.T) {
	prov := NewMyGoAppProvider()

	// Without App.Run(), mygo.App.IsReady() is false; ListApps and IconPNG must return (nil, nil) safely.
	apps, err := prov.ListApps(context.Background())
	if err != nil || len(apps) != 0 {
		t.Fatalf("expected (nil, nil) before App.IsReady(), got apps=%+v err=%v", apps, err)
	}
	icon, err := prov.IconPNG("/Applications/Safari.app", 64)
	if err != nil || len(icon) != 0 {
		t.Fatalf("expected (nil, nil) icon before App.IsReady(), got len=%d err=%v", len(icon), err)
	}
}

func TestMyGoAppProvider_CachingAndRetinaSizing(t *testing.T) {
	var listCalls atomic.Int32
	var iconCalls atomic.Int32
	var lastIconSize atomic.Int32

	prov := NewMyGoAppProvider()
	prov.IsReadyFn = func() bool { return true }
	prov.InstalledAppsFn = func() ([]mygo.InstalledApp, error) {
		listCalls.Add(1)
		return []mygo.InstalledApp{
			{Name: "Safari", ID: "com.apple.Safari", Path: "/Applications/Safari.app"},
			{Name: "Terminal", ID: "com.apple.Terminal", Path: "/System/Applications/Utilities/Terminal.app"},
			// Duplicate should be deduplicated
			{Name: "Safari", ID: "com.apple.Safari", Path: "/Applications/Safari.app"},
		}, nil
	}
	prov.FileIconFn = func(path string, sizePx int) ([]byte, error) {
		iconCalls.Add(1)
		lastIconSize.Store(int32(sizePx))
		return []byte("PNG:" + path), nil
	}

	// First ListApps call populates cache
	list1, err := prov.ListApps(context.Background())
	if err != nil || len(list1) != 2 {
		t.Fatalf("expected 2 deduplicated apps, got %+v (err=%v)", list1, err)
	}
	if listCalls.Load() != 1 {
		t.Fatalf("expected 1 InstalledApps call, got %d", listCalls.Load())
	}

	// Second ListApps call serves from cache without calling backend again
	list2, err := prov.ListApps(context.Background())
	if err != nil || len(list2) != 2 || listCalls.Load() != 1 {
		t.Fatalf("expected cached ListApps (calls=%d), got %+v", listCalls.Load(), list2)
	}

	// IconPNG with sizePx <= 0 defaults to DefaultIconSizePx (64px Retina) and caches result
	png1, err := prov.IconPNG("/Applications/Safari.app", 0)
	if err != nil || !bytes.Equal(png1, []byte("PNG:/Applications/Safari.app")) {
		t.Fatalf("unexpected IconPNG: %q (%v)", string(png1), err)
	}
	if lastIconSize.Load() != DefaultIconSizePx || iconCalls.Load() != 1 {
		t.Fatalf("expected size=%d calls=1, got size=%d calls=%d", DefaultIconSizePx, lastIconSize.Load(), iconCalls.Load())
	}

	// Second IconPNG call for same path and size hits cache
	png2, err := prov.IconPNG("/Applications/Safari.app", 64)
	if err != nil || !bytes.Equal(png1, png2) || iconCalls.Load() != 1 {
		t.Fatalf("expected cached IconPNG (calls=%d)", iconCalls.Load())
	}

	// Verify RetinaSizePx helper
	if got := RetinaSizePx(24, 2.0); got != 48 {
		t.Fatalf("RetinaSizePx(24, 2.0) = %d, want 48", got)
	}
	if got := RetinaSizePx(0, 0); got != 64 {
		t.Fatalf("RetinaSizePx(0, 0) = %d, want 64", got)
	}

	// Concurrent reads
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = prov.ListApps(context.Background())
			_, _ = prov.IconPNG("/Applications/Safari.app", 64)
		}()
	}
	wg.Wait()
	if listCalls.Load() != 1 || iconCalls.Load() != 1 {
		t.Fatalf("expected cache to hold under concurrency: listCalls=%d iconCalls=%d", listCalls.Load(), iconCalls.Load())
	}

	// InvalidateCache forces reload on next call
	prov.InvalidateCache()
	_, _ = prov.ListApps(context.Background())
	if listCalls.Load() != 2 {
		t.Fatalf("expected reload after InvalidateCache, listCalls=%d", listCalls.Load())
	}
}

func TestMockAppProvider(t *testing.T) {
	mock := NewMockAppProvider(
		engine.InstalledAppEntry{Name: "Notes", BundleID: "com.apple.Notes", Path: "/System/Applications/Notes.app"},
	)
	mock.SetIcon("/System/Applications/Notes.app", []byte{0x89, 'P', 'N', 'G'})

	apps, err := mock.ListApps(context.Background())
	if err != nil || len(apps) != 1 || apps[0].Name != "Notes" {
		t.Fatalf("unexpected MockAppProvider.ListApps: %+v (%v)", apps, err)
	}
	icon, err := mock.IconPNG("/System/Applications/Notes.app", 64)
	if err != nil || !bytes.Equal(icon, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("unexpected MockAppProvider.IconPNG: %v (%v)", icon, err)
	}
	if mock.ListCalls != 1 || mock.IconCalls != 1 {
		t.Fatalf("unexpected call counts: list=%d icon=%d", mock.ListCalls, mock.IconCalls)
	}
}
