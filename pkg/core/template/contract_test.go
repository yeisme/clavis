package template

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestContractCanonicalDigestAndBinding(t *testing.T) {
	body := []byte("Hello {{name}}")
	s := Solution{PackageID: "pkg", ID: "solution", Version: "1"}
	tmpl := Template{Role: "main", Locale: "en", Path: "solutions/pkg/prompts/main.en.md", Digest: Digest(body)}
	min := 1
	c := Contract{
		SchemaVersion: ContractSchema, PackageID: s.PackageID, SolutionID: s.ID, Version: s.Version,
		Role: tmpl.Role, Locale: tmpl.Locale, TemplatePath: tmpl.Path, TemplateDigest: tmpl.Digest,
		License: "MIT", Permissions: []string{"execute", "preview"},
		Inputs: []InputDefinition{{Name: "name", Type: "string", Required: true, MinLength: &min}},
	}
	c.Digest, _ = CanonicalContractDigest(c)
	if err := VerifyContractDigest(c); err != nil {
		t.Fatal(err)
	}
	if ds := ValidateContractBinding(c, s, tmpl, body); len(ds) != 0 {
		t.Fatalf("binding diagnostics: %v", ds)
	}

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	parsed, ds := ParseContract(data)
	if parsed == nil || len(ds) != 0 {
		t.Fatalf("parse diagnostics: %v", ds)
	}
	if !reflect.DeepEqual(parsed.Inputs, c.Inputs) {
		t.Fatalf("parsed inputs differ: %#v %#v", parsed.Inputs, c.Inputs)
	}
	wrong := body[:len(body)-1]
	if ds := ValidateContractBinding(c, s, tmpl, wrong); len(ds) == 0 {
		t.Fatal("tampered body accepted")
	}
}

func TestContractCanonicalSortDoesNotMutate(t *testing.T) {
	c := Contract{SchemaVersion: ContractSchema, Permissions: []string{"z", "a"}, Inputs: []InputDefinition{{Name: "z"}, {Name: "a"}}}
	beforePermissions := append([]string(nil), c.Permissions...)
	beforeInputs := append([]InputDefinition(nil), c.Inputs...)
	if _, err := CanonicalContractDigest(c); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Permissions, beforePermissions) || !reflect.DeepEqual(c.Inputs, beforeInputs) {
		t.Fatal("canonical digest mutated caller-owned slices")
	}
}

func TestNormalizeAndValidateMultiLocaleContracts(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"en", "en", true},
		{"EN", "en", true},
		{"zh", "zh", true},
		{"zh-CN", "zh-CN", true},
		{"zh-cn", "zh-CN", true},
		{"zh-hans-cn", "zh-Hans-CN", true},
		{"ja-jp", "ja-JP", true},
		{"es-419", "es-419", true},
		{"", "", false},
		{"   ", "", false},
		{"en_US", "", false},
		{"../zh", "", false},
		{"123", "", false},
		{"a", "", false},
	} {
		got, ok := NormalizeLocale(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("NormalizeLocale(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}

	body := []byte("请总结：{{topic}}")
	c := Contract{
		SchemaVersion:    ContractSchema,
		PackageID:        "demo/writing",
		SolutionID:       "zh-summary",
		Version:          "1.0.0",
		Role:             "main",
		Locale:           "zh-CN",
		TemplatePath:     "solutions/writing/zh-summary/prompts/main.zh-CN.md",
		TemplateDigest:   Digest(body),
		License:          "MIT",
		Permissions:      []string{"preview", ExecuteRequiresReview},
		SupportedLocales: []string{"en", "zh-CN"},
		Inputs: []InputDefinition{{
			Name:     "topic",
			Type:     "string",
			Required: true,
			Labels:   map[string]string{"en": "Topic", "zh-CN": "主题"},
		}},
	}
	c.Digest, _ = CanonicalContractDigest(c)
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	parsed, ds := ParseContract(raw)
	if parsed == nil || len(ds) != 0 {
		t.Fatalf("ParseContract(zh-CN) failed: %+v", ds)
	}
	if got := parsed.Inputs[0].LocalizedLabel("zh-cn"); got != "主题" {
		t.Fatalf("LocalizedLabel(zh-cn) = %q, want 主题", got)
	}
	if !parsed.SupportsLocale("zh-cn") || !parsed.SupportsLocale("EN") || parsed.SupportsLocale("fr") {
		t.Fatalf("SupportsLocale mismatch for %+v", parsed.SupportedLocales)
	}

	// Disallowed locale outside SupportedLocales must fail closed.
	disallowed := c
	disallowed.Locale = "ja"
	disallowed.Digest, _ = CanonicalContractDigest(disallowed)
	badRaw, _ := json.Marshal(disallowed)
	if got, badDS := ParseContract(badRaw); got != nil || len(badDS) == 0 || badDS[0].Code != LocaleUnsupported {
		t.Fatalf("disallowed locale should fail closed with locale_unsupported, got=%v ds=%+v", got, badDS)
	}
}
