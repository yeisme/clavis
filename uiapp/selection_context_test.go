package uiapp

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
)

// probingBridge is a mockBridge whose platform can copy the selection
// (bridge.SelectionProber), as darwinBridge does with ⌘C.
type probingBridge struct {
	*mockBridge
	pmu   sync.Mutex
	text  string
	calls int
}

func (p *probingBridge) ProbeSelection(context.Context, time.Duration) (string, error) {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	p.calls++
	return p.text, nil
}

func (p *probingBridge) probeCalls() int {
	p.pmu.Lock()
	defer p.pmu.Unlock()
	return p.calls
}

// newProbingController is newTestController with a bridge that can probe
// the selection, and a frontmost app whose snapshot is snap.
func newProbingController(t *testing.T, snap host.ContextSnapshot, probed string) (*AppController, *probingBridge) {
	t.Helper()
	t.Setenv("DEEPSEEK_API_KEY", "")
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}
	secStore := secrets.NewMemoryAPIKeyStore()
	_ = secStore.SaveKey(cfgStore.Current().Main.AI.KeychainAccount, "sk-test-key")
	br := newMockBridge()
	br.snapshot = snap
	pb := &probingBridge{mockBridge: br, text: probed}
	ctrl := NewAppController(Options{
		ConfigStore:    cfgStore,
		ClipboardStore: clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		SecretStore:    secStore,
		Bridge:         pb,
		HTTPDoer: mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
			return sseResponse("rewritten"), nil
		}},
	})
	t.Cleanup(ctrl.Close)
	return ctrl, pb
}

// slackSnapshot is an app whose accessibility tree cannot report its
// selection (Electron), with something stale on the clipboard.
func slackSnapshot() host.ContextSnapshot {
	return host.ContextSnapshot{AppName: "Slack", BundleID: "com.tinyspeck.slackmacgap", ClipboardPreview: "stale clipboard text"}
}

// TestLeaderRewritesTheProbedSelection: in an app whose selection
// accessibility cannot read, the Leader copies it, and AI rewrite works on
// it instead of on whatever was last on the clipboard.
func TestLeaderRewritesTheProbedSelection(t *testing.T) {
	ctrl, pb := newProbingController(t, slackSnapshot(), "i has a apple")
	ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerLeaderKey})
	if st := ctrl.State(); st.Phase != engine.PhaseLeader || st.Context.SelectedText != "i has a apple" {
		t.Fatalf("Leader context = %q in %s, want the probed selection", st.Context.SelectedText, st.Phase)
	}
	if pb.probeCalls() != 1 {
		t.Fatalf("probe calls = %d, want 1", pb.probeCalls())
	}

	// a → AI Rewrite, p → Polish (the default Leader tree).
	ctrl.Dispatch(engine.Event{Type: engine.EventLeaderKeyPress, Key: "a"})
	ctrl.Dispatch(engine.Event{Type: engine.EventLeaderKeyPress, Key: "p"})
	st := ctrl.State()
	if st.Phase != engine.PhaseAIStreaming || st.AISourceText != "i has a apple" {
		t.Fatalf("rewrite source = %q in %s, want the selection, not the clipboard", st.AISourceText, st.Phase)
	}
}

// TestGlossaryNormalizesTheProbedSelection: "Normalize glossary" replaces
// the probed selection in place instead of rewriting the clipboard.
func TestGlossaryNormalizesTheProbedSelection(t *testing.T) {
	const sel = "we ship clavis today"
	ctrl, pb := newProbingController(t, slackSnapshot(), sel)
	ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerCapsuleToggle})
	actionCtx := ctrl.State().Context
	if actionCtx.SelectedText != sel {
		t.Fatalf("Capsule context selection = %q", actionCtx.SelectedText)
	}
	want := ctrl.Glossary.NormalizeAliases(sel, true, true)
	if want == sel {
		t.Fatalf("the default glossary should normalize %q", sel)
	}

	ctrl.normalizeGlossary(actionCtx)
	waitUntil(t, time.Second, func() bool { return len(pb.getInjected()) == 1 }, "normalized injection")
	if got := pb.getInjected()[0]; got.text != want || got.delCount != 0 {
		t.Fatalf("injected %+v, want %q over the selection", got, want)
	}
}

// TestSelectionProbeOnlyWhenUnknown: a platform that read the selection
// (even an empty one) is trusted, and triggers that don't act on the
// selection never probe.
func TestSelectionProbeOnlyWhenUnknown(t *testing.T) {
	known := slackSnapshot()
	known.SelectionKnown = true
	ctrl, pb := newProbingController(t, known, "should not be used")
	ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerLeaderKey})
	if pb.probeCalls() != 0 || ctrl.State().Context.SelectedText != "" {
		t.Fatalf("probed although accessibility said nothing is selected (calls %d, selection %q)",
			pb.probeCalls(), ctrl.State().Context.SelectedText)
	}

	axText := slackSnapshot()
	axText.SelectedText = "read by accessibility"
	ctrl2, pb2 := newProbingController(t, axText, "should not be used")
	ctrl2.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerLeaderKey})
	if pb2.probeCalls() != 0 || ctrl2.State().Context.SelectedText != "read by accessibility" {
		t.Fatalf("probed although accessibility read the selection (calls %d)", pb2.probeCalls())
	}

	ctrl3, pb3 := newProbingController(t, slackSnapshot(), "should not be used")
	ctrl3.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerClipboardToggle})
	if pb3.probeCalls() != 0 {
		t.Fatalf("Clipboard History probed the selection (%d calls)", pb3.probeCalls())
	}
}

// TestNoProberKeepsTheClipboardFallback: a bridge that cannot probe
// (Windows today) behaves as before: the rewrite falls back to the
// clipboard.
func TestNoProberKeepsTheClipboardFallback(t *testing.T) {
	ctrl, br, _, _ := newTestController(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		return sseResponse("rewritten"), nil
	}})
	br.mu.Lock()
	br.snapshot = slackSnapshot()
	br.mu.Unlock()
	ctrl.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerLeaderKey})
	ctrl.Dispatch(engine.Event{Type: engine.EventLeaderKeyPress, Key: "a"})
	ctrl.Dispatch(engine.Event{Type: engine.EventLeaderKeyPress, Key: "p"})
	if got := ctrl.State().AISourceText; got != "stale clipboard text" {
		t.Fatalf("rewrite source without a prober = %q, want the clipboard fallback", got)
	}
}
