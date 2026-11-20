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
