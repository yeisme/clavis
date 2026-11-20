package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yeisme/clavis/pkg/core/toml"
)

type PromptSlotKind string

const (
	PromptSlotText       PromptSlotKind = "text"
	PromptSlotSelect     PromptSlotKind = "select"
	PromptSlotNumber     PromptSlotKind = "number"
	PromptSlotKindText                  = PromptSlotText
	PromptSlotKindSelect                = PromptSlotSelect
	PromptSlotKindNumber                = PromptSlotNumber
)

type PromptSlot struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"`
	Default  string   `json:"default"`
	Options  []string `json:"options"`
	Required bool     `json:"required"`
}
type PromptTemplate struct {
	ID              string       `json:"id"`
	Trigger         string       `json:"trigger"`
	Title           string       `json:"title"`
	Category        string       `json:"category"`
	Description     string       `json:"description"`
	Template        string       `json:"template"`
	OptimizerPreset string       `json:"optimizerPreset"`
	InjectGlossary  bool         `json:"injectGlossary"`
	AppBundleIDs    []string     `json:"appBundleIds"`
	Slots           []PromptSlot `json:"slots"`
}
type PromptsConfig struct {
	Items                  []PromptTemplate `json:"items"`
	DefaultOptimizerPreset string           `json:"defaultOptimizerPreset"`
	InlineOptimizePrefix   string           `json:"inlineOptimizePrefix"`
}

func DefaultPromptsConfig() PromptsConfig {
	return PromptsConfig{DefaultOptimizerPreset: "coding", InlineOptimizePrefix: "//", Items: []PromptTemplate{
		{ID: "code-review", Trigger: "/cr", Title: "Code Review", Category: "coding", Description: "Review code for correctness, security, and maintainability.", Template: "Review the following {{language}} code. Identify correctness, security, performance, and maintainability issues. Rank findings by severity and provide concrete fixes.\n\nCode:\n{{code}}", OptimizerPreset: "coding", Slots: []PromptSlot{{Name: "language", Label: "Language", Kind: "text", Default: "", Required: true}, {Name: "code", Label: "Code", Kind: "text", Required: true}}},
		{ID: "architecture-spec", Trigger: "/arch", Title: "Architecture Spec", Category: "coding", Description: "Turn a problem statement into an implementable architecture proposal.", Template: "Create an architecture specification for {{project}}. State assumptions, goals, non-goals, components, data flows, API contracts, failure modes, security, observability, and a staged rollout.\n\nProblem:\n{{problem}}", OptimizerPreset: "deep_research", Slots: []PromptSlot{{Name: "project", Label: "Project", Kind: "text", Required: true}, {Name: "problem", Label: "Problem", Kind: "text", Required: true}}},
		{ID: "root-cause-debug", Trigger: "/bug", Title: "Root-Cause Debug", Category: "coding", Description: "Diagnose a bug from symptoms, evidence, and reproduction steps.", Template: "Diagnose this bug. Separate observed facts from hypotheses, identify the most likely root cause, propose a minimal reproduction, and give a safe fix plus regression tests.\n\nSymptoms and evidence:\n{{evidence}}", OptimizerPreset: "coding", Slots: []PromptSlot{{Name: "evidence", Label: "Symptoms and evidence", Kind: "text", Required: true}}},
		{ID: "prompt-upgrader", Trigger: "/opt", Title: "Structured Prompt Upgrader", Category: "productivity", Description: "Rewrite a rough request into a precise, testable prompt.", Template: "Rewrite the request below into a clear prompt with role, context, constraints, deliverables, and acceptance criteria. Preserve intent and flag missing information.\n\nRequest:\n{{request}}", OptimizerPreset: "concise", Slots: []PromptSlot{{Name: "request", Label: "Request", Kind: "text", Required: true}}},
		{ID: "technical-english", Trigger: "/en", Title: "Bilingual Technical English Polish", Category: "writing", Description: "Polish technical English while preserving meaning and terminology.", Template: "Polish the following text into natural, precise technical English. Preserve code, names, and intent. Return the polished version followed by brief notes on material changes.\n\nText:\n{{text}}", OptimizerPreset: "english_pro", InjectGlossary: true, Slots: []PromptSlot{{Name: "text", Label: "Text", Kind: "text", Required: true}}},
	}}
}

