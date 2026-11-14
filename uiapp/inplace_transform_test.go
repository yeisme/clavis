package uiapp

import (
	"context"
	"sync"
	"testing"

	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

type inplaceTestBridge struct {
	*mockBridge
	mu           sync.Mutex
	field        host.EditableFieldSnapshot
	replacements []struct {
		text string
		only bool
	}
	anchor host.CursorAnchor
}

func (b *inplaceTestBridge) FastCaretAnchor(context.Context) (host.CursorAnchor, error) {
	return b.anchor, nil
}
func (b *inplaceTestBridge) CaptureEditableField(context.Context) (host.EditableFieldSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.field, nil
}
func (b *inplaceTestBridge) ReplaceEditableField(_ context.Context, text string, only bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.replacements = append(b.replacements, struct {
		text string
		only bool
	}{text, only})
	return nil
}
func (b *inplaceTestBridge) CommitText(context.Context, string, int, int) error { return nil }
func (b *inplaceTestBridge) SetInlineIMEHandler(func(host.InlineKeyEvent) bool) {}
func (b *inplaceTestBridge) SetInlineIMEEnabled(bool)                           {}
func (b *inplaceTestBridge) UpdateDaemonShortcut(string)                        {}
func (b *inplaceTestBridge) UpdateSelectionBarConfig(config.SelectionBarConfig) {}
func (b *inplaceTestBridge) ListListeningPorts(context.Context) ([]host.ListeningPort, error) {
	return nil, nil
}
func (b *inplaceTestBridge) KillProcess(context.Context, int, bool) error { return nil }

func newInplaceTestController(t *testing.T, b *inplaceTestBridge, opts Options) *AppController {
	t.Helper()
	if opts.ConfigStore == nil {
		opts.ConfigStore = config.NewConfigStore(t.TempDir())
	}
	if opts.SecretStore == nil {
		opts.SecretStore = secrets.NewMemoryAPIKeyStore()
	}
	opts.Bridge = b
	c := NewAppController(opts)
	t.Cleanup(c.Close)
	return c
}

func TestInPlaceSelectionAndWholeField(t *testing.T) {
	b := &inplaceTestBridge{mockBridge: newMockBridge(), field: host.EditableFieldSnapshot{Value: "whole text", SelectedText: "text", SelectionStart: 6, SelectionEnd: 10}}
	c := newInplaceTestController(t, b, Options{})
	c.startInPlaceTransform(false, "optimize")
	if c.State().AIPrompt != "text" {
		t.Fatalf("prompt = %q, want selection", c.State().AIPrompt)
	}
	b.field = host.EditableFieldSnapshot{Value: "whole text"}
	c.cancelAIStream()
	c.startInPlaceTransform(false, "optimize")
	if c.State().AIPrompt != "whole text" {
		t.Fatalf("prompt = %q, want whole field", c.State().AIPrompt)
	}
}

func TestInPlaceEmptyInputStops(t *testing.T) {
	b := &inplaceTestBridge{mockBridge: newMockBridge(), field: host.EditableFieldSnapshot{Value: "   "}}
	c := newInplaceTestController(t, b, Options{})
	c.startInPlaceTransform(false, "optimize")
	if c.State().Phase != "hud" || len(b.replacements) != 0 {
		t.Fatalf("empty input started or wrote: state=%+v replacements=%v", c.State(), b.replacements)
	}
}

func TestInPlaceApplyStaleAndExpectedReplacement(t *testing.T) {
	b := &inplaceTestBridge{mockBridge: newMockBridge(), field: host.EditableFieldSnapshot{Value: "before", SelectedText: "be", SelectionStart: 0, SelectionEnd: 2}}
	c := newInplaceTestController(t, b, Options{})
	ts := &inplaceSession{original: "before", onlySelection: true, mode: "optimize"}
	b.field.Value = "before"
	c.applyInPlaceResult(ts, "after")
	if len(b.replacements) != 1 || b.replacements[0].text != "after" || !b.replacements[0].only {
		t.Fatalf("replacement = %#v", b.replacements)
	}
	b.field.Value = "changed"
	c.applyInPlaceResult(&inplaceSession{original: "before", onlySelection: true, mode: "optimize"}, "ignored")
	if len(b.replacements) != 1 {
		t.Fatalf("stale result overwrote field: %#v", b.replacements)
	}
}

func TestInPlaceGenerationSupersedesOlder(t *testing.T) {
	b := &inplaceTestBridge{mockBridge: newMockBridge(), field: host.EditableFieldSnapshot{Value: "one"}}
	c := newInplaceTestController(t, b, Options{})
	first := &inplaceSession{original: "one", onlySelection: false, mode: "optimize"}
	c.mu.Lock()
	c.transform = first
	c.aiGen = 4
	c.mu.Unlock()
	c.startInPlaceTransform(false, "optimize")
	c.mu.Lock()
	current := c.transform
	c.mu.Unlock()
	if current == first {
		t.Fatal("new transform did not replace older session")
	}
}

func TestSelectionBarPayloadAndDisabled(t *testing.T) {
	b := &inplaceTestBridge{mockBridge: newMockBridge(), field: host.EditableFieldSnapshot{Value: "hello world", SelectedText: "world", SelectionStart: 6, SelectionEnd: 11}, anchor: host.CursorAnchor{X: 10, Y: 20, Valid: true}}
	var got SelectionBarPayload
	c := newInplaceTestController(t, b, Options{OnShowSelectionBar: func(p SelectionBarPayload) { got = p }})
	c.showSelectionQuickBar()
	if got.Text != "world" || !got.Anchor.Valid || len(got.Actions) == 0 {
		t.Fatalf("payload = %#v", got)
	}
	cfg := c.ConfigStore.Current()
	cfg.Main.SelectionBar.Enabled = false
	if err := c.ConfigStore.SaveMain(cfg.Main); err != nil {
		t.Fatal(err)
	}
	c.StateMachine.SetConfig(cfg)
	got = SelectionBarPayload{}
	c.showSelectionQuickBar()
	if got.Text != "" {
		t.Fatalf("disabled bar showed payload: %#v", got)
	}
}
