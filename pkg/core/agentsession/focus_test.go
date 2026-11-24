package agentsession

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/host"
)

type fakeFocusExecutor struct {
	pathErr  error
	list     []byte
	listErr  error
	focus    []byte
	focusErr error
	block    bool
	calls    []string
}

func (f *fakeFocusExecutor) LookPath(string) (string, error) {
	f.calls = append(f.calls, "lookpath")
	return "/fake/herdr", f.pathErr
}
func (f *fakeFocusExecutor) Output(ctx context.Context, _ string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(args) >= 2 && args[0] == "agent" && args[1] == "list" {
		return f.list, f.listErr
	}
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.focus, f.focusErr
}

func TestHerdrFocuserCapabilityAndFocus(t *testing.T) {
	fake := &fakeFocusExecutor{list: []byte(`[{"agent_session":{"value":"/tmp/2026_session-abc.jsonl"},"pane_id":"pane-7","tab_id":"tab-1"}]`)}
	h := NewHerdrFocuserWithExecutor(fake)
	available, err := h.Available(context.Background())
	if err != nil || !available {
		t.Fatalf("available=%v err=%v", available, err)
	}
	result, err := h.Focus(context.Background(), "session-abc")
	if err != nil || result.State != host.StateReady {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if got := fake.calls[len(fake.calls)-1]; got != "agent focus pane-7" {
		t.Fatalf("focus call=%q calls=%v", got, fake.calls)
	}
}

func TestHerdrFocuserMissingAndMalformedDegrade(t *testing.T) {
	missing := &fakeFocusExecutor{pathErr: errors.New("not found")}
	available, err := NewHerdrFocuserWithExecutor(missing).Available(context.Background())
	if err != nil || available {
		t.Fatalf("missing herdr available=%v err=%v", available, err)
	}
	malformed := &fakeFocusExecutor{list: []byte("not json")}
	available, err = NewHerdrFocuserWithExecutor(malformed).Available(context.Background())
	if err != nil || available {
		t.Fatalf("malformed list available=%v err=%v", available, err)
	}
}

func TestHerdrFocuserUnknownIsNotRetryable(t *testing.T) {
	fake := &fakeFocusExecutor{list: []byte(`[{"agent_session":{"value":"/tmp/other.jsonl"},"pane_id":"pane-7"}]`)}
	result, err := NewHerdrFocuserWithExecutor(fake).Focus(context.Background(), "missing")
	if err != nil || result.State != host.StateUnknown || result.Retryable {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestHerdrFocuserRejectsSubstringAndAmbiguousMatches(t *testing.T) {
	for _, fixture := range []string{
		`[{"agent_session":{"value":"/tmp/session-abc-longer.jsonl"},"pane_id":"pane-7"}]`,
		`[{"agent_session":{"value":"/tmp/session-abc.jsonl"},"pane_id":"pane-7"},{"agent_session":{"value":"/other/session-abc.jsonl"},"pane_id":"pane-8"}]`,
	} {
		fake := &fakeFocusExecutor{list: []byte(fixture)}
		result, err := NewHerdrFocuserWithExecutor(fake).Focus(context.Background(), "session-abc")
		if err != nil || result.State != host.StateUnknown || len(fake.calls) != 2 {
			t.Fatalf("unsafe focus result=%+v err=%v calls=%v", result, err, fake.calls)
		}
	}
}

func TestHerdrFocuserFocusTimeoutIsObservable(t *testing.T) {
	fake := &fakeFocusExecutor{list: []byte(`[{"agent_session":{"value":"/tmp/session-abc.jsonl"},"pane_id":"pane-7"}]`), block: true}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	started := time.Now()
	result, err := NewHerdrFocuserWithExecutor(fake).Focus(ctx, "session-abc")
	if time.Since(started) > time.Second {
		t.Fatalf("focus timeout blocked too long: %v", time.Since(started))
	}
	if err != nil || result.State != host.StateFailed || result.Code != "focus_failed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestHerdrFocuserParsesWrappedResultEnvelopeAndFocusPane(t *testing.T) {
	wrapped := `{"id":"cli:agent:list","result":{"agents":[{"agent":"agy","agent_session":{"agent":"agy","kind":"id","source":"herdr:antigravity_cli","value":"ff81383b-733e-49bd-95e2-37a5a0bdecfd"},"agent_status":"idle","cwd":"/Users/yeshugen/workspace/clavis","display_agent":"agy","focused":true,"name":"clavis-main","pane_id":"w4:pB","tab_id":"w4:t2","workspace_id":"w4"}]},"type":"agent_list"}`
	fake := &fakeFocusExecutor{list: []byte(wrapped)}
	h := NewHerdrFocuserWithExecutor(fake)

	available, err := h.Available(context.Background())
	if err != nil || !available {
		t.Fatalf("available=%v err=%v", available, err)
	}
	result, err := h.Focus(context.Background(), "ff81383b-733e-49bd-95e2-37a5a0bdecfd")
	if err != nil || result.State != host.StateReady {
		t.Fatalf("Focus wrapped envelope result=%+v err=%v", result, err)
	}
	if got := fake.calls[len(fake.calls)-1]; got != "agent focus w4:pB" {
		t.Fatalf("focus call=%q", got)
	}

	// Direct FocusPane skips `agent list` and jumps straight to the pane.
	beforeCalls := len(fake.calls)
	res2, err := h.FocusPane(context.Background(), "w4:pB")
	if err != nil || res2.State != host.StateReady {
		t.Fatalf("FocusPane result=%+v err=%v", res2, err)
	}
	if len(fake.calls)-beforeCalls != 2 || fake.calls[len(fake.calls)-1] != "agent focus w4:pB" {
		t.Fatalf("FocusPane calls=%v", fake.calls[beforeCalls:])
	}
}