func decodePromptSlot(t *toml.Table, path string) (PromptSlot, error) {
	n, e := requiredString(t, "name", path)
	if e != nil {
		return PromptSlot{}, e
	}
	l, _, e := optionalString(t, "label", path)
	if e != nil {
		return PromptSlot{}, e
	}
	k := "text"
	if x, o, e := optionalString(t, "kind", path); e != nil {
		return PromptSlot{}, e
	} else if o {
		k = x
	}
	d, _, e := optionalString(t, "default", path)
	if e != nil {
		return PromptSlot{}, e
	}
	opts, _, e := optionalStringArray(t, "options", path)
	if e != nil {
		return PromptSlot{}, e
	}
	if len(opts) == 0 {
		opts = nil
	}
	r := false
	if b, o, e := optionalBool(t, "required", path); e != nil {
		return PromptSlot{}, e
	} else if o {
		r = b
	}
	return PromptSlot{Name: n, Label: l, Kind: k, Default: d, Options: opts, Required: r}, nil
}
func (s PromptSlot) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("name", toml.StringVal(s.Name))
	t.Set("label", toml.StringVal(s.Label))
	t.Set("kind", toml.StringVal(s.Kind))
	t.Set("default", toml.StringVal(s.Default))
	t.Set("options", stringsToTOMLArray(s.Options))
	t.Set("required", toml.BoolVal(s.Required))
	return t
}
func decodePrompt(t *toml.Table, path string) (PromptTemplate, error) {
	id, e := requiredString(t, "id", path)
	if e != nil {
		return PromptTemplate{}, e
	}
	tr, e := requiredString(t, "trigger", path)
	if e != nil {
		return PromptTemplate{}, e
	}
	title, e := requiredString(t, "title", path)
	if e != nil {
		return PromptTemplate{}, e
	}
	cat, _, e := optionalString(t, "category", path)
	if e != nil {
		return PromptTemplate{}, e
	}
	desc, _, e := optionalString(t, "description", path)
	if e != nil {
		return PromptTemplate{}, e
	}
	body, e := requiredString(t, "template", path)
	if e != nil {
		return PromptTemplate{}, e
	}
	preset, _, e := optionalString(t, "optimizer_preset", path)
	if e != nil {
		return PromptTemplate{}, e
	}
	inj := false
	if b, o, e := optionalBool(t, "inject_glossary", path); e != nil {
		return PromptTemplate{}, e
	} else if o {
		inj = b
	}
	apps, _, e := optionalStringArray(t, "app_bundle_ids", path)
	if e != nil {
		return PromptTemplate{}, e
	}
	if len(apps) == 0 {
		apps = nil
	}
	sts, _, e := optionalTableArray(t, "slots", path)
	if e != nil {
		return PromptTemplate{}, e
	}
	slots := make([]PromptSlot, 0, len(sts))
	for i, st := range sts {
		x, e := decodePromptSlot(st, fmt.Sprintf("%s.slots[%d]", path, i))
		if e != nil {
			return PromptTemplate{}, e
		}
		slots = append(slots, x)
	}
	return PromptTemplate{ID: id, Trigger: tr, Title: title, Category: cat, Description: desc, Template: body, OptimizerPreset: preset, InjectGlossary: inj, AppBundleIDs: apps, Slots: slots}, nil
}
func (p PromptTemplate) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("id", toml.StringVal(p.ID))
	t.Set("trigger", toml.StringVal(p.Trigger))
	t.Set("title", toml.StringVal(p.Title))
	t.Set("category", toml.StringVal(p.Category))
	t.Set("description", toml.StringVal(p.Description))
	t.Set("template", toml.StringVal(p.Template))
	t.Set("optimizer_preset", toml.StringVal(p.OptimizerPreset))
	t.Set("inject_glossary", toml.BoolVal(p.InjectGlossary))
	t.Set("app_bundle_ids", stringsToTOMLArray(p.AppBundleIDs))
	v := make([]toml.Value, len(p.Slots))
	for i, s := range p.Slots {
		v[i] = toml.TableVal(s.toTOML())
	}
	t.Set("slots", toml.ArrayVal(v))
	return t
}
func DecodePromptsConfig(input string) (PromptsConfig, error) {
	t, e := toml.Parse(input)
	if e != nil {
		return PromptsConfig{}, e
	}
	return PromptsConfigFromTOML(t)
}
func PromptsConfigFromTOML(t *toml.Table) (PromptsConfig, error) {
	c := PromptsConfig{DefaultOptimizerPreset: "coding", InlineOptimizePrefix: "//"}
	if s, o, e := optionalString(t, "default_optimizer_preset", ""); e != nil {
		return c, e
	} else if o {
		c.DefaultOptimizerPreset = s
	}
	if s, o, e := optionalString(t, "inline_optimize_prefix", ""); e != nil {
		return c, e
	} else if o {
		c.InlineOptimizePrefix = s
	}
	ts, _, e := optionalTableArray(t, "prompts", "")
	if e != nil {
		return c, e
	}
	c.Items = make([]PromptTemplate, 0, len(ts))
	for i, x := range ts {
		p, e := decodePrompt(x, fmt.Sprintf("prompts[%d]", i))
		if e != nil {
			return c, e
		}
		c.Items = append(c.Items, p)
	}
	return c, nil
}
func (c PromptsConfig) Encode() string {
	r := toml.NewTable()
	r.Set("default_optimizer_preset", toml.StringVal(c.DefaultOptimizerPreset))
	r.Set("inline_optimize_prefix", toml.StringVal(c.InlineOptimizePrefix))
	v := make([]toml.Value, len(c.Items))
	for i, p := range c.Items {
		v[i] = toml.TableVal(p.toTOML())
	}
	r.Set("prompts", toml.ArrayVal(v))
	return toml.Encode(r)
}
func ValidatePrompts(c PromptsConfig) []ValidationIssue {
	var out []ValidationIssue
	seen := map[string]bool{}
	for i, p := range c.Items {
		path := fmt.Sprintf("prompts[%d]", i)
		if strings.TrimSpace(p.ID) == "" {
			out = append(out, ValidationIssue{File: string(FilePrompts), KeyPath: path + ".id", Severity: SeverityError, Message: "prompt id cannot be empty"})
		} else if seen[p.ID] {
			out = append(out, ValidationIssue{File: string(FilePrompts), KeyPath: path + ".id", Severity: SeverityError, Message: "duplicate prompt id"})
		} else {
			seen[p.ID] = true
		}
		if strings.TrimSpace(p.Trigger) == "" {
			out = append(out, ValidationIssue{File: string(FilePrompts), KeyPath: path + ".trigger", Severity: SeverityError, Message: "prompt trigger cannot be empty"})
		}
		for j, s := range p.Slots {
			if s.Kind != "text" && s.Kind != "select" && s.Kind != "number" {
				out = append(out, ValidationIssue{File: string(FilePrompts), KeyPath: fmt.Sprintf("%s.slots[%d].kind", path, j), Severity: SeverityError, Message: "prompt slot kind must be text, select, or number"})
			}
		}
	}
	return out
}

