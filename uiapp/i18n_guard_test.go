package uiapp

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/yeisme/clavis/pkg/core/i18n"
)

// This file enforces i18n hygiene across uiapp non-test Go sources via AST inspection:
//
//  1. Every literal i18n.T("...") / i18n.Tf("...") key used in uiapp/*.go must
//     exist in BOTH pkg/core/i18n/table_en.go and table_zh.go, and the two
//     tables must stay in 100% key parity.
//  2. All migrated files in i18nMigratedZeroToleranceFiles must contain ZERO
//     pickLang(...) calls and ZERO raw Chinese (Han) string literals.
//  3. Remaining uiapp/*.go files are held to a strict per-file ratchet ceiling
//     (i18nDebtBaseline) so legacy pickLang calls and raw Chinese literals can
//     only shrink over time and never regress; any brand-new file defaults to
//     {0, 0}.

type i18nFileDebt struct {
	pickLangCalls      int
	rawChineseLiterals int
}

// i18nMigratedZeroToleranceFiles lists files that have been fully migrated to
// pkg/core/i18n tables and must remain strictly free of pickLang calls and raw
// Chinese string literals.
var i18nMigratedZeroToleranceFiles = []string{
	"ai_continue.go",
	"inplace_transform.go",
	"leader_dashboard_view.go",
	"media_search_view.go",
	"selection_bar_view.go",
	"settings_clipboard.go",
	"settings_security.go",
	"stats_view.go",
}

// i18nDebtBaseline records the upper bound of legacy pickLang(...) calls and
// raw Chinese (Han) string literals in non-migrated uiapp/*.go files.
// Lower these numbers as additional files migrate to i18n tables; never raise them.
// Regenerate with: CLAVIS_I18N_BASELINE=1 go test -v -run TestI18nHardcodedDebtRatchet ./uiapp
var i18nDebtBaseline = map[string]i18nFileDebt{
	"agent_manage_controls.go": {pickLangCalls: 20, rawChineseLiterals: 23},
	"agent_sessions.go":        {pickLangCalls: 30, rawChineseLiterals: 34},
	"agent_workbench_view.go":  {pickLangCalls: 3, rawChineseLiterals: 41},
	"ai_center_view.go":        {pickLangCalls: 0, rawChineseLiterals: 28},
	"ai_usage_view.go":         {pickLangCalls: 0, rawChineseLiterals: 4},
	"annotate_view.go":         {pickLangCalls: 15, rawChineseLiterals: 15},
	"assets_hub_view.go":       {pickLangCalls: 11, rawChineseLiterals: 11},
	"capsule_preview.go":       {pickLangCalls: 11, rawChineseLiterals: 28},
	"capsule_view.go":          {pickLangCalls: 3, rawChineseLiterals: 8},
	"clipboard_actions.go":     {pickLangCalls: 13, rawChineseLiterals: 13},
	"clipboard_media.go":       {pickLangCalls: 7, rawChineseLiterals: 7},
	"clipboard_stats_view.go":  {pickLangCalls: 0, rawChineseLiterals: 7},
	"controller.go":            {pickLangCalls: 0, rawChineseLiterals: 3},
	"controller_ai.go":         {pickLangCalls: 1, rawChineseLiterals: 1},
	"controller_settings.go":   {pickLangCalls: 0, rawChineseLiterals: 4},
	"input_candidate_view.go":  {pickLangCalls: 0, rawChineseLiterals: 2},
	"input_composer.go":        {pickLangCalls: 29, rawChineseLiterals: 38},
	"kit_states.go":            {pickLangCalls: 0, rawChineseLiterals: 7},
	"lexicon_stats_view.go":    {pickLangCalls: 0, rawChineseLiterals: 6},
	"management_view.go":       {pickLangCalls: 44, rawChineseLiterals: 44},
	"markdown_kit.go":          {pickLangCalls: 1, rawChineseLiterals: 1},
	"media_preview.go":         {pickLangCalls: 1, rawChineseLiterals: 1},
	"media_search.go":          {pickLangCalls: 4, rawChineseLiterals: 4},
	"ocr_translate_view.go":    {pickLangCalls: 7, rawChineseLiterals: 7},
	"pill_view.go":             {pickLangCalls: 0, rawChineseLiterals: 2},
	"search_keys.go":           {pickLangCalls: 0, rawChineseLiterals: 1},
	"settings_ai_profiles.go":  {pickLangCalls: 52, rawChineseLiterals: 54},
	"settings_editors.go":      {pickLangCalls: 2, rawChineseLiterals: 2},
	"settings_input_engine.go": {pickLangCalls: 21, rawChineseLiterals: 21},
	"settings_rows.go":         {pickLangCalls: 0, rawChineseLiterals: 7},
	"settings_sync.go":         {pickLangCalls: 24, rawChineseLiterals: 24},
	"settings_view.go":         {pickLangCalls: 4, rawChineseLiterals: 22},
	"tab_registry.go":          {pickLangCalls: 0, rawChineseLiterals: 3},
	"vault_assets_view.go":     {pickLangCalls: 22, rawChineseLiterals: 22},
}

