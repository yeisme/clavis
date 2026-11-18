package prompt

import (
	"fmt"
	"github.com/yeisme/clavis/pkg/core/config"
	"strings"
)

// ThirdPartyTemplate is the deliberately narrow interchange shape used by remote
// catalogs. It does not execute remote templates or import their runtime.
type ThirdPartyTemplate struct {
	Ref, Version, Digest string
	Slots                []ThirdPartySlot
}
type ThirdPartySlot struct {
	Name, InputType string
	Required        bool
	Options         []string
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
