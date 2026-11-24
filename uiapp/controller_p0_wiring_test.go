package uiapp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

type recordingUsageRecorder struct {
	mu      sync.Mutex
	records []ai.UsageRecord
}

func (r *recordingUsageRecorder) RecordAIUsage(rec ai.UsageRecord) {
	r.mu.Lock()
	r.records = append(r.records, rec)
	r.mu.Unlock()
}

func (r *recordingUsageRecorder) snapshot() []ai.UsageRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ai.UsageRecord(nil), r.records...)
}

type secureMethodBridge struct {
	*mockBridge
	secure atomic.Bool
}

func (b *secureMethodBridge) IsSecureInputEnabled() bool {
	return b.secure.Load()
}

func TestTranslatePreservesDirectionalSystemPromptOverride(t *testing.T) {
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	sec := secrets.NewMemoryAPIKeyStore()
	_ = sec.SaveKey("clavis.ai.deepseek", "sk-ds-test")

	mockHTTP := &sseMockDoer{
		body: "data: {\"choices\":[{\"delta\":{\"content\":\"你好世界\"}}]}\n\ndata: [DONE]\n\n",
	}
	ctrl := NewAppController(Options{
		ConfigStore:     cfgStore,
		SecretStore:     sec,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:   disabledHerdrExecutor{},
		HTTPDoer:        mockHTTP,
		ClipboardWriter: func(string) {},
	})
	t.Cleanup(ctrl.Close)

	// English input -> TranslateEnToZh -> BuildTranslatePrompt system prompt must be preserved.
	ctrl.StartTranslation("Hello, world", "")
	waitUntil(t, 2*time.Second, func() bool {
		return ctrl.State().AIComplete
	}, "en->zh translation completion")

	mockHTTP.mu.Lock()
	enReqRaw := mockHTTP.requests[len(mockHTTP.requests)-1]
	mockHTTP.mu.Unlock()

	var enPayload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(enReqRaw), &enPayload); err != nil {
		t.Fatalf("unmarshal en->zh request: %v", err)
	}
	if len(enPayload.Messages) == 0 || enPayload.Messages[0].Role != "system" {
		t.Fatalf("expected system message, got %+v", enPayload.Messages)
	}
	wantEnSys, _ := engine.BuildTranslatePrompt("Hello, world", engine.TranslateEnToZh)
	if !strings.Contains(enPayload.Messages[0].Content, wantEnSys) {
		t.Fatalf("en->zh system prompt was overwritten!\ngot:  %q\nwant substring: %q", enPayload.Messages[0].Content, wantEnSys)
	}

	// Chinese input with explicit zh->en direction -> BuildTranslatePrompt zh->en system prompt preserved.
	ctrl.StartTranslation("保持代码块和格式不变", string(engine.TranslateZhToEn))
	waitUntil(t, 2*time.Second, func() bool {
		mockHTTP.mu.Lock()
		n := len(mockHTTP.requests)
		mockHTTP.mu.Unlock()
		return n >= 2 && ctrl.State().AIComplete
	}, "zh->en translation completion")

	mockHTTP.mu.Lock()
	zhReqRaw := mockHTTP.requests[len(mockHTTP.requests)-1]
	mockHTTP.mu.Unlock()

	var zhPayload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(zhReqRaw), &zhPayload); err != nil {
		t.Fatalf("unmarshal zh->en request: %v", err)
	}
	wantZhSys, _ := engine.BuildTranslatePrompt("保持代码块和格式不变", engine.TranslateZhToEn)
	if !strings.Contains(zhPayload.Messages[0].Content, wantZhSys) {
		t.Fatalf("zh->en system prompt was overwritten!\ngot:  %q\nwant substring: %q", zhPayload.Messages[0].Content, wantZhSys)
	}
}

func TestAIUsageRecorderAssignedOnInitAndPreservedOnReload(t *testing.T) {
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	sec := secrets.NewMemoryAPIKeyStore()
	_ = sec.SaveKey("clavis.ai.deepseek", "sk-ds-test")

	rec := &recordingUsageRecorder{}
	mockHTTP := &sseMockDoer{
		body: "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n",
	}
	ctrl := NewAppController(Options{
		ConfigStore:     cfgStore,
		SecretStore:     sec,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		HerdrExecutor:   disabledHerdrExecutor{},
		HTTPDoer:        mockHTTP,
		AIUsageRecorder: rec,
		ClipboardWriter: func(string) {},
	})
	t.Cleanup(ctrl.Close)

	if ctrl.AIClient == nil || ctrl.AIClient.UsageRecorder == nil {
		t.Fatal("AIClient.UsageRecorder was not wired in NewAppController")
	}

	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "?ping"})
	ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
	waitUntil(t, 2*time.Second, func() bool {
		return len(rec.snapshot()) == 1
	}, "first AI usage record")

	if !ctrl.ReloadConfig() {
		t.Fatal("ReloadConfig returned false")
	}
	if ctrl.AIClient == nil || ctrl.AIClient.UsageRecorder == nil {
		t.Fatal("AIClient.UsageRecorder was lost after ReloadConfig")
	}

	ctrl.Dispatch(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "?ping again"})
	ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
	waitUntil(t, 2*time.Second, func() bool {
		return len(rec.snapshot()) == 2
	}, "second AI usage record after reload")

	got := rec.snapshot()
	if got[1].Usage.InputTokens != 12 || got[1].Usage.OutputTokens != 4 || !got[1].Usage.Reported {
		t.Fatalf("unexpected usage record: %+v", got[1])
	}
}

