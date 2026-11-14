package prompt_test

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/prompt"
)

func TestParseTemplateEdgeCases(t *testing.T) {
	t.Run("unclosed brace stays literal", func(t *testing.T) {
		p := prompt.ParseTemplate("Hello {{unclosed world")
		if len(p.Tokens) != 0 {
			t.Fatalf("expected 0 tokens for unclosed brace, got %+v", p.Tokens)
		}
		if !reflect.DeepEqual(p.Parts, []string{"Hello {{unclosed world"}) {
			t.Fatalf("unexpected parts: %+v", p.Parts)
		}
		if slots := p.Slots(config.PromptTemplate{}); len(slots) != 0 {
			t.Fatalf("expected 0 slots, got %+v", slots)
		}
	})

	t.Run("unclosed brace before valid token does not swallow valid token", func(t *testing.T) {
		p := prompt.ParseTemplate("Broken {{oops and {{name}}")
		if len(p.Tokens) != 1 || p.Tokens[0].Name != "name" {
			t.Fatalf("expected 1 token 'name', got %+v", p.Tokens)
		}
		if !reflect.DeepEqual(p.Parts, []string{"Broken {{oops and ", ""}) {
			t.Fatalf("unexpected parts: %+v", p.Parts)
		}
	})

	t.Run("empty and whitespace-only placeholders", func(t *testing.T) {
		empty := prompt.ParseTemplate("A {{}} B")
		if len(empty.Tokens) != 0 {
			t.Fatalf("expected {{}} to remain literal, got tokens=%+v", empty.Tokens)
		}

		ws := prompt.Compile("A {{   }} B")
		if len(ws.Tokens) != 1 || ws.Tokens[0].Name != "" {
			t.Fatalf("expected whitespace token with empty name, got %+v", ws.Tokens)
		}
		if slots := ws.Slots(config.PromptTemplate{}); len(slots) != 0 {
			t.Fatalf("expected empty-named slot to be skipped, got %+v", slots)
		}
		res := prompt.RenderPrompt(config.PromptTemplate{Template: "A {{   }} B"}, nil, prompt.RenderContext{})
		if res.RenderedText != "A  B" || len(res.MissingRequired) != 0 {
			t.Fatalf("unexpected render result for whitespace placeholder: %+v", res)
		}
	})

	t.Run("default values and empty default edges", func(t *testing.T) {
		p := prompt.ParseTemplate(`{{role="staff engineer"}} {{mode=fast}} {{blank=""}} {{trailing=}}`)
		if len(p.Tokens) != 4 {
			t.Fatalf("expected 4 tokens, got %+v", p.Tokens)
		}
		if p.Tokens[0].Kind != "text" || p.Tokens[0].Name != "role" || p.Tokens[0].Default != "staff engineer" {
			t.Fatalf("unexpected token[0]: %+v", p.Tokens[0])
		}
		if p.Tokens[1].Kind != "text" || p.Tokens[1].Name != "mode" || p.Tokens[1].Default != "fast" {
			t.Fatalf("unexpected token[1]: %+v", p.Tokens[1])
		}
		if p.Tokens[2].Kind != "text" || p.Tokens[2].Name != "blank" || p.Tokens[2].Default != "" {
			t.Fatalf("unexpected token[2]: %+v", p.Tokens[2])
		}
		if p.Tokens[3].Name != "trailing=" {
			t.Fatalf("expected trailing= fallback name, got %+v", p.Tokens[3])
		}
	})

	t.Run("select options list and malformed options", func(t *testing.T) {
		p := prompt.ParseTemplate("Lang: {{ lang : Go | Rust | TypeScript }} EmptyOpts: {{bad:}}")
		if len(p.Tokens) != 2 {
			t.Fatalf("expected 2 tokens, got %+v", p.Tokens)
		}
		sel := p.Tokens[0]
		if sel.Name != "lang" || sel.Kind != "select" || sel.Default != "Go" {
			t.Fatalf("unexpected select token: %+v", sel)
		}
		if !reflect.DeepEqual(sel.Options, []string{"Go", "Rust", "TypeScript"}) {
			t.Fatalf("unexpected select options: %+v", sel.Options)
		}
		bad := p.Tokens[1]
		if bad.Kind == "select" || bad.Name != "bad:" {
			t.Fatalf("malformed options should not parse as select: %+v", bad)
		}
	})

	t.Run("duplicate names, explicit overrides, and built-in exclusion", func(t *testing.T) {
		tpl := config.PromptTemplate{
			Template: "{{selection}} {{task}} in {{lang:Go|Rust}} {{task=fallback}} {{app}} {{cursor}}",
			Slots: []config.PromptSlot{
				{Name: "lang", Label: "Language", Kind: "select", Options: []string{"Rust", "Go"}, Default: "Rust"},
				{Name: "lang", Label: "Duplicate Explicit"},
				{Name: "", Label: "Empty Name"},
			},
		}
		p := prompt.ParseTemplate(tpl.Template)
		if !p.Tokens[0].BuiltIn || !p.Tokens[4].BuiltIn || !p.Tokens[5].BuiltIn {
			t.Fatalf("expected selection/app/cursor built-ins: %+v", p.Tokens)
		}
		slots := p.Slots(tpl)
		if len(slots) != 2 {
			t.Fatalf("expected 2 deduplicated slots (explicit lang + shorthand task), got %+v", slots)
		}
		if slots[0].Name != "lang" || slots[0].Default != "Rust" || slots[0].Required {
			t.Fatalf("explicit slot should win and preserve config: %+v", slots[0])
		}
		if slots[1].Name != "task" || slots[1].Kind != "text" || !slots[1].Required {
			t.Fatalf("shorthand task slot mismatch: %+v", slots[1])
		}
	})
}

