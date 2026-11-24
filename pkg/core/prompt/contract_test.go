package prompt

import (
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/template"
)

func testContract(locale string, inputs ...template.InputDefinition) template.Contract {
	return template.Contract{SchemaVersion: template.ContractSchema, Locale: locale, Inputs: inputs}
}

func hasContractCode(ds []ContractDiagnostic, code, name string) bool {
	for _, d := range ds {
		if d.Code == code && (name == "" || d.Name == name) {
			return true
		}
	}
	return false
}

func TestContractInputTypeMapping(t *testing.T) {
	min, max := -2.5, 4.5
	got := PromptSlotsFromContract(testContract("en",
		template.InputDefinition{Name: "text", Type: "string"},
		template.InputDefinition{Name: "choice", Type: "enum", Enum: []string{"a", "b"}},
		template.InputDefinition{Name: "integer", Type: "integer", Min: &min, Max: &max},
		template.InputDefinition{Name: "number", Type: "number"},
		template.InputDefinition{Name: "flag", Type: "boolean"},
	))
	if len(got) != 5 {
		t.Fatalf("mapped %d slots, want 5: %+v", len(got), got)
	}
	want := []struct {
		name, kind string
		options    []string
	}{
		{"text", "text", nil},
		{"choice", "select", []string{"a", "b"}},
		{"integer", "number", nil},
		{"number", "number", nil},
		{"flag", "select", []string{"true", "false"}},
	}
	for i, w := range want {
		if got[i].Name != w.name || got[i].Kind != w.kind || strings.Join(got[i].Options, "\x00") != strings.Join(w.options, "\x00") {
			t.Fatalf("slot[%d] = %+v, want %+v", i, got[i], w)
		}
	}
	if issues := config.ValidatePrompts(config.PromptsConfig{Items: []config.PromptTemplate{{ID: "n", Trigger: "/n", Slots: got}}}); len(issues) != 0 {
		t.Fatalf("number slots rejected: %+v", issues)
	}
}

