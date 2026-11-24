package template

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func renderContract() Contract {
	return Contract{
		SchemaVersion: ContractSchema,
		Locale:        "en",
		Inputs: []InputDefinition{
			{Name: "name", Type: "string", Required: true},
			{Name: "tone", Type: "string"},
		},
	}
}

func TestRenderFailsClosedWithStructuredDiagnostics(t *testing.T) {
	c := renderContract()
	cases := []struct {
		name   string
		body   string
		values map[string]string
		code   Code
		path   string
	}{
		{"undeclared placeholder", "Hello {{secret}}", nil, UndeclaredPlaceholder, "secret"},
		{"missing required", "Hello {{name}}", nil, MissingRequired, "name"},
		{"malformed body", "Hello {{name", map[string]string{"name": "Clavis"}, InvalidBody, "body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderContract(c, tc.body, tc.values, nil)
			if got.RenderedText != "" {
				t.Fatalf("rendered text must be empty on failure: %q", got.RenderedText)
			}
			if len(got.Diagnostics) == 0 || got.Diagnostics[0].Code != tc.code || got.Diagnostics[0].Path != tc.path {
				t.Fatalf("diagnostics = %+v, want code=%s path=%s", got.Diagnostics, tc.code, tc.path)
			}
			encoded := RenderDiagnosticString(got.Diagnostics[0])
			if strings.Contains(encoded, "Hello") || strings.Contains(encoded, "Clavis") {
				t.Fatalf("diagnostic leaked template/value: %q", encoded)
			}
		})
	}
}

func TestRenderOptionalMissingAndDeclaredBuiltinConflict(t *testing.T) {
	c := Contract{SchemaVersion: ContractSchema, Locale: "en", Inputs: []InputDefinition{{Name: "app", Type: "string"}}}
	got := RenderContract(c, "{{app}} {{selection}}", map[string]string{"app": "contract"}, map[string]string{"selection": "ambient"})
	if got.RenderedText != "contract ambient" {
		t.Fatalf("rendered text = %q", got.RenderedText)
	}
	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != BuiltinConflict {
		t.Fatalf("diagnostics = %+v", got.Diagnostics)
	}

	optional := RenderContract(renderContract(), "A{{tone}}B", map[string]string{"name": "ok"}, nil)
	if optional.RenderedText != "AB" || !reflect.DeepEqual(optional.Missing, []string{"tone"}) || len(optional.Diagnostics) != 0 {
		t.Fatalf("optional result = %+v", optional)
	}
}

type fakeAIExecutor struct {
	calls int
	err   error
}

func (f *fakeAIExecutor) Execute(_ context.Context, _ string) error {
	f.calls++
	return f.err
}

func TestExecuteRenderedAIRequiresExplicitReviewPermissionAndConfirmation(t *testing.T) {
	fake := &fakeAIExecutor{}
	c := renderContract()
	rendered := RenderContract(c, "Hello {{name}}", map[string]string{"name": "Clavis"}, nil)
	if len(rendered.Diagnostics) != 0 || rendered.RenderedText == "" {
		t.Fatalf("render result = %+v", rendered)
	}
	failedRender := RenderContract(c, "Hello {{unknown}}", nil, nil)
	if err := c.ExecuteRenderedAI(context.Background(), failedRender, true, fake); err == nil {
		t.Fatal("failed render must be rejected before execution")
	}
	if fake.calls != 0 {
		t.Fatalf("fake provider calls after failed render = %d, want 0", fake.calls)
	}

	for _, tc := range []struct {
		name      string
		contract  Contract
		confirmed bool
		wantCode  Code
	}{
		{"missing permission", c, true, ExecutionPermissionDenied},
		{"not confirmed", Contract{SchemaVersion: ContractSchema, Locale: "en", Permissions: []string{ExecuteRequiresReview}}, false, ExecutionReviewRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.contract.ExecuteRenderedAI(context.Background(), rendered, tc.confirmed, fake); err == nil {
				t.Fatal("expected execution to be denied")
			} else {
				var executionErr *ExecutionError
				if !errors.As(err, &executionErr) || executionErr.Code != tc.wantCode {
					t.Fatalf("error = %v, want %s", err, tc.wantCode)
				}
			}
			if fake.calls != 0 {
				t.Fatalf("fake provider calls = %d, want 0", fake.calls)
			}
		})
	}
}

