package engine_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

func TestTranslateDetectionAndOmniboxTrigger(t *testing.T) {
	if got := engine.DetectTranslateDirection("请把这段错误日志翻成英文"); got != engine.TranslateZhToEn {
		t.Fatalf("DetectTranslateDirection zh = %q", got)
	}
	if got := engine.DetectTranslateDirection("Refactor this mutex carefully"); got != engine.TranslateEnToZh {
		t.Fatalf("DetectTranslateDirection en = %q", got)
	}

	sm := engine.NewStateMachine(config.DefaultConfig())
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "tr Hello world"})
	st := sm.State()
	if len(st.OmniboxItems) == 0 || st.OmniboxItems[0].AIMode != "translate" || st.OmniboxItems[0].AISourceText != "Hello world" {
		t.Fatalf("expected top translate item, got %+v", st.OmniboxItems)
	}

	effs := sm.Handle(engine.Event{Type: engine.EventConfirmSelection})
	if len(effs) != 1 || effs[0].Type != engine.EffectStartAI || effs[0].Mode != "translate" || effs[0].SourceText != "Hello world" {
		t.Fatalf("expected EffectStartAI translate with SourceText, got %+v", effs)
	}
}

func TestDevMicroUtilitiesAndSensitivePW(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	// 1. JSON pretty + compact
	jItems := engine.EvaluateUtilities(`json {"b":2,"a":1}`, now)
	if len(jItems) < 2 || !strings.Contains(jItems[0].UtilityValue, "\n") || jItems[1].UtilityValue != `{"b":2,"a":1}` {
		t.Fatalf("unexpected json utility output: %+v", jItems)
	}

	// 2. URL encode / decode
	uItems := engine.EvaluateUtilities("url hello world&a=1", now)
	if len(uItems) == 0 || uItems[0].UtilityValue != "hello+world%26a%3D1" {
		t.Fatalf("unexpected url utility output: %+v", uItems)
	}
	udItems := engine.EvaluateUtilities("urld hello+world%26a%3D1", now)
	if len(udItems) == 0 || udItems[0].UtilityValue != "hello world&a=1" {
		t.Fatalf("unexpected urld utility output: %+v", udItems)
	}

	// 3. JWT claims decoding
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	pay := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user-42","exp":1893456000}`))
	jwtItems := engine.EvaluateUtilities("jwt "+hdr+"."+pay+".sig", now)
	if len(jwtItems) != 2 || !strings.Contains(jwtItems[0].UtilityValue, `"sub": "user-42"`) || !strings.Contains(jwtItems[0].Subtitle, "unverified") {
		t.Fatalf("unexpected jwt utility output: %+v", jwtItems)
	}

	// 4. Hash
	wantSHA := sha256.Sum256([]byte("clavis"))
	hItems := engine.EvaluateUtilities("hash clavis", now)
	if len(hItems) != 2 || hItems[0].UtilityValue != hex.EncodeToString(wantSHA[:]) {
		t.Fatalf("unexpected hash utility output: %+v", hItems)
	}

	// 5. Case conversion
	cItems := engine.EvaluateUtilities("case hello_world fooBar", now)
	if len(cItems) != 5 || cItems[0].Title != "helloWorldFooBar" || cItems[2].Title != "hello_world_foo_bar" {
		t.Fatalf("unexpected case utility output: %+v", cItems)
	}

	// 6. Regexp RE2
	reItems := engine.EvaluateUtilities(`re /v([0-9]+\.[0-9]+)/ release v0.9 and v1.2`, now)
	if len(reItems) != 1 || reItems[0].Title != "v0.9" || !strings.Contains(reItems[0].UtilityValue, "v1.2") {
		t.Fatalf("unexpected re utility output: %+v", reItems)
	}

	// 7. Sensitive password generation — Title stays masked, Effect carries Sensitive=true.
	sm := engine.NewStateMachine(config.DefaultConfig())
	_ = sm.Handle(engine.Event{Type: engine.EventOpenOmnibox, Tab: config.TabActions, Query: "pw 20"})
	pwItems := sm.State().OmniboxItems
	if len(pwItems) == 0 || !pwItems[0].UtilitySensitive || len(pwItems[0].UtilityValue) != 20 {
		t.Fatalf("unexpected pw item: %+v", pwItems)
	}
	if strings.Contains(pwItems[0].Title, pwItems[0].UtilityValue) || strings.Contains(pwItems[0].Subtitle, pwItems[0].UtilityValue) {
		t.Fatalf("pw leaked plaintext in Title/Subtitle: %+v", pwItems[0])
	}
	effs := sm.Handle(engine.Event{Type: engine.EventConfirmSelection})
	if len(effs) < 1 || effs[0].Type != engine.EffectCopyText || !effs[0].Sensitive || effs[0].Text != pwItems[0].UtilityValue {
		t.Fatalf("expected sensitive EffectCopyText, got %+v", effs)
	}
	_ = json.Valid
}