func TestRenderPromptContextCursorAndValidation(t *testing.T) {
	fixedNow := time.Date(2026, 11, 14, 9, 8, 7, 0, time.UTC)

	t.Run("context vars and aliases", func(t *testing.T) {
		tpl := config.PromptTemplate{
			Template: "sel={{selection}}|clip={{clipboard}}|app={{app}}|url={{url}}|win={{window_title}}|gl={{glossary}}|d={{date}}|t={{time}}|dt={{datetime}}",
		}
		// Alias fallback when primary fields are empty
		aliasCtx := prompt.RenderContext{
			SelectedText:     "sel-alias",
			ClipboardPreview: "clip-alias",
			AppName:          "Code",
			ActiveURL:        "https://example.com",
			WindowTitle:      "main.go",
			Glossary:         "<glossary/>",
			Now:              fixedNow,
		}
		res := prompt.RenderPrompt(tpl, nil, aliasCtx)
		want := "sel=sel-alias|clip=clip-alias|app=Code|url=https://example.com|win=main.go|gl=<glossary/>|d=2026-11-14|t=09:08:07|dt=2026-11-14T09:08:07Z"
		if res.RenderedText != want {
			t.Fatalf("alias render =\n%q\nwant:\n%q", res.RenderedText, want)
		}

		// Direct fields override aliases, and explicit Date/Time/DateTime override Now
		directCtx := aliasCtx
		directCtx.Selection = "sel-direct"
		directCtx.Clipboard = "clip-direct"
		directCtx.App = "Xcode"
		directCtx.URL = "https://clavis.dev"
		directCtx.Date = "custom-d"
		directCtx.Time = "custom-t"
		directCtx.DateTime = "custom-dt"
		res2 := prompt.RenderPrompt(tpl, nil, directCtx)
		want2 := "sel=sel-direct|clip=clip-direct|app=Xcode|url=https://clavis.dev|win=main.go|gl=<glossary/>|d=custom-d|t=custom-t|dt=custom-dt"
		if res2.RenderedText != want2 {
			t.Fatalf("direct render =\n%q\nwant:\n%q", res2.RenderedText, want2)
		}
	})

	t.Run("cursor back offset with multibyte UTF-8", func(t *testing.T) {
		tpl := config.PromptTemplate{Template: "Prefix 中文 {{cursor}}后缀 🚀!"}
		res := prompt.RenderPrompt(tpl, nil, prompt.RenderContext{})
		if res.RenderedText != "Prefix 中文 后缀 🚀!" {
			t.Fatalf("unexpected rendered text: %q", res.RenderedText)
		}
		wantOffset := utf8.RuneCountInString("后缀 🚀!")
		if res.CursorBackOffset != wantOffset {
			t.Fatalf("CursorBackOffset = %d, want %d", res.CursorBackOffset, wantOffset)
		}

		noCursor := prompt.RenderPrompt(config.PromptTemplate{Template: "plain text"}, nil, prompt.RenderContext{})
		if noCursor.CursorBackOffset != 0 {
			t.Fatalf("expected 0 cursor offset when {{cursor}} absent, got %d", noCursor.CursorBackOffset)
		}

		withMarker := prompt.RenderPrompt(config.PromptTemplate{Template: "A{{cursor}}BC"}, nil, prompt.RenderContext{Cursor: "|"})
		if withMarker.RenderedText != "A|BC" || withMarker.CursorBackOffset != 3 {
			t.Fatalf("unexpected cursor marker render: %+v", withMarker)
		}
	})

	t.Run("missing required reporting and defaults", func(t *testing.T) {
		tpl := config.PromptTemplate{
			Template: `{{req1}} {{req1}} {{opt}} {{def="ok"}} {{shorthand_def="yes"}}`,
			Slots: []config.PromptSlot{
				{Name: "req1", Kind: "text", Required: true},
				{Name: "opt", Kind: "text", Required: false},
				{Name: "def", Kind: "text", Default: "ok", Required: true},
				{Name: "shorthand_def", Kind: "text", Required: true},
			},
		}
		res := prompt.RenderPrompt(tpl, nil, prompt.RenderContext{})
		if !reflect.DeepEqual(res.MissingRequired, []string{"req1"}) {
			t.Fatalf("MissingRequired = %+v, want [req1]", res.MissingRequired)
		}
		if res.RenderedText != "   ok yes" {
			t.Fatalf("RenderedText = %q, want %q", res.RenderedText, "   ok yes")
		}

		resFilled := prompt.RenderPrompt(tpl, map[string]string{
			"req1": "done",
			"def":  "override",
		}, prompt.RenderContext{})
		if len(resFilled.MissingRequired) != 0 {
			t.Fatalf("expected no missing required, got %+v", resFilled.MissingRequired)
		}
		if resFilled.RenderedText != "done done  override yes" {
			t.Fatalf("RenderedText = %q, want %q", resFilled.RenderedText, "done done  override yes")
		}
	})
}

