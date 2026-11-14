package prompt

import (
	"github.com/yeisme/clavis/pkg/core/config"
)

// SlotComposerSession is the pure state of an inline prompt composer.
type SlotComposerSession struct {
	Template        config.PromptTemplate
	Parsed          ParsedTemplate
	Slots           []config.PromptSlot
	ActiveSlotIndex int
	Values          map[string]string
	Context         RenderContext
}

func NewSlotComposerSession(t config.PromptTemplate, ctx RenderContext) *SlotComposerSession {
	p := ParseTemplate(t.Template)
	slots := p.Slots(t)
	vals := map[string]string{}
	for _, s := range slots {
		if s.Default != "" {
			vals[s.Name] = s.Default
		}
	}
	return &SlotComposerSession{Template: t, Parsed: p, Slots: slots, Values: vals, Context: ctx}
}
func (s *SlotComposerSession) Preview() PromptRenderResult {
	return RenderPrompt(s.Template, s.Values, s.Context)
}
func (s *SlotComposerSession) CanCommit() bool { return len(s.Preview().MissingRequired) == 0 }
func (s *SlotComposerSession) SetValue(value string) {
	if s.ActiveSlotIndex >= 0 && s.ActiveSlotIndex < len(s.Slots) {
		if s.Values == nil {
			s.Values = map[string]string{}
		}
		s.Values[s.Slots[s.ActiveSlotIndex].Name] = value
	}
}
func (s *SlotComposerSession) MoveNext() {
	if len(s.Slots) > 0 {
		s.ActiveSlotIndex = (s.ActiveSlotIndex + 1) % len(s.Slots)
	}
}
func (s *SlotComposerSession) MovePrevious() {
	if len(s.Slots) > 0 {
		s.ActiveSlotIndex = (s.ActiveSlotIndex + len(s.Slots) - 1) % len(s.Slots)
	}
}

// HandleKey accepts Tab, Shift+Tab, Left and Right.
func (s *SlotComposerSession) HandleKey(key string) {
	switch key {
	case "Tab", "tab":
		s.MoveNext()
	case "Shift+Tab", "shift+tab":
		s.MovePrevious()
	case "Left", "left":
		s.cycle(-1)
	case "Right", "right":
		s.cycle(1)
	}
}
func (s *SlotComposerSession) cycle(d int) {
	if s.ActiveSlotIndex < 0 || s.ActiveSlotIndex >= len(s.Slots) {
		return
	}
	slot := s.Slots[s.ActiveSlotIndex]
	if slot.Kind != "select" || len(slot.Options) == 0 {
		return
	}
	cur := s.Values[slot.Name]
	idx := 0
	for i, v := range slot.Options {
		if v == cur {
			idx = i
			break
		}
	}
	idx = (idx + d + len(slot.Options)) % len(slot.Options)
	s.Values[slot.Name] = slot.Options[idx]
}
