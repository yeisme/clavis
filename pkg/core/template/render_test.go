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
