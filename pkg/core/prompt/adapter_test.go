package prompt_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeisme/clavis/pkg/core/prompt"
)

func TestAdaptThirdPartyDecodesDocumentedTemplateRef(t *testing.T) {
	var got prompt.ThirdPartyTemplate
	raw := []byte(`{"templateRef":"catalog/review","version":"2.1","digest":"sha256:abc","slots":[{"name":"notes","inputType":"textarea","required":true},{"name":"priority","inputType":"enum","options":["low","high"]}]}`)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Ref != "catalog/review" || got.Version != "2.1" || got.Digest != "sha256:abc" {
		t.Fatalf("metadata not preserved: %+v", got)
	}
	adapted, err := prompt.AdaptThirdParty(got)
	if err != nil {
		t.Fatalf("AdaptThirdParty: %v", err)
	}
	if adapted.ID != "catalog/review" || len(adapted.Slots) != 2 {
		t.Fatalf("unexpected template: %+v", adapted)
	}
	if adapted.Slots[0].Kind != "text" || !adapted.Slots[0].Required {
		t.Fatalf("textarea not mapped: %+v", adapted.Slots[0])
	}
	if adapted.Slots[1].Kind != "select" || len(adapted.Slots[1].Options) != 2 {
		t.Fatalf("enum not mapped: %+v", adapted.Slots[1])
	}
	adapted.Slots[1].Options[0] = "changed"
	if got.Slots[1].Options[0] != "low" {
		t.Fatal("slot options were not copied")
	}
}

func TestThirdPartyTemplateJSONRoundTripUsesDocumentedFields(t *testing.T) {
	original := prompt.ThirdPartyTemplate{Ref: "catalog/review", Version: "2.1", Digest: "sha256:abc", Slots: []prompt.ThirdPartySlot{{Name: "notes", InputType: "textarea", Required: true}}}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"templateRef", "version", "digest", "slots", "inputType"} {
		if !strings.Contains(string(data), `"`+field+`"`) {
			t.Fatalf("missing documented field %q: %s", field, data)
		}
	}
	var decoded prompt.ThirdPartyTemplate
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Ref != original.Ref || decoded.Version != original.Version || decoded.Digest != original.Digest || decoded.Slots[0].InputType != "textarea" {
		t.Fatalf("round trip mismatch: %+v", decoded)
	}
}

func TestAdaptThirdPartyAcceptsLegacyRefAndRejectsInvalidMetadata(t *testing.T) {
	var legacy prompt.ThirdPartyTemplate
	if err := json.Unmarshal([]byte(`{"ref":"legacy/ref","slots":[{"name":"body","input_type":"text"}]}`), &legacy); err != nil {
		t.Fatalf("legacy Unmarshal: %v", err)
	}
	if legacy.Ref != "legacy/ref" {
		t.Fatalf("legacy ref = %q", legacy.Ref)
	}
	if _, err := prompt.AdaptThirdParty(prompt.ThirdPartyTemplate{}); err == nil || !strings.Contains(err.Error(), "missing ref") {
		t.Fatalf("expected missing ref error, got %v", err)
	}
	_, err := prompt.AdaptThirdParty(prompt.ThirdPartyTemplate{Ref: "x", Slots: []prompt.ThirdPartySlot{{Name: "file", InputType: "file"}}})
	if err == nil || !strings.Contains(err.Error(), "unsupported input type") {
		t.Fatalf("expected unsupported type error, got %v", err)
	}
}
