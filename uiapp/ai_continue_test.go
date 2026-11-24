package uiapp

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/host"
)

// Models add numbering, quotes and commentary even when told not to; the parser must clean
// those rather than pass them through into the user's document.
func TestParseAIContinuationCleansModelNoise(t *testing.T) {
	reply := "1. 不过预算部分需要再确认\n" +
		"2) \"整体方向没问题\"\n" +
		"- 我今晚整理一份风险清单\n" +
		"以上是三个选项\n"
	got := ParseAIContinuation(reply)
	if len(got) != 3 {
		t.Fatalf("got %d options: %v", len(got), got)
	}
	for _, want := range []string{"不过预算部分需要再确认", "整体方向没问题", "我今晚整理一份风险清单"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %q in %v", want, got)
		}
	}
}

func TestParseAIContinuationDropsDuplicatesAndEmpty(t *testing.T) {
	got := ParseAIContinuation("\n\n重复一句\n重复一句\n\n")
	if len(got) != 1 || got[0] != "重复一句" {
		t.Fatalf("got %v", got)
	}
}

func TestParseAIContinuationCapsAtThree(t *testing.T) {
	got := ParseAIContinuation("一\n二\n三\n四\n五\n")
	if len(got) != aiContinueOptions {
		t.Fatalf("got %d, want %d", len(got), aiContinueOptions)
	}
}

// Only text before the caret matters, and only the tail: continuation is about what comes next.
func TestTailBeforeCaretUsesTailOnly(t *testing.T) {
	long := strings.Repeat("字", 500)
	snap := host.EditableFieldSnapshot{Value: long + "后面的内容", SelectionEnd: utf8.RuneCountInString(long)}
	got := tailBeforeCaret(snap)
	if n := len([]rune(got)); n != aiContinueContextChars {
		t.Fatalf("context is %d runes, want %d", n, aiContinueContextChars)
	}
	if strings.Contains(got, "后面的内容") {
		t.Fatal("text after the caret must not be sent")
	}
}

func TestTailBeforeCaretCJKRuneSlicing(t *testing.T) {
	text := "今天天气真好，我们去公园散步"
	// Caret after the 6th rune ("今天天气真好"): byte index would be 18, rune index is 6.
	// With byte slicing text[:6] would only be "今天" (2 runes); with rune index 5 byte slicing
	// would split a 3-byte UTF-8 rune and produce invalid UTF-8.
	for _, tc := range []struct {
		caret int
		want  string
	}{
		{caret: 5, want: "今天天气真"},
		{caret: 6, want: "今天天气真好"},
		{caret: 7, want: "今天天气真好，"},
		{caret: 0, want: ""},
		{caret: -1, want: text},
		{caret: 999, want: text},
	} {
		got := tailBeforeCaret(host.EditableFieldSnapshot{Value: text, SelectionEnd: tc.caret})
		if !utf8.ValidString(got) {
			t.Fatalf("caret=%d produced invalid UTF-8: %q", tc.caret, got)
		}
		if got != tc.want {
			t.Fatalf("caret=%d got %q, want %q", tc.caret, got, tc.want)
		}
	}
}

func TestAIContinuePromptAsksForNoNumbering(t *testing.T) {
	p := AIContinuePrompt("这个方案我看了，")
	if !strings.Contains(p, "no numbering") || !strings.Contains(p, "这个方案我看了，") {
		t.Fatalf("prompt missing constraints or context:\n%s", p)
	}
}

type secureInplaceBridge struct {
	*inplaceTestBridge
	secure atomic.Bool
}

func (b *secureInplaceBridge) IsSecureInputEnabled() bool {
	return b.secure.Load()
}