func TestContractFailClosedSevenRules(t *testing.T) {
	min, max := 1.5, 3.5
	minLen, maxLen := 3, 6
	contract := testContract("en",
		template.InputDefinition{Name: "required", Type: "string", Required: true},
		template.InputDefinition{Name: "number", Type: "number", Min: &min, Max: &max},
		template.InputDefinition{Name: "code", Type: "string", Regex: `^[A-Z]+$`, MinLength: &minLen, MaxLength: &maxLen},
		template.InputDefinition{Name: "optional", Type: "string"},
		template.InputDefinition{Name: "app", Type: "string", Required: true},
	)

	t.Run("1 undeclared placeholder is rejected", func(t *testing.T) {
		got := RenderContractPrompt(contract, "{{unknown}}", nil, RenderContext{})
		if !hasContractCode(got.Diagnostics, "undeclared_placeholder", "unknown") {
			t.Fatalf("diagnostics = %+v", got.Diagnostics)
		}
	})
	t.Run("2 missing required is rejected", func(t *testing.T) {
		got := RenderContractPrompt(contract, "{{required}}", nil, RenderContext{})
		if !hasContractCode(got.Diagnostics, "missing_required", "required") {
			t.Fatalf("diagnostics = %+v", got.Diagnostics)
		}
	})
	t.Run("3 type and constraints are rejected without leaking value", func(t *testing.T) {
		got := RenderContractPrompt(contract, "{{number}} {{code}}", map[string]string{"number": "9.9", "code": "secret"}, RenderContext{})
		if !hasContractCode(got.Diagnostics, "input_invalid", "number") || !hasContractCode(got.Diagnostics, "input_invalid", "code") {
			t.Fatalf("diagnostics = %+v", got.Diagnostics)
		}
		for _, d := range got.Diagnostics {
			if strings.Contains(d.Message, "9.9") || strings.Contains(d.Message, "secret") {
				t.Fatalf("diagnostic leaked input: %+v", d)
			}
		}
	})
	t.Run("4 optional without default renders empty and is missing", func(t *testing.T) {
		optional := testContract("en", template.InputDefinition{Name: "optional", Type: "string"})
		got := RenderContractPrompt(optional, "A{{optional}}B", nil, RenderContext{})
		if got.RenderedText != "AB" || len(got.Missing) != 1 || got.Missing[0] != "optional" || len(got.Diagnostics) != 0 {
			t.Fatalf("result = %+v", got)
		}
	})
	t.Run("5 missing contract remains explicitly degraded", func(t *testing.T) {
		items, ds := template.MergeView(nil, nil, []template.Item{{ID: "x", ContractStatus: "header_only_unvalidated"}})
		if len(ds) != 0 || len(items) != 1 || items[0].ContractStatus != "header_only_unvalidated" {
			t.Fatalf("unexpected imported item state: items=%+v diagnostics=%+v", items, ds)
		}
		local, _ := template.MergeView([]template.Item{{ID: "x"}}, nil, nil)
		if local[0].ContractStatus != "degraded_no_contract" {
			t.Fatalf("local no-contract status = %q", local[0].ContractStatus)
		}
	})
	t.Run("6 contract input wins over same-name builtin and diagnoses conflict", func(t *testing.T) {
		builtin := testContract("en", template.InputDefinition{Name: "app", Type: "string"})
		got := RenderContractPrompt(builtin, "{{app}}", map[string]string{"app": "contract-value"}, RenderContext{App: "builtin-value"})
		if !hasContractCode(got.Diagnostics, "builtin_conflict", "app") || got.RenderedText != "contract-value" {
			t.Fatalf("result = %+v", got)
		}
	})
	t.Run("7 multi-locale zh-CN renders and invalid or disallowed locales fail closed", func(t *testing.T) {
		zh := template.Contract{
			SchemaVersion: template.ContractSchema,
			Locale:        "zh-CN",
			Inputs: []template.InputDefinition{
				{Name: "topic", Type: "string", Required: true, Labels: map[string]string{"zh-CN": "主题", "en": "Topic"}},
				{Name: "tone", Type: "enum", Enum: []string{"正式", "口语"}, Default: "正式"},
			},
		}
		slots := PromptSlotsFromContract(zh)
		if len(slots) != 2 || slots[0].Label != "主题" || slots[1].Kind != "select" {
			t.Fatalf("zh-CN slots = %+v", slots)
		}
		got := RenderContractPrompt(zh, "请以{{tone}}语气撰写关于{{topic}}的摘要。", map[string]string{"topic": "Clavis 模板"}, RenderContext{})
		if len(got.Diagnostics) != 0 || got.RenderedText != "请以正式语气撰写关于Clavis 模板的摘要。" {
			t.Fatalf("zh-CN render result = %+v", got)
		}

		// Case-insensitive and other valid BCP-47 locales render cleanly.
		ja := testContract("ja-jp", template.InputDefinition{Name: "x", Type: "string"})
		if jaRes := RenderContractPrompt(ja, "入力:{{x}}", map[string]string{"x": "テスト"}, RenderContext{}); len(jaRes.Diagnostics) != 0 || jaRes.RenderedText != "入力:テスト" {
			t.Fatalf("ja-jp render result = %+v", jaRes)
		}

		// Empty, malformed, or explicitly disallowed locales fail closed with locale_unsupported.
		for _, bad := range []template.Contract{
			testContract("", template.InputDefinition{Name: "x", Type: "string"}),
			testContract("zh_CN", template.InputDefinition{Name: "x", Type: "string"}),
			{SchemaVersion: template.ContractSchema, Locale: "fr", SupportedLocales: []string{"en", "zh-CN"}, Inputs: []template.InputDefinition{{Name: "x", Type: "string"}}},
		} {
			badRes := RenderContractPrompt(bad, "{{x}}", map[string]string{"x": "值"}, RenderContext{})
			if !hasContractCode(badRes.Diagnostics, "locale_unsupported", "") || badRes.RenderedText != "" {
				t.Fatalf("expected locale_unsupported for %q, got %+v", bad.Locale, badRes)
			}
		}

		// RenderContractPromptForLocale matches case-insensitively and rejects mismatched locale.
		if okRes := RenderContractPromptForLocale(zh, "{{topic}}", map[string]string{"topic": "值"}, RenderContext{}, "zh-cn"); len(okRes.Diagnostics) != 0 || okRes.RenderedText != "值" {
			t.Fatalf("RenderContractPromptForLocale(zh-cn) = %+v", okRes)
		}
		if badReq := RenderContractPromptForLocale(zh, "{{topic}}", map[string]string{"topic": "值"}, RenderContext{}, "fr"); !hasContractCode(badReq.Diagnostics, "locale_unsupported", "") || badReq.RenderedText != "" {
			t.Fatalf("RenderContractPromptForLocale(fr) = %+v", badReq)
		}
	})
}
