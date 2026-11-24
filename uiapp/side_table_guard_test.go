package uiapp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestSideTableGuardNoGlobalControllerMaps enforces via Go AST inspection that
// non-test uiapp/*.go files contain ZERO package-global map[*AppController]...
// side tables, ZERO package-global sync.Map side tables, and ZERO process-global
// agentLatestResult state. Per-controller state must live inside
// AppController.subStates (controller_substates.go) so controllers never leak
// memory or interfere with one another.
func TestSideTableGuardNoGlobalControllerMaps(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob uiapp/*.go: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected non-empty uiapp/*.go file set")
	}

	// Temporary single-file allowlist for settings_clipboard.go, which is owned
	// by the parallel Wave 2 i18n worktree during CLV-P2-1. Its replacement field
	// (controllerSubStates.clipboardTTLSync + withClipboardTTLSync) is already
	// staged in controller_substates.go; remove this allowlist entry immediately
	// after migrating settings_clipboard.go post-merge.
	temporaryAllowlist := map[string]bool{
		"settings_clipboard.go": true,
	}

	fset := token.NewFileSet()
	var violations []string

	for _, path := range files {
		base := filepath.Base(path)
		if strings.HasSuffix(base, "_test.go") {
			continue
		}
		if temporaryAllowlist[base] {
			continue
		}

		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range vs.Names {
					if name.Name == "agentLatestResult" {
						pos := fset.Position(name.Pos())
						violations = append(violations, pos.String()+": forbidden process-global var agentLatestResult")
					}
				}
				ast.Inspect(vs, func(n ast.Node) bool {
					switch x := n.(type) {
					case *ast.MapType:
						if isAppControllerPointer(x.Key) {
							pos := fset.Position(x.Pos())
							violations = append(violations, pos.String()+": forbidden package-global map[*AppController] side table")
						}
					case *ast.SelectorExpr:
						if id, ok := x.X.(*ast.Ident); ok && id.Name == "sync" && x.Sel.Name == "Map" {
							pos := fset.Position(x.Pos())
							violations = append(violations, pos.String()+": forbidden package-global sync.Map side table")
						}
					}
					return true
				})
			}
		}
	}

	if len(violations) > 0 {
		t.Fatalf("found %d forbidden global side table(s) in uiapp:\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

func isAppControllerPointer(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	return ok && id.Name == "AppController"
}

// TestSideTableGuardZeroValueIsolation verifies that zero-value &AppController{}
// instances lazily initialize all owned sub-states without panicking and remain
// strictly isolated from one another.
func TestSideTableGuardZeroValueIsolation(t *testing.T) {
	a := &AppController{}
	b := &AppController{}
	defer a.Close()
	defer b.Close()

	a.SetHubSurface(HubSurfaceStats)
	b.SetHubSurface(HubSurfaceManage)
	if a.ActiveHubSurface() != HubSurfaceStats || b.ActiveHubSurface() != HubSurfaceManage {
		t.Fatalf("hub surfaces leaked: a=%q b=%q", a.ActiveHubSurface(), b.ActiveHubSurface())
	}

	a.setAssetsSubTab(assetsSubVault)
	if b.assetsSubTab() != assetsSubMedia {
		t.Fatalf("assets sub-tab leaked into b: got %v", b.assetsSubTab())
	}

	a.setAIProfilePreviewTarget("prof-a")
	if id, _ := b.aiProfilePreviewTarget(); id != "" {
		t.Fatalf("aiProfilePreviewTarget leaked into b: %q", id)
	}

	capsuleUI.setPreviewVisible(a, true)
	capsuleUI.setLaneFilter(a, "approval")
	if capsuleUI.previewFor(b, false) || capsuleUI.laneFilter(b) != "" {
		t.Fatalf("capsuleUI leaked into b")
	}

	a.addComposerChip(composerChip{ID: "chip-a", Label: "A", Payload: "payload-a"})
	if len(b.composerChips()) != 0 {
		t.Fatalf("composer chips leaked into b: %+v", b.composerChips())
	}

	a.SetMediaSearchQuery("invoice")
	if b.MediaSearchQuery() != "" {
		t.Fatalf("media search query leaked into b: %q", b.MediaSearchQuery())
	}

	a.SetAgentWorkspaceFilter("ws-a")
	if b.agentWorkspaceFilter() != "" {
		t.Fatalf("agent workspace filter leaked into b: %q", b.agentWorkspaceFilter())
	}

	a.SetClipboardSensitiveTTLSeconds(45)
	if b.ClipboardKeyManagerPolicy().SensitiveTTLSeconds == 45 {
		t.Fatalf("clipboard key manager policy leaked into b")
	}

	a.setTranslateDirection("zh->en")
	if b.ocrTranslateStateForCtrl().translateDir != "" {
		t.Fatalf("ocr/translate state leaked into b")
	}

	seSet(a.settingsEditor(), func(s *settingsEditorState) { s.railQuery = "sync" })
	if q := seGet(b.settingsEditor(), func(s *settingsEditorState) string { return s.railQuery }); q != "" {
		t.Fatalf("settingsEditor leaked into b: %q", q)
	}

	a.inputEngineUI().mu.Lock()
	a.inputEngineUI().blocklistDraft = "foo"
	a.inputEngineUI().mu.Unlock()
	b.inputEngineUI().mu.Lock()
	bDraft := b.inputEngineUI().blocklistDraft
	b.inputEngineUI().mu.Unlock()
	if bDraft != "" {
		t.Fatalf("inputEngineUI leaked into b: %q", bDraft)
	}
}
