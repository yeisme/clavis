package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// Frame benchmarks for the transient surfaces: one forced rebuild of the
// Capsule view per iteration. They keep the cost of visual polish (keycaps,
// tiles, contrast-derived colors) visible next to the 16ms frame budget:
//
//	go test -run '^$' -bench BenchmarkFrame ./uiapp/
func benchController(b *testing.B) *AppController {
	b.Helper()
	cfgStore := config.NewConfigStore(b.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		b.Fatal(err)
	}
	ctrl := NewAppController(Options{
		ConfigStore:    cfgStore,
		ClipboardStore: clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		SecretStore:    secrets.NewMemoryAPIKeyStore(),
		Bridge:         newMockBridge(),
	})
	b.Cleanup(ctrl.Close)
	return ctrl
}

func benchFrames(b *testing.B, ctrl *AppController) {
	w, h := PreferredCapsuleSizeForState(ctrl.State(), ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tt.Frame()
	}
}

func BenchmarkFrameHUD(b *testing.B) {
	ctrl := benchController(b)
	ctrl.Dispatch(engine.Event{Type: engine.EventShowHUD, Message: "Copied to clipboard"})
	benchFrames(b, ctrl)
}

func BenchmarkFrameOmnibox(b *testing.B) {
	ctrl := benchController(b)
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	benchFrames(b, ctrl)
}

func BenchmarkFrameLeader(b *testing.B) {
	ctrl := benchController(b)
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenLeader})
	benchFrames(b, ctrl)
}