func TestSlotComposerSessionNavigationCyclingAndCommitGating(t *testing.T) {
	tpl := config.PromptTemplate{
		ID:       "review",
		Template: "Review {{target}} in {{lang:Go|Rust|Zig}} with {{tone=concise}}.",
		Slots: []config.PromptSlot{
			{Name: "target", Label: "Target", Kind: "text", Required: true},
			{Name: "lang", Label: "Language", Kind: "select", Options: []string{"Go", "Rust", "Zig"}, Default: "Go"},
			{Name: "tone", Label: "Tone", Kind: "text", Default: "concise", Required: false},
		},
	}

	sess := prompt.NewSlotComposerSession(tpl, prompt.RenderContext{})
	if sess.ActiveSlotIndex != 0 {
		t.Fatalf("initial ActiveSlotIndex = %d, want 0", sess.ActiveSlotIndex)
	}
	if sess.CanCommit() {
		t.Fatal("expected CanCommit() false while required 'target' is empty")
	}
	if sess.Values["lang"] != "Go" || sess.Values["tone"] != "concise" {
		t.Fatalf("expected defaults populated, got %+v", sess.Values)
	}

	// Left/Right on a text slot is a safe no-op.
	sess.HandleKey("Left")
	if sess.Values["target"] != "" {
		t.Fatalf("Left on text slot should not mutate value, got %q", sess.Values["target"])
	}

	// SetValue updates active slot and live preview.
	sess.SetValue("auth.go")
	if !sess.CanCommit() {
		t.Fatal("expected CanCommit() true once required 'target' is set")
	}
	if got := sess.Preview().RenderedText; got != "Review auth.go in Go with concise." {
		t.Fatalf("live preview = %q", got)
	}

	// Tab / Shift+Tab navigation wraps cleanly.
	sess.HandleKey("Tab")
	if sess.ActiveSlotIndex != 1 {
		t.Fatalf("after Tab index = %d, want 1", sess.ActiveSlotIndex)
	}
	// Cycle select options forward and backward with wrap-around.
	sess.HandleKey("Right")
	if sess.Values["lang"] != "Rust" {
		t.Fatalf("after Right lang = %q, want Rust", sess.Values["lang"])
	}
	sess.HandleKey("right")
	if sess.Values["lang"] != "Zig" {
		t.Fatalf("after second right lang = %q, want Zig", sess.Values["lang"])
	}
	sess.HandleKey("Right")
	if sess.Values["lang"] != "Go" {
		t.Fatalf("after wrap Right lang = %q, want Go", sess.Values["lang"])
	}
	sess.HandleKey("Left")
	if sess.Values["lang"] != "Zig" {
		t.Fatalf("after wrap Left lang = %q, want Zig", sess.Values["lang"])
	}

	sess.HandleKey("tab")
	if sess.ActiveSlotIndex != 2 {
		t.Fatalf("after tab index = %d, want 2", sess.ActiveSlotIndex)
	}
	sess.HandleKey("Tab")
	if sess.ActiveSlotIndex != 0 {
		t.Fatalf("after wrap Tab index = %d, want 0", sess.ActiveSlotIndex)
	}
	sess.HandleKey("Shift+Tab")
	if sess.ActiveSlotIndex != 2 {
		t.Fatalf("after wrap Shift+Tab index = %d, want 2", sess.ActiveSlotIndex)
	}
	sess.HandleKey("shift+tab")
	if sess.ActiveSlotIndex != 1 {
		t.Fatalf("after shift+tab index = %d, want 1", sess.ActiveSlotIndex)
	}

	// Zero-slot session safety.
	emptySess := prompt.NewSlotComposerSession(config.PromptTemplate{Template: "Hello {{app}}"}, prompt.RenderContext{App: "Notes"})
	if !emptySess.CanCommit() {
		t.Fatal("zero-slot template should be committable")
	}
	emptySess.MoveNext()
	emptySess.MovePrevious()
	emptySess.SetValue("ignored")
	emptySess.HandleKey("Tab")
	emptySess.HandleKey("Left")
	if emptySess.Preview().RenderedText != "Hello Notes" {
		t.Fatalf("unexpected zero-slot preview: %q", emptySess.Preview().RenderedText)
	}
}

