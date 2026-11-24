package uiapp

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/host"
)

func TestDetectPreviewContentKindAndBudget(t *testing.T) {
	if got := detectPreviewContentKind(`{"name":"clavis","ok":true}`, ""); got != previewKindJSON {
		t.Fatalf("json detection got %v", got)
	}
	if got := detectPreviewContentKind("# Title\n\n- item 1\n- [x] done\n", ""); got != previewKindMarkdown {
		t.Fatalf("markdown detection got %v", got)
	}
	if got := detectPreviewContentKind("plain prose sentence without markdown", ".md"); got != previewKindMarkdown {
		t.Fatalf(".md hint detection got %v", got)
	}
	if got := detectPreviewContentKind("hello world", ""); got != previewKindPlain {
		t.Fatalf("plain detection got %v", got)
	}

	var b strings.Builder
	b.WriteString("```go\n")
	for i := 0; i < 400; i++ {
		b.WriteString("fmt.Println(\"line\")\n")
	}
	b.WriteString("```\n")
	trimmed, truncated, omitted := prepareMarkdownBudget(b.String(), 24<<10, 120)
	if !truncated || omitted <= 0 {
		t.Fatalf("expected truncation, got truncated=%v omitted=%d", truncated, omitted)
	}
	if strings.Count(trimmed, "```")%2 != 0 {
		t.Fatalf("unclosed fence after truncation: %q", trimmed)
	}
	if !isSafeExternalURL("https://herdr.dev/docs") || isSafeExternalURL("javascript:alert(1)") || isSafeExternalURL("file:///etc/passwd") {
		t.Fatal("unsafe URL validation failed")
	}
}

func TestClipboardPreviewMarkdownRenderAndRawToggle(t *testing.T) {
	ctrl, _, _, _ := newTestController(t, nil)
	mdText := "# Release Notes\n\n- **Herdr** live workbench\n\n```go\npackage main\nfunc main() {}\n```"
	ctrl.Dispatch(engine.Event{
		Type: engine.EventSetClipboardEntries,
		ClipboardEntries: []engine.OmniboxClipboardEntry{{
			ID:            "clip-md-1",
			Content:       mdText,
			SourceAppName: "Notes",
			CopiedAt:      time.Now(),
		}},
	})
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabClipboard})
	capsuleUI.setPreviewVisible(ctrl, true)

	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()

	if !tt.HasText("Release Notes") || !tt.HasText("go") || !tt.HasText("MD") {
		t.Fatalf("markdown preview missing expected elements: %v", tt.Texts())
	}

	if err := tt.Click("MD"); err != nil {
		t.Fatalf("click MD toggle: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Raw") && !tt.HasText("原文") {
		t.Fatalf("expected raw mode toggle label after click: %v", tt.Texts())
	}
}

type mockHerdrLiveCLI struct {
	mu       sync.Mutex
	calls    []string
	agents   string
	workspc  string
	readBody string
}

func (m *mockHerdrLiveCLI) LookPath(string) (string, error) { return "/fake/herdr", nil }
func (m *mockHerdrLiveCLI) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	m.mu.Lock()
	m.calls = append(m.calls, strings.Join(args, " "))
	m.mu.Unlock()
	if len(args) >= 2 && args[0] == "agent" && args[1] == "list" {
		return []byte(m.agents), nil
	}
	if len(args) >= 2 && args[0] == "workspace" && args[1] == "list" {
		return []byte(m.workspc), nil
	}
	if len(args) >= 2 && args[0] == "agent" && args[1] == "read" {
		return []byte(m.readBody), nil
	}
	return []byte("{}"), nil
}

type fakePaneFocuser struct {
	mu       sync.Mutex
	paneID   string
	session  string
	res      host.OperationResult
}

func (f *fakePaneFocuser) Name() string                         { return "fake-herdr-pane" }
func (f *fakePaneFocuser) Available(context.Context) (bool, error) { return true, nil }
func (f *fakePaneFocuser) Focus(_ context.Context, id string) (host.OperationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.session = id
	return f.res, nil
}
func (f *fakePaneFocuser) FocusPane(_ context.Context, paneID string) (host.OperationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneID = paneID
	return f.res, nil
}

func TestAgentTabWithLiveHerdrOutputMarkdownAndHeaderPill(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfgStore := config.NewConfigStore(dir)
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	mockCLI := &mockHerdrLiveCLI{
		workspc: `{"id":"cli:workspace:list","result":{"workspaces":[{"workspace_id":"w4","label":"clavis","number":4,"agent_status":"working","focused":true}]}}`,
		agents:  `{"id":"cli:agent:list","result":{"agents":[{"agent":"agy","agent_session":{"value":"sess-live-1"},"agent_status":"working","cwd":"/Users/alice/workspace/clavis","name":"clavis-fe","pane_id":"w4:pB","tab_id":"w4:t2","workspace_id":"w4","focused":true}]}}`,
		readBody: "## Live Build\n- All checks green\n- secret=sk-neverleak123456\n",
	}
	ctrl := NewAppController(Options{
		ConfigStore:     cfgStore,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:   mockCLI,
		ClipboardWriter: func(string) {},
	})
	t.Cleanup(ctrl.Close)

	waitUntil(t, 2*time.Second, func() bool {
		sessions, _ := ctrl.cachedAgentData()
		for _, s := range sessions {
			if s.PaneID == "w4:pB" {
				return true
			}
		}
		return false
	}, "live herdr session discovered")

	// While on Actions tab, Header shows the neutral Herdr running count micro-chip.
	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions})
	w, h := PreferredCapsuleSize(engine.PhaseOmnibox, ctrl.Config().Main.Appearance)
	tt := ui.NewTester(ctrl.CapsuleView, w, h)
	tt.Frame()
	if _, ok := tt.Find("Agent 1"); !ok {
		t.Fatalf("header-agent-pill missing on Actions tab: %v", tt.Texts())
	}

	// Switch to Agent tab and let its initial refresh settle before triggering output load.
	beforeSwitch := ctrl.agentRecordedOperationResult().ObservedAt
	ctrl.Dispatch(engine.Event{Type: engine.EventSwitchTab, Tab: config.TabAgent})
	waitUntil(t, 2*time.Second, func() bool {
		return !ctrl.agentRefreshing() && ctrl.agentRecordedOperationResult().ObservedAt.After(beforeSwitch)
	}, "agent tab refresh should settle")
	tt.Frame()
	waitUntil(t, 2*time.Second, func() bool {
		snap := ctrl.cachedAgentOutput("w4:pB")
		return !snap.Loading && snap.Content != ""
	}, "live herdr output should load")

	tt.Frame()
	if !tt.HasText("clavis-fe") || !tt.HasText("w4:pB") || !tt.HasText("Live Build") {
		t.Fatalf("live herdr workbench missing expected output/badges: %v", tt.Texts())
	}
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "sk-neverleak123456") {
			t.Fatalf("live herdr output leaked secret: %q", txt)
		}
	}

	// Focus uses direct PaneFocuser when PaneID is available.
	pf := &fakePaneFocuser{res: host.OperationResult{State: host.StateReady}}
	restore := setAgentFocuserForTest(pf)
	defer restore()
	items := ctrl.agentVisibleItems("")
	if len(items) == 0 || !ctrl.agentItemAction(items[0]) {
		t.Fatalf("failed to activate live herdr item: %+v", items)
	}
	waitUntil(t, time.Second, func() bool {
		pf.mu.Lock()
		defer pf.mu.Unlock()
		return pf.paneID == "w4:pB"
	}, "direct PaneFocuser should receive w4:pB")
}
