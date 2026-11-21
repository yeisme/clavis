package uiapp

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// TestUIIconNamesHaveOwnGeometry checks that every icon name the UI asks for
// has its own embedded vector. On Windows and in headless renders a missing
// name silently drew the generic bolt: the footer's preview toggle ("eye"),
// image / video / file rows ("photo", "film", "doc") and workflow items
// ("arrow.triangle.branch") all showed a bolt.
func TestUIIconNamesHaveOwnGeometry(t *testing.T) {
	bolt := SemanticIconSVG("bolt")
	used := map[string]string{}
	add := func(where string, names ...string) {
		for _, n := range names {
			if _, seen := used[n]; !seen {
				used[n] = where
			}
		}
	}
	add("omnibox footer toggles", "eye", "ellipsis")
	// engine.clipboardMediaIcon names image, video and file entries.
	add("clipboard media kinds", "photo", "film", "doc")
	for _, tab := range omniboxTabs {
		add("omnibox tab", omniboxTabIcon(tab))
	}
	for _, kind := range []engine.OmniboxItemKind{
		engine.ItemClipboardEntry, engine.ItemSnippet, engine.ItemGlossaryTerm, engine.ItemAIMode,
		engine.ItemInstalledApp, engine.ItemAppIMERule, engine.ItemWorkflow,
	} {
		add("preview header", previewIcon(engine.OmniboxItem{Kind: kind}))
	}
	for _, action := range []string{"optimize_prompt", "explain", "translate", "polish", "copy"} {
		add("selection bar", selectionBarIcon(action))
	}
	for _, content := range []string{"https://example.com", "me@example.com", "/usr/local/bin", "git status", "func main() {}", "plain words"} {
		add("clipboard content", clipboardContentIcon(content, ""))
	}
	theme := ui.LightTheme()
	for _, msg := range []string{"已复制", "failed: boom", "Notes → English", "hello"} {
		icon, _ := pillMessageIcon(theme, msg)
		add("pill message", icon)
	}
	for name, where := range used {
		if name == "bolt" {
			continue
		}
		if SemanticIconSVG(name) == bolt {
			t.Errorf("%s asks for %q, which has no embedded vector and draws the bolt fallback", where, name)
		}
	}
	for _, name := range []string{"eye", "photo", "film", "doc", "arrow.triangle.branch"} {
		if _, ok := semanticIconBodies[name]; !ok {
			t.Errorf("%q missing from semanticIconBodies", name)
		}
	}
	t.Logf("%d icon names checked", len(used))
}