func mergePromptMarkdown(c PromptsConfig, dir string) (PromptsConfig, error) {
	files, e := filepath.Glob(filepath.Join(dir, "prompts", "*.md"))
	if e != nil {
		return c, e
	}
	sort.Strings(files)
	for _, f := range files {
		b, e := os.ReadFile(f)
		if e != nil {
			return c, e
		}
		p, e := promptFromMarkdown(string(b), f)
		if e != nil {
			return c, e
		}
		replaced := false
		for i := range c.Items {
			if c.Items[i].ID == p.ID {
				c.Items[i] = p
				replaced = true
				break
			}
		}
		if !replaced {
			c.Items = append(c.Items, p)
		}
	}
	return c, nil
}
func promptFromMarkdown(raw, path string) (PromptTemplate, error) {
	p := PromptTemplate{OptimizerPreset: ""}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	start := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		end := -1
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				end = i
				break
			}
		}
		if end < 0 {
			return p, fmt.Errorf("%s: unterminated frontmatter", path)
		}
		for _, line := range lines[1:end] {
			kv := strings.SplitN(line, ":", 2)
			if len(kv) != 2 {
				continue
			}
			k, v := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
			switch k {
			case "id":
				p.ID = v
			case "trigger":
				p.Trigger = v
			case "title":
				p.Title = v
			case "category":
				p.Category = v
			case "description":
				p.Description = v
			case "optimizer_preset":
				p.OptimizerPreset = v
			case "inject_glossary":
				p.InjectGlossary = strings.EqualFold(v, "true")
			}
		}
		start = end + 1
	}
	p.Template = strings.TrimSpace(strings.Join(lines[start:], "\n"))
	if p.ID == "" {
		p.ID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if p.Trigger == "" {
		p.Trigger = "/" + p.ID
	}
	if p.Title == "" {
		p.Title = p.ID
	}
	return p, nil
}
