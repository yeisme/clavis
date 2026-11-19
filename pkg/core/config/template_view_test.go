package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yeisme/clavis/pkg/core/template"
)

func TestTemplateViewUsesMergedTOMLAndMarkdown(t *testing.T) {
	dir := t.TempDir()
	store := NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	prompts := PromptsConfig{Items: []PromptTemplate{{ID: "demo-summary.main.en", Trigger: "/toml", Title: "TOML", Template: "TOML synthetic body"}, {ID: "toml-only", Trigger: "/only", Title: "Only TOML", Template: "Only TOML synthetic body", AppBundleIDs: []string{"com.example.App"}, Slots: []PromptSlot{{Name: "x", Default: "original", Kind: "text"}}}}}
	tomlRaw := []byte(prompts.Encode())
	if err := os.WriteFile(filepath.Join(dir, "prompts.toml"), tomlRaw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0700); err != nil {
		t.Fatal(err)
	}
	markdown := []byte("---\nid: demo-summary.main.en\ntrigger: /markdown\ntitle: Markdown\n---\nMarkdown synthetic body\n")
	mdPath := filepath.Join(dir, "prompts", "override.md")
	if err := os.WriteFile(mdPath, markdown, 0600); err != nil {
		t.Fatal(err)
	}
	store.Reload()
	if ds := store.LastErrors(); len(ds) > 0 {
		t.Fatalf("config load: %v", ds)
	}
	root := t.TempDir()
	l, err := template.OpenLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	source := t.TempDir()
	if err = os.CopyFS(source, os.DirFS("../template/testdata/synthetic")); err != nil {
		t.Fatal(err)
	}
	// Missing sidecars are explicitly degraded; invalid placeholder T1 fixture
	// sidecars aren't promoted as real contracts.
	if err = os.RemoveAll(filepath.Join(source, "solutions/writing/demo-summary/contracts")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"solutions/code/demo-review/contracts", "solutions/writing/demo-outline/contracts"} {
		if err = os.RemoveAll(filepath.Join(source, p)); err != nil {
			t.Fatal(err)
		}
	}
	for _, ds := range [][]template.Diagnostic{l.Refresh(source), l.Import(source, "demo/writing", "demo-summary", "main", "en", template.SourceFile)} {
		for _, d := range ds {
			if d.Code != template.DegradedNoContract {
				t.Fatalf("install: %v", ds)
			}
		}
	}
	rows, ds := store.TemplateView(l)
	duplicates := 0
	for _, d := range ds {
		if d.Code == template.DuplicateID {
			duplicates++
		} else if d.Code != template.DegradedNoContract {
			t.Fatalf("view: %v", ds)
		}
	}
	if duplicates != 1 || len(rows) != 2 {
		t.Fatalf("wrong merge %d rows / %v", len(rows), ds)
	}
	for _, row := range rows {
		if row.SourceKind != template.SourceLocal || row.Provenance != nil {
			t.Fatal("local lost precedence")
		}
		if row.ID == "demo-summary.main.en" && (row.Title != "Markdown" || row.Body != "Markdown synthetic body" || row.Trigger != "/markdown") {
			t.Fatal("adapter did not use Markdown precedence")
		}
		if row.ID == "toml-only" {
			row.AppBundleIDs[0] = "mutated"
			*row.SlotDefaults["x"].Default = "mutated"
		}
	}
	current := store.Current().Prompts
	for _, p := range current.Items {
		if p.ID == "toml-only" && (p.AppBundleIDs[0] != "com.example.App" || p.Slots[0].Default != "original") {
			t.Fatal("adapter mutated active config")
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "prompts.toml"))
	mdAfter, _ := os.ReadFile(mdPath)
	if string(after) != string(tomlRaw) || string(mdAfter) != string(markdown) {
		t.Fatal("adapter rewrote legacy files")
	}
	rows, ds = store.TemplateView(nil)
	if len(rows) != 2 || len(ds) != 0 {
		t.Fatal("nil library should expose existing locals")
	}
}
