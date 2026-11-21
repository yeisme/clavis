package uiapp

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// BenchmarkClipboardSwitch measures one selection step in the clipboard tab:
// dispatching ↓ or ↑ and rebuilding the frame, the path a user feels as lag
// when arrowing or sweeping the pointer through history.
//
//	go test -run '^$' -bench BenchmarkClipboardSwitch -benchmem ./uiapp/
//
// "typical" is 60 short entries (the tab loads the newest 50). "large" adds a 256 KiB log and a 64 KiB
// source file among the visible rows — ordinary things to have copied.
func BenchmarkClipboardSwitch(b *testing.B) {
	for _, tc := range []struct {
		name  string
		large bool
	}{{"typical", false}, {"large", true}} {
		b.Run(tc.name, func(b *testing.B) {
			ctrl, store := benchClipboardController(b)
			ctx := context.Background()
			now := time.Now().Add(-time.Hour)
			record := func(i int, content string) {
				if _, _, err := store.Record(ctx, clipboard.CaptureCandidate{Content: content, SourceAppName: "Notes", Timestamp: now.Add(time.Duration(i) * time.Second)}); err != nil {
					b.Fatal(err)
				}
			}
			for i := 0; i < 60; i++ {
				record(i, fmt.Sprintf("clipboard entry %d: https://example.com/path/%d", i, i))
			}
			if tc.large {
				record(61, strings.Repeat("2026-11-21T10:00:00Z INFO request served path=/api/v1/items status=200 latency=12ms\n", 3000))
				record(62, strings.Repeat("func handler(w http.ResponseWriter, r *http.Request) {\n\tfmt.Fprintln(w, \"ok\")\n}\n", 900))
			}
			ctrl.OpenOmnibox(config.TabClipboard, "")
			deadline := time.Now().Add(2 * time.Second)
			for len(ctrl.State().OmniboxItems) < 50 && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if len(ctrl.State().OmniboxItems) < 50 {
				b.Fatalf("clipboard items not loaded: %d", len(ctrl.State().OmniboxItems))
			}
			w, h := PreferredCapsuleSizeForState(ctrl.State(), ctrl.Config().Main.Appearance)
			tt := ui.NewTester(ctrl.CapsuleView, w, h)
			tt.Frame()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ev := engine.EventSelectNext
				if i%4 >= 2 { // ↓↓↑↑: stay among the first rows, where the large entries are
					ev = engine.EventSelectPrevious
				}
				ctrl.Dispatch(engine.Event{Type: ev})
				tt.Frame()
			}
		})
	}
}

func benchClipboardController(b *testing.B) (*AppController, clipboard.Store) {
	b.Helper()
	cfgStore := config.NewConfigStore(b.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		b.Fatal(err)
	}
	store := clipboard.NewMemoryStore(clipboard.NewPrivacyFilter())
	ctrl := NewAppController(Options{
		ConfigStore:    cfgStore,
		ClipboardStore: store,
		SecretStore:    secrets.NewMemoryAPIKeyStore(),
		Bridge:         newMockBridge(),
	})
	b.Cleanup(ctrl.Close)
	return ctrl, store
}
