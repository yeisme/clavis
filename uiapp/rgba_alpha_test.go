package uiapp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestRGBAAlphaIsUnitInterval guards a mistake that shipped once: ui.RGBA's
// alpha is 0..1, and passing a 0..255 byte (ui.RGBA(0, 0, 0, 65)) clamps to
// fully opaque, turning soft shadows into hard black ones. It flags any
// ui.RGBA whose alpha expression holds a numeric literal above 1 without a
// division (65.0/255 and (40+30*lift)/255 are fine).
func TestRGBAAlphaIsUnitInterval(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 4 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "RGBA" {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "ui" {
				return true
			}
			alpha := call.Args[3]
			divided, over := false, false
			ast.Inspect(alpha, func(m ast.Node) bool {
				switch e := m.(type) {
				case *ast.BinaryExpr:
					if e.Op == token.QUO {
						divided = true
					}
				case *ast.BasicLit:
					if v, err := strconv.ParseFloat(e.Value, 64); err == nil && v > 1 {
						over = true
					}
				}
				return true
			})
			if over && !divided {
				t.Errorf("%s: ui.RGBA alpha %s is a 0..255 value; the API takes 0..1 (write n/255)",
					fset.Position(call.Pos()), src[fset.Position(alpha.Pos()).Offset:fset.Position(alpha.End()).Offset])
			}
			return true
		})
	}
}