type i18nScanResult struct {
	keys []string
	debt i18nFileDebt
}

func scanGoSourceI18n(filename string, src any) (i18nScanResult, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return i18nScanResult{}, err
	}
	var res i18nScanResult
	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			// Detect pickLang(...) calls.
			if ident, ok := node.Fun.(*ast.Ident); ok && ident.Name == "pickLang" {
				res.debt.pickLangCalls++
			}
			// Detect i18n.T("...") and i18n.Tf("...") literal keys.
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "T" || sel.Sel.Name == "Tf") {
				if pkgIdent, ok := sel.X.(*ast.Ident); ok && pkgIdent.Name == "i18n" && len(node.Args) > 0 {
					if lit, ok := node.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if key, err := strconv.Unquote(lit.Value); err == nil {
							res.keys = append(res.keys, key)
						}
					}
				}
			}
		case *ast.BasicLit:
			if node.Kind == token.STRING {
				if val, err := strconv.Unquote(node.Value); err == nil && containsHanRune(val) {
					res.debt.rawChineseLiterals++
				}
			}
		}
		return true
	})
	return res, nil
}

func containsHanRune(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func listNonTestUIAppGoFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read uiapp dir: %v", err)
	}
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, name)
	}
	sort.Strings(files)
	return files
}

func TestI18nCallSiteKeysExistInBothTables(t *testing.T) {
	if missing := i18n.MissingKeys(); len(missing) > 0 {
		t.Fatalf("EN/ZH i18n tables drifted apart:\n  %s", strings.Join(missing, "\n  "))
	}

	files := listNonTestUIAppGoFiles(t)
	totalCalls := 0
	var missing []string
	for _, name := range files {
		res, err := scanGoSourceI18n(name, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, key := range res.keys {
			totalCalls++
			if !i18n.HasKey(key) {
				missing = append(missing, fmt.Sprintf("%s: missing key %q", name, key))
			}
		}
	}
	if totalCalls == 0 {
		t.Fatal("expected i18n.T / i18n.Tf call sites in uiapp/*.go, found 0")
	}
	if len(missing) > 0 {
		t.Fatalf("i18n keys referenced in uiapp/*.go are missing from table_en.go or table_zh.go:\n  %s", strings.Join(missing, "\n  "))
	}
}

func TestI18nMigratedFilesStrictZeroDebt(t *testing.T) {
	for _, name := range i18nMigratedZeroToleranceFiles {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("migrated file %s missing: %v", name, err)
		}
		if debt, ok := i18nDebtBaseline[name]; ok && (debt.pickLangCalls > 0 || debt.rawChineseLiterals > 0) {
			t.Errorf("%s is in i18nMigratedZeroToleranceFiles and must not have non-zero baseline debt %+v", name, debt)
		}
		res, err := scanGoSourceI18n(name, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if res.debt.pickLangCalls != 0 {
			t.Errorf("%s: found %d pickLang calls; migrated files must have 0", name, res.debt.pickLangCalls)
		}
		if res.debt.rawChineseLiterals != 0 {
			t.Errorf("%s: found %d raw Chinese string literals; migrated files must have 0", name, res.debt.rawChineseLiterals)
		}
	}
}

func TestI18nHardcodedDebtRatchet(t *testing.T) {
	files := listNonTestUIAppGoFiles(t)
	actual := make(map[string]i18nFileDebt)
	for _, name := range files {
		res, err := scanGoSourceI18n(name, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if res.debt != (i18nFileDebt{}) {
			actual[name] = res.debt
		}
	}

	if os.Getenv("CLAVIS_I18N_BASELINE") != "" {
		var keys []string
		for k := range actual {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("var i18nDebtBaseline = map[string]i18nFileDebt{\n")
		for _, k := range keys {
			d := actual[k]
			fmt.Fprintf(&b, "\t%q: {pickLangCalls: %d, rawChineseLiterals: %d},\n", k, d.pickLangCalls, d.rawChineseLiterals)
		}
		b.WriteString("}")
		t.Log("\n" + b.String())
		return
	}

	var problems []string
	for _, name := range files {
		got := actual[name]
		want := i18nDebtBaseline[name]
		if got.pickLangCalls > want.pickLangCalls {
			problems = append(problems, fmt.Sprintf(
				"%s: pickLang calls %d > baseline %d — add keys to pkg/core/i18n/table_{en,zh}.go instead",
				name, got.pickLangCalls, want.pickLangCalls))
		}
		if got.rawChineseLiterals > want.rawChineseLiterals {
			problems = append(problems, fmt.Sprintf(
				"%s: raw Chinese string literals %d > baseline %d — add keys to pkg/core/i18n/table_{en,zh}.go instead",
				name, got.rawChineseLiterals, want.rawChineseLiterals))
		}
	}
	if len(problems) > 0 {
		t.Fatalf("i18n hardcoded string debt increased:\n  %s", strings.Join(problems, "\n  "))
	}
}

func TestI18nGuardDetectsViolations(t *testing.T) {
	src := `package uiapp

import "github.com/yeisme/clavis/pkg/core/i18n"

// 中文注释不应被算作字符串字面量
func sample(zh bool) string {
	_ = i18n.T("common.settings")
	_ = i18n.Tf("ai.header", "ask")
	_ = pickLang(zh, "你好", "Hello")
	return "纯中文提示"
}
`
	res, err := scanGoSourceI18n("synthetic.go", src)
	if err != nil {
		t.Fatalf("scan synthetic: %v", err)
	}
	if res.debt.pickLangCalls != 1 {
		t.Fatalf("pickLangCalls = %d, want 1", res.debt.pickLangCalls)
	}
	if res.debt.rawChineseLiterals != 2 {
		t.Fatalf("rawChineseLiterals = %d, want 2", res.debt.rawChineseLiterals)
	}
	if len(res.keys) != 2 || res.keys[0] != "common.settings" || res.keys[1] != "ai.header" {
		t.Fatalf("extracted keys = %v, want [common.settings ai.header]", res.keys)
	}
}

func TestI18nMigratedKeysRenderBothLanguages(t *testing.T) {
	prev := i18n.Current()
	defer i18n.SetLanguage(prev)

	for _, name := range i18nMigratedZeroToleranceFiles {
		res, err := scanGoSourceI18n(name, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if len(res.keys) == 0 {
			t.Fatalf("%s: expected at least one i18n key reference", name)
		}
		for _, key := range res.keys {
			i18n.SetLanguage(i18n.English)
			en := i18n.T(key)
			if en == "" || en == key {
				t.Errorf("%s: key %q did not resolve in English (got %q)", name, key, en)
			}
			i18n.SetLanguage(i18n.Chinese)
			zh := i18n.T(key)
			if zh == "" || zh == key {
				t.Errorf("%s: key %q did not resolve in Chinese (got %q)", name, key, zh)
			}
		}
	}
}
