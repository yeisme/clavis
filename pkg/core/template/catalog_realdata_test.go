package template

import (
	"encoding/json"
	"os"
	"testing"
)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// TestRealCatalogDigestsMatchDeclared pins the digest protocol against the authoritative
// catalog produced by template-registry on s101. The synthetic fixtures cannot catch a
// struct-tag mistake (a missing omitempty or a dropped field such as aliases), because the
// fixture is authored from the same mistaken assumptions. This file is the regression net
// that actually caught the Locale.Usage/Aliases mismatch.
//
// The snippet is a verbatim slice of data/yeisme-prompt-templates/catalog.json, including
// its declared digests, so a tag change fails here immediately.
func TestRealCatalogDigestsMatchDeclared(t *testing.T) {
	raw, err := os.ReadFile("testdata/real/catalog-slice.json")
	if err != nil {
		t.Skipf("real catalog slice not present: %v", err)
	}
	c, diags := ParseCatalog(raw)
	if c == nil {
		t.Fatalf("parse returned nil catalog: %v", diags)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics on a verbatim catalog slice: %v", diags)
	}
	if err := VerifyCatalogDigest(*c); err != nil {
		t.Fatalf("catalog digest mismatch against declared value: %v", err)
	}
	for i := range c.Solutions {
		if err := VerifySolutionDigest(c.Solutions[i]); err != nil {
			t.Fatalf("solution %s digest mismatch: %v", c.Solutions[i].Key(), err)
		}
	}
	if len(c.Solutions) == 0 {
		t.Fatal("slice parsed zero solutions")
	}
}

// TestLocaleAliasesAndOptionalUsageRoundTrip guards the exact fields the real catalog
// exercises: locales without usage, locales with aliases.
func TestLocaleAliasesAndOptionalUsageRoundTrip(t *testing.T) {
	withAliases := Locale{Title: "t", Summary: "s", Aliases: []string{"a1", "a2"}}
	noUsage := Locale{Title: "t", Summary: "s"}

	got, err := canonicalJSONOf(withAliases)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"title":"t","summary":"s","aliases":["a1","a2"]}`; got != want {
		t.Fatalf("aliases locale = %s, want %s", got, want)
	}
	got, err = canonicalJSONOf(noUsage)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"title":"t","summary":"s"}`; got != want {
		t.Fatalf("usage-less locale = %s, want %s (usage must stay omitempty)", got, want)
	}
}

func canonicalJSONOf(v any) (string, error) {
	b, err := jsonMarshal(v)
	return string(b), err
}