func TestExecuteRenderedAIInvokesOnceAfterFreshConfirmation(t *testing.T) {
	fake := &fakeAIExecutor{}
	c := renderContract()
	c.Permissions = []string{ExecuteRequiresReview}
	rendered := RenderContract(c, "Hello {{name}}", map[string]string{"name": "Clavis"}, nil)
	if err := c.ExecuteRenderedAI(context.Background(), rendered, true, fake); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatalf("fake provider calls = %d, want 1", fake.calls)
	}
}

func TestRenderMultiLocaleChineseAndUnsupportedLocales(t *testing.T) {
	zhContract := Contract{
		SchemaVersion: ContractSchema,
		Locale:        "zh-CN",
		Permissions:   []string{"preview", ExecuteRequiresReview},
		Inputs: []InputDefinition{
			{Name: "topic", Type: "string", Required: true, Labels: map[string]string{"zh-CN": "主题"}},
			{Name: "style", Type: "enum", Enum: []string{"简洁", "详细"}, Default: "简洁"},
		},
	}

	got := RenderContract(zhContract, "请用{{style}}风格介绍{{topic}}", map[string]string{"topic": "模板平台"}, nil)
	if len(got.Diagnostics) != 0 || got.RenderedText != "请用简洁风格介绍模板平台" {
		t.Fatalf("zh-CN render failed: %+v", got)
	}

	// Case-insensitive locale matching via RenderContractForLocale.
	ciGot := RenderContractForLocale(zhContract, "主题：{{topic}}", map[string]string{"topic": "多语言"}, nil, "zh-cn")
	if len(ciGot.Diagnostics) != 0 || ciGot.RenderedText != "主题：多语言" {
		t.Fatalf("case-insensitive zh-cn render failed: %+v", ciGot)
	}
	mismatch := RenderContractForLocale(zhContract, "主题：{{topic}}", map[string]string{"topic": "多语言"}, nil, "ja")
	if mismatch.RenderedText != "" || len(mismatch.Diagnostics) == 0 || mismatch.Diagnostics[0].Code != LocaleUnsupported {
		t.Fatalf("mismatched requested locale must fail closed: %+v", mismatch)
	}

	// ExecuteRenderedAI works for zh-CN contracts once review is explicitly confirmed.
	fake := &fakeAIExecutor{}
	if err := zhContract.ExecuteRenderedAI(context.Background(), got, false, fake); err == nil || fake.calls != 0 {
		t.Fatalf("unconfirmed zh-CN AI execution must be denied, err=%v calls=%d", err, fake.calls)
	}
	if err := zhContract.ExecuteRenderedAI(context.Background(), got, true, fake); err != nil || fake.calls != 1 {
		t.Fatalf("confirmed zh-CN AI execution failed: err=%v calls=%d", err, fake.calls)
	}

	// Empty, invalid, or explicitly disallowed locales fail closed.
	for _, bad := range []Contract{
		{SchemaVersion: ContractSchema, Locale: "", Inputs: zhContract.Inputs},
		{SchemaVersion: ContractSchema, Locale: "invalid_locale", Inputs: zhContract.Inputs},
		{SchemaVersion: ContractSchema, Locale: "ja", SupportedLocales: []string{"en", "zh-CN"}, Inputs: zhContract.Inputs},
	} {
		res := RenderContract(bad, "{{topic}}", map[string]string{"topic": "测试"}, nil)
		if res.RenderedText != "" || len(res.Diagnostics) == 0 || res.Diagnostics[0].Code != LocaleUnsupported {
			t.Fatalf("expected locale_unsupported for %+v, got %+v", bad, res)
		}
	}
}