func TestOptimizerPresetsAndGlossaryRequestBuilding(t *testing.T) {
	all := prompt.BuiltInPresets()
	wantIDs := []string{
		prompt.PresetCoding,
		prompt.PresetConcise,
		prompt.PresetDeepResearch,
		prompt.PresetEnglishPro,
		prompt.PresetPolite,
	}
	if len(all) != len(wantIDs) {
		t.Fatalf("BuiltInPresets() returned %d presets, want %d", len(all), len(wantIDs))
	}
	for i, p := range all {
		if p.ID != wantIDs[i] {
			t.Fatalf("preset[%d].ID = %q, want %q", i, p.ID, wantIDs[i])
		}
		if p.Name == "" || p.NameZH == "" || p.Description == "" || p.SystemPrompt == "" {
			t.Fatalf("preset %q has empty metadata: %+v", p.ID, p)
		}
	}

	if got := prompt.GetPreset("  ENGLISH_PRO "); got.ID != prompt.PresetEnglishPro {
		t.Fatalf("GetPreset normalization = %q, want english_pro", got.ID)
	}
	if got := prompt.GetPreset("nonexistent"); got.ID != prompt.PresetCoding {
		t.Fatalf("unknown preset fallback = %q, want coding", got.ID)
	}

	glossary := []config.GlossaryTerm{
		{Term: "Clavis", Aliases: []string{"clavis"}, Category: "product", Hint: "Capitalize C"},
	}

	for _, id := range wantIDs {
		t.Run("preset_"+id, func(t *testing.T) {
			p := prompt.GetPreset(id)
			req := prompt.BuildOptimizationRequest(
				"  fix clavis startup race  ",
				id,
				"  macOS capsule  ",
				glossary,
				[]string{"", " add validation criteria ", "   "},
			)
			if req.Mode != "optimize" {
				t.Fatalf("Mode = %q, want optimize", req.Mode)
			}
			if req.SystemPromptOverride != p.SystemPrompt {
				t.Fatalf("SystemPromptOverride mismatch for %s", id)
			}
			if !strings.Contains(req.Prompt, "fix clavis startup race") ||
				!strings.Contains(req.Prompt, "Context:\nmacOS capsule") ||
				!strings.Contains(req.Prompt, "Refinement:\nadd validation criteria") {
				t.Fatalf("unexpected optimizer prompt body:\n%s", req.Prompt)
			}
			if len(req.GlossaryTerms) != 1 || req.GlossaryTerms[0].Term != "Clavis" {
				t.Fatalf("expected glossary terms preserved, got %+v", req.GlossaryTerms)
			}

			// Verify OpenAICompatibleClient honors SystemPromptOverride and injects <glossary>.
			client := ai.NewOpenAICompatibleClient(config.DefaultAIConfig(), engine.GlossaryEngine{}, nil, nil)
			prof := config.DefaultAIProfiles()[0]
			prof.SystemPrompt = "SHOULD_BE_OVERRIDDEN"
			req.Profile = &prof

			httpReq, err := client.BuildRequest(context.Background(), req, "sk-test")
			if err != nil {
				t.Fatalf("BuildRequest(%s): %v", id, err)
			}
			rawBody, err := io.ReadAll(httpReq.Body)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			var payload struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(rawBody, &payload); err != nil {
				t.Fatalf("Unmarshal payload: %v", err)
			}
			if len(payload.Messages) < 2 || payload.Messages[0].Role != "system" {
				t.Fatalf("unexpected messages: %+v", payload.Messages)
			}
			sys := payload.Messages[0].Content
			if strings.Contains(sys, "SHOULD_BE_OVERRIDDEN") {
				t.Fatalf("SystemPromptOverride did not isolate optimizer from profile prompt: %s", sys)
			}
			if !strings.Contains(sys, p.SystemPrompt) || !strings.Contains(sys, "<glossary>") || !strings.Contains(sys, "Clavis") {
				t.Fatalf("expected preset system prompt + glossary block, got:\n%s", sys)
			}
		})
	}

	optStr := prompt.OptimizationPrompt("  my draft ", prompt.PresetPolite)
	if !strings.Contains(optStr, prompt.GetPreset(prompt.PresetPolite).SystemPrompt) || !strings.HasSuffix(optStr, "Draft:\nmy draft") {
		t.Fatalf("unexpected OptimizationPrompt: %q", optStr)
	}
}
