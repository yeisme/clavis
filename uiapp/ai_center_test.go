package uiapp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestAICenterFiveStatesAndStepLogWithoutPercentages(t *testing.T) {
	prof := config.AIProfile{ID: "deepseek", Model: "deepseek-chat"}

	cases := []struct {
		name      string
		state     engine.State
		hasReview bool
		wantPhase aiInteractionPhase
	}{
		{
			name:      "pending",
			state:     engine.State{Phase: engine.PhaseAIStreaming},
			wantPhase: aiPhasePending,
		},
		{
			name: "running",
			state: engine.State{
				Phase:      engine.PhaseAIStreaming,
				AIPrompt:   "Explain mutex",
				AIOutput:   "A mutex guards shared state.",
				AIComplete: false,
			},
			wantPhase: aiPhaseRunning,
		},
		{
			name: "awaiting_human",
			state: engine.State{
				Phase:        engine.PhaseAIStreaming,
				AIPrompt:     "Fix grammar",
				AISourceText: "I has a apple",
				AIOutput:     "I have an apple",
				AIComplete:   true,
			},
			hasReview: true,
			wantPhase: aiPhaseAwaitingHuman,
		},
		{
			name: "done",
			state: engine.State{
				Phase:      engine.PhaseAIStreaming,
				AIPrompt:   "Explain mutex",
				AIOutput:   "A mutex guards shared state.",
				AIComplete: true,
			},
			wantPhase: aiPhaseDone,
		},
		{
			name: "failed",
			state: engine.State{
				Phase:      engine.PhaseAIStreaming,
				AIPrompt:   "Explain mutex",
				AIComplete: true,
				AIError:    "upstream timeout",
			},
			wantPhase: aiPhaseFailed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveAIInteractionPhase(tc.state, tc.hasReview)
			if got != tc.wantPhase {
				t.Fatalf("phase = %q, want %q", got, tc.wantPhase)
			}
			label := aiPhaseBadgeLabel(got)
			if strings.Contains(label, "%") {
				t.Fatalf("phase badge %q must not contain percentage progress", label)
			}
			steps := aiStepLog(tc.state, prof, tc.hasReview)
			if len(steps) != 3 {
				t.Fatalf("step log len = %d, want 3", len(steps))
			}
			for _, step := range steps {
				if strings.Contains(step.Title, "%") || strings.Contains(step.Detail, "%") {
					t.Fatalf("step log %+v must not use percentage progress", step)
				}
			}
		})
	}
}

func TestAICenterRunningShowsStepLogAndStopTransitionsToFailed(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	f := newInteractionFixture(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		pr, pw := io.Pipe()
		go func() {
			_, _ = fmt.Fprint(pw, "data: {\"choices\":[{\"delta\":{\"content\":\"Partial answer\"}}]}\n\n")
			<-release
			_ = pw.Close()
		}()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       pr,
		}, nil
	}})
	f.ctrl.Dispatch(engine.Event{
		Type: engine.EventOpenOmnibox, Tab: config.TabActions,
		Context: &engine.CapsuleContext{AppName: "Notes"},
	})
	w, h := PreferredCapsuleSize(engine.PhaseAIStreaming, f.ctrl.Config().Main.Appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()
	tt.Type("?stream check")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && !st.AIComplete && st.AIOutput == "Partial answer"
	}, "streaming partial output")

	tt.Frame()
	if _, ok := tt.Find("ai-step-log"); !ok {
		t.Fatalf("countable step log missing during running state: %v", tt.Texts())
	}
	if err := tt.Click("Stop"); err != nil {
		t.Fatalf("click Stop button: %v (texts=%v)", err, tt.Texts())
	}
	tt.Frame()
	st := f.ctrl.State()
	if st.Phase != engine.PhaseAIStreaming || !st.AIComplete || st.AIError == "" {
		t.Fatalf("Stop did not transition cleanly to failed/stopped state: %+v", st)
	}
}

