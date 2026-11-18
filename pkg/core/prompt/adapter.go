package prompt

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yeisme/clavis/pkg/core/config"
)

// ThirdPartyTemplate is the deliberately narrow interchange shape used by remote
// catalogs. It does not execute remote templates or import their runtime.
type ThirdPartyTemplate struct {
	Ref, Version, Digest string
	Slots                []ThirdPartySlot
}

// UnmarshalJSON accepts the documented templateRef field and the historical ref spelling.
func (t *ThirdPartyTemplate) UnmarshalJSON(data []byte) error {
	var raw struct {
		TemplateRef string           `json:"templateRef"`
		Ref         string           `json:"ref"`
		Version     string           `json:"version"`
		Digest      string           `json:"digest"`
		Slots       []ThirdPartySlot `json:"slots"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	t.Ref = strings.TrimSpace(raw.TemplateRef)
	if t.Ref == "" {
		t.Ref = strings.TrimSpace(raw.Ref)
	}
	t.Version, t.Digest, t.Slots = raw.Version, raw.Digest, raw.Slots
	return nil
}

type ThirdPartySlot struct {
	Name, InputType string
	Required        bool
	Options         []string
}

func (s *ThirdPartySlot) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name       string   `json:"name"`
		InputType  string   `json:"inputType"`
		LegacyType string   `json:"input_type"`
		Required   bool     `json:"required"`
		Options    []string `json:"options"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.Name, s.InputType, s.Required, s.Options = raw.Name, raw.InputType, raw.Required, raw.Options
	if s.InputType == "" {
		s.InputType = raw.LegacyType
	}
	return nil
}

// AdaptThirdParty converts metadata-only catalog slots into a Clavis template.
// It refuses unknown control constructs because silently emulating them is unsafe.
func AdaptThirdParty(t ThirdPartyTemplate) (config.PromptTemplate, error) {
	if strings.TrimSpace(t.Ref) == "" {
		return config.PromptTemplate{}, fmt.Errorf("template adapter: missing ref")
	}
	out := config.PromptTemplate{ID: t.Ref}
	for _, s := range t.Slots {
		kind := s.InputType
		if kind == "textarea" {
			kind = "text"
		}
		if kind == "enum" {
			kind = "select"
		}
		if kind != "text" && kind != "select" {
			return config.PromptTemplate{}, fmt.Errorf("template adapter: unsupported input type %q", s.InputType)
		}
		out.Slots = append(out.Slots, config.PromptSlot{Name: s.Name, Label: s.Name, Kind: kind, Options: append([]string(nil), s.Options...), Required: s.Required})
	}
	return out, nil
}