func TestAIContinueArmsBackspaceUndo(t *testing.T) {
	b := &inplaceTestBridge{mockBridge: newMockBridge()}
	c := newInplaceTestController(t, b, Options{})

	inserted := "我们可以明天上午再对齐细节。"
	c.insertContinuation([]string{inserted, "备选二"})

	if got := b.getInjected(); len(got) != 1 || got[0].text != inserted || got[0].delCount != 0 {
		t.Fatalf("initial insertion = %+v, want %q", got, inserted)
	}

	// Pressing Backspace within 5s must undo the remaining runes with empty replacement text.
	c.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerSnippetBack})
	waitUntil(t, time.Second, func() bool { return len(b.getInjected()) == 2 }, "continuation backspace undo")
	undo := b.getInjected()[1]
	wantDel := utf8.RuneCountInString(inserted) - 1
	if undo.text != "" || undo.delCount != wantDel || undo.backOffset != 0 {
		t.Fatalf("undo call = %+v, want text=%q delCount=%d backOffset=0", undo, "", wantDel)
	}

	// Second Backspace must be a normal backspace (single-shot undo).
	c.HandleTriggerEvent(host.TriggerEvent{Kind: host.TriggerSnippetBack})
	time.Sleep(3 * snippetUndoDelay)
	if n := len(b.getInjected()); n != 2 {
		t.Fatalf("second Backspace unexpectedly injected again (%d calls)", n)
	}
}

func TestAIContinueRegisteredWithRuntimeAndCancellable(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	b := &inplaceTestBridge{
		mockBridge: newMockBridge(),
		field: host.EditableFieldSnapshot{
			Value:        "这个方案整体可行，接下来",
			SelectionEnd: utf8.RuneCountInString("这个方案整体可行，接下来"),
		},
	}
	sec := secrets.NewMemoryAPIKeyStore()
	_ = sec.SaveKey("clavis.ai.deepseek", "sk-test-key")
	c := newInplaceTestController(t, b, Options{
		SecretStore: sec,
		HTTPDoer: mockHTTPDoer{fn: func(req *http.Request) (*http.Response, error) {
			close(started)
			<-req.Context().Done()
			close(canceled)
			return nil, req.Context().Err()
		}},
	})
	if err := c.UpdateMainConfig(func(m *config.MainConfig) {
		m.AI.ContinueEnabled = true
		m.AI.ContinueDisclosed = true
	}); err != nil {
		t.Fatal(err)
	}

	c.startAIContinue()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("startAIContinue did not start HTTP request")
	}

	report := c.StopAll("cancel-continue")
	if len(report.Operations) == 0 {
		t.Fatalf("expected AI continue registered in runtime registry, got empty StopAll report")
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("StopAll did not cancel in-flight AI continue request")
	}
	if got := b.getInjected(); len(got) != 0 {
		t.Fatalf("canceled continuation unexpectedly injected text: %+v", got)
	}
}

func TestAIContinueBlockedDuringSecureInput(t *testing.T) {
	var httpCalls atomic.Int32
	b := &secureInplaceBridge{
		inplaceTestBridge: &inplaceTestBridge{
			mockBridge: newMockBridge(),
			field: host.EditableFieldSnapshot{
				Value:        "my secret password draft",
				SelectionEnd: 24,
			},
		},
	}
	b.secure.Store(true)

	sec := secrets.NewMemoryAPIKeyStore()
	_ = sec.SaveKey("clavis.ai.deepseek", "sk-test-key")
	c := newInplaceTestController(t, b.inplaceTestBridge, Options{
		SecretStore: sec,
		HTTPDoer: mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
			httpCalls.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
			}, nil
		}},
	})
	c.Bridge = b
	if err := c.UpdateMainConfig(func(m *config.MainConfig) {
		m.AI.ContinueEnabled = true
		m.AI.ContinueDisclosed = true
	}); err != nil {
		t.Fatal(err)
	}

	c.startAIContinue()
	time.Sleep(50 * time.Millisecond)
	if httpCalls.Load() != 0 {
		t.Fatalf("AI continue sent %d HTTP requests while Secure Input was enabled", httpCalls.Load())
	}
	if !strings.Contains(strings.ToLower(c.State().HUDMessage), "secure") && !strings.Contains(c.State().HUDMessage, "安全输入") {
		t.Fatalf("expected Secure Input HUD warning, got %q", c.State().HUDMessage)
	}
}