func TestAICenterDoneCollapsesStepsAndExpandsOnDemand(t *testing.T) {
	f := newInteractionFixture(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		return sseResponse("Concise completed answer"), nil
	}})
	f.ctrl.Dispatch(engine.Event{
		Type: engine.EventOpenOmnibox, Tab: config.TabActions,
		Context: &engine.CapsuleContext{AppName: "Notes"},
	})
	w, h := PreferredCapsuleSize(engine.PhaseAIStreaming, f.ctrl.Config().Main.Appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()
	tt.Type("?explain mutex")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete
	}, "done state")

	tt.Frame()
	if _, ok := tt.Find("ai-step-log"); ok {
		t.Fatalf("step log should be collapsed by default in done state")
	}
	if !tt.HasText("Concise completed answer") {
		t.Fatalf("completed result missing: %v", tt.Texts())
	}
	if err := tt.Click("Process · 3/3 steps"); err != nil {
		t.Fatalf("expand collapsed steps toggle: %v (texts=%v)", err, tt.Texts())
	}
	tt.Frame()
	if _, ok := tt.Find("ai-step-log"); !ok {
		t.Fatalf("step log did not expand on toggle click: %v", tt.Texts())
	}
}

func TestAICenterErrorRetryAndEditWithIMEArbitration(t *testing.T) {
	var calls atomic.Int32
	f := newInteractionFixture(t, mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return nil, context.DeadlineExceeded
		}
		return sseResponse("Recovered response after retry"), nil
	}})
	f.ctrl.Dispatch(engine.Event{
		Type: engine.EventOpenOmnibox, Tab: config.TabActions,
		Context: &engine.CapsuleContext{AppName: "Notes"},
	})
	w, h := PreferredCapsuleSize(engine.PhaseAIStreaming, f.ctrl.Config().Main.Appearance)
	tt := ui.NewTester(f.ctrl.CapsuleView, w, h)
	tt.Frame()
	tt.Type("?summarize notes")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)

	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIError != ""
	}, "AI error state")

	tt.Frame()
	if !tt.HasText("summarize notes") {
		t.Fatalf("current prompt strip missing on error: %v", tt.Texts())
	}

	// Focus multi-line retry composer and verify IME composition blocks Enter/Escape.
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	tt.Compose("chongshi", 8)
	tt.Key(0, ui.KeyEnter)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if calls.Load() != 1 || f.ctrl.State().Phase != engine.PhaseAIStreaming {
		t.Fatalf("IME preedit leaked into retry/close: calls=%d phase=%s", calls.Load(), f.ctrl.State().Phase)
	}

	// End IME composition and retry via ⌘R / Ctrl+R.
	tt.Compose("", 0)
	tt.Key(primaryMod(), ui.KeyR)
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete && st.AIError == "" && st.AIOutput == "Recovered response after retry"
	}, "AI recovery after Cmd+R retry")
	tt.Frame()
	if !tt.HasText("Recovered response after retry") {
		t.Fatalf("recovered answer missing: %v", tt.Texts())
	}
}

func TestAICenterAwaitingHumanBlocksUntilActedOrRevised(t *testing.T) {
	f, tt := runRewrite(t, "I has a apple and its very tasty", "I have an apple and it's very tasty")
	if _, ok := tt.Find("ai-approval-gate"); !ok {
		t.Fatalf("blocking approval gate missing in awaiting_human: %v", tt.Texts())
	}
	if _, ok := tt.Find("ai-center-composer"); ok {
		t.Fatalf("follow-up composer should stay gated behind Revise until requested: %v", tt.Texts())
	}
	// Clicking Revise opens the multi-line revision composer for EventFollowUpAI.
	if err := tt.Click("Revise"); err != nil {
		t.Fatalf("click Revise button: %v (texts=%v)", err, tt.Texts())
	}
	tt.Frame()
	if _, ok := tt.Find("ai-center-composer"); !ok {
		t.Fatalf("revision composer did not open after clicking Revise: %v", tt.Texts())
	}
	tt.Type("make it playful")
	tt.Frame()
	if err := tt.Click("Send"); err != nil {
		t.Fatalf("click Send button: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		st := f.ctrl.State()
		return st.Phase == engine.PhaseAIStreaming && st.AIComplete && st.AIPrompt == "make it playful"
	}, "follow-up via Send button")
}

func TestAICenterDisplayHistoryDeduplicatesFailedRetries(t *testing.T) {
	dedup := aiDisplayHistory([]engine.ChatTurn{
		{Role: "user", Content: "same prompt"},
		{Role: "user", Content: "same prompt"},
		{Role: "assistant", Content: "answer"},
	})
	if len(dedup) != 2 {
		t.Fatalf("expected consecutive duplicate user turns collapsed, got %+v", dedup)
	}
}