func TestStartAIStreamSecureInputGuardAndSecretScrubbing(t *testing.T) {
	t.Run("blocks sending sensitive selection or clipboard when IsSecureInputEnabled is true", func(t *testing.T) {
		var calls atomic.Int32
		br := &secureMethodBridge{mockBridge: newMockBridge()}
		br.secure.Store(true)

		cfgStore := config.NewConfigStore(t.TempDir())
		if _, err := cfgStore.LoadOrInitialize(); err != nil {
			t.Fatal(err)
		}
		sec := secrets.NewMemoryAPIKeyStore()
		_ = sec.SaveKey("clavis.ai.deepseek", "sk-ds-test")

		ctrl := NewAppController(Options{
			ConfigStore:    cfgStore,
			SecretStore:    sec,
			Bridge:         br,
			ClipboardStore: clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
			HerdrExecutor:  disabledHerdrExecutor{},
			HTTPDoer: mockHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
				}, nil
			}},
			ClipboardWriter: func(string) {},
		})
		t.Cleanup(ctrl.Close)

		ctrl.Dispatch(engine.Event{
			Type:    engine.EventOpenOmnibox,
			Tab:     config.TabActions,
			Query:   "//improve",
			Context: &engine.CapsuleContext{SelectedText: "top-secret-password"},
		})
		ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})

		waitUntil(t, time.Second, func() bool {
			return ctrl.State().AIError != ""
		}, "Secure Input refusal error")
		if calls.Load() != 0 {
			t.Fatalf("expected 0 HTTP calls while Secure Input is active, got %d", calls.Load())
		}
	})

	t.Run("scrubs obvious secrets from auto-attached context while preserving explicit prompt", func(t *testing.T) {
		cfgStore := config.NewConfigStore(t.TempDir())
		if _, err := cfgStore.LoadOrInitialize(); err != nil {
			t.Fatal(err)
		}
		sec := secrets.NewMemoryAPIKeyStore()
		_ = sec.SaveKey("clavis.ai.deepseek", "sk-ds-test")

		mockHTTP := &sseMockDoer{
			body: "data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\n\ndata: [DONE]\n\n",
		}
		ctrl := NewAppController(Options{
			ConfigStore:     cfgStore,
			SecretStore:     sec,
			ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
			HerdrExecutor:   disabledHerdrExecutor{},
			HTTPDoer:        mockHTTP,
			ClipboardWriter: func(string) {},
		})
		t.Cleanup(ctrl.Close)

		pemKey := "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkq\n-----END PRIVATE KEY-----"
		origCtx := &engine.CapsuleContext{
			AppName:          "Terminal",
			SelectedText:     "openai=sk-proj-secret12345678 github=ghp_abcdef1234567890 pat=github_pat_11AA22BB33CC slack=xoxb-123456-789012 " + pemKey,
			ClipboardPreview: "copied sk-ant-9876543210abcdef",
		}
		ctrl.Dispatch(engine.Event{
			Type:    engine.EventOpenOmnibox,
			Tab:     config.TabActions,
			Query:   "//fix deploy script",
			Context: origCtx,
		})
		ctrl.Dispatch(engine.Event{Type: engine.EventConfirmSelection})
		waitUntil(t, 2*time.Second, func() bool {
			return ctrl.State().AIComplete
		}, "scrubbed AI optimize completion")

		mockHTTP.mu.Lock()
		sent := mockHTTP.requests[len(mockHTTP.requests)-1]
		mockHTTP.mu.Unlock()

		for _, secret := range []string{
			"sk-proj-secret12345678",
			"ghp_abcdef1234567890",
			"github_pat_11AA22BB33CC",
			"xoxb-123456-789012",
			"MIIEvQIBADANBgkq",
			"sk-ant-9876543210abcdef",
		} {
			if strings.Contains(sent, secret) {
				t.Fatalf("auto-attached secret %q leaked into AI request:\n%s", secret, sent)
			}
		}
		if !strings.Contains(sent, "[REDACTED]") {
			t.Fatalf("expected [REDACTED] marker in scrubbed request:\n%s", sent)
		}

		// Explicitly typed token in prompt is NOT stripped.
		out, changed := scrubAutoAttachedSecrets("key=sk-explicit123456", "explain sk-explicit123456 format")
		if changed || out != "key=sk-explicit123456" {
			t.Fatalf("explicitly typed secret should not be scrubbed: got (%q, %v)", out, changed)
		}
	})
}
