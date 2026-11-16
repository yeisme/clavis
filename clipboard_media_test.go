package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 12, 7))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestEnrichMediaCandidate(t *testing.T) {
	cfg := config.DefaultClipboardConfig()
	data := testPNG(t)
	reads := 0
	src := mediaSource{ReadPNG: func() ([]byte, error) { reads++; return data, nil }}

	t.Run("text passes through untouched", func(t *testing.T) {
		c := &clipboard.CaptureCandidate{Content: "x"}
		if keep, err := enrichMediaCandidate(c, cfg, nil, src); !keep || err != nil {
			t.Fatal(keep, err)
		}
	})
	t.Run("image saved", func(t *testing.T) {
		ms := clipboard.NewMediaStore(t.TempDir())
		c := &clipboard.CaptureCandidate{Media: clipboard.Media{Kind: clipboard.KindImage}}
		keep, err := enrichMediaCandidate(c, cfg, ms, src)
		if !keep || err != nil || c.Width != 12 || c.Height != 7 || c.BlobSHA == "" {
			t.Fatalf("%v %v %+v", keep, err, c.Media)
		}
		if _, err := os.Stat(c.MediaPath); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("privacy filter runs before any read", func(t *testing.T) {
		reads = 0
		ms := clipboard.NewMediaStore(t.TempDir())
		c := &clipboard.CaptureCandidate{SourceAppName: "KeePass.exe", Media: clipboard.Media{Kind: clipboard.KindImage}}
		if keep, _ := enrichMediaCandidate(c, cfg, ms, src); keep || reads != 0 {
			t.Fatalf("keep=%v reads=%d", keep, reads)
		}
		c = &clipboard.CaptureCandidate{PasteboardTypes: []string{"ExcludeClipboardContentFromMonitorProcessing"}, Media: clipboard.Media{Kind: clipboard.KindImage}}
		if keep, _ := enrichMediaCandidate(c, cfg, ms, src); keep || reads != 0 {
			t.Fatalf("marker: keep=%v reads=%d", keep, reads)
		}
		if entries, _ := os.ReadDir(ms.Dir); len(entries) != 0 {
			t.Fatalf("blob written for filtered image: %v", entries)
		}
	})
	t.Run("capture disabled", func(t *testing.T) {
		off := cfg
		off.MediaCapture = false
		c := &clipboard.CaptureCandidate{Media: clipboard.Media{Kind: clipboard.KindImage}}
		if keep, _ := enrichMediaCandidate(c, off, clipboard.NewMediaStore(t.TempDir()), src); keep {
			t.Fatal("kept with media_capture=false")
		}
	})
	t.Run("clipboard changed mid-read", func(t *testing.T) {
		seq := int64(1)
		s := mediaSource{ReadPNG: func() ([]byte, error) { seq++; return data, nil }, Sequence: func() int64 { return seq }, ObservedSequence: 1}
		c := &clipboard.CaptureCandidate{Media: clipboard.Media{Kind: clipboard.KindImage}}
		if keep, err := enrichMediaCandidate(c, cfg, clipboard.NewMediaStore(t.TempDir()), s); keep || !errors.Is(err, errClipboardChanged) {
			t.Fatal(keep, err)
		}
	})
	t.Run("undecodable payload dropped", func(t *testing.T) {
		s := mediaSource{ReadPNG: func() ([]byte, error) { return []byte("junk"), nil }}
		c := &clipboard.CaptureCandidate{Media: clipboard.Media{Kind: clipboard.KindImage}}
		if keep, err := enrichMediaCandidate(c, cfg, clipboard.NewMediaStore(t.TempDir()), s); keep || err == nil {
			t.Fatal(keep, err)
		}
	})
}

func TestMaintainClipboardMediaQuotaAndGC(t *testing.T) {
	ctx := context.Background()
	ms := clipboard.NewMediaStore(t.TempDir())
	store := clipboard.NewMemoryStore(clipboard.NewPrivacyFilter())
	m1, err := ms.SaveImage(testPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Record(ctx, clipboard.CaptureCandidate{Media: m1}); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultClipboardConfig()
	maintainClipboardMedia(ctx, store, ms, cfg)
	if _, err := os.Stat(m1.MediaPath); err != nil {
		t.Fatal("referenced blob removed under quota")
	}
	// Quota of 1 byte (below one image) → the unpinned image is evicted and its blob GC'd.
	cfg.MediaQuotaMB = 0
	items, _ := store.Recent(ctx, 0)
	if got := clipboard.QuotaEvictions(items, 1); len(got) != 1 {
		t.Fatalf("expected one eviction candidate, got %v", got)
	}
	_ = store.Delete(ctx, items[0].ID)
	maintainClipboardMedia(ctx, store, ms, cfg)
	if _, err := os.Stat(m1.MediaPath); err == nil {
		t.Fatal("orphan blob not GC'd")
	}
}

func TestEnrichMediaCandidateClipboardSequence(t *testing.T) {
	cfg := config.DefaultClipboardConfig()

	t.Run("stable image reads are saved", func(t *testing.T) {
		ms := clipboard.NewMediaStore(t.TempDir())
		seq := int64(11)
		sequenceReads := 0
		src := mediaSource{
			ReadPNG: func() ([]byte, error) { return testPNG(t), nil },
			Sequence: func() int64 {
				sequenceReads++
				return seq
			},
			ObservedSequence: 11,
		}
		c := &clipboard.CaptureCandidate{Media: clipboard.Media{Kind: clipboard.KindImage}}
		keep, err := enrichMediaCandidate(c, cfg, ms, src)
		if !keep || err != nil || c.BlobSHA == "" {
			t.Fatalf("keep=%v err=%v media=%+v", keep, err, c.Media)
		}
		if sequenceReads != 2 {
			t.Fatalf("sequence reads=%d, want before and after reads", sequenceReads)
		}
	})

	t.Run("image changes during read are discarded before saving", func(t *testing.T) {
		ms := clipboard.NewMediaStore(t.TempDir())
		seq := int64(21)
		src := mediaSource{
			ReadPNG: func() ([]byte, error) {
				seq++
				return testPNG(t), nil
			},
			Sequence:         func() int64 { return seq },
			ObservedSequence: 21,
		}
		c := &clipboard.CaptureCandidate{Media: clipboard.Media{Kind: clipboard.KindImage}}
		keep, err := enrichMediaCandidate(c, cfg, ms, src)
		if keep || !errors.Is(err, errClipboardChanged) {
			t.Fatalf("keep=%v err=%v, want changed clipboard discarded", keep, err)
		}
		if entries, readErr := os.ReadDir(ms.Dir); readErr != nil || len(entries) != 0 {
			t.Fatalf("media written after image changed: entries=%v err=%v", entries, readErr)
		}
	})

	t.Run("stable file reads are saved", func(t *testing.T) {
		ms := clipboard.NewMediaStore(t.TempDir())
		seq := int64(31)
		sequenceReads := 0
		src := mediaSource{
			ReadFiles: func() ([]string, error) { return []string{`C:\\clip.txt`}, nil },
			Sequence: func() int64 {
				sequenceReads++
				return seq
			},
			ObservedSequence: 31,
			Probe:            clipboard.FileProbe{Size: func(string) (int64, bool) { return 1, true }},
		}
		c := &clipboard.CaptureCandidate{Media: clipboard.Media{Kind: clipboard.KindFile}}
		keep, err := enrichMediaCandidate(c, cfg, ms, src)
		if !keep || err != nil || c.MediaPath == "" || c.Content != "clip.txt" {
			t.Fatalf("keep=%v err=%v media=%+v content=%q", keep, err, c.Media, c.Content)
		}
		if sequenceReads != 2 {
			t.Fatalf("sequence reads=%d, want before and after reads", sequenceReads)
		}
	})

	t.Run("file list changes during read are discarded before saving", func(t *testing.T) {
		ms := clipboard.NewMediaStore(t.TempDir())
		seq := int64(41)
		probeCalled := false
		src := mediaSource{
			ReadFiles: func() ([]string, error) {
				seq++
				return []string{`C:\\clip.txt`}, nil
			},
			Sequence:         func() int64 { return seq },
			ObservedSequence: 41,
			Probe: clipboard.FileProbe{Size: func(string) (int64, bool) {
				probeCalled = true
				return 1, true
			}},
		}
		c := &clipboard.CaptureCandidate{Media: clipboard.Media{Kind: clipboard.KindFile}}
		keep, err := enrichMediaCandidate(c, cfg, ms, src)
		if keep || !errors.Is(err, errClipboardChanged) {
			t.Fatalf("keep=%v err=%v, want changed clipboard discarded", keep, err)
		}
		if probeCalled {
			t.Fatal("file metadata read after clipboard changed")
		}
	})
}

func TestEnrichMediaCandidateFiles(t *testing.T) {
	cfg := config.DefaultClipboardConfig()
	ms := clipboard.NewMediaStore(t.TempDir())
	src := mediaSource{
		ReadFiles: func() ([]string, error) { return []string{`C:\Videos\clip.mp4`}, nil },
		Probe:     clipboard.FileProbe{DurationMs: func(string) int64 { return 5000 }},
	}
	c := &clipboard.CaptureCandidate{Media: clipboard.Media{Kind: clipboard.KindFile}}
	keep, err := enrichMediaCandidate(c, cfg, ms, src)
	if !keep || err != nil || c.Kind != clipboard.KindVideo || c.DurationMs != 5000 || c.Content != "clip.mp4" {
		t.Fatalf("%v %v %+v %q", keep, err, c.Media, c.Content)
	}
	// Files from a password manager are filtered before ReadFiles runs.
	called := false
	src.ReadFiles = func() ([]string, error) { called = true; return nil, nil }
	c = &clipboard.CaptureCandidate{SourceAppName: "1Password.exe", Media: clipboard.Media{Kind: clipboard.KindFile}}
	if keep, _ := enrichMediaCandidate(c, cfg, ms, src); keep || called {
		t.Fatal("filtered file list was read")
	}
}
