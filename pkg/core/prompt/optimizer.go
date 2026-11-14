package prompt

import (
	"fmt"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/config"
	"strings"
)

const (
	PresetCoding       = "coding"
	PresetConcise      = "concise"
	PresetDeepResearch = "deep_research"
	PresetEnglishPro   = "english_pro"
	PresetPolite       = "polite"
)

type Preset struct{ ID, Name, NameZH, Description, SystemPrompt string }

var presets = map[string]Preset{
	PresetCoding:       {PresetCoding, "Coding Prompt Upgrader", "编程提示词升级", "Make engineering requests precise and testable.", `You are a prompt optimizer for software engineering. Rewrite the user's rough draft into one clear, ready-to-send prompt. Preserve intent; infer safe unstated details only when obvious. Include role, context, constraints, deliverables, acceptance criteria, and validation requirements when useful. Do not solve the task. Return ONLY the upgraded prompt text, with no preamble, markdown fence, or commentary.`},
	PresetConcise:      {PresetConcise, "Concise Prompt Upgrader", "精炼提示词升级", "Remove ambiguity with minimal wording.", `You are a concise prompt optimizer. Rewrite the user's draft into the shortest complete, unambiguous, ready-to-send prompt. Preserve all requirements and ask for missing critical information only when necessary. Do not execute or answer the task. Return ONLY the upgraded prompt text, without markdown fences or commentary.`},
	PresetDeepResearch: {PresetDeepResearch, "Deep Research Prompt Upgrader", "深度研究提示词升级", "Turn questions into evidence-driven research briefs.", `You are a deep-research prompt optimizer. Rewrite the draft as a rigorous research brief with scope, definitions, assumptions, sub-questions, evidence standards, comparison criteria, risks, and a structured deliverable. Do not research or answer it. Return ONLY the upgraded prompt text, without markdown fences or commentary.`},
	PresetEnglishPro:   {PresetEnglishPro, "Technical English Pro", "专业技术英语", "Produce natural precise technical English.", `You are a technical English prompt optimizer. Rewrite the draft into natural, precise professional English while preserving intent, code identifiers, terminology, and constraints. Do not perform the requested task. Return ONLY the upgraded prompt text, without markdown fences or commentary.`},
	PresetPolite:       {PresetPolite, "Polite Professional", "礼貌专业", "Make requests respectful and collaborative.", `You are a professional communication prompt optimizer. Rewrite the draft into a clear, respectful, collaborative request with explicit context, constraints, deliverables, and acceptance criteria where useful. Preserve urgency and intent. Do not perform the task. Return ONLY the upgraded prompt text, without markdown fences or commentary.`},
}

func BuiltInPresets() []Preset {
	out := make([]Preset, 0, len(presets))
	for _, id := range []string{PresetCoding, PresetConcise, PresetDeepResearch, PresetEnglishPro, PresetPolite} {
		out = append(out, presets[id])
	}
	return out
}
func GetPreset(id string) Preset {
	if p, ok := presets[strings.ToLower(strings.TrimSpace(id))]; ok {
		return p
	}
	return presets[PresetCoding]
}

// BuildOptimizationRequest creates a completion request whose system override isolates optimizer behavior from saved profiles.
func BuildOptimizationRequest(draft, preset, contextText string, glossary []config.GlossaryTerm, refinements []string) ai.CompletionRequest {
	p := GetPreset(preset)
	parts := []string{strings.TrimSpace(draft)}
	if strings.TrimSpace(contextText) != "" {
		parts = append(parts, "Context:\n"+strings.TrimSpace(contextText))
	}
	for _, r := range refinements {
		if strings.TrimSpace(r) != "" {
			parts = append(parts, "Refinement:\n"+strings.TrimSpace(r))
		}
	}
	return ai.CompletionRequest{Mode: "optimize", Prompt: strings.Join(parts, "\n\n"), GlossaryTerms: glossary, SystemPromptOverride: p.SystemPrompt}
}

// OptimizationPrompt returns a stable human-readable request for adapters that don't use CompletionRequest.
func OptimizationPrompt(draft, preset string) string {
	p := GetPreset(preset)
	return fmt.Sprintf("%s\n\nDraft:\n%s", p.SystemPrompt, strings.TrimSpace(draft))
}
