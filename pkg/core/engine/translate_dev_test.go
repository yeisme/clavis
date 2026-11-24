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

func TestTranslatePureLogicAndEdgePaths(t *testing.T) {
	// NormalizeTranslateTarget covers English, Simplified/Traditional Chinese, Japanese, Korean, and passthrough.
	targetCases := []struct {
		in   string
		want string
	}{
		{" EN ", "English"},
		{"英语", "English"},
		{"zh-cn", "Simplified Chinese"},
		{"简体中文", "Simplified Chinese"},
		{"繁体", "Traditional Chinese"},
		{"jp", "Japanese"},
		{"日语", "Japanese"},
		{"韩语", "Korean"},
		{"  French ", "French"},
	}
	for _, tc := range targetCases {
		if got := engine.NormalizeTranslateTarget(tc.in); got != tc.want {
			t.Fatalf("NormalizeTranslateTarget(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// DetectTranslateDirection handles empty/punctuation/Kana-only as auto->zh and mixed Han+Latin as zh->en.
	if got := engine.DetectTranslateDirection("12345 !@# 🚀"); got != engine.TranslateAutoToZh {
		t.Fatalf("punctuation DetectTranslateDirection = %q", got)
	}
	if got := engine.DetectTranslateDirection("修复 mutex 死锁"); got != engine.TranslateZhToEn {
		t.Fatalf("mixed Han+Latin DetectTranslateDirection = %q", got)
	}

	// BuildTranslatePrompt handles explicit and empty direction fallback cleanly.
	sys, instr := engine.BuildTranslatePrompt("Hello world", "")
	if !strings.Contains(sys, "Simplified Chinese") || !strings.Contains(sys, "Markdown") || instr == "" {
		t.Fatalf("BuildTranslatePrompt auto en = (%q, %q)", sys, instr)
	}
	sysAuto, _ := engine.BuildTranslatePrompt("12345", engine.TranslateAutoToZh)
	if !strings.Contains(sysAuto, "already Chinese") {
		t.Fatalf("BuildTranslatePrompt auto->zh = %q", sysAuto)
	}

	// ParseTranslateQuery supports bare triggers, colon/space prefixes, fullwidth prefixes, and rejects unrelated terms.
	for _, bare := range []string{"tr", "fy", "翻译", "translate"} {
		txt, ok := engine.ParseTranslateQuery(bare)
		if !ok || txt != "" {
			t.Fatalf("ParseTranslateQuery(%q) = (%q, %v), want (\"\", true)", bare, txt, ok)
		}
	}
	for _, tc := range []struct {
		q, want string
	}{
		{"TR: hello world", "hello world"},
		{"fy:需要翻译", "需要翻译"},
		{"翻译:你好", "你好"},
		{"ｔｒ：  全角前缀", "全角前缀"},
	} {
		txt, ok := engine.ParseTranslateQuery(tc.q)
		if !ok || txt != tc.want {
			t.Fatalf("ParseTranslateQuery(%q) = (%q, %v), want (%q, true)", tc.q, txt, ok, tc.want)
		}
	}
	for _, miss := range []string{"", "   ", "train station", "translation-mode"} {
		if _, ok := engine.ParseTranslateQuery(miss); ok {
			t.Fatalf("ParseTranslateQuery(%q) should not match", miss)
		}
	}

	// MakeTranslateOmniboxItem falls back Selection -> Clipboard -> empty and truncates >40 rune previews.
	item, ok := engine.MakeTranslateOmniboxItem("tr", engine.CapsuleContext{
		SelectedText:     "selected text",
		ClipboardPreview: "clipboard text",
	})
	if !ok || item.AISourceText != "selected text" {
		t.Fatalf("expected SelectedText priority, got %+v ok=%v", item, ok)
	}
	item, ok = engine.MakeTranslateOmniboxItem("fy", engine.CapsuleContext{ClipboardPreview: "clip fallback"})
	if !ok || item.AISourceText != "clip fallback" {
		t.Fatalf("expected ClipboardPreview fallback, got %+v ok=%v", item, ok)
	}
	long := strings.Repeat("字", 45)
	longItem, ok := engine.MakeTranslateOmniboxItem("翻译 "+long, engine.CapsuleContext{})
	if !ok || !strings.HasSuffix(longItem.Title, "…") {
		t.Fatalf("expected truncated preview in Title, got %+v ok=%v", longItem, ok)
	}
}

func TestDevUtilitiesErrorAndBoundaryPaths(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	// Password boundaries: min clamp 8, max clamp 64, invalid non-numeric length -> nil.
	if items := engine.EvaluateUtilities("pw 3", now); len(items) != 1 || len(items[0].UtilityValue) != 8 {
		t.Fatalf("expected min clamp 8 for pw 3, got %+v", items)
	}
	if items := engine.EvaluateUtilities("pass 128", now); len(items) != 1 || len(items[0].UtilityValue) != 64 {
		t.Fatalf("expected max clamp 64 for pass 128, got %+v", items)
	}
	if items := engine.EvaluateUtilities("pw notanumber", now); len(items) != 0 {
		t.Fatalf("expected nil for invalid pw length, got %+v", items)
	}

	// JSON invalid vs bare object detection vs empty.
	if items := engine.EvaluateUtilities(`json {"broken":`, now); len(items) != 1 || items[0].ID != "utility:json:invalid" {
		t.Fatalf("expected utility:json:invalid, got %+v", items)
	}
	if items := engine.EvaluateUtilities("json ", now); len(items) != 0 {
		t.Fatalf("expected nil for bare json trigger without payload, got %+v", items)
	}
	if items := engine.EvaluateUtilities(`{"auto":true}`, now); len(items) != 2 {
		t.Fatalf("expected bare JSON object auto-eval, got %+v", items)
	}

	// URL encode single item when path==query, and decode error for malformed %ZZ.
	if items := engine.EvaluateUtilities("url abc", now); len(items) != 1 {
		t.Fatalf("expected single url item when no spaces, got %+v", items)
	}
	if items := engine.EvaluateUtilities("urld %ZZ", now); len(items) != 1 || items[0].ID != "utility:urld:invalid" {
		t.Fatalf("expected utility:urld:invalid, got %+v", items)
	}

	// Standalone sha256 / md5 & empty input guards.
	if items := engine.EvaluateUtilities("sha256 abc", now); len(items) != 1 || items[0].ID != "utility:hash:sha256" {
		t.Fatalf("expected sha256 single item, got %+v", items)
	}
	if items := engine.EvaluateUtilities("md5 abc", now); len(items) != 1 || items[0].ID != "utility:hash:md5" {
		t.Fatalf("expected md5 single item, got %+v", items)
	}
	if items := engine.EvaluateUtilities("hash   ", now); len(items) != 0 {
		t.Fatalf("expected nil for empty hash, got %+v", items)
	}

	// JWT malformed segment count / expired claim / invalid base64.
	if items := engine.EvaluateUtilities("jwt only.two", now); len(items) != 0 {
		t.Fatalf("expected nil for invalid jwt segment count, got %+v", items)
	}
	if items := engine.EvaluateUtilities("jwt !!!.@@@.###", now); len(items) != 0 {
		t.Fatalf("expected nil for invalid jwt base64, got %+v", items)
	}
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`))
	expired := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"old","exp":1000}`))
	if items := engine.EvaluateUtilities("eyJ." + expired + ".sig", now); len(items) != 0 {
		t.Fatalf("bare invalid eyJ header should fail cleanly, got %+v", items)
	}
	if items := engine.EvaluateUtilities("jwt "+hdr+"."+expired+".sig", now); len(items) != 2 || !strings.Contains(items[0].Subtitle, "expired") {
		t.Fatalf("expected expired status in jwt subtitle, got %+v", items)
	}

	// Case conversion empty & acronyms.
	if items := engine.EvaluateUtilities("case ---", now); len(items) != 0 {
		t.Fatalf("expected nil for separator-only case input, got %+v", items)
	}
	if items := engine.EvaluateUtilities("case HTTPServerAPI_v2", now); len(items) != 5 || items[2].Title != "http_server_api_v2" {
		t.Fatalf("expected acronym snake_case http_server_api_v2, got %+v", items)
	}

	// Regex error, ready (empty tail), 0-match, escaped delimiter, and flags (i/m).
	if items := engine.EvaluateUtilities("re no-slash", now); len(items) != 0 {
		t.Fatalf("expected nil for regex without leading slash, got %+v", items)
	}
	if items := engine.EvaluateUtilities("re /(/ abc", now); len(items) != 1 || items[0].ID != "utility:re:error" {
		t.Fatalf("expected utility:re:error, got %+v", items)
	}
	if items := engine.EvaluateUtilities("re /^foo/im", now); len(items) != 1 || items[0].ID != "utility:re:ready" {
		t.Fatalf("expected utility:re:ready, got %+v", items)
	}
	if items := engine.EvaluateUtilities("re /xyz/ abc", now); len(items) != 1 || items[0].ID != "utility:re:nomatch" {
		t.Fatalf("expected utility:re:nomatch, got %+v", items)
	}
	if items := engine.EvaluateUtilities(`re /a\/(b)/i A/B`, now); len(items) != 1 || items[0].Title != "A/B" || !strings.Contains(items[0].Subtitle, "group[1]=B") {
		t.Fatalf("expected escaped slash & case-insensitive group match, got %+v", items)
	}
}
